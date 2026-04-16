import React, { useState, useRef } from 'react';
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
import { useTheme } from '../theme/ThemeContext';

function FullTrendChart({ snaps, selectedId, onSelect, theme, scrollViewRef }) {
  if (!snaps || snaps.length < 2) return null;

  const H = 160;
  const BAR_W = 6;
  const GAP = 2;
  const values = snaps.map((s) => s.total_monthly_cost);
  const maxVal = Math.max(...values, 0.01);
  const W = snaps.length * (BAR_W + GAP);

  return (
    <ScrollView ref={scrollViewRef} horizontal showsHorizontalScrollIndicator={false} style={{ flexGrow: 0 }}>
      <View style={{ width: W, height: H, flexDirection: 'row', alignItems: 'flex-end', paddingHorizontal: 16 }}>
        {snaps.map((s, i) => {
          const barH = Math.max(4, Math.round((s.total_monthly_cost / maxVal) * H));
          const isSelected = selectedId === s.snapshot_at;
          const isLast = i === snaps.length - 1;

          let bgColor = 'rgba(249,115,22,0.45)';
          if (isSelected) bgColor = theme.accent;
          else if (isLast) bgColor = theme.accent;

          return (
            <TouchableOpacity
              key={i}
              activeOpacity={0.7}
              onPress={() => onSelect(s)}
              style={{ width: BAR_W, height: H, justifyContent: 'flex-end', marginRight: i < snaps.length - 1 ? GAP : 0 }}
            >
              <View style={{ width: BAR_W, height: barH, backgroundColor: bgColor, borderTopLeftRadius: 3, borderTopRightRadius: 3 }} />
            </TouchableOpacity>
          );
        })}
      </View>
    </ScrollView>
  );
}

