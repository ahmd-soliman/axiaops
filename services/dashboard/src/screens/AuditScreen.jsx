import { useState } from 'react';
import { useInfiniteQuery } from '@tanstack/react-query';
import { fetchAuditEvents } from '../api/client';
import { useTheme } from '../theme/ThemeContext';
import { Spinner } from '../components/primitives';

// ─── Action catalogue ────────────────────────────────────────────────────────
// Keep in sync with services/shared/model/audit.go `AuditAction*`. The order
// here is the order shown in the filter dropdown.
const ACTIONS = [
  { value: '',                       label: 'All actions' },
  { value: 'dismiss_zombie',         label: 'Dismissed' },
  { value: 'snooze_zombie',          label: 'Snoozed' },
  { value: 'revoke_dismissal',       label: 'Revoked' },
  { value: 'scan_triggered',         label: 'Scan triggered' },
  { value: 'account_connected',      label: 'Account connected' },
  { value: 'account_updated',        label: 'Account updated' },
  { value: 'account_deleted',        label: 'Account deleted' },
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

function toneColors(tone, theme, isDark) {
  switch (tone) {
    case 'success': return { bg: isDark ? 'rgba(16,185,129,0.15)' : '#d1fae5', fg: '#10b981' };
    case 'danger':  return { bg: isDark ? 'rgba(239,68,68,0.15)'  : '#fee2e2', fg: '#ef4444' };
    case 'warn':    return { bg: isDark ? 'rgba(245,158,11,0.15)' : '#fef3c7', fg: '#f59e0b' };
    case 'info':    return { bg: isDark ? 'rgba(59,130,246,0.15)' : '#dbeafe', fg: '#3b82f6' };
    case 'accent':  return { bg: theme.accentLight || '#ede9fe',  fg: theme.accent };
    case 'muted':   return { bg: theme.surfaceRaised,             fg: theme.textMuted };
    default:        return { bg: theme.surfaceRaised,             fg: theme.textMuted };
  }
}

function ActionChip({ action, theme, isDark }) {
  const { label, tone } = actionDisplay(action);
  const { bg, fg } = toneColors(tone, theme, isDark);
  return (
    <span style={{
      display: 'inline-block',
      padding: '2px 8px',
      borderRadius: 4,
      fontSize: 11,
      fontWeight: 600,
      color: fg,
      backgroundColor: bg,
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

function MetadataPanel({ event, theme }) {
  const m = event.metadata;
  if (!m || Object.keys(m).length === 0) return null;
  return (
    <pre style={{
      marginTop: 8,
      padding: 10,
      backgroundColor: theme.surfaceRaised,
      border: `1px solid ${theme.border}`,
      borderRadius: 6,
      fontSize: 11,
      lineHeight: '16px',
      color: theme.textMid,
      overflow: 'auto',
      maxWidth: '100%',
    }}>
      {JSON.stringify(m, null, 2)}
    </pre>
  );
}

function EventRow({ event, theme, isDark }) {
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

  // ARIA: role="row" with aria-expanded is the treegrid pattern for an
  // expandable row. Putting role="button" on the row itself would invalidate
  // the role="cell" descendants (cells must live inside a row, not a button)
  // and break column attribution for screen readers.
  return (
    <div
      role="row"
      tabIndex={hasMetadata ? 0 : undefined}
      aria-expanded={hasMetadata ? expanded : undefined}
      style={{
        padding: '12px 16px',
        borderBottom: `1px solid ${theme.border}`,
        cursor: hasMetadata ? 'pointer' : 'default',
      }}
      onClick={hasMetadata ? () => setExpanded((v) => !v) : undefined}
      onKeyDown={handleKey}
    >
      <div style={{ display: 'flex', alignItems: 'center', gap: 12, flexWrap: 'wrap' }}>
        <div role="cell" style={{ flex: '0 0 200px', fontSize: 12, color: theme.textMuted, fontFamily: '"Geist Mono Variable", monospace' }}>
          {fmtTime(event.created_at)}
        </div>
        <div role="cell" style={{ flex: '0 0 140px' }}>
          <ActionChip action={event.action} theme={theme} isDark={isDark} />
        </div>
        <div role="cell" style={{ flex: '1 1 180px', fontSize: 13, color: theme.text, minWidth: 0 }}>
          {event.actor_email || event.user_id || <em style={{ color: theme.textMuted }}>system</em>}
        </div>
        <div role="cell" style={{ flex: '1 1 220px', fontSize: 12, color: theme.textMid, fontFamily: '"Geist Mono Variable", monospace', overflow: 'hidden', textOverflow: 'ellipsis', whiteSpace: 'nowrap', minWidth: 0 }}>
          {event.resource_type ? `${event.resource_type}/${event.resource_id}` : event.resource_id || '—'}
        </div>
        {hasMetadata && (
          <div aria-hidden="true" style={{ flex: '0 0 auto', color: theme.textMuted, fontSize: 12 }}>
            {expanded ? '▾' : '▸'}
          </div>
        )}
      </div>
      {expanded && <MetadataPanel event={event} theme={theme} />}
    </div>
  );
}

// ─── Filters ──────────────────────────────────────────────────────────────────

function Select({ label, value, onChange, options, theme }) {
  return (
    <label style={{ display: 'flex', flexDirection: 'column', gap: 4, minWidth: 140 }}>
      <span style={{ fontSize: 11, fontWeight: 600, color: theme.textMuted, letterSpacing: 1.2, textTransform: 'uppercase' }}>
        {label}
      </span>
      <select
        value={value}
        onChange={(e) => onChange(e.target.value)}
        style={{
          padding: '6px 8px',
          borderRadius: 6,
          border: `1px solid ${theme.border}`,
          backgroundColor: theme.surface,
          color: theme.text,
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

function DateInput({ label, value, onChange, theme }) {
  return (
    <label style={{ display: 'flex', flexDirection: 'column', gap: 4, minWidth: 160 }}>
      <span style={{ fontSize: 11, fontWeight: 600, color: theme.textMuted, letterSpacing: 1.2, textTransform: 'uppercase' }}>
        {label}
      </span>
      <input
        type="date"
        value={value}
        onChange={(e) => onChange(e.target.value)}
        style={{
          padding: '5px 8px',
          borderRadius: 6,
          border: `1px solid ${theme.border}`,
          backgroundColor: theme.surface,
          color: theme.text,
          fontSize: 13,
        }}
      />
    </label>
  );
}

// ─── Main screen ──────────────────────────────────────────────────────────────

export default function AuditScreen() {
  const { theme, isDark } = useTheme();

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

  const events = (query.data?.pages ?? []).flatMap((p) => p.events || []);
  const hasNext = query.hasNextPage;

  return (
    <div style={{ padding: 24 }}>
      {/* Header */}
      <div style={{ marginBottom: 20 }}>
        <h1 style={{ margin: 0, fontSize: 24, fontWeight: 800, color: theme.text }}>
          Audit Log
        </h1>
        <p style={{ margin: '6px 0 0', fontSize: 13, color: theme.textMuted }}>
          Every dismiss, scan, and account change across your organization. Click a row with metadata to expand.
        </p>
      </div>

      {/* Filter bar */}
      <div style={{
        display: 'flex',
        gap: 12,
        flexWrap: 'wrap',
        padding: 16,
        backgroundColor: theme.surface,
        border: `1px solid ${theme.border}`,
        borderRadius: 10,
        marginBottom: 16,
      }}>
        <Select label="Action" value={action} onChange={setAction} options={ACTIONS} theme={theme} />
        <Select label="Resource type" value={resourceType} onChange={setResourceType} options={RESOURCE_TYPES} theme={theme} />
        <DateInput label="From" value={sinceDate} onChange={setSinceDate} theme={theme} />
        <DateInput label="To"   value={untilDate} onChange={setUntilDate} theme={theme} />
      </div>

      {/* Results */}
      <div role="table" aria-label="Audit events" style={{
        backgroundColor: theme.surface,
        border: `1px solid ${theme.border}`,
        borderRadius: 10,
        overflow: 'hidden',
      }}>
        {/* Column headers */}
        <div role="row" style={{
          display: 'flex',
          gap: 12,
          padding: '10px 16px',
          backgroundColor: theme.surfaceRaised,
          borderBottom: `1px solid ${theme.border}`,
          fontSize: 10,
          fontWeight: 700,
          color: theme.textMuted,
          letterSpacing: 1.2,
          textTransform: 'uppercase',
        }}>
          <div role="columnheader" style={{ flex: '0 0 200px' }}>Time</div>
          <div role="columnheader" style={{ flex: '0 0 140px' }}>Action</div>
          <div role="columnheader" style={{ flex: '1 1 180px' }}>Actor</div>
          <div role="columnheader" style={{ flex: '1 1 220px' }}>Resource</div>
          <div role="columnheader" aria-label="Expand" style={{ flex: '0 0 20px' }} />
        </div>

        {query.isLoading && (
          <div style={{ padding: 40, display: 'flex', justifyContent: 'center' }}>
            <Spinner color={theme.accent} />
          </div>
        )}

        {query.isError && (
          <div role="alert" style={{ padding: 40, textAlign: 'center', color: theme.textMuted, fontSize: 13 }}>
            Failed to load audit events.
            {query.error?.message && (
              <div style={{ marginTop: 6, fontSize: 11, fontFamily: '"Geist Mono Variable", monospace', color: theme.textMuted, opacity: 0.7 }}>
                {query.error.message}
              </div>
            )}
          </div>
        )}

        {!query.isLoading && !query.isError && events.length === 0 && (
          <div style={{ padding: 40, textAlign: 'center', color: theme.textMuted, fontSize: 13 }}>
            No audit events match the current filter.
          </div>
        )}

        {events.map((e) => (
          <EventRow key={e.id} event={e} theme={theme} isDark={isDark} />
        ))}

        {hasNext && (
          <div style={{ padding: 16, textAlign: 'center', borderTop: `1px solid ${theme.border}` }}>
            <button
              onClick={() => query.fetchNextPage()}
              disabled={query.isFetchingNextPage}
              style={{
                padding: '8px 16px',
                borderRadius: 6,
                border: `1px solid ${theme.border}`,
                backgroundColor: theme.surface,
                color: theme.text,
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
