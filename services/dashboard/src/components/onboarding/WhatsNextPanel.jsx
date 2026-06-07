import { useState } from 'react';
import { Link } from 'react-router-dom';
import { useQuery } from '@tanstack/react-query';
import { fetchAccounts, listMemberships, listInvitations } from '../../api/client';
import { useMe } from '../../context/MeContext';
import { PERM } from '../../api/permissions';

// Per-user localStorage key — namespaced so dismissals on a shared device
// don't leak across logged-in users.
const DISMISSED_KEY = 'axiaops:whatsnext:dismissed';

function readFlag(key, userID) {
  if (typeof window === 'undefined' || !userID) return false;
  return window.localStorage.getItem(`${key}:${userID}`) === '1';
}

function writeFlag(key, userID) {
  if (typeof window === 'undefined' || !userID) return;
  window.localStorage.setItem(`${key}:${userID}`, '1');
}

// WhatsNextPanel — first-run checklist on the dashboard home.
//
// Tiles derive their ✓ state from existing API calls (accounts, memberships,
// invitations). The whole panel can be dismissed via the × button; it also
// auto-hides once every visible tile is complete. See docs/onboarding-wizard.md
// §8.3.
export default function WhatsNextPanel() {
  const { me, can } = useMe();
  const userID = me?.user_id ?? '';

  const [dismissed, setDismissed] = useState(() => readFlag(DISMISSED_KEY, userID));

  const canInvite = can(PERM.MEMBERS_INVITE);
  const canRead   = can(PERM.MEMBERS_READ) || canInvite;

  const accounts = useQuery({
    queryKey: ['accounts'],
    queryFn: fetchAccounts,
  });
  const memberships = useQuery({
    queryKey: ['memberships'],
    queryFn: listMemberships,
    enabled: canRead,
  });
  const invitations = useQuery({
    queryKey: ['invitations', 'pending'],
    queryFn: () => listInvitations('pending'),
    enabled: canInvite,
  });

  // Render nothing until the primary fetch resolves — avoids an "all unchecked"
  // flash for users who already finished onboarding.
  if (accounts.isLoading) return null;
  if (dismissed) return null;

  const accountsArr     = accounts.data ?? [];
  const membershipsArr  = memberships.data ?? [];
  const invitationsArr  = invitations.data ?? [];

  const tiles = [
    {
      key: 'connect',
      label: 'Connect AWS account',
      done: accountsArr.length > 0,
      href: '/connect',
      show: true,
    },
    {
      key: 'invite',
      label: 'Invite members',
      done: membershipsArr.length > 1 || invitationsArr.length > 0,
      href: '/settings/members',
      show: canInvite,
    },
    {
      key: 'scan',
      label: 'Run your first scan',
      done: accountsArr.some((a) => a.last_scanned_at),
      href: '/settings/cloud-accounts',
      show: true,
    },
  ].filter((tile) => tile.show);

  // Auto-hide once every visible tile is checked. Owners who deleted their last
  // account post-onboarding may see it re-appear (acceptable per the design).
  if (tiles.every((tile) => tile.done)) return null;

  function dismiss() {
    writeFlag(DISMISSED_KEY, userID);
    setDismissed(true);
  }

  return (
    <div
      // `position: fixed` anchors to the viewport ONLY if no ancestor has
      // transform / filter / perspective / will-change / contain / backdrop-filter
      // set — any of those create a new containing block and this panel would
      // silently anchor to that element instead. Don't add those to AppShell.
      style={{
        position: 'fixed',
        bottom: 20,
        right: 20,
        width: 320,
        maxWidth: 'calc(100vw - 32px)',
        backgroundColor: 'var(--color-surface)',
        border: '1px solid var(--color-border)',
        borderRadius: 12,
        padding: '14px 16px',
        boxShadow: '0 8px 24px rgba(0,0,0,0.18)',
        zIndex: 100,
      }}
    >
      <div style={{ display: 'flex', alignItems: 'center', marginBottom: 10 }}>
        <span
          style={{
            fontSize: 11,
            fontWeight: 700,
            color: 'var(--color-text-muted)',
            letterSpacing: 1.2,
            textTransform: 'uppercase',
          }}
        >
          What's next
        </span>
        <div style={{ flex: 1 }} />
        <button
          type="button"
          onClick={dismiss}
          aria-label="Dismiss what's next checklist"
          style={{
            background: 'none',
            border: 'none',
            cursor: 'pointer',
            color: 'var(--color-text-muted)',
            fontSize: 18,
            lineHeight: 1,
            padding: '2px 6px',
          }}
        >
          ×
        </button>
      </div>

      <div
        style={{
          display: 'flex',
          flexDirection: 'column',
          gap: 2,
        }}
      >
        {tiles.map((tile) => (
          <Tile
            key={tile.key}
            label={tile.label}
            done={tile.done}
            href={tile.href}
          />
        ))}
      </div>
    </div>
  );
}

function Tile({ label, done, href }) {
  // Per docs/ui-color-system-review.md §5: strikethrough reads as "removed",
  // not "done" — use a muted-gray label + green check icon for done state.
  // Pending items: neutral text + arrow in brand orange (the arrow is the
  // only brand-accent surface, so the label doesn't read as a hot CTA).
  // Real <Link> so the checklist steps support new-tab open (issue #130).
  return (
    <Link
      to={href}
      style={{
        display: 'flex',
        alignItems: 'center',
        gap: 8,
        padding: '4px 0',
        textDecoration: 'none',
        cursor: 'pointer',
        textAlign: 'left',
      }}
    >
      <CheckCircle done={done} />
      <span
        style={{
          fontSize: 13,
          fontWeight: 600,
          color: done ? 'var(--color-text-muted)' : 'var(--color-text)',
        }}
      >
        {label}
      </span>
      {!done && (
        // Arrow is an affordance hint, not a CTA — keep brand orange reserved
        // for the logo, primary buttons, and active nav (see UI color system
        // review §7). The whole row is clickable; the arrow is decoration.
        <span aria-hidden="true" style={{ color: 'var(--color-text-muted)', fontSize: 13, marginLeft: 'auto', paddingLeft: 8 }}>
          →
        </span>
      )}
    </Link>
  );
}

function CheckCircle({ done }) {
  if (done) {
    return (
      <svg
        width="18"
        height="18"
        viewBox="0 0 24 24"
        fill="none"
        stroke="var(--color-status-ok)"
        strokeWidth="2.5"
        strokeLinecap="round"
        strokeLinejoin="round"
        aria-hidden="true"
      >
        <circle cx="12" cy="12" r="10" />
        <path d="m9 12 2 2 4-4" />
      </svg>
    );
  }
  return (
    <svg
      width="18"
      height="18"
      viewBox="0 0 24 24"
      fill="none"
      stroke="var(--color-text-muted)"
      strokeWidth="2"
      strokeLinecap="round"
      strokeLinejoin="round"
      aria-hidden="true"
    >
      <circle cx="12" cy="12" r="10" />
    </svg>
  );
}
