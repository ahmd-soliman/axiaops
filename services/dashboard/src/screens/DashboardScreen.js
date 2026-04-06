import React from 'react';
import {
  View,
  Text,
  FlatList,
  ScrollView,
  TouchableOpacity,
  ActivityIndicator,
  StyleSheet,
  RefreshControl,
} from 'react-native';
import { useQuery } from '@tanstack/react-query';
import { fetchSummary, fetchGhosts } from '../api/client';
import { serviceConfig } from '../components/serviceConfig';

// Design tokens
const C = {
  bg: '#F8FAFC',
  navy: '#0F172A',
  navyMid: '#1E293B',
  navyLight: '#334155',
  accent: '#F97316',   // orange — savings / money
  accentLight: '#FFF7ED',
  text: '#0F172A',
  textMid: '#475569',
  textMuted: '#94A3B8',
  white: '#FFFFFF',
  border: '#E2E8F0',
};

export default function DashboardScreen({ onSelectGhost, onLogout, orgName }) {
  const summary = useQuery({ queryKey: ['summary'], queryFn: fetchSummary });
  const ghosts  = useQuery({ queryKey: ['ghosts'],  queryFn: fetchGhosts  });

  const isLoading   = summary.isLoading || ghosts.isLoading;
  const isError     = summary.isError   || ghosts.isError;
  const isRefreshing = summary.isFetching || ghosts.isFetching;

  function refresh() {
    summary.refetch();
    ghosts.refetch();
  }

  if (isLoading) {
    return (
      <View style={styles.center}>
        <ActivityIndicator size="large" color={C.accent} />
        <Text style={styles.loadingText}>Analysing resources…</Text>
      </View>
    );
  }

  if (isError) {
    return (
      <View style={styles.center}>
        <Text style={styles.errorIcon}>⚠</Text>
        <Text style={styles.errorTitle}>Service unavailable</Text>
        <Text style={styles.errorHint}>Make sure the ingestion service is running on localhost:8080</Text>
        <TouchableOpacity style={styles.retryBtn} onPress={refresh}>
          <Text style={styles.retryText}>Retry</Text>
        </TouchableOpacity>
      </View>
    );
  }

  const byService = Object.entries(summary.data.by_service ?? {}).sort(
    (a, b) => b[1].savings - a[1].savings
  );

  return (
    <FlatList
      style={styles.list}
      refreshControl={<RefreshControl refreshing={isRefreshing} onRefresh={refresh} tintColor={C.accent} />}
      ListHeaderComponent={
        <View>
          {/* Navbar */}
          <View style={styles.navbar}>
            <Text style={styles.navBrand}>AxiaOps</Text>
            <View style={styles.navPill}>
              <Text style={styles.navPillText}>MVP</Text>
            </View>
            <View style={{ flex: 1 }} />
            {orgName ? (
              <View style={styles.orgPill}>
                <Text style={styles.orgPillText}>{orgName}</Text>
              </View>
            ) : null}
            <TouchableOpacity onPress={onLogout} style={styles.logoutBtn}>
              <Text style={styles.logoutText}>Sign out</Text>
            </TouchableOpacity>
          </View>

          {/* Hero */}
          <View style={styles.hero}>
            <Text style={styles.heroEyebrow}>Potential Monthly Savings</Text>
            <Text style={styles.heroAmount}>
              {summary.data.currency}{' '}
              {summary.data.potential_monthly_savings.toFixed(2)}
            </Text>
            <Text style={styles.heroSub}>
              {summary.data.total_ghosts} zombie resource{summary.data.total_ghosts !== 1 ? 's' : ''} detected across your account
            </Text>

            {/* By-service pills */}
            <ScrollView
              horizontal
              showsHorizontalScrollIndicator={false}
              style={styles.pillsRow}
              contentContainerStyle={{ gap: 8, paddingRight: 4 }}
            >
              {byService.map(([svc, data]) => {
                const cfg = serviceConfig(svc);
                return (
                  <View key={svc} style={styles.pill}>
                    <View style={[styles.pillDot, { backgroundColor: cfg.color }]} />
                    <Text style={styles.pillLabel}>{cfg.label}</Text>
                    <Text style={styles.pillSavings}>
                      {summary.data.currency}{data.savings.toFixed(0)}
                    </Text>
                  </View>
                );
              })}
            </ScrollView>
          </View>

          <Text style={styles.sectionTitle}>Ghost Resources</Text>
        </View>
      }
      data={ghosts.data}
      keyExtractor={(item) => item.resource_id}
      renderItem={({ item }) => {
        const cfg = serviceConfig(item.service);
        const isProd = item.tags?.env === 'production';
        return (
          <TouchableOpacity
            style={[styles.card, { borderLeftColor: cfg.color }]}
            onPress={() => onSelectGhost(item)}
            activeOpacity={0.75}
          >
            {/* Top row */}
            <View style={styles.cardTop}>
              <View style={[styles.badge, { backgroundColor: cfg.bg }]}>
                <Text style={[styles.badgeText, { color: cfg.color }]}>{cfg.label}</Text>
              </View>
              <Text style={styles.cardService} numberOfLines={1}>{item.service}</Text>
              <Text style={styles.cardCost}>{item.currency} {item.monthly_cost.toFixed(2)}</Text>
            </View>

            {/* Resource ID */}
            <Text style={styles.cardResource} numberOfLines={1}>{item.resource_id}</Text>

            {/* Meta row */}
            <View style={styles.cardMeta}>
              <Chip label={item.region} />
              <Chip label={item.tags?.env ?? 'unknown'} variant={isProd ? 'prod' : 'stag'} />
              <Text style={styles.cardOwner}>👤 {item.owner}</Text>
            </View>

            {/* Reason */}
            <Text style={styles.cardReason}>{item.reason}</Text>
          </TouchableOpacity>
        );
      }}
      ItemSeparatorComponent={() => <View style={{ height: 8 }} />}
      contentContainerStyle={{ paddingBottom: 40 }}
    />
  );
}

