import { useState, useMemo } from 'react';
import { useInfiniteQuery } from '@tanstack/react-query';
import { fetchAuditEvents } from '../api/client';
import { Spinner } from '../components/primitives';
import { useBreakpoint } from '../components/primitives/useBreakpoint';

// ─── Action catalogue ────────────────────────────────────────────────────────
// Keep in sync with services/shared/model/audit.go `AuditAction*`. The order
// here is the order shown in the filter dropdown.
const ACTIONS = [
  { value: '',                       label: 'All actions' },
  { value: 'dismiss_zombie',         label: 'Dismissed' },
  { value: 'snooze_zombie',          label: 'Snoozed' },
  { value: 'revoke_dismissal',       label: 'Revoked' },
  { value: 'scan_triggered',         label: 'Scan triggered' },
  { value: 'account_connected',      label: 'Account added' },
  { value: 'account_updated',        label: 'Account updated' },
  { value: 'account_deleted',        label: 'Account removed' },
  { value: 'member_invited',         label: 'Member added' },
  { value: 'member_role_changed',    label: 'Role changed' },
  { value: 'member_removed',         label: 'Member removed' },
  { value: 'ownership_transferred',  label: 'Ownership transferred' },
];

const RESOURCE_TYPES = [
  { value: '',           label: 'All resources' },
  { value: 'dismissal',  label: 'Dismissals' },
  { value: 'account',    label: 'Accounts' },
  { value: 'membership', label: 'Members' },
  { value: 'organization', label: 'Organization' },
];

const PAGE_SIZE = 50;

// Friendly label + accent colour per action. Accent colour keys map to keys on
// the theme object so the badge adapts to light/dark.
function actionDisplay(action) {
  switch (action) {
    case 'dismiss_zombie':         return { label: 'Dismissed',             tone: 'muted' };
    case 'snooze_zombie':          return { label: 'Snoozed',               tone: 'info' };
    case 'revoke_dismissal':       return { label: 'Revoked',               tone: 'warn' };
    case 'scan_triggered':         return { label: 'Scan triggered',        tone: 'accent' };
    case 'account_connected':      return { label: 'Account added',         tone: 'success' };
    case 'account_updated':        return { label: 'Account updated',       tone: 'info' };
    case 'account_deleted':        return { label: 'Account removed',       tone: 'danger' };
    case 'member_invited':         return { label: 'Member added',          tone: 'success' };
    case 'member_role_changed':    return { label: 'Role changed',          tone: 'info' };
    case 'member_removed':         return { label: 'Member removed',        tone: 'danger' };
    case 'ownership_transferred':  return { label: 'Ownership transferred', tone: 'warn' };
    default:                       return { label: action,                  tone: 'muted' };
  }
}

function toneFg(tone) {
  switch (tone) {
    case 'success': return 'var(--color-success)';
    case 'danger':  return 'var(--color-error)';
    case 'warn':    return 'var(--color-warning)';
    case 'info':    return '#3b82f6'; // blue — no semantic token in theme
    case 'accent':  return 'var(--color-accent)';
    case 'muted':   return 'var(--color-text-muted)';
    default:        return 'var(--color-text-muted)';
  }
}

function ActionChip({ action }) {
  const { label, tone } = actionDisplay(action);
  return (
    <span style={{
      fontSize: 11,
      fontWeight: 600,
      color: toneFg(tone),
      letterSpacing: 0.2,
      whiteSpace: 'nowrap',
    }}>
      {label}
    </span>
  );
}

// localDateBoundary parses a "YYYY-MM-DD" string from a <input type="date">
// element and returns a Date at the given local-time wall-clock — the
// numeric constructor avoids the spec ambiguity of date-time strings without
// timezone offsets and handles DST-gap days (where local midnight may not
// exist) by resolving to the next valid local instant.
function localDateBoundary(yyyyMmDd, hh, mm, ss, ms) {
  const [y, m, d] = yyyyMmDd.split('-').map(Number);
  return new Date(y, m - 1, d, hh, mm, ss, ms);
}

