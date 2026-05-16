import { Outlet, Navigate } from 'react-router-dom';
import { useMe } from '../context/MeContext';
import { DEV_MODE } from '../config';
import ErrorPage from './ErrorPage';

// Authenticated when /v1/me returned a user_id. The native session cookie
// is HttpOnly so JS can't read it directly — the server's response is the
// only auth signal available to the client. DEV_MODE bypasses the gate
// entirely so a fresh dev DB can mount the dashboard before /v1/me resolves.
//
// While MeProvider's first /v1/me is in flight we render nothing rather
// than redirecting; bouncing to /login mid-load would flash the login
// form for users who are about to be authenticated.
//
// `error` (set by MeContext when /v1/me failed transiently — 429, 5xx,
// network) distinguishes "we know you're not authed" (401, me=null, no
// error → redirect) from "we couldn't tell" (me=null, error set → render
// a retry fallback). The previous all-failures-redirect behaviour silently
// logged users out on any rate-limit hit because /v1/me is the first call
// every page refresh fires and shares the org's request budget.
//
// The loading guard skips a render on the FIRST /v1/me call only —
// otherwise clicking Retry would flash a blank between the ErrorPage and
// the retry result, since refresh() flips loading=true. Once we've seen
// either a successful me OR an error, keep showing whatever we have until
// the new call resolves.
export default function AuthGuard() {
  const { me, loading, error, refresh } = useMe();
  if (DEV_MODE) return <Outlet />;
  if (loading && !error && !me) return null;
  if (me?.user_id) return <Outlet />;
  if (error) {
    const code = error.status ? String(error.status) : undefined;
    return (
      <ErrorPage
        code={code}
        title="Couldn't reach the API"
        description="This is usually a brief connectivity blip or a rate limit. Try again in a moment."
        actions={[
          {
            label: loading ? 'Retrying…' : 'Retry',
            onClick: loading ? () => {} : refresh,
            primary: true,
          },
        ]}
      />
    );
  }
  return <Navigate to="/login" replace />;
}
