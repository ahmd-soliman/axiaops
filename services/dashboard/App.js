import React, { useState, useEffect } from 'react';
import { SafeAreaView, StatusBar, StyleSheet } from 'react-native';
import { QueryClient, QueryClientProvider } from '@tanstack/react-query';
import * as AuthSession from 'expo-auth-session';

import DashboardScreen from './src/screens/DashboardScreen';
import DetailScreen from './src/screens/DetailScreen';
import LoginScreen from './src/screens/LoginScreen';
import { useKindeAuth } from './src/auth/kinde';
import { saveToken, getToken, clearToken } from './src/auth/storage';
import { setAuthToken } from './src/api/client';

const queryClient = new QueryClient();

function parseJwt(token) {
  try {
    const base64 = token.split('.')[1].replace(/-/g, '+').replace(/_/g, '/');
    return JSON.parse(atob(base64));
  } catch {
    return {};
  }
}

function AuthenticatedApp({ token, onLogout }) {
  const [selectedGhost, setSelectedGhost] = useState(null);
  const claims = parseJwt(token);
  const orgName = claims.org_name || claims.org_code || '';

  return selectedGhost ? (
    <DetailScreen ghost={selectedGhost} onBack={() => setSelectedGhost(null)} />
  ) : (
    <DashboardScreen onSelectGhost={setSelectedGhost} onLogout={onLogout} orgName={orgName} />
  );
}

function Root() {
  const [token, setToken] = useState(null);
  const [loading, setLoading] = useState(true);
  const [signingIn, setSigningIn] = useState(false);

  const { request, response, promptAsync, discovery } = useKindeAuth();

  // Restore token from storage on startup
  useEffect(() => {
    getToken().then((stored) => {
      if (stored) {
        setAuthToken(stored);
        setToken(stored);
      }
      setLoading(false);
    });
  }, []);

  // Handle OAuth callback
  useEffect(() => {
    if (response?.type === 'success' && request && discovery) {
      AuthSession.exchangeCodeAsync(
        {
          clientId: process.env.EXPO_PUBLIC_KINDE_CLIENT_ID,
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
          setToken(accessToken);
        })
        .catch((e) => {
          console.error('Token exchange failed:', e);
        })
        .finally(() => {
          setSigningIn(false);
        });
    } else if (response?.type === 'error' || response?.type === 'dismiss') {
      setSigningIn(false);
    }
  }, [response, request, discovery]);

  async function handleLogin() {
    setSigningIn(true);
    await promptAsync();
  }

  async function handleLogout() {
    await clearToken();
    setAuthToken(null);
    setToken(null);
  }

  if (loading) return null;

  return token ? (
    <AuthenticatedApp token={token} onLogout={handleLogout} key={token} />
  ) : (
    <LoginScreen onLogin={handleLogin} loading={signingIn} />
  );
}

export default function App() {
  return (
    <QueryClientProvider client={queryClient}>
      <SafeAreaView style={styles.container}>
        <StatusBar barStyle="light-content" backgroundColor="#0F172A" />
        <Root />
      </SafeAreaView>
    </QueryClientProvider>
  );
}

const styles = StyleSheet.create({
  container: { flex: 1, backgroundColor: '#0F172A' },
});
