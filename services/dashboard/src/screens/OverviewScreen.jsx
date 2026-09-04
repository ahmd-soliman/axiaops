import { useState, useMemo } from 'react';
import { useQuery, useQueryClient, useMutation } from '@tanstack/react-query';
import { fetchSummary, fetchResources, fetchTrend, fetchCosts, fetchDismissals, scanAccount, dismissZombie, revokeDismissal } from '../api/client';
import DateRangeChips, { DEFAULT_DAYS } from '../components/DateRangeChips';
import { serviceConfig, resourceTypeConfig } from '../components/serviceConfig';
import AccountSelector from '../components/AccountSelector';
import { useTheme } from '../theme/ThemeContext';
import { Spinner, InfoTooltip, LinkButton, RowLink } from '../components/primitives';
import { useBreakpoint } from '../components/primitives/useBreakpoint';
import { useToast } from '../context/ToastContext';
import { useScanStatus } from '../hooks/useScanStatus';
import { csvEncode, downloadCSV } from '../utils/csv';
import { formatDate } from '../utils/formatDate';
import { sumCostRecords } from '../utils/costTotals';

// ─── Constants ────────────────────────────────────────────────────────────────

const SORT_OPTIONS = [
  { value: 'cost_desc',  label: 'Cost: High → Low' },
  { value: 'cost_asc',   label: 'Cost: Low → High' },
  { value: 'service',    label: 'Service' },
  { value: 'region',     label: 'Region' },
];

const DISMISS_REASONS = [
  { value: 'intentional',        label: 'Intentionally idle' },
  { value: 'scheduled_deletion', label: 'Scheduled for deletion' },
  { value: 'false_positive',     label: 'False positive' },
  { value: 'cost_accepted',      label: 'Cost accepted' },
  { value: 'other',              label: 'Other (add note)' },
];

const SNOOZE_OPTIONS = [
  { label: '1 day',  days: 1 },
  { label: '7 days', days: 7 },
  { label: '30 days', days: 30 },
  { label: '90 days', days: 90 },
];

// ─── Overview section ─────────────────────────────────────────────────────────

// MonthlyWasteCard — clickable card replacement for the previous <button>.
// We can't use a <button> here because the InfoTooltip inside is itself a
// <button>, and nested interactives are invalid HTML. div role="button"
// needs explicit keyboard handling and a visible focus ring (browsers don't
// apply :focus-visible to non-button elements consistently).
function MonthlyWasteCard({ onShowTrend, children }) {
  const [focused, setFocused] = useState(false);
  return (
    <div
      role="button"
      tabIndex={0}
      onClick={onShowTrend}
      onKeyDown={(e) => {
        if (e.key === 'Enter' || e.key === ' ') {
          e.preventDefault();
          onShowTrend();
        }
      }}
      onFocus={() => setFocused(true)}
      onBlur={() => setFocused(false)}
      style={{
        flex: 1,
        textAlign: 'left',
        background: 'none',
        border: 'none',
        cursor: 'pointer',
        padding: 4,
        margin: -4,
        borderRadius: 6,
        outline: focused ? `2px solid var(--color-accent)` : 'none',
        outlineOffset: 2,
      }}
    >
      {children}
    </div>
  );
}

function OverviewHero({ summary, totalSpend, trend, period, customRange, onPeriodChange, onShowTrend, onShowCosts, isMobile }) {
  const data = summary.data;
  const monthlyWaste = data?.potential_monthly_savings ?? 0;
  // Scale the monthly waste to the chosen window so the headline number and the
  // % ratio against the period's totalSpend stay on the same time scale.
  // potential_monthly_savings is "monthly cost of currently-detected zombies if
  // they persist for a month" — extrapolating linearly to other windows is the
  // natural interpretation. period=30 collapses to the previous behaviour.
  // The Monthly Waste label flips to "{period}-day Waste" when period !== 30 so
  // the unit is unambiguous; the InfoTooltip body still describes the source.
  const waste = monthlyWaste * (period / 30);
  const zombieCount = data?.total_zombies ?? 0;
  const currency = data?.currency || '$';
  const wastePercent = totalSpend > 0 ? (waste / totalSpend) * 100 : 0;
  const wasteLabel = period === 30 ? 'Monthly Waste' : `${period}-day Waste`;

  // /v1/trend returns one row per (account, scan), not one row per scan day.
  // In All Accounts mode each scan day produces N rows (one per account that
  // scanned), so naively comparing the last two rows compared two arbitrary
  // accounts' totals — making the headline ▲/▼ % delta meaningless. Roll up
  // by date here so the delta compares yesterday's org-wide total to today's.
  // Then trim to the selected window so the chip selection scopes the
  // dailyTotals slice exactly the way it scopes the fetchCosts window in the
  // parent component. A Custom… pick scopes by the actual applied calendar
  // dates (`customRange`) rather than trailing-N-entries — slice(-period)
  // would silently show the most recent `period` days of data regardless of
  // which historical dates the user actually picked, the same label/data
  // mismatch fixed in DateRangeChips itself. Preset chips keep the trailing
  // "period days back from the most recent snapshot" window.
  const dailyTotals = useMemo(() => {
    const m = new Map();
    for (const s of trend.data ?? []) {
      const day = s.snapshot_at.slice(0, 10);
      m.set(day, (m.get(day) || 0) + s.total_monthly_cost);
    }
    const sorted = [...m.entries()].sort();
    if (customRange) {
      return sorted.filter(([day]) => day >= customRange.sinceIso && day <= customRange.untilIso);
    }
    return period > 0 && sorted.length > period ? sorted.slice(-period) : sorted;
  }, [trend.data, period, customRange]);
  // Compare today's org-wide total to the total at the WINDOW START — so the
  // ▲/▼ headline answers "how have we trended over the last {period} days?"
  // rather than the previous "vs yesterday" which collapses to noise when the
  // user picks 90d / 6m. dailyTotals is already trimmed to `slice(-period)`
  // above, so .at(0) is the oldest day in the window. When the window has
  // only one day (Custom… same-day pick) there's no earlier point to compare
  // to — delta is undefined and the headline omits the arrow.
  const latest   = dailyTotals.at(-1)?.[1];
  const earliest = dailyTotals.length > 1 ? dailyTotals.at(0)?.[1] : undefined;
  const delta    = latest != null && earliest != null
    ? ((latest - earliest) / Math.max(earliest, 0.01)) * 100
    : null;

  return (
    <div style={{ backgroundColor: 'var(--color-surface-alt)', borderBottom: '1px solid var(--color-border)', padding: isMobile ? '16px' : '20px' }}>
      {/* Date range chips — same shared picker used on TrendScreen +
          CostAnalyticsScreen. Right-aligned above the two-stat row so the
          stats stay the visual anchor; on phones the chips wrap below the
          flex row but stay tappable. */}
      <div style={{ display: 'flex', justifyContent: 'flex-end', marginBottom: 12 }}>
        <DateRangeChips value={period} activeRange={customRange} onChange={onPeriodChange} mobile={isMobile} />
      </div>

      {/* Two-stat row — stacks vertically on phones; the 28px-bold spend value
          + 11px label cluster only fits side-by-side once the viewport has
          ~440px of inner width. */}
      <div style={{ display: 'flex', flexDirection: isMobile ? 'column' : 'row', gap: isMobile ? 12 : 16, marginBottom: 16 }}>
        {/* Total Spend */}
        <button
          type="button"
          onClick={onShowCosts}
          style={{ flex: 1, textAlign: 'left', background: 'none', border: 'none', cursor: 'pointer', padding: 0 }}
        >
          <span style={{ fontSize: 11, fontWeight: 600, color: 'var(--color-text-muted)', letterSpacing: 1.2, textTransform: 'uppercase', display: 'block', marginBottom: 4 }}>
            Total Spend
          </span>
          <span style={{ fontSize: 28, fontWeight: 800, color: 'var(--color-text)', letterSpacing: -0.5, display: 'block', fontVariantNumeric: 'tabular-nums' }}>
            {currency} {totalSpend.toFixed(2)}
          </span>
          <span style={{ fontSize: 12, color: 'var(--color-text-muted)', marginTop: 2, display: 'block' }}>
            last {period} day{period === 1 ? '' : 's'}
          </span>
        </button>

        {/* Monthly Waste — clickable card; the InfoTooltip lives next to the
            label, so we can't use a <button> wrapper (no nested interactives).
            We use div role="button" with explicit Enter/Space + focus-ring. */}
        <MonthlyWasteCard onShowTrend={onShowTrend}>
          <div style={{ display: 'flex', alignItems: 'center', gap: 6, marginBottom: 4 }}>
            <span style={{ fontSize: 11, fontWeight: 600, color: 'var(--color-text-muted)', letterSpacing: 1.2, textTransform: 'uppercase' }}>
              {wasteLabel}
            </span>
            <InfoTooltip
              label="What does Monthly Waste mean?"
              placement="left"
              body={
                <>
                  <p style={{ margin: 0 }}>
                    Sum of monthly cost across detected zombie resources, based on net amortized cost.
                  </p>
                  <p style={{ margin: '8px 0 0', color: 'var(--color-text-mid)' }}>
                    <strong>Live, after dismissals.</strong> Computed from current resources minus anything
                    you&rsquo;ve dismissed or snoozed. This is why it can differ from the latest point on the
                    Trend chart, which is captured at scan time <em>before</em> dismissals are applied.
                  </p>
                  <p style={{ margin: '8px 0 0', color: 'var(--color-text-mid)' }}>
                    <strong>Savings Plans / RIs.</strong> If a resource is covered by a Savings Plan or
                    Reserved Instance, killing it may not reduce your bill until the commitment ends.
                    AxiaOps does not yet detect SP/RI coverage, so this number can overstate savings for
                    accounts with active commitments.
                  </p>
                </>
              }
            />
          </div>
          {/* Headline number uses --color-accent (orange) — part of the visual
              identity: main numbers across the dashboard are in orange (matches
              the Savings Trend headline on TrendScreen). See
              docs/ui-color-system-review.md §3 "Main numbers in orange". */}
          <span style={{ fontSize: 28, fontWeight: 800, color: 'var(--color-accent)', letterSpacing: -0.5, display: 'block', fontVariantNumeric: 'tabular-nums' }}>
            {currency} {waste.toFixed(2)}
          </span>
          <span style={{ fontSize: 11, color: 'var(--color-text-muted)', fontStyle: 'italic', display: 'block', marginTop: 1 }}>
            Live · after dismissals
          </span>
          <div style={{ display: 'flex', alignItems: 'center', gap: 8, marginTop: 2 }}>
            <span style={{ fontSize: 12, color: 'var(--color-text-muted)' }}>
              {zombieCount} zombie{zombieCount !== 1 ? 's' : ''}
            </span>
            {delta !== null && (
              <span style={{ fontSize: 11, color: delta > 0 ? 'var(--color-alert-critical)' : 'var(--color-status-ok)', fontWeight: 700, fontVariantNumeric: 'tabular-nums' }}>
                {delta > 0 ? '▲' : '▼'} {Math.abs(delta).toFixed(1)}%
              </span>
            )}
          </div>
        </MonthlyWasteCard>
      </div>

      {/* Waste bar */}
      {totalSpend > 0 && (() => {
        // Red is reserved for the headline pain — waste ratio over the threshold.
        // Below threshold, amber/green carry the warning/ok semantics. See
        // docs/ui-color-system-review.md §4 (red is overused).
        const ratioColor = wastePercent > 20
          ? 'var(--color-alert-critical)'
          : wastePercent > 10
            ? 'var(--color-alert-warning)'
            : 'var(--color-status-ok)';
        return (
          <div>
            <div style={{ display: 'flex', justifyContent: 'space-between', marginBottom: 4 }}>
              <span style={{ fontSize: 11, fontWeight: 600, color: 'var(--color-text-muted)' }}>Waste ratio</span>
              <span style={{ fontSize: 11, fontWeight: 700, color: ratioColor, fontVariantNumeric: 'tabular-nums' }}>
                {wastePercent.toFixed(1)}%
              </span>
            </div>
            <div style={{ height: 6, backgroundColor: 'var(--color-track)', borderRadius: 3, overflow: 'hidden' }}>
              <div style={{
                height: '100%',
                width: `${Math.min(wastePercent, 100)}%`,
                backgroundColor: ratioColor,
                borderRadius: 3,
                transition: 'width 0.3s',
              }} />
            </div>
          </div>
        );
      })()}
    </div>
  );
}

