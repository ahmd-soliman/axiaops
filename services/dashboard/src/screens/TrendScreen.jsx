import { useState, useRef, useEffect, useCallback } from 'react';
import { useQuery } from '@tanstack/react-query';
import { fetchTrend } from '../api/client';
import { useTheme } from '../theme/ThemeContext';
import { useWindowWidth } from '../components/primitives';
import { Spinner } from '../components/primitives';

const CHART_HEIGHT = 200;
const MARGIN = { top: 16, right: 20, bottom: 32, left: 56 };

// Max rows to render in scan history list before paginating
const LIST_PAGE_SIZE = 50;

const PERIOD_OPTIONS = [
  { label: '7d',  days: 7 },
  { label: '30d', days: 30 },
  { label: '90d', days: 90 },
  { label: '6m',  days: 180 },
  { label: '1y',  days: 365 },
];

// ─── Downsampling ────────────────────────────────────────────────────────────
// 7d/30d: every scan. 90d/6m: weekly avg. 1y: monthly avg.

function downsample(snaps, periodDays) {
  if (periodDays <= 30 || snaps.length <= 30) return snaps;

  // Group by bucket key (ISO week or month)
  const bucketKey = periodDays <= 180
    ? (iso) => {
        // Week bucket: Monday of the ISO week
        const d = new Date(iso);
        const day = d.getUTCDay();
        const diff = d.getUTCDate() - day + (day === 0 ? -6 : 1);
        const mon = new Date(Date.UTC(d.getUTCFullYear(), d.getUTCMonth(), diff));
        return mon.toISOString().slice(0, 10);
      }
    : (iso) => {
        // Month bucket
        return new Date(iso).toISOString().slice(0, 7);
      };

  const buckets = new Map();
  for (const s of snaps) {
    const key = bucketKey(s.snapshot_at);
    if (!buckets.has(key)) buckets.set(key, []);
    buckets.get(key).push(s);
  }

  // Take the average per bucket, keep the latest snapshot_at as the representative timestamp
  return [...buckets.values()].map(group => {
    const latest = group[group.length - 1];
    const avgCost = group.reduce((sum, s) => sum + s.total_monthly_cost, 0) / group.length;
    const avgGhosts = Math.round(group.reduce((sum, s) => sum + s.ghost_count, 0) / group.length);
    return {
      ...latest,
      total_monthly_cost: Math.round(avgCost * 100) / 100,
      ghost_count: avgGhosts,
      _scanCount: group.length,
    };
  });
}

// ─── Format helpers ──────────────────────────────────────────────────────────

function formatCost(val) {
  if (val >= 10000) return `${(val / 1000).toFixed(0)}k`;
  if (val >= 1000) return `${(val / 1000).toFixed(1)}k`;
  return val.toFixed(0);
}

function formatDate(iso) {
  return new Date(iso).toLocaleDateString('en-GB', { day: 'numeric', month: 'short' });
}

function formatDateTime(iso) {
  const d = new Date(iso);
  return d.toLocaleDateString('en-GB', { day: 'numeric', month: 'short', year: 'numeric' })
    + ' · ' + d.toLocaleTimeString('en-GB', { hour: '2-digit', minute: '2-digit' });
}

// ─── SVG Area Chart ──────────────────────────────────────────────────────────

