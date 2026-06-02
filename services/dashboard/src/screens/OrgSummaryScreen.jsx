import { useMemo } from 'react';
import { useQuery } from '@tanstack/react-query';
import { fetchSummary, fetchCosts, fetchSummaryByAccount } from '../api/client';
import { serviceConfig } from '../components/serviceConfig';
import { Spinner } from '../components/primitives';
import { useBreakpoint } from '../components/primitives/useBreakpoint';

// OrgSummaryScreen — read-only organization summary at `/`. Slices: headline
// tiles (#1) + by-service breakdown (#4) + per-account breakdown (#3) + account
// health strip (#6). Org-wide trend, top-zombies, and member-activity are later.
//
// Deliberately self-contained: it does NOT import the heavy OverviewHero /
// ServiceBreakdown out of the ~1800-line workbench (OverviewScreen.jsx). The
// client-side reductions it needs (totalSpend) are re-implemented here in a
// few lines rather than reaching into the don't-touch file.
//
// This screen only ever renders for orgs with 2+ accounts — the zero-account
// and single-account cases are handled by redirects in pages/OrgSummary.jsx.
export default function OrgSummaryScreen({ accounts = [], onViewAccounts, onSelectAccount }) {
  const { isAtMost } = useBreakpoint();
  const isMobile = isAtMost('sm');

  // Org-wide summary (no account arg → fetchSummary() hits /v1/summary clean).
  const summary = useQuery({ queryKey: ['summary', 'org'], queryFn: () => fetchSummary() });
  // Org-wide spend, 30-day window. fetchCosts(accountId, service, days) — pass
  // null for both account and service to get the unfiltered org total.
  const costs = useQuery({ queryKey: ['costs', 'org', 30], queryFn: () => fetchCosts(null, null, 30) });
  // Per-account waste breakdown (slice-0 endpoint). Only accounts with zombies
  // appear; we overlay the full account list (prop) for the health strip.
  const byAccount = useQuery({ queryKey: ['summary-by-account'], queryFn: fetchSummaryByAccount });

  // #1 "total spend": /v1/costs returns a raw []CostRecord with no org total,
  // so reduce client-side. Re-implemented here (the workbench has its own copy).
  const totalSpend = useMemo(() => {
    return (costs.data ?? []).reduce((a, c) => a + (c.amount || 0), 0);
  }, [costs.data]);

  // Merge per-account waste (keyed by internal_account_id) over the full account
  // list so zero-waste and never-scanned accounts still show in the strip.
  const accountRows = useMemo(() => {
    const waste = new Map(
      (byAccount.data?.accounts ?? []).map((a) => [a.internal_account_id, a]),
    );
    return accounts
      .map((acc) => ({ acc, w: waste.get(acc.id) }))
      .sort((a, b) => (b.w?.potential_monthly_savings ?? 0) - (a.w?.potential_monthly_savings ?? 0));
  }, [accounts, byAccount.data]);

  const loading = summary.isPending || costs.isPending || byAccount.isPending;
  const errored = summary.isError || costs.isError || byAccount.isError;

  if (loading) {
    return (
      <div style={{ display: 'flex', justifyContent: 'center', alignItems: 'center', padding: 80 }}>
        <Spinner size={32} color={'var(--color-accent)'} />
      </div>
    );
  }

  if (errored) {
    return (
      <div style={{ maxWidth: 560, margin: '64px auto', padding: '0 20px', textAlign: 'center' }}>
        <h2 style={{ fontSize: 18, fontWeight: 800, color: 'var(--color-text)', margin: '0 0 8px' }}>
          Couldn’t load the organization summary
        </h2>
        <p style={{ fontSize: 14, color: 'var(--color-text-muted)', lineHeight: 1.5, margin: 0 }}>
          Something went wrong fetching your savings and spend data. Refresh the page to try again.
        </p>
      </div>
    );
  }

  const currency = summary.data?.currency || 'USD';
  const monthlyWaste = summary.data?.potential_monthly_savings ?? 0;
  const zombieCount = summary.data?.total_zombies ?? 0;
  const wasteRatio = totalSpend > 0 ? (monthlyWaste / totalSpend) * 100 : 0;

  const byService = Object.entries(summary.data?.by_service ?? {})
    .sort((a, b) => b[1].savings - a[1].savings);

  // Scans-pending: accounts are connected but nothing has been detected yet
  // (no zombies AND no cost data). Render a clear "results appear after the
  // first scan" state instead of a misleading all-zeros page.
  const scansPending = zombieCount === 0 && totalSpend === 0;

  return (
    <div style={{ padding: isMobile ? '16px' : '24px', maxWidth: 1100, margin: '0 auto' }}>
      <header style={{ display: 'flex', alignItems: 'baseline', justifyContent: 'space-between', gap: 16, marginBottom: 20, flexWrap: 'wrap' }}>
        <div>
          <h1 style={{ fontSize: isMobile ? 20 : 24, fontWeight: 800, color: 'var(--color-text)', margin: '0 0 4px', letterSpacing: -0.5 }}>
            Organization overview
          </h1>
          <p style={{ fontSize: 13, color: 'var(--color-text-muted)', margin: 0 }}>
            Waste and spend across {accounts.length} connected account{accounts.length === 1 ? '' : 's'}.
          </p>
        </div>
        <button
          type="button"
          onClick={onViewAccounts}
          style={{
            cursor: 'pointer',
            background: 'var(--color-accent)',
            color: 'var(--color-text-on-dark)',
            border: 'none',
            borderRadius: 8,
            padding: '10px 16px',
            fontSize: 13,
            fontWeight: 700,
          }}
        >
          View resources →
        </button>
      </header>

      {scansPending ? (
        <ScansPending />
      ) : (
        <>
          <TileGrid
            isMobile={isMobile}
            tiles={[
              { label: 'Monthly waste', value: `${currency} ${monthlyWaste.toFixed(2)}`, accent: true,
                hint: 'Cost of currently-detected idle resources' },
              { label: 'Total zombies', value: String(zombieCount),
                hint: 'Idle / abandoned resources detected' },
              { label: 'Waste ratio', value: `${wasteRatio.toFixed(1)}%`,
                hint: 'Waste as a share of total spend' },
              { label: 'Total spend', value: `${currency} ${totalSpend.toFixed(2)}`,
                hint: 'Last 30 days' },
            ]}
          />

          <ByServiceBreakdown currency={currency} byService={byService} />
        </>
      )}

      {/* Accounts: per-account waste (#3) + health (#6). Rendered even when no
          scan results exist yet — it's the most useful view in that state. */}
      <AccountsSection rows={accountRows} currency={currency} isMobile={isMobile} onSelectAccount={onSelectAccount} />
    </div>
  );
}

