import React from 'react';
import {
  View,
  Text,
  ScrollView,
  FlatList,
  TouchableOpacity,
  ActivityIndicator,
  StyleSheet,
} from 'react-native';
import { useQuery } from '@tanstack/react-query';
import { fetchTrend } from '../api/client';

const C = {
  bg: '#F8FAFC',
  navy: '#0F172A',
  navyMid: '#1E293B',
  accent: '#F97316',
  text: '#0F172A',
  textMid: '#475569',
  textMuted: '#94A3B8',
  white: '#FFFFFF',
  border: '#E2E8F0',
};

function FullTrendChart({ snaps }) {
  if (!snaps || snaps.length < 2) return null;

  const H = 160;
  const BAR_W = 6;
  const GAP = 2;
  const values = snaps.map((s) => s.total_monthly_cost);
  const maxVal = Math.max(...values, 0.01);

  // We show all items in a horizontally scrolling view
  const W = snaps.length * (BAR_W + GAP);

  return (
    <ScrollView horizontal showsHorizontalScrollIndicator={false} style={{ flexGrow: 0 }}>
      {/* Container aligned to flex-end so bars grow upwards */}
      <View style={{ width: W, height: H, flexDirection: 'row', alignItems: 'flex-end', paddingHorizontal: 16 }}>
        {snaps.map((s, i) => {
          const v = s.total_monthly_cost;
          const barH = Math.max(4, Math.round((v / maxVal) * H));
          const isLast = i === snaps.length - 1;
          return (
            <View
              key={i}
              style={{
                width: BAR_W,
                height: barH,
                backgroundColor: isLast ? C.accent : 'rgba(249,115,22,0.45)',
                marginRight: i < snaps.length - 1 ? GAP : 0,
                borderTopLeftRadius: 3,
                borderTopRightRadius: 3,
              }}
            />
          );
        })}
      </View>
    </ScrollView>
  );
}

export default function TrendScreen({ onBack }) {
  const trend = useQuery({ queryKey: ['trend'], queryFn: () => fetchTrend(null) });

  if (trend.isLoading) {
    return (
      <View style={styles.center}>
        <ActivityIndicator size="large" color={C.accent} />
        <Text style={styles.loadingText}>Loading trend data...</Text>
      </View>
    );
  }

  if (trend.isError || !trend.data) {
    return (
      <View style={styles.center}>
        <Text style={styles.errorText}>Failed to load trend data.</Text>
        <TouchableOpacity style={styles.backBtnError} onPress={onBack}>
          <Text style={styles.backBtnErrorText}>Go Back</Text>
        </TouchableOpacity>
      </View>
    );
  }

  const snaps = trend.data;
  const latestCost = snaps.length > 0 ? snaps[snaps.length - 1].total_monthly_cost : 0;
  
  // Create a reversed array for the list so we see the newest first
  const reversedSnaps = [...snaps].reverse();

  return (
    <View style={styles.container}>
      {/* Dark Header */}
      <View style={styles.header}>
        <TouchableOpacity onPress={onBack} style={styles.back}>
          <Text style={styles.backText}>← Back</Text>
        </TouchableOpacity>
        <View style={styles.headerBody}>
          <Text style={styles.headerEyebrow}>Historical Savings Trend</Text>
          <Text style={styles.headerAmount}>
            {snaps[0]?.currency || '$'} {latestCost.toFixed(2)}
          </Text>
          <Text style={styles.headerSub}>Latest projected monthly cost</Text>
        </View>
      </View>

      {/* Chart Section */}
      <View style={styles.chartContainer}>
        <Text style={styles.sectionTitle}>Monthly Projection Timeline</Text>
        <View style={styles.chartWrapper}>
          <FullTrendChart snaps={snaps} />
        </View>
        <View style={styles.chartLegend}>
          <Text style={styles.legendText}>{snaps.length} days of recorded history</Text>
        </View>
      </View>

      {/* List Section */}
      <View style={{ flex: 1 }}>
        <Text style={[styles.sectionTitle, { paddingHorizontal: 16 }]}>Detailed Records</Text>
        <FlatList
          data={reversedSnaps}
          keyExtractor={(item, idx) => item.snapshot_at + idx}
          contentContainerStyle={styles.listContent}
          renderItem={({ item }) => (
            <View style={styles.row}>
              <View>
                <Text style={styles.rowDate}>
                  {new Date(item.snapshot_at).toLocaleDateString('en-GB', {
                    day: 'numeric',
                    month: 'short',
                    year: 'numeric',
                  })}
                </Text>
                <Text style={styles.rowGhosts}>{item.ghost_count} ghost resources flagged</Text>
              </View>
              <Text style={styles.rowCost}>
                {item.currency} {item.total_monthly_cost.toFixed(2)}
              </Text>
            </View>
          )}
        />
      </View>
    </View>
  );
}

const styles = StyleSheet.create({
  container: { flex: 1, backgroundColor: C.bg },
  center: { flex: 1, justifyContent: 'center', alignItems: 'center', backgroundColor: C.bg },
  loadingText: { marginTop: 14, color: C.textMuted, fontSize: 14 },
  errorText: { color: C.textMid, fontSize: 16, marginBottom: 20 },
  backBtnError: { paddingHorizontal: 20, paddingVertical: 10, backgroundColor: C.navy, borderRadius: 8 },
  backBtnErrorText: { color: C.white, fontWeight: '600' },

  header: { backgroundColor: C.navyMid, paddingBottom: 28 },
  back: { paddingHorizontal: 20, paddingTop: 16, paddingBottom: 12 },
  backText: { color: C.textMuted, fontWeight: '600', fontSize: 14 },
  headerBody: { paddingHorizontal: 20 },
  headerEyebrow: { color: C.textMuted, fontSize: 11, fontWeight: '600', letterSpacing: 1.5, textTransform: 'uppercase', marginBottom: 4 },
  headerAmount: { color: C.accent, fontSize: 46, fontWeight: '800', letterSpacing: -1 },
  headerSub: { color: C.textMid, fontSize: 13, marginTop: 4 },

  chartContainer: {
    backgroundColor: C.white,
    paddingTop: 20,
    paddingBottom: 16,
    borderBottomWidth: StyleSheet.hairlineWidth,
    borderBottomColor: C.border,
  },
  sectionTitle: {
    fontSize: 11, fontWeight: '700', color: C.textMuted,
    letterSpacing: 1.5, textTransform: 'uppercase', marginBottom: 12, paddingHorizontal: 16,
  },
  chartWrapper: { marginTop: 4 },
  chartLegend: { paddingHorizontal: 16, marginTop: 12 },
  legendText: { fontSize: 12, color: C.textMuted, fontStyle: 'italic' },

  listContent: { paddingHorizontal: 16, paddingBottom: 40 },
  row: {
    flexDirection: 'row',
    justifyContent: 'space-between',
    alignItems: 'center',
    paddingVertical: 14,
    borderBottomWidth: StyleSheet.hairlineWidth,
    borderBottomColor: C.border,
  },
  rowDate: { fontSize: 14, color: C.text, fontWeight: '600', marginBottom: 4 },
  rowGhosts: { fontSize: 12, color: C.textMuted },
  rowCost: { fontSize: 15, fontWeight: '700', color: C.accent },
});
