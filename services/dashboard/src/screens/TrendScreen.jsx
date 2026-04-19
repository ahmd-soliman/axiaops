import { useState, useRef } from 'react';
import { useQuery } from '@tanstack/react-query';
import { fetchTrend } from '../api/client';
import { useTheme } from '../theme/ThemeContext';
import { useWindowWidth } from '../components/primitives';
import { Spinner } from '../components/primitives';

const PAGE_SIZE = 90;
const CHART_HEIGHT = 160;
const CHART_GAP = 2;
const CHART_PADDING = 16;

function FullTrendChart({ snaps, selectedId, onSelect, theme, scrollRef, page = 0, barWidth, contentWidth, screenWidth }) {
  if (!snaps || snaps.length < 2) return null;

  const startIndex = Math.max(0, snaps.length - (page + 1) * PAGE_SIZE);
  const endIndex = snaps.length - page * PAGE_SIZE;
  const pageSnaps = snaps.slice(startIndex, endIndex);

  const values = pageSnaps.map((s) => s.total_monthly_cost);
  const maxVal = Math.max(...values, 0.01);

  const barsWidth = pageSnaps.length * barWidth + Math.max(0, pageSnaps.length - 1) * CHART_GAP;
  const horizontalPadding = Math.max(CHART_PADDING, (screenWidth - barsWidth) / 2);

  return (
    <div ref={scrollRef} style={{ width: '100%', overflowX: 'auto' }}>
      <div style={{ width: contentWidth, height: CHART_HEIGHT, display: 'flex', flexDirection: 'row', alignItems: 'flex-end', paddingLeft: horizontalPadding, paddingRight: horizontalPadding }}>
        {pageSnaps.map((s, i) => {
          const barH = Math.max(4, Math.round((s.total_monthly_cost / maxVal) * CHART_HEIGHT));
          const isSelected = selectedId === s.snapshot_at;
          const isLast = i === pageSnaps.length - 1 && page === 0;
          const bgColor = isSelected ? theme.accent : isLast ? theme.accent : `${theme.accent}73`;

          return (
            <button
              key={i}
              onClick={() => onSelect(s)}
              style={{ width: barWidth, height: CHART_HEIGHT, display: 'flex', flexDirection: 'column', justifyContent: 'flex-end', marginRight: i < pageSnaps.length - 1 ? CHART_GAP : 0, background: 'none', border: 'none', padding: 0, cursor: 'pointer', flexShrink: 0 }}
            >
              <div style={{ width: barWidth, height: barH, backgroundColor: bgColor, borderTopLeftRadius: 3, borderTopRightRadius: 3 }} />
            </button>
          );
        })}
      </div>
    </div>
  );
}