// Always render audit timestamps in UTC and label them as such. Audit logs
// are a compliance/security artefact — comparing two operators' views of the
// same event must produce the same string regardless of where they're sitting.
// The trailing " UTC" makes the timezone explicit so nobody has to guess.
function fmtTime(iso) {
  if (!iso) return '';
  const d = new Date(iso);
  const formatted = d.toLocaleString('en-GB', {
    day: '2-digit', month: 'short', year: 'numeric',
    hour: '2-digit', minute: '2-digit', second: '2-digit',
    timeZone: 'UTC',
  });
  return `${formatted} UTC`;
}

// ActorCell renders the human attribution column. Three cases the row layout
// has to handle gracefully:
//   1) named user:    "Alice Engineer"  +  "alice@acme.com" (muted)
//   2) emailed-only:  "alice@acme.com"  (single line, name absent)
//   3) system:        italicised "system"  (no actor_email)
// The collapse from 2 → 1 line is deliberate: a row with no name shouldn't
// reserve vertical space that would look like a layout bug.
//
// actor_email is denormalised on write (see services/shared/model/audit.go),
// so any user-initiated row carries it; a missing actor_email genuinely
// indicates a system action. We do NOT fall back to user_id — that's a UUID
// and surfacing it as the human label would be a UX regression.
function ActorCell({ event }) {
  if (!event.actor_email) {
    return <em style={{ color: 'var(--color-text-muted)' }}>system</em>;
  }
  const name = event.actor_name || '';
  if (!name) {
    return <span>{event.actor_email}</span>;
  }
  return (
    <span style={{ display: 'inline-flex', flexDirection: 'column', minWidth: 0 }}>
      <span style={{ overflow: 'hidden', textOverflow: 'ellipsis', whiteSpace: 'nowrap' }}>{name}</span>
      <span style={{ fontSize: 11, color: 'var(--color-text-muted)', overflow: 'hidden', textOverflow: 'ellipsis', whiteSpace: 'nowrap' }}>{event.actor_email}</span>
    </span>
  );
}

function MetadataPanel({ event }) {
  const m = event.metadata;
  if (!m || Object.keys(m).length === 0) return null;
  return (
    <pre style={{
      marginTop: 8,
      padding: 10,
      backgroundColor: 'var(--color-surface-raised)',
      border: `1px solid var(--color-border)`,
      borderRadius: 6,
      fontSize: 11,
      lineHeight: '16px',
      color: 'var(--color-text-mid)',
      overflow: 'auto',
      maxWidth: '100%',
    }}>
      {JSON.stringify(m, null, 2)}
    </pre>
  );
}

