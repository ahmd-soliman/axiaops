import { useEffect, useState, useMemo, useRef, useCallback } from 'react';
import { Routes, Route, Navigate, useNavigate, useLocation, useParams } from 'react-router-dom';
import { getToken, saveToken, clearToken } from './auth/storage';
import {
  authLogout,
  authBootstrapState,
  setAuthToken,
  UNAUTHORIZED_EVENT,
  SERVICE_UNAVAILABLE_EVENT,
} from './api/client';
import { DEV_MODE, DEV_ORG_NAME } from './config';
import { AppProvider } from './context/AppContext';
import { MeProvider } from './context/MeContext';

import AppShell   from './components/AppShell';
import AppErrorBoundary from './components/AppErrorBoundary';
import AuthGuard  from './components/AuthGuard';
import OnboardingGate from './components/OnboardingGate';
import OrgSummary from './pages/OrgSummary';
import Overview   from './pages/Overview';
import Detail     from './pages/Detail';
import Trend      from './pages/Trend';
import CloudSpend  from './pages/CloudSpend';
import Connect              from './pages/Connect';
import CloudAccounts        from './pages/CloudAccounts';
import CloudAccountSettings from './pages/CloudAccountSettings';
import Profile           from './pages/Profile';
import Settings          from './pages/Settings';
import SettingsMembers   from './pages/settings/Members';
import SettingsIntegrations from './pages/settings/Integrations';
import SettingsAudit     from './pages/settings/Audit';
import SettingsSSO       from './pages/settings/SSO';
import SettingsOrganization from './pages/settings/Organization';
import OnboardingLayout  from './pages/onboarding/OnboardingLayout';
import OnboardingInvite  from './pages/onboarding/Invite';
import OnboardingAws     from './pages/onboarding/AwsAccount';
import Login      from './pages/Login';
import NotFound   from './pages/NotFound';
import ServiceUnavailable from './pages/ServiceUnavailable';
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
  // The token is written once at mount (DEV_MODE) and never changes after —
  // under native auth getToken() is always null. Parse it once rather than
  // re-reading localStorage + base64-decoding on every render.
  const claims  = useMemo(() => parseJwt(getToken() ?? ''), []);
  const orgName = claims.org_name || claims.org_code || '';

  // Navigate first, revoke the session after. Doing it the other way
  // round invalidates the cookie while the dashboard's react-query
  // hooks are still mounted — they refetch, each 401 fires
  // UNAUTHORIZED_EVENT, each handler call cleared the cache and
  // refetched again, and Chrome eventually throttled the navigate
  // flood. The navigate-first version unmounts AuthenticatedApp before
  // the cookie dies, so no in-flight request ever sees a 401.
  const handleLogout = useCallback(() => {
    navigate('/login', { replace: true });
    // Navigate-first protects against the 401-cascade described in the
    // comment above. If logout returns an SSO logout_url (server has an
    // OIDC end_session_endpoint for this session), do a SECOND navigation
    // to the IdP so the IdP session dies too — without it, the next
    // sign-in on this browser inherits the previous user's identity.
    // window.location.assign supersedes the prior react-router navigate
    // and is safe to fire even when we're already at /login.
    authLogout()
      .then((body) => {
        if (body?.logout_url) {
          window.location.assign(body.logout_url);
        }
      })
      .catch(() => { /* tolerant — server returns 204 on the native path anyway */ });
    clearToken();
    setAuthToken(null);
  }, [navigate]);

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
                <Route path="/"                    element={<OrgSummary />} />
                <Route path="/zombies"            element={<Overview />} />
                <Route path="/account"            element={<Navigate to="/zombies" replace />} />
                <Route path="/detail/:id"          element={<Detail />} />
                <Route path="/trend"               element={<Trend />} />
                <Route path="/spend"               element={<CloudSpend />} />
                <Route path="/cost"                element={<Navigate to="/spend" replace />} />
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
                  <Route path="members"   element={<SettingsMembers />} />
                  <Route path="team"      element={<Navigate to="/settings/members" replace />} />
                  <Route path="integrations" element={<SettingsIntegrations />} />
                  <Route path="audit"     element={<SettingsAudit />} />
                  <Route path="sso"       element={<SettingsSSO />} />
                  <Route path="organization" element={<SettingsOrganization />} />
                </Route>
                {/* Catch-all inside AppShell so unknown URLs render NotFound
                    with the nav still visible, instead of an empty pane. */}
                <Route path="*" element={<NotFound />} />
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

  // Any 503 response from the API bounces the user to the dedicated
  // /service-unavailable page. client.js dispatches the event once per
  // 503 response; we de-dupe by checking the current path so a flurry
  // of in-flight requests all returning 503 doesn't fire a navigate
  // loop. The page itself has a "Try again" button that reloads —
  // that's the recovery path once the API is back.
  //
  // The current path is read through a ref so the listener can stay mounted
  // for App's lifetime — depending on location.pathname directly would tear
  // down and re-add the listener on every navigation.
  const pathRef = useRef(location.pathname);
  pathRef.current = location.pathname;
  useEffect(() => {
    const handler = () => {
      if (pathRef.current === '/service-unavailable') return;
      navigate('/service-unavailable', { replace: true });
    };
    window.addEventListener(SERVICE_UNAVAILABLE_EVENT, handler);
    return () => window.removeEventListener(SERVICE_UNAVAILABLE_EVENT, handler);
  }, [navigate]);

  if (!ready) return null;

  // AppErrorBoundary takes resetKey={location.pathname} so a transient
  // render error doesn't latch the 500 fallback for the rest of the
  // session — navigating to a new route clears the latch and gives the
  // new tree a fresh attempt.
  return (
    <AppErrorBoundary resetKey={location.pathname}>
      <Routes>
        <Route path="/login"                element={<Login />} />
        <Route path="/bootstrap"            element={<BootstrapScreen />} />
        <Route path="/select-org"           element={<OrgPickerScreen />} />
        <Route path="/accept-invite"        element={<AcceptInviteScreen />} />
        <Route path="/password-reset"       element={<PasswordResetScreen />} />
        <Route path="/service-unavailable"  element={<ServiceUnavailable />} />
        {/* "/*" splats catch every remaining path. AuthenticatedApp
            owns its own inner NotFound route, so unauthenticated junk
            paths hit AuthGuard → redirect to /login, and authenticated
            junk paths render NotFound inside AppShell. */}
        <Route path="/*"                    element={<AuthenticatedApp />} />
      </Routes>
    </AppErrorBoundary>
  );
}
