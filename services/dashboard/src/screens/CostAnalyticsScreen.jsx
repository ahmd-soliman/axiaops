import { useState, useMemo, useCallback } from 'react';
import { useQuery, useQueries } from '@tanstack/react-query';
import { fetchCosts, fetchTrend, fetchAccounts } from '../api/client';
import { serviceConfig } from '../components/serviceConfig';
import AccountSelector from '../components/AccountSelector';
import AreaChart from '../components/AreaChart';
import { useTheme } from '../theme/ThemeContext';
import { Spinner } from '../components/primitives';
import { useWindowWidth } from '../components/primitives';

const PERIOD_OPTIONS = [
  { label: '7d',  days: 7 },
  { label: '30d', days: 30 },
  { label: '90d', days: 90 },
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

export default function CostAnalyticsScreen({ accounts: passedAccounts, selectedAccount: passedSelectedAccount, onSelectAccount }) {
  const { theme } = useTheme();
  const screenWidth = useWindowWidth();
  const [period, setPeriod] = useState(30);
  const [filterService, setFilterService] = useState(null);
  const [selectedCost, setSelectedCost] = useState(null);
  const [selectedTrendDate, setSelectedTrendDate] = useState(null);

  // Use passed accounts or fetch if not provided
  const accountsQuery = useQuery({ queryKey: ['accounts'], queryFn: fetchAccounts });
  const accounts = passedAccounts?.length > 0 ? passedAccounts : (accountsQuery.data ?? []);
  const selectedAccount = passedSelectedAccount;

  // Fetch costs and trends
  // Cost records now filter by internal_account_id directly (no resolution needed)
  const costsQuery = useQuery({
    queryKey: ['costs', selectedAccount, filterService, period],
    queryFn: () => fetchCosts(selectedAccount, filterService, period),
  });

  const trendQuery = useQuery({
    queryKey: ['trend', selectedAccount],
    queryFn: () => fetchTrend(selectedAccount, null, null),
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

  // Calculate totals
  const totalCost = useMemo(() => {
    if (!costsQuery.data) return 0;
    return costsQuery.data.reduce((sum, r) => sum + (r.amount || 0), 0);
  }, [costsQuery.data]);

  const records = costsQuery.data || [];
  const trends = trendQuery.data || [];
  const trendIsLoading = trendQuery.isLoading;
  const costsIsLoading = costsQuery.isLoading;

  const t = theme;

  // Loading state
  if (costsIsLoading || trendIsLoading) {
    return (
      <div style={{ display: 'flex', justifyContent: 'center', alignItems: 'center', minHeight: '60vh', backgroundColor: t.bg, flexDirection: 'column', gap: 14 }}>
        <Spinner />
        <span style={{ fontSize: 14, color: t.textMuted }}>Loading cost analytics…</span>
      </div>
    );
  }

  // Error state
  if (costsQuery.isError || trendQuery.isError) {
    return (
      <div style={{ display: 'flex', justifyContent: 'center', alignItems: 'center', minHeight: '60vh', backgroundColor: t.bg, flexDirection: 'column', gap: 14 }}>
        <span style={{ fontSize: 14, color: t.error }}>Failed to load cost analytics</span>
      </div>
    );
  }

  return (
    <div style={{ backgroundColor: t.bg, minHeight: '100vh' }}>
      {/* Header */}
      <div style={{ backgroundColor: t.surface, borderBottom: `1px solid ${t.border}`, padding: '16px' }}>
        <AccountSelector
          accounts={accounts}
          selectedAccount={selectedAccount}
          onSelectAccount={onSelectAccount}
        />
      </div>

      {/* Period selector */}
      <div style={{ display: 'flex', gap: 8, padding: '16px', backgroundColor: t.bg, borderBottom: `1px solid ${t.border}`, overflowX: 'auto' }}>
        {PERIOD_OPTIONS.map(p => (
          <button
            key={p.days}
            onClick={() => { setPeriod(p.days); setFilterService(null); setSelectedCost(null); }}
            style={{
              padding: '6px 12px',
              borderRadius: 4,
              border: `1px solid ${period === p.days ? t.accent : t.border}`,
              backgroundColor: period === p.days ? t.accent : t.surface,
              color: period === p.days ? t.bg : t.text,
              fontWeight: 600,
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

      {/* Trends chart section */}
      {trends.length > 0 && (
        <div style={{ padding: '20px', backgroundColor: t.bg, borderBottom: `1px solid ${t.border}` }}>
          <span style={{ fontSize: 12, fontWeight: 600, color: t.textMuted, textTransform: 'uppercase', letterSpacing: 0.5, display: 'block', marginBottom: 16 }}>
            Cost Trend (Last 30 Days)
          </span>
          <AreaChart
            data={trends}
            selectedId={selectedTrendDate}
            onSelect={(snap) => setSelectedTrendDate(snap.snapshot_at)}
            theme={t}
            screenWidth={screenWidth}
          />
        </div>
      )}

      {/* Service filter pills */}
      {allServices.length > 0 && (
        <div style={{ display: 'flex', gap: 8, padding: '12px 16px', backgroundColor: t.bg, borderBottom: `1px solid ${t.border}`, overflowX: 'auto', flexWrap: 'wrap' }}>
          <button
            onClick={() => setFilterService(null)}
            style={{
              padding: '4px 10px',
              borderRadius: 12,
              border: `1px solid ${!filterService ? t.accent : t.border}`,
              backgroundColor: !filterService ? t.accent : t.surface,
              color: !filterService ? t.bg : t.text,
              fontWeight: 600,
              fontSize: 11,
              cursor: 'pointer',
              whiteSpace: 'nowrap',
              flexShrink: 0,
            }}
          >
            All
          </button>
          {allServices.map(svc => {
            const cfg = serviceConfig(svc);
            return (
              <button
                key={svc}
                onClick={() => setFilterService(filterService === svc ? null : svc)}
                style={{
                  padding: '4px 10px',
                  borderRadius: 12,
                  border: `1px solid ${filterService === svc ? t.accent : t.border}`,
                  backgroundColor: filterService === svc ? t.accent : t.surface,
                  color: filterService === svc ? t.bg : t.text,
                  fontWeight: 600,
                  fontSize: 11,
                  cursor: 'pointer',
                  whiteSpace: 'nowrap',
                  flexShrink: 0,
                  display: 'flex',
                  alignItems: 'center',
                  gap: 4,
                }}
              >
                <div style={{ width: 4, height: 4, borderRadius: '50%', backgroundColor: cfg.color, flexShrink: 0 }} />
                {cfg.label}
              </button>
            );
          })}
        </div>
      )}

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
            <div style={{ marginBottom: 16, paddingBottom: 12, borderBottom: `1px solid ${t.border}` }}>
              <span style={{ fontSize: 13, fontWeight: 600, color: t.textMuted, textTransform: 'uppercase', letterSpacing: 0.5 }}>
                Total Cost · ${totalCost.toFixed(2)} · {records.length} records
              </span>
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
