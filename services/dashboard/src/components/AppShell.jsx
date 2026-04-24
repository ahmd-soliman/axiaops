import { Outlet, useNavigate, useLocation } from 'react-router-dom';
import { useTheme } from '../theme/ThemeContext';
import { useApp } from '../context/AppContext';

// ─── SVG icons ────────────────────────────────────────────────────────────────

function IconOverview({ color, size = 18 }) {
  return (
    <svg width={size} height={size} viewBox="0 0 24 24" fill="none" stroke={color} strokeWidth="2" strokeLinecap="round" strokeLinejoin="round">
      <rect x="3" y="3" width="7" height="7" />
      <rect x="14" y="3" width="7" height="7" />
      <rect x="3" y="14" width="7" height="7" />
      <rect x="14" y="14" width="7" height="7" />
    </svg>
  );
}

function IconTrend({ color, size = 18 }) {
  return (
    <svg width={size} height={size} viewBox="0 0 24 24" fill="none" stroke={color} strokeWidth="2" strokeLinecap="round" strokeLinejoin="round">
      <polyline points="22 7 13.5 15.5 8.5 10.5 2 17" />
      <polyline points="16 7 22 7 22 13" />
    </svg>
  );
}

function IconSun({ color, size = 17 }) {
  return (
    <svg width={size} height={size} viewBox="0 0 24 24" fill="none" stroke={color} strokeWidth="2" strokeLinecap="round" strokeLinejoin="round">
      <circle cx="12" cy="12" r="4" />
      <line x1="12" y1="2" x2="12" y2="4" />
      <line x1="12" y1="20" x2="12" y2="22" />
      <line x1="4.22" y1="4.22" x2="5.64" y2="5.64" />
      <line x1="18.36" y1="18.36" x2="19.78" y2="19.78" />
      <line x1="2" y1="12" x2="4" y2="12" />
      <line x1="20" y1="12" x2="22" y2="12" />
      <line x1="4.22" y1="19.78" x2="5.64" y2="18.36" />
      <line x1="18.36" y1="5.64" x2="19.78" y2="4.22" />
    </svg>
  );
}

function IconMoon({ color, size = 17 }) {
  return (
    <svg width={size} height={size} viewBox="0 0 24 24" fill="none" stroke={color} strokeWidth="2" strokeLinecap="round" strokeLinejoin="round">
      <path d="M12 3c.132 0 .263 0 .393 0a7.5 7.5 0 0 0 7.92 12.446a9 9 0 1 1 -8.313 -12.454z" />
    </svg>
  );
}

function IconCost({ color, size = 18 }) {
  return (
    <svg width={size} height={size} viewBox="0 0 24 24" fill="none" stroke={color} strokeWidth="2" strokeLinecap="round" strokeLinejoin="round">
      <line x1="12" y1="1" x2="12" y2="23" />
      <path d="M17 5H9.5a3.5 3.5 0 0 0 0 7h5a3.5 3.5 0 0 1 0 7H6" />
    </svg>
  );
}

function IconAudit({ color, size = 18 }) {
  // Clipboard-with-checkmark — reads "logged activity" more cleanly than a
  // generic list icon. Two sides of the clipboard plus a tick inside.
  return (
    <svg width={size} height={size} viewBox="0 0 24 24" fill="none" stroke={color} strokeWidth="2" strokeLinecap="round" strokeLinejoin="round">
      <rect x="5" y="4" width="14" height="17" rx="2" />
      <path d="M9 4V2h6v2" />
      <polyline points="9 13 11 15 15 11" />
    </svg>
  );
}

// ─── Nav config ───────────────────────────────────────────────────────────────

const NAV_ITEMS = [
  { label: 'Overview',         path: '/',      Icon: IconOverview },
  { label: 'Trends',           path: '/trend', Icon: IconTrend },
  { label: 'Costs',            path: '/cost',  Icon: IconCost },
  { label: 'Audit',            path: '/audit', Icon: IconAudit },
];

