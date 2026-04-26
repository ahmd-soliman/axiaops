import { Outlet, useNavigate, useLocation } from 'react-router-dom';
import { useQuery } from '@tanstack/react-query';
import { useTheme } from '../theme/ThemeContext';
import { useApp } from '../context/AppContext';
import { useMe } from '../context/MeContext';
import { fetchVersion } from '../api/client';
import { APP_VERSION, APP_COMMIT_SHA } from '../config';
import AvatarMenu from './AvatarMenu';

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

function IconSettings({ color, size = 18 }) {
  return (
    <svg width={size} height={size} viewBox="0 0 24 24" fill="none" stroke={color} strokeWidth="2" strokeLinecap="round" strokeLinejoin="round">
      <circle cx="12" cy="12" r="3" />
      <path d="M19.4 15a1.65 1.65 0 0 0 .33 1.82l.06.06a2 2 0 0 1-2.83 2.83l-.06-.06a1.65 1.65 0 0 0-1.82-.33 1.65 1.65 0 0 0-1 1.51V21a2 2 0 0 1-4 0v-.09a1.65 1.65 0 0 0-1-1.51 1.65 1.65 0 0 0-1.82.33l-.06.06a2 2 0 0 1-2.83-2.83l.06-.06a1.65 1.65 0 0 0 .33-1.82 1.65 1.65 0 0 0-1.51-1H3a2 2 0 0 1 0-4h.09a1.65 1.65 0 0 0 1.51-1 1.65 1.65 0 0 0-.33-1.82l-.06-.06a2 2 0 0 1 2.83-2.83l.06.06a1.65 1.65 0 0 0 1.82.33H9a1.65 1.65 0 0 0 1-1.51V3a2 2 0 0 1 4 0v.09a1.65 1.65 0 0 0 1 1.51 1.65 1.65 0 0 0 1.82-.33l.06-.06a2 2 0 0 1 2.83 2.83l-.06.06a1.65 1.65 0 0 0-.33 1.82V9a1.65 1.65 0 0 0 1.51 1H21a2 2 0 0 1 0 4h-.09a1.65 1.65 0 0 0-1.51 1z" />
    </svg>
  );
}

// ─── Nav config ───────────────────────────────────────────────────────────────
//
// `requires` gates the entry on a permission grant from MeContext. Items
// without it are visible to every authenticated user. Filtering happens in
// the render path, not here, so role changes show up on the next render.
//
// Settings is ungated: every authenticated user has audit:read, so the
// hub's sub-nav always has at least one accessible tab. The hub itself
// filters tabs per-permission internally.

const NAV_ITEMS = [
  { label: 'Overview', path: '/',         Icon: IconOverview },
  { label: 'Trends',   path: '/trend',    Icon: IconTrend },
  { label: 'Costs',    path: '/cost',     Icon: IconCost },
  { label: 'Settings', path: '/settings', Icon: IconSettings },
];

// ─── Top navbar ───────────────────────────────────────────────────────────────

export default function AppShell() {
  const { theme, isDark, toggleTheme } = useTheme();
  const { orgName } = useApp();
  const { can } = useMe();
  const navigate  = useNavigate();
  const location  = useLocation();
  const t = theme;

  const visibleNavItems = NAV_ITEMS.filter((item) => !item.requires || can(item.requires));

  // Backend build identifier — fetched once per session and cached. The footer
  // pairs it with the dashboard build identifier so support tickets carry both
  // versions in a single click-to-select string. Failures are silently absorbed
  // (apiVersion stays undefined) so a momentarily-unreachable API doesn't break
  // the shell.
  const apiVersion = useQuery({
    queryKey: ['api-version'],
    queryFn: fetchVersion,
    staleTime: Infinity,
    gcTime: Infinity,
    retry: false,
  });

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
          {visibleNavItems.map(({ label, path, Icon }) => {
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

          <AvatarMenu />
        </div>
      </header>

      {/* ── Page content ── */}
      <main id="main-content" style={{ flex: 1, overflowY: 'auto' }}>
        <Outlet />
      </main>

      {/* ── Build footer ── */}
      {/* Tiny, dim, monospace. Identifies the dashboard build *and* the API
          build so support tickets carry both. user-select:all lets a click
          highlight the whole identifier — paste straight into a bug report.

          Version values are rendered verbatim — no "v" prefix wrapper. Tagged
          builds set them to e.g. "v2.6.0" already; branch builds set them to
          the branch slug ("develop", "feature/foo"); local dev shows "dev".
          A hard-coded "v" prefix here would double up to "vv2.6.0" on tags.

          API line is shown only after a successful fetch — a momentarily
          unreachable backend just hides that line rather than yelling. */}
      <footer
        aria-label="Build version"
        title="Click to select build identifier"
        style={{
          padding: '6px 12px',
          textAlign: 'right',
          fontSize: 10,
          fontFamily: 'monospace',
          color: t.textMuted,
          opacity: 0.6,
          flexShrink: 0,
          letterSpacing: 0.3,
          userSelect: 'all',
          lineHeight: '14px',
        }}
      >
        <div>dashboard {APP_VERSION} · {APP_COMMIT_SHA}</div>
        {apiVersion.data && (
          <div>api {apiVersion.data.version} · {apiVersion.data.commit}</div>
        )}
      </footer>

    </div>
  );
}