export default function TrendScreen({ onBack }) {
  const { theme } = useTheme();
  const styles = createStyles(theme);
  const trend = useQuery({ queryKey: ['trend'], queryFn: () => fetchTrend(null) });
  const [selectedSnap, setSelectedSnap] = useState(null);
  const flatListRef = useRef(null);
  const chartScrollRef = useRef(null);

  if (trend.isLoading) {
    return (
      <View style={styles.center}>
        <ActivityIndicator size="large" color={theme.accent} />
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
  const latestSnap = snaps.length > 0 ? snaps[snaps.length - 1] : null;
  const displaySnap = selectedSnap || latestSnap;
  const reversedSnaps = [...snaps].reverse();

  return (
    <View style={styles.container}>
      <View style={styles.header}>
        <TouchableOpacity onPress={onBack} style={styles.back}>
          <Text style={styles.backText}>← Back</Text>
        </TouchableOpacity>
        <View style={styles.headerBody}>
          <Text style={styles.headerEyebrow}>
            {selectedSnap ? `Snapshot: ${new Date(selectedSnap.snapshot_at).toLocaleDateString('en-GB')}` : 'Historical Savings Trend'}
          </Text>
          <Text style={styles.headerAmount}>
            {displaySnap?.currency || '$'} {displaySnap ? displaySnap.total_monthly_cost.toFixed(2) : '0.00'}
          </Text>
          <Text style={styles.headerSub}>
            {selectedSnap
              ? `${selectedSnap.ghost_count} zombie resource${selectedSnap.ghost_count !== 1 ? 's' : ''} detected across your accounts`
              : 'Latest projected monthly cost'}
          </Text>
        </View>
      </View>

      <View style={styles.chartContainer}>
        <Text style={styles.sectionTitle}>Monthly Projection Timeline</Text>
        <View style={styles.chartWrapper}>
          <FullTrendChart
            snaps={snaps}
            selectedId={selectedSnap?.snapshot_at}
            onSelect={(s) => {
              const newSelection = s.snapshot_at === selectedSnap?.snapshot_at ? null : s;
              setSelectedSnap(newSelection);
              if (newSelection && flatListRef.current) {
                setTimeout(() => {
                  const index = reversedSnaps.findIndex(snap => snap.snapshot_at === newSelection.snapshot_at);
                  if (index >= 0) {
                    flatListRef.current.scrollToOffset({ offset: index * 60, animated: true });
                  }
                }, 100);
              }
            }}
            theme={theme}
            scrollViewRef={chartScrollRef}
          />
        </View>
        <View style={styles.chartLegend}>
          <Text style={styles.legendText}>{snaps.length} days of recorded history</Text>
        </View>
      </View>

      <View style={{ flex: 1 }}>
        <Text style={[styles.sectionTitle, { paddingHorizontal: 16 }]}>Scan History</Text>
        <FlatList
          ref={flatListRef}
          data={reversedSnaps}
          keyExtractor={(item, idx) => item.snapshot_at + idx}
          contentContainerStyle={styles.listContent}
          extraData={selectedSnap}
          renderItem={({ item, index }) => {
            const isSelected = selectedSnap?.snapshot_at === item.snapshot_at;
            return (
              <TouchableOpacity
                activeOpacity={0.6}
                onPress={() => {
                  const newSelection = isSelected ? null : item;
                  setSelectedSnap(newSelection);
                  if (newSelection && chartScrollRef.current) {
                    setTimeout(() => {
                      const chartIndex = snaps.findIndex(snap => snap.snapshot_at === newSelection.snapshot_at);
                      if (chartIndex >= 0) {
                        const BAR_W = 6;
                        const GAP = 2;
                        const scrollX = chartIndex * (BAR_W + GAP);
                        chartScrollRef.current.scrollTo({ x: scrollX, animated: true });
                      }
                    }, 100);
                  }
                }}
                style={[styles.row, isSelected && styles.rowSelected]}
              >
                <View>
                  <Text style={styles.rowDate}>
                    {new Date(item.snapshot_at).toLocaleDateString('en-GB', { day: 'numeric', month: 'short', year: 'numeric' })}
                    {' at '}
                    {new Date(item.snapshot_at).toLocaleTimeString('en-GB', { hour: '2-digit', minute: '2-digit' })}
                  </Text>
                  <Text style={styles.rowGhosts}>
                    {item.ghost_count === 0 ? 'No ghosts found' : `${item.ghost_count} ghost${item.ghost_count !== 1 ? 's' : ''} found`}
                  </Text>
                </View>
                <Text style={styles.rowCost}>{item.currency} {item.total_monthly_cost.toFixed(2)}</Text>
              </TouchableOpacity>
            );
          }}
        />
      </View>
    </View>
  );
}

const createStyles = (theme) => StyleSheet.create({
  container: { flex: 1, backgroundColor: theme.bg },
  center: { flex: 1, justifyContent: 'center', alignItems: 'center', backgroundColor: theme.bg },
  loadingText: { marginTop: 14, color: theme.textMuted, fontSize: 14 },
  errorText: { color: theme.textMid, fontSize: 16, marginBottom: 20 },
  backBtnError: { paddingHorizontal: 20, paddingVertical: 10, backgroundColor: theme.navy, borderRadius: 8 },
  backBtnErrorText: { color: theme.white, fontWeight: '600' },

  header: { backgroundColor: theme.surfaceAlt, paddingBottom: 28, borderBottomWidth: StyleSheet.hairlineWidth, borderBottomColor: theme.border },
  back: { paddingHorizontal: 20, paddingTop: 16, paddingBottom: 12 },
  backText: { color: theme.textMuted, fontWeight: '600', fontSize: 14 },
  headerBody: { paddingHorizontal: 20 },
  headerEyebrow: { color: theme.textMuted, fontSize: 11, fontWeight: '600', letterSpacing: 1.5, textTransform: 'uppercase', marginBottom: 4 },
  headerAmount: { color: theme.accent, fontSize: 46, fontWeight: '800', letterSpacing: -1 },
  headerSub: { color: theme.textMid, fontSize: 13, marginTop: 4 },

  chartContainer: {
    backgroundColor: theme.surface,
    paddingTop: 20,
    paddingBottom: 16,
    borderBottomWidth: StyleSheet.hairlineWidth,
    borderBottomColor: theme.border,
  },
  sectionTitle: {
    fontSize: 11, fontWeight: '700', color: theme.textMuted,
    letterSpacing: 1.5, textTransform: 'uppercase', marginBottom: 12, paddingHorizontal: 16,
  },
  chartWrapper: { marginTop: 4 },
  chartLegend: { paddingHorizontal: 16, marginTop: 12 },
  legendText: { fontSize: 12, color: theme.textMuted, fontStyle: 'italic' },

  listContent: { paddingHorizontal: 8, paddingBottom: 40 },
  row: {
    flexDirection: 'row',
    justifyContent: 'space-between',
    alignItems: 'center',
    paddingVertical: 14,
    paddingHorizontal: 8,
    borderBottomWidth: StyleSheet.hairlineWidth,
    borderBottomColor: theme.border,
    borderRadius: 8,
  },
  rowSelected: {
    backgroundColor: theme.accentLight,
    borderColor: theme.accentBorder,
    borderWidth: 1,
    borderBottomWidth: 1,
  },
  rowDate: { fontSize: 14, color: theme.text, fontWeight: '600', marginBottom: 4 },
  rowGhosts: { fontSize: 12, color: theme.textMuted },
  rowCost: { fontSize: 15, fontWeight: '700', color: theme.accent },
});
