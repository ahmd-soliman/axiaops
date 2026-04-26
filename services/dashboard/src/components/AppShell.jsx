import { Outlet, useNavigate, useLocation } from 'react-router-dom';
import { useQuery } from '@tanstack/react-query';
import { useTheme } from '../theme/ThemeContext';
import { useApp } from '../context/AppContext';
import { useMe } from '../context/MeContext';
import { fetchVersion } from '../api/client';
import { APP_VERSION, APP_COMMIT_SHA } from '../config';

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

function IconUsers({ color, size = 18 }) {
  return (
    <svg width={size} height={size} viewBox="0 0 24 24" fill="none" stroke={color} strokeWidth="2" strokeLinecap="round" strokeLinejoin="round">
      <path d="M17 21v-2a4 4 0 0 0-4-4H5a4 4 0 0 0-4 4v2" />
      <circle cx="9" cy="7" r="4" />
      <path d="M23 21v-2a4 4 0 0 0-3-3.87" />
      <path d="M16 3.13a4 4 0 0 1 0 7.75" />
    </svg>
  );
}

function IconAccount({ color, size = 18 }) {
  return (
    <svg width={size} height={size} viewBox="0 0 24 24" fill="none" stroke={color} strokeWidth="2" strokeLinecap="round" strokeLinejoin="round">
      <circle cx="12" cy="8" r="4" />
      <path d="M4 21v-1a6 6 0 0 1 6-6h4a6 6 0 0 1 6 6v1" />
    </svg>
  );
}

// ─── Nav config ───────────────────────────────────────────────────────────────
//
// `requires` gates the entry on a permission grant from MeContext. Items
// without it are visible to every authenticated user. Filtering happens in
// the render path, not here, so role changes show up on the next render.

const NAV_ITEMS = [
  { label: 'Overview', path: '/',      Icon: IconOverview },
  { label: 'Trends',   path: '/trend', Icon: IconTrend },
  { label: 'Costs',    path: '/cost',  Icon: IconCost },
  { label: 'Audit',    path: '/audit', Icon: IconAudit },
  { label: 'Users',    path: '/users', Icon: IconUsers, requires: 'members:invite' },
  { label: 'Account',  path: '/account', Icon: IconAccount },
];

// ─── Top navbar ───────────────────────────────────────────────────────────────

export default function AppShell() {
  const { theme, isDark, toggleTheme } = useTheme();
  const { orgName, onLogout } = useApp();
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