// ─── Service breakdown ───────────────────────────────────────────────────────

// Single chart row. Lives outside ServiceBreakdown so it can hold the
// `focused` state used for the keyboard focus ring. Children are all <span>s
// (not <div>s) because <button> only accepts phrasing content per the HTML
// spec — block descendants inside a <button> aren't valid and trip up some
// screen readers / validators. display:flex on a span behaves the same as on
// a div visually.
function ServiceBreakdownRow({ svc, data, maxSavings, totalSavings, currency, isMobile, active, onToggleSvc }) {
  const cfg = serviceConfig(svc);
  const barWidth = (data.savings / maxSavings) * 100;
  const pctOfTotal = (data.savings / Math.max(totalSavings, 0.01)) * 100;
  const [focused, setFocused] = useState(false);
  // Active = the resource list below is filtered to this service. Multi-select:
  // same shape as the FilterPills row, so the chart and the pills stay in sync
  // without either being the source of truth.
  //
  // Per-service color (cfg.color) is used on both the dot AND the bar so
  // service identity is consistent with the FilterPills + resource rows. Bar
  // length already encodes magnitude; a separate rank-ramp would put the same
  // service under different colors in different surfaces and break visual
  // continuity.
  return (
    <button
      type="button"
      onClick={() => onToggleSvc(svc)}
      onFocus={() => setFocused(true)}
      onBlur={() => setFocused(false)}
      aria-pressed={active}
      aria-label={`Filter resources by ${cfg.label} — ${currency}${data.savings.toFixed(2)}, ${pctOfTotal.toFixed(1)}% of total waste`}
      style={{
        background: active ? 'var(--color-accent-light)' : 'transparent',
        border: `1px solid ${active ? 'var(--color-accent-border)' : 'transparent'}`,
        borderRadius: 6,
        padding: '6px 8px',
        cursor: 'pointer',
        textAlign: 'left',
        font: 'inherit',
        color: 'inherit',
        width: '100%',
        transition: 'background 0.15s, border-color 0.15s',
        outline: focused ? `2px solid var(--color-accent)` : 'none',
        outlineOffset: 1,
      }}
    >
      {/* Header — at xs the label cluster (dot + name + count) and the
          right-aligned cost share too little width once names like
          "AmazonOpenSearchService" appear. Drop the row to a column with
          the cost on its own line. */}
      <span style={{
        display: 'flex',
        flexDirection: isMobile ? 'column' : 'row',
        alignItems: isMobile ? 'flex-start' : 'center',
        justifyContent: 'space-between',
        gap: isMobile ? 2 : 8,
        marginBottom: 4,
      }}>
        <span style={{ display: 'flex', alignItems: 'center', gap: 6, minWidth: 0, flexWrap: 'wrap' }}>
          <span style={{ display: 'inline-block', width: 6, height: 6, borderRadius: '50%', backgroundColor: cfg.color, flexShrink: 0 }} />
          <span style={{ fontSize: 12, fontWeight: 600, color: 'var(--color-text)' }}>{cfg.label}</span>
          <span style={{ fontSize: 11, color: 'var(--color-text-muted)' }}>{data.zombies} resource{data.zombies !== 1 ? 's' : ''}</span>
        </span>
        <span style={{ fontSize: 12, fontWeight: 700, color: 'var(--color-text)', fontVariantNumeric: 'tabular-nums' }}>
          {currency}{data.savings.toFixed(2)}
          <span style={{ fontWeight: 500, color: 'var(--color-text-muted)', marginLeft: 6 }}>· {pctOfTotal.toFixed(1)}%</span>
        </span>
      </span>
      <span style={{ display: 'block', height: 6, backgroundColor: 'var(--color-track)', borderRadius: 2, overflow: 'hidden' }}>
        <span style={{
          display: 'block',
          height: '100%',
          width: `${barWidth}%`,
          backgroundColor: cfg.color,
          borderRadius: 2,
          transition: 'width 0.3s',
        }} />
      </span>
    </button>
  );
}

// Aggregate row for the "long tail" — services contributing tiny amounts.
// Clickable to expand: when expanded, the parent (ServiceBreakdown) renders
// each constituent service as an indented ServiceBreakdownRow below this
// row. The aggregate bar is hidden in the expanded state — the constituents
// below carry the visual weight.
function OtherRow({ tail, totalSavings, maxSavings, currency, isMobile, expanded, onToggle }) {
  const savings = tail.reduce((s, [, d]) => s + d.savings, 0);
  const zombies = tail.reduce((s, [, d]) => s + d.zombies, 0);
  const pctOfTotal = (savings / Math.max(totalSavings, 0.01)) * 100;
  const barWidth = (savings / maxSavings) * 100;
  const [focused, setFocused] = useState(false);
  return (
    <button
      type="button"
      onClick={onToggle}
      onFocus={() => setFocused(true)}
      onBlur={() => setFocused(false)}
      aria-expanded={expanded}
      aria-controls="service-breakdown-other-list"
      aria-label={`${expanded ? 'Collapse' : 'Expand'} other services — ${tail.length} services, ${currency}${savings.toFixed(2)} total (${pctOfTotal.toFixed(1)}% of waste)`}
      style={{
        background: 'transparent',
        border: '1px solid transparent',
        borderRadius: 6,
        padding: '6px 8px',
        cursor: 'pointer',
        textAlign: 'left',
        font: 'inherit',
        color: 'inherit',
        width: '100%',
        outline: focused ? `2px solid var(--color-accent)` : 'none',
        outlineOffset: 1,
      }}
    >
      <span style={{
        display: 'flex',
        flexDirection: isMobile ? 'column' : 'row',
        alignItems: isMobile ? 'flex-start' : 'center',
        justifyContent: 'space-between',
        gap: isMobile ? 2 : 8,
        marginBottom: expanded ? 0 : 4,
      }}>
        <span style={{ display: 'flex', alignItems: 'center', gap: 6, minWidth: 0, flexWrap: 'wrap' }}>
          <span aria-hidden="true" style={{ display: 'inline-block', width: 10, fontSize: 9, color: 'var(--color-text-muted)', lineHeight: 1, textAlign: 'center' }}>
            {expanded ? '▾' : '▸'}
          </span>
          <span style={{ display: 'inline-block', width: 6, height: 6, borderRadius: '50%', backgroundColor: 'var(--color-text-sub)', flexShrink: 0 }} />
          <span style={{ fontSize: 12, fontWeight: 600, color: 'var(--color-text-mid)' }}>Other</span>
          <span style={{ fontSize: 11, color: 'var(--color-text-muted)' }}>{tail.length} services · {zombies} resource{zombies !== 1 ? 's' : ''}</span>
        </span>
        <span style={{ fontSize: 12, fontWeight: 700, color: 'var(--color-text-mid)', fontVariantNumeric: 'tabular-nums' }}>
          {currency}{savings.toFixed(2)}
          <span style={{ fontWeight: 500, color: 'var(--color-text-muted)', marginLeft: 6 }}>· {pctOfTotal.toFixed(1)}%</span>
        </span>
      </span>
      {/* Aggregate bar only when collapsed — when expanded the constituents
          rendered below carry the visual weight and a redundant aggregate bar
          here just doubles the height of the disclosure. */}
      {!expanded && (
        <span style={{ display: 'block', height: 6, backgroundColor: 'var(--color-track)', borderRadius: 2, overflow: 'hidden' }}>
          <span style={{
            display: 'block',
            height: '100%',
            width: `${barWidth}%`,
            backgroundColor: 'var(--color-text-sub)',
            borderRadius: 2,
          }} />
        </span>
      )}
    </button>
  );
}

