import { useState, useRef, useEffect, useCallback } from 'react';
import { useQuery, useQueries } from '@tanstack/react-query';
import { fetchTrend, fetchTrendServices, fetchTrendResourceTypes } from '../api/client';
import { serviceConfig, resourceTypeConfig } from '../components/serviceConfig';
import AccountSelector from '../components/AccountSelector';
import AreaChart from '../components/AreaChart';
import DateRangeChips, { DEFAULT_DAYS } from '../components/DateRangeChips';
import { useToast } from '../context/ToastContext';
import { useWindowWidth } from '../components/primitives';
import { useBreakpoint } from '../components/primitives/useBreakpoint';
import { Spinner } from '../components/primitives';
import { csvEncode, downloadCSV } from '../utils/csv';

const CHART_HEIGHT = 200;
const MARGIN = { top: 16, right: 20, bottom: 32, left: 56 };

// Max rows to render in scan history list before paginating
const LIST_PAGE_SIZE = 50;

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
    const avgZombies = Math.round(group.reduce((sum, s) => sum + s.zombie_count, 0) / group.length);
    return {
      ...latest,
      total_monthly_cost: Math.round(avgCost * 100) / 100,
      zombie_count: avgZombies,
      _scanCount: group.length,
    };
  });
}

// Group snapshots by calendar month — sum costs, sum zombie counts.
function downsampleByMonth(snaps) {
  if (!snaps || snaps.length === 0) return [];
  const buckets = new Map();
  for (const s of snaps) {
    const key = new Date(s.snapshot_at).toISOString().slice(0, 7);
    if (!buckets.has(key)) buckets.set(key, []);
    buckets.get(key).push(s);
  }
  return [...buckets.values()].map(group => {
    const latest = group[group.length - 1];
    const avgCost = group.reduce((sum, s) => sum + s.total_monthly_cost, 0) / group.length;
    const avgZombies = Math.round(group.reduce((sum, s) => sum + s.zombie_count, 0) / group.length);
    return {
      ...latest,
      total_monthly_cost: Math.round(avgCost * 100) / 100,
      zombie_count: avgZombies,
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
// snapshot_at. Works because all buckets for a single organization share the same
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
        existing.zombie_count       += snap.zombie_count;
      }
    }
  }
  return [...byTimestamp.values()].sort((a, b) => a.snapshot_at.localeCompare(b.snapshot_at));
}

// ─── CSV export ──────────────────────────────────────────────────────────────

// Walks per-bucket data (not the merged sum) so each row self-describes its
// service / resource_type filter. When multiple services are selected, rows
// are emitted per service per timestamp instead of being collapsed to a sum.
function exportCSV(seriesByBucket, { periodDays }, toast) {
  const services      = [...new Set(seriesByBucket.map(b => b.service).filter(Boolean))];
  const resourceTypes = [...new Set(seriesByBucket.map(b => b.resourceType).filter(Boolean))];

  const slugParts = [];
  if (services.length)      slugParts.push(services.map(s => s.replace(/^Amazon|^AWS/, '').toLowerCase()).join('-'));
  if (resourceTypes.length) slugParts.push(resourceTypes.map(rt => rt.toLowerCase()).join('-'));
  const filterSlug = slugParts.length ? `-${slugParts.join('-')}` : '';
  const filename = `axiaops-trend${filterSlug}-${periodDays}d-${new Date().toISOString().split('T')[0]}.csv`;

  const headers = ['snapshot_at', 'service', 'resource_type', 'account_id', 'zombie_count', 'total_monthly_cost', 'currency'];
  const rows = [];
  for (const bucket of seriesByBucket) {
    for (const s of bucket.snaps.slice(-periodDays)) {
      rows.push([
        s.snapshot_at,
        bucket.service ?? '',
        bucket.resourceType ?? '',
        s.account_id,
        s.zombie_count,
        s.total_monthly_cost.toFixed(2),
        s.currency,
      ]);
    }
  }
  rows.sort((a, b) =>
    a[0].localeCompare(b[0]) || a[1].localeCompare(b[1]) || a[2].localeCompare(b[2])
  );

  downloadCSV(csvEncode(headers, rows), filename);

  toast(`Exported ${rows.length} row${rows.length !== 1 ? 's' : ''} to CSV`, 'success');
}

