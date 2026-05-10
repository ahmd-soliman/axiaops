import { useEffect, useState } from 'react';
import { Routes, Route, Navigate, useNavigate, useLocation, useParams } from 'react-router-dom';
import { getToken, saveToken, clearToken } from './auth/storage';
import { authLogout, authBootstrapState, setAuthToken, UNAUTHORIZED_EVENT } from './api/client';
import { DEV_MODE, DEV_ORG_NAME } from './config';
import { AppProvider } from './context/AppContext';
import { MeProvider } from './context/MeContext';

import AppShell   from './components/AppShell';
import AuthGuard  from './components/AuthGuard';
import OnboardingGate from './components/OnboardingGate';
import Dashboard  from './pages/Dashboard';
import Detail     from './pages/Detail';
import Trend      from './pages/Trend';
import CostAnalytics from './pages/CostAnalytics';
import Connect              from './pages/Connect';
import CloudAccounts        from './pages/CloudAccounts';
import CloudAccountSettings from './pages/CloudAccountSettings';
import Profile           from './pages/Profile';
import Settings          from './pages/Settings';
import SettingsTeam      from './pages/settings/Team';
import SettingsAudit     from './pages/settings/Audit';
import SettingsSSO       from './pages/settings/SSO';
import SettingsOrganization from './pages/settings/Organization';
import SettingsLicense   from './pages/settings/License';
import OnboardingLayout  from './pages/onboarding/OnboardingLayout';
import OnboardingInvite  from './pages/onboarding/Invite';
import OnboardingAws     from './pages/onboarding/AwsAccount';
import Login      from './pages/Login';
import NotFound   from './pages/NotFound';
import BootstrapScreen     from './screens/BootstrapScreen';
import AcceptInviteScreen  from './screens/AcceptInviteScreen';
import PasswordResetScreen from './screens/PasswordResetScreen';
import OrgPickerScreen     from './screens/OrgPickerScreen';

// Preserves the :accountId param when redirecting from the legacy
// /cloud-accounts/:id path to /settings/cloud-accounts/:id.
function RedirectAccount() {
  const { accountId } = useParams();
  return <Navigate to={`/settings/cloud-accounts/${accountId}`} replace />;
}

function parseJwt(token) {
  try {
    return JSON.parse(atob(token.split('.')[1].replace(/-/g, '+').replace(/_/g, '/')));
  } catch {
    return {};
  }
}