// ─── Top navbar ───────────────────────────────────────────────────────────────

export default function AppShell() {
  const { theme, isDark, toggleTheme } = useTheme();
  const { orgName, onLogout } = useApp();
  const navigate  = useNavigate();
  const location  = useLocation();
  const t = theme;

  return (
    <div style={{ display: 'flex', flexDirection: 'column', minHeight: '100vh', backgroundColor: t.bg }}>

      {/* ── Sticky top navbar ── */}
      <header
        style={{
          position: 'sticky',
          top: 0,
          zIndex: 100,
          backgroundColor: t.surface,
          borderBottom: `1px solid ${t.border}`,
          height: 52,
          display: 'flex',
          alignItems: 'center',
          padding: '0 16px',
          gap: 8,
          flexShrink: 0,
        }}
      >
        {/* Logo */}
        <span style={{ color: t.accent, fontSize: 17, fontWeight: 800, letterSpacing: 0.3, marginRight: 8 }}>
          AxiaOps
        </span>

        {/* Nav links */}
        <nav aria-label="Main navigation" style={{ display: 'flex', alignItems: 'center', gap: 2, flex: 1 }}>
          {NAV_ITEMS.map(({ label, path, Icon }) => {
            const isActive = path === '/' ? location.pathname === '/' : location.pathname.startsWith(path);
            const activeBg = isDark ? 'rgba(255, 255, 255, 0.05)' : t.accentLight;
            return (
              <button
                key={path}
                onClick={() => navigate(path)}
                aria-current={isActive ? 'page' : undefined}
                style={{
                  display: 'flex',
                  alignItems: 'center',
                  gap: 6,
                  padding: '5px 10px',
                  borderRadius: 7,
                  border: 'none',
                  backgroundColor: isActive ? activeBg : 'transparent',
                  cursor: 'pointer',
                }}
              >
                <Icon color={isActive ? t.accent : t.accentMuted} />
                <span style={{
                  fontSize: 13,
                  fontWeight: isActive ? 700 : 500,
                  color: isActive ? t.accent : t.accentMuted,
                }}>
                  {label}
                </span>
              </button>
            );
          })}
        </nav>

        {/* Right-side actions */}
        <div style={{ display: 'flex', alignItems: 'center', gap: 6 }}>
          {/* Theme toggle */}
          <button
            onClick={toggleTheme}
            aria-label={isDark ? 'Switch to light mode' : 'Switch to dark mode'}
            style={{
              padding: '6px 7px',
              borderRadius: 7,
              border: `1px solid ${t.border}`,
              backgroundColor: 'transparent',
              cursor: 'pointer',
              display: 'flex',
              alignItems: 'center',
            }}
          >
            {isDark ? <IconSun color={t.accentMuted} /> : <IconMoon color={t.accentMuted} />}
          </button>

          {/* Org badge */}
          {orgName && (
            <div style={{
              padding: '4px 10px',
              borderRadius: 7,
              border: `1px solid ${t.border}`,
              backgroundColor: t.surfaceRaised,
            }}>
              <span style={{ fontSize: 12, fontWeight: 600, color: t.textMid }}>{orgName}</span>
            </div>
          )}

          {/* Sign out */}
          <button
            onClick={onLogout}
            aria-label="Sign out"
            style={{
              padding: '5px 11px',
              borderRadius: 7,
              border: `1px solid ${t.border}`,
              backgroundColor: 'transparent',
              cursor: 'pointer',
            }}
          >
            <span style={{ fontSize: 12, fontWeight: 600, color: t.accentMuted }}>Sign out</span>
          </button>
        </div>
      </header>

      {/* ── Page content ── */}
      <main id="main-content" style={{ flex: 1, overflowY: 'auto' }}>
        <Outlet />
      </main>

    </div>
  );
}