function EventRow({ event, isMobile }) {
  const [expanded, setExpanded] = useState(false);
  const hasMetadata = event.metadata && Object.keys(event.metadata).length > 0;

  // Toggle on Enter or Space when the row is keyboard-focused. Without this,
  // a click-to-expand row is invisible to keyboard-only users.
  function handleKey(e) {
    if (!hasMetadata) return;
    if (e.key === 'Enter' || e.key === ' ') {
      e.preventDefault();
      setExpanded((v) => !v);
    }
  }

  const resourceText = event.resource_type
    ? `${event.resource_type}/${event.resource_id}`
    : event.resource_id || '—';

  // Two-line attribution: actor_name (primary) over actor_email (muted), with
  // a graceful collapse when name is empty (GDPR-anonymised user, system
  // action, or just no display name set). System actions — no actor at all —
  // keep the italicised "system" treatment so they read as not-a-person.
  const actorNode = <ActorCell event={event} />;

  // ARIA: role="row" with aria-expanded is the treegrid pattern for an
  // expandable row. Putting role="button" on the row itself would invalidate
  // the role="cell" descendants (cells must live inside a row, not a button)
  // and break column attribution for screen readers.
  if (isMobile) {
    // Phone layout — desktop's pseudo-table sums to >340px in fixed
    // column bases alone (200 + 140) before any flexible cells, which
    // is wider than a 375px viewport's content area. Stack the cells
    // vertically: action chip + UTC timestamp + chevron on the top
    // row, actor on the second, resource on the third (mono, allowed
    // to wrap with break-all so long ARNs don't horizontal-scroll).
    return (
      <div
        role="row"
        tabIndex={hasMetadata ? 0 : undefined}
        aria-expanded={hasMetadata ? expanded : undefined}
        onClick={hasMetadata ? () => setExpanded((v) => !v) : undefined}
        onKeyDown={handleKey}
        style={{
          padding: '12px 14px',
          borderBottom: `1px solid var(--color-border)`,
          cursor: hasMetadata ? 'pointer' : 'default',
          display: 'flex',
          flexDirection: 'column',
          gap: 4,
        }}
      >
        <div style={{ display: 'flex', alignItems: 'center', gap: 8, flexWrap: 'wrap' }}>
          <ActionChip action={event.action} />
          <span style={{ flex: 1, minWidth: 0 }} />
          <span style={{ fontSize: 11, color: 'var(--color-text-muted)', fontFamily: '"Geist Mono Variable", monospace' }}>
            {fmtTime(event.created_at)}
          </span>
          {hasMetadata && (
            <span aria-hidden="true" style={{ color: 'var(--color-text-muted)', fontSize: 12 }}>
              {expanded ? '▾' : '▸'}
            </span>
          )}
        </div>
        <div style={{ fontSize: 13, color: 'var(--color-text)' }}>
          {actorNode}
        </div>
        <div style={{ fontSize: 12, color: 'var(--color-text-mid)', fontFamily: '"Geist Mono Variable", monospace', wordBreak: 'break-all' }}>
          {resourceText}
        </div>
        {expanded && <MetadataPanel event={event} />}
      </div>
    );
  }

  return (
    <div
      role="row"
      tabIndex={hasMetadata ? 0 : undefined}
      aria-expanded={hasMetadata ? expanded : undefined}
      style={{
        padding: '12px 16px',
        borderBottom: `1px solid var(--color-border)`,
        cursor: hasMetadata ? 'pointer' : 'default',
      }}
      onClick={hasMetadata ? () => setExpanded((v) => !v) : undefined}
      onKeyDown={handleKey}
    >
      <div style={{ display: 'flex', alignItems: 'center', gap: 12, flexWrap: 'wrap' }}>
        <div role="cell" style={{ flex: '0 0 200px', fontSize: 12, color: 'var(--color-text-muted)', fontFamily: '"Geist Mono Variable", monospace' }}>
          {fmtTime(event.created_at)}
        </div>
        <div role="cell" style={{ flex: '0 0 140px' }}>
          <ActionChip action={event.action} />
        </div>
        <div role="cell" style={{ flex: '1 1 180px', fontSize: 13, color: 'var(--color-text)', minWidth: 0 }}>
          {actorNode}
        </div>
        <div role="cell" style={{ flex: '1 1 220px', fontSize: 12, color: 'var(--color-text-mid)', fontFamily: '"Geist Mono Variable", monospace', overflow: 'hidden', textOverflow: 'ellipsis', whiteSpace: 'nowrap', minWidth: 0 }}>
          {resourceText}
        </div>
        {hasMetadata && (
          <div aria-hidden="true" style={{ flex: '0 0 auto', color: 'var(--color-text-muted)', fontSize: 12 }}>
            {expanded ? '▾' : '▸'}
          </div>
        )}
      </div>
      {expanded && <MetadataPanel event={event} />}
    </div>
  );
}

// ─── Filters ──────────────────────────────────────────────────────────────────