function TileGrid({ tiles, isMobile }) {
  return (
    <div
      style={{
        display: 'grid',
        gridTemplateColumns: isMobile ? 'repeat(2, 1fr)' : 'repeat(4, 1fr)',
        gap: 12,
        marginBottom: 28,
      }}
    >
      {tiles.map((t) => (
        <div
          key={t.label}
          style={{
            backgroundColor: 'var(--color-surface)',
            border: '1px solid var(--color-border)',
            borderRadius: 12,
            padding: '16px 18px',
          }}
        >
          <span style={{ fontSize: 11, fontWeight: 600, color: 'var(--color-text-muted)', letterSpacing: 1.2, textTransform: 'uppercase', display: 'block', marginBottom: 8 }}>
            {t.label}
          </span>
          <span style={{ fontSize: 26, fontWeight: 800, color: t.accent ? 'var(--color-accent)' : 'var(--color-text)', letterSpacing: -0.5, display: 'block', fontVariantNumeric: 'tabular-nums' }}>
            {t.value}
          </span>
          <span style={{ fontSize: 11, color: 'var(--color-text-muted)', display: 'block', marginTop: 4, lineHeight: 1.4 }}>
            {t.hint}
          </span>
        </div>
      ))}
    </div>
  );
}

function ByServiceBreakdown({ byService, currency }) {
  const totalSavings = byService.reduce((s, [, d]) => s + (d.savings || 0), 0);

  return (
    <section
      style={{
        backgroundColor: 'var(--color-surface)',
        border: '1px solid var(--color-border)',
        borderRadius: 12,
        overflow: 'hidden',
      }}
    >
      <div style={{ padding: '16px 20px', borderBottom: '1px solid var(--color-border)' }}>
        <span style={{ fontSize: 12, fontWeight: 600, color: 'var(--color-text-muted)', textTransform: 'uppercase', letterSpacing: 0.5 }}>
          Waste by service
        </span>
      </div>

      {byService.length === 0 ? (
        <p style={{ fontSize: 13, color: 'var(--color-text-muted)', margin: 0, padding: '20px' }}>
          No zombie resources detected across your accounts.
        </p>
      ) : (
        <ul style={{ listStyle: 'none', margin: 0, padding: 0 }}>
          {byService.map(([svc, data], idx) => {
            const cfg = serviceConfig(svc);
            const pct = totalSavings > 0 ? (data.savings / totalSavings) * 100 : 0;
            return (
              <li
                key={svc}
                style={{
                  display: 'flex',
                  alignItems: 'center',
                  gap: 12,
                  padding: '12px 20px',
                  borderTop: idx === 0 ? 'none' : '1px solid var(--color-border)',
                }}
              >
                <span style={{ width: 8, height: 8, borderRadius: '50%', backgroundColor: cfg.color, flexShrink: 0 }} />
                <span style={{ flex: 1, minWidth: 0, fontSize: 13, fontWeight: 600, color: 'var(--color-text)', overflow: 'hidden', textOverflow: 'ellipsis', whiteSpace: 'nowrap' }}>
                  {cfg.label}
                </span>
                <span style={{ fontSize: 12, color: 'var(--color-text-muted)', flexShrink: 0, fontVariantNumeric: 'tabular-nums' }}>
                  {data.zombies} resource{data.zombies === 1 ? '' : 's'}
                </span>
                <span style={{ fontSize: 13, fontWeight: 700, color: 'var(--color-text)', flexShrink: 0, minWidth: 92, textAlign: 'right', fontVariantNumeric: 'tabular-nums' }}>
                  {currency} {(data.savings || 0).toFixed(2)}
                  <span style={{ fontWeight: 500, color: 'var(--color-text-muted)', marginLeft: 6 }}>· {pct.toFixed(1)}%</span>
                </span>
              </li>
            );
          })}
        </ul>
      )}
    </section>
  );
}

