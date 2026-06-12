import { useEffect, useRef, useState } from 'react';
import { Link } from 'react-router-dom';
import { useTheme } from '../theme/ThemeContext';
import { useMe } from '../context/MeContext';
import { useApp } from '../context/AppContext';

// AvatarMenu replaces the bare "Sign out" button in the top nav. Items
// stay narrow on purpose — destructive actions (Delete account, Download
// data) live on the Profile page itself, not in this dropdown, so a stray
// click can't detonate anything from the navbar.
//
// `compact` is set by AppShell at xs/sm where the user label doesn't fit
// alongside the logo, hamburger, and other navbar mass. Compact drops the
// label span and renders just the avatar circle as the trigger; the
// dropdown body is unchanged.
export default function AvatarMenu({ compact = false }) {
  const { isDark } = useTheme();
  const { me } = useMe();
  const { onLogout } = useApp();
  const [open, setOpen] = useState(false);
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

  const pickLabel = () => {
    const firstName = (me?.name || '').trim().split(/\s+/)[0];
    if (firstName) return firstName;
    const email = me?.email || '';
    const localPart = email.split('@')[0];
    if (localPart) return localPart;
    return 'Account';
  };
  const label = pickLabel();
  const initial = (label[0] || '?').toUpperCase();

  const signOut = () => { setOpen(false); onLogout(); };

  return (
    <div ref={wrapperRef} style={{ position: 'relative' }}>
      <button
        type="button"
        onClick={() => setOpen((v) => !v)}
        aria-haspopup="menu"
        aria-expanded={open}
        aria-label={compact && label !== 'Account' ? `Account menu (${label})` : 'Account menu'}
        style={compact ? {
          display: 'inline-flex',
          alignItems: 'center',
          justifyContent: 'center',
          width: 44,
          height: 44,
          borderRadius: 22,
          border: '1px solid var(--color-border)',
          backgroundColor: 'transparent',
          cursor: 'pointer',
          padding: 0,
        } : {
          display: 'flex',
          alignItems: 'center',
          gap: 8,
          padding: '4px 10px 4px 4px',
          borderRadius: 7,
          border: '1px solid var(--color-border)',
          backgroundColor: 'transparent',
          cursor: 'pointer',
        }}
      >
        <span
          aria-hidden="true"
          style={{
            display: 'inline-flex',
            alignItems: 'center',
            justifyContent: 'center',
            width: compact ? 28 : 24,
            height: compact ? 28 : 24,
            borderRadius: '50%',
            backgroundColor: 'var(--color-accent)',
            color: 'var(--color-text-on-dark)',
            fontSize: compact ? 13 : 12,
            fontWeight: 700,
          }}
        >
          {initial}
        </span>
        {!compact && (
          <span style={{ fontSize: 12, fontWeight: 600, color: 'var(--color-accent-muted)', maxWidth: 160, overflow: 'hidden', textOverflow: 'ellipsis', whiteSpace: 'nowrap' }}>
            {label}
          </span>
        )}
      </button>

      {open && (
        <div
          role="menu"
          style={{
            position: 'absolute',
            top: 'calc(100% + 6px)',
            right: 0,
            minWidth: 200,
            maxWidth: 280,
            backgroundColor: 'var(--color-surface)',
            border: '1px solid var(--color-border)',
            borderRadius: 8,
            boxShadow: isDark ? '0 8px 24px rgba(0,0,0,0.5)' : '0 8px 24px rgba(0,0,0,0.12)',
            padding: 4,
            zIndex: 150,
          }}
        >
          {/*
            Identity header — non-clickable. Trigger label stays first-name
            only (MR !164, 2026-05-19); the email lives here so a user
            switching between work and personal orgs can confirm which
            account they're signed in as without leaving the navbar. Falls
            back gracefully when either field is missing.
          */}
          <IdentityHeader name={me?.name || ''} email={me?.email || ''} />
          <div style={{ height: 1, backgroundColor: 'var(--color-border)', margin: '4px 0' }} />
          <MenuItem to="/settings" onClick={() => setOpen(false)}>Settings</MenuItem>
          <div style={{ height: 1, backgroundColor: 'var(--color-border)', margin: '4px 0' }} />
          <MenuItem onClick={signOut}>Sign Out</MenuItem>
        </div>
      )}
    </div>
  );
}

function IdentityHeader({ name, email }) {
  // Three render shapes, in order of preference:
  //   1. name + email — both lines, name bold + email muted.
  //   2. email only — single line, no fake-name placeholder.
  //   3. neither — render nothing; the dropdown is just Settings + Sign Out.
  if (!name && !email) return null;
  return (
    <div
      style={{
        padding: '7px 10px 6px 10px',
        cursor: 'default',
      }}
    >
      {name && (
        <div
          style={{
            fontSize: 13,
            fontWeight: 700,
            color: 'var(--color-text)',
            overflow: 'hidden',
            textOverflow: 'ellipsis',
            whiteSpace: 'nowrap',
          }}
          title={name}
        >
          {name}
        </div>
      )}
      {email && (
        <div
          style={{
            fontSize: 11,
            color: 'var(--color-text-muted)',
            overflow: 'hidden',
            textOverflow: 'ellipsis',
            whiteSpace: 'nowrap',
            marginTop: name ? 1 : 0,
          }}
          title={email}
        >
          {email}
        </div>
      )}
    </div>
  );
}

// MenuItem — a dropdown row. Navigation items pass `to` and render as a real
// <Link> (issue #130: so Ctrl/middle-click "Settings" opens it in a new tab);
// action items (Sign Out) omit `to` and render as a <button>. Both share the
// same chrome and still run `onClick` on plain activation to close the menu.
function MenuItem({ to, onClick, children }) {
  const style = {
    display: 'block',
    width: '100%',
    textAlign: 'left',
    padding: '7px 10px',
    borderRadius: 6,
    border: 'none',
    backgroundColor: 'transparent',
    color: 'var(--color-text)',
    fontSize: 13,
    fontWeight: 500,
    cursor: 'pointer',
    textDecoration: 'none',
    boxSizing: 'border-box',
  };
  const hover = {
    onMouseEnter: (e) => { e.currentTarget.style.backgroundColor = 'var(--color-surface-raised)'; },
    onMouseLeave: (e) => { e.currentTarget.style.backgroundColor = 'transparent'; },
  };
  if (to) {
    return (
      <Link to={to} role="menuitem" onClick={onClick} style={style} {...hover}>
        {children}
      </Link>
    );
  }
  return (
    <button type="button" role="menuitem" onClick={onClick} style={style} {...hover}>
      {children}
    </button>
  );
}
