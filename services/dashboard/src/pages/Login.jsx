import { useEffect, useState } from 'react';
import { useNavigate } from 'react-router-dom';
import { authLogin } from '../api/client';
import { getKindeClient } from '../auth/kinde';
import { getToken } from '../auth/storage';
import { AUTH_PROVIDER, DEV_MODE } from '../config';
import LoginScreen from '../screens/LoginScreen';
import NativeLoginScreen from '../screens/NativeLoginScreen';

// Login is the unauthenticated landing page. Branches on AUTH_PROVIDER:
//   - "native" / "both" / unset → NativeLoginScreen with email+password form
//   - "kinde"                   → legacy LoginScreen (Kinde redirect button)
//
// DEV_MODE skips the page entirely — App.jsx mounts the dashboard
// directly via DevBypass.
export default function Login() {
  const navigate = useNavigate();
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState('');

  useEffect(() => {
    if (DEV_MODE || getToken()) navigate('/', { replace: true });
    // No /v1/me probe under native auth: it races with logout's
    // authLogout — if the request is sent before the cookie is cleared,
    // we get a 200 and navigate to "/" while MeContext immediately sees
    // the dead cookie and bounces back to /login. That ping-pong is the
    // "blinks while logging out" symptom. The "user is already logged in
    // on /login" case is rare and the worst outcome is logging in again.
  }, []); // eslint-disable-line react-hooks/exhaustive-deps

  // Reset busy on bfcache restore (browser back from Kinde redirect).
  // Both client.login() and client.register() redirect away mid-call; bfcache
  // restores this page with React state intact, leaving the spinner stuck.
  useEffect(() => {
    const handler = (e) => { if (e.persisted) setBusy(false); };
    window.addEventListener('pageshow', handler);
    return () => window.removeEventListener('pageshow', handler);
  }, []);

  // ── Native path ──────────────────────────────────────────────────────────
  async function handleNativeLogin({ email, password }) {
    setBusy(true);
    setError('');
    try {
      await authLogin(email, password);
      // Cookie set by the server; no token to stash on the JS side.
      navigate('/', { replace: true });
    } catch (e) {
      // The auth client throws a structured error with .code on 4xx.
      if (e.code === 'multi_org_not_supported') {
        setError(
          'This account belongs to multiple organisations. Multi-org login lands ' +
          'in the next release — please ask your administrator to consolidate or ' +
          'wait for the update.',
        );
      } else if (e.status === 401) {
        setError('Invalid email or password.');
      } else if (e.status === 400) {
        setError(e.message || 'Please check your input and try again.');
      } else {
        setError('Sign in failed — please try again in a moment.');
      }
      setBusy(false);
    }
  }

  // ── Kinde path (legacy, AUTH_PROVIDER=kinde only) ────────────────────────
  async function handleKindeLogin() {
    setBusy(true);
    try {
      const client = await getKindeClient();
      await client.login(); // browser redirects; Promise never resolves
    } catch (e) {
      console.error('Login failed:', e);
      setBusy(false);
    }
  }

  async function handleKindeSignUp() {
    setBusy(true);
    try {
      const client = await getKindeClient();
      await client.register({ authUrlParams: { is_create_org: 'true' } });
    } catch (e) {
      console.error('Sign-up failed:', e);
      setBusy(false);
    }
  }

  if (AUTH_PROVIDER === 'kinde') {
    return <LoginScreen onLogin={handleKindeLogin} onSignUp={handleKindeSignUp} loading={busy} />;
  }
  return <NativeLoginScreen onSubmit={handleNativeLogin} loading={busy} error={error} />;
}
