import { useEffect, useState } from 'react';
import { useNavigate } from 'react-router-dom';
import { getKindeClient } from '../auth/kinde';
import { getToken } from '../auth/storage';
import { DEV_MODE } from '../config';
import LoginScreen from '../screens/LoginScreen';

export default function Login() {
  const navigate = useNavigate();
  const [signingIn, setSigningIn] = useState(false);

  useEffect(() => {
    if (DEV_MODE || getToken()) navigate('/', { replace: true });
  }, []); // eslint-disable-line react-hooks/exhaustive-deps

  async function handleLogin() {
    setSigningIn(true);
    try {
      const client = await getKindeClient();
      await client.login(); // browser redirects; Promise never resolves
    } catch (e) {
      console.error('Login failed:', e);
      setSigningIn(false);
    }
  }

  return <LoginScreen onLogin={handleLogin} loading={signingIn} />;
}
