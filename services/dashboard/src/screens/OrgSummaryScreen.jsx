import { useMemo } from 'react';
import { useQuery } from '@tanstack/react-query';
import { fetchSummary, fetchCosts, fetchSummaryByAccount, fetchTrend, fetchZombies, fetchAuditEvents } from '../api/client';
import { serviceConfig } from '../components/serviceConfig';
import { Spinner, useWindowWidth, LinkButton, RowLink } from '../components/primitives';
import { useBreakpoint } from '../components/primitives/useBreakpoint';
import AreaChart from '../components/AreaChart';
import { sumCostRecords } from '../utils/costTotals';
import { STATUS_HINT } from '../utils/accountStatus';

// OrgSummaryScreen — read-only organization summary at `/`. Slices: headline
// tiles (#1) + by-service breakdown (#4) + per-account breakdown (#3) + account
// health strip (#6). Org-wide trend, top-zombies, and member-activity are later.
//
// Deliberately self-contained: it does NOT import the heavy OverviewHero /
// ServiceBreakdown out of the ~1800-line workbench (OverviewScreen.jsx). The
// client-side reductions it needs (totalSpend) use the shared costTotals
// helper rather than reaching into the don't-touch file.
//
// This screen only ever renders for orgs with 2+ accounts — the zero-account
// and single-account cases are handled by redirects in pages/OrgSummary.jsx.
export default function OrgSummaryScreen({ accounts = [], viewAccountsHref, accountHref, zombieHref, serviceHref, auditHref, trendsHref }) {
  const { isAtMost } = useBreakpoint();
  const isMobile = isAtMost('sm');
  const windowWidth = useWindowWidth();

  // Org-wide summary (no account arg → fetchSummary() hits /v1/summary clean).
  const summary = useQuery({ queryKey: ['summary', 'org'], queryFn: () => fetchSummary() });
  // Org-wide spend, 30-day window. fetchCosts(accountId, service, days) — pass
  // null for both account and service to get the unfiltered org total.
  const costs = useQuery({ queryKey: ['costs', 'org', 30], queryFn: () => fetchCosts(null, null, 30) });
  // Per-account waste breakdown (slice-0 endpoint). Only accounts with zombies
  // appear; we overlay the full account list (prop) for the health strip.
  const byAccount = useQuery({ queryKey: ['summary', 'by-account'], queryFn: fetchSummaryByAccount });
  // Org-wide trend (#2) and zombie list (#5) — both no-account → org-wide. Each
  // loads independently and renders its own state; they do NOT gate the page.
  const trend = useQuery({ queryKey: ['trend', 'org'], queryFn: () => fetchTrend() });
  const zombies = useQuery({ queryKey: ['zombies', 'org'], queryFn: () => fetchZombies() });
  // Recent org activity (#7) — last few audit events. Independent of scans
  // (account/member events exist before any scan), so rendered unconditionally.
  const activity = useQuery({ queryKey: ['audit', 'recent', 5], queryFn: () => fetchAuditEvents({ limit: 5 }) });

  // /v1/trend returns one row per (account, scan); roll up to one point per day
  // for an org-wide line (same approach as TrendScreen's aggregateToDays). Keep
  // the first row's FULL ISO snapshot_at (not the bare 'YYYY-MM-DD' slice) so
  // AreaChart's formatDate stays timezone-anchored — a date-only string parses
  // as UTC midnight and shifts the label a day back in UTC-negative zones. Carry
  // zombie_count too so the chart tooltip can show it.
  const trendDays = useMemo(() => {
    const byDay = new Map();
    for (const s of trend.data ?? []) {
      const day = s.snapshot_at.slice(0, 10);
      const existing = byDay.get(day);
      if (existing) {
        existing.total_monthly_cost += s.total_monthly_cost ?? 0;
        existing.zombie_count += s.zombie_count ?? 0;
      } else {
        byDay.set(day, { ...s, total_monthly_cost: s.total_monthly_cost ?? 0, zombie_count: s.zombie_count ?? 0 });
      }
    }
    return [...byDay.values()].sort((a, b) => a.snapshot_at.localeCompare(b.snapshot_at));
  }, [trend.data]);

  // Top zombies by monthly cost, capped — each links into the detail view.
  const topZombies = useMemo(() => {
    return [...(zombies.data ?? [])]
      .sort((a, b) => (b.monthly_cost ?? 0) - (a.monthly_cost ?? 0))
      .slice(0, 8);
  }, [zombies.data]);

  // #1 "total spend": /v1/costs returns a raw []CostRecord with no org total,
  // so reduce client-side via the shared helper (CloudSpendScreen has the
  // canonical copy) -- it must exclude resource-level rows or every dollar
  // with resource-level attribution gets counted twice (once as a general
  // service/day row, once per resource).
  const totalSpend = useMemo(() => sumCostRecords(costs.data), [costs.data]);

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

  // Per-service savings breakdown, sorted high→low. Memoized (and placed with
  // the other derivations, above the early returns) so it isn't rebuilt on
  // every re-render.
  const byService = useMemo(
    () => Object.entries(summary.data?.by_service ?? {})
      .sort((a, b) => b[1].savings - a[1].savings),
    [summary.data],
  );

  // The per-account section loads independently — it must NOT gate the whole
  // page (the tiles + by-service shouldn't wait on it, and a transient error on
  // this secondary endpoint shouldn't blank a page whose core data loaded fine).
  const loading = summary.isPending || costs.isPending;
  const errored = summary.isError || costs.isError;

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

  // Scans-pending: accounts are connected but nothing has been detected yet
  // (no zombies AND no cost data). Render a clear "results appear after the
  // first scan" state instead of a misleading all-zeros page.
  const scansPending = zombieCount === 0 && totalSpend === 0;

  return (
    <div style={{ padding: isMobile ? '16px' : '24px' }}>
      <header style={{ display: 'flex', alignItems: 'baseline', justifyContent: 'space-between', gap: 16, marginBottom: 20, flexWrap: 'wrap' }}>
        <div>
          <h1 style={{ fontSize: isMobile ? 20 : 24, fontWeight: 800, color: 'var(--color-text)', margin: '0 0 4px', letterSpacing: -0.5 }}>
            Organization overview
          </h1>
          <p style={{ fontSize: 13, color: 'var(--color-text-muted)', margin: 0 }}>
            Waste and spend across {accounts.length} connected account{accounts.length === 1 ? '' : 's'}.
          </p>
        </div>
        <LinkButton
          to={viewAccountsHref}
          style={{
            background: 'var(--color-accent)',
            color: 'var(--color-text-on-dark)',
            borderRadius: 8,
            padding: '10px 16px',
            fontSize: 13,
            fontWeight: 700,
          }}
        >
          View resources →
        </LinkButton>
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

          <TrendChart days={trendDays} pending={trend.isPending} error={trend.isError} screenWidth={windowWidth - (isMobile ? 32 : 48)} trendsHref={trendsHref} />

          <ByServiceBreakdown currency={currency} byService={byService} serviceHref={serviceHref} />

          <TopZombies
            rows={topZombies}
            currency={currency}
            pending={zombies.isPending}
            error={zombies.isError}
            zombieHref={zombieHref}
          />
        </>
      )}

      {/* Accounts: per-account waste (#3) + health (#6). Loads independently of
          the tiles; renders even when no scan results exist yet (health is the
          most useful view in that state). */}
      <AccountsSection
        rows={accountRows}
        currency={currency}
        isMobile={isMobile}
        accountHref={accountHref}
        wastePending={byAccount.isPending}
        wasteError={byAccount.isError}
      />

      <MemberActivity
        events={activity.data?.events ?? []}
        pending={activity.isPending}
        error={activity.isError}
        auditHref={auditHref}
      />
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

