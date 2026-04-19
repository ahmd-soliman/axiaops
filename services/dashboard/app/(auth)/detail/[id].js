import React from 'react';
import { View, Text, ActivityIndicator, StyleSheet } from 'react-native';
import { useRouter, useLocalSearchParams } from 'expo-router';
import { useQuery } from '@tanstack/react-query';

import DetailScreen from '../../../src/screens/DetailScreen';
import { fetchResources } from '../../../src/api/client';
import { queryClient } from '../../_layout';
import { useTheme } from '../../../src/theme/ThemeContext';

export default function Detail() {
  const router = useRouter();
  const { theme } = useTheme();

  // Composite key from query params — resource_id alone is not globally unique.
  const { id, account, region, service } = useLocalSearchParams();

  // Fetch the resources list for this account — usually a cache hit when
  // navigating from the dashboard, but runs a live request on cold entry.
  const { data: resources, isLoading } = useQuery({
    queryKey: ['resources', account ?? null],
    queryFn: () => fetchResources(account ?? null),
    enabled: true,
  });

  // Resolve the ghost object from the cached list using the composite key.
  const ghost = resources?.find(
    (r) =>
      r.resource_id === id &&
      r.service === service &&
      r.region === region,
  );

  function goBack() {
    if (router.canGoBack()) {
      router.back();
    } else {
      router.replace('/');
    }
  }

  if (isLoading) {
    return (
      <View style={[styles.center, { backgroundColor: theme.bg }]}>
        <ActivityIndicator size="large" color={theme.accent} />
      </View>
    );
  }

  if (!ghost) {
    // Resource not found — replace with the 404 route.
    router.replace('/+not-found');
    return null;
  }

  return (
    <DetailScreen
      ghost={ghost}
      onBack={goBack}
      onDismissed={() => {
        queryClient.invalidateQueries({ queryKey: ['resources'] });
        queryClient.invalidateQueries({ queryKey: ['ghosts'] });
      }}
    />
  );
}

const styles = StyleSheet.create({
  center: { flex: 1, justifyContent: 'center', alignItems: 'center' },
});
