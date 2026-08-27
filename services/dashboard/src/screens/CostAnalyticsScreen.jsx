import { useState, useMemo, useEffect } from 'react';
import { useQuery, useQueryClient, useMutation } from '@tanstack/react-query';
import { fetchCosts, fetchZombies, fetchAccounts, scanAccount } from '../api/client';
import { serviceConfig, resourceTypeConfig, resourceTypeFromId } from '../components/serviceConfig';
import AccountSelector from '../components/AccountSelector';
import AreaChart from '../components/AreaChart';
import DateRangeChips, { PRESET_OPTIONS, DEFAULT_DAYS } from '../components/DateRangeChips';
import { useToast } from '../context/ToastContext';
import { useScanStatus } from '../hooks/useScanStatus';
import { Spinner } from '../components/primitives';
import { useWindowWidth } from '../components/primitives';
import { useBreakpoint } from '../components/primitives/useBreakpoint';
import { MobileSheet } from '../components/primitives/MobileSheet';
import { csvEncode, downloadCSV } from '../utils/csv';

// AWS Cost Explorer's GetCostAndUsageWithResources tops out at 14 days of
// resource-level history. The drill-down panel is clamped to this window
// so its service total reconciles with its per-resource breakdown — see
// the comment beside `panelWindowDays` in the component body.
const PANEL_MAX_DAYS = 14;

function formatCost(val) {
  if (val >= 1000) return `${(val / 1000).toFixed(2)}k`;
  if (val >= 1) return val.toFixed(2);
  return val.toFixed(6);
}

function formatDateShort(iso) {
  return new Date(iso).toLocaleDateString('en-GB', { day: 'numeric', month: 'short' });
}

// ─── CSV export ──────────────────────────────────────────────────────────────

// Render an amount for the CSV at full fidelity. The DB stores NUMERIC and the
// API ships the raw float, so the only place precision was being lost was the
// old `toFixed(2)` here — which collapsed sub-cent services (S3, CloudWatch,
// DynamoDB, …) to "0.00" and broke reconciliation against AWS CUR / Vantage.
// We keep up to 10 decimals (CUR-grade), strip float-representation noise via
// the fixed-point round-trip, and trim trailing zeros so whole-cent values
// still read cleanly (0.19, not 0.1900000000). Fixed-point avoids scientific
// notation that would confuse spreadsheet imports. On-screen display stays at
// 2dp — this is the data-interchange path only.
function csvAmount(n) {
  if (typeof n !== 'number' || !isFinite(n)) return '';
  return n.toFixed(10).replace(/\.?0+$/, '');
}

function exportCSV(records, { services, resourceTypes = [] }, toast) {
  const slugParts = services.map(s => s.replace(/^Amazon|^AWS/, '').toLowerCase());
  if (resourceTypes.length) slugParts.push(resourceTypes.map(rt => rt.toLowerCase()).join('-'));
  const filterSlug = slugParts.length ? '-' + slugParts.join('-') : '';

  const headers = ['service', 'region', 'amount', 'currency', 'period_start', 'period_end', 'resource_id'];
  const rows = records.map(r => [
    r.service,
    r.region,
    csvAmount(r.amount),
    r.currency,
    new Date(r.period_start).toISOString().split('T')[0],
    new Date(r.period_end).toISOString().split('T')[0],
    r.resource_id,
  ]);

  // Name the file after the actual data window (min/max period_start) rather
  // than the requested lookback + today's date. A trailing window that ends on
  // an unsettled day, or a custom calendar range, then labels itself honestly
  // instead of advertising coverage the rows don't have.
  const dayStamps = rows.map(r => r[4]).sort();
  const windowSlug = dayStamps.length
    ? `${dayStamps[0]}_${dayStamps[dayStamps.length - 1]}`
    : 'empty';
  const filename = `axiaops-costs${filterSlug}-${windowSlug}.csv`;

  downloadCSV(csvEncode(headers, rows), filename);

  toast(`Exported ${records.length} cost record${records.length !== 1 ? 's' : ''} to CSV`, 'success');
}

