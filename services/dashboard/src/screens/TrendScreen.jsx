import { useState, useRef, useEffect } from 'react';
import { useQuery } from '@tanstack/react-query';
import { fetchTrend } from '../api/client';
import { useTheme } from '../theme/ThemeContext';
import { useWindowWidth } from '../components/primitives';
import { Spinner } from '../components/primitives';

const CHART_HEIGHT  = 140;
const CHART_PADDING = 16;
const CHART_GAP     = 2;
const PAGE_SIZE     = 90; // bars per chart page

const PERIOD_OPTIONS = [
  { label: '7d',  days: 7 },
  { label: '30d', days: 30 },
  { label: '90d', days: 90 },
  { label: 'All', days: Infinity },
];

// Max rows to render in scan history list before paginating
const LIST_PAGE_SIZE = 50;

// ─── Bar chart — max PAGE_SIZE bars per page ──────────────────────────────────
function FullTrendChart({ snaps, selectedId, onSelect, theme, scrollRef, screenWidth, chartPage }) {
  if (!snaps || snaps.length === 0) return null;

  // Slice to current page (newest bars on page 0)
  const startIdx  = Math.max(0, snaps.length - (chartPage + 1) * PAGE_SIZE);
  const endIdx    = snaps.length - chartPage * PAGE_SIZE;
  const pageSnaps = snaps.slice(startIdx, endIdx);

  const values    = pageSnaps.map(s => s.total_monthly_cost);
  const maxVal    = Math.max(...values, 0.01);
  const barCount  = pageSnaps.length;

  // Fit bars to screen width; clamp between 4–28 px
  const available = screenWidth - CHART_PADDING * 2;
  const barW      = Math.min(28, Math.max(4, Math.floor(available / Math.max(1, barCount)) - CHART_GAP));
  const barsTotal = barCount * barW + Math.max(0, barCount - 1) * CHART_GAP;
  const hPad      = Math.max(CHART_PADDING, (screenWidth - barsTotal) / 2);
  const totalW    = Math.max(screenWidth, barsTotal + CHART_PADDING * 2);

  return (
    <div ref={scrollRef} style={{ width: '100%', overflowX: 'auto' }}>
      <div
        role="img"
        aria-label={`Savings trend chart, page ${chartPage + 1}`}
        style={{ width: totalW, height: CHART_HEIGHT, display: 'flex', alignItems: 'flex-end', paddingLeft: hPad, paddingRight: hPad }}
      >
        {pageSnaps.map((s, i) => {
          const barH     = Math.max(4, Math.round((s.total_monthly_cost / maxVal) * CHART_HEIGHT));
          const isSelect = s.snapshot_at === selectedId;
          const isLast   = chartPage === 0 && i === pageSnaps.length - 1;
          const bg       = (isSelect || isLast) ? theme.accent : `${theme.accent}55`;

          return (
            <button
              key={`${s.snapshot_at}-${i}`}
              onClick={() => onSelect(s)}
              aria-label={`${new Date(s.snapshot_at).toLocaleDateString('en-GB')}: ${s.currency} ${s.total_monthly_cost.toFixed(2)}`}
              aria-pressed={isSelect}
              style={{
                width: barW, height: CHART_HEIGHT,
                display: 'flex', flexDirection: 'column', justifyContent: 'flex-end',
                marginRight: i < pageSnaps.length - 1 ? CHART_GAP : 0,
                background: 'none', border: 'none', padding: 0,
                cursor: 'pointer', flexShrink: 0,
              }}
            >
              <div style={{ width: barW, height: barH, backgroundColor: bg, borderTopLeftRadius: 3, borderTopRightRadius: 3 }} />
            </button>
          );
        })}
      </div>
    </div>
  );
}