// Pareto divider — horizontal marker labelled with the cumulative percentage
// of waste accumulated above it. Renders between rows. Standard FinOps trope:
// "these N services are your problem; everything below is rounding error."
function ParetoDivider({ cumulativePct }) {
  return (
    <div
      aria-hidden="true"
      style={{
        display: 'flex',
        alignItems: 'center',
        gap: 8,
        margin: '4px 8px',
        color: 'var(--color-text-muted)',
        fontSize: 10,
        fontWeight: 700,
        letterSpacing: 1,
        textTransform: 'uppercase',
      }}
    >
      <span style={{ flex: 1, borderTop: `1px dashed var(--color-border)` }} />
      <span style={{ fontVariantNumeric: 'tabular-nums', whiteSpace: 'nowrap' }}>
        {cumulativePct.toFixed(0)}% of waste above
      </span>
      <span style={{ flex: 1, borderTop: `1px dashed var(--color-border)` }} />
    </div>
  );
}

// Split byService into (shown, tail). Services where savings < the threshold
// max(1% of total, $5) collapse into a single "Other" row — kills the long-
// tail clutter (a $0.40 service contributes nothing actionable). Guards:
//   - Don't collapse if tail would be 0 or 1 entry (a single "Other" hiding
//     one service is silly — show it directly).
//   - Always keep at least the top 3 shown so very small totals don't render
//     as an empty chart of just "Other".
function splitTail(byService, totalSavings) {
  const threshold = Math.max(totalSavings * 0.01, 5);
  let shown = byService.filter(([, d]) => d.savings >= threshold);
  let tail  = byService.filter(([, d]) => d.savings <  threshold);
  if (tail.length <= 1) return { shown: byService, tail: [] };
  if (shown.length < 3 && byService.length > 3) {
    shown = byService.slice(0, 3);
    tail  = byService.slice(3);
  }
  return { shown, tail };
}

function ServiceBreakdown({ byService, currency, isMobile, filterSvcs, onToggleSvc }) {
  const [otherExpanded, setOtherExpanded] = useState(false);
  if (byService.length === 0) return null;
  const maxSavings = Math.max(...byService.map(([, d]) => d.savings), 0.01);
  const totalSavings = byService.reduce((sum, [, d]) => sum + d.savings, 0);
  const { shown, tail } = splitTail(byService, totalSavings);

  // Walk the shown rows, inserting the Pareto divider as soon as cumulative
  // crosses 80%. Done as a side-effecting loop because the divider position
  // depends on running-sum state we can't get from a pure map.
  const rows = [];
  let cumulative = 0;
  let paretoMarked = false;
  for (const [svc, data] of shown) {
    rows.push(
      <ServiceBreakdownRow
        key={svc}
        svc={svc}
        data={data}
        maxSavings={maxSavings}
        totalSavings={totalSavings}
        currency={currency}
        isMobile={isMobile}
        active={filterSvcs.has(svc)}
        onToggleSvc={onToggleSvc}
      />
    );
    cumulative += (data.savings / Math.max(totalSavings, 0.01)) * 100;
    // Only show the divider if there's still something visually below it
    // (more shown rows OR a tail) — a divider at the very bottom is noise.
    const hasMoreBelow = rows.length < shown.length || tail.length > 0;
    if (!paretoMarked && cumulative >= 80 && hasMoreBelow) {
      paretoMarked = true;
      rows.push(<ParetoDivider key="pareto" cumulativePct={cumulative} />);
    }
  }
  if (tail.length > 0) {
    rows.push(
      <OtherRow
        key="other"
        tail={tail}
        totalSavings={totalSavings}
        maxSavings={maxSavings}
        currency={currency}
        isMobile={isMobile}
        expanded={otherExpanded}
        onToggle={() => setOtherExpanded((v) => !v)}
      />
    );
    if (otherExpanded) {
      // Each constituent rendered as a normal ServiceBreakdownRow, indented
      // by paddingLeft on the group wrapper (NOT marginLeft — margin would
      // push the wrapper outside the flex parent's content box and risk a
      // horizontal scrollbar on narrow viewports). The group wrapper carries
      // the id that the OtherRow button's aria-controls points at, so AT can
      // navigate from the disclosure trigger to the revealed list. Sub-rows
      // share filterSvcs / onToggleSvc with the top-level chart, so clicking
      // AWSGlue inside the expansion has the same effect as clicking a
      // top-level row would.
      rows.push(
        <div
          key="other-list"
          id="service-breakdown-other-list"
          role="group"
          aria-label="Other services"
          style={{
            display: 'flex',
            flexDirection: 'column',
            gap: 2,
            paddingLeft: 22,
            boxSizing: 'border-box',
            borderLeft: `2px solid var(--color-border)`,
            marginLeft: 8,
          }}
        >
          {tail.map(([svc, data]) => (
            <ServiceBreakdownRow
              key={svc}
              svc={svc}
              data={data}
              maxSavings={maxSavings}
              totalSavings={totalSavings}
              currency={currency}
              isMobile={isMobile}
              active={filterSvcs.has(svc)}
              onToggleSvc={onToggleSvc}
            />
          ))}
        </div>
      );
    }
  }

  return (
    <div style={{ padding: isMobile ? '16px' : '16px 20px', borderBottom: `1px solid var(--color-border)` }}>
      <span style={{ fontSize: 12, fontWeight: 600, color: 'var(--color-text-muted)', textTransform: 'uppercase', letterSpacing: 0.5, display: 'block', marginBottom: 12 }}>
        Waste by Service
      </span>
      <div style={{ display: 'flex', flexDirection: 'column', gap: 2 }}>
        {rows}
      </div>
    </div>
  );
}

// ─── Search + Sort bar ────────────────────────────────────────────────────────

function FilterBar({ search, onSearch, sortBy, onSort, activeFilters, onClearFilter, isMobile }) {
  return (
    <div style={{ padding: '12px 16px', display: 'flex', flexDirection: 'column', gap: 8 }}>
      <div style={{ display: 'flex', gap: 8 }}>
        {/* Search */}
        <div style={{ flex: 1, position: 'relative' }}>
          <svg
            style={{ position: 'absolute', left: 10, top: '50%', transform: 'translateY(-50%)', pointerEvents: 'none' }}
            width="14" height="14" viewBox="0 0 24 24" fill="none" stroke={'var(--color-text-muted)'} strokeWidth="2.5" strokeLinecap="round" strokeLinejoin="round"
          >
            <circle cx="11" cy="11" r="8" />
            <path d="m21 21-4.35-4.35" />
          </svg>
          <input
            type="search"
            value={search}
            onChange={e => onSearch(e.target.value)}
            placeholder="Search resource ID, owner, region…"
            aria-label="Search resources"
            style={{
              width: '100%',
              paddingLeft: 32,
              paddingRight: 12,
              paddingTop: 8,
              paddingBottom: 8,
              backgroundColor: 'var(--color-surface-raised)',
              border: `1px solid var(--color-border)`,
              borderRadius: 8,
              fontSize: 13,
              color: 'var(--color-text)',
              boxSizing: 'border-box',
            }}
          />
        </div>

        {/* Sort */}
        <select
          value={sortBy}
          onChange={e => onSort(e.target.value)}
          aria-label="Sort resources"
          style={{
            padding: '8px 10px',
            backgroundColor: 'var(--color-surface-raised)',
            border: `1px solid var(--color-border)`,
            borderRadius: 8,
            fontSize: 13,
            color: 'var(--color-text-mid)',
            cursor: 'pointer',
            flexShrink: 0,
          }}
        >
          {SORT_OPTIONS.map(o => (
            <option key={o.value} value={o.value}>{o.label}</option>
          ))}
        </select>
      </div>

      {/* Active filter chips — phone bumps pill padding from 3/8/3/10 →
          7/12/7/14 so the 22px-tall desktop chip becomes a ~32px-tall
          mobile chip; still tighter than the 44px HIG floor but reliably
          tappable. Same rationale for the "Clear all" link beside them. */}
      {activeFilters.length > 0 && (
        <div style={{ display: 'flex', gap: 6, flexWrap: 'wrap' }}>
          {activeFilters.map(f => (
            <button
              key={f.key}
              onClick={() => onClearFilter(f.key)}
              aria-label={`Remove ${f.label} filter`}
              style={{
                display: 'flex',
                alignItems: 'center',
                gap: 5,
                padding: isMobile ? '7px 12px 7px 14px' : '3px 8px 3px 10px',
                backgroundColor: 'var(--color-accent-light)',
                border: `1px solid var(--color-accent-border)`,
                borderRadius: 20,
                cursor: 'pointer',
                fontSize: 12,
                color: 'var(--color-accent-text)',
                fontWeight: 600,
              }}
            >
              {f.label}
              <span style={{ fontSize: 14, lineHeight: 1, opacity: 0.7 }}>×</span>
            </button>
          ))}
          <button
            onClick={() => activeFilters.forEach(f => onClearFilter(f.key))}
            style={{ padding: isMobile ? '7px 10px' : '3px 8px', background: 'none', border: 'none', cursor: 'pointer', fontSize: 12, color: 'var(--color-text-muted)' }}
          >
            Clear all
          </button>
        </div>
      )}
    </div>
  );
}

// ─── Service + owner pill rows ────────────────────────────────────────────────