function AccountsSection({ rows, currency, isMobile, onSelectAccount }) {
  return (
    <section
      style={{
        marginTop: 28,
        backgroundColor: 'var(--color-surface)',
        border: '1px solid var(--color-border)',
        borderRadius: 12,
        overflow: 'hidden',
      }}
    >
      <div style={{ padding: '16px 20px', borderBottom: '1px solid var(--color-border)' }}>
        <span style={{ fontSize: 12, fontWeight: 600, color: 'var(--color-text-muted)', textTransform: 'uppercase', letterSpacing: 0.5 }}>
          Accounts
        </span>
      </div>

      {rows.length === 0 ? (
        <p style={{ fontSize: 13, color: 'var(--color-text-muted)', margin: 0, padding: '20px' }}>
          No connected accounts.
        </p>
      ) : (
        <ul style={{ listStyle: 'none', margin: 0, padding: 0 }}>
          {rows.map(({ acc, w }, idx) => (
            <li key={acc.id} style={{ borderTop: idx === 0 ? 'none' : '1px solid var(--color-border)' }}>
              <button
                type="button"
                onClick={() => onSelectAccount?.(acc.id)}
                style={{
                  display: 'flex',
                  alignItems: 'center',
                  gap: 12,
                  width: '100%',
                  textAlign: 'left',
                  background: 'transparent',
                  border: 'none',
                  cursor: 'pointer',
                  padding: '12px 20px',
                  fontFamily: 'inherit',
                }}
                onMouseEnter={(e) => { e.currentTarget.style.backgroundColor = 'var(--color-bg)'; }}
                onMouseLeave={(e) => { e.currentTarget.style.backgroundColor = 'transparent'; }}
              >
                <div style={{ flex: 1, minWidth: 0 }}>
                  <div style={{ fontSize: 13, fontWeight: 600, color: 'var(--color-text)', overflow: 'hidden', textOverflow: 'ellipsis', whiteSpace: 'nowrap' }}>
                    {acc.label || acc.account_id || acc.id}
                  </div>
                  <div style={{ fontSize: 11, color: 'var(--color-text-muted)', fontVariantNumeric: 'tabular-nums' }}>
                    {acc.account_id || '—'}
                    {acc.status === 'error' && acc.error_message && (
                      <span style={{ color: 'var(--color-error)', marginLeft: 8 }}>· {acc.error_message}</span>
                    )}
                  </div>
                </div>

                {!isMobile && (
                  <div style={{ display: 'flex', flexDirection: 'column', alignItems: 'flex-end', gap: 2, flexShrink: 0, minWidth: 120 }}>
                    <AccountStatus status={acc.status} />
                    <span style={{ fontSize: 11, color: 'var(--color-text-muted)' }}>
                      {formatScanned(acc.last_scanned_at)}
                    </span>
                  </div>
                )}

                <div style={{ flexShrink: 0, textAlign: 'right', minWidth: 92 }}>
                  <div style={{ fontSize: 13, fontWeight: 700, color: 'var(--color-text)', fontVariantNumeric: 'tabular-nums' }}>
                    {currency} {(w?.potential_monthly_savings ?? 0).toFixed(2)}
                  </div>
                  <div style={{ fontSize: 11, color: 'var(--color-text-muted)', fontVariantNumeric: 'tabular-nums' }}>
                    {w?.total_zombies ?? 0} zombie{(w?.total_zombies ?? 0) === 1 ? '' : 's'}
                  </div>
                </div>

                <span aria-hidden style={{ color: 'var(--color-text-muted)', flexShrink: 0 }}>›</span>
              </button>
            </li>
          ))}
        </ul>
      )}
    </section>
  );
}