function AuthenticatedApp() {
  const navigate = useNavigate();
  const claims  = parseJwt(getToken() ?? '');
  const orgName = claims.org_name || claims.org_code || '';

  // Navigate first, revoke the session after. Doing it the other way
  // round invalidates the cookie while the dashboard's react-query
  // hooks are still mounted — they refetch, each 401 fires
  // UNAUTHORIZED_EVENT, each handler call cleared the cache and
  // refetched again, and Chrome eventually throttled the navigate
  // flood. The navigate-first version unmounts AuthenticatedApp before
  // the cookie dies, so no in-flight request ever sees a 401.
  function handleLogout() {
    navigate('/login', { replace: true });
    authLogout().catch(() => { /* tolerant — server returns 204 anyway */ });
    clearToken();
    setAuthToken(null);
  }

  // Authentication lost mid-session (cookie expired or revoked
  // server-side): api/client.js fires UNAUTHORIZED_EVENT and we bounce
  // to /login. Same navigate-first ordering as handleLogout —
  // queryClient.clear() here would cascade with concurrent 401s into a
  // navigate-throttle loop.
  useEffect(() => {
    const handler = () => {
      clearToken();
      setAuthToken(null);
      navigate('/login', { replace: true });
    };
    window.addEventListener(UNAUTHORIZED_EVENT, handler);
    return () => window.removeEventListener(UNAUTHORIZED_EVENT, handler);
  }, [navigate]);

  return (
    <AppProvider orgName={orgName} onLogout={handleLogout}>
      <MeProvider>
        <Routes>
          <Route element={<AuthGuard />}>
            <Route element={<OnboardingGate />}>
              <Route path="/onboarding" element={<OnboardingLayout />}>
                <Route index element={<Navigate to="invite" replace />} />
                <Route path="invite"      element={<OnboardingInvite />} />
                <Route path="aws-account" element={<OnboardingAws />} />
              </Route>
              <Route element={<AppShell />}>
                <Route path="/"                    element={<Dashboard />} />
                <Route path="/detail/:id"          element={<Detail />} />
                <Route path="/trend"               element={<Trend />} />
                <Route path="/cost"                element={<CostAnalytics />} />
                <Route path="/connect"             element={<Connect />} />
                {/* Profile and Cloud Accounts moved under /settings; old
                    paths kept as redirects so bookmarks and share-links
                    don't break. */}
                <Route path="/profile"             element={<Navigate to="/settings/profile" replace />} />
                <Route path="/cloud-accounts"      element={<Navigate to="/settings/cloud-accounts" replace />} />
                <Route path="/cloud-accounts/:accountId" element={<RedirectAccount />} />
                <Route path="/settings"            element={<Settings />}>
                  <Route path="profile"   element={<Profile />} />
                  <Route path="cloud-accounts"             element={<CloudAccounts />} />
                  <Route path="cloud-accounts/:accountId"  element={<CloudAccountSettings />} />
                  <Route path="team"      element={<SettingsTeam />} />
                  <Route path="audit"     element={<SettingsAudit />} />
                  <Route path="sso"       element={<SettingsSSO />} />
                  <Route path="organization" element={<SettingsOrganization />} />
                  <Route path="license"   element={<SettingsLicense />} />
                </Route>
              </Route>
            </Route>
          </Route>
        </Routes>
      </MeProvider>
    </AppProvider>
  );
}

export default function App() {
  const [ready, setReady] = useState(false);
  const navigate = useNavigate();
  const location = useLocation();

  useEffect(() => {
    if (DEV_MODE) {
      // DEV_MODE seeds a fake token only so AuthenticatedApp's parseJwt
      // can pull org_name for the navbar; the API itself bypasses auth
      // and ignores the token. The native session cookie is HttpOnly
      // (not legible from JS), so under live auth the navbar pulls
      // org_name from /v1/me via MeContext instead.
      const payload = btoa(JSON.stringify({ org_name: DEV_ORG_NAME }));
      const devToken = `dev.${payload}.dev`;
      saveToken(devToken);
      setAuthToken(devToken);
    }
    setReady(true);
  }, []);

  // First-run auto-redirect (Tasks.md row 2.7.16). On a fresh self-hosted
  // install no organization exists yet, so /login is a dead-end — the
  // user must reach /bootstrap to consume the install token. Probe the
  // public state endpoint and bounce only from the routes a newcomer
  // typically lands on (/ and /login). Token-bearing public routes
  // (/accept-invite, /password-reset, /select-org) and /bootstrap itself
  // are intentionally left alone. Best-effort: api error → no-op (the
  // helper resolves to false on any failure), so a degraded api can't
  // freeze the dashboard at the door.
  useEffect(() => {
    if (DEV_MODE) return;
    const eligible = location.pathname === '/' || location.pathname === '/login';
    if (!eligible) return;
    let cancelled = false;
    authBootstrapState().then((available) => {
      if (cancelled || !available) return;
      navigate('/bootstrap', { replace: true });
    });
    return () => { cancelled = true; };
  }, [location.pathname, navigate]);

  if (!ready) return null;

  return (
    <Routes>
      <Route path="/login"          element={<Login />} />
      <Route path="/bootstrap"      element={<BootstrapScreen />} />
      <Route path="/select-org"     element={<OrgPickerScreen />} />
      <Route path="/accept-invite"  element={<AcceptInviteScreen />} />
      <Route path="/password-reset" element={<PasswordResetScreen />} />
      <Route path="/*"              element={<AuthenticatedApp />} />
      <Route path="*"               element={<NotFound />} />
    </Routes>
  );
}