function Select({ label, value, onChange, options }) {
  return (
    <label style={{ display: 'flex', flexDirection: 'column', gap: 4, minWidth: 140 }}>
      <span style={{ fontSize: 11, fontWeight: 600, color: 'var(--color-text-muted)', letterSpacing: 1.2, textTransform: 'uppercase' }}>
        {label}
      </span>
      <select
        value={value}
        onChange={(e) => onChange(e.target.value)}
        style={{
          padding: '6px 8px',
          borderRadius: 6,
          border: `1px solid var(--color-border)`,
          backgroundColor: 'var(--color-surface)',
          color: 'var(--color-text)',
          fontSize: 13,
        }}
      >
        {options.map((o) => (
          <option key={o.value} value={o.value}>{o.label}</option>
        ))}
      </select>
    </label>
  );
}

function DateInput({ label, value, onChange }) {
  return (
    <label style={{ display: 'flex', flexDirection: 'column', gap: 4, minWidth: 160 }}>
      <span style={{ fontSize: 11, fontWeight: 600, color: 'var(--color-text-muted)', letterSpacing: 1.2, textTransform: 'uppercase' }}>
        {label}
      </span>
      <input
        type="date"
        value={value}
        onChange={(e) => onChange(e.target.value)}
        style={{
          padding: '5px 8px',
          borderRadius: 6,
          border: `1px solid var(--color-border)`,
          backgroundColor: 'var(--color-surface)',
          color: 'var(--color-text)',
          fontSize: 13,
        }}
      />
    </label>
  );
}

// ─── Main screen ──────────────────────────────────────────────────────────────