// ─── Scan history list row ────────────────────────────────────────────────────
function HistoryRow({ item, prevItem, isSelected, onClick }) {
  const costDelta = prevItem ? item.total_monthly_cost - prevItem.total_monthly_cost : null;
  return (
    <div
      data-snap=""
      onClick={onClick}
      style={{
        display: 'flex',
        alignItems: 'center',
        gap: 12,
        padding: '12px 14px',
        backgroundColor: isSelected ? 'var(--color-surface-raised)' : 'var(--color-surface)',
        border: `1px solid ${isSelected ? 'var(--color-accent)' : 'var(--color-border)'}`,
        borderRadius: 8,
        cursor: 'pointer',
      }}
    >
      <div style={{ flex: 1, minWidth: 0 }}>
        <div style={{ fontSize: 13, fontWeight: 600, color: 'var(--color-text)' }}>
          {formatDateTime(item.snapshot_at)}
        </div>
        <div style={{ fontSize: 11, color: 'var(--color-text-muted)', marginTop: 2 }}>
          {item.zombie_count === 0 ? 'No zombies found' : `${item.zombie_count} zombie${item.zombie_count !== 1 ? 's' : ''}`}
          {costDelta !== null && Math.abs(costDelta) >= 0.01 && (
            <span style={{ marginLeft: 8, color: costDelta > 0 ? 'var(--color-error)' : 'var(--color-success)', fontWeight: 600 }}>
              {costDelta > 0 ? '+' : ''}{costDelta.toFixed(2)}
            </span>
          )}
        </div>
      </div>
      <div style={{ fontSize: 14, fontWeight: 700, color: 'var(--color-accent)', textAlign: 'right', flexShrink: 0 }}>
        {item.currency} {item.total_monthly_cost.toFixed(2)}
      </div>
    </div>
  );
}