function ByServiceBreakdown({ byService, currency, serviceHref }) {
  const totalSavings = byService.reduce((s, [, d]) => s + (d.savings || 0), 0);

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
              <li key={svc} style={{ borderTop: idx === 0 ? 'none' : '1px solid var(--color-border)' }}>
                <RowLink
                  to={serviceHref(svc)}
                  title={`View ${cfg.label} resources`}
                  style={{
                    alignItems: 'center', gap: 12, textAlign: 'left', padding: '12px 20px',
                  }}
                  onMouseEnter={(e) => { e.currentTarget.style.backgroundColor = 'var(--color-bg)'; }}
                  onMouseLeave={(e) => { e.currentTarget.style.backgroundColor = 'transparent'; }}
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
                  <span aria-hidden style={{ color: 'var(--color-text-muted)', flexShrink: 0 }}>›</span>
                </RowLink>
              </li>
            );
          })}
        </ul>
      )}
    </section>
  );
}

function TrendChart({ days, pending, error, screenWidth, trendsHref }) {
  let body;
  if (pending) {
    body = <div style={{ padding: 32, textAlign: 'center' }}><Spinner size={24} color={'var(--color-accent)'} /></div>;
  } else if (error) {
    body = <p style={sectionMuted}>Couldn’t load the trend.</p>;
  } else if (days.length < 2) {
    body = <p style={sectionMuted}>Not enough scan history yet to chart a trend — it builds up as scans run.</p>;
  } else {
    body = <div style={{ padding: '8px 4px' }}><AreaChart data={days} screenWidth={screenWidth} /></div>;
  }
  // This chart is a glanceable org-wide summary; the filterable trend view
  // (by service / resource-type / account + date ranges) is the dedicated
  // /trend screen. Link to it rather than duplicating its filters here.
  const action = trendsHref && (
    <LinkButton
      to={trendsHref}
      style={{ fontSize: 12, fontWeight: 600, color: 'var(--color-accent)' }}
    >
      View trends →
    </LinkButton>
  );
  // Range caption — only when there's a chartable series. `days` is
  // oldest-first (one point per day), so [0] is the start and [last] the end.
  const rangeLabel = (!pending && !error && days.length >= 2)
    ? `${humanizeSpan(days.length)} · ${formatRangeLabel(days[0].snapshot_at, days[days.length - 1].snapshot_at)}`
    : null;
  return <SectionShell title="Waste over time" subtitle={rangeLabel} action={action}>{body}</SectionShell>;
}

