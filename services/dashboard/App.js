import React, { useState, useEffect } from 'react';
import { SafeAreaView, StatusBar, StyleSheet } from 'react-native';
import { QueryClient, QueryClientProvider, useQuery } from '@tanstack/react-query';
import * as AuthSession from 'expo-auth-session';

import DashboardScreen from './src/screens/DashboardScreen';
import DetailScreen from './src/screens/DetailScreen';
import LoginScreen from './src/screens/LoginScreen';
import ConnectScreen from './src/screens/ConnectScreen';
import { useKindeAuth } from './src/auth/kinde';
import { saveToken, getToken, clearToken } from './src/auth/storage';
import { setAuthToken, fetchAccounts } from './src/api/client';

const DEV_MODE = process.env.EXPO_PUBLIC_DEV_MODE === 'true';

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
  const [showConnect, setShowConnect]     = useState(false);
  const claims  = parseJwt(token);
  const orgName = claims.org_name || claims.org_code || '';

  const accounts = useQuery({ queryKey: ['accounts'], queryFn: fetchAccounts });

  // Show connect screen on first load if no accounts connected.
  useEffect(() => {
    if (accounts.data && accounts.data.length === 0) {
      setShowConnect(true);
    }
  }, [accounts.data]);

  if (showConnect) {
    return (
      <ConnectScreen
        onConnected={() => {
          setShowConnect(false);
          accounts.refetch();
          queryClient.invalidateQueries();
        }}
        onSkip={() => setShowConnect(false)}
      />
    );
  }

  if (selectedGhost) {
    return <DetailScreen ghost={selectedGhost} onBack={() => setSelectedGhost(null)} />;
  }

  return (
    <DashboardScreen
      onSelectGhost={setSelectedGhost}
      onLogout={onLogout}
      orgName={orgName}
      accounts={accounts.data ?? []}
      onConnectAccount={() => setShowConnect(true)}
    />
  );
}

function Root() {
  const [token, setToken]       = useState(null);
  const [loading, setLoading]   = useState(true);
  const [signingIn, setSigningIn] = useState(false);

  const { request, response, promptAsync, discovery } = useKindeAuth();

  useEffect(() => {
    if (DEV_MODE) {
      setAuthToken('dev');
      setToken('dev');
      setLoading(false);
      return;
    }
    getToken().then((stored) => {
      if (stored) {
        setAuthToken(stored);
        setToken(stored);
      }
      setLoading(false);
    });
  }, []);

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
        .catch((e) => console.error('Token exchange failed:', e))
        .finally(() => setSigningIn(false));
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