function AreaChart({ snaps, selectedId, onSelect, theme, screenWidth }) {
  const [hoverIdx, setHoverIdx] = useState(null);
  const svgRef = useRef(null);
  const gradientId = 'trend-area-grad';

  if (!snaps || snaps.length < 2) return null;

  const t = theme;
  const width = Math.max(320, screenWidth - 32);
  const plotW = width - MARGIN.left - MARGIN.right;
  const plotH = CHART_HEIGHT - MARGIN.top - MARGIN.bottom;

  const values = snaps.map(s => s.total_monthly_cost);
  const rawMax = Math.max(...values);
  const rawMin = Math.min(...values);
  const range = rawMax - rawMin || rawMax * 0.1 || 1;
  const maxVal = rawMax + range * 0.08;
  const minVal = Math.max(0, rawMin - range * 0.05);
  const valRange = maxVal - minVal || 1;

  // Map data to pixel coordinates
  const points = snaps.map((s, i) => ({
    x: MARGIN.left + (i / (snaps.length - 1)) * plotW,
    y: MARGIN.top + plotH - ((s.total_monthly_cost - minVal) / valRange) * plotH,
  }));

  // SVG paths
  const linePath = points.map((p, i) => `${i === 0 ? 'M' : 'L'}${p.x},${p.y}`).join(' ');
  const areaPath = linePath
    + ` L${points[points.length - 1].x},${MARGIN.top + plotH}`
    + ` L${points[0].x},${MARGIN.top + plotH} Z`;

  // Y-axis: 4 ticks
  const yTicks = [0, 1, 2, 3].map(i => {
    const val = minVal + valRange * (i / 3);
    const y = MARGIN.top + plotH - (i / 3) * plotH;
    return { val, y };
  });

  // X-axis: ~5 evenly spaced labels
  const xLabelCount = Math.min(6, snaps.length);
  const xLabels = [];
  for (let i = 0; i < xLabelCount; i++) {
    const idx = Math.round(i * (snaps.length - 1) / (xLabelCount - 1));
    xLabels.push({ x: points[idx].x, label: formatDate(snaps[idx].snapshot_at) });
  }

  // Find nearest data point to cursor
  const handleMouseMove = useCallback((e) => {
    const rect = svgRef.current?.getBoundingClientRect();
    if (!rect) return;
    const mouseX = e.clientX - rect.left;
    let closest = 0;
    let minDist = Infinity;
    for (let i = 0; i < points.length; i++) {
      const dist = Math.abs(points[i].x - mouseX);
      if (dist < minDist) { minDist = dist; closest = i; }
    }
    setHoverIdx(closest);
  }, [snaps.length, width]);

  function handleClick() {
    if (hoverIdx !== null) onSelect(snaps[hoverIdx]);
  }

  const hoverPoint = hoverIdx !== null ? points[hoverIdx] : null;
  const hoverSnap = hoverIdx !== null ? snaps[hoverIdx] : null;
  const selectedPointIdx = selectedId ? snaps.findIndex(s => s.snapshot_at === selectedId) : -1;
  const selectedPoint = selectedPointIdx >= 0 ? points[selectedPointIdx] : null;

  // Tooltip position — keep it within the chart bounds
  const tooltipW = 140;
  const tooltipLeft = hoverPoint
    ? Math.max(0, Math.min(hoverPoint.x - tooltipW / 2, width - tooltipW))
    : 0;

  return (
    <div style={{ padding: '0 16px', position: 'relative' }}>
      <svg
        ref={svgRef}
        width={width}
        height={CHART_HEIGHT}
        style={{ display: 'block', cursor: 'crosshair' }}
        onMouseMove={handleMouseMove}
        onMouseLeave={() => setHoverIdx(null)}
        onClick={handleClick}
      >
        <defs>
          <linearGradient id={gradientId} x1="0" y1="0" x2="0" y2="1">
            <stop offset="0%" stopColor={t.accent} stopOpacity="0.25" />
            <stop offset="100%" stopColor={t.accent} stopOpacity="0.02" />
          </linearGradient>
        </defs>

        {/* Horizontal grid lines */}
        {yTicks.map((tick, i) => (
          <line key={i} x1={MARGIN.left} y1={tick.y} x2={width - MARGIN.right} y2={tick.y}
            stroke={t.border} strokeWidth="1" opacity="0.4" />
        ))}

        {/* Area fill */}
        <path d={areaPath} fill={`url(#${gradientId})`} />

        {/* Line */}
        <path d={linePath} fill="none" stroke={t.accent} strokeWidth="2"
          strokeLinecap="round" strokeLinejoin="round" />

        {/* Y-axis labels */}
        {yTicks.map((tick, i) => (
          <text key={i} x={MARGIN.left - 10} y={tick.y + 4} textAnchor="end"
            fontSize="10" fontFamily="system-ui, sans-serif" fill={t.textMuted}>
            ${formatCost(tick.val)}
          </text>
        ))}

        {/* X-axis labels */}
        {xLabels.map((l, i) => (
          <text key={i} x={l.x} y={CHART_HEIGHT - 8} textAnchor="middle"
            fontSize="10" fontFamily="system-ui, sans-serif" fill={t.textMuted}>
            {l.label}
          </text>
        ))}

        {/* Selected point */}
        {selectedPoint && (
          <>
            <line x1={selectedPoint.x} y1={MARGIN.top} x2={selectedPoint.x} y2={MARGIN.top + plotH}
              stroke={t.accent} strokeWidth="1" strokeDasharray="4 3" opacity="0.6" />
            <circle cx={selectedPoint.x} cy={selectedPoint.y} r="5"
              fill={t.accent} stroke={t.surface} strokeWidth="2" />
          </>
        )}

        {/* Hover crosshair + dot */}
        {hoverPoint && hoverIdx !== selectedPointIdx && (
          <>
            <line x1={hoverPoint.x} y1={MARGIN.top} x2={hoverPoint.x} y2={MARGIN.top + plotH}
              stroke={t.textMuted} strokeWidth="1" opacity="0.3" />
            <circle cx={hoverPoint.x} cy={hoverPoint.y} r="4"
              fill={t.accent} stroke={t.surface} strokeWidth="2" />
          </>
        )}
      </svg>

      {/* Hover tooltip */}
      {hoverSnap && (
        <div style={{
          position: 'absolute',
          top: 4,
          left: tooltipLeft,
          width: tooltipW,
          backgroundColor: t.surface,
          border: `1px solid ${t.border}`,
          borderRadius: 8,
          padding: '8px 10px',
          pointerEvents: 'none',
          boxShadow: '0 4px 12px rgba(0,0,0,0.15)',
          zIndex: 10,
        }}>
          <span style={{ fontSize: 11, color: t.textMuted, display: 'block', marginBottom: 2 }}>
            {formatDate(hoverSnap.snapshot_at)}
          </span>
          <span style={{ fontSize: 15, fontWeight: 800, color: t.accent, display: 'block' }}>
            {hoverSnap.currency} {hoverSnap.total_monthly_cost.toFixed(2)}
          </span>
          <span style={{ fontSize: 11, color: t.textMid, display: 'block', marginTop: 1 }}>
            {hoverSnap.ghost_count} zombie{hoverSnap.ghost_count !== 1 ? 's' : ''}
          </span>
        </div>
      )}
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
          {formatDateTime(item.snapshot_at)}
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
  const [listPage, setListPage]         = useState(1);
  const [showScrollTop, setShowScrollTop] = useState(false);
  const listRef      = useRef(null);
  const topRef       = useRef(null);
  const loadMoreRef  = useRef(null);
  const t = theme;

  // Show "scroll to top" button when page is scrolled past the chart
  useEffect(() => {
    function onScroll() { setShowScrollTop(window.scrollY > 350); }
    window.addEventListener('scroll', onScroll, { passive: true });
    return () => window.removeEventListener('scroll', onScroll);
  }, []);

  // Derive data — raw snaps for history list, downsampled for chart
  const allSnaps      = trend.data ?? [];
  const filteredSnaps = allSnaps.slice(-period);
  const chartSnaps    = downsample(filteredSnaps, period);
  const reversedSnaps = [...filteredSnaps].reverse();

  // Scroll to selected row in history list
  useEffect(() => {
    if (!selectedSnap) return;
    const idx = reversedSnaps.findIndex(r => r.snapshot_at === selectedSnap.snapshot_at);
    if (idx < 0) return;

    const pageNeeded = Math.ceil((idx + 1) / LIST_PAGE_SIZE);
    if (listPage < pageNeeded) {
      setListPage(pageNeeded);
      return;
    }

    const el = listRef.current?.querySelectorAll('[data-snap]')?.[idx];
    el?.scrollIntoView({ behavior: 'smooth', block: 'center' });
  }, [selectedSnap, listPage]); // eslint-disable-line react-hooks/exhaustive-deps

  function changePeriod(days) {
    setPeriod(days);
    setSelectedSnap(null);
    setListPage(1);
  }

  function handleSelect(s) {
    setSelectedSnap(prev => prev?.snapshot_at === s.snapshot_at ? null : s);
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

  const latestSnap  = filteredSnaps[filteredSnaps.length - 1] ?? null;
  const displaySnap = selectedSnap ?? latestSnap;
  const firstSnap   = filteredSnaps[0];

  const delta = latestSnap && firstSnap && firstSnap !== latestSnap
    ? ((latestSnap.total_monthly_cost - firstSnap.total_monthly_cost) / Math.max(firstSnap.total_monthly_cost, 0.01)) * 100
    : null;

  const visibleRows = reversedSnaps.slice(0, listPage * LIST_PAGE_SIZE);
  const hasMoreRows = visibleRows.length < reversedSnaps.length;

  return (
    <div ref={topRef} style={{ backgroundColor: t.bg, minHeight: '100%' }}>

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
      <div style={{ backgroundColor: t.surface, borderBottom: `1px solid ${t.border}`, paddingTop: 16, paddingBottom: 16 }}>
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

        {chartSnaps.length < 2 ? (
          <div style={{ padding: '24px 16px', textAlign: 'center' }}>
            <span style={{ fontSize: 13, color: t.textMuted }}>
              Not enough data for this period. Try a longer range or run more scans.
            </span>
          </div>
        ) : (
          <>
            <AreaChart
              snaps={chartSnaps}
              selectedId={selectedSnap?.snapshot_at}
              onSelect={handleSelect}
              theme={t}
              screenWidth={screenWidth}
            />

            <div style={{ padding: '8px 16px 0', textAlign: 'center' }}>
              <span style={{ fontSize: 11, color: t.textMuted }}>
                {filteredSnaps.length} scan{filteredSnaps.length !== 1 ? 's' : ''}
                {chartSnaps.length < filteredSnaps.length && ` · ${chartSnaps.length} points (averaged)`}
                {' · click to inspect'}
              </span>
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
              onClick={() => handleSelect(item)}
            />
          ))}

          {hasMoreRows && (
            <button
              ref={loadMoreRef}
              onClick={() => {
                const btn = loadMoreRef.current;
                const btnTop = btn?.getBoundingClientRect().top ?? 0;
                setListPage(p => p + 1);
                // After React re-renders, keep the button in the same viewport position
                requestAnimationFrame(() => {
                  if (!btn) return;
                  const newTop = btn.getBoundingClientRect().top;
                  window.scrollBy(0, newTop - btnTop);
                });
              }}
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

      {/* Scroll to top */}
      {showScrollTop && (
        <button
          onClick={() => topRef.current?.scrollIntoView({ behavior: 'smooth' })}
          aria-label="Scroll to top"
          style={{
            position: 'fixed',
            bottom: 24,
            right: 24,
            width: 40,
            height: 40,
            borderRadius: 20,
            backgroundColor: t.surface,
            border: `1px solid ${t.border}`,
            boxShadow: '0 4px 12px rgba(0,0,0,0.15)',
            cursor: 'pointer',
            display: 'flex',
            alignItems: 'center',
            justifyContent: 'center',
            zIndex: 100,
          }}
        >
          <svg width="16" height="16" viewBox="0 0 24 24" fill="none"
            stroke={t.accent} strokeWidth="2.5" strokeLinecap="round" strokeLinejoin="round">
            <polyline points="18 15 12 9 6 15" />
          </svg>
        </button>
      )}
    </div>
  );
}