function TopZombies({ rows, currency, pending, error, zombieHref }) {
  let body;
  if (pending) {
    body = <div style={{ padding: 32, textAlign: 'center' }}><Spinner size={24} color={'var(--color-accent)'} /></div>;
  } else if (error) {
    body = <p style={sectionMuted}>Couldn’t load zombie resources.</p>;
  } else if (rows.length === 0) {
    body = <p style={sectionMuted}>No zombie resources detected across your accounts.</p>;
  } else {
    body = (
      <ul style={{ listStyle: 'none', margin: 0, padding: 0 }}>
        {rows.map((z, idx) => {
          const cfg = serviceConfig(z.service);
          return (
            <li key={`${z.internal_account_id}:${z.service}:${z.region}:${z.resource_id}`} style={{ borderTop: idx === 0 ? 'none' : '1px solid var(--color-border)' }}>
              <RowLink
                to={zombieHref(z)}
                style={{
                  alignItems: 'center', gap: 12, textAlign: 'left', padding: '12px 20px',
                }}
                onMouseEnter={(e) => { e.currentTarget.style.backgroundColor = 'var(--color-bg)'; }}
                onMouseLeave={(e) => { e.currentTarget.style.backgroundColor = 'transparent'; }}
              >
                <span style={{ width: 8, height: 8, borderRadius: '50%', backgroundColor: cfg.color, flexShrink: 0 }} />
                <div style={{ flex: 1, minWidth: 0 }}>
                  <div style={{ fontSize: 13, fontWeight: 600, color: 'var(--color-text)', overflow: 'hidden', textOverflow: 'ellipsis', whiteSpace: 'nowrap' }}>
                    {z.resource_id}
                  </div>
                  <div style={{ fontSize: 11, color: 'var(--color-text-muted)' }}>
                    {cfg.label}{z.resource_type ? ` · ${z.resource_type}` : ''} · {z.region}
                  </div>
                </div>
                <span style={{ fontSize: 13, fontWeight: 700, color: 'var(--color-text)', flexShrink: 0, fontVariantNumeric: 'tabular-nums' }}>
                  {currency} {(z.monthly_cost ?? 0).toFixed(2)}
                </span>
                <span aria-hidden style={{ color: 'var(--color-text-muted)', flexShrink: 0 }}>›</span>
              </RowLink>
            </li>
          );
        })}
      </ul>
    );
  }
  return <SectionShell title="Top zombies by cost">{body}</SectionShell>;
}