export default function CostAnalyticsScreen({ accounts: passedAccounts, selectedAccount: passedSelectedAccount, onSelectAccount, connectHref, editAccountHref }) {
  const { toast }     = useToast();
  const { watch }     = useScanStatus();
  const queryClient   = useQueryClient();
  const screenWidth = useWindowWidth();
  const { isAtMost } = useBreakpoint();
  const isMobile = isAtMost('sm');
  const [period, setPeriod] = useState(DEFAULT_DAYS);
  // Absolute calendar window from the Custom… picker ({ sinceIso, untilIso }),
  // or null for the trailing `period`-day window. Presets clear it back to null.
  const [customRange, setCustomRange] = useState(null);
  const [granularity, setGranularity] = useState('daily'); // 'daily' | 'monthly'
  const [filterServices, setFilterServices] = useState(() => new Set());
  const [filterResourceTypes, setFilterResourceTypes] = useState(new Set());
  const [sortBy, setSortBy] = useState('cost_desc');
  const [selectedService, setSelectedService] = useState(null);
  const [selectedChartDate, setSelectedChartDate] = useState(null);

  // Use passed accounts or fetch if not provided
  const accountsQuery = useQuery({ queryKey: ['accounts'], queryFn: fetchAccounts, enabled: !passedAccounts?.length });
  const accounts = passedAccounts?.length > 0 ? passedAccounts : (accountsQuery.data ?? []);
  const selectedAccount = passedSelectedAccount;

  // Fetch costs and trends
  // Cost records now filter by internal_account_id directly (no resolution needed)
  const costsQuery = useQuery({
    queryKey: ['costs', selectedAccount, period, customRange],
    queryFn: () => fetchCosts({ accountId: selectedAccount, period, sinceIso: customRange?.sinceIso, untilIso: customRange?.untilIso }),
    staleTime: 60_000,
  });

  const zombiesQuery = useQuery({
    queryKey: ['zombies', selectedAccount],
    queryFn: () => fetchZombies({ accountId: selectedAccount }),
    staleTime: 60_000,
  });

  const zombieResourceIds = useMemo(() => {
    if (!zombiesQuery.data) return new Set();
    const ids = new Set();
    for (const z of zombiesQuery.data) {
      if (z.resource_id) ids.add(z.resource_id);
    }
    return ids;
  }, [zombiesQuery.data]);

  // Derive distinct services from costs
  const allServices = useMemo(() => {
    if (!costsQuery.data) return [];
    const services = new Set();
    for (const record of costsQuery.data) {
      if (record.service) services.add(record.service);
    }
    return Array.from(services).sort();
  }, [costsQuery.data]);

  // Resource-type sub-filter — only meaningful under a single service,
  // mirroring the trend screen's two-tier filter. Types are derived from
  // resource ID prefixes (resourceTypeFromId), so only services with
  // resource-level cost data and recognizable prefixes grow the second row.
  const singleService = filterServices.size === 1 ? [...filterServices][0] : null;

  const availableResourceTypes = useMemo(() => {
    if (!costsQuery.data || !singleService) return [];
    const types = new Set();
    for (const r of costsQuery.data) {
      if (r.service !== singleService) continue;
      const rt = resourceTypeFromId(r.resource_id);
      if (rt) types.add(rt);
    }
    return [...types].sort();
  }, [costsQuery.data, singleService]);

  // Filter records client-side by selected services, then by resource type.
  // An active type filter keeps only records whose ID derives to a selected
  // type — records without resource-level data (no resource_id) drop out,
  // which is why the chip row carries the 14-day resource-data caveat.
  const filteredCosts = useMemo(() => {
    if (!costsQuery.data) return [];
    let records = costsQuery.data;
    if (filterServices.size > 0) {
      records = records.filter(r => filterServices.has(r.service));
    }
    if (singleService && filterResourceTypes.size > 0) {
      records = records.filter(r => filterResourceTypes.has(resourceTypeFromId(r.resource_id)));
    }
    return records;
  }, [costsQuery.data, filterServices, singleService, filterResourceTypes]);

  // Calculate totals
  const totalCost = useMemo(() => {
    return filteredCosts.reduce((sum, r) => sum + (r.amount || 0), 0);
  }, [filteredCosts]);

  // Auto-select granularity when period changes
  const effectiveGranularity = period <= 30 ? 'daily' : granularity;
  const showGranularityToggle = period >= 90;

  // Aggregate cost records for the chart — daily or monthly.
  const costChartData = useMemo(() => {
    if (filteredCosts.length === 0) return [];
    const byKey = new Map();
    for (const r of filteredCosts) {
      // period_start is a UTC RFC3339 string (Z-suffixed) from the Go API, so
      // a raw slice is equivalent to parse→toISOString and skips the per-record
      // Date allocation.
      const key = effectiveGranularity === 'monthly'
        ? r.period_start.slice(0, 7)
        : r.period_start.slice(0, 10);
      const entry = byKey.get(key);
      if (entry) {
        entry.total_monthly_cost += r.amount || 0;
      } else {
        byKey.set(key, {
          snapshot_at: r.period_start,
          total_monthly_cost: r.amount || 0,
          currency: r.currency || 'USD',
        });
      }
    }
    return [...byKey.values()]
      .sort((a, b) => a.snapshot_at.localeCompare(b.snapshot_at));
  }, [filteredCosts, effectiveGranularity]);

  // Aggregate cost records by service for the table view.
  const serviceGroups = useMemo(() => {
    if (filteredCosts.length === 0) return [];
    const byService = new Map();
    for (const r of filteredCosts) {
      const g = byService.get(r.service);
      if (g) {
        g.total += r.amount || 0;
        g.count += 1;
        g.regions.add(r.region || 'NoRegion');
        if (r.period_start < g.periodStart) g.periodStart = r.period_start;
        if (r.period_end   > g.periodEnd)   g.periodEnd   = r.period_end;
      } else {
        byService.set(r.service, {
          service: r.service,
          total: r.amount || 0,
          count: 1,
          regions: new Set([r.region || 'NoRegion']),
          periodStart: r.period_start,
          periodEnd: r.period_end,
          currency: r.currency || 'USD',
        });
      }
    }
    const list = [...byService.values()].map(g => ({ ...g, regions: [...g.regions].sort() }));
    if (sortBy === 'cost_asc') return list.sort((a, b) => a.total - b.total);
    if (sortBy === 'name_asc') return list.sort((a, b) => serviceConfig(a.service).label.localeCompare(serviceConfig(b.service).label));
    if (sortBy === 'name_desc') return list.sort((a, b) => serviceConfig(b.service).label.localeCompare(serviceConfig(a.service).label));
    return list.sort((a, b) => b.total - a.total);
  }, [filteredCosts, sortBy]);

  // The drill-down side panel is clamped to a maximum 14-day window
  // (PANEL_MAX_DAYS at module scope) to match AWS Cost Explorer's
  // resource-level data ceiling — without it, the panel's service total
  // spans the user's selected period (up to a year) while the resource
  // breakdown only spans the last 14 days, and the two numbers don't
  // reconcile so resource rows look like they're "missing" most of the cost.
  const panelWindowDays = Math.min(period, PANEL_MAX_DAYS);
  const panelClamped = period > PANEL_MAX_DAYS;

  // Records for the selected service within the panel's clamped window.
  // Drives both the panel summary (total / period / regions / count) and
  // the per-resource breakdown so the two always reconcile.
  const panelServiceRecords = useMemo(() => {
    if (!selectedService) return [];
    const cutoff = Date.now() - panelWindowDays * 24 * 60 * 60 * 1000;
    return filteredCosts.filter(r => {
      if (r.service !== selectedService) return false;
      return new Date(r.period_start).getTime() >= cutoff;
    });
  }, [selectedService, filteredCosts, panelWindowDays]);

  // Aggregate the panel records: total, count, regions, period bounds.
  const panelStats = useMemo(() => {
    if (!selectedService || panelServiceRecords.length === 0) return null;
    let total = 0;
    let periodStart = panelServiceRecords[0].period_start;
    let periodEnd   = panelServiceRecords[0].period_end;
    const regions = new Set();
    for (const r of panelServiceRecords) {
      total += r.amount || 0;
      if (r.period_start < periodStart) periodStart = r.period_start;
      if (r.period_end   > periodEnd)   periodEnd   = r.period_end;
      regions.add(r.region || 'NoRegion');
    }
    return {
      service: selectedService,
      total,
      count: panelServiceRecords.length,
      regions: [...regions].sort(),
      periodStart,
      periodEnd,
      currency: panelServiceRecords[0].currency || 'USD',
    };
  }, [selectedService, panelServiceRecords]);

  // Per-resource_id breakdown for the side panel — also scoped to the clamped window.
  const selectedServiceBreakdown = useMemo(() => {
    if (!selectedService) return null;
    const byResource = new Map();
    for (const r of panelServiceRecords) {
      const key = r.resource_id || '__none__';
      const e = byResource.get(key);
      if (e) {
        e.total += r.amount || 0;
        e.count += 1;
        e.regions.add(r.region || 'NoRegion');
      } else {
        byResource.set(key, {
          resourceId: r.resource_id || null,
          total: r.amount || 0,
          count: 1,
          regions: new Set([r.region || 'NoRegion']),
        });
      }
    }
    return [...byResource.values()]
      .map(e => ({ ...e, regions: [...e.regions].sort() }))
      .sort((a, b) => b.total - a.total);
  }, [selectedService, panelServiceRecords]);

  // Scan account
  const scanMutation = useMutation({
    mutationFn: scanAccount,
    onMutate: async (accountId) => {
      await queryClient.cancelQueries({ queryKey: ['accounts'] });
      const previous = queryClient.getQueryData(['accounts']);
      const label    = previous?.find(a => a.id === accountId)?.label;
      const display  = label ?? accountId.slice(0, 8);

      queryClient.setQueryData(['accounts'], (accs = []) =>
        accs.map(a => a.id === accountId ? { ...a, status: 'scanning' } : a),
      );
      toast(`Starting scan for ${display}…`, 'info');
      return { previous, label, display };
    },
    onError: (err, accountId, ctx) => {
      if (err?.code === 'already_scanning') {
        toast(`Scan already running for ${ctx?.display ?? 'account'}`, 'info');
        watch(accountId, { label: ctx?.label });
        return;
      }
      if (ctx?.previous) queryClient.setQueryData(['accounts'], ctx.previous);
      toast(`Couldn't start scan for ${ctx?.display ?? 'account'}`, 'error');
    },
    onSuccess: (_data, accountId, ctx) => {
      watch(accountId, { label: ctx?.label });
    },
  });

  const handleScanAccount = (accountId) => scanMutation.mutate(accountId);

  // Reset the drill-down panel AND the service filter when the account
  // changes — a service selected on the old account may not exist on the new
  // one (its chip wouldn't even render, leaving an empty chart with no visible
  // active filter), and even when it does by name the data behind it has
  // changed.
  useEffect(() => {
    setSelectedService(null);
    setFilterServices(new Set());
    setFilterResourceTypes(new Set());
  }, [selectedAccount]);

  // Toggle service filter. Only close the drill-down panel if the
  // toggle actually hides the currently-selected service.
  const toggleServiceFilter = (svc) => {
    const next = new Set(filterServices);
    next.has(svc) ? next.delete(svc) : next.add(svc);
    setFilterServices(next);
    // Sub-types only make sense under a single service; clear them otherwise.
    if (next.size !== 1) setFilterResourceTypes(new Set());
    if (selectedService && next.size > 0 && !next.has(selectedService)) {
      setSelectedService(null);
    }
  };

  const clearServiceFilter = () => {
    setFilterServices(new Set());
    setFilterResourceTypes(new Set());
  };

  const toggleResourceTypeFilter = (rt) => {
    setFilterResourceTypes(prev => {
      const next = new Set(prev);
      next.has(rt) ? next.delete(rt) : next.add(rt);
      return next;
    });
  };

  const clearResourceTypeFilter = () => {
    setFilterResourceTypes(new Set());
  };

  const records = filteredCosts;
  const costsIsLoading = costsQuery.isLoading;


  // Loading state
  if (costsIsLoading) {
    return (
      <div style={{ display: 'flex', justifyContent: 'center', alignItems: 'center', minHeight: '60vh', backgroundColor: 'var(--color-bg)', flexDirection: 'column', gap: 14 }}>
        <Spinner />
        <span style={{ fontSize: 14, color: 'var(--color-text-muted)' }}>Loading cost analytics…</span>
      </div>
    );
  }

  // Error state
  if (costsQuery.isError) {
    return (
      <div style={{ display: 'flex', justifyContent: 'center', alignItems: 'center', minHeight: '60vh', backgroundColor: 'var(--color-bg)', flexDirection: 'column', gap: 14 }}>
        <span style={{ fontSize: 14, color: 'var(--color-error)' }}>Failed to load cost analytics</span>
      </div>
    );
  }

  return (
    <div style={{ backgroundColor: 'var(--color-bg)', minHeight: '100vh' }}>
      {/* Header */}
      <div style={{ backgroundColor: 'var(--color-surface)', borderBottom: `1px solid var(--color-border)`, padding: '16px' }}>
        <AccountSelector
          accounts={accounts}
          selectedAccount={selectedAccount}
          onSelectAccount={onSelectAccount}
          connectHref={connectHref}
          editAccountHref={editAccountHref}
          onScanAccount={handleScanAccount}
        />
      </div>

      {/* Total cost hero */}
      <div style={{ backgroundColor: 'var(--color-surface-alt)', borderBottom: '1px solid var(--color-border)', padding: '20px 20px 16px' }}>
        <span style={{ fontSize: 11, fontWeight: 600, color: 'var(--color-text-muted)', letterSpacing: 1.2, textTransform: 'uppercase', display: 'block', marginBottom: 4 }}>
          Total Spend · {PRESET_OPTIONS.find(p => p.days === period)?.label || `${period}d`}
        </span>
        <span style={{ fontSize: 32, fontWeight: 800, color: 'var(--color-accent)', letterSpacing: -0.5, display: 'block' }}>
          ${totalCost.toFixed(2)}
        </span>
        <span style={{ fontSize: 13, color: 'var(--color-text-mid)', marginTop: 4, display: 'block' }}>
          {records.length} record{records.length !== 1 ? 's' : ''} across {allServices.length} service{allServices.length !== 1 ? 's' : ''}
        </span>
        <span style={{ fontSize: 11, color: 'var(--color-text-muted)', marginTop: 2, display: 'block' }}>
          Net amortized cost · post-credits, RI/SP amortized
        </span>
      </div>

      {/* Cost chart section */}
      <div style={{ backgroundColor: 'var(--color-bg)', borderBottom: `1px solid var(--color-border)` }}>
        <div style={{ display: 'flex', alignItems: 'center', justifyContent: 'space-between', padding: '16px 20px 0', flexWrap: 'wrap', gap: 8 }}>
          <div style={{ display: 'flex', alignItems: 'center', gap: 10 }}>
            <span style={{ fontSize: 12, fontWeight: 600, color: 'var(--color-text-muted)', textTransform: 'uppercase', letterSpacing: 0.5 }}>
              {effectiveGranularity === 'monthly' ? 'Monthly Spend' : 'Daily Spend'}
            </span>
            {showGranularityToggle && (
              <div style={{ display: 'flex', gap: 2, backgroundColor: 'var(--color-surface-raised)', borderRadius: 6, padding: 2 }}>
                {['daily', 'monthly'].map(g => (
                  <button
                    key={g}
                    onClick={() => { setGranularity(g); setSelectedChartDate(null); }}
                    style={{
                      padding: isMobile ? '6px 12px' : '3px 8px', borderRadius: 4, border: 'none', cursor: 'pointer',
                      backgroundColor: effectiveGranularity === g ? 'var(--color-accent)' : 'transparent',
                      color: effectiveGranularity === g ? 'var(--color-text-on-dark)' : 'var(--color-text-muted)',
                      fontSize: isMobile ? 12 : 11, fontWeight: 600, textTransform: 'capitalize',
                    }}
                  >
                    {g}
                  </button>
                ))}
              </div>
            )}
          </div>
          <DateRangeChips
            value={period}
            onChange={(days, range) => {
              setPeriod(days);
              // Presets call onChange(days) with no range → trailing window.
              // Custom… passes { sinceIso, untilIso } → absolute window.
              setCustomRange(range ?? null);
              setSelectedService(null);
              setSelectedChartDate(null);
              setFilterResourceTypes(new Set());
            }}
            mobile={isMobile}
          />
        </div>

        {/* Service filter pills */}
        {allServices.length > 0 && (
          <div style={{ display: 'flex', gap: 6, padding: '8px 16px 16px', overflowX: 'auto' }}>
            <button
              onClick={clearServiceFilter}
              style={{
                padding: '4px 10px',
                borderRadius: 20,
                border: `1px solid ${filterServices.size === 0 ? 'var(--color-accent)' : 'var(--color-border)'}`,
                backgroundColor: filterServices.size === 0 ? 'var(--color-accent)' : 'var(--color-surface-raised)',
                color: filterServices.size === 0 ? 'var(--color-text-on-dark)' : 'var(--color-text-mid)',
                fontWeight: 700,
                fontSize: 12,
                cursor: 'pointer',
                whiteSpace: 'nowrap',
                flexShrink: 0,
              }}
            >
              All Services
            </button>
            {allServices.map(svc => {
              const cfg = serviceConfig(svc);
              const active = filterServices.has(svc);
              return (
                <button
                  key={svc}
                  onClick={() => toggleServiceFilter(svc)}
                  style={{
                    display: 'flex',
                    alignItems: 'center',
                    gap: 5,
                    padding: isMobile ? '8px 14px' : '4px 10px',
                    borderRadius: 20,
                    border: `1px solid ${active ? 'var(--color-accent)' : 'var(--color-border)'}`,
                    backgroundColor: active ? 'var(--color-accent)' : 'var(--color-surface-raised)',
                    color: active ? 'var(--color-text-on-dark)' : 'var(--color-text-mid)',
                    fontWeight: 700,
                    fontSize: 12,
                    cursor: 'pointer',
                    whiteSpace: 'nowrap',
                    flexShrink: 0,
                  }}
                >
                  <div style={{ width: 6, height: 6, borderRadius: '50%', backgroundColor: active ? 'var(--color-text-on-dark)' : cfg.color, flexShrink: 0 }} />
                  {cfg.label}
                </button>
              );
            })}
          </div>
        )}

        {/* Resource type sub-filter pills (shown when exactly one service is selected and has derivable sub-types) */}
        {singleService && availableResourceTypes.length > 0 && (
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
            {availableResourceTypes.map(rt => {
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

        {/* An active type filter narrows to records that have resource-level
            cost data, which AWS only provides for the trailing 14 days —
            longer windows will look truncated without this explanation. */}
        {singleService && filterResourceTypes.size > 0 && (customRange
          ? (new Date(customRange.untilIso) - new Date(customRange.sinceIso)) / 86400000 >= PANEL_MAX_DAYS
          : period > PANEL_MAX_DAYS) && (
          <div style={{ padding: '0 16px 12px' }}>
            <span style={{ fontSize: 11, color: 'var(--color-text-muted)', fontStyle: 'italic' }}>
              Type filters use resource-level cost data, which AWS caps at the last {PANEL_MAX_DAYS} days — earlier records are excluded.
            </span>
          </div>
        )}

        {costChartData.length < 2 ? (
          <div style={{ padding: '24px 16px', textAlign: 'center' }}>
            <span style={{ fontSize: 13, color: 'var(--color-text-muted)' }}>
              {records.length === 0 ? 'No cost data yet — run a scan first.' : 'Not enough data points to chart. Try a longer time period.'}
            </span>
          </div>
        ) : (
          <div style={{ padding: '0 16px 16px' }}>
            <AreaChart
              data={costChartData}
              selectedId={selectedChartDate}
              onSelect={(point) => setSelectedChartDate(point.snapshot_at)}
              screenWidth={screenWidth}
            />
            <div style={{ padding: '8px 0 0', textAlign: 'center' }}>
              <span style={{ fontSize: 11, color: 'var(--color-text-muted)' }}>
                {costChartData.length} {effectiveGranularity === 'monthly' ? 'month' : 'day'}{costChartData.length !== 1 ? 's' : ''} · {records.length} record{records.length !== 1 ? 's' : ''}
              </span>
            </div>
          </div>
        )}
      </div>

      {/* Empty state */}
      {records.length === 0 && (
        <div style={{ display: 'flex', justifyContent: 'center', alignItems: 'center', minHeight: '40vh', flexDirection: 'column', gap: 8 }}>
          <span style={{ fontSize: 14, color: 'var(--color-text-muted)' }}>No cost records found</span>
          <span style={{ fontSize: 12, color: 'var(--color-text-muted)' }}>Try adjusting your filters or time period</span>
        </div>
      )}

      {/* Cost records list — desktop puts the records column and the
          service-detail panel side-by-side via flex; on phones (xs/sm)
          the 350px sticky panel doesn't fit alongside any list, so the
          records column takes the full width and the panel renders as
          a bottom sheet only when a service is selected. */}
      {records.length > 0 && (
        <div style={{ display: 'flex', gap: isMobile ? 0 : 16, padding: isMobile ? '12px' : '16px' }}>
          {/* Records list */}
          <div style={{ flex: 1, minWidth: 0 }}>
            {/* Summary header */}
            <div style={{ display: 'flex', alignItems: 'center', justifyContent: 'space-between', marginBottom: 16, paddingBottom: 12, borderBottom: `1px solid var(--color-border)` }}>
              <span style={{ fontSize: 13, fontWeight: 600, color: 'var(--color-text-muted)', textTransform: 'uppercase', letterSpacing: 0.5 }}>
                By Service · {serviceGroups.length}
              </span>
              <div style={{ display: 'flex', alignItems: 'center', gap: 12 }}>
                <div style={{ display: 'flex', alignItems: 'center', gap: 6 }}>
                  <span style={{ fontSize: 11, color: 'var(--color-text-muted)', fontWeight: 600 }}>Sort:</span>
                  <select
                    value={sortBy}
                    onChange={(e) => setSortBy(e.target.value)}
                    style={{
                      padding: '4px 8px',
                      borderRadius: 6,
                      border: `1px solid var(--color-border)`,
                      backgroundColor: 'var(--color-surface)',
                      color: 'var(--color-text)',
                      fontSize: 11,
                      fontWeight: 600,
                      cursor: 'pointer',
                    }}
                  >
                    <option value="cost_desc">Cost (High → Low)</option>
                    <option value="cost_asc">Cost (Low → High)</option>
                    <option value="name_asc">Service (A → Z)</option>
                    <option value="name_desc">Service (Z → A)</option>
                  </select>
                </div>
                <button
                  onClick={() => exportCSV(records, {
                    services: [...filterServices],
                    resourceTypes: [...filterResourceTypes],
                  }, toast)}
                  disabled={records.length === 0}
                  aria-label="Export to CSV"
                  style={{
                    padding: isMobile ? '8px 12px' : '4px 10px',
                    borderRadius: 6,
                    border: `1px solid var(--color-border)`,
                    backgroundColor: 'var(--color-surface-raised)',
                    cursor: records.length === 0 ? 'not-allowed' : 'pointer',
                    opacity: records.length === 0 ? 0.5 : 1,
                  }}
                >
                  <span style={{ fontSize: 11, fontWeight: 700, color: 'var(--color-text-mid)' }}>↓ CSV</span>
                </button>
              </div>
            </div>

            {/* Service-group rows — at xs/sm the four-column row (dot + info
                + 100px period + 70px cost) is too tight, so stack the
                period+cost block under the info column. Cost gets larger
                emphasis on its own line on phones to stay scannable. */}
            <div style={{ display: 'flex', flexDirection: 'column', gap: 8 }}>
              {serviceGroups.map(group => {
                const cfg = serviceConfig(group.service);
                const isSelected = selectedService === group.service;
                return (
                  <div
                    key={group.service}
                    onClick={() => setSelectedService(isSelected ? null : group.service)}
                    style={{
                      display: 'flex',
                      flexDirection: isMobile ? 'column' : 'row',
                      alignItems: isMobile ? 'stretch' : 'center',
                      gap: isMobile ? 6 : 12,
                      padding: '12px 14px',
                      backgroundColor: isSelected ? 'var(--color-surface-raised)' : 'var(--color-surface)',
                      border: `1px solid ${isSelected ? 'var(--color-accent)' : 'var(--color-border)'}`,
                      borderRadius: 8,
                      cursor: 'pointer',
                    }}
                  >
                    <div style={{ display: 'flex', alignItems: 'center', gap: 8, minWidth: 0, flex: 1 }}>
                      <div style={{ width: 6, height: 6, borderRadius: '50%', backgroundColor: cfg.color, flexShrink: 0 }} />
                      <div style={{ flex: 1, minWidth: 0 }}>
                        <div style={{ display: 'flex', alignItems: 'center', gap: 6, flexWrap: 'wrap' }}>
                          <div style={{ fontSize: 13, fontWeight: 600, color: 'var(--color-text)' }}>
                            {cfg.label}
                          </div>
                          {zombiesQuery.data?.some(z => z.service === group.service) && (
                            <span style={{ fontSize: 10, fontWeight: 700, padding: '2px 6px', borderRadius: 10, backgroundColor: '#FEF2F2', color: '#EF4444', border: '1px solid #FCA5A5' }}>
                              🧟 Zombie Waste
                            </span>
                          )}
                        </div>
                        <div style={{ fontSize: 11, color: 'var(--color-text-muted)', marginTop: 2 }}>
                          {group.count} record{group.count !== 1 ? 's' : ''} · {group.regions.length} region{group.regions.length !== 1 ? 's' : ''}
                        </div>
                      </div>
                    </div>
                    {isMobile ? (
                      <div style={{ display: 'flex', alignItems: 'baseline', justifyContent: 'space-between', gap: 8, paddingLeft: 14 }}>
                        <span style={{ fontSize: 11, color: 'var(--color-text-muted)' }}>
                          {formatDateShort(group.periodStart)} – {formatDateShort(group.periodEnd)}
                        </span>
                        <span style={{ fontSize: 15, fontWeight: 700, color: 'var(--color-accent)' }}>
                          ${formatCost(group.total)}
                        </span>
                      </div>
                    ) : (
                      <>
                        <div style={{ fontSize: 11, color: 'var(--color-text-muted)', textAlign: 'right', flexShrink: 0, minWidth: 100 }}>
                          {formatDateShort(group.periodStart)} – {formatDateShort(group.periodEnd)}
                        </div>
                        <div style={{ fontSize: 14, fontWeight: 700, color: 'var(--color-accent)', textAlign: 'right', flexShrink: 0, minWidth: 70 }}>
                          ${formatCost(group.total)}
                        </div>
                      </>
                    )}
                  </div>
                );
              })}
            </div>
          </div>

          {/* Service-detail panel (desktop) — drill-down by resource_id,
              clamped to 14d. On phones this same content renders inside
              a MobileSheet outside the flex row (see below). */}
          {!isMobile && selectedService && panelStats && selectedServiceBreakdown && (
            <div style={{ width: 350, backgroundColor: 'var(--color-surface)', borderRadius: 8, border: `1px solid var(--color-border)`, padding: 16, height: 'fit-content', position: 'sticky', top: 16 }}>
              <ServiceDetailPanelBody
                selectedService={selectedService}
                panelStats={panelStats}
                panelClamped={panelClamped}
                selectedServiceBreakdown={selectedServiceBreakdown}
                zombieResourceIds={zombieResourceIds}
              />
            </div>
          )}
        </div>
      )}

      {/* Service-detail sheet (mobile) — same content as the desktop side
          panel, surfaced as a bottom sheet so it doesn't compete for
          horizontal space with the records list. Closing the sheet
          clears the selection so the next tap starts fresh. */}
      <MobileSheet
        visible={isMobile && !!selectedService && !!panelStats && !!selectedServiceBreakdown}
        onClose={() => setSelectedService(null)}
        ariaLabel="Service cost detail"
      >
        <div style={{ padding: '4px 16px 24px' }}>
          {selectedService && panelStats && selectedServiceBreakdown && (
            <ServiceDetailPanelBody
              selectedService={selectedService}
              panelStats={panelStats}
              panelClamped={panelClamped}
              selectedServiceBreakdown={selectedServiceBreakdown}
              zombieResourceIds={zombieResourceIds}
            />
          )}
        </div>
      </MobileSheet>
    </div>
  );
}

// Extracted so desktop's sticky right-rail and mobile's bottom sheet
// render identical content without diverging.
function ServiceDetailPanelBody({ selectedService, panelStats, panelClamped, selectedServiceBreakdown, zombieResourceIds = new Set() }) {
  const cfg = serviceConfig(selectedService);
  return (
    <>
      <div style={{ display: 'flex', alignItems: 'center', gap: 8, marginBottom: 16, paddingBottom: 12, borderBottom: `1px solid var(--color-border)` }}>
        <div style={{ width: 8, height: 8, borderRadius: '50%', backgroundColor: cfg.color }} />
        <div style={{ minWidth: 0 }}>
          <div style={{ fontSize: 14, fontWeight: 700, color: 'var(--color-text)' }}>{cfg.label}</div>
          <div style={{ fontSize: 11, color: 'var(--color-text-muted)', wordBreak: 'break-all' }}>{selectedService}</div>
        </div>
      </div>

      {panelClamped && (
        <div style={{ fontSize: 10, color: 'var(--color-text-muted)', fontStyle: 'italic', marginBottom: 12, padding: '6px 8px', backgroundColor: 'var(--color-surface-raised)', borderRadius: 4 }}>
          Showing last {PANEL_MAX_DAYS} days · resource-level cost data is capped at {PANEL_MAX_DAYS} days by AWS Cost Explorer. The chart and the table still reflect your selected period.
        </div>
      )}

      <div style={{ display: 'flex', flexDirection: 'column', gap: 12, marginBottom: 16 }}>
        <div>
          <div style={{ fontSize: 11, color: 'var(--color-text-muted)', textTransform: 'uppercase', fontWeight: 600, marginBottom: 4 }}>
            Total
          </div>
          <div style={{ fontSize: 20, fontWeight: 700, color: 'var(--color-accent)' }}>
            ${panelStats.total.toFixed(2)} {panelStats.currency}
          </div>
        </div>

        <div>
          <div style={{ fontSize: 11, color: 'var(--color-text-muted)', textTransform: 'uppercase', fontWeight: 600, marginBottom: 4 }}>
            Period
          </div>
          <div style={{ fontSize: 12, color: 'var(--color-text)' }}>
            {formatDateShort(panelStats.periodStart)} – {formatDateShort(panelStats.periodEnd)} · {panelStats.count} record{panelStats.count !== 1 ? 's' : ''}
          </div>
        </div>

        <div>
          <div style={{ fontSize: 11, color: 'var(--color-text-muted)', textTransform: 'uppercase', fontWeight: 600, marginBottom: 4 }}>
            Regions
          </div>
          <div style={{ fontSize: 12, color: 'var(--color-text)' }}>
            {panelStats.regions.join(', ')}
          </div>
        </div>
      </div>

      <div style={{ borderTop: `1px solid var(--color-border)`, paddingTop: 12 }}>
        <div style={{ fontSize: 11, color: 'var(--color-text-muted)', textTransform: 'uppercase', fontWeight: 600, marginBottom: 8 }}>
          Resources · {selectedServiceBreakdown.length}
        </div>
        <div style={{ display: 'flex', flexDirection: 'column', gap: 6, maxHeight: 360, overflowY: 'auto' }}>
          {selectedServiceBreakdown.map((e, i) => {
            const resourceType = resourceTypeFromId(e.resourceId);
            const isZombie = e.resourceId && zombieResourceIds.has(e.resourceId);
            return (
            <div key={e.resourceId ?? `__none__${i}`} style={{ display: 'flex', alignItems: 'flex-start', gap: 8, padding: '6px 0', borderBottom: i < selectedServiceBreakdown.length - 1 ? `1px solid var(--color-border)` : 'none' }}>
              <div style={{ flex: 1, minWidth: 0 }}>
                <div style={{ display: 'flex', alignItems: 'center', gap: 6, flexWrap: 'wrap' }}>
                  <span style={{ fontSize: 11, color: 'var(--color-text)', fontFamily: e.resourceId ? '"Geist Mono Variable", monospace' : 'inherit', fontStyle: e.resourceId ? 'normal' : 'italic', wordBreak: 'break-all' }}>
                    {e.resourceId ?? 'No resource ID'}
                  </span>
                  {isZombie && (
                    <span style={{ fontSize: 9, fontWeight: 700, padding: '1px 5px', borderRadius: 8, backgroundColor: '#FEF2F2', color: '#EF4444', border: '1px solid #FCA5A5' }}>
                      🧟 Zombie
                    </span>
                  )}
                </div>
                <div style={{ fontSize: 10, color: 'var(--color-text-muted)', marginTop: 2 }}>
                  {resourceType ? `${resourceTypeConfig(resourceType).label} · ` : ''}{e.count} record{e.count !== 1 ? 's' : ''} · {e.regions.join(', ')}
                </div>
              </div>
              <div style={{ fontSize: 12, fontWeight: 700, color: 'var(--color-accent)', flexShrink: 0 }}>
                ${formatCost(e.total)}
              </div>
            </div>
            );
          })}
        </div>
      </div>
    </>
  );
}
