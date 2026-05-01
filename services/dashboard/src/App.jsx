import { useEffect, useState } from 'react';
import { Routes, Route, Navigate, useNavigate } from 'react-router-dom';
import { getToken, saveToken, clearToken } from './auth/storage';
import { authLogout, setAuthToken, UNAUTHORIZED_EVENT } from './api/client';
import { DEV_MODE, DEV_ORG_NAME } from './config';
import { getKindeClient } from './auth/kinde';
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
import SettingsOrganization from './pages/settings/Organization';
import OnboardingLayout  from './pages/onboarding/OnboardingLayout';
import OnboardingInvite  from './pages/onboarding/Invite';
import OnboardingAws     from './pages/onboarding/AwsAccount';
import Login      from './pages/Login';
import Callback   from './pages/Callback';
import NotFound   from './pages/NotFound';
import BootstrapScreen     from './screens/BootstrapScreen';
import AcceptInviteScreen  from './screens/AcceptInviteScreen';
import PasswordResetScreen from './screens/PasswordResetScreen';

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
                <Route path="/cloud-accounts"      element={<CloudAccounts />} />
                <Route path="/cloud-accounts/:accountId" element={<CloudAccountSettings />} />
                <Route path="/profile"             element={<Profile />} />
                <Route path="/settings"            element={<Settings />}>
                  <Route path="team"      element={<SettingsTeam />} />
                  <Route path="audit"     element={<SettingsAudit />} />
                  <Route path="organization" element={<SettingsOrganization />} />
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

  useEffect(() => {
    if (DEV_MODE) {
      const payload = btoa(JSON.stringify({ org_name: DEV_ORG_NAME }));
      const devToken = `dev.${payload}.dev`;
      saveToken(devToken);
      setAuthToken(devToken);
      setReady(true);
      return;
    }
    getKindeClient().catch(() => {});
    const stored = getToken();
    if (stored) setAuthToken(stored);
    setReady(true);
  }, []);

  if (!ready) return null;

  return (
    <Routes>
      <Route path="/login"          element={<Login />} />
      <Route path="/callback"       element={<Callback />} />
      <Route path="/bootstrap"      element={<BootstrapScreen />} />
      <Route path="/accept-invite"  element={<AcceptInviteScreen />} />
      <Route path="/password-reset" element={<PasswordResetScreen />} />
      <Route path="/*"              element={<AuthenticatedApp />} />
      <Route path="*"               element={<NotFound />} />
    </Routes>
  );
}
