import { useEffect, useState } from 'react';
import { useLocation, useNavigate } from 'react-router-dom';
import { authLogin, setPendingOrgPick } from '../api/client';
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
  const location = useLocation();
  // OrgPickerScreen bounces back here with a state.error message when
  // /select-org returned 401 (limiter fired or something raced) — show
  // it on first render so the user knows why they're back at /login
  // rather than seeing a silently-empty form. Clear it from history
  // state immediately so the message doesn't reappear if the user
  // back-buttons to this entry later.
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState(location.state?.error || '');

  useEffect(() => {
    if (DEV_MODE || getToken()) navigate('/', { replace: true });
    // One-shot consume of the bounce-back error: replace the current
    // history entry with the same URL but no state, so a back-navigation
    // here later doesn't redisplay the stale error.
    if (location.state?.error) {
      navigate('.', { replace: true, state: {} });
    }
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
      const result = await authLogin(email, password);
      // Multi-membership branch (B1.5): server returned the picker payload
      // with no cookie. Hand the creds + orgs list to /select-org via a
      // module-level variable in api/client.js (NOT React Router state —
      // that gets persisted to window.history.state and survives a tab
      // refresh, which would mean the password persists too). The
      // module-level let is wiped on hard refresh (the bundle re-inits),
      // which is the actual transient lifetime we want.
      if (result.needs_org_selection) {
        setPendingOrgPick({ email, password, orgs: result.orgs });
        navigate('/select-org', { replace: true });
        return;
      }
      // Single-membership branch: cookie set by the server, dashboard ready.
      navigate('/', { replace: true });
    } catch (e) {
      // The auth client throws a structured error with .code on 4xx.
      if (e.status === 401) {
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
