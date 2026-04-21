import { useState, useMemo } from 'react';
import { useQuery } from '@tanstack/react-query';
import { fetchCosts } from '../api/client';
import { serviceConfig } from '../components/serviceConfig';
import AccountSelector from '../components/AccountSelector';
import { useTheme } from '../theme/ThemeContext';
import { Spinner } from '../components/primitives';

// ─── Format helpers ──────────────────────────────────────────────────────────

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

// ─── Main screen ──────────────────────────────────────────────────────────────
export default function CostScreen({ accounts, selectedAccount, selectedAwsAccount, onSelectAccount, onConnectAccount, onEditAccount }) {
  const { theme } = useTheme();
  const [period, setPeriod] = useState(30);
  const [filterService, setFilterService] = useState(null);

  const costsQuery = useQuery({
    queryKey: ['costs', selectedAwsAccount, filterService, period],
    queryFn: () => fetchCosts(selectedAwsAccount, filterService, period),
  });

  // Derive distinct services from fetched data
  const allServices = useMemo(() => {
    if (!costsQuery.data) return [];
    const services = new Set();
    for (const record of costsQuery.data) {
      if (record.service) services.add(record.service);
    }
    return Array.from(services).sort();
  }, [costsQuery.data]);

  // Calculate total cost
  const totalCost = useMemo(() => {
    if (!costsQuery.data) return 0;
    return costsQuery.data.reduce((sum, r) => sum + (r.amount || 0), 0);
  }, [costsQuery.data]);

  const t = theme;

  // Loading state
  if (costsQuery.isLoading) {
    return (
      <div style={{ display: 'flex', justifyContent: 'center', alignItems: 'center', minHeight: '60vh', backgroundColor: t.bg, flexDirection: 'column', gap: 14 }}>
        <Spinner />
        <span style={{ fontSize: 14, color: t.textMuted }}>Loading cost records…</span>
      </div>
    );
  }

  // Error state
  if (costsQuery.isError) {
    return (
      <div style={{ display: 'flex', justifyContent: 'center', alignItems: 'center', minHeight: '60vh', backgroundColor: t.bg, flexDirection: 'column', gap: 14 }}>
        <span style={{ fontSize: 14, color: t.error }}>Failed to load cost records</span>
      </div>
    );
  }

  const records = costsQuery.data || [];

  return (
    <div style={{ backgroundColor: t.bg, minHeight: '100vh' }}>
      {/* Header */}
      <div style={{ backgroundColor: t.surface, borderBottom: `1px solid ${t.border}`, padding: '16px' }}>
        <AccountSelector
          accounts={accounts}
          selectedAccount={selectedAccount}
          onSelectAccount={onSelectAccount}
          onConnectAccount={onConnectAccount}
          onEditAccount={onEditAccount}
        />
      </div>

      {/* Period selector */}
      <div style={{ display: 'flex', gap: 8, padding: '16px', backgroundColor: t.bg, borderBottom: `1px solid ${t.border}`, overflowX: 'auto' }}>
        {[7, 30, 90].map(days => (
          <button
            key={days}
            onClick={() => { setPeriod(days); setFilterService(null); }}
            style={{
              padding: '6px 12px',
              borderRadius: 4,
              border: `1px solid ${period === days ? t.accent : t.border}`,
              backgroundColor: period === days ? t.accent : t.surface,
              color: period === days ? t.bg : t.text,
              fontWeight: 600,
              fontSize: 12,
              cursor: 'pointer',
              whiteSpace: 'nowrap',
              flexShrink: 0,
            }}
          >
            {days}d
          </button>
        ))}
      </div>

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
        <div style={{ padding: '16px' }}>
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
              return (
                <div
                  key={idx}
                  style={{
                    display: 'flex',
                    alignItems: 'center',
                    gap: 12,
                    padding: '12px 14px',
                    backgroundColor: t.surface,
                    border: `1px solid ${t.border}`,
                    borderRadius: 8,
                  }}
                >
                  {/* Service color dot */}
                  <div style={{ width: 6, height: 6, borderRadius: '50%', backgroundColor: cfg.color, flexShrink: 0 }} />

                  {/* Service + Region info */}
                  <div style={{ flex: 1, minWidth: 0 }}>
                    <div style={{ fontSize: 13, fontWeight: 600, color: t.text }}>
                      {cfg.label}
                    </div>
                    <div style={{ fontSize: 11, color: t.textMuted, marginTop: 2 }}>
                      {record.region || 'NoRegion'}
                    </div>
                  </div>

                  {/* Period */}
                  <div style={{ fontSize: 11, color: t.textMuted, textAlign: 'right', flexShrink: 0, minWidth: 100 }}>
                    {formatDate(record.period_start)}
                  </div>

                  {/* Amount */}
                  <div style={{ fontSize: 14, fontWeight: 700, color: t.accent, textAlign: 'right', flexShrink: 0, minWidth: 70 }}>
                    ${formatCost(record.amount)}
                  </div>
                </div>
              );
            })}
          </div>
        </div>
      )}
    </div>
  );
}
