import { useEffect, useRef, useState } from 'react';
import { useNavigate } from 'react-router-dom';
import { useTheme } from '../theme/ThemeContext';
import { useMe } from '../context/MeContext';
import { useToast } from '../context/ToastContext';
import { authSwitchOrg } from '../api/client';
import { queryClient } from '../main';

// OrgSwitcher renders in the top navbar in place of the static org-name
// badge. Two modes:
//
//   - Single-membership user: behaves like the old static badge — just
//     shows the org name. No dropdown, no clickable affordance. The
//     switcher only earns its real estate when there's something to
//     switch *to*.
//
//   - Multi-membership user: clickable button opens a dropdown listing
//     every membership from /v1/me. The currently-active org is marked
//     and not actionable; clicking a different one calls
//     /v1/auth/switch-org. On success: drop org-bound react-query
//     entries (every cached query is bound to the OLD org_id at the
//     cookie layer; refetching them under the new identity gives a
//     clean slate), refresh MeContext, navigate to /. We intentionally
//     leave the dashboard's current page unless the post-switch
//     identity doesn't have access — easiest signal is "go home and
//     let the user re-navigate."
//
// `fallbackName` is shown during the initial /v1/me round-trip so the
// navbar slot isn't blank on first paint. Under DEV_MODE it's the
// parseJwt-extracted DEV_ORG_NAME; otherwise empty until /v1/me lands.
//
// Failure modes:
//   - 403 not_a_member: usually a stale dropdown (admin removed the
//     membership between /v1/me fetch and the click). After refresh,
//     if the user has zero memberships left, bounce to /login — they
//     have no recoverable state. Otherwise stay on / with a toast.
//   - 401: session evaporated. The global UNAUTHORIZED_EVENT handler
//     in App.jsx already navigates to /login on the originating
//     ifetch's 401 dispatch, so this path just toasts (no double-
//     fire of refresh, which would 401 again).
//   - other: generic toast, log to console.
export default function OrgSwitcher({ fallbackName = '' }) {
  const { theme: t, isDark } = useTheme();
  const { me, refresh } = useMe();
  const { toast } = useToast();
  const navigate = useNavigate();
  const [open, setOpen] = useState(false);
  const [busy, setBusy] = useState(false);
  const wrapperRef = useRef(null);

  useEffect(() => {
    if (!open) return;
    const onDocClick = (e) => {
      if (wrapperRef.current && !wrapperRef.current.contains(e.target)) setOpen(false);
    };
    const onEsc = (e) => { if (e.key === 'Escape') setOpen(false); };
    document.addEventListener('mousedown', onDocClick);
    document.addEventListener('keydown', onEsc);
    return () => {
      document.removeEventListener('mousedown', onDocClick);
      document.removeEventListener('keydown', onEsc);
    };
  }, [open]);

  const memberships = me?.memberships || [];
  const activeOrgID = me?.organization_id || '';
  const activeOrgName =
    memberships.find((m) => m.organization_id === activeOrgID)?.organization_name ||
    me?.organization?.name ||
    fallbackName ||
    '';

  // Single-membership (or zero, defensive) → render as a non-interactive
  // badge. Matches the previous AppShell shape exactly so single-org
  // users see no UI change from B1.
  if (memberships.length < 2) {
    if (!activeOrgName) return null;
    return (
      <div style={{
        padding: '4px 10px',
        borderRadius: 7,
        border: `1px solid ${t.border}`,
        backgroundColor: t.surfaceRaised,
      }}>
        <span style={{ fontSize: 12, fontWeight: 600, color: t.textMid }}>{activeOrgName}</span>
      </div>
    );
  }

  async function handlePick(orgID) {
    if (busy) return;
    if (orgID === activeOrgID) {
      // Defensive: server returns 200 no-op for same-org POST, but
      // there's no point round-tripping. Just close the dropdown.
      setOpen(false);
      return;
    }
    setBusy(true);
    try {
      await authSwitchOrg(orgID);
      // Drop every org-bound react-query entry. Predicate-keyed remove
      // (NOT queryClient.clear()) so session-stable queries like
      // ['api-version'] survive — clear() would evict them and their
      // staleTime: Infinity prevents a refetch, leaving the footer's
      // API build-line permanently blank for the rest of the session.
      queryClient.removeQueries({
        predicate: (q) => {
          const root = q.queryKey?.[0];
          // Allowlist of session-stable keys whose data is identical
          // across orgs. Everything else gets wiped.
          return root !== 'api-version' && root !== 'app-version';
        },
      });
      // Re-pull /v1/me so the active org_id + role + memberships array
      // reflect the new binding. Triggers re-render of every component
      // that reads useMe().
      await refresh();
      setOpen(false);
      setBusy(false);
      // Navigate home — pages like /detail/:id are bound to specific
      // resource IDs that don't exist in the new org.
      navigate('/', { replace: true });
    } catch (e) {
      if (e.status === 403) {
        // Stale dropdown. Refresh MeContext, and if that comes back with
        // zero memberships (membership removed across the board, not
        // just from target), bounce to /login — there's no recoverable
        // state. The 403 from ifetch already fired FORBIDDEN_EVENT
        // which triggered MeContext's own refresh, but awaiting again
        // here is harmless (fetchMe in-flight de-dupes).
        toast('You no longer have access to that organisation.', 'error');
        await refresh();
        if (!me?.organization_id) {
          navigate('/login', { replace: true });
          return;
        }
      } else if (e.status === 401) {
        // ifetch already fired UNAUTHORIZED_EVENT which navigates to
        // /login via App.jsx's global handler. Don't call refresh()
        // here — it would 401 again and double-fire the event.
        toast('Sign-in expired. Please sign in again.', 'error');
      } else {
        toast('Could not switch organisations. Please try again.', 'error');
        // eslint-disable-next-line no-console
        console.error('switch-org failed', e);
      }
      setBusy(false);
    }
  }

  return (
    <div ref={wrapperRef} style={{ position: 'relative' }}>
      <button
        type="button"
        onClick={() => setOpen((v) => !v)}
        disabled={busy}
        aria-haspopup="menu"
        aria-expanded={open}
        aria-label="Switch organisation"
        style={{
          display: 'flex',
          alignItems: 'center',
          gap: 6,
          padding: '4px 8px 4px 10px',
          borderRadius: 7,
          border: `1px solid ${t.border}`,
          backgroundColor: t.surfaceRaised,
          cursor: busy ? 'wait' : 'pointer',
        }}
      >
        <span style={{ fontSize: 12, fontWeight: 600, color: t.textMid, maxWidth: 180, overflow: 'hidden', textOverflow: 'ellipsis', whiteSpace: 'nowrap' }}>
          {activeOrgName || 'Pick organisation'}
        </span>
        <Caret color={t.accentMuted} />
      </button>

      {open && (
        <div
          role="menu"
          style={{
            position: 'absolute',
            top: 'calc(100% + 6px)',
            right: 0,
            minWidth: 240,
            backgroundColor: t.surface,
            border: `1px solid ${t.border}`,
            borderRadius: 8,
            boxShadow: isDark ? '0 8px 24px rgba(0,0,0,0.5)' : '0 8px 24px rgba(0,0,0,0.12)',
            padding: 4,
            zIndex: 150,
          }}
        >
          <div style={{
            padding: '6px 10px',
            fontSize: 11,
            fontWeight: 600,
            letterSpacing: 0.4,
            textTransform: 'uppercase',
            color: t.textMuted,
          }}>
            Switch organisation
          </div>
          {memberships.map((m) => {
            const isActive = m.organization_id === activeOrgID;
            return (
              <button
                key={m.organization_id}
                type="button"
                role="menuitem"
                onClick={() => handlePick(m.organization_id)}
                disabled={busy || isActive}
                style={{
                  display: 'flex',
                  width: '100%',
                  alignItems: 'center',
                  justifyContent: 'space-between',
                  gap: 8,
                  padding: '7px 10px',
                  borderRadius: 6,
                  border: 'none',
                  backgroundColor: isActive ? (isDark ? 'rgba(255,255,255,0.04)' : t.accentLight) : 'transparent',
                  color: t.text,
                  fontSize: 13,
                  fontWeight: isActive ? 700 : 500,
                  cursor: isActive ? 'default' : (busy ? 'wait' : 'pointer'),
                  textAlign: 'left',
                  fontFamily: 'inherit',
                }}
                onMouseEnter={(e) => {
                  if (!isActive && !busy) e.currentTarget.style.backgroundColor = t.surfaceRaised;
                }}
                onMouseLeave={(e) => {
                  if (!isActive) e.currentTarget.style.backgroundColor = 'transparent';
                }}
              >
                <span style={{ flex: 1, overflow: 'hidden', textOverflow: 'ellipsis', whiteSpace: 'nowrap' }}>
                  {m.organization_name || m.organization_id}
                </span>
                <span style={{ fontSize: 11, fontWeight: 500, color: t.textMuted, textTransform: 'capitalize' }}>
                  {m.role}
                </span>
                {isActive && <Check color={t.accent} />}
              </button>
            );
          })}
        </div>
      )}
    </div>
  );
}

function Caret({ color }) {
  return (
    <svg width="10" height="10" viewBox="0 0 10 10" aria-hidden focusable="false">
      <path d="M2 4l3 3 3-3" stroke={color} strokeWidth="1.5" fill="none" strokeLinecap="round" strokeLinejoin="round" />
    </svg>
  );
}

function Check({ color }) {
  return (
    <svg width="12" height="12" viewBox="0 0 12 12" aria-hidden focusable="false">
      <path d="M2.5 6.5l2.5 2.5 5-6" stroke={color} strokeWidth="1.8" fill="none" strokeLinecap="round" strokeLinejoin="round" />
    </svg>
  );
}
