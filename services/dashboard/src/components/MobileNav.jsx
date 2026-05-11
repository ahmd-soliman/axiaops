import { useState } from 'react';
import { useNavigate, useLocation } from 'react-router-dom';
import { useTheme } from '../theme/ThemeContext';
import { useMe } from '../context/MeContext';
import { useApp } from '../context/AppContext';
import { useToast } from '../context/ToastContext';
import { authSwitchOrg } from '../api/client';
import { queryClient } from '../main';
import { MobileSheet } from './primitives/MobileSheet';
import { NAV_ITEMS, isNavActive } from './navItems';

// MobileNav — hamburger trigger that lives in the AppShell at xs/sm,
// opening a bottom sheet that lists the nav items + memberships +
// theme toggle. Replaces the desktop top-bar's middle (nav links) and
// right (OrgSwitcher dropdown + theme button) clusters; AvatarMenu
// stays in the navbar in compact form.
//
// Org-switching logic mirrors OrgSwitcher.jsx — the failure shape and
// query-eviction policy are identical and intentionally so. If you
// change the recovery handling there (403 stale-membership, 401
// session-evaporated, etc.), mirror it here. Extracting to a shared
// hook is on the table for a later phase but isn't worth the churn now
// while the screen-by-screen rewrite is still landing.
//
// The hamburger button is rendered inline in this component so callers
// just drop <MobileNav /> into the navbar — AppShell doesn't have to
// hold the open/close state.
export default function MobileNav() {
  const { theme: t, isDark, preference, setPreference } = useTheme();
  const { me, refresh } = useMe();
  const { orgName } = useApp();
  const { toast } = useToast();
  const navigate = useNavigate();
  const location = useLocation();
  const [open, setOpen] = useState(false);
  const [busy, setBusy] = useState(false);

  const memberships = me?.memberships || [];
  const activeOrgID = me?.organization_id || '';
  const activeOrgName =
    memberships.find((m) => m.organization_id === activeOrgID)?.organization_name ||
    me?.organization?.name ||
    orgName ||
    '';

  const close = () => setOpen(false);

  const goTo = (path) => {
    close();
    navigate(path);
  };

  const handlePick = async (orgID) => {
    if (busy || orgID === activeOrgID) {
      close();
      return;
    }
    setBusy(true);
    try {
      await authSwitchOrg(orgID);
      queryClient.removeQueries({
        predicate: (q) => {
          const root = q.queryKey?.[0];
          return root !== 'api-version' && root !== 'app-version';
        },
      });
      await refresh();
      setBusy(false);
      close();
      navigate('/', { replace: true });
    } catch (e) {
      if (e.status === 403) {
        toast('You no longer have access to that organisation.', 'error');
        await refresh();
        if (!me?.organization_id) {
          navigate('/login', { replace: true });
          return;
        }
      } else if (e.status === 401) {
        toast('Sign-in expired. Please sign in again.', 'error');
      } else {
        toast('Could not switch organisations. Please try again.', 'error');
        console.error('switch-org failed', e);
      }
      setBusy(false);
    }
  };

  return (
    <>
      <button
        type="button"
        onClick={() => setOpen(true)}
        aria-label="Open navigation menu"
        aria-expanded={open}
        aria-haspopup="dialog"
        style={{
          display: 'inline-flex',
          alignItems: 'center',
          justifyContent: 'center',
          width: 44,
          height: 44,
          borderRadius: 8,
          border: `1px solid ${t.border}`,
          backgroundColor: 'transparent',
          color: t.text,
          cursor: 'pointer',
        }}
      >
        <Hamburger color={t.text} />
      </button>

      <MobileSheet visible={open} onClose={close} ariaLabel="Main menu">
        <div style={{ padding: '8px 12px 24px' }}>
          <SectionLabel t={t}>Navigate</SectionLabel>
          <nav aria-label="Main navigation">
            {NAV_ITEMS.map(({ label, path, Icon }) => {
              const active = isNavActive(path, location.pathname);
              return (
                <button
                  key={path}
                  type="button"
                  onClick={() => goTo(path)}
                  aria-current={active ? 'page' : undefined}
                  style={navRowStyle(t, isDark, active)}
                >
                  <Icon color={active ? t.accent : t.text} size={20} />
                  <span style={{
                    fontSize: 15,
                    fontWeight: active ? 700 : 550,
                    color: active ? t.accent : t.text,
                  }}>
                    {label}
                  </span>
                </button>
              );
            })}
          </nav>

          {memberships.length > 1 && (
            <>
              <Divider color={t.border} />
              <SectionLabel t={t}>Organisation</SectionLabel>
              <div role="group" aria-label="Switch organisation">
                {memberships.map((m) => {
                  const active = m.organization_id === activeOrgID;
                  return (
                    <button
                      key={m.organization_id}
                      type="button"
                      onClick={() => handlePick(m.organization_id)}
                      disabled={busy || active}
                      style={navRowStyle(t, isDark, active, busy)}
                    >
                      <span style={{
                        flex: 1,
                        textAlign: 'left',
                        fontSize: 15,
                        fontWeight: active ? 700 : 500,
                        color: active ? t.accent : t.text,
                        overflow: 'hidden',
                        textOverflow: 'ellipsis',
                        whiteSpace: 'nowrap',
                      }}>
                        {m.organization_name || m.organization_id}
                      </span>
                      <span style={{
                        fontSize: 12,
                        fontWeight: 500,
                        color: t.textMuted,
                        textTransform: 'capitalize',
                      }}>
                        {m.role}
                      </span>
                    </button>
                  );
                })}
              </div>
            </>
          )}

          {memberships.length === 1 && activeOrgName && (
            <>
              <Divider color={t.border} />
              <SectionLabel t={t}>Organisation</SectionLabel>
              <div style={{
                padding: '12px 14px',
                borderRadius: 8,
                border: `1px solid ${t.border}`,
                backgroundColor: t.surfaceRaised,
                fontSize: 14,
                fontWeight: 600,
                color: t.textMid,
              }}>
                {activeOrgName}
              </div>
            </>
          )}

          <Divider color={t.border} />
          <SectionLabel t={t}>Theme</SectionLabel>
          {[
            { key: 'light',  label: 'Light' },
            { key: 'system', label: 'Match system' },
            { key: 'dark',   label: 'Dark' },
          ].map(({ key, label }) => {
            const active = preference === key;
            return (
              <button
                key={key}
                type="button"
                onClick={() => setPreference(key) /* leave sheet open: small action */}
                aria-pressed={active}
                style={navRowStyle(t, isDark, active)}
              >
                <span style={{ fontSize: 15, fontWeight: active ? 700 : 550, color: active ? t.accent : t.text }}>
                  {label}
                </span>
              </button>
            );
          })}
        </div>
      </MobileSheet>
    </>
  );
}

