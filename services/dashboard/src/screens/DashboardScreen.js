import React from 'react';
import {
  View,
  Text,
  FlatList,
  TouchableOpacity,
  ActivityIndicator,
  StyleSheet,
  RefreshControl,
} from 'react-native';
import { useQuery } from '@tanstack/react-query';
import { fetchSummary, fetchGhosts } from '../api/client';

export default function DashboardScreen({ onSelectGhost }) {
  const summary = useQuery({ queryKey: ['summary'], queryFn: fetchSummary });
  const ghosts = useQuery({ queryKey: ['ghosts'], queryFn: fetchGhosts });

  const isLoading = summary.isLoading || ghosts.isLoading;
  const isError = summary.isError || ghosts.isError;
  const isRefreshing = summary.isFetching || ghosts.isFetching;

  function refresh() {
    summary.refetch();
    ghosts.refetch();
  }

  if (isLoading) {
    return (
      <View style={styles.center}>
        <ActivityIndicator size="large" color="#E53E3E" />
      </View>
    );
  }

  if (isError) {
    return (
      <View style={styles.center}>
        <Text style={styles.errorText}>Could not connect to the ingestion service.</Text>
        <Text style={styles.errorHint}>Make sure it is running on localhost:8080</Text>
        <TouchableOpacity style={styles.retryBtn} onPress={refresh}>
          <Text style={styles.retryText}>Retry</Text>
        </TouchableOpacity>
      </View>
    );
  }

  return (
    <FlatList
      style={styles.list}
      refreshControl={<RefreshControl refreshing={isRefreshing} onRefresh={refresh} />}
      ListHeaderComponent={
        <View>
          <View style={styles.hero}>
            <Text style={styles.heroLabel}>Potential Monthly Savings</Text>
            <Text style={styles.heroAmount}>
              {summary.data.currency} {summary.data.potential_monthly_savings.toFixed(2)}
            </Text>
            <Text style={styles.heroSub}>
              {summary.data.total_ghosts} zombie resource{summary.data.total_ghosts !== 1 ? 's' : ''} detected
            </Text>
          </View>
          <Text style={styles.sectionTitle}>Ghost Resources</Text>
        </View>
      }
      data={ghosts.data}
      keyExtractor={(item) => item.resource_id}
      renderItem={({ item }) => (
        <TouchableOpacity style={styles.card} onPress={() => onSelectGhost(item)}>
          <View style={styles.cardHeader}>
            <Text style={styles.cardService}>{item.service}</Text>
            <Text style={styles.cardCost}>
              {item.currency} {item.monthly_cost.toFixed(2)}/mo
            </Text>
          </View>
          <Text style={styles.cardResource} numberOfLines={1}>{item.resource_id}</Text>
          <View style={styles.cardFooter}>
            <Text style={styles.cardRegion}>{item.region}</Text>
            <Text style={styles.cardOwner}>owner: {item.owner}</Text>
          </View>
          <Text style={styles.cardReason}>{item.reason}</Text>
        </TouchableOpacity>
      )}
      ItemSeparatorComponent={() => <View style={styles.separator} />}
      contentContainerStyle={styles.listContent}
    />
  );
}

const styles = StyleSheet.create({
  list: { flex: 1, backgroundColor: '#F7FAFC' },
  listContent: { paddingBottom: 32 },
  center: { flex: 1, justifyContent: 'center', alignItems: 'center', padding: 24 },

  hero: {
    backgroundColor: '#E53E3E',
    padding: 32,
    alignItems: 'center',
  },
  heroLabel: { color: '#FEB2B2', fontSize: 13, fontWeight: '600', letterSpacing: 1, textTransform: 'uppercase' },
  heroAmount: { color: '#FFFFFF', fontSize: 48, fontWeight: '800', marginTop: 8 },
  heroSub: { color: '#FEB2B2', fontSize: 14, marginTop: 4 },

  sectionTitle: {
    fontSize: 13,
    fontWeight: '700',
    color: '#718096',
    letterSpacing: 1,
    textTransform: 'uppercase',
    paddingHorizontal: 16,
    paddingTop: 24,
    paddingBottom: 8,
  },

  card: {
    backgroundColor: '#FFFFFF',
    marginHorizontal: 16,
    borderRadius: 8,
    padding: 16,
    shadowColor: '#000',
    shadowOpacity: 0.05,
    shadowRadius: 4,
    shadowOffset: { width: 0, height: 2 },
  },
  cardHeader: { flexDirection: 'row', justifyContent: 'space-between', alignItems: 'center' },
  cardService: { fontSize: 15, fontWeight: '700', color: '#2D3748' },
  cardCost: { fontSize: 15, fontWeight: '700', color: '#E53E3E' },
  cardResource: { fontSize: 12, color: '#718096', marginTop: 4, fontFamily: 'monospace' },
  cardFooter: { flexDirection: 'row', justifyContent: 'space-between', marginTop: 8 },
  cardRegion: { fontSize: 12, color: '#A0AEC0' },
  cardOwner: { fontSize: 12, color: '#A0AEC0' },
  cardReason: { fontSize: 13, color: '#E53E3E', marginTop: 8, fontStyle: 'italic' },

  separator: { height: 8 },

  errorText: { fontSize: 16, fontWeight: '600', color: '#2D3748', textAlign: 'center' },
  errorHint: { fontSize: 13, color: '#718096', marginTop: 8, textAlign: 'center' },
  retryBtn: { marginTop: 16, backgroundColor: '#E53E3E', paddingHorizontal: 24, paddingVertical: 10, borderRadius: 6 },
  retryText: { color: '#FFFFFF', fontWeight: '600' },
});
