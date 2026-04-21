import { useState, useRef, useEffect, useCallback } from 'react';
import { useQuery, useQueries } from '@tanstack/react-query';
import { fetchTrend, fetchTrendServices, fetchTrendResourceTypes } from '../api/client';
import { serviceConfig, resourceTypeConfig } from '../components/serviceConfig';
import AccountSelector from '../components/AccountSelector';
import AreaChart from '../components/AreaChart';
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

// ─── Snapshot merge ──────────────────────────────────────────────────────────
// When multiple filter buckets are active (e.g. EC2 + RDS, or EC2 · Volumes +
// EC2 · Snapshots), we fetch one time series per bucket and sum them here by
// snapshot_at. Works because all buckets for a single tenant share the same
// snapshot timestamps.
function mergeSnapshotSeries(seriesList) {
  const byTimestamp = new Map();
  for (const series of seriesList) {
    if (!Array.isArray(series)) continue;
    for (const snap of series) {
      const existing = byTimestamp.get(snap.snapshot_at);
      if (!existing) {
        byTimestamp.set(snap.snapshot_at, { ...snap });
      } else {
        existing.total_monthly_cost += snap.total_monthly_cost;
        existing.ghost_count       += snap.ghost_count;
      }
    }
  }
  return [...byTimestamp.values()].sort((a, b) => a.snapshot_at.localeCompare(b.snapshot_at));
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
export default function TrendScreen({ accounts, selectedAccount, selectedAwsAccount, onSelectAccount, onBack }) {
  const { theme, isDark } = useTheme();
  const screenWidth = useWindowWidth();
  const [filterServices, setFilterServices]         = useState(() => new Set());
  const [filterResourceTypes, setFilterResourceTypes] = useState(() => new Set());

  const trendServices = useQuery({ queryKey: ['trend-services'], queryFn: fetchTrendServices });

  // Sub-types only make sense under a single service, so we only fetch & render
  // the sub-filter when exactly one service is selected.
  const singleService = filterServices.size === 1 ? [...filterServices][0] : null;
  const trendResourceTypes = useQuery({
    queryKey: ['trend-resource-types', singleService],
    queryFn: () => fetchTrendResourceTypes(singleService),
    enabled: !!singleService,
  });

  // Build the set of trend queries to run. Each "bucket" produces its own time
  // series; we sum them below.
  //  - nothing selected            → one query, no filter (all services)
  //  - N services, no sub-types    → N queries, one per service
  //  - 1 service, M sub-types      → M queries, one per service/sub-type combo
  const filterBuckets = (() => {
    if (filterServices.size === 0) {
      return [{ service: null, resourceType: null }];
    }
    if (filterServices.size === 1 && filterResourceTypes.size > 0) {
      const [svc] = filterServices;
      return [...filterResourceTypes].map(rt => ({ service: svc, resourceType: rt }));
    }
    return [...filterServices].map(svc => ({ service: svc, resourceType: null }));
  })();

  const trendQueries = useQueries({
    queries: filterBuckets.map(b => ({
      queryKey: ['trend', selectedAwsAccount, b.service, b.resourceType],
      queryFn: () => fetchTrend(selectedAwsAccount, b.service, b.resourceType),
    })),
  });

  // Aggregate query state across all buckets
  const trendIsLoading = trendQueries.some(q => q.isLoading);
  const trendIsError   = trendQueries.length > 0 && trendQueries.every(q => q.isError);
  const trendHasData   = trendQueries.some(q => Array.isArray(q.data));
  const mergedSnaps    = mergeSnapshotSeries(trendQueries.map(q => q.data));
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
  const allSnaps      = mergedSnaps;
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

  function toggleServiceFilter(svc) {
    const next = new Set(filterServices);
    next.has(svc) ? next.delete(svc) : next.add(svc);
    setFilterServices(next);
    // Sub-types only make sense under a single service; clear them otherwise.
    if (next.size !== 1) setFilterResourceTypes(new Set());
    setSelectedSnap(null);
    setListPage(1);
  }

  function clearServiceFilter() {
    setFilterServices(new Set());
    setFilterResourceTypes(new Set());
    setSelectedSnap(null);
    setListPage(1);
  }

  function toggleResourceTypeFilter(rt) {
    setFilterResourceTypes(prev => {
      const next = new Set(prev);
      next.has(rt) ? next.delete(rt) : next.add(rt);
      return next;
    });
    setSelectedSnap(null);
    setListPage(1);
  }

  function clearResourceTypeFilter() {
    setFilterResourceTypes(new Set());
    setSelectedSnap(null);
    setListPage(1);
  }

  function handleSelect(s) {
    setSelectedSnap(prev => prev?.snapshot_at === s.snapshot_at ? null : s);
  }

  if (trendIsLoading && !trendHasData) {
    return (
      <div style={{ display: 'flex', justifyContent: 'center', alignItems: 'center', minHeight: '60vh', backgroundColor: t.bg, flexDirection: 'column', gap: 14 }}>
        <Spinner size={32} color={t.accent} />
        <span style={{ color: t.textMuted, fontSize: 14 }}>Loading trend data…</span>
      </div>
    );
  }

  if (trendIsError || !trendHasData) {
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

      {/* Account selector header */}
      <div style={{ backgroundColor: t.surface, borderBottom: `1px solid ${t.border}`, padding: '16px' }}>
        <AccountSelector
          accounts={accounts}
          selectedAccount={selectedAccount}
          onSelectAccount={onSelectAccount}
        />
      </div>

      {/* Page header */}
      <div style={{ backgroundColor: t.surfaceAlt, borderBottom: `1px solid ${t.border}`, padding: '14px 20px 20px' }}>
        <button onClick={onBack} style={{ padding: '4px 0', background: 'none', border: 'none', cursor: 'pointer', marginBottom: 12 }}>
          <span style={{ color: t.textMuted, fontWeight: 600, fontSize: 14 }}>← Back</span>
        </button>

        <span style={{ fontSize: 11, fontWeight: 600, color: t.textMuted, letterSpacing: 1.2, textTransform: 'uppercase', display: 'block', marginBottom: 3 }}>
          {(() => {
            if (selectedSnap) {
              return `Snapshot · ${new Date(selectedSnap.snapshot_at).toLocaleDateString('en-GB', { day: 'numeric', month: 'short', year: 'numeric' })}`;
            }
            const svcLabels = [...filterServices].map(s => serviceConfig(s).label);
            if (filterServices.size === 1 && filterResourceTypes.size > 0) {
              const rtLabels = [...filterResourceTypes].map(rt => resourceTypeConfig(rt).label).join(' + ');
              return `${svcLabels[0]} · ${rtLabels}`;
            }
            if (filterServices.size >= 1) {
              return `${svcLabels.join(' + ')} Trend`;
            }
            return 'Savings Trend';
          })()}
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

        {/* Service filter pills */}
        {(trendServices.data?.length ?? 0) > 0 && (
          <div
            role="group"
            aria-label="Filter by service"
            style={{ display: 'flex', gap: 6, padding: '0 16px 12px', overflowX: 'auto' }}
          >
            <button
              onClick={clearServiceFilter}
              aria-pressed={filterServices.size === 0}
              style={{
                padding: '4px 10px', borderRadius: 20, cursor: 'pointer', flexShrink: 0,
                backgroundColor: filterServices.size === 0 ? t.accent : t.surfaceRaised,
                border: `1px solid ${filterServices.size === 0 ? t.accent : t.border}`,
                fontSize: 12, fontWeight: 700,
                color: filterServices.size === 0 ? '#fff' : t.textMid,
              }}
            >
              All Services
            </button>
            {trendServices.data.map(svc => {
              const cfg = serviceConfig(svc);
              const active = filterServices.has(svc);
              return (
                <button
                  key={svc}
                  onClick={() => toggleServiceFilter(svc)}
                  aria-pressed={active}
                  style={{
                    display: 'flex', alignItems: 'center', gap: 5,
                    padding: '4px 10px', borderRadius: 20, cursor: 'pointer', flexShrink: 0,
                    backgroundColor: active ? t.accent : t.surfaceRaised,
                    border: `1px solid ${active ? t.accent : t.border}`,
                  }}
                >
                  <div style={{ width: 6, height: 6, borderRadius: '50%', backgroundColor: active ? '#fff' : cfg.color }} />
                  <span style={{ fontSize: 12, fontWeight: 700, color: active ? '#fff' : t.textMid }}>{cfg.label}</span>
                </button>
              );
            })}
          </div>
        )}

        {/* Resource type sub-filter pills (shown when exactly one service is selected and has sub-types) */}
        {filterServices.size === 1 && (trendResourceTypes.data?.length ?? 0) > 0 && (
          <div
            role="group"
            aria-label="Filter by resource type"
            style={{ display: 'flex', gap: 6, padding: '0 16px 12px', overflowX: 'auto' }}
          >
            <button
              onClick={clearResourceTypeFilter}
              aria-pressed={filterResourceTypes.size === 0}
              style={{
                padding: '3px 8px', borderRadius: 14, cursor: 'pointer', flexShrink: 0,
                backgroundColor: filterResourceTypes.size === 0 ? t.textMid : t.surfaceRaised,
                border: `1px solid ${filterResourceTypes.size === 0 ? t.textMid : t.border}`,
                fontSize: 11, fontWeight: 600,
                color: filterResourceTypes.size === 0 ? '#fff' : t.textMuted,
              }}
            >
              All Types
            </button>
            {trendResourceTypes.data.map(rt => {
              const cfg = resourceTypeConfig(rt);
              const active = filterResourceTypes.has(rt);
              return (
                <button
                  key={rt}
                  onClick={() => toggleResourceTypeFilter(rt)}
                  aria-pressed={active}
                  style={{
                    display: 'flex', alignItems: 'center', gap: 4,
                    padding: '3px 8px', borderRadius: 14, cursor: 'pointer', flexShrink: 0,
                    backgroundColor: active ? t.textMid : t.surfaceRaised,
                    border: `1px solid ${active ? t.textMid : t.border}`,
                  }}
                >
                  <div style={{ width: 5, height: 5, borderRadius: '50%', backgroundColor: active ? '#fff' : cfg.color }} />
                  <span style={{ fontSize: 11, fontWeight: 600, color: active ? '#fff' : t.textMuted }}>{cfg.label}</span>
                </button>
              );
            })}
          </div>
        )}

        {chartSnaps.length < 2 ? (
          <div style={{ padding: '24px 16px', textAlign: 'center' }}>
            <span style={{ fontSize: 13, color: t.textMuted }}>
              Not enough data for this period. Try a longer range or run more scans.
            </span>
          </div>
        ) : (
          <>
            <AreaChart
              data={chartSnaps}
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