function Chip({ label, variant }) {
  return (
    <View style={[styles.chip, variant === 'prod' && styles.chipProd, variant === 'stag' && styles.chipStag]}>
      <Text style={[styles.chipText, variant === 'prod' && styles.chipTextProd, variant === 'stag' && styles.chipTextStag]}>
        {label}
      </Text>
    </View>
  );
}

const styles = StyleSheet.create({
  list: { flex: 1, backgroundColor: C.bg },
  center: { flex: 1, justifyContent: 'center', alignItems: 'center', padding: 32, backgroundColor: C.bg },

  loadingText: { marginTop: 14, color: C.textMuted, fontSize: 14 },

  errorIcon: { fontSize: 32, marginBottom: 12 },
  errorTitle: { fontSize: 17, fontWeight: '700', color: C.text, marginBottom: 6 },
  errorHint: { fontSize: 13, color: C.textMuted, textAlign: 'center', lineHeight: 20 },
  retryBtn: { marginTop: 20, backgroundColor: C.accent, paddingHorizontal: 28, paddingVertical: 11, borderRadius: 8 },
  retryText: { color: C.white, fontWeight: '700', fontSize: 14 },

  // Navbar
  navbar: {
    backgroundColor: C.navy,
    paddingHorizontal: 20,
    paddingVertical: 15,
    flexDirection: 'row',
    alignItems: 'center',
    gap: 10,
  },
  navBrand: { color: C.white, fontSize: 18, fontWeight: '800', letterSpacing: 0.3, flex: 1 },
  navPill: { backgroundColor: C.navyLight, paddingHorizontal: 9, paddingVertical: 3, borderRadius: 5 },
  navPillText: { color: C.textMuted, fontSize: 10, fontWeight: '700', letterSpacing: 1 },
  orgPill: { backgroundColor: C.navyLight, paddingHorizontal: 10, paddingVertical: 4, borderRadius: 5, marginRight: 8 },
  orgPillText: { color: C.white, fontSize: 12, fontWeight: '600' },
  logoutBtn: { paddingHorizontal: 10, paddingVertical: 4, borderRadius: 5, borderWidth: 1, borderColor: C.navyLight },
  logoutText: { color: C.textMuted, fontSize: 12, fontWeight: '600' },

  // Hero
  hero: {
    backgroundColor: C.navyMid,
    paddingHorizontal: 20,
    paddingTop: 28,
    paddingBottom: 24,
  },
  heroEyebrow: { color: C.textMuted, fontSize: 11, fontWeight: '600', letterSpacing: 1.5, textTransform: 'uppercase', marginBottom: 6 },
  heroAmount: { color: C.accent, fontSize: 46, fontWeight: '800', letterSpacing: -1 },
  heroSub: { color: C.navyLight, fontSize: 13, marginTop: 4, marginBottom: 20, color: '#64748B' },

  pillsRow: { marginTop: 4 },
  pill: {
    flexDirection: 'row',
    alignItems: 'center',
    gap: 6,
    backgroundColor: C.navyLight,
    borderRadius: 20,
    paddingHorizontal: 12,
    paddingVertical: 6,
  },
  pillDot: { width: 6, height: 6, borderRadius: 3 },
  pillLabel: { color: C.white, fontSize: 12, fontWeight: '700' },
  pillSavings: { color: C.textMuted, fontSize: 12 },

  // Section title
  sectionTitle: {
    fontSize: 11,
    fontWeight: '700',
    color: C.textMuted,
    letterSpacing: 1.5,
    textTransform: 'uppercase',
    paddingHorizontal: 16,
    paddingTop: 20,
    paddingBottom: 10,
  },

  // Cards
  card: {
    backgroundColor: C.white,
    marginHorizontal: 16,
    borderRadius: 10,
    padding: 16,
    borderLeftWidth: 4,
    shadowColor: '#000',
    shadowOpacity: 0.05,
    shadowRadius: 6,
    shadowOffset: { width: 0, height: 2 },
  },
  cardTop: { flexDirection: 'row', alignItems: 'center', gap: 8, marginBottom: 6 },
  badge: { paddingHorizontal: 7, paddingVertical: 3, borderRadius: 5 },
  badgeText: { fontSize: 11, fontWeight: '800' },
  cardService: { flex: 1, fontSize: 13, fontWeight: '700', color: C.text },
  cardCost: { fontSize: 14, fontWeight: '800', color: C.accent },
  cardResource: { fontSize: 11, color: C.textMuted, fontFamily: 'monospace', marginBottom: 10 },

  cardMeta: { flexDirection: 'row', alignItems: 'center', gap: 6, marginBottom: 10 },
  chip: { backgroundColor: '#F1F5F9', paddingHorizontal: 8, paddingVertical: 3, borderRadius: 4 },
  chipProd: { backgroundColor: '#FEF2F2' },
  chipStag: { backgroundColor: '#FEFCE8' },
  chipText: { fontSize: 11, color: C.textMid, fontWeight: '500' },
  chipTextProd: { color: '#B91C1C' },
  chipTextStag: { color: '#A16207' },
  cardOwner: { fontSize: 11, color: C.textMuted, marginLeft: 'auto' },

  cardReason: { fontSize: 12, color: C.textMid, fontStyle: 'italic', lineHeight: 18 },
});