// ─── Scan history list row ────────────────────────────────────────────────────
function HistoryRow({ item, prevItem, isSelected, theme, onClick }) {
  const costDelta = prevItem ? item.total_monthly_cost - prevItem.total_monthly_cost : null;
  const t = theme;
  return (
    <button
      data-snap=""
      onClick={onClick}
      style={{
        display: 'flex',
        alignItems: 'center',
        justifyContent: 'space-between',
        padding: '13px 16px',
        borderBottom: `1px solid ${t.border}`,
        width: '100%',
        background: isSelected ? t.accentLight : 'none',
        cursor: 'pointer',
        textAlign: 'left',
      }}
    >
      <div style={{ display: 'flex', flexDirection: 'column', gap: 3 }}>
        <span style={{ fontSize: 14, color: t.text, fontWeight: 600 }}>
          {new Date(item.snapshot_at).toLocaleDateString('en-GB', { day: 'numeric', month: 'short', year: 'numeric' })}
          {' · '}
          {new Date(item.snapshot_at).toLocaleTimeString('en-GB', { hour: '2-digit', minute: '2-digit' })}
        </span>
        <span style={{ fontSize: 12, color: t.textMuted }}>
          {item.ghost_count === 0 ? 'No zombies found' : `${item.ghost_count} zombie${item.ghost_count !== 1 ? 's' : ''}`}
          {costDelta !== null && Math.abs(costDelta) >= 0.01 && (
            <span style={{ marginLeft: 8, color: costDelta > 0 ? t.error : t.success, fontWeight: 600 }}>
              {costDelta > 0 ? '+' : ''}{costDelta.toFixed(2)}
            </span>
          )}
        </span>
      </div>
      <span style={{ fontSize: 15, fontWeight: 700, color: isSelected ? t.accent : t.text, flexShrink: 0, marginLeft: 16 }}>
        {item.currency} {item.total_monthly_cost.toFixed(2)}
      </span>
    </button>
  );
}

