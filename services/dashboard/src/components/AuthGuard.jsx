import { Outlet, Navigate } from 'react-router-dom';
import { useMe } from '../context/MeContext';
import { DEV_MODE } from '../config';

// Authenticated when /v1/me returned a user_id. The native session cookie
// is HttpOnly so JS can't read it directly — the server's response is the
// only auth signal available to the client. DEV_MODE bypasses the gate
// entirely so a fresh dev DB can mount the dashboard before /v1/me resolves.
//
// While MeProvider's first /v1/me is in flight we render nothing rather
// than redirecting; bouncing to /login mid-load would flash the login
// form for users who are about to be authenticated.
export default function AuthGuard() {
  const { me, loading } = useMe();
  if (DEV_MODE) return <Outlet />;
  if (loading) return null;
  if (me?.user_id) return <Outlet />;
  return <Navigate to="/login" replace />;
}
