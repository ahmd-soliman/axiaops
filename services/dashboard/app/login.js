import React, { useState, useEffect } from 'react';
import { useRouter } from 'expo-router';
import * as AuthSession from 'expo-auth-session';

import LoginScreen from '../src/screens/LoginScreen';
import { useKindeAuth } from '../src/auth/kinde';
import { saveToken } from '../src/auth/storage';
import { setAuthToken } from '../src/api/client';
import { DEV_MODE, KINDE_CLIENT_ID } from '../src/config';

export default function Login() {
  const router = useRouter();
  const [signingIn, setSigningIn] = useState(false);

  // DEV_MODE: skip login entirely — the root layout has already minted the
  // dev token. Redirect straight to the dashboard.
  useEffect(() => {
    if (DEV_MODE) {
      router.replace('/');
    }
  }, []);

  const { request, response, promptAsync, discovery } = DEV_MODE
    ? { request: null, response: null, promptAsync: null, discovery: null }
    : useKindeAuth(); // eslint-disable-line react-hooks/rules-of-hooks

  // Handle Kinde callback response.
  useEffect(() => {
    if (!response || !request || !discovery) return;

    if (response.type === 'success') {
      AuthSession.exchangeCodeAsync(
        {
          clientId: KINDE_CLIENT_ID,
          redirectUri: AuthSession.makeRedirectUri({ useProxy: false }),
          code: response.params.code,
          extraParams: { code_verifier: request.codeVerifier },
        },
        discovery,
      )
        .then(async (tokenResponse) => {
          const accessToken = tokenResponse.accessToken;
          await saveToken(accessToken);
          setAuthToken(accessToken);
          router.replace('/');
        })
        .catch((e) => console.error('Token exchange failed:', e))
        .finally(() => setSigningIn(false));
    } else if (response.type === 'error' || response.type === 'dismiss') {
      setSigningIn(false);
    }
  }, [response, request, discovery]);

  async function handleLogin() {
    setSigningIn(true);
    await promptAsync();
  }

  if (DEV_MODE) {
    // Render nothing while the redirect effect fires.
    return null;
  }

  return <LoginScreen onLogin={handleLogin} loading={signingIn} />;
}