// AccountStatus — small colored label; mirrors the status-colour idiom used by
// the SSO/account panes (colour carries the cue, no pill chrome).
function AccountStatus({ status }) {
  const color = {
    connected: '#10b981',
    scanning: 'var(--color-accent)',
    error: 'var(--color-error)',
    pending_role_setup: 'var(--color-text-muted)',
  }[status] || 'var(--color-text-muted)';
  return (
    <span style={{ fontSize: 11, fontWeight: 600, color, letterSpacing: 0.2 }}>
      {(status || 'unknown').replace(/_/g, ' ')}
    </span>
  );
}

// formatScanned — last_scanned_at is null until the first scan completes.
function formatScanned(ts) {
  if (!ts) return 'never scanned';
  const d = new Date(ts);
  if (Number.isNaN(d.getTime())) return 'never scanned';
  return `scanned ${d.toLocaleDateString()}`;
}

function ScansPending() {
  return (
    <div
      style={{
        backgroundColor: 'var(--color-surface)',
        border: '1px solid var(--color-border)',
        borderRadius: 12,
        padding: '40px 24px',
        textAlign: 'center',
      }}
    >
      <h2 style={{ fontSize: 16, fontWeight: 800, color: 'var(--color-text)', margin: '0 0 8px' }}>
        No scan results yet
      </h2>
      <p style={{ fontSize: 14, color: 'var(--color-text-muted)', lineHeight: 1.5, margin: '0 auto', maxWidth: 420 }}>
        Your accounts are connected — savings and spend figures appear here after the first scan completes.
      </p>
    </div>
  );
}
