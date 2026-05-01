import { Outlet, Navigate } from 'react-router-dom';
import { getToken } from '../auth/storage';
import { useMe } from '../context/MeContext';

// Authenticated when EITHER:
//   - a Kinde JWT is in localStorage (legacy AUTH_PROVIDER=kinde), OR
//   - /v1/me returned a user_id (native session cookie — JS can't read
//     the HttpOnly cookie directly, so the server's response is the only
//     auth signal available to the client).
//
// While MeProvider's first /v1/me is in flight we render nothing rather
// than redirecting; bouncing to /login mid-load would flash the login
// form for users who are about to be authenticated.
export default function AuthGuard() {
  const { me, loading } = useMe();
  if (getToken()) return <Outlet />;
  if (loading) return null;
  if (me?.user_id) return <Outlet />;
  return <Navigate to="/login" replace />;
}