function MemberActivity({ events, pending, error, auditHref }) {
  let body;
  if (pending) {
    body = <div style={{ padding: 32, textAlign: 'center' }}><Spinner size={24} color={'var(--color-accent)'} /></div>;
  } else if (error) {
    body = <p style={sectionMuted}>Couldn’t load recent activity.</p>;
  } else if (events.length === 0) {
    body = <p style={sectionMuted}>No recent activity.</p>;
  } else {
    body = (
      <ul style={{ listStyle: 'none', margin: 0, padding: 0 }}>
        {events.map((e, idx) => {
          // actor_name/actor_email are denormalised on the row — no lookup.
          // Mirror AuditScreen's rule: a missing actor_email IS a system action
          // (regardless of actor_name); otherwise prefer the name over the email.
          const actor = e.actor_email ? (e.actor_name || e.actor_email) : 'system';
          return (
            <li
              key={e.id}
              style={{
                display: 'flex', alignItems: 'baseline', gap: 8, padding: '12px 20px',
                borderTop: idx === 0 ? 'none' : '1px solid var(--color-border)',
              }}
            >
              <div style={{ flex: 1, minWidth: 0, fontSize: 13, color: 'var(--color-text)', overflow: 'hidden', textOverflow: 'ellipsis', whiteSpace: 'nowrap' }}>
                <span style={{ fontWeight: 600 }}>{actor}</span>
                <span style={{ color: 'var(--color-text-muted)' }}> · {(e.action || '').replace(/_/g, ' ')}</span>
                {e.resource_type && <span style={{ color: 'var(--color-text-muted)' }}> · {e.resource_type}</span>}
              </div>
              <span style={{ fontSize: 11, color: 'var(--color-text-muted)', flexShrink: 0 }}>{timeAgo(e.created_at)}</span>
            </li>
          );
        })}
      </ul>
    );
  }
  // The whole audit log is the drill-down for activity — link the section, not
  // each (heterogeneous, often target-less) row.
  const action = auditHref && (
    <LinkButton
      to={auditHref}
      style={{ fontSize: 12, fontWeight: 600, color: 'var(--color-accent)' }}
    >
      View all →
    </LinkButton>
  );
  return <SectionShell title="Recent activity" action={action}>{body}</SectionShell>;
}

// timeAgo — compact relative time; falls back to a short date past a week.
function timeAgo(ts) {
  if (!ts) return '';
  const d = new Date(ts);
  if (Number.isNaN(d.getTime())) return '';
  const secs = Math.round((Date.now() - d.getTime()) / 1000);
  if (secs < 60) return 'just now';
  const mins = Math.round(secs / 60);
  if (mins < 60) return `${mins}m ago`;
  const hrs = Math.round(mins / 60);
  if (hrs < 24) return `${hrs}h ago`;
  const days = Math.round(hrs / 24);
  if (days < 7) return `${days}d ago`;
  return d.toLocaleDateString(undefined, { month: 'short', day: 'numeric' });
}

// SectionShell — shared card chrome for the trend + top-zombies sections.
function SectionShell({ title, subtitle, action, children }) {
  return (
    <section style={{ marginTop: 28, backgroundColor: 'var(--color-surface)', border: '1px solid var(--color-border)', borderRadius: 12, overflow: 'hidden' }}>
      <div style={{ padding: '16px 20px', borderBottom: '1px solid var(--color-border)', display: 'flex', alignItems: 'center', justifyContent: 'space-between', gap: 12 }}>
        <div style={{ display: 'flex', alignItems: 'baseline', gap: 8, flexWrap: 'wrap', minWidth: 0 }}>
          <span style={{ fontSize: 12, fontWeight: 600, color: 'var(--color-text-muted)', textTransform: 'uppercase', letterSpacing: 0.5 }}>
            {title}
          </span>
          {subtitle && (
            <span style={{ fontSize: 11, fontWeight: 500, color: 'var(--color-text-sub)' }}>
              {subtitle}
            </span>
          )}
        </div>
        {action}
      </div>
      {children}
    </section>
  );
}

// Humanize a span (in days) to the largest sensible unit — "1 year",
// "3 months", "12 days" — so the caption reads at a glance instead of making
// the reader divide 365 in their head.
function humanizeSpan(days) {
  if (days >= 345) {
    const years = Math.round(days / 365);
    return `${years} year${years === 1 ? '' : 's'}`;
  }
  if (days >= 45) {
    const months = Math.round(days / 30);
    return `${months} month${months === 1 ? '' : 's'}`;
  }
  return `${days} day${days === 1 ? '' : 's'}`;
}

