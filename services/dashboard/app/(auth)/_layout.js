import React, { useState, useEffect } from 'react';
import { Redirect, Stack } from 'expo-router';

import { getToken } from '../../src/auth/storage';
import { setAuthToken } from '../../src/api/client';

export default function AuthLayout() {
  const [token, setToken] = useState(undefined); // undefined = not yet checked

  useEffect(() => {
    getToken().then((stored) => {
      if (stored) {
        // Ensure the API client has the token (e.g. on a hard refresh where the
        // root layout DEV_MODE effect may not have run yet).
        setAuthToken(stored);
      }
      setToken(stored ?? null);
    });
  }, []);

  // Not yet resolved — render nothing while checking storage.
  if (token === undefined) return null;

  // No token → send to login.
  if (!token) return <Redirect href="/login" />;

  // Authenticated → render child routes.
  return <Stack screenOptions={{ headerShown: false }} />;
}