export default function AuditScreen() {
  const { isAtMost } = useBreakpoint();
  const isMobile = isAtMost('sm');

  const [action, setAction]             = useState('');
  const [resourceType, setResourceType] = useState('');
  const [sinceDate, setSinceDate]       = useState('');
  const [untilDate, setUntilDate]       = useState('');

  // <input type="date"> emits a local-day string ("2026-04-01"). Build the
  // boundary instants via the year/month/day Date constructor, which always
  // resolves through local calendar arithmetic — including the DST-gap days
  // where "2026-03-29T02:30:00" doesn't exist in CET. Using the string
  // constructor (`new Date("...T00:00:00")`) is correct on most days but
  // produces invalid times on transition days, which would silently shift
  // the filter window by an hour at boundaries. The numeric constructor
  // sidesteps that by interpreting the input as a calendar date, and
  // toISOString() then produces the UTC instant the server expects.
  const since = sinceDate ? localDateBoundary(sinceDate, 0, 0, 0, 0).toISOString() : undefined;
  const until = untilDate ? localDateBoundary(untilDate, 23, 59, 59, 999).toISOString() : undefined;

  const query = useInfiniteQuery({
    queryKey: ['audit', { action, resourceType, since, until }],
    initialPageParam: undefined,
    queryFn: ({ pageParam }) => fetchAuditEvents({
      action: action || undefined,
      resourceType: resourceType || undefined,
      since, until,
      limit: PAGE_SIZE,
      cursor: pageParam,
    }),
    // Empty next_cursor signals end of results. Returning undefined tells
    // React Query there are no more pages, which disables fetchNextPage.
    getNextPageParam: (last) => last.next_cursor || undefined,
  });

  // Flatten the accumulated cursor pages once per data change rather than on
  // every render (filter/breakpoint/theme changes re-render this component).
  const events = useMemo(
    () => (query.data?.pages ?? []).flatMap((p) => p.events || []),
    [query.data],
  );
  const hasNext = query.hasNextPage;

  return (
    <div style={{ padding: isMobile ? 16 : 24 }}>
      {/* Header */}
      <div style={{ marginBottom: 20 }}>
        <h1 style={{ margin: 0, fontSize: 24, fontWeight: 800, color: 'var(--color-text)' }}>
          Audit Log
        </h1>
        <p style={{ margin: '6px 0 0', fontSize: 13, color: 'var(--color-text-muted)' }}>
          Every dismiss, scan, and account change across your organization. Click a row with metadata to expand.
        </p>
      </div>

      {/* Filter bar */}
      <div style={{
        display: 'flex',
        gap: 12,
        flexWrap: 'wrap',
        padding: 16,
        backgroundColor: 'var(--color-surface)',
        border: `1px solid var(--color-border)`,
        borderRadius: 10,
        marginBottom: 16,
      }}>
        <Select label="Action" value={action} onChange={setAction} options={ACTIONS} />
        <Select label="Resource type" value={resourceType} onChange={setResourceType} options={RESOURCE_TYPES} />
        <DateInput label="From" value={sinceDate} onChange={setSinceDate} />
        <DateInput label="To"   value={untilDate} onChange={setUntilDate} />
      </div>

      {/* Results */}
      <div role="table" aria-label="Audit events" style={{
        backgroundColor: 'var(--color-surface)',
        border: `1px solid var(--color-border)`,
        borderRadius: 10,
        overflow: 'hidden',
      }}>
        {/* Column headers — desktop only. The phone layout renders cards
            with implicit semantics (each card carries its action chip,
            timestamp, actor, resource on labelled rows), so a column
            header would be redundant and would force a horizontal-scroll
            mismatch with the cell layout below. */}
        {!isMobile && (
          <div role="row" style={{
            display: 'flex',
            gap: 12,
            padding: '10px 16px',
            backgroundColor: 'var(--color-surface-raised)',
            borderBottom: `1px solid var(--color-border)`,
            fontSize: 10,
            fontWeight: 700,
            color: 'var(--color-text-muted)',
            letterSpacing: 1.2,
            textTransform: 'uppercase',
          }}>
            <div role="columnheader" style={{ flex: '0 0 200px' }}>Time</div>
            <div role="columnheader" style={{ flex: '0 0 140px' }}>Action</div>
            <div role="columnheader" style={{ flex: '1 1 180px' }}>Actor</div>
            <div role="columnheader" style={{ flex: '1 1 220px' }}>Resource</div>
            <div role="columnheader" aria-label="Expand" style={{ flex: '0 0 20px' }} />
          </div>
        )}

        {query.isLoading && (
          <div style={{ padding: 40, display: 'flex', justifyContent: 'center' }}>
            <Spinner color={'var(--color-accent)'} />
          </div>
        )}

        {query.isError && (
          <div role="alert" style={{ padding: 40, textAlign: 'center', color: 'var(--color-text-muted)', fontSize: 13 }}>
            Failed to load audit events.
            {query.error?.message && (
              <div style={{ marginTop: 6, fontSize: 11, fontFamily: '"Geist Mono Variable", monospace', color: 'var(--color-text-muted)', opacity: 0.7 }}>
                {query.error.message}
              </div>
            )}
          </div>
        )}

        {!query.isLoading && !query.isError && events.length === 0 && (
          <div style={{ padding: 40, textAlign: 'center', color: 'var(--color-text-muted)', fontSize: 13 }}>
            No audit events match the current filter.
          </div>
        )}

        {events.map((e) => (
          <EventRow key={e.id} event={e} isMobile={isMobile} />
        ))}

        {hasNext && (
          <div style={{ padding: 16, textAlign: 'center', borderTop: `1px solid var(--color-border)` }}>
            <button
              onClick={() => query.fetchNextPage()}
              disabled={query.isFetchingNextPage}
              style={{
                padding: '8px 16px',
                borderRadius: 6,
                border: `1px solid var(--color-border)`,
                backgroundColor: 'var(--color-surface)',
                color: 'var(--color-text)',
                fontSize: 13,
                fontWeight: 600,
                cursor: query.isFetchingNextPage ? 'wait' : 'pointer',
              }}
            >
              {query.isFetchingNextPage ? 'Loading…' : 'Load more'}
            </button>
          </div>
        )}
      </div>
    </div>
  );
}
