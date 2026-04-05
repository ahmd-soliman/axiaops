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

function AuthenticatedApp() {
  const [selectedGhost, setSelectedGhost] = useState(null);

  return selectedGhost ? (
    <DetailScreen ghost={selectedGhost} onBack={() => setSelectedGhost(null)} />
  ) : (
    <DashboardScreen onSelectGhost={setSelectedGhost} />
  );
}

function Root() {
  const [token, setToken] = useState(null);
  const [loading, setLoading] = useState(true);
  const [signingIn, setSigningIn] = useState(false);

  const { request, response, promptAsync } = useKindeAuth();

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
    if (response?.type === 'success') {
      const { code } = response.params;
      exchangeCode(code);
    } else if (response?.type === 'error' || response?.type === 'dismiss') {
      setSigningIn(false);
    }
  }, [response]);

  async function exchangeCode(code) {
    try {
      const issuer = process.env.EXPO_PUBLIC_KINDE_ISSUER;
      const clientId = process.env.EXPO_PUBLIC_KINDE_CLIENT_ID;
      const redirectUri = AuthSession.makeRedirectUri({ useProxy: false });

      const body = new URLSearchParams({
        grant_type: 'authorization_code',
        client_id: clientId,
        redirect_uri: redirectUri,
        code,
        code_verifier: request.codeVerifier,
      });

      const res = await fetch(`${issuer}/oauth2/token`, {
        method: 'POST',
        headers: { 'Content-Type': 'application/x-www-form-urlencoded' },
        body: body.toString(),
      });

      if (!res.ok) throw new Error('Token exchange failed');

      const data = await res.json();
      const accessToken = data.access_token;

      await saveToken(accessToken);
      setAuthToken(accessToken);
      setToken(accessToken);
    } catch (e) {
      console.error('Auth error:', e);
    } finally {
      setSigningIn(false);
    }
  }

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
    <AuthenticatedApp onLogout={handleLogout} />
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