// ─── Main screen ──────────────────────────────────────────────────────────────
export default function TrendScreen({ onBack }) {
  const { theme }   = useTheme();
  const screenWidth = useWindowWidth();
  const trend       = useQuery({ queryKey: ['trend'], queryFn: () => fetchTrend(null) });
  const [selectedSnap, setSelectedSnap] = useState(null);
  const [period, setPeriod]             = useState(30);
  const [chartPage, setChartPage]       = useState(0); // 0 = most recent
  const [listPage, setListPage]         = useState(1); // how many LIST_PAGE_SIZE rows to show
  const listRef        = useRef(null);
  const chartScrollRef = useRef(null);
  const t = theme;

  // Derive data unconditionally (safe to do before early returns — defaults to [] while loading)
  const allSnaps      = trend.data ?? [];
  const filteredSnaps = period === Infinity ? allSnaps : allSnaps.slice(-period);
  const reversedSnaps = [...filteredSnaps].reverse();

  // Hooks must all be called before any conditional return (Rules of Hooks)
  // When selectedSnap or listPage changes: expand list if needed, then scroll to the row.
  useEffect(() => {
    if (!selectedSnap) return;
    const idx = reversedSnaps.findIndex(r => r.snapshot_at === selectedSnap.snapshot_at);
    if (idx < 0) return;

    // If the row isn't rendered yet, expand the list — effect will re-run after re-render
    const pageNeeded = Math.ceil((idx + 1) / LIST_PAGE_SIZE);
    if (listPage < pageNeeded) {
      setListPage(pageNeeded);
      return;
    }

    // Row is in the DOM — scroll to it
    const el = listRef.current?.querySelectorAll('[data-snap]')?.[idx];
    el?.scrollIntoView({ behavior: 'smooth', block: 'center' });
  }, [selectedSnap, listPage]); // eslint-disable-line react-hooks/exhaustive-deps

  // Reset pagination when period changes
  function changePeriod(days) {
    setPeriod(days);
    setSelectedSnap(null);
    setChartPage(0);
    setListPage(1);
  }

  function handleSelectBar(s) {
    const newSel = s.snapshot_at === selectedSnap?.snapshot_at ? null : s;
    setSelectedSnap(newSel);
  }

  if (trend.isLoading) {
    return (
      <div style={{ display: 'flex', justifyContent: 'center', alignItems: 'center', minHeight: '60vh', backgroundColor: t.bg, flexDirection: 'column', gap: 14 }}>
        <Spinner size={32} color={t.accent} />
        <span style={{ color: t.textMuted, fontSize: 14 }}>Loading trend data…</span>
      </div>
    );
  }

  if (trend.isError || !trend.data) {
    return (
      <div style={{ display: 'flex', justifyContent: 'center', alignItems: 'center', minHeight: '60vh', backgroundColor: t.bg, flexDirection: 'column', gap: 16 }}>
        <span style={{ color: t.textMid, fontSize: 16 }}>Failed to load trend data.</span>
        <button onClick={onBack} style={{ padding: '10px 20px', backgroundColor: t.accent, borderRadius: 8, border: 'none', cursor: 'pointer' }}>
          <span style={{ color: '#fff', fontWeight: 600 }}>Go Back</span>
        </button>
      </div>
    );
  }

  const totalChartPages = Math.max(1, Math.ceil(filteredSnaps.length / PAGE_SIZE));
  const canGoOlder      = chartPage < totalChartPages - 1;
  const canGoNewer      = chartPage > 0;

  const latestSnap  = filteredSnaps[filteredSnaps.length - 1] ?? null;
  const displaySnap = selectedSnap ?? latestSnap;
  const firstSnap   = filteredSnaps[0];

  const delta = latestSnap && firstSnap && firstSnap !== latestSnap
    ? ((latestSnap.total_monthly_cost - firstSnap.total_monthly_cost) / Math.max(firstSnap.total_monthly_cost, 0.01)) * 100
    : null;

  function handleSelectRow(item) {
    const newSel = item.snapshot_at === selectedSnap?.snapshot_at ? null : item;
    setSelectedSnap(newSel);
    if (newSel) {
      const globalIdx  = filteredSnaps.findIndex(s => s.snapshot_at === newSel.snapshot_at);
      const targetPage = Math.floor((filteredSnaps.length - 1 - globalIdx) / PAGE_SIZE);

      // Switch chart page first, then scroll the bar to center after render
      setChartPage(targetPage);
      setTimeout(() => {
        if (!chartScrollRef.current) return;
        const container  = chartScrollRef.current;
        // Index of the bar within the target page
        const pageStart  = Math.max(0, filteredSnaps.length - (targetPage + 1) * PAGE_SIZE);
        const idxInPage  = globalIdx - pageStart;
        // Approximate bar width (matches FullTrendChart calculation)
        const available  = container.clientWidth - CHART_PADDING * 2;
        const pageCount  = Math.min(PAGE_SIZE, filteredSnaps.length - targetPage * PAGE_SIZE);
        const barW       = Math.min(28, Math.max(4, Math.floor(available / Math.max(1, pageCount)) - CHART_GAP));
        const barCenter  = CHART_PADDING + idxInPage * (barW + CHART_GAP) + barW / 2;
        const scrollLeft = barCenter - container.clientWidth / 2;
        container.scrollTo({ left: Math.max(0, scrollLeft), behavior: 'smooth' });
      }, 50);
    }
  }

  // Paginate the list — show more rows on demand
  const visibleRows = reversedSnaps.slice(0, listPage * LIST_PAGE_SIZE);
  const hasMoreRows = visibleRows.length < reversedSnaps.length;

  return (
    <div style={{ backgroundColor: t.bg, minHeight: '100%' }}>

      {/* Page header */}
      <div style={{ backgroundColor: t.surfaceAlt, borderBottom: `1px solid ${t.border}`, padding: '14px 20px 20px' }}>
        <button onClick={onBack} style={{ padding: '4px 0', background: 'none', border: 'none', cursor: 'pointer', marginBottom: 12 }}>
          <span style={{ color: t.textMuted, fontWeight: 600, fontSize: 14 }}>← Back</span>
        </button>

        <span style={{ fontSize: 11, fontWeight: 600, color: t.textMuted, letterSpacing: 1.2, textTransform: 'uppercase', display: 'block', marginBottom: 3 }}>
          {selectedSnap
            ? `Snapshot · ${new Date(selectedSnap.snapshot_at).toLocaleDateString('en-GB', { day: 'numeric', month: 'short', year: 'numeric' })}`
            : 'Savings Trend'}
        </span>
        <span style={{ fontSize: 32, fontWeight: 800, color: t.accent, letterSpacing: -0.5, display: 'block' }}>
          {displaySnap?.currency ?? '$'} {displaySnap ? displaySnap.total_monthly_cost.toFixed(2) : '0.00'}
        </span>
        <div style={{ display: 'flex', alignItems: 'center', gap: 12, marginTop: 4, flexWrap: 'wrap' }}>
          <span style={{ fontSize: 13, color: t.textMid }}>
            {displaySnap
              ? `${displaySnap.ghost_count} zombie resource${displaySnap.ghost_count !== 1 ? 's' : ''}`
              : 'No data'}
          </span>
          {delta !== null && (
            <span style={{ fontSize: 12, fontWeight: 700, color: delta > 0 ? t.error : t.success }}>
              {delta > 0 ? '▲' : '▼'} {Math.abs(delta).toFixed(1)}% over period
            </span>
          )}
        </div>
      </div>

      {/* Chart section */}
      <div style={{ backgroundColor: t.surface, borderBottom: `1px solid ${t.border}`, paddingTop: 16, paddingBottom: 12 }}>
        {/* Period selector */}
        <div style={{ display: 'flex', alignItems: 'center', justifyContent: 'space-between', padding: '0 16px 14px', flexWrap: 'wrap', gap: 8 }}>
          <span style={{ fontSize: 11, fontWeight: 700, color: t.textMuted, letterSpacing: 1.2, textTransform: 'uppercase' }}>
            Timeline
          </span>
          <div role="group" aria-label="Select time period" style={{ display: 'flex', gap: 4 }}>
            {PERIOD_OPTIONS.map(p => {
              const active = period === p.days;
              return (
                <button
                  key={p.label}
                  onClick={() => changePeriod(p.days)}
                  aria-pressed={active}
                  style={{
                    padding: '4px 10px', borderRadius: 6, cursor: 'pointer',
                    backgroundColor: active ? t.accent : t.surfaceRaised,
                    border: `1px solid ${active ? t.accent : t.border}`,
                    fontSize: 12, fontWeight: 700,
                    color: active ? '#fff' : t.textMid,
                  }}
                >
                  {p.label}
                </button>
              );
            })}
          </div>
        </div>

        {filteredSnaps.length < 2 ? (
          <div style={{ padding: '24px 16px', textAlign: 'center' }}>
            <span style={{ fontSize: 13, color: t.textMuted }}>
              Not enough data for this period. Try a longer range or run more scans.
            </span>
          </div>
        ) : (
          <>
            <FullTrendChart
              snaps={filteredSnaps}
              selectedId={selectedSnap?.snapshot_at}
              onSelect={handleSelectBar}
              theme={t}
              scrollRef={chartScrollRef}
              screenWidth={screenWidth}
              chartPage={chartPage}
            />

            {/* Pagination controls */}
            <div style={{ display: 'flex', alignItems: 'center', justifyContent: 'space-between', padding: '10px 16px 0' }}>
              <button
                onClick={() => { setChartPage(p => p + 1); setSelectedSnap(null); }}
                disabled={!canGoOlder}
                style={{ padding: '4px 10px', background: 'none', border: 'none', cursor: canGoOlder ? 'pointer' : 'default', opacity: canGoOlder ? 1 : 0.3 }}
              >
                <span style={{ fontSize: 12, fontWeight: 700, color: t.accent }}>← Older</span>
              </button>

              <span style={{ fontSize: 11, color: t.textMuted }}>
                {totalChartPages > 1
                  ? `Page ${chartPage + 1} of ${totalChartPages}`
                  : `${filteredSnaps.length} scan${filteredSnaps.length !== 1 ? 's' : ''}`}
                {' · '}click to inspect
              </span>

              <button
                onClick={() => { setChartPage(p => p - 1); setSelectedSnap(null); }}
                disabled={!canGoNewer}
                style={{ padding: '4px 10px', background: 'none', border: 'none', cursor: canGoNewer ? 'pointer' : 'default', opacity: canGoNewer ? 1 : 0.3 }}
              >
                <span style={{ fontSize: 12, fontWeight: 700, color: t.accent }}>Newer →</span>
              </button>
            </div>
          </>
        )}
      </div>

      {/* Scan history list */}
      <div ref={listRef}>
        <div style={{ padding: '16px 16px 0' }}>
          <span style={{ fontSize: 11, fontWeight: 700, color: t.textMuted, letterSpacing: 1.2, textTransform: 'uppercase' }}>
            Scan History · {filteredSnaps.length}
          </span>
        </div>

        <div style={{ paddingBottom: 48 }}>
          {visibleRows.map((item, idx) => (
            <HistoryRow
              key={item.snapshot_at + idx}
              item={item}
              prevItem={reversedSnaps[idx + 1]}
              isSelected={selectedSnap?.snapshot_at === item.snapshot_at}
              theme={t}
              onClick={() => handleSelectRow(item)}
            />
          ))}

          {/* Load more button — avoids rendering 500+ rows at once */}
          {hasMoreRows && (
            <button
              onClick={() => setListPage(p => p + 1)}
              style={{
                display: 'block',
                width: '100%',
                padding: '14px 16px',
                background: 'none',
                border: 'none',
                borderTop: `1px solid ${t.border}`,
                cursor: 'pointer',
                color: t.accent,
                fontSize: 13,
                fontWeight: 600,
                textAlign: 'center',
              }}
            >
              Load more ({reversedSnaps.length - visibleRows.length} remaining)
            </button>
          )}
        </div>
      </div>
    </div>
  );
}
