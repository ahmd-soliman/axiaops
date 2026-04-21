import { useState, useMemo, useCallback } from 'react';
import { useQuery } from '@tanstack/react-query';
import { fetchCosts, fetchAccounts, scanAccount } from '../api/client';
import { serviceConfig } from '../components/serviceConfig';
import AccountSelector from '../components/AccountSelector';
import AreaChart from '../components/AreaChart';
import { useTheme } from '../theme/ThemeContext';
import { Spinner, Toast } from '../components/primitives';
import { useWindowWidth } from '../components/primitives';

const PERIOD_OPTIONS = [
  { label: '7d',  days: 7 },
  { label: '30d', days: 30 },
  { label: '90d', days: 90 },
  { label: '6m',  days: 180 },
  { label: '1y',  days: 365 },
];

function formatCost(val) {
  if (val >= 1000) return `${(val / 1000).toFixed(2)}k`;
  if (val >= 1) return val.toFixed(2);
  return val.toFixed(6);
}

function formatDate(iso) {
  const d = new Date(iso);
  return d.toLocaleDateString('en-GB', { day: 'numeric', month: 'short', year: 'numeric' })
    + ' · ' + d.toLocaleTimeString('en-GB', { hour: '2-digit', minute: '2-digit' });
}

function formatDateShort(iso) {
  return new Date(iso).toLocaleDateString('en-GB', { day: 'numeric', month: 'short' });
}

