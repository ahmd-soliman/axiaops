import React, { useEffect } from 'react';
import { SafeAreaView, StatusBar } from 'react-native';
import { Stack } from 'expo-router';
import { QueryClient, QueryClientProvider } from '@tanstack/react-query';

import { ThemeProvider, useTheme } from '../src/theme/ThemeContext';
import { saveToken, getToken } from '../src/auth/storage';
import { setAuthToken } from '../src/api/client';
import { DEV_MODE, DEV_ORG_NAME } from '../src/config';

export const queryClient = new QueryClient();

// ThemedShell wraps the navigator in a theme-aware SafeAreaView + StatusBar.
// It must live inside ThemeProvider so it can call useTheme().
function ThemedShell() {
  const { theme, isDark } = useTheme();

  return (
    <SafeAreaView style={{ flex: 1, backgroundColor: theme.bg }}>
      <StatusBar
        barStyle={isDark ? 'light-content' : 'dark-content'}
        backgroundColor={theme.bg}
      />
      <Stack screenOptions={{ headerShown: false }} />
    </SafeAreaView>
  );
}

export default function RootLayout() {
  useEffect(() => {
    if (!DEV_MODE) return;
    // DEV_MODE: mint a fake JWT and persist it so the auth guard sees a token.
    getToken().then((existing) => {
      if (existing) return; // already set (e.g. previous dev session)
      const payload = btoa(JSON.stringify({ org_name: DEV_ORG_NAME }));
      const devToken = `dev.${payload}.dev`;
      saveToken(devToken);
      setAuthToken(devToken);
    });
  }, []);

  return (
    <QueryClientProvider client={queryClient}>
      <ThemeProvider>
        <ThemedShell />
      </ThemeProvider>
    </QueryClientProvider>
  );
}
