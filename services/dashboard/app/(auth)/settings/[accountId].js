import React from 'react';
import { View, ActivityIndicator, StyleSheet } from 'react-native';
import { useRouter, useLocalSearchParams } from 'expo-router';
import { useQuery } from '@tanstack/react-query';

import AccountSettingsScreen from '../../../src/screens/AccountSettingsScreen';
import { fetchAccounts } from '../../../src/api/client';
import { queryClient } from '../../_layout';
import { useTheme } from '../../../src/theme/ThemeContext';

export default function Settings() {
  const router = useRouter();
  const { theme } = useTheme();
  const { accountId } = useLocalSearchParams();

  // Accounts list is typically cached after the dashboard loads.
  const { data: accounts, isLoading } = useQuery({
    queryKey: ['accounts'],
    queryFn: fetchAccounts,
  });

  const account = accounts?.find((a) => a.id === accountId);

  function goBack() {
    if (router.canGoBack()) {
      router.back();
    } else {
      router.replace('/');
    }
  }

  function handleUpdatedOrDeleted() {
    queryClient.invalidateQueries({ queryKey: ['accounts'] });
    router.replace('/');
  }

  if (isLoading) {
    return (
      <View style={[styles.center, { backgroundColor: theme.bg }]}>
        <ActivityIndicator size="large" color={theme.accent} />
      </View>
    );
  }

  if (!account) {
    router.replace('/+not-found');
    return null;
  }

  return (
    <AccountSettingsScreen
      account={account}
      onBack={goBack}
      onAccountUpdated={handleUpdatedOrDeleted}
      onAccountDeleted={handleUpdatedOrDeleted}
    />
  );
}

const styles = StyleSheet.create({
  center: { flex: 1, justifyContent: 'center', alignItems: 'center' },
});
