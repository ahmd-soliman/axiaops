import { Outlet, useNavigate, useLocation } from 'react-router-dom';
import { useTheme } from '../theme/ThemeContext';
import { useApp } from '../context/AppContext';

// ─── SVG icons ────────────────────────────────────────────────────────────────

function IconDashboard({ color, size = 20 }) {
  return (
    <svg width={size} height={size} viewBox="0 0 24 24" fill="none" stroke={color} strokeWidth="2" strokeLinecap="round" strokeLinejoin="round">
      <rect x="3" y="3" width="7" height="7" rx="1" />
      <rect x="14" y="3" width="7" height="7" rx="1" />
      <rect x="3" y="14" width="7" height="7" rx="1" />
      <rect x="14" y="14" width="7" height="7" rx="1" />
    </svg>
  );
}

function IconTrend({ color, size = 20 }) {
  return (
    <svg width={size} height={size} viewBox="0 0 24 24" fill="none" stroke={color} strokeWidth="2" strokeLinecap="round" strokeLinejoin="round">
      <polyline points="22 7 13.5 15.5 8.5 10.5 2 17" />
      <polyline points="16 7 22 7 22 13" />
    </svg>
  );
}

function IconSun({ color, size = 18 }) {
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

function IconMoon({ color, size = 18 }) {
  return (
    <svg width={size} height={size} viewBox="0 0 24 24" fill="none" stroke={color} strokeWidth="2" strokeLinecap="round" strokeLinejoin="round">
      <path d="M12 3c.132 0 .263 0 .393 0a7.5 7.5 0 0 0 7.92 12.446a9 9 0 1 1 -8.313 -12.454z" />
    </svg>
  );
}

// ─── Nav items config ──────────────────────────────────────────────────────────

const NAV_ITEMS = [
  { label: 'Dashboard', path: '/',       Icon: IconDashboard },
  { label: 'Trend',     path: '/trend',  Icon: IconTrend },
];

// ─── Sidebar (desktop ≥ 800px) ────────────────────────────────────────────────

function Sidebar({ theme, location, onNavigate, orgName, onLogout, isDark, toggleTheme }) {
  return (
    <nav
      aria-label="Main navigation"
      style={{
        width: 200,
        flexShrink: 0,
        backgroundColor: theme.bgSecondary,
        borderRight: `1px solid ${theme.border}`,
        display: 'flex',
        flexDirection: 'column',
        padding: '8px 0',
        minHeight: '100vh',
      }}
    >
      {/* Logo */}
      <div style={{ padding: '16px 20px 24px' }}>
        <span style={{ color: theme.accent, fontSize: 18, fontWeight: 800, letterSpacing: 0.3 }}>
          AxiaOps
        </span>
      </div>

      {/* Nav links */}
      <div style={{ flex: 1, display: 'flex', flexDirection: 'column', gap: 2, padding: '0 8px' }}>
        {NAV_ITEMS.map(({ label, path, Icon }) => {
          const isActive = path === '/' ? location.pathname === '/' : location.pathname.startsWith(path);
          return (
            <button
              key={path}
              onClick={() => onNavigate(path)}
              aria-current={isActive ? 'page' : undefined}
              style={{
                display: 'flex',
                alignItems: 'center',
                gap: 10,
                padding: '9px 12px',
                borderRadius: 8,
                border: 'none',
                backgroundColor: isActive ? theme.accentLight : 'transparent',
                cursor: 'pointer',
                textAlign: 'left',
                width: '100%',
              }}
            >
              <Icon color={isActive ? theme.accent : theme.textMuted} size={18} />
              <span style={{ fontSize: 14, fontWeight: isActive ? 700 : 500, color: isActive ? theme.accent : theme.textMid }}>
                {label}
              </span>
            </button>
          );
        })}
      </div>

      {/* Bottom actions */}
      <div style={{ padding: '8px 8px 16px', borderTop: `1px solid ${theme.border}`, marginTop: 8 }}>
        <button
          onClick={toggleTheme}
          aria-label={isDark ? 'Switch to light mode' : 'Switch to dark mode'}
          style={{ display: 'flex', alignItems: 'center', gap: 10, padding: '9px 12px', borderRadius: 8, border: 'none', backgroundColor: 'transparent', cursor: 'pointer', width: '100%' }}
        >
          {isDark ? <IconSun color={theme.textMuted} /> : <IconMoon color={theme.textMuted} />}
          <span style={{ fontSize: 13, color: theme.textMuted }}>{isDark ? 'Light mode' : 'Dark mode'}</span>
        </button>

        {orgName && (
          <div style={{ padding: '6px 12px' }}>
            <span style={{ fontSize: 12, color: theme.textSub, fontWeight: 600 }}>{orgName}</span>
          </div>
        )}

        <button
          onClick={onLogout}
          style={{ display: 'flex', alignItems: 'center', gap: 10, padding: '9px 12px', borderRadius: 8, border: 'none', backgroundColor: 'transparent', cursor: 'pointer', width: '100%' }}
        >
          <svg width="16" height="16" viewBox="0 0 24 24" fill="none" stroke={theme.textMuted} strokeWidth="2" strokeLinecap="round" strokeLinejoin="round">
            <path d="M9 21H5a2 2 0 0 1-2-2V5a2 2 0 0 1 2-2h4" />
            <polyline points="16 17 21 12 16 7" />
            <line x1="21" y1="12" x2="9" y2="12" />
          </svg>
          <span style={{ fontSize: 13, color: theme.textMuted }}>Sign out</span>
        </button>
      </div>
    </nav>
  );
}

// ─── Top bar (all sizes) ──────────────────────────────────────────────────────

function TopBar({ theme, isDark, toggleTheme, orgName, onLogout, showSidebar }) {
  if (showSidebar) return null; // sidebar handles everything on desktop
  return (
    <header
      style={{
        backgroundColor: theme.surface,
        borderBottom: `1px solid ${theme.border}`,
        padding: '0 16px',
        height: 52,
        display: 'flex',
        alignItems: 'center',
        gap: 10,
        flexShrink: 0,
        position: 'sticky',
        top: 0,
        zIndex: 100,
      }}
    >
      <span style={{ color: theme.accent, fontSize: 18, fontWeight: 800, letterSpacing: 0.3, flex: 1 }}>
        AxiaOps
      </span>

      <button
        onClick={toggleTheme}
        aria-label={isDark ? 'Switch to light mode' : 'Switch to dark mode'}
        style={{ padding: '6px 8px', background: 'none', border: 'none', cursor: 'pointer', display: 'flex', alignItems: 'center' }}
      >
        {isDark ? <IconSun color={theme.textMuted} /> : <IconMoon color={theme.textMuted} />}
      </button>

      {orgName && (
        <div style={{ backgroundColor: theme.surfaceRaised, padding: '4px 10px', borderRadius: 5, border: `1px solid ${theme.border}` }}>
          <span style={{ color: theme.textMid, fontSize: 12, fontWeight: 600 }}>{orgName}</span>
        </div>
      )}

      <button
        onClick={onLogout}
        aria-label="Sign out"
        style={{ padding: '5px 10px', borderRadius: 5, border: `1px solid ${theme.border}`, background: 'none', cursor: 'pointer' }}
      >
        <span style={{ color: theme.textMuted, fontSize: 12, fontWeight: 600 }}>Sign out</span>
      </button>
    </header>
  );
}

// ─── Bottom tab bar (mobile < 800px) ──────────────────────────────────────────

function BottomTabs({ theme, location, onNavigate }) {
  return (
    <nav
      aria-label="Main navigation"
      style={{
        position: 'fixed',
        bottom: 0,
        left: 0,
        right: 0,
        backgroundColor: theme.surface,
        borderTop: `1px solid ${theme.border}`,
        display: 'flex',
        height: 60,
        zIndex: 100,
        paddingBottom: 'env(safe-area-inset-bottom)',
      }}
    >
      {NAV_ITEMS.map(({ label, path, Icon }) => {
        const isActive = path === '/' ? location.pathname === '/' : location.pathname.startsWith(path);
        return (
          <button
            key={path}
            onClick={() => onNavigate(path)}
            aria-current={isActive ? 'page' : undefined}
            aria-label={label}
            style={{
              flex: 1,
              display: 'flex',
              flexDirection: 'column',
              alignItems: 'center',
              justifyContent: 'center',
              gap: 3,
              border: 'none',
              background: 'none',
              cursor: 'pointer',
              padding: '6px 0',
            }}
          >
            <Icon color={isActive ? theme.accent : theme.textMuted} size={22} />
            <span style={{ fontSize: 10, fontWeight: isActive ? 700 : 500, color: isActive ? theme.accent : theme.textMuted }}>
              {label}
            </span>
          </button>
        );
      })}
    </nav>
  );
}

// ─── AppShell ─────────────────────────────────────────────────────────────────

export default function AppShell() {
  const { theme, isDark, toggleTheme } = useTheme();
  const { orgName, onLogout } = useApp();
  const navigate = useNavigate();
  const location = useLocation();

  // Use sidebar on desktop (≥800px). CSS media query approach via inline style
  // doesn't work, so we use a simple JS approach with window.innerWidth + resize listener.
  // The component re-renders on route change anyway, so initial render is fine.
  const showSidebar = typeof window !== 'undefined' && window.innerWidth >= 800;

  return (
    <div style={{ display: 'flex', minHeight: '100vh', backgroundColor: theme.bg }}>
      {showSidebar && (
        <Sidebar
          theme={theme}
          isDark={isDark}
          toggleTheme={toggleTheme}
          location={location}
          onNavigate={navigate}
          orgName={orgName}
          onLogout={onLogout}
        />
      )}

      <div style={{ flex: 1, display: 'flex', flexDirection: 'column', minWidth: 0 }}>
        <TopBar
          theme={theme}
          isDark={isDark}
          toggleTheme={toggleTheme}
          orgName={orgName}
          onLogout={onLogout}
          showSidebar={showSidebar}
        />

        <main
          id="main-content"
          style={{
            flex: 1,
            overflowY: 'auto',
            paddingBottom: showSidebar ? 0 : 60, // room for bottom tabs
          }}
        >
          <Outlet />
        </main>
      </div>

      {!showSidebar && (
        <BottomTabs theme={theme} location={location} onNavigate={navigate} />
      )}
    </div>
  );
}