export default function TrendScreen({ onBack }) {
  const { theme } = useTheme();
  const screenWidth = useWindowWidth();
  const trend = useQuery({ queryKey: ['trend'], queryFn: () => fetchTrend(null) });
  const [selectedSnap, setSelectedSnap] = useState(null);
  const listRef = useRef(null);
  const chartScrollRef = useRef(null);
  const [chartPage, setChartPage] = useState(0);

  const t = theme;

  if (trend.isLoading) {
    return (
      <div style={{ display: 'flex', justifyContent: 'center', alignItems: 'center', minHeight: '100vh', backgroundColor: t.bg, flexDirection: 'column', gap: 14 }}>
        <Spinner size={32} color={t.accent} />
        <span style={{ color: t.textMuted, fontSize: 14 }}>Loading trend data...</span>
      </div>
    );
  }

  if (trend.isError || !trend.data) {
    return (
      <div style={{ display: 'flex', justifyContent: 'center', alignItems: 'center', minHeight: '100vh', backgroundColor: t.bg, flexDirection: 'column', gap: 20 }}>
        <span style={{ color: t.textMid, fontSize: 16 }}>Failed to load trend data.</span>
        <button onClick={onBack} style={{ paddingLeft: 20, paddingRight: 20, paddingTop: 10, paddingBottom: 10, backgroundColor: t.navy, borderRadius: 8, border: 'none', cursor: 'pointer' }}>
          <span style={{ color: t.white, fontWeight: 600 }}>Go Back</span>
        </button>
      </div>
    );
  }

  const snaps = trend.data;
  const latestSnap = snaps.length > 0 ? snaps[snaps.length - 1] : null;
  const displaySnap = selectedSnap || latestSnap;
  const reversedSnaps = [...snaps].reverse();

  const totalPages = Math.max(1, Math.ceil(snaps.length / PAGE_SIZE));
  const canGoBack = chartPage < totalPages - 1;
  const canGoForward = chartPage > 0;

  const currentPageCount = Math.min(PAGE_SIZE, snaps.length - chartPage * PAGE_SIZE);
  const availableChartWidth = screenWidth - CHART_PADDING * 2;
  const computedBarW = Math.floor(availableChartWidth / Math.max(1, currentPageCount)) - CHART_GAP;
  const BAR_W = Math.min(24, Math.max(4, computedBarW));
  const barsWidth = currentPageCount * BAR_W + Math.max(0, currentPageCount - 1) * CHART_GAP;
  const chartContentWidth = Math.max(screenWidth, barsWidth + CHART_PADDING * 2);

  const scrollChartToSnap = (snap) => {
    if (!snap || !chartScrollRef.current) return;
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
      chartScrollRef.current.scrollLeft = offset;
    };
    if (targetPage !== chartPage) {
      setChartPage(targetPage);
      setTimeout(doScroll, 120);
    } else {
      setTimeout(doScroll, 50);
    }
  };

  return (
    <div style={{ display: 'flex', flexDirection: 'column', backgroundColor: t.bg, minHeight: '100vh' }}>
      {/* Header */}
      <div style={{ backgroundColor: t.surfaceAlt, paddingBottom: 28, borderBottom: `1px solid ${t.border}` }}>
        <button onClick={onBack} style={{ paddingLeft: 20, paddingTop: 16, paddingBottom: 12, background: 'none', border: 'none', cursor: 'pointer' }}>
          <span style={{ color: t.textMuted, fontWeight: 600, fontSize: 14 }}>← Back</span>
        </button>
        <div style={{ paddingLeft: 20, paddingRight: 20 }}>
          <span style={{ color: t.textMuted, fontSize: 11, fontWeight: 600, letterSpacing: 1.5, textTransform: 'uppercase', marginBottom: 4, display: 'block' }}>
            {selectedSnap ? `Snapshot: ${new Date(selectedSnap.snapshot_at).toLocaleDateString('en-GB')}` : 'Historical Savings Trend'}
          </span>
          <span style={{ color: t.accent, fontSize: 46, fontWeight: 800, letterSpacing: -1, display: 'block' }}>
            {displaySnap?.currency || '$'} {displaySnap ? displaySnap.total_monthly_cost.toFixed(2) : '0.00'}
          </span>
          <span style={{ color: t.textMid, fontSize: 13, marginTop: 4, display: 'block' }}>
            {selectedSnap
              ? `${selectedSnap.ghost_count} zombie resource${selectedSnap.ghost_count !== 1 ? 's' : ''} detected across your accounts`
              : 'Latest projected monthly cost'}
          </span>
        </div>
      </div>

      {/* Chart */}
      <div style={{ backgroundColor: t.surface, paddingTop: 20, paddingBottom: 16, borderBottom: `1px solid ${t.border}` }}>
        <div style={{ display: 'flex', flexDirection: 'row', justifyContent: 'space-between', alignItems: 'center', paddingLeft: 16, paddingRight: 16, marginBottom: 12 }}>
          <span style={{ fontSize: 11, fontWeight: 700, color: t.textMuted, letterSpacing: 1.5, textTransform: 'uppercase' }}>Monthly Projection Timeline</span>
          <div style={{ display: 'flex', flexDirection: 'row', alignItems: 'center', gap: 12 }}>
            <button onClick={() => setChartPage(chartPage + 1)} disabled={!canGoBack} style={{ paddingLeft: 8, paddingRight: 8, paddingTop: 4, paddingBottom: 4, background: 'none', border: 'none', cursor: canGoBack ? 'pointer' : 'default', opacity: canGoBack ? 1 : 0.3 }}>
              <span style={{ fontSize: 12, fontWeight: 600, color: t.accent }}>← Older</span>
            </button>
            <span style={{ fontSize: 11, color: t.textMuted, fontWeight: 600 }}>
              {chartPage === 0 ? 'Recent 90 days' : `${chartPage * PAGE_SIZE + 1}-${Math.min((chartPage + 1) * PAGE_SIZE, snaps.length)} days ago`}
            </span>
            <button onClick={() => setChartPage(chartPage - 1)} disabled={!canGoForward} style={{ paddingLeft: 8, paddingRight: 8, paddingTop: 4, paddingBottom: 4, background: 'none', border: 'none', cursor: canGoForward ? 'pointer' : 'default', opacity: canGoForward ? 1 : 0.3 }}>
              <span style={{ fontSize: 12, fontWeight: 600, color: t.accent }}>Newer →</span>
            </button>
          </div>
        </div>
        <FullTrendChart
          snaps={snaps}
          selectedId={selectedSnap?.snapshot_at}
          onSelect={(s) => {
            const newSelection = s.snapshot_at === selectedSnap?.snapshot_at ? null : s;
            setSelectedSnap(newSelection);
            if (newSelection) scrollChartToSnap(newSelection);
          }}
          theme={t}
          scrollRef={chartScrollRef}
          page={chartPage}
          barWidth={BAR_W}
          contentWidth={chartContentWidth}
          screenWidth={screenWidth}
        />
        <span style={{ paddingLeft: 16, marginTop: 12, fontSize: 12, color: t.textMuted, fontStyle: 'italic', display: 'block' }}>{snaps.length} days of recorded history</span>
      </div>

      {/* Scan history list */}
      <div style={{ flex: 1, overflowY: 'auto' }} ref={listRef}>
        <span style={{ fontSize: 11, fontWeight: 700, color: t.textMuted, letterSpacing: 1.5, textTransform: 'uppercase', marginBottom: 12, paddingLeft: 16, paddingTop: 16, display: 'block' }}>Scan History</span>
        <div style={{ paddingLeft: 8, paddingRight: 8, paddingBottom: 40 }}>
          {reversedSnaps.map((item, idx) => {
            const isSelected = selectedSnap?.snapshot_at === item.snapshot_at;
            return (
              <button
                key={item.snapshot_at + idx}
                onClick={() => {
                  const newSelection = isSelected ? null : item;
                  setSelectedSnap(newSelection);
                  if (newSelection) scrollChartToSnap(newSelection);
                }}
                style={{ display: 'flex', flexDirection: 'row', justifyContent: 'space-between', alignItems: 'center', paddingTop: 14, paddingBottom: 14, paddingLeft: 8, paddingRight: 8, borderBottom: `1px solid ${t.border}`, borderRadius: 8, width: '100%', background: isSelected ? t.accentLight : 'none', border: isSelected ? `1px solid ${t.accentBorder}` : `1px solid transparent`, cursor: 'pointer', textAlign: 'left', marginBottom: 2 }}
              >
                <div>
                  <span style={{ fontSize: 14, color: t.text, fontWeight: 600, marginBottom: 4, display: 'block' }}>
                    {new Date(item.snapshot_at).toLocaleDateString('en-GB', { day: 'numeric', month: 'short', year: 'numeric' })}
                    {' at '}
                    {new Date(item.snapshot_at).toLocaleTimeString('en-GB', { hour: '2-digit', minute: '2-digit' })}
                  </span>
                  <span style={{ fontSize: 12, color: t.textMuted, display: 'block' }}>
                    {item.ghost_count === 0 ? 'No ghosts found' : `${item.ghost_count} ghost${item.ghost_count !== 1 ? 's' : ''} found`}
                  </span>
                </div>
                <span style={{ fontSize: 15, fontWeight: 700, color: t.accent }}>{item.currency} {item.total_monthly_cost.toFixed(2)}</span>
              </button>
            );
          })}
        </div>
      </div>
    </div>
  );
}
