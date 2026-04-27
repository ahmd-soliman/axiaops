import { useEffect, useState } from 'react';
import { useNavigate } from 'react-router-dom';
import { getKindeClient } from '../auth/kinde';
import { getToken } from '../auth/storage';
import { DEV_MODE } from '../config';
import LoginScreen from '../screens/LoginScreen';

export default function Login() {
  const navigate = useNavigate();
  const [busy, setBusy] = useState(false);

  useEffect(() => {
    if (DEV_MODE || getToken()) navigate('/', { replace: true });
  }, []); // eslint-disable-line react-hooks/exhaustive-deps

  // Reset the busy state when the user comes back via the browser back button.
  // Both client.login() and client.register() redirect the browser away mid-call;
  // bfcache restores this page with React state intact, leaving the button stuck
  // showing the spinner. pageshow with persisted=true indicates bfcache restore.
  useEffect(() => {
    const handler = (e) => { if (e.persisted) setBusy(false); };
    window.addEventListener('pageshow', handler);
    return () => window.removeEventListener('pageshow', handler);
  }, []);

  async function handleLogin() {
    setBusy(true);
    try {
      const client = await getKindeClient();
      await client.login(); // browser redirects; Promise never resolves
    } catch (e) {
      console.error('Login failed:', e);
      setBusy(false);
    }
  }

  // handleSignUp asks Kinde to create a brand-new organisation for this user
  // and assign them as owner. Without is_create_org=true the signup flow joins
  // the application's default Kinde org, so every signup ends up in the same
  // AxiaOps organisation. With it, each signup gets a fresh org_code in their
  // JWT and the auth middleware mints a new AxiaOps organisation row.
  async function handleSignUp() {
    setBusy(true);
    try {
      const client = await getKindeClient();
      await client.register({
        authUrlParams: { is_create_org: 'true' },
      });
    } catch (e) {
      console.error('Sign-up failed:', e);
      setBusy(false);
    }
  }

  return <LoginScreen onLogin={handleLogin} onSignUp={handleSignUp} loading={busy} />;
}
