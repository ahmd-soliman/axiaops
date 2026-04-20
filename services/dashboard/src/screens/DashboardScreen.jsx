import { useState, useMemo } from 'react';
import { useQuery } from '@tanstack/react-query';
import { fetchSummary, fetchResources, fetchTrend, scanAccount, fetchDismissals } from '../api/client';
import { serviceConfig } from '../components/serviceConfig';
import AccountSelector from '../components/AccountSelector';
import { useTheme } from '../theme/ThemeContext';
import { Spinner } from '../components/primitives';

function calculateNextScanTime(account) {
  if (!account || account.scan_interval_hours === null || account.scan_interval_hours === undefined) return null;
  if (account.scan_interval_hours === 0) return 'On-demand';
  if (account.scan_interval_hours < 0) return null;
  if (!account.last_scanned_at) return `in ${account.scan_interval_hours}h`;
  const lastScanned = new Date(account.last_scanned_at);
  const nextScan = new Date(lastScanned.getTime() + account.scan_interval_hours * 60 * 60 * 1000);
  const diffMs = nextScan - new Date();
  if (diffMs <= 0) return 'Now';
  const diffHours = Math.floor(diffMs / (60 * 60 * 1000));
  if (diffHours > 0) return `in ${diffHours}h`;
  return `in ${Math.ceil(diffMs / (60 * 1000))}m`;
}

function SavingsSparkline({ snaps, theme }) {
  if (!snaps || snaps.length < 2) return null;
  const W = 220, H = 36, BAR_W = 4, GAP = 2;
  const values = snaps.map((s) => s.total_monthly_cost);
  const maxVal = Math.max(...values, 0.01);
  const maxBars = Math.floor(W / (BAR_W + GAP));
  const visible = values.slice(-maxBars);
  return (
    <div style={{ width: W, height: H, display: 'flex', flexDirection: 'row', alignItems: 'flex-end', marginTop: 8, opacity: 0.85 }}>
      {visible.map((v, i) => {
        const barH = Math.max(3, Math.round((v / maxVal) * H));
        const isLast = i === visible.length - 1;
        return (
          <div key={i} style={{ width: BAR_W, height: barH, backgroundColor: isLast ? theme.accent : `${theme.accent}73`, marginRight: i < visible.length - 1 ? GAP : 0, borderRadius: 1 }} />
        );
      })}
    </div>
  );
}

function Chip({ label, variant, theme }) {
  const bg = variant === 'prod' ? theme.chipProdBg : variant === 'stag' ? theme.chipStagBg : theme.chipBg;
  const color = variant === 'prod' ? theme.chipProdText : variant === 'stag' ? theme.chipStagText : theme.chipText;
  return (
    <div style={{ backgroundColor: bg, paddingLeft: 8, paddingRight: 8, paddingTop: 3, paddingBottom: 3, borderRadius: 4 }}>
      <span style={{ fontSize: 11, color, fontWeight: 500 }}>{label}</span>
    </div>
  );
}

