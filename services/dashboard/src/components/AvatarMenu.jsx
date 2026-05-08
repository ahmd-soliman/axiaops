import { useEffect, useRef, useState } from 'react';
import { useNavigate } from 'react-router-dom';
import { useTheme } from '../theme/ThemeContext';
import { useMe } from '../context/MeContext';
import { useApp } from '../context/AppContext';

// AvatarMenu replaces the bare "Sign out" button in the top nav. Items
// stay narrow on purpose — destructive actions (Delete account, Download
// data) live on the Profile page itself, not in this dropdown, so a stray
// click can't detonate anything from the navbar.
export default function AvatarMenu() {
  const { theme: t, isDark, paletteId, setPaletteId, palettes } = useTheme();
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
        aria-label="Account menu"
        style={{
          display: 'flex',
          alignItems: 'center',
          gap: 8,
          padding: '4px 10px 4px 4px',
          borderRadius: 7,
          border: `1px solid ${t.border}`,
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
            width: 24,
            height: 24,
            borderRadius: '50%',
            backgroundColor: t.accent,
            color: '#fff',
            fontSize: 12,
            fontWeight: 700,
          }}
        >
          {initial}
        </span>
        <span style={{ fontSize: 12, fontWeight: 600, color: t.accentMuted, maxWidth: 160, overflow: 'hidden', textOverflow: 'ellipsis', whiteSpace: 'nowrap' }}>
          {email || 'Account'}
        </span>
      </button>

      {open && (
        <div
          role="menu"
          style={{
            position: 'absolute',
            top: 'calc(100% + 6px)',
            right: 0,
            minWidth: 320,
            backgroundColor: t.surface,
            border: `1px solid ${t.border}`,
            borderRadius: 8,
            boxShadow: isDark ? '0 8px 24px rgba(0,0,0,0.5)' : '0 8px 24px rgba(0,0,0,0.12)',
            padding: 4,
            zIndex: 150,
          }}
        >
          <MenuItem t={t} onClick={() => go('/profile')}>My Profile</MenuItem>
          <div style={{ height: 1, backgroundColor: t.border, margin: '4px 0' }} />
          <PaletteSwitcher
            t={t}
            isDark={isDark}
            paletteId={paletteId}
            setPaletteId={setPaletteId}
            palettes={palettes}
          />
          <StatusPreview t={t} />
          <div style={{ height: 1, backgroundColor: t.border, margin: '4px 0' }} />
          <MenuItem t={t} onClick={signOut}>Sign Out</MenuItem>
        </div>
      )}
    </div>
  );
}

// Experimental palette switcher (branch: experiment/theme-explore).
// Lets us flip the brand palette live without rebuilding. Remove before
// merging back to develop — or promote to a real preference if we ship it.
function PaletteSwitcher({ t, isDark, paletteId, setPaletteId, palettes }) {
  const entries = Object.values(palettes);
  return (
    <div style={{ padding: '6px 10px 8px' }}>
      <div
        style={{
          fontSize: 10,
          fontWeight: 700,
          letterSpacing: 0.6,
          textTransform: 'uppercase',
          color: t.textMuted,
          marginBottom: 6,
        }}
      >
        Palette (dev)
      </div>
      <div style={{ display: 'flex', flexWrap: 'wrap', gap: '8px 10px' }}>
        {entries.map((p) => {
          const selected = p.id === paletteId;
          const swatch = isDark ? p.swatch.dark : p.swatch.light;
          return (
            <button
              key={p.id}
              type="button"
              onClick={() => setPaletteId(p.id)}
              aria-label={`Use ${p.name} palette`}
              aria-pressed={selected}
              style={{
                display: 'flex',
                flexDirection: 'column',
                alignItems: 'center',
                gap: 3,
                width: 50,
                background: 'transparent',
                border: 'none',
                padding: 0,
                cursor: 'pointer',
                outline: 'none',
              }}
            >
              <span
                aria-hidden="true"
                style={{
                  width: 22,
                  height: 22,
                  borderRadius: '50%',
                  border: selected ? `2px solid ${t.text}` : `1px solid ${t.border}`,
                  backgroundColor: swatch,
                }}
              />
              <span
                style={{
                  fontSize: 10,
                  fontWeight: selected ? 700 : 500,
                  color: selected ? t.text : t.textMuted,
                  whiteSpace: 'nowrap',
                  overflow: 'hidden',
                  textOverflow: 'ellipsis',
                  maxWidth: '100%',
                }}
              >
                {p.name}
              </span>
            </button>
          );
        })}
      </div>
    </div>
  );
}

// Experimental — shows the four semantic status tokens in a single row so we
// can eyeball them across palette/mode flips without triggering the actual
// state in the product (license banner, scan-timeout, etc.).
function StatusPreview({ t }) {
  const tokens = [
    { key: 'error',   label: 'Error',   bg: `${t.error}22`,   fg: t.error },
    { key: 'warning', label: 'Warning', bg: `${t.warning}22`, fg: t.warning },
    { key: 'success', label: 'Success', bg: `${t.success}22`, fg: t.success },
    { key: 'accent',  label: 'Brand',   bg: t.accentLight,    fg: t.accentText },
  ];
  return (
    <div style={{ padding: '4px 10px 8px' }}>
      <div
        style={{
          fontSize: 10,
          fontWeight: 700,
          letterSpacing: 0.6,
          textTransform: 'uppercase',
          color: t.textMuted,
          marginBottom: 6,
        }}
      >
        Status (dev)
      </div>
      <div style={{ display: 'flex', flexWrap: 'wrap', gap: 4 }}>
        {tokens.map(({ key, label, bg, fg }) => (
          <span
            key={key}
            style={{
              fontSize: 10,
              fontWeight: 600,
              padding: '3px 7px',
              borderRadius: 5,
              backgroundColor: bg,
              color: fg,
              whiteSpace: 'nowrap',
            }}
          >
            {label}
          </span>
        ))}
      </div>
    </div>
  );
}

function MenuItem({ t, onClick, children }) {
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
        color: t.text,
        fontSize: 13,
        fontWeight: 500,
        cursor: 'pointer',
      }}
      onMouseEnter={(e) => { e.currentTarget.style.backgroundColor = t.surfaceRaised; }}
      onMouseLeave={(e) => { e.currentTarget.style.backgroundColor = 'transparent'; }}
    >
      {children}
    </button>
  );
}