function navRowStyle(t, isDark, active, disabled = false) {
  return {
    display: 'flex',
    alignItems: 'center',
    gap: 12,
    width: '100%',
    minHeight: 48, // 44px HIG floor; +4 for vertical padding visual
    padding: '8px 14px',
    margin: '2px 0',
    borderRadius: 8,
    border: 'none',
    backgroundColor: active
      ? (isDark ? 'rgba(255,255,255,0.04)' : t.accentLight)
      : 'transparent',
    color: t.text,
    cursor: disabled ? 'wait' : (active ? 'default' : 'pointer'),
    textAlign: 'left',
    fontFamily: 'inherit',
    transition: 'background-color 120ms ease',
  };
}

function SectionLabel({ t, children }) {
  return (
    <div style={{
      padding: '12px 14px 6px',
      fontSize: 11,
      fontWeight: 600,
      letterSpacing: 0.4,
      textTransform: 'uppercase',
      color: t.textMuted,
    }}>
      {children}
    </div>
  );
}

function Divider({ color }) {
  return <div style={{ height: 1, backgroundColor: color, margin: '12px 0' }} />;
}

function Hamburger({ color }) {
  return (
    <svg width="22" height="22" viewBox="0 0 24 24" fill="none" stroke={color} strokeWidth="2" strokeLinecap="round" strokeLinejoin="round" aria-hidden="true">
      <line x1="3" y1="6"  x2="21" y2="6"  />
      <line x1="3" y1="12" x2="21" y2="12" />
      <line x1="3" y1="18" x2="21" y2="18" />
    </svg>
  );
}
