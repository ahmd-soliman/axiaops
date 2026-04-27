import { useEffect, useState } from 'react';
import { Routes, Route, Navigate, useNavigate } from 'react-router-dom';
import { getToken, saveToken, clearToken } from './auth/storage';
import { setAuthToken } from './api/client';
import { DEV_MODE, DEV_ORG_NAME } from './config';
import { getKindeClient } from './auth/kinde';
import { queryClient } from './main';
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
import OnboardingOrgName from './pages/onboarding/OrgName';
import OnboardingInvite  from './pages/onboarding/Invite';
import OnboardingAws     from './pages/onboarding/AwsAccount';
import Login      from './pages/Login';
import Callback   from './pages/Callback';
import NotFound   from './pages/NotFound';

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

  function handleLogout() {
    clearToken();
    setAuthToken(null);
    queryClient.clear();
    navigate('/login', { replace: true });
  }

  return (
    <AppProvider orgName={orgName} onLogout={handleLogout}>
      <MeProvider>
        <Routes>
          <Route element={<AuthGuard />}>
            <Route element={<OnboardingGate />}>
              <Route path="/onboarding" element={<OnboardingLayout />}>
                <Route index element={<Navigate to="org-name" replace />} />
                <Route path="org-name"    element={<OnboardingOrgName />} />
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
      <Route path="/login"    element={<Login />} />
      <Route path="/callback" element={<Callback />} />
      <Route path="/*"        element={<AuthenticatedApp />} />
      <Route path="*"         element={<NotFound />} />
    </Routes>
  );
}