export default function CostAnalyticsScreen({ accounts: passedAccounts, selectedAccount: passedSelectedAccount, onSelectAccount, onConnectAccount, onEditAccount }) {
  const { theme } = useTheme();
  const screenWidth = useWindowWidth();
  const [period, setPeriod] = useState(30);
  const [filterServices, setFilterServices] = useState(() => new Set());
  const [selectedCost, setSelectedCost] = useState(null);
  const [selectedChartDate, setSelectedChartDate] = useState(null);
  const [scanning, setScanning] = useState(null);
  const [notification, setNotification] = useState(null);

  // Use passed accounts or fetch if not provided
  const accountsQuery = useQuery({ queryKey: ['accounts'], queryFn: fetchAccounts });
  const accounts = passedAccounts?.length > 0 ? passedAccounts : (accountsQuery.data ?? []);
  const selectedAccount = passedSelectedAccount;

  // Fetch costs and trends
  // Cost records now filter by internal_account_id directly (no resolution needed)
  const costsQuery = useQuery({
    queryKey: ['costs', selectedAccount, period],
    queryFn: () => fetchCosts(selectedAccount, null, period),
  });

  // Derive distinct services from costs
  const allServices = useMemo(() => {
    if (!costsQuery.data) return [];
    const services = new Set();
    for (const record of costsQuery.data) {
      if (record.service) services.add(record.service);
    }
    return Array.from(services).sort();
  }, [costsQuery.data]);

  // Filter records client-side by selected services
  const filteredCosts = useMemo(() => {
    if (!costsQuery.data) return [];
    if (filterServices.size === 0) return costsQuery.data;
    return costsQuery.data.filter(r => filterServices.has(r.service));
  }, [costsQuery.data, filterServices]);

  // Calculate totals
  const totalCost = useMemo(() => {
    return filteredCosts.reduce((sum, r) => sum + (r.amount || 0), 0);
  }, [filteredCosts]);

  // Aggregate cost records by date for the chart.
  // Groups by period_start date, sums amounts, and maps to the shape AreaChart expects.
  const costChartData = useMemo(() => {
    if (filteredCosts.length === 0) return [];
    const byDate = new Map();
    for (const r of filteredCosts) {
      const day = new Date(r.period_start).toISOString().slice(0, 10);
      const entry = byDate.get(day);
      if (entry) {
        entry.total_monthly_cost += r.amount || 0;
      } else {
        byDate.set(day, {
          snapshot_at: r.period_start,
          total_monthly_cost: r.amount || 0,
          currency: r.currency || 'USD',
        });
      }
    }
    return [...byDate.values()]
      .sort((a, b) => a.snapshot_at.localeCompare(b.snapshot_at));
  }, [filteredCosts]);

  // Scan account
  const handleScanAccount = useCallback(async (accountId) => {
    const accountLabel = accounts.find(a => a.id === accountId)?.label || accountId.slice(0, 8);
    setScanning(accountId);
    setNotification({ message: `Scan started for ${accountLabel}...`, type: 'info' });
    try {
      await scanAccount(accountId);
      setNotification({ message: `Scan completed for ${accountLabel}!`, type: 'success' });
    } catch (err) {
      console.error('Failed to scan account:', err);
      setNotification({ message: `Scan failed for ${accountLabel}`, type: 'error' });
    } finally {
      setScanning(null);
    }
  }, [accounts]);

  // Toggle service filter
  const toggleServiceFilter = (svc) => {
    const next = new Set(filterServices);
    next.has(svc) ? next.delete(svc) : next.add(svc);
    setFilterServices(next);
    setSelectedCost(null);
  };

  const clearServiceFilter = () => {
    setFilterServices(new Set());
    setSelectedCost(null);
  };

  // CSV export
  const exportToCSV = () => {
    if (filteredCosts.length === 0) return;
    const headers = ['Service', 'Region', 'Amount', 'Currency', 'Period Start', 'Period End', 'Resource ID'];
    const rows = filteredCosts.map(r => [
      r.service,
      r.region || 'N/A',
      r.amount.toFixed(2),
      r.currency,
      new Date(r.period_start).toISOString().split('T')[0],
      new Date(r.period_end).toISOString().split('T')[0],
      r.resource_id || '',
    ]);
    const csv = [headers, ...rows].map(row => row.map(cell => `"${String(cell).replace(/"/g, '""')}"`).join(',')).join('\n');
    const blob = new Blob([csv], { type: 'text/csv' });
    const url = URL.createObjectURL(blob);
    const a = document.createElement('a');
    a.href = url;
    a.download = `cost-breakdown-${new Date().toISOString().split('T')[0]}.csv`;
    a.click();
    URL.revokeObjectURL(url);
  };

  const records = filteredCosts;
  const costsIsLoading = costsQuery.isLoading;

  const t = theme;

  // Loading state
  if (costsIsLoading) {
    return (
      <div style={{ display: 'flex', justifyContent: 'center', alignItems: 'center', minHeight: '60vh', backgroundColor: t.bg, flexDirection: 'column', gap: 14 }}>
        <Spinner />
        <span style={{ fontSize: 14, color: t.textMuted }}>Loading cost analytics…</span>
      </div>
    );
  }

  // Error state
  if (costsQuery.isError) {
    return (
      <div style={{ display: 'flex', justifyContent: 'center', alignItems: 'center', minHeight: '60vh', backgroundColor: t.bg, flexDirection: 'column', gap: 14 }}>
        <span style={{ fontSize: 14, color: t.error }}>Failed to load cost analytics</span>
      </div>
    );
  }

  return (
    <div style={{ backgroundColor: t.bg, minHeight: '100vh' }}>
      <Toast
        message={notification?.message}
        type={notification?.type}
        onDismiss={() => setNotification(null)}
      />
      {/* Header */}
      <div style={{ backgroundColor: t.surface, borderBottom: `1px solid ${t.border}`, padding: '16px' }}>
        <AccountSelector
          accounts={accounts}
          selectedAccount={selectedAccount}
          onSelectAccount={onSelectAccount}
          onConnectAccount={onConnectAccount}
          onEditAccount={onEditAccount}
          onScanAccount={handleScanAccount}
          scanning={scanning}
        />
      </div>

      {/* Total cost hero */}
      <div style={{ backgroundColor: t.surfaceAlt || t.surface, borderBottom: `1px solid ${t.border}`, padding: '20px 20px 16px' }}>
        <span style={{ fontSize: 11, fontWeight: 600, color: t.textMuted, letterSpacing: 1.2, textTransform: 'uppercase', display: 'block', marginBottom: 4 }}>
          Total Spend · {PERIOD_OPTIONS.find(p => p.days === period)?.label || `${period}d`}
        </span>
        <span style={{ fontSize: 32, fontWeight: 800, color: t.accent, letterSpacing: -0.5, display: 'block' }}>
          ${totalCost.toFixed(2)}
        </span>
        <span style={{ fontSize: 13, color: t.textMid, marginTop: 4, display: 'block' }}>
          {records.length} record{records.length !== 1 ? 's' : ''} across {allServices.length} service{allServices.length !== 1 ? 's' : ''}
        </span>
      </div>

      {/* Cost chart section */}
      <div style={{ backgroundColor: t.bg, borderBottom: `1px solid ${t.border}` }}>
        <div style={{ display: 'flex', alignItems: 'center', justifyContent: 'space-between', padding: '16px 20px 0', flexWrap: 'wrap', gap: 8 }}>
          <span style={{ fontSize: 12, fontWeight: 600, color: t.textMuted, textTransform: 'uppercase', letterSpacing: 0.5 }}>
            Daily Spend
          </span>
          <div style={{ display: 'flex', gap: 4 }}>
            {PERIOD_OPTIONS.map(p => (
              <button
                key={p.days}
                onClick={() => { setPeriod(p.days); setSelectedCost(null); }}
                style={{
                  padding: '4px 10px',
                  borderRadius: 6,
                  border: `1px solid ${period === p.days ? t.accent : t.border}`,
                  backgroundColor: period === p.days ? t.accent : t.surfaceRaised,
                  color: period === p.days ? '#fff' : t.textMid,
                  fontWeight: 700,
                  fontSize: 12,
                  cursor: 'pointer',
                  whiteSpace: 'nowrap',
                  flexShrink: 0,
                }}
              >
                {p.label}
              </button>
            ))}
          </div>
        </div>

        {/* Service filter pills */}
        {allServices.length > 0 && (
          <div style={{ display: 'flex', gap: 6, padding: '8px 16px 16px', overflowX: 'auto' }}>
            <button
              onClick={clearServiceFilter}
              style={{
                padding: '4px 10px',
                borderRadius: 20,
                border: `1px solid ${filterServices.size === 0 ? t.accent : t.border}`,
                backgroundColor: filterServices.size === 0 ? t.accent : t.surfaceRaised,
                color: filterServices.size === 0 ? '#fff' : t.textMid,
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
                    padding: '4px 10px',
                    borderRadius: 20,
                    border: `1px solid ${active ? t.accent : t.border}`,
                    backgroundColor: active ? t.accent : t.surfaceRaised,
                    color: active ? '#fff' : t.textMid,
                    fontWeight: 700,
                    fontSize: 12,
                    cursor: 'pointer',
                    whiteSpace: 'nowrap',
                    flexShrink: 0,
                  }}
                >
                  <div style={{ width: 6, height: 6, borderRadius: '50%', backgroundColor: active ? '#fff' : cfg.color, flexShrink: 0 }} />
                  {cfg.label}
                </button>
              );
            })}
          </div>
        )}

        {costChartData.length < 2 ? (
          <div style={{ padding: '24px 16px', textAlign: 'center' }}>
            <span style={{ fontSize: 13, color: t.textMuted }}>
              {records.length === 0 ? 'No cost data yet — run a scan first.' : 'Not enough data points to chart. Try a longer time period.'}
            </span>
          </div>
        ) : (
          <div style={{ padding: '0 16px 16px' }}>
            <AreaChart
              data={costChartData}
              selectedId={selectedChartDate}
              onSelect={(point) => setSelectedChartDate(point.snapshot_at)}
              theme={t}
              screenWidth={screenWidth}
            />
            <div style={{ padding: '8px 0 0', textAlign: 'center' }}>
              <span style={{ fontSize: 11, color: t.textMuted }}>
                {costChartData.length} day{costChartData.length !== 1 ? 's' : ''} · {records.length} record{records.length !== 1 ? 's' : ''}
              </span>
            </div>
          </div>
        )}
      </div>

      {/* Empty state */}
      {records.length === 0 && (
        <div style={{ display: 'flex', justifyContent: 'center', alignItems: 'center', minHeight: '40vh', flexDirection: 'column', gap: 8 }}>
          <span style={{ fontSize: 14, color: t.textMuted }}>No cost records found</span>
          <span style={{ fontSize: 12, color: t.textMuted }}>Try adjusting your filters or time period</span>
        </div>
      )}

      {/* Cost records list */}
      {records.length > 0 && (
        <div style={{ display: 'flex', gap: 16, padding: '16px' }}>
          {/* Records list */}
          <div style={{ flex: 1 }}>
            {/* Summary header */}
            <div style={{ display: 'flex', alignItems: 'center', justifyContent: 'space-between', marginBottom: 16, paddingBottom: 12, borderBottom: `1px solid ${t.border}` }}>
              <span style={{ fontSize: 13, fontWeight: 600, color: t.textMuted, textTransform: 'uppercase', letterSpacing: 0.5 }}>
                Cost Records · {records.length}
              </span>
              <button
                onClick={exportToCSV}
                style={{
                  padding: '6px 12px',
                  borderRadius: 6,
                  border: `1px solid ${t.border}`,
                  backgroundColor: t.surfaceRaised,
                  color: t.textMid,
                  fontWeight: 600,
                  fontSize: 12,
                  cursor: 'pointer',
                  whiteSpace: 'nowrap',
                }}
              >
                ⬇ Export CSV
              </button>
            </div>

            {/* Records table-like view */}
            <div style={{ display: 'flex', flexDirection: 'column', gap: 8 }}>
              {records.map((record, idx) => {
                const cfg = serviceConfig(record.service);
                const isSelected = selectedCost === idx;
                return (
                  <div
                    key={idx}
                    onClick={() => setSelectedCost(isSelected ? null : idx)}
                    style={{
                      display: 'flex',
                      alignItems: 'center',
                      gap: 12,
                      padding: '12px 14px',
                      backgroundColor: isSelected ? t.surfaceRaised : t.surface,
                      border: `1px solid ${isSelected ? t.accent : t.border}`,
                      borderRadius: 8,
                      cursor: 'pointer',
                    }}
                  >
                    <div style={{ width: 6, height: 6, borderRadius: '50%', backgroundColor: cfg.color, flexShrink: 0 }} />
                    <div style={{ flex: 1, minWidth: 0 }}>
                      <div style={{ fontSize: 13, fontWeight: 600, color: t.text }}>
                        {cfg.label}
                      </div>
                      <div style={{ fontSize: 11, color: t.textMuted, marginTop: 2 }}>
                        {record.region || 'NoRegion'}
                      </div>
                    </div>
                    <div style={{ fontSize: 11, color: t.textMuted, textAlign: 'right', flexShrink: 0, minWidth: 100 }}>
                      {formatDate(record.period_start)}
                    </div>
                    <div style={{ fontSize: 14, fontWeight: 700, color: t.accent, textAlign: 'right', flexShrink: 0, minWidth: 70 }}>
                      ${formatCost(record.amount)}
                    </div>
                  </div>
                );
              })}
            </div>
          </div>

          {/* Details panel */}
          {selectedCost !== null && records[selectedCost] && (
            <div style={{ width: 350, backgroundColor: t.surface, borderRadius: 8, border: `1px solid ${t.border}`, padding: 16, height: 'fit-content', position: 'sticky', top: 16 }}>
              {(() => {
                const record = records[selectedCost];
                const cfg = serviceConfig(record.service);
                return (
                  <div>
                    <div style={{ display: 'flex', alignItems: 'center', gap: 8, marginBottom: 16, paddingBottom: 12, borderBottom: `1px solid ${t.border}` }}>
                      <div style={{ width: 8, height: 8, borderRadius: '50%', backgroundColor: cfg.color }} />
                      <div>
                        <div style={{ fontSize: 14, fontWeight: 700, color: t.text }}>{cfg.label}</div>
                        <div style={{ fontSize: 11, color: t.textMuted }}>{record.service}</div>
                      </div>
                    </div>

                    <div style={{ display: 'flex', flexDirection: 'column', gap: 12 }}>
                      <div>
                        <div style={{ fontSize: 11, color: t.textMuted, textTransform: 'uppercase', fontWeight: 600, marginBottom: 4 }}>
                          Amount
                        </div>
                        <div style={{ fontSize: 20, fontWeight: 700, color: t.accent }}>
                          ${record.amount.toFixed(2)}
                        </div>
                      </div>

                      <div>
                        <div style={{ fontSize: 11, color: t.textMuted, textTransform: 'uppercase', fontWeight: 600, marginBottom: 4 }}>
                          Region
                        </div>
                        <div style={{ fontSize: 13, color: t.text }}>
                          {record.region || 'N/A'}
                        </div>
                      </div>

                      <div>
                        <div style={{ fontSize: 11, color: t.textMuted, textTransform: 'uppercase', fontWeight: 600, marginBottom: 4 }}>
                          Period
                        </div>
                        <div style={{ fontSize: 12, color: t.text }}>
                          {formatDateShort(record.period_start)} - {formatDateShort(record.period_end)}
                        </div>
                      </div>

                      <div>
                        <div style={{ fontSize: 11, color: t.textMuted, textTransform: 'uppercase', fontWeight: 600, marginBottom: 4 }}>
                          Currency
                        </div>
                        <div style={{ fontSize: 13, color: t.text }}>
                          {record.currency}
                        </div>
                      </div>

                      {record.resource_id && (
                        <div>
                          <div style={{ fontSize: 11, color: t.textMuted, textTransform: 'uppercase', fontWeight: 600, marginBottom: 4 }}>
                            Resource ID
                          </div>
                          <div style={{ fontSize: 12, color: t.text, wordBreak: 'break-all', fontFamily: 'monospace' }}>
                            {record.resource_id}
                          </div>
                        </div>
                      )}

                      {record.tags && Object.keys(record.tags).length > 0 && (
                        <div>
                          <div style={{ fontSize: 11, color: t.textMuted, textTransform: 'uppercase', fontWeight: 600, marginBottom: 4 }}>
                            Tags
                          </div>
                          <div style={{ fontSize: 11, color: t.text }}>
                            {Object.entries(record.tags).map(([k, v]) => (
                              <div key={k} style={{ marginBottom: 4 }}>
                                <span style={{ fontWeight: 600 }}>{k}:</span> {v}
                              </div>
                            ))}
                          </div>
                        </div>
                      )}
                    </div>
                  </div>
                );
              })()}
            </div>
          )}
        </div>
      )}
    </div>
  );
}
