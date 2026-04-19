import { useEffect, useState } from 'react';
import { Routes, Route } from 'react-router-dom';
import { getToken, saveToken } from './auth/storage';
import { setAuthToken } from './api/client';
import { DEV_MODE, DEV_ORG_NAME } from './config';
import { getKindeClient } from './auth/kinde';

import AuthGuard   from './components/AuthGuard';
import Dashboard   from './pages/Dashboard';
import Detail      from './pages/Detail';
import Trend       from './pages/Trend';
import Connect     from './pages/Connect';
import Settings    from './pages/Settings';
import Login       from './pages/Login';
import Callback    from './pages/Callback';
import NotFound    from './pages/NotFound';

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
    // Warm up the Kinde client so Login/Callback can await a resolved Promise.
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
      <Route element={<AuthGuard />}>
        <Route path="/"                    element={<Dashboard />} />
        <Route path="/detail/:id"          element={<Detail />} />
        <Route path="/trend"               element={<Trend />} />
        <Route path="/connect"             element={<Connect />} />
        <Route path="/settings/:accountId" element={<Settings />} />
      </Route>
      <Route path="*" element={<NotFound />} />
    </Routes>
  );
}