// ─── Main screen ──────────────────────────────────────────────────────────────
export default function TrendScreen({ accounts, selectedAccount, selectedAwsAccount, onSelectAccount, onConnectAccount, onEditAccount }) {
  const { toast } = useToast();
  const screenWidth = useWindowWidth();
  const { isAtMost } = useBreakpoint();
  const isMobile = isAtMost('xs');
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
  const [period, setPeriod]             = useState(DEFAULT_DAYS);
  const [granularity, setGranularity]   = useState('daily');
  const [listPage, setListPage]         = useState(1);
  const [showScrollTop, setShowScrollTop] = useState(false);
  const listRef      = useRef(null);
  const topRef       = useRef(null);
  const loadMoreRef  = useRef(null);

  // Show "scroll to top" button when page is scrolled past the chart
  useEffect(() => {
    function onScroll() { setShowScrollTop(window.scrollY > 350); }
    window.addEventListener('scroll', onScroll, { passive: true });
    return () => window.removeEventListener('scroll', onScroll);
  }, []);

  // Auto-select granularity when period changes
  const effectiveGranularity = period <= 30 ? 'daily' : granularity;
  const showGranularityToggle = period >= 90;

  // Derive data — raw snaps for history list, aggregated for chart
  const allSnaps      = mergedSnaps;
  const filteredSnaps = allSnaps.slice(-period);
  const chartSnaps    = effectiveGranularity === 'monthly'
    ? downsampleByMonth(filteredSnaps)
    : downsample(filteredSnaps, period);
  const reversedSnaps = [...filteredSnaps].reverse();

  // Total CSV rows across all buckets (one row per snap per bucket, period-windowed).
  const exportRowCount = filterBuckets.reduce(
    (sum, _, i) => sum + Math.min(trendQueries[i].data?.length ?? 0, period),
    0,
  );

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
      <div style={{ display: 'flex', justifyContent: 'center', alignItems: 'center', minHeight: '60vh', backgroundColor: 'var(--color-bg)', flexDirection: 'column', gap: 14 }}>
        <Spinner size={32} color={'var(--color-accent)'} />
        <span style={{ color: 'var(--color-text-muted)', fontSize: 14 }}>Loading trend data…</span>
      </div>
    );
  }

  if (trendIsError || !trendHasData) {
    return (
      <div style={{ display: 'flex', justifyContent: 'center', alignItems: 'center', minHeight: '60vh', backgroundColor: 'var(--color-bg)', flexDirection: 'column', gap: 16 }}>
        <span style={{ color: 'var(--color-text-mid)', fontSize: 16 }}>Failed to load trend data.</span>
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
    <div ref={topRef} style={{ backgroundColor: 'var(--color-bg)', minHeight: '100%' }}>

      {/* Account selector header */}
      <div style={{ backgroundColor: 'var(--color-surface)', borderBottom: `1px solid var(--color-border)`, padding: '16px' }}>
        <AccountSelector
          accounts={accounts}
          selectedAccount={selectedAccount}
          onSelectAccount={onSelectAccount}
          onConnectAccount={onConnectAccount}
          onEditAccount={onEditAccount}
        />
      </div>

      {/* Page header */}
      <div style={{ backgroundColor: 'var(--color-surface-alt)', borderBottom: `1px solid var(--color-border)`, padding: '20px 20px 16px' }}>
        <span style={{ fontSize: 11, fontWeight: 600, color: 'var(--color-text-muted)', letterSpacing: 1.2, textTransform: 'uppercase', display: 'block', marginBottom: 4 }}>
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
        <span style={{ fontSize: 32, fontWeight: 800, color: 'var(--color-accent)', letterSpacing: -0.5, display: 'block' }}>
          {displaySnap?.currency ?? '$'} {displaySnap ? displaySnap.total_monthly_cost.toFixed(2) : '0.00'}
        </span>
        {/* Honesty label — the number above is captured at scan time, BEFORE the
            user's dismissals/snoozes are applied. The Overview's Monthly Waste
            headline is the live current-state total AFTER dismissals; the two
            can legitimately differ. Only show when looking at the most-recent
            snapshot — if the user picked a past snapshot from the history list,
            they already understand it's historical. */}
        {displaySnap && !selectedSnap && (
          <span style={{ fontSize: 11, color: 'var(--color-text-muted)', fontStyle: 'italic', display: 'block', marginTop: 1 }}>
            At last scan · before dismissals
          </span>
        )}
        <div style={{ display: 'flex', alignItems: 'center', gap: 12, marginTop: 4, flexWrap: 'wrap' }}>
          <span style={{ fontSize: 13, color: 'var(--color-text-mid)' }}>
            {displaySnap
              ? `${displaySnap.zombie_count} zombie resource${displaySnap.zombie_count !== 1 ? 's' : ''}`
              : 'No data'}
          </span>
          {delta !== null && (
            <span style={{ fontSize: 12, fontWeight: 700, color: delta > 0 ? 'var(--color-error)' : 'var(--color-success)' }}>
              {delta > 0 ? '▲' : '▼'} {Math.abs(delta).toFixed(1)}% over period
            </span>
          )}
        </div>
      </div>

      {/* Chart section */}
      <div style={{ backgroundColor: 'var(--color-bg)', borderBottom: `1px solid var(--color-border)`, paddingTop: 16, paddingBottom: 16 }}>
        <div style={{ display: 'flex', alignItems: 'center', justifyContent: 'space-between', padding: '0 20px 12px', flexWrap: 'wrap', gap: 8 }}>
          <div style={{ display: 'flex', alignItems: 'center', gap: 10 }}>
            <span style={{ fontSize: 12, fontWeight: 600, color: 'var(--color-text-muted)', textTransform: 'uppercase', letterSpacing: 0.5 }}>
              Waste Over Time
            </span>
            {showGranularityToggle && (
              <div style={{ display: 'flex', gap: 2, backgroundColor: 'var(--color-surface-raised)', borderRadius: 6, padding: 2 }}>
                {['daily', 'monthly'].map(g => (
                  <button
                    key={g}
                    onClick={() => { setGranularity(g); setSelectedSnap(null); }}
                    style={{
                      // Bumped from 3/8 padding so the segmented toggle clears
                      // a finger-pad on touch input. The visual size grows by
                      // ~6px each — fine on desktop, important on phones.
                      padding: '6px 12px', borderRadius: 4, border: 'none', cursor: 'pointer',
                      backgroundColor: effectiveGranularity === g ? 'var(--color-accent)' : 'transparent',
                      color: effectiveGranularity === g ? '#fff' : 'var(--color-text-muted)',
                      fontSize: 12, fontWeight: 600, textTransform: 'capitalize',
                    }}
                  >
                    {g}
                  </button>
                ))}
              </div>
            )}
          </div>
          <DateRangeChips value={period} onChange={changePeriod} mobile={isMobile} />
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
                padding: isMobile ? '8px 14px' : '4px 10px', borderRadius: 20, cursor: 'pointer', flexShrink: 0,
                backgroundColor: filterServices.size === 0 ? 'var(--color-accent)' : 'var(--color-surface-raised)',
                border: `1px solid ${filterServices.size === 0 ? 'var(--color-accent)' : 'var(--color-border)'}`,
                fontSize: 12, fontWeight: 700,
                color: filterServices.size === 0 ? '#fff' : 'var(--color-text-mid)',
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
                    padding: isMobile ? '8px 14px' : '4px 10px', borderRadius: 20, cursor: 'pointer', flexShrink: 0,
                    backgroundColor: active ? 'var(--color-accent)' : 'var(--color-surface-raised)',
                    border: `1px solid ${active ? 'var(--color-accent)' : 'var(--color-border)'}`,
                  }}
                >
                  <div style={{ width: 6, height: 6, borderRadius: '50%', backgroundColor: active ? '#fff' : cfg.color }} />
                  <span style={{ fontSize: 12, fontWeight: 700, color: active ? '#fff' : 'var(--color-text-mid)' }}>{cfg.label}</span>
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
                padding: isMobile ? '7px 12px' : '3px 8px', borderRadius: 14, cursor: 'pointer', flexShrink: 0,
                backgroundColor: filterResourceTypes.size === 0 ? 'var(--color-text-mid)' : 'var(--color-surface-raised)',
                border: `1px solid ${filterResourceTypes.size === 0 ? 'var(--color-text-mid)' : 'var(--color-border)'}`,
                fontSize: 11, fontWeight: 600,
                color: filterResourceTypes.size === 0 ? '#fff' : 'var(--color-text-muted)',
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
                    padding: isMobile ? '7px 12px' : '3px 8px', borderRadius: 14, cursor: 'pointer', flexShrink: 0,
                    backgroundColor: active ? 'var(--color-text-mid)' : 'var(--color-surface-raised)',
                    border: `1px solid ${active ? 'var(--color-text-mid)' : 'var(--color-border)'}`,
                  }}
                >
                  <div style={{ width: 5, height: 5, borderRadius: '50%', backgroundColor: active ? '#fff' : cfg.color }} />
                  <span style={{ fontSize: 11, fontWeight: 600, color: active ? '#fff' : 'var(--color-text-muted)' }}>{cfg.label}</span>
                </button>
              );
            })}
          </div>
        )}

        {chartSnaps.length < 2 ? (
          <div style={{ padding: '24px 16px', textAlign: 'center' }}>
            <span style={{ fontSize: 13, color: 'var(--color-text-muted)' }}>
              Not enough data for this period. Try a longer range or run more scans.
            </span>
          </div>
        ) : (
          <>
            <AreaChart
              data={chartSnaps}
              selectedId={selectedSnap?.snapshot_at}
              onSelect={handleSelect}
              screenWidth={screenWidth}
            />

            <div style={{ padding: '8px 16px 0', textAlign: 'center' }}>
              <span style={{ fontSize: 11, color: 'var(--color-text-muted)' }}>
                {filteredSnaps.length} scan{filteredSnaps.length !== 1 ? 's' : ''}
                {chartSnaps.length < filteredSnaps.length && ` · ${chartSnaps.length} points (averaged)`}
              </span>
            </div>
          </>
        )}
      </div>

      {/* Scan history list */}
      <div ref={listRef}>
        <div style={{ display: 'flex', alignItems: 'center', padding: '16px 16px 0' }}>
          <span style={{ flex: 1, fontSize: 11, fontWeight: 700, color: 'var(--color-text-muted)', letterSpacing: 1.2, textTransform: 'uppercase' }}>
            Scan History · {filteredSnaps.length}
          </span>
          <button
            onClick={() => {
              const seriesByBucket = filterBuckets.map((b, i) => ({
                service:      b.service,
                resourceType: b.resourceType,
                snaps:        Array.isArray(trendQueries[i].data) ? trendQueries[i].data : [],
              }));
              exportCSV(seriesByBucket, { periodDays: period }, toast);
            }}
            disabled={exportRowCount === 0}
            aria-label="Export to CSV"
            style={{
              padding: isMobile ? '8px 12px' : '4px 10px',
              borderRadius: 6,
              border: `1px solid var(--color-border)`,
              backgroundColor: 'var(--color-surface-raised)',
              cursor: exportRowCount === 0 ? 'not-allowed' : 'pointer',
              opacity: exportRowCount === 0 ? 0.5 : 1,
            }}
          >
            <span style={{ fontSize: 11, fontWeight: 700, color: 'var(--color-text-mid)' }}>↓ CSV</span>
          </button>
        </div>

        <div style={{ display: 'flex', flexDirection: 'column', gap: 8, padding: '8px 16px 48px' }}>
          {visibleRows.map((item, idx) => (
            <HistoryRow
              key={item.snapshot_at + idx}
              item={item}
              prevItem={reversedSnaps[idx + 1]}
              isSelected={selectedSnap?.snapshot_at === item.snapshot_at}
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
                padding: '12px 14px',
                backgroundColor: 'var(--color-surface)',
                border: `1px solid var(--color-border)`,
                borderRadius: 8,
                cursor: 'pointer',
                color: 'var(--color-accent)',
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
            backgroundColor: 'var(--color-surface)',
            border: `1px solid var(--color-border)`,
            boxShadow: '0 4px 12px rgba(0,0,0,0.15)',
            cursor: 'pointer',
            display: 'flex',
            alignItems: 'center',
            justifyContent: 'center',
            zIndex: 100,
          }}
        >
          <svg width="16" height="16" viewBox="0 0 24 24" fill="none"
            stroke={'var(--color-accent)'} strokeWidth="2.5" strokeLinecap="round" strokeLinejoin="round">
            <polyline points="18 15 12 9 6 15" />
          </svg>
        </button>
      )}
    </div>
  );
}