export default function DashboardScreen({ onShowTrend, onSelectGhost, onLogout, orgName, accounts = [], onConnectAccount, onEditAccount, onDeleteAccount, selectedAccount, onSelectAccount }) {
  const { theme, toggleTheme, isDark } = useTheme();
  const t = theme;
  const [filterSvc, setFilterSvc]         = useState(null);
  const [filterOwner, setFilterOwner]     = useState(null);
  const [ghostOnly, setGhostOnly]         = useState(true);
  const [showDismissed, setShowDismissed] = useState(false);
  const [scanning, setScanning]           = useState(null);

  const summary    = useQuery({ queryKey: ['summary', selectedAccount],    queryFn: () => fetchSummary(selectedAccount) });
  const resources  = useQuery({ queryKey: ['resources', selectedAccount],  queryFn: () => fetchResources(selectedAccount) });
  const trend      = useQuery({ queryKey: ['trend'],                       queryFn: () => fetchTrend(null) });
  const dismissals = useQuery({ queryKey: ['dismissals', selectedAccount], queryFn: () => fetchDismissals(selectedAccount) });

  const isLoading    = summary.isLoading || resources.isLoading;
  const isError      = summary.isError   || resources.isError;
  const isRefreshing = summary.isFetching || resources.isFetching;

  const dismissedSet = useMemo(() => {
    const set = new Set();
    (dismissals.data ?? []).forEach((d) => set.add(d.resource_id));
    return set;
  }, [dismissals.data]);

  const owners = useMemo(() => {
    const set = new Set((resources.data ?? []).map((r) => r.owner).filter(Boolean));
    return [...set].sort();
  }, [resources.data]);

  function refresh() {
    summary.refetch(); resources.refetch(); trend.refetch(); dismissals.refetch();
  }

  function handleExportCSV() {
    let list = resources.data ?? [];
    if (ghostOnly) list = list.filter((r) => r.is_ghost);
    list = list.filter((r) => !dismissedSet.has(r.resource_id));
    if (filterSvc) list = list.filter((r) => r.service === filterSvc);
    if (filterOwner) list = list.filter((r) => r.owner === filterOwner);
    const headers = ['resource_id', 'service', 'region', 'monthly_cost', 'currency', 'usage_metric', 'usage_avg', 'usage_unit', 'owner', 'is_ghost', 'reason'];
    const rows = list.map((r) => [
      r.resource_id, r.service, r.region, r.monthly_cost.toFixed(2), r.currency,
      r.usage_metric ?? '', r.usage_avg ?? '', r.usage_unit ?? '',
      r.owner ?? '', r.is_ghost ? 'true' : 'false', r.reason ?? '',
    ].map((v) => `"${String(v).replace(/"/g, '""')}"`).join(','));
    const csv = [headers.join(','), ...rows].join('\n');
    const blob = new Blob([csv], { type: 'text/csv' });
    const url = URL.createObjectURL(blob);
    const a = document.createElement('a');
    a.href = url;
    a.download = `axiaops-ghosts-${new Date().toISOString().slice(0, 10)}.csv`;
    a.click();
    URL.revokeObjectURL(url);
  }

  async function handleScan(accountId) {
    setScanning(accountId);
    try {
      await scanAccount(accountId);
      setTimeout(() => { refresh(); setScanning(null); }, 3000);
    } catch {
      setScanning(null);
    }
  }

  if (isLoading) {
    return (
      <div style={{ display: 'flex', flex: 1, justifyContent: 'center', alignItems: 'center', backgroundColor: t.bg, minHeight: '100vh', flexDirection: 'column', gap: 14 }}>
        <Spinner size={32} color={t.accent} />
        <span style={{ color: t.textMuted, fontSize: 14 }}>Analysing resources…</span>
      </div>
    );
  }

  if (isError) {
    return (
      <div style={{ display: 'flex', flex: 1, justifyContent: 'center', alignItems: 'center', backgroundColor: t.bg, minHeight: '100vh', flexDirection: 'column', gap: 12, padding: 32 }}>
        <span style={{ fontSize: 32, color: t.textMuted }}>⚠</span>
        <span style={{ fontSize: 17, fontWeight: 700, color: t.text }}>Service unavailable</span>
        <span style={{ fontSize: 13, color: t.textMuted, textAlign: 'center', lineHeight: '20px' }}>Make sure the ingestion service is running on localhost:8080</span>
        <button onClick={refresh} style={{ marginTop: 20, backgroundColor: t.accent, paddingLeft: 28, paddingRight: 28, paddingTop: 11, paddingBottom: 11, borderRadius: 8, border: 'none', cursor: 'pointer' }}>
          <span style={{ color: t.white, fontWeight: 700, fontSize: 14 }}>Retry</span>
        </button>
      </div>
    );
  }

  const byService = Object.entries(summary.data.by_service ?? {}).sort((a, b) => b[1].savings - a[1].savings);

  const listData = (() => {
    if (showDismissed) return dismissals.data ?? [];
    let list = resources.data ?? [];
    if (ghostOnly) list = list.filter((r) => r.is_ghost);
    list = list.filter((r) => !dismissedSet.has(r.resource_id));
    if (filterSvc) list = list.filter((r) => r.service === filterSvc);
    if (filterOwner) list = list.filter((r) => r.owner === filterOwner);
    return list;
  })();

  return (
    <div style={{ flex: 1, backgroundColor: t.bg, minHeight: '100vh', overflowY: 'auto' }}>
      {/* Navbar */}
      <div style={{ backgroundColor: t.surface, paddingLeft: 20, paddingRight: 20, paddingTop: 15, paddingBottom: 15, display: 'flex', flexDirection: 'row', alignItems: 'center', gap: 10 }}>
        <span style={{ color: t.accent, fontSize: 18, fontWeight: 800, letterSpacing: 0.3 }}>AxiaOps</span>
        {accounts.length > 0 && (
          <AccountSelector
            accounts={accounts}
            selectedAccount={selectedAccount}
            onSelectAccount={onSelectAccount}
            onConnectAccount={onConnectAccount}
            onEditAccount={onEditAccount}
            onScanAccount={handleScan}
            scanning={scanning}
          />
        )}
        <div style={{ flex: 1 }} />
        <button onClick={toggleTheme} style={{ paddingLeft: 8, paddingRight: 8, paddingTop: 4, paddingBottom: 4, background: 'none', border: 'none', cursor: 'pointer', marginRight: 8 }}>
          <svg xmlns="http://www.w3.org/2000/svg" width="20" height="20" viewBox="0 0 24 24" fill="none" stroke={t.text} strokeWidth="2" strokeLinecap="round" strokeLinejoin="round">
            {isDark ? (
              <>
                <path d="M8 12a4 4 0 1 0 8 0a4 4 0 1 0 -8 0" />
                <path d="M3 12h1m8 -9v1m8 8h1m-9 8v1m-6.4 -15.4l.7 .7m12.1 -.7l-.7 .7m0 11.4l.7 .7m-12.1 -.7l-.7 .7" />
              </>
            ) : (
              <path d="M12 3c.132 0 .263 0 .393 0a7.5 7.5 0 0 0 7.92 12.446a9 9 0 1 1 -8.313 -12.454z" />
            )}
          </svg>
        </button>
        {orgName ? (
          <div style={{ backgroundColor: t.surfaceRaised, paddingLeft: 10, paddingRight: 10, paddingTop: 4, paddingBottom: 4, borderRadius: 5, marginRight: 8 }}>
            <span style={{ color: t.textMid, fontSize: 12, fontWeight: 600 }}>{orgName}</span>
          </div>
        ) : null}
        <button onClick={onLogout} style={{ paddingLeft: 10, paddingRight: 10, paddingTop: 4, paddingBottom: 4, borderRadius: 5, border: `1px solid ${t.border}`, background: 'none', cursor: 'pointer' }}>
          <span style={{ color: t.textMuted, fontSize: 12, fontWeight: 600 }}>Sign out</span>
        </button>
        {isRefreshing && <Spinner size={16} color={t.accent} />}
        <button onClick={refresh} style={{ paddingLeft: 8, paddingRight: 8, paddingTop: 4, paddingBottom: 4, background: 'none', border: 'none', cursor: 'pointer' }}>
          <span style={{ color: t.textMuted, fontSize: 12 }}>↻</span>
        </button>
      </div>

      {/* Connect prompt */}
      {accounts.length === 0 && (
        <div style={{ backgroundColor: t.surfaceAlt, paddingLeft: 16, paddingRight: 16, paddingTop: 16, paddingBottom: 16, display: 'flex', flexDirection: 'column', alignItems: 'center' }}>
          <span style={{ color: t.textSub, fontSize: 14, marginBottom: 12 }}>Connect your first AWS account to get started</span>
          <button onClick={onConnectAccount} style={{ border: `1px dashed ${t.accent}`, borderRadius: 8, paddingLeft: 16, paddingRight: 16, paddingTop: 8, paddingBottom: 8, background: 'none', cursor: 'pointer' }}>
            <span style={{ color: t.accent, fontSize: 13, fontWeight: 600 }}>+ Connect AWS Account</span>
          </button>
        </div>
      )}

      {/* Hero */}
      <div style={{ backgroundColor: t.surfaceAlt, paddingLeft: 20, paddingRight: 20, paddingTop: 28, paddingBottom: 24 }}>
        <span style={{ color: t.textMuted, fontSize: 11, fontWeight: 600, letterSpacing: 1.5, textTransform: 'uppercase', marginBottom: 6, display: 'block' }}>Potential Monthly Savings</span>
        <span style={{ color: t.accent, fontSize: 46, fontWeight: 800, letterSpacing: -1, display: 'block' }}>
          {summary.data.currency} {summary.data.potential_monthly_savings.toFixed(2)}
        </span>
        <span style={{ color: t.textSub, fontSize: 13, marginTop: 4, marginBottom: 4, display: 'block' }}>
          {summary.data.total_ghosts} zombie resource{summary.data.total_ghosts !== 1 ? 's' : ''} detected across your accounts
        </span>

        <button onClick={() => onShowTrend && onShowTrend()} style={{ background: 'none', border: 'none', padding: 0, cursor: 'pointer', textAlign: 'left' }}>
          <SavingsSparkline snaps={trend.data} theme={t} />
          {trend.data && trend.data.length >= 2 && (
            <span style={{ color: t.textSub, fontSize: 10, marginTop: 4, marginBottom: 16, display: 'block' }}>Savings trend ({trend.data.length} scans)</span>
          )}
        </button>

        {/* Service pills */}
        <div style={{ display: 'flex', flexDirection: 'row', overflowX: 'auto', gap: 8, marginTop: 4, paddingBottom: 4 }}>
          {byService.map(([svc, data]) => {
            const cfg = serviceConfig(svc);
            const active = filterSvc === svc;
            return (
              <button
                key={svc}
                onClick={() => setFilterSvc(active ? null : svc)}
                style={{ display: 'flex', flexDirection: 'row', alignItems: 'center', gap: 6, backgroundColor: active ? t.accent : t.surfaceRaised, borderRadius: 20, paddingLeft: 12, paddingRight: 12, paddingTop: 6, paddingBottom: 6, border: 'none', cursor: 'pointer', flexShrink: 0 }}
              >
                <div style={{ width: 6, height: 6, borderRadius: '50%', backgroundColor: cfg.color }} />
                <span style={{ color: t.text, fontSize: 12, fontWeight: 700 }}>{cfg.label}</span>
                <span style={{ color: t.textMuted, fontSize: 12 }}>{summary.data.currency}{data.savings.toFixed(0)}</span>
              </button>
            );
          })}
        </div>

        {/* Owner pills */}
        {owners.length > 1 && (
          <div style={{ display: 'flex', flexDirection: 'row', overflowX: 'auto', gap: 6, marginTop: 10, paddingBottom: 4 }}>
            {owners.map((owner) => {
              const active = filterOwner === owner;
              return (
                <button
                  key={owner}
                  onClick={() => setFilterOwner(active ? null : owner)}
                  style={{ backgroundColor: active ? t.navy : t.surfaceRaised, borderRadius: 20, paddingLeft: 12, paddingRight: 12, paddingTop: 5, paddingBottom: 5, border: `1px solid ${active ? t.navy : t.border}`, cursor: 'pointer', flexShrink: 0 }}
                >
                  <span style={{ fontSize: 12, fontWeight: 600, color: active ? t.textOnDark : t.textMid }}>👤 {owner}</span>
                </button>
              );
            })}
          </div>
        )}
      </div>

      {/* Toggle row */}
      <div style={{ display: 'flex', flexDirection: 'row', paddingLeft: 16, paddingRight: 16, paddingTop: 16, gap: 8 }}>
        {[
          { label: 'Ghost Resources', active: ghostOnly && !showDismissed, onClick: () => { setGhostOnly(true); setShowDismissed(false); } },
          { label: 'All Resources', active: !ghostOnly && !showDismissed, onClick: () => { setGhostOnly(false); setShowDismissed(false); } },
        ].map(({ label, active, onClick }) => (
          <button key={label} onClick={onClick} style={{ paddingLeft: 14, paddingRight: 14, paddingTop: 7, paddingBottom: 7, borderRadius: 20, backgroundColor: active ? t.navy : t.surfaceRaised, border: 'none', cursor: 'pointer' }}>
            <span style={{ fontSize: 13, fontWeight: 600, color: active ? t.textOnDark : t.textMid }}>{label}</span>
          </button>
        ))}
        {(dismissals.data?.length ?? 0) > 0 && (
          <button onClick={() => setShowDismissed((v) => !v)} style={{ paddingLeft: 14, paddingRight: 14, paddingTop: 7, paddingBottom: 7, borderRadius: 20, backgroundColor: showDismissed ? '#374151' : t.surfaceRaised, border: 'none', cursor: 'pointer' }}>
            <span style={{ fontSize: 13, fontWeight: 600, color: showDismissed ? t.textOnDark : t.textMid }}>Dismissed ({dismissals.data?.length ?? 0})</span>
          </button>
        )}
      </div>

      {/* Section header */}
      <div style={{ display: 'flex', flexDirection: 'row', alignItems: 'center', paddingLeft: 16, paddingRight: 16, paddingTop: 20, paddingBottom: 10 }}>
        <span style={{ flex: 1, fontSize: 11, fontWeight: 700, color: t.textMuted, letterSpacing: 1.5, textTransform: 'uppercase' }}>
          {showDismissed ? 'Dismissed Resources' : ghostOnly ? 'Ghost Resources' : 'All Resources'}
        </span>
        {!showDismissed && (
          <button onClick={handleExportCSV} style={{ paddingLeft: 10, paddingRight: 10, paddingTop: 4, paddingBottom: 4, borderRadius: 6, border: `1px solid ${t.border}`, backgroundColor: t.surfaceRaised, cursor: 'pointer' }}>
            <span style={{ fontSize: 11, fontWeight: 700, color: t.textMid }}>↓ CSV</span>
          </button>
        )}
      </div>

      {/* Resource list */}
      <div style={{ paddingBottom: 40 }}>
        {listData.map((item, idx) => {
          if (showDismissed) {
            const cfg = serviceConfig(item.service);
            const reasonLabel = { intentional: 'Intentional', scheduled_deletion: 'Scheduled deletion', false_positive: 'False positive', cost_accepted: 'Cost accepted', other: 'Other' }[item.reason] ?? item.reason;
            const isSnoozed = item.action === 'snooze';
            return (
              <div key={String(item.id)} style={{ backgroundColor: t.card, marginLeft: 16, marginRight: 16, marginBottom: 8, borderRadius: 10, padding: 16, borderLeft: `4px solid ${cfg.color}`, opacity: 0.75 }}>
                <div style={{ display: 'flex', flexDirection: 'row', alignItems: 'center', gap: 8, marginBottom: 6 }}>
                  <div style={{ paddingLeft: 7, paddingRight: 7, paddingTop: 3, paddingBottom: 3, borderRadius: 5, backgroundColor: isDark ? cfg.darkBg : cfg.bg }}>
                    <span style={{ fontSize: 11, fontWeight: 800, color: cfg.color }}>{cfg.label}</span>
                  </div>
                  <div style={{ backgroundColor: isSnoozed ? (isDark ? '#1e3a5f' : '#DBEAFE') : (isDark ? '#374151' : '#F3F4F6'), paddingLeft: 6, paddingRight: 6, paddingTop: 2, paddingBottom: 2, borderRadius: 4 }}>
                    <span style={{ fontSize: 10, fontWeight: 700, color: isSnoozed ? '#60a5fa' : '#9CA3AF' }}>{isSnoozed ? 'snoozed' : 'dismissed'}</span>
                  </div>
                </div>
                <span style={{ fontSize: 11, color: t.textMuted, fontFamily: 'monospace', marginBottom: 10, display: 'block', overflow: 'hidden', textOverflow: 'ellipsis', whiteSpace: 'nowrap' }}>{item.resource_id}</span>
                <div style={{ display: 'flex', flexDirection: 'row', alignItems: 'center', gap: 6, marginBottom: 10 }}>
                  <Chip label={item.region} theme={t} />
                  <Chip label={reasonLabel} theme={t} />
                </div>
                {item.note ? <span style={{ fontSize: 12, color: t.textMid, fontStyle: 'italic', lineHeight: '18px', display: 'block' }}>"{item.note}"</span> : null}
                {isSnoozed && item.snoozed_until && (
                  <span style={{ fontSize: 12, color: t.textMuted, lineHeight: '18px', display: 'block' }}>
                    Until {new Date(item.snoozed_until).toLocaleDateString('en-GB', { day: 'numeric', month: 'short', year: 'numeric' })}
                  </span>
                )}
              </div>
            );
          }

          const cfg = serviceConfig(item.service);
          const isProd = item.tags?.env === 'production';
          return (
            <button
              key={item.resource_id}
              onClick={() => onSelectGhost(item)}
              style={{ backgroundColor: t.card, marginLeft: 16, marginRight: 16, marginBottom: 8, borderRadius: 10, padding: 16, borderLeft: `4px solid ${cfg.color}`, display: 'flex', flexDirection: 'column', width: 'calc(100% - 32px)', textAlign: 'left', cursor: 'pointer', boxShadow: '0px 2px 6px rgba(0,0,0,0.05)' }}
            >
              <div style={{ display: 'flex', flexDirection: 'row', alignItems: 'center', gap: 8, marginBottom: 6 }}>
                <div style={{ paddingLeft: 7, paddingRight: 7, paddingTop: 3, paddingBottom: 3, borderRadius: 5, backgroundColor: t.surfaceRaised, border: `1px solid ${t.border}` }}>
                  <span style={{ fontSize: 11, fontWeight: 800, color: cfg.color }}>{cfg.label}</span>
                </div>
                {item.is_ghost && (
                  <div style={{ backgroundColor: t.surfaceRaised, border: `1px solid ${t.border}`, paddingLeft: 6, paddingRight: 6, paddingTop: 2, paddingBottom: 2, borderRadius: 4 }}>
                    <span style={{ fontSize: 10, fontWeight: 700, color: t.ghostBadgeText }}>zombie</span>
                  </div>
                )}
                <div style={{ flex: 1 }} />
                <span style={{ fontSize: 14, fontWeight: 800, color: t.accent }}>{item.currency} {item.monthly_cost.toFixed(2)}</span>
              </div>
              <span style={{ fontSize: 11, color: t.textMuted, fontFamily: 'monospace', marginBottom: 10, display: 'block', overflow: 'hidden', textOverflow: 'ellipsis', whiteSpace: 'nowrap' }}>{item.resource_id}</span>
              <div style={{ display: 'flex', flexDirection: 'row', alignItems: 'center', gap: 6, marginBottom: 10 }}>
                <Chip label={item.region} theme={t} />
                <Chip label={item.tags?.env ?? 'unknown'} variant={isProd ? 'prod' : 'stag'} theme={t} />
                <span style={{ fontSize: 11, color: t.textMuted, marginLeft: 'auto' }}>👤 {item.owner}</span>
              </div>
              {item.is_ghost
                ? <span style={{ fontSize: 12, color: t.textMid, fontStyle: 'italic', lineHeight: '18px', display: 'block' }}>{item.reason}</span>
                : item.usage_metric
                  ? <span style={{ fontSize: 12, color: t.textMuted, lineHeight: '18px', display: 'block' }}>{item.usage_metric}: {item.usage_avg.toFixed(2)} {item.usage_unit}</span>
                  : null
              }
            </button>
          );
        })}
      </div>
    </div>
  );
}