function FilterPills({
  byService, owners, resourceTypes,
  filterSvcs, filterOwner, filterResourceTypes,
  onToggleSvc, onFilterOwner, onToggleResourceType, onClearResourceTypes,
  currency, isMobile,
}) {
  const showSubfilter = filterSvcs.size === 1 && resourceTypes.length > 0;
  const noneSelected = filterResourceTypes.size === 0;
  return (
    <div style={{ padding: '0 16px 12px' }}>
      {/* Service pills */}
      {byService.length > 0 && (
        <div
          role="group"
          aria-label="Filter by service"
          style={{ display: 'flex', gap: 6, overflowX: 'auto', paddingBottom: 6, marginBottom: 6 }}
        >
          {byService.map(([svc, data]) => {
            const cfg = serviceConfig(svc);
            const active = filterSvcs.has(svc);
            return (
              <button
                key={svc}
                onClick={() => onToggleSvc(svc)}
                aria-pressed={active}
                style={{
                  display: 'flex',
                  alignItems: 'center',
                  gap: 5,
                  backgroundColor: active ? 'var(--color-accent)' : 'var(--color-surface-raised)',
                  borderRadius: 20,
                  padding: isMobile ? '8px 14px' : '5px 10px',
                  border: `1px solid ${active ? 'var(--color-accent)' : 'var(--color-border)'}`,
                  cursor: 'pointer',
                  flexShrink: 0,
                }}
              >
                <div style={{ width: 6, height: 6, borderRadius: '50%', backgroundColor: active ? '#ffffff' : cfg.color }} />
                <span style={{ fontSize: 12, fontWeight: 700, color: active ? '#ffffff' : 'var(--color-text)' }}>{cfg.label}</span>
              </button>
            );
          })}
        </div>
      )}

      {/* Resource type sub-filter pills (shown when exactly one service is selected and has sub-types) */}
      {showSubfilter && (
        <div
          role="group"
          aria-label="Filter by resource type"
          style={{ display: 'flex', gap: 6, overflowX: 'auto', paddingBottom: 6, marginBottom: 6 }}
        >
          <button
            onClick={onClearResourceTypes}
            aria-pressed={noneSelected}
            style={{
              padding: isMobile ? '7px 12px' : '3px 8px', borderRadius: 14, cursor: 'pointer', flexShrink: 0,
              backgroundColor: noneSelected ? 'var(--color-text-mid)' : 'var(--color-surface-raised)',
              border: `1px solid ${noneSelected ? 'var(--color-text-mid)' : 'var(--color-border)'}`,
              fontSize: 11, fontWeight: 600,
              color: noneSelected ? '#fff' : 'var(--color-text-muted)',
            }}
          >
            All Types
          </button>
          {resourceTypes.map(rt => {
            const cfg = resourceTypeConfig(rt);
            const active = filterResourceTypes.has(rt);
            return (
              <button
                key={rt}
                onClick={() => onToggleResourceType(rt)}
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

      {/* Owner pills */}
      {owners.length > 1 && (
        <div
          role="group"
          aria-label="Filter by owner"
          style={{ display: 'flex', gap: 6, overflowX: 'auto', paddingBottom: 2 }}
        >
          {owners.map(owner => {
            const active = filterOwner === owner;
            return (
              <button
                key={owner}
                onClick={() => onFilterOwner(active ? null : owner)}
                aria-pressed={active}
                style={{
                  backgroundColor: active ? 'var(--color-navy)' : 'var(--color-surface-raised)',
                  borderRadius: 20,
                  padding: isMobile ? '8px 12px' : '4px 10px',
                  border: `1px solid ${active ? 'var(--color-navy)' : 'var(--color-border)'}`,
                  cursor: 'pointer',
                  flexShrink: 0,
                }}
              >
                <span style={{ fontSize: 12, fontWeight: 600, color: active ? '#fff' : 'var(--color-text-mid)' }}>
                  {owner}
                </span>
              </button>
            );
          })}
        </div>
      )}
    </div>
  );
}

// ─── Resource card ────────────────────────────────────────────────────────────

function ResourceCard({ item, href, isSelected, onToggleSelect }) {
  const cfg  = serviceConfig(item.service);
  const env  = item.tags?.env ?? 'unknown';
  const envVariant = ['prod', 'production'].includes(env) ? 'prod' : ['staging', 'stg'].includes(env) ? 'stag' : null;
  const envColor = envVariant === 'prod' ? 'var(--color-error)' : envVariant === 'stag' ? 'var(--color-warning)' : 'var(--color-text-muted)';

  return (
    <div
      style={{
        backgroundColor: 'var(--color-card)',
        marginLeft: 16,
        marginRight: 16,
        marginBottom: 8,
        borderRadius: 10,
        boxShadow: '0 1px 4px rgba(0,0,0,0.06)',
        display: 'flex',
        alignItems: 'stretch',
        overflow: 'hidden',
        border: isSelected ? `1px solid var(--color-accent)` : `1px solid var(--color-border)`,
        transition: 'border-color 0.15s',
      }}
    >
      {/* Checkbox column — div onClick widens the tap target to the whole
          padded column. The input's own click must stopPropagation so it
          doesn't bubble back into the div and double-toggle (input onChange
          fires first, then div onClick on bubble → net zero change). */}
      <div
        style={{ padding: '16px 0 16px 12px', display: 'flex', alignItems: 'flex-start', flexShrink: 0 }}
        onClick={e => { e.stopPropagation(); onToggleSelect(item.resource_id); }}
      >
        <input
          type="checkbox"
          checked={isSelected}
          onChange={() => onToggleSelect(item.resource_id)}
          onClick={e => e.stopPropagation()}
          aria-label={`Select ${item.resource_id}`}
          style={{ width: 16, height: 16, cursor: 'pointer', accentColor: 'var(--color-accent)', marginTop: 1 }}
        />
      </div>

      {/* Main content — a real anchor so middle/Ctrl-click opens the resource
          detail in a new tab. The checkbox column above is a sibling, not a
          child, so it stays independently clickable (no nested interactives). */}
      <RowLink
        to={href}
        style={{
          flex: 1,
          flexDirection: 'column',
          width: 'auto',
          padding: '12px 14px 12px 10px',
          textAlign: 'left',
          minWidth: 0,
        }}
      >
        {/* Row 1: service dot + label + cost */}
        <div style={{ display: 'flex', alignItems: 'center', gap: 8, marginBottom: 5 }}>
          <div style={{ width: 6, height: 6, borderRadius: '50%', backgroundColor: cfg.color, flexShrink: 0 }} />
          <span style={{ fontSize: 13, fontWeight: 600, color: 'var(--color-text)' }}>{cfg.label}</span>

          {item.is_zombie && (
            <span style={{ display: 'inline-flex', alignItems: 'center', gap: 5, flexShrink: 0 }}>
              <span style={{ width: 5, height: 5, borderRadius: '50%', backgroundColor: 'var(--color-zombie-badge-text)' }} />
              <span style={{ fontSize: 12, fontWeight: 600, color: 'var(--color-zombie-badge-text)' }}>
                zombie
              </span>
            </span>
          )}

          <div style={{ flex: 1 }} />

          <span style={{ fontSize: 15, fontWeight: 800, color: 'var(--color-accent)', flexShrink: 0 }}>
            {item.currency} {item.monthly_cost.toFixed(2)}<span style={{ fontSize: 11, fontWeight: 500, color: 'var(--color-text-muted)' }}>/mo</span>
          </span>
        </div>

        {/* Row 2: resource ID */}
        <span style={{
          fontSize: 11,
          color: 'var(--color-text-muted)',
          fontFamily: '"Geist Mono Variable", monospace',
          display: 'block',
          overflow: 'hidden',
          textOverflow: 'ellipsis',
          whiteSpace: 'nowrap',
          marginBottom: 7,
        }}>
          {item.resource_id}
        </span>

        {/* Row 3: metadata chips + owner */}
        <div style={{ display: 'flex', alignItems: 'center', gap: 6, flexWrap: 'wrap' }}>
          <span style={{
            fontSize: 11,
            color: 'var(--color-text-mid)',
            backgroundColor: 'var(--color-surface-raised)',
            border: `1px solid var(--color-border)`,
            padding: '2px 7px',
            borderRadius: 4,
          }}>
            {item.region}
          </span>
          <span style={{
            fontSize: 11,
            fontWeight: envVariant === 'prod' ? 700 : 500,
            color: envColor,
            backgroundColor: envVariant === 'prod' ? `var(--color-error)18` : 'var(--color-surface-raised)',
            border: `1px solid ${envVariant === 'prod' ? `var(--color-error)33` : 'var(--color-border)'}`,
            padding: '2px 7px',
            borderRadius: 4,
          }}>
            {env}
          </span>
          {item.owner && (
            <span style={{ fontSize: 11, color: 'var(--color-text-muted)', marginLeft: 'auto' }}>
              {item.owner}
            </span>
          )}
        </div>

        {/* Row 4: detection reason / usage */}
        {(item.is_zombie || item.usage_metric) && (
          <div style={{ marginTop: 6 }}>
            <span style={{ fontSize: 12, color: item.is_zombie ? 'var(--color-text-mid)' : 'var(--color-text-muted)', fontStyle: 'italic', lineHeight: '18px', display: 'block' }}>
              {item.is_zombie ? item.reason : `${item.usage_metric}: ${item.usage_avg?.toFixed(2)} ${item.usage_unit}`}
            </span>
          </div>
        )}
      </RowLink>
    </div>
  );
}

// ─── Dismissed resource card ──────────────────────────────────────────────────

function DismissedCard({ item, href, isSelected, onToggleSelect }) {
  const cfg = serviceConfig(item.service);
  const reasonLabel = {
    intentional: 'Intentional', scheduled_deletion: 'Scheduled', false_positive: 'False positive',
    cost_accepted: 'Cost accepted', other: 'Other',
  }[item.reason] ?? item.reason;
  const isSnoozed = item.action === 'snooze';
  const [focused, setFocused] = useState(false);

  // Same shape as ResourceCard — outer is a <div>, NOT an anchor, so the
  // checkbox column can be clickable independently of the row body. The body
  // (cost / metadata) is a <RowLink> that opens the detail view (issue #130).
  return (
    <div
      style={{
        backgroundColor: 'var(--color-card)',
        marginLeft: 16,
        marginRight: 16,
        marginBottom: 8,
        borderRadius: 10,
        opacity: 0.75,
        display: 'flex',
        alignItems: 'stretch',
        overflow: 'hidden',
        border: isSelected ? `1px solid var(--color-accent)` : `1px solid var(--color-border)`,
        transition: 'border-color 0.15s',
      }}
    >
      {/* Checkbox column — stopPropagation on the input so its native
          onChange isn't double-counted with the parent div's onClick. */}
      <div
        style={{ padding: '16px 0 16px 12px', display: 'flex', alignItems: 'flex-start', flexShrink: 0 }}
        onClick={(e) => { e.stopPropagation(); onToggleSelect?.(item.id); }}
      >
        <input
          type="checkbox"
          checked={!!isSelected}
          onChange={() => onToggleSelect?.(item.id)}
          onClick={(e) => e.stopPropagation()}
          aria-label={`Select ${item.resource_id} for bulk restore`}
          style={{ width: 16, height: 16, cursor: 'pointer', accentColor: 'var(--color-accent)', marginTop: 1 }}
        />
      </div>

      {/* Main content — opens detail view on click. */}
      <RowLink
        to={href}
        onFocus={() => setFocused(true)}
        onBlur={() => setFocused(false)}
        style={{
          flex: 1,
          flexDirection: 'column',
          width: 'auto',
          padding: '12px 14px 12px 10px',
          textAlign: 'left',
          minWidth: 0,
          outline: focused ? `2px solid var(--color-accent)` : 'none',
          outlineOffset: -2,
        }}
      >
        <div style={{ display: 'flex', alignItems: 'center', gap: 6, marginBottom: 5 }}>
          <div style={{ width: 6, height: 6, borderRadius: '50%', backgroundColor: cfg.color, flexShrink: 0 }} />
          <span style={{ fontSize: 13, fontWeight: 600, color: 'var(--color-text)' }}>{cfg.label}</span>
          <span style={{ display: 'inline-flex', alignItems: 'center', gap: 5, flexShrink: 0 }}>
            <span style={{ width: 5, height: 5, borderRadius: '50%', backgroundColor: isSnoozed ? '#2563EB' : '#9CA3AF' }} />
            <span style={{ fontSize: 12, fontWeight: 600, color: isSnoozed ? '#2563EB' : '#9CA3AF' }}>
              {isSnoozed ? 'snoozed' : 'dismissed'}
            </span>
          </span>
          <div style={{ flex: 1 }} />
          {typeof item.monthly_cost === 'number' && (
            <span style={{ fontSize: 14, fontWeight: 700, color: 'var(--color-accent)', flexShrink: 0 }}>
              {item.currency} {item.monthly_cost.toFixed(2)}<span style={{ fontSize: 10, fontWeight: 500, color: 'var(--color-text-muted)' }}>/mo</span>
            </span>
          )}
        </div>
        <span style={{ fontSize: 11, color: 'var(--color-text-muted)', fontFamily: '"Geist Mono Variable", monospace', display: 'block', overflow: 'hidden', textOverflow: 'ellipsis', whiteSpace: 'nowrap', marginBottom: 4 }}>
          {item.resource_id}
        </span>
        <div style={{ display: 'flex', gap: 6, flexWrap: 'wrap', alignItems: 'center' }}>
          <span style={{ fontSize: 11, color: 'var(--color-text-muted)', backgroundColor: 'var(--color-surface-raised)', border: `1px solid var(--color-border)`, padding: '2px 7px', borderRadius: 4 }}>
            {item.region}
          </span>
          <span style={{ fontSize: 11, color: 'var(--color-text-mid)', backgroundColor: 'var(--color-surface-raised)', border: `1px solid var(--color-border)`, padding: '2px 7px', borderRadius: 4, fontWeight: 600 }}>
            {reasonLabel}
          </span>
          {isSnoozed && item.snoozed_until && (
            <span style={{ fontSize: 11, color: 'var(--color-text-muted)' }}>
              until {formatDate(item.snoozed_until)}
            </span>
          )}
        </div>
        {item.note ? (
          <span style={{ fontSize: 12, color: 'var(--color-text-mid)', fontStyle: 'italic', display: 'block', marginTop: 4 }}>&ldquo;{item.note}&rdquo;</span>
        ) : null}
      </RowLink>
    </div>
  );
}

// ─── Bulk action bar ──────────────────────────────────────────────────────────

function BulkActionBar({ count, onDismiss, onSnooze, onExport, onClear, onRestore, isMobile }) {
  // Sum of pixel widths inside the toolbar (~417px including padding/gap)
  // overflows a 360px viewport. On mobile the bar spans the viewport with
  // 12px side margins instead of centring around `left: 50%`, drops the
  // divider, and reduces label/padding sizes to fit. The position stays
  // fixed-to-bottom so the bar is reachable while the resource list scrolls.
  //
  // Two action modes — zombie list (Dismiss/Snooze/Export) vs Hidden tab
  // (Restore/Export). Picked by which of `onRestore` or `onDismiss` is wired
  // by the caller; both modes share the same toolbar shape so the user gets
  // consistent positioning + selection counts.
  const restoreMode = !!onRestore;
  const buttonStyle = (color) => ({
    padding: isMobile ? '6px 8px' : '5px 12px',
    borderRadius: 6,
    backgroundColor: 'rgba(255,255,255,0.12)',
    border: 'none',
    cursor: 'pointer',
    color,
    fontSize: isMobile ? 12 : 13,
    fontWeight: 600,
  });
  return (
    <div
      role="toolbar"
      aria-label="Bulk actions"
      style={{
        position: 'fixed',
        bottom: isMobile ? 16 : 72,
        ...(isMobile
          ? { left: 12, right: 12 }
          : { left: '50%', transform: 'translateX(-50%)' }),
        backgroundColor: 'var(--color-navy)',
        borderRadius: 12,
        padding: isMobile ? '8px 10px' : '10px 16px',
        display: 'flex',
        alignItems: 'center',
        gap: isMobile ? 6 : 10,
        boxShadow: '0 8px 24px rgba(0,0,0,0.3)',
        zIndex: 200,
        whiteSpace: 'nowrap',
      }}
    >
      <span style={{ fontSize: isMobile ? 12 : 13, fontWeight: 700, color: 'var(--color-text-on-dark)' }}>
        {count} {isMobile ? '' : 'selected'}
      </span>
      {!isMobile && <div style={{ width: 1, height: 20, backgroundColor: 'rgba(255,255,255,0.2)' }} />}
      {restoreMode ? (
        <button onClick={onRestore} style={buttonStyle('#fbbf24')}>Restore</button>
      ) : (
        <>
          <button onClick={onDismiss} style={buttonStyle('#fff')}>Dismiss</button>
          <button onClick={onSnooze} style={buttonStyle('#60a5fa')}>{isMobile ? 'Snooze' : 'Snooze 7d'}</button>
        </>
      )}
      {onExport && <button onClick={onExport} style={buttonStyle('#34d399')}>Export</button>}
      <div style={{ flex: 1 }} />
      <button
        onClick={onClear}
        aria-label="Clear selection"
        style={{ padding: '5px 8px', background: 'none', border: 'none', cursor: 'pointer', color: 'rgba(255,255,255,0.5)', fontSize: 18, lineHeight: 1 }}
      >
        ×
      </button>
    </div>
  );
}

// ─── Bulk dismiss modal ───────────────────────────────────────────────────────

function BulkDismissModal({ visible, onClose, onConfirm, count, modalAction, isDark }) {
  const [reason, setReason]  = useState('intentional');
  const [note, setNote]      = useState('');
  const [loading, setLoading] = useState(false);
  const { toast } = useToast();

  if (!visible) return null;

  async function handleConfirm() {
    if (reason === 'other' && !note.trim()) {
      toast('Please add a note when selecting "Other".', 'error');
      return;
    }
    setLoading(true);
    await onConfirm({ reason, note: note.trim() });
    setLoading(false);
  }

  return (
    <div
      style={{ position: 'fixed', inset: 0, backgroundColor: isDark ? 'rgba(0,0,0,0.5)' : 'rgba(15,23,42,0.35)', display: 'flex', alignItems: 'center', justifyContent: 'center', zIndex: 1000 }}
      onClick={onClose}
    >
      <div
        onClick={e => e.stopPropagation()}
        role="dialog"
        aria-modal="true"
        aria-label={`Bulk ${modalAction}`}
        style={{ backgroundColor: 'var(--color-surface)', borderRadius: 16, padding: 24, maxWidth: 420, width: '90vw', boxShadow: '0 16px 40px rgba(0,0,0,0.3)' }}
      >
        <span style={{ fontSize: 17, fontWeight: 800, color: 'var(--color-text)', display: 'block', marginBottom: 4 }}>
          {modalAction === 'dismiss' ? `Dismiss ${count} resources` : `Snooze ${count} resources`}
        </span>
        <span style={{ fontSize: 13, color: 'var(--color-text-muted)', display: 'block', marginBottom: 16 }}>
          {modalAction === 'dismiss' ? 'These resources will be hidden from the zombie list.' : 'These resources will be hidden for 7 days.'}
        </span>

        <span style={{ fontSize: 11, fontWeight: 700, color: 'var(--color-text-muted)', letterSpacing: 1, textTransform: 'uppercase', display: 'block', marginBottom: 8 }}>Reason</span>
        {DISMISS_REASONS.map(r => (
          <button
            key={r.value}
            onClick={() => setReason(r.value)}
            style={{
              display: 'flex', alignItems: 'center', gap: 10,
              padding: '9px 12px', borderRadius: 8, marginBottom: 4,
              border: `1px solid ${reason === r.value ? 'var(--color-accent)' : 'var(--color-border)'}`,
              backgroundColor: reason === r.value ? 'var(--color-accent-light)' : 'transparent',
              cursor: 'pointer', width: '100%', textAlign: 'left',
            }}
          >
            <div style={{ width: 15, height: 15, borderRadius: '50%', border: `2px solid ${reason === r.value ? 'var(--color-accent)' : 'var(--color-text-muted)'}`, backgroundColor: reason === r.value ? 'var(--color-accent)' : 'transparent', flexShrink: 0 }} />
            <span style={{ fontSize: 14, color: reason === r.value ? 'var(--color-accent)' : 'var(--color-text-mid)', fontWeight: reason === r.value ? 600 : 400 }}>{r.label}</span>
          </button>
        ))}

        {(reason === 'other' || note.length > 0) && (
          <textarea
            value={note}
            onChange={e => setNote(e.target.value)}
            placeholder={reason === 'other' ? 'Note (required)…' : 'Add a note (optional)…'}
            style={{ marginTop: 8, backgroundColor: 'var(--color-surface-raised)', border: `1px solid var(--color-border)`, borderRadius: 8, padding: 12, color: 'var(--color-text)', fontSize: 14, minHeight: 56, width: '100%', boxSizing: 'border-box', resize: 'vertical' }}
          />
        )}

        <div style={{ display: 'flex', gap: 10, marginTop: 20 }}>
          <button onClick={onClose} style={{ flex: 1, padding: '12px', borderRadius: 10, border: `1px solid var(--color-border)`, backgroundColor: 'transparent', cursor: 'pointer' }}>
            <span style={{ color: 'var(--color-text-mid)', fontWeight: 700, fontSize: 14 }}>Cancel</span>
          </button>
          <button onClick={handleConfirm} disabled={loading} style={{ flex: 1, padding: '12px', borderRadius: 10, backgroundColor: 'var(--color-accent)', border: 'none', cursor: 'pointer', opacity: loading ? 0.6 : 1, display: 'flex', alignItems: 'center', justifyContent: 'center' }}>
            {loading ? <Spinner size={18} color={'var(--color-text-on-dark)'} /> : <span style={{ color: 'var(--color-text-on-dark)', fontWeight: 800, fontSize: 14 }}>{modalAction === 'dismiss' ? 'Dismiss All' : 'Snooze All'}</span>}
          </button>
        </div>
      </div>
    </div>
  );
}

// ─── CSV export ───────────────────────────────────────────────────────────────

function exportCSV(list, { zombieOnly }, toast) {
  const kind = zombieOnly ? 'zombies' : 'resources';
  const filename = `axiaops-${kind}-${new Date().toISOString().split('T')[0]}.csv`;

  const headers = ['resource_id', 'service', 'region', 'monthly_cost', 'currency', 'usage_metric', 'usage_avg', 'usage_unit', 'owner', 'is_zombie', 'reason'];
  const rows = list.map(r => [
    r.resource_id,
    r.service,
    r.region,
    r.monthly_cost.toFixed(2),
    r.currency,
    r.usage_metric ?? '',
    r.usage_avg ?? '',
    r.usage_unit ?? '',
    r.owner ?? '',
    r.is_zombie ? 'true' : 'false',
    r.reason ?? '',
  ]);

  downloadCSV(csvEncode(headers, rows), filename);

  const noun = zombieOnly ? 'zombie' : 'resource';
  toast(`Exported ${list.length} ${noun}${list.length !== 1 ? 's' : ''} to CSV`, 'success');
}

// ─── Sort function ────────────────────────────────────────────────────────────

function sortResources(list, sortBy) {
  return [...list].sort((a, b) => {
    switch (sortBy) {
      case 'cost_asc':  return a.monthly_cost - b.monthly_cost;
      case 'cost_desc': return b.monthly_cost - a.monthly_cost;
      case 'service':   return a.service.localeCompare(b.service);
      case 'region':    return a.region.localeCompare(b.region);
      default:          return b.monthly_cost - a.monthly_cost;
    }
  });
}

// ─── Main component ───────────────────────────────────────────────────────────

export default function OverviewScreen({
  onShowTrend, onShowCosts, zombieHref, accounts = [], connectHref, editAccountHref,
  selectedAccount, onSelectAccount, initialServiceFilter,
}) {
  const { isDark } = useTheme();
  const { toast }         = useToast();
  const { watch }         = useScanStatus();
  const queryClient       = useQueryClient();
  const { isAtMost }      = useBreakpoint();
  const isMobile          = isAtMost('xs');

  // Seed the service filter from a deep-link (org summary's "Waste by service"
  // rows → /zombies?service=<svc>). Read once on mount; further toggling is the
  // user's via the existing filter pills.
  const [filterSvcs, setFilterSvcs]                   = useState(() => new Set(initialServiceFilter ? [initialServiceFilter] : []));
  const [filterResourceTypes, setFilterResourceTypes] = useState(() => new Set());
  const [filterOwner, setFilterOwner]                 = useState(null);
  const [zombieOnly, setZombieOnly]       = useState(true);
  const [showDismissed, setShowDismissed] = useState(false);
  const [search, setSearch]             = useState('');
  const [sortBy, setSortBy]             = useState('cost_desc');
  const [selected, setSelected]         = useState(new Set());
  const [bulkModal, setBulkModal]       = useState(null); // 'dismiss' | 'snooze' | null
  const [hiddenFilter, setHiddenFilter] = useState('all'); // 'all' | 'dismissed' | 'snoozed'
  // Time window applied to the hero stat block (Total Spend + Monthly Waste
  // delta) and the underlying fetchCosts / trend trim. Default matches the
  // previous hardcoded 30-day behaviour; the chip row in OverviewHero lets
  // the user widen/narrow without leaving the screen.
  const [period, setPeriod] = useState(DEFAULT_DAYS);
  // Absolute calendar window from the Custom… picker ({ sinceIso, untilIso}),
  // or null for the trailing `period`-day window. Presets clear it back to
  // null. Mirrors CloudSpendScreen so every chip row behaves the same way.
  const [customRange, setCustomRange] = useState(null);

  function handlePeriodChange(days, range) {
    setPeriod(days);
    setCustomRange(range ?? null);
  }

  const summary    = useQuery({ queryKey: ['summary', selectedAccount],    queryFn: () => fetchSummary(selectedAccount) });
  const resources  = useQuery({ queryKey: ['resources', selectedAccount],  queryFn: () => fetchResources(selectedAccount) });
  // placeholderData keeps the previous window's costs visible while the new
  // window's request is in flight — chip clicks invite rapid exploration and
  // a fresh isLoading state on every click flashes the chart on each change.
  const costs      = useQuery({
    queryKey: ['costs', selectedAccount, period, customRange?.sinceIso, customRange?.untilIso],
    queryFn: () => fetchCosts(selectedAccount, null, period, customRange?.sinceIso, customRange?.untilIso),
    placeholderData: (prev) => prev,
  });
  // placeholderData mirrors the costs query so the chart doesn't flash empty
  // when account-switching (chip changes don't invalidate this key — the
  // period/customRange window is applied client-side in dailyTotals — so the
  // placeholder only matters for the account selector path).
  const trend      = useQuery({ queryKey: ['trend', selectedAccount],      queryFn: () => fetchTrend(selectedAccount), placeholderData: (prev) => prev });
  const dismissals = useQuery({ queryKey: ['dismissals', selectedAccount], queryFn: () => fetchDismissals(selectedAccount) });

  const totalSpend = useMemo(() => sumCostRecords(costs.data), [costs.data]);

  const isLoading    = summary.isLoading || resources.isLoading;
  const isError      = summary.isError   || resources.isError;
  const isRefreshing = summary.isFetching || resources.isFetching;

  const dismissedSet = useMemo(() => {
    const set = new Set();
    (dismissals.data ?? []).forEach(d => set.add(d.resource_id));
    return set;
  }, [dismissals.data]);

  const owners = useMemo(() => {
    const set = new Set((resources.data ?? []).map(r => r.owner).filter(Boolean));
    return [...set].sort();
  }, [resources.data]);

  const byService = useMemo(
    () => Object.entries(summary.data?.by_service ?? {}).sort((a, b) => b[1].savings - a[1].savings),
    [summary.data],
  );

  // Hidden-view sub-pill counts. Lifted above the isLoading/isError early
  // returns so the hook order stays stable across renders.
  const dismissedCount = useMemo(
    () => (dismissals.data ?? []).filter(d => d.action === 'dismiss').length,
    [dismissals.data],
  );
  const snoozedCount = useMemo(
    () => (dismissals.data ?? []).filter(d => d.action === 'snooze').length,
    [dismissals.data],
  );

  // Distinct resource sub-types within the currently selected service.
  // Sub-types are service-scoped, so we only surface them when exactly one
  // service is selected — otherwise the labels would collide across services.
  const resourceTypes = useMemo(() => {
    if (filterSvcs.size !== 1) return [];
    const [svc] = filterSvcs;
    const set = new Set();
    for (const r of resources.data ?? []) {
      if (r.service !== svc) continue;
      if (dismissedSet.has(r.resource_id)) continue;
      if (r.resource_type) set.add(r.resource_type);
    }
    return [...set].sort();
  }, [resources.data, filterSvcs, dismissedSet]);

  function toggleService(svc) {
    setFilterSvcs((prev) => {
      const next = new Set(prev);
      if (next.has(svc)) next.delete(svc); else next.add(svc);
      // Sub-types only make sense when exactly one service is selected.
      // Nested setter is fine — React batches state updates queued from inside
      // an updater; the resource-type clear runs after filterSvcs commits.
      if (next.size !== 1) setFilterResourceTypes(new Set());
      return next;
    });
  }

  function toggleResourceType(rt) {
    setFilterResourceTypes(prev => {
      const next = new Set(prev);
      next.has(rt) ? next.delete(rt) : next.add(rt);
      return next;
    });
  }

  function clearResourceTypes() {
    setFilterResourceTypes(new Set());
  }

  function refresh() {
    summary.refetch(); resources.refetch(); costs.refetch(); trend.refetch(); dismissals.refetch();
  }

  function toggleSelect(id) {
    setSelected(prev => {
      const next = new Set(prev);
      next.has(id) ? next.delete(id) : next.add(id);
      return next;
    });
  }

  function toggleSelectAll(ids) {
    setSelected(prev => ids.every(id => prev.has(id)) ? new Set() : new Set(ids));
  }

  const scanMutation = useMutation({
    mutationFn: scanAccount,
    onMutate: async (accountId) => {
      // Cancel in-flight refetches so they don't overwrite our optimistic state.
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
      // 409 means the server really is scanning — our optimistic state matches
      // reality. Don't roll back; attach the watcher and inform the user.
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

  const handleScan = (accountId) => scanMutation.mutate(accountId);

  async function handleBulkAction({ reason, note }) {
    const ids = [...selected];
    const items = (resources.data ?? []).filter(r => ids.includes(r.resource_id));
    const action = bulkModal === 'snooze' ? 'snooze' : 'dismiss';
    const snoozeUntil = action === 'snooze'
      ? new Date(Date.now() + 7 * 24 * 60 * 60 * 1000).toISOString()
      : undefined;

    let succeeded = 0;
    for (const item of items) {
      try {
        await dismissZombie({
          accountId: item.internal_account_id, provider: item.provider,
          service: item.service, region: item.region, resourceId: item.resource_id,
          action, reason, note, snoozeUntil,
        });
        succeeded++;
      } catch {
        // skip already-dismissed
      }
    }

    queryClient.invalidateQueries({ queryKey: ['resources'] });
    queryClient.invalidateQueries({ queryKey: ['dismissals'] });
    queryClient.invalidateQueries({ queryKey: ['summary'] });
    setSelected(new Set());
    setBulkModal(null);
    toast(
      `${action === 'snooze' ? 'Snoozed' : 'Dismissed'} ${succeeded} resource${succeeded !== 1 ? 's' : ''}`,
      action === 'snooze' ? 'info' : 'success',
    );
  }

  // Bulk restore — fans out DELETE /v1/dismissals/{id} per selected. The
  // `selected` Set holds dismissal row ids (item.id) in the Hidden tab, so
  // no extra lookup is needed. Per-row errors are swallowed (race with a
  // separate revoke would 404, which is fine — the row is gone either way).
  // Guarded against being called from the zombie tabs (`selected` would hold
  // resource_id strings, every revoke would 404, toast would lie "Restored 0").
  //
  // Parallel via Promise.allSettled — sequential await would stall the UI
  // for hundreds of ms on lists of 50+ rows. The API has no per-account
  // mutex on revoke, so parallel is safe.
  async function handleBulkRestore() {
    if (!showDismissed) return;
    const ids = [...selected];
    const results = await Promise.allSettled(ids.map((id) => revokeDismissal(id)));
    const succeeded = results.filter((r) => r.status === 'fulfilled').length;
    queryClient.invalidateQueries({ queryKey: ['resources'] });
    queryClient.invalidateQueries({ queryKey: ['dismissals'] });
    queryClient.invalidateQueries({ queryKey: ['summary'] });
    setSelected(new Set());
    toast(
      `Restored ${succeeded} resource${succeeded !== 1 ? 's' : ''}`,
      'success',
    );
  }

  const activeFilters = useMemo(() => [
    ...[...filterSvcs].map(svc => ({ key: `svc:${svc}`, label: serviceConfig(svc).label })),
    ...[...filterResourceTypes].map(rt => ({ key: `rt:${rt}`, label: resourceTypeConfig(rt).label })),
    filterOwner && { key: 'owner', label: filterOwner },
  ].filter(Boolean), [filterSvcs, filterResourceTypes, filterOwner]);

  function clearFilter(key) {
    if (key === 'owner') setFilterOwner(null);
    if (key.startsWith('svc:')) {
      const svc = key.slice(4);
      const next = new Set(filterSvcs);
      next.delete(svc);
      setFilterSvcs(next);
      if (next.size !== 1) setFilterResourceTypes(new Set());
    }
    if (key.startsWith('rt:')) {
      const rt = key.slice(3);
      setFilterResourceTypes(prev => {
        const next = new Set(prev);
        next.delete(rt);
        return next;
      });
    }
  }

  // ── Compute list ───────────────────────────────────────────────────────────
  // Memoized so unrelated state changes — selecting a row, opening the bulk
  // bar, typing in search — don't re-run the full filter+sort pipeline over
  // the (potentially several-hundred-row) resource list. Lives above the
  // loading/error early-returns to keep hook order stable.
  const listData = useMemo(() => {
    if (showDismissed) {
      let list = dismissals.data ?? [];
      if (hiddenFilter === 'dismissed') list = list.filter(d => d.action === 'dismiss');
      if (hiddenFilter === 'snoozed')   list = list.filter(d => d.action === 'snooze');
      // Snoozed-only view: sort soonest-returning first so the user sees what's
      // about to come back at the top. Other views keep server order.
      if (hiddenFilter === 'snoozed') {
        list = [...list]
          .map(d => ({ d, t: d.snoozed_until ? new Date(d.snoozed_until).getTime() : Infinity }))
          .sort((a, b) => a.t - b.t)
          .map(x => x.d);
      }
      return list;
    }
    let list = resources.data ?? [];
    if (zombieOnly) list = list.filter(r => r.is_zombie);
    list = list.filter(r => !dismissedSet.has(r.resource_id));
    if (filterSvcs.size > 0)          list = list.filter(r => filterSvcs.has(r.service));
    if (filterResourceTypes.size > 0) list = list.filter(r => filterResourceTypes.has(r.resource_type));
    if (filterOwner)                  list = list.filter(r => r.owner === filterOwner);
    if (search.trim()) {
      const q = search.toLowerCase();
      list = list.filter(r =>
        r.resource_id.toLowerCase().includes(q) ||
        (r.owner ?? '').toLowerCase().includes(q) ||
        r.service.toLowerCase().includes(q) ||
        r.region.toLowerCase().includes(q)
      );
    }
    return sortResources(list, sortBy);
  }, [
    showDismissed, hiddenFilter, dismissals.data, resources.data, zombieOnly,
    dismissedSet, filterSvcs, filterResourceTypes, filterOwner, search, sortBy,
  ]);

  // ── Loading state ──────────────────────────────────────────────────────────
  if (isLoading) {
    return (
      <div style={{ display: 'flex', flex: 1, justifyContent: 'center', alignItems: 'center', backgroundColor: 'var(--color-bg)', minHeight: '60vh', flexDirection: 'column', gap: 14 }}>
        <Spinner size={32} color={'var(--color-accent)'} />
        <span style={{ color: 'var(--color-text-muted)', fontSize: 14 }}>Analysing resources…</span>
      </div>
    );
  }

  if (isError) {
    return (
      <div style={{ display: 'flex', flex: 1, justifyContent: 'center', alignItems: 'center', backgroundColor: 'var(--color-bg)', minHeight: '60vh', flexDirection: 'column', gap: 12, padding: 32 }}>
        <span style={{ fontSize: 28, color: 'var(--color-text-muted)' }}>⚠</span>
        <span style={{ fontSize: 17, fontWeight: 700, color: 'var(--color-text)' }}>Service unavailable</span>
        <span style={{ fontSize: 13, color: 'var(--color-text-muted)', textAlign: 'center', lineHeight: '20px' }}>
          Make sure the API service is running
        </span>
        <button
          onClick={refresh}
          style={{ marginTop: 12, backgroundColor: 'var(--color-accent)', padding: '10px 24px', borderRadius: 8, border: 'none', cursor: 'pointer' }}
        >
          <span style={{ color: 'var(--color-text-on-dark)', fontWeight: 700, fontSize: 14 }}>Retry</span>
        </button>
      </div>
    );
  }

  // Visible IDs depend on the active tab: dismissal-row ids in Hidden, resource
  // ids elsewhere. The `selected` Set always holds whichever shape matches the
  // current tab (cleared on tab switch).
  const visibleIds = showDismissed
    ? listData.filter(d => d.id).map(d => d.id)
    : listData.filter(r => r.resource_id).map(r => r.resource_id);
  const allSelected = visibleIds.length > 0 && visibleIds.every(id => selected.has(id));
  // Zombie-tab selection only — in Hidden mode `selected` holds dismissal-row
  // ids, which never match `resource_id`, so this filter would silently return
  // []. Name makes the zombie-only assumption explicit so a future caller
  // doesn't reuse it for a dismissal-export path.
  const zombieSelectedItems = showDismissed
    ? []
    : (resources.data ?? []).filter(r => selected.has(r.resource_id));

  // ── Render ─────────────────────────────────────────────────────────────────
  return (
    <div style={{ backgroundColor: 'var(--color-bg)', minHeight: '100%', paddingBottom: selected.size > 0 ? (isMobile ? 80 : 100) : 40 }}>

      {/* Account selector + refresh bar */}
      <div style={{
        backgroundColor: 'var(--color-surface)',
        borderBottom: `1px solid var(--color-border)`,
        padding: '16px',
        display: 'flex',
        alignItems: 'center',
        gap: 10,
      }}>
        {accounts.length > 0 ? (
          <AccountSelector
            accounts={accounts}
            selectedAccount={selectedAccount}
            onSelectAccount={onSelectAccount}
            connectHref={connectHref}
            editAccountHref={editAccountHref}
            onScanAccount={handleScan}
          />
        ) : (
          <LinkButton
            to={connectHref}
            style={{ border: `1px dashed var(--color-accent)`, borderRadius: 8, padding: '6px 14px' }}
          >
            <span style={{ color: 'var(--color-accent)', fontSize: 13, fontWeight: 600 }}>+ Connect AWS Account</span>
          </LinkButton>
        )}
        <div style={{ flex: 1 }} />
        {isRefreshing && <Spinner size={14} color={'var(--color-accent)'} />}
        <button
          onClick={refresh}
          aria-label="Refresh data"
          style={{ padding: '5px 8px', background: 'none', border: 'none', cursor: 'pointer' }}
        >
          <svg width="16" height="16" viewBox="0 0 24 24" fill="none" stroke={'var(--color-text-muted)'} strokeWidth="2.5" strokeLinecap="round" strokeLinejoin="round">
            <polyline points="23 4 23 10 17 10" /><polyline points="1 20 1 14 7 14" />
            <path d="M3.51 9a9 9 0 0 1 14.85-3.36L23 10M1 14l4.64 4.36A9 9 0 0 0 20.49 15" />
          </svg>
        </button>
      </div>

      {/* Overview hero */}
      <OverviewHero summary={summary} totalSpend={totalSpend} trend={trend} period={period} customRange={customRange} onPeriodChange={handlePeriodChange} onShowTrend={onShowTrend} onShowCosts={onShowCosts} isMobile={isMobile} />

      {/* Service breakdown */}
      <ServiceBreakdown
        byService={byService}
        currency={summary.data?.currency ?? '$'}
        isMobile={isMobile}
        filterSvcs={filterSvcs}
        onToggleSvc={toggleService}
      />

      {/* Filter pills */}
      <div style={{ padding: '12px 16px 0' }}>
        <FilterPills
          byService={byService}
          owners={owners}
          resourceTypes={resourceTypes}
          filterSvcs={filterSvcs}
          filterOwner={filterOwner}
          filterResourceTypes={filterResourceTypes}
          onToggleSvc={toggleService}
          onFilterOwner={setFilterOwner}
          onToggleResourceType={toggleResourceType}
          onClearResourceTypes={clearResourceTypes}
          currency={summary.data?.currency ?? '$'}
          isMobile={isMobile}
        />
      </div>

      {/* Tab row — switching tabs clears the selection because the `selected`
          Set holds resource_ids in the zombie list and dismissal-row ids in
          the Hidden tab. Mixing them would point bulk actions at the wrong
          rows (e.g. a Restore on a resource_id, a Dismiss on a dismissal_id). */}
      <div style={{ display: 'flex', gap: 6, padding: '0 16px 0', marginTop: 4 }}>
        {[
          { label: 'Zombies', active: zombieOnly && !showDismissed, onClick: () => { setZombieOnly(true); setShowDismissed(false); setSelected(new Set()); } },
          { label: 'All', active: !zombieOnly && !showDismissed, onClick: () => { setZombieOnly(false); setShowDismissed(false); setSelected(new Set()); } },
          (dismissals.data?.length ?? 0) > 0 && {
            label: `Hidden (${dismissals.data?.length})`,
            active: showDismissed,
            onClick: () => { setShowDismissed(v => !v); setSelected(new Set()); },
          },
        ].filter(Boolean).map(({ label, active, onClick }) => (
          <button
            key={label}
            onClick={onClick}
            aria-pressed={active}
            style={{
              padding: '7px 14px',
              borderRadius: 20,
              backgroundColor: active ? 'var(--color-navy)' : 'var(--color-surface-raised)',
              border: `1px solid ${active ? 'var(--color-navy)' : 'var(--color-border)'}`,
              cursor: 'pointer',
              fontSize: 13,
              fontWeight: 600,
              color: active ? '#fff' : 'var(--color-text-mid)',
            }}
          >
            {label}
          </button>
        ))}
      </div>

      {/* Hidden-view sub-filter pills: All / Dismissed / Snoozed */}
      {showDismissed && (dismissals.data?.length ?? 0) > 0 && (
        <div
          role="group"
          aria-label="Filter hidden resources"
          style={{ display: 'flex', gap: 6, padding: '8px 16px 0', flexWrap: 'wrap' }}
        >
          {[
            { value: 'all',       label: `All (${dismissals.data.length})` },
            { value: 'dismissed', label: `Dismissed (${dismissedCount})` },
            { value: 'snoozed',   label: `Snoozed (${snoozedCount})` },
          ].map(p => {
            const active = hiddenFilter === p.value;
            return (
              <button
                key={p.value}
                onClick={() => setHiddenFilter(p.value)}
                aria-pressed={active}
                style={{
                  padding: isMobile ? '7px 12px' : '4px 10px',
                  borderRadius: 14,
                  backgroundColor: active ? 'var(--color-text-mid)' : 'var(--color-surface-raised)',
                  border: `1px solid ${active ? 'var(--color-text-mid)' : 'var(--color-border)'}`,
                  cursor: 'pointer',
                  fontSize: 12,
                  fontWeight: 600,
                  color: active ? '#fff' : 'var(--color-text-mid)',
                }}
              >
                {p.label}
              </button>
            );
          })}
        </div>
      )}

      {/* Search + sort (only for non-dismissed view) */}
      {!showDismissed && (
        <FilterBar
          search={search}
          onSearch={setSearch}
          sortBy={sortBy}
          onSort={setSortBy}
          activeFilters={activeFilters}
          onClearFilter={clearFilter}
          isMobile={isMobile}
        />
      )}

      {/* Section header */}
      <div style={{ display: 'flex', alignItems: 'center', padding: '4px 16px 8px' }}>
        {visibleIds.length > 0 && (
          <input
            type="checkbox"
            checked={allSelected}
            onChange={() => toggleSelectAll(visibleIds)}
            aria-label={showDismissed ? 'Select all hidden resources' : 'Select all resources'}
            style={{ width: 15, height: 15, accentColor: 'var(--color-accent)', marginRight: 10, cursor: 'pointer' }}
          />
        )}
        <span style={{ flex: 1, fontSize: 11, fontWeight: 700, color: 'var(--color-text-muted)', letterSpacing: 1.2, textTransform: 'uppercase' }}>
          {showDismissed
            ? (hiddenFilter === 'snoozed' ? 'Snoozed Resources' : hiddenFilter === 'dismissed' ? 'Dismissed Resources' : 'Hidden Resources')
            : zombieOnly ? `Zombie Resources` : 'All Resources'}
          {!showDismissed && ` · ${listData.length}`}
          {showDismissed && ` · ${listData.length}`}
        </span>
        {!showDismissed && (
          <button
            onClick={() => exportCSV(listData, { zombieOnly }, toast)}
            disabled={listData.length === 0}
            aria-label="Export to CSV"
            style={{
              padding: '4px 10px',
              borderRadius: 6,
              border: `1px solid var(--color-border)`,
              backgroundColor: 'var(--color-surface-raised)',
              cursor: listData.length === 0 ? 'not-allowed' : 'pointer',
              opacity: listData.length === 0 ? 0.5 : 1,
            }}
          >
            <span style={{ fontSize: 11, fontWeight: 700, color: 'var(--color-text-mid)' }}>↓ CSV</span>
          </button>
        )}
      </div>

      {/* Empty state */}
      {listData.length === 0 && (
        <div style={{ display: 'flex', flexDirection: 'column', alignItems: 'center', padding: '48px 32px', gap: 8 }}>
          <span style={{ fontSize: 32 }}>🎉</span>
          <span style={{ fontSize: 16, fontWeight: 700, color: 'var(--color-text)' }}>
            {zombieOnly && !showDismissed ? 'No zombie resources found!' : 'No resources match your filters'}
          </span>
          <span style={{ fontSize: 13, color: 'var(--color-text-muted)', textAlign: 'center' }}>
            {zombieOnly && !showDismissed
              ? 'All your AWS resources appear to be actively used.'
              : 'Try adjusting the search or removing filters.'}
          </span>
        </div>
      )}

      {/* Resource list */}
      {listData.map((item) => (
        showDismissed
          ? <DismissedCard
              key={String(item.id)}
              item={item}
              href={zombieHref({ resource_id: item.resource_id, internal_account_id: item.account_id, region: item.region, service: item.service })}
              isSelected={selected.has(item.id)}
              onToggleSelect={toggleSelect}
            />
          : <ResourceCard
              key={item.resource_id}
              item={item}
              href={zombieHref(item)}
              isSelected={selected.has(item.resource_id)}
              onToggleSelect={toggleSelect}
            />
      ))}

      {/* Bulk action bar — Hidden tab mode (Restore) is selected by passing
          onRestore; zombie list mode wires onDismiss + onSnooze. Export is
          only offered in zombie mode because the existing exportCSV expects
          zombie/resource rows; dismissal rows have a different shape and
          would need their own export path (TODO follow-up). */}
      {selected.size > 0 && (
        <BulkActionBar
          count={selected.size}
          onDismiss={showDismissed ? undefined : () => setBulkModal('dismiss')}
          onSnooze={showDismissed ? undefined : () => setBulkModal('snooze')}
          onRestore={showDismissed ? handleBulkRestore : undefined}
          onExport={showDismissed ? undefined : () => exportCSV(zombieSelectedItems, { zombieOnly }, toast)}
          onClear={() => setSelected(new Set())}
          isMobile={isMobile}
        />
      )}

      {/* Bulk dismiss/snooze modal */}
      <BulkDismissModal
        visible={!!bulkModal}
        onClose={() => setBulkModal(null)}
        onConfirm={handleBulkAction}
        count={selected.size}
        modalAction={bulkModal}
        isDark={isDark}
      />
    </div>
  );
}
