import { useEffect, useRef, useState } from 'react';
import { useNavigate } from 'react-router-dom';
import { useTheme } from '../theme/ThemeContext';
import { useMe } from '../context/MeContext';
import { useApp } from '../context/AppContext';

// AvatarMenu replaces the bare "Sign out" button in the top nav. Items
// stay narrow on purpose — destructive actions (Delete account, Download
// data) live on the Profile page itself, not in this dropdown, so a stray
// click can't detonate anything from the navbar.
//
// `compact` is set by AppShell at xs/sm where the email text doesn't fit
// alongside the logo, hamburger, and other navbar mass. Compact drops the
// email span and renders just the avatar circle as the trigger; the
// dropdown body is unchanged.
export default function AvatarMenu({ compact = false }) {
  const { isDark } = useTheme();
  const { me } = useMe();
  const { onLogout } = useApp();
  const navigate = useNavigate();
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

  const email = me?.email || '';
  const initial = (email[0] || '?').toUpperCase();

  const go = (path) => { setOpen(false); navigate(path); };
  const signOut = () => { setOpen(false); onLogout(); };

  return (
    <div ref={wrapperRef} style={{ position: 'relative' }}>
      <button
        type="button"
        onClick={() => setOpen((v) => !v)}
        aria-haspopup="menu"
        aria-expanded={open}
        aria-label={compact && email ? `Account menu (${email})` : 'Account menu'}
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
            {email || 'Account'}
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
            backgroundColor: 'var(--color-surface)',
            border: '1px solid var(--color-border)',
            borderRadius: 8,
            boxShadow: isDark ? '0 8px 24px rgba(0,0,0,0.5)' : '0 8px 24px rgba(0,0,0,0.12)',
            padding: 4,
            zIndex: 150,
          }}
        >
          <MenuItem onClick={() => go('/settings')}>Settings</MenuItem>
          <div style={{ height: 1, backgroundColor: 'var(--color-border)', margin: '4px 0' }} />
          <MenuItem onClick={signOut}>Sign Out</MenuItem>
        </div>
      )}
    </div>
  );
}

function MenuItem({ onClick, children }) {
  return (
    <button
      type="button"
      role="menuitem"
      onClick={onClick}
      style={{
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
      }}
      onMouseEnter={(e) => { e.currentTarget.style.backgroundColor = 'var(--color-surface-raised)'; }}
      onMouseLeave={(e) => { e.currentTarget.style.backgroundColor = 'transparent'; }}
    >
      {children}
    </button>
  );
}