// Compact span label for the Waste-over-time card header, e.g.
// "12 Mar – 9 Jun 2026". The year always shows on the end date (this is the
// fix for the "is this a whole year of data?" ambiguity) and on the start
// too when the range straddles a year boundary.
function formatRangeLabel(startIso, endIso) {
  const s = new Date(startIso);
  const e = new Date(endIso);
  const base = { day: 'numeric', month: 'short' };
  const sameYear = s.getFullYear() === e.getFullYear();
  const start = s.toLocaleDateString('en-GB', sameYear ? base : { ...base, year: 'numeric' });
  const end = e.toLocaleDateString('en-GB', { ...base, year: 'numeric' });
  return `${start} – ${end}`;
}

const sectionMuted = { fontSize: 13, color: 'var(--color-text-muted)', margin: 0, padding: '20px' };

function AccountsSection({ rows, currency, isMobile, accountHref, wastePending, wasteError }) {
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

      {wasteError && (
        <div style={{ padding: '8px 20px', fontSize: 12, color: 'var(--color-text-muted)', borderBottom: '1px solid var(--color-border)' }}>
          Couldn’t load per-account savings — showing account health only.
        </div>
      )}

      {rows.length === 0 ? (
        <p style={{ fontSize: 13, color: 'var(--color-text-muted)', margin: 0, padding: '20px' }}>
          No connected accounts.
        </p>
      ) : (
        <ul style={{ listStyle: 'none', margin: 0, padding: 0 }}>
          {rows.map(({ acc, w }, idx) => (
            <li key={acc.id} style={{ borderTop: idx === 0 ? 'none' : '1px solid var(--color-border)' }}>
              <RowLink
                to={accountHref(acc.id)}
                style={{
                  alignItems: 'center',
                  gap: 12,
                  textAlign: 'left',
                  padding: '12px 20px',
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

                {/* Status badge always shows (it's the health cue); the
                    scanned-date is desktop-only to save width on mobile. */}
                <div style={{ display: 'flex', flexDirection: 'column', alignItems: 'flex-end', gap: 2, flexShrink: 0, minWidth: isMobile ? 'auto' : 120 }}>
                  <AccountStatus status={acc.status} />
                  {!isMobile && (
                    <span style={{ fontSize: 11, color: 'var(--color-text-muted)' }}>
                      {formatScanned(acc.last_scanned_at)}
                    </span>
                  )}
                </div>

                <div style={{ flexShrink: 0, textAlign: 'right', minWidth: 92 }}>
                  {wastePending ? (
                    <span style={{ fontSize: 12, color: 'var(--color-text-muted)' }}>…</span>
                  ) : wasteError ? (
                    <span style={{ fontSize: 12, color: 'var(--color-text-muted)' }}>—</span>
                  ) : (
                    <>
                      <div style={{ fontSize: 13, fontWeight: 700, color: 'var(--color-text)', fontVariantNumeric: 'tabular-nums' }}>
                        {currency} {(w?.potential_monthly_savings ?? 0).toFixed(2)}
                      </div>
                      <div style={{ fontSize: 11, color: 'var(--color-text-muted)', fontVariantNumeric: 'tabular-nums' }}>
                        {w?.total_zombies ?? 0} zombie{(w?.total_zombies ?? 0) === 1 ? '' : 's'}
                      </div>
                    </>
                  )}
                </div>

                <span aria-hidden style={{ color: 'var(--color-text-muted)', flexShrink: 0 }}>›</span>
              </RowLink>
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
    pending_cur_delivery: 'var(--color-text-muted)',
  }[status] || 'var(--color-text-muted)';
  const hint = STATUS_HINT[status];
  return (
    <span
      title={hint}
      style={{ fontSize: 11, fontWeight: 600, color, letterSpacing: 0.2, cursor: hint ? 'help' : undefined }}
    >
      {(status || 'unknown').replace(/_/g, ' ')}
    </span>
  );
}

// formatScanned — last_scanned_at is null until the first scan completes.
function formatScanned(ts) {
  if (!ts) return 'never scanned';
  const d = new Date(ts);
  if (Number.isNaN(d.getTime())) return 'never scanned';
  // Explicit format so the date is unambiguous regardless of browser locale
  // (avoids the en-US 6/2 vs en-GB 2/6 ambiguity).
  return `scanned ${d.toLocaleDateString(undefined, { year: 'numeric', month: 'short', day: 'numeric' })}`;
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
