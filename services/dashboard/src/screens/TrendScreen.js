import React, { useState, useRef } from 'react';
import {
  View,
  Text,
  ScrollView,
  FlatList,
  TouchableOpacity,
  ActivityIndicator,
  StyleSheet,
  useWindowDimensions,
} from 'react-native';
import { useQuery } from '@tanstack/react-query';
import { fetchTrend } from '../api/client';
import { useTheme } from '../theme/ThemeContext';

const PAGE_SIZE = 90;
const CHART_HEIGHT = 160;
const CHART_GAP = 2;
const CHART_PADDING = 16; // horizontal padding inside scroll content

function FullTrendChart({ snaps, selectedId, onSelect, theme, scrollViewRef, page = 0, barWidth, contentWidth, screenWidth }) {
  if (!snaps || snaps.length < 2) return null;

  const startIndex = Math.max(0, snaps.length - (page + 1) * PAGE_SIZE);
  const endIndex = snaps.length - page * PAGE_SIZE;
  const pageSnaps = snaps.slice(startIndex, endIndex);

  const values = pageSnaps.map((s) => s.total_monthly_cost);
  const maxVal = Math.max(...values, 0.01);

  // Center bars if content is smaller than screen width
  const barsWidth = pageSnaps.length * barWidth + Math.max(0, pageSnaps.length - 1) * CHART_GAP;
  const horizontalPadding = Math.max(CHART_PADDING, (screenWidth - barsWidth) / 2);

  return (
    <ScrollView ref={scrollViewRef} horizontal showsHorizontalScrollIndicator={false} style={{ width: '100%', flexGrow: 0 }}>
      <View style={{ width: contentWidth, height: CHART_HEIGHT, flexDirection: 'row', alignItems: 'flex-end', paddingHorizontal: horizontalPadding }}>
        {pageSnaps.map((s, i) => {
          const barH = Math.max(4, Math.round((s.total_monthly_cost / maxVal) * CHART_HEIGHT));
          const isSelected = selectedId === s.snapshot_at;
          const isLast = i === pageSnaps.length - 1 && page === 0;

          let bgColor = 'rgba(249,115,22,0.45)';
          if (isSelected) bgColor = theme.accent;
          else if (isLast) bgColor = theme.accent;

          return (
            <TouchableOpacity
              key={i}
              activeOpacity={0.7}
              onPress={() => onSelect(s)}
              style={{ width: barWidth, height: CHART_HEIGHT, justifyContent: 'flex-end', marginRight: i < pageSnaps.length - 1 ? CHART_GAP : 0 }}
            >
              <View style={{ width: barWidth, height: barH, backgroundColor: bgColor, borderTopLeftRadius: 3, borderTopRightRadius: 3 }} />
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
  const { width: screenWidth } = useWindowDimensions();
  const trend = useQuery({ queryKey: ['trend'], queryFn: () => fetchTrend(null) });
  const [selectedSnap, setSelectedSnap] = useState(null);
  const flatListRef = useRef(null);
  const chartScrollRef = useRef(null);
  const [chartPage, setChartPage] = useState(0); // 0 = most recent 90 days

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

  const totalPages = Math.max(1, Math.ceil(snaps.length / PAGE_SIZE));
  const canGoBack = chartPage < totalPages - 1;
  const canGoForward = chartPage > 0;

  // Current page's bar layout — bars expand to fill the screen.
  const currentPageCount = Math.min(PAGE_SIZE, snaps.length - chartPage * PAGE_SIZE);
  const availableChartWidth = screenWidth - CHART_PADDING * 2;
  const computedBarW = Math.floor(availableChartWidth / Math.max(1, currentPageCount)) - CHART_GAP;
  const BAR_W = Math.min(24, Math.max(4, computedBarW));
  // Accurate width: bars + gaps between them (not after last bar) + padding
  const barsWidth = currentPageCount * BAR_W + Math.max(0, currentPageCount - 1) * CHART_GAP;
  const totalPadding = CHART_PADDING * 2;
  const naturalWidth = barsWidth + totalPadding;
  const chartContentWidth = Math.max(screenWidth, naturalWidth);

  // Scroll the chart so the selected snap's bar is centered on screen.
  // If the snap is on another page, switch pages first, then scroll once the
  // ScrollView has re-rendered with the new content.
  const scrollChartToSnap = (snap) => {
    if (!snap) return;
    const globalIndex = snaps.findIndex((s) => s.snapshot_at === snap.snapshot_at);
    if (globalIndex < 0) return;

    const targetPage = Math.floor((snaps.length - 1 - globalIndex) / PAGE_SIZE);
    const pageStart = Math.max(0, snaps.length - (targetPage + 1) * PAGE_SIZE);
    const indexInPage = globalIndex - pageStart;

    const doScroll = () => {
      if (!chartScrollRef.current) return;
      const barLeft = CHART_PADDING + indexInPage * (BAR_W + CHART_GAP);
      const barCenter = barLeft + BAR_W / 2;
      const offset = Math.max(0, barCenter - screenWidth / 2);
      chartScrollRef.current.scrollTo({ x: offset, animated: true });
    };

    if (targetPage !== chartPage) {
      setChartPage(targetPage);
      // Wait for the chart to re-render with the new page before scrolling.
      setTimeout(doScroll, 120);
    } else {
      setTimeout(doScroll, 50);
    }
  };

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
              ? `${selectedSnap.ghost_count} idle resource${selectedSnap.ghost_count !== 1 ? 's' : ''} detected across your accounts`
              : 'Latest projected monthly cost'}
          </Text>
        </View>
      </View>

      <View style={styles.chartContainer}>
        <View style={styles.chartHeader}>
          <Text style={styles.sectionTitle}>Monthly Projection Timeline</Text>
          <View style={styles.chartPagination}>
            <TouchableOpacity
              onPress={() => setChartPage(chartPage + 1)}
              disabled={!canGoBack}
              style={[styles.pageBtn, !canGoBack && styles.pageBtnDisabled]}
            >
              <Text style={[styles.pageBtnText, !canGoBack && styles.pageBtnTextDisabled]}>← Older</Text>
            </TouchableOpacity>
            <Text style={styles.pageInfo}>
              {chartPage === 0 ? 'Recent 90 days' : `${chartPage * PAGE_SIZE + 1}-${Math.min((chartPage + 1) * PAGE_SIZE, snaps.length)} days ago`}
            </Text>
            <TouchableOpacity
              onPress={() => setChartPage(chartPage - 1)}
              disabled={!canGoForward}
              style={[styles.pageBtn, !canGoForward && styles.pageBtnDisabled]}
            >
              <Text style={[styles.pageBtnText, !canGoForward && styles.pageBtnTextDisabled]}>Newer →</Text>
            </TouchableOpacity>
          </View>
        </View>
        <View style={styles.chartWrapper}>
          <FullTrendChart
            snaps={snaps}
            selectedId={selectedSnap?.snapshot_at}
            onSelect={(s) => {
              const newSelection = s.snapshot_at === selectedSnap?.snapshot_at ? null : s;
              setSelectedSnap(newSelection);
              if (newSelection) {
                scrollChartToSnap(newSelection);
                if (flatListRef.current) {
                  setTimeout(() => {
                    const index = reversedSnaps.findIndex(snap => snap.snapshot_at === newSelection.snapshot_at);
                    if (index >= 0) {
                      flatListRef.current.scrollToIndex({ index, viewPosition: 0.15, animated: true });
                    }
                  }, 100);
                }
              }
            }}
            theme={theme}
            scrollViewRef={chartScrollRef}
            page={chartPage}
            barWidth={BAR_W}
            contentWidth={chartContentWidth}
            screenWidth={screenWidth}
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
                  if (newSelection) {
                    scrollChartToSnap(newSelection);
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
                    {item.ghost_count === 0 ? 'No idle resources found' : `${item.ghost_count} idle resource${item.ghost_count !== 1 ? 's' : ''} found`}
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
  chartHeader: {
    flexDirection: 'row',
    justifyContent: 'space-between',
    alignItems: 'center',
    paddingHorizontal: 16,
    marginBottom: 12,
  },
  chartPagination: {
    flexDirection: 'row',
    alignItems: 'center',
    gap: 12,
  },
  pageBtn: {
    paddingHorizontal: 8,
    paddingVertical: 4,
  },
  pageBtnDisabled: {
    opacity: 0.3,
  },
  pageBtnText: {
    fontSize: 12,
    fontWeight: '600',
    color: theme.accent,
  },
  pageBtnTextDisabled: {
    color: theme.textMuted,
  },
  pageInfo: {
    fontSize: 11,
    color: theme.textMuted,
    fontWeight: '600',
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
