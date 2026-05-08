import { Outlet, useNavigate, useLocation } from 'react-router-dom';
import { useQuery } from '@tanstack/react-query';
import { useTheme } from '../theme/ThemeContext';
import { useApp } from '../context/AppContext';
import { useMe } from '../context/MeContext';
import { fetchVersion } from '../api/client';
import { APP_VERSION, APP_COMMIT_SHA } from '../config';
import AvatarMenu from './AvatarMenu';
import OrgSwitcher from './OrgSwitcher';
import LicenseBanner from './LicenseBanner';

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

function IconCloud({ color, size = 18 }) {
  return (
    <svg width={size} height={size} viewBox="0 0 24 24" fill="none" stroke={color} strokeWidth="2" strokeLinecap="round" strokeLinejoin="round">
      <path d="M18 10h-1.26A8 8 0 1 0 9 20h9a5 5 0 0 0 0-10z" />
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
  { label: 'Overview',       path: '/',                Icon: IconOverview },
  { label: 'Trends',         path: '/trend',           Icon: IconTrend },
  { label: 'Costs',          path: '/cost',            Icon: IconCost },
  { label: 'Cloud Accounts', path: '/cloud-accounts',  Icon: IconCloud },
  { label: 'Settings',       path: '/settings',        Icon: IconSettings },
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
          backgroundColor: t.bgSecondary,
          borderBottom: `1px solid ${t.border}`,
          height: 52,
          display: 'flex',
          alignItems: 'center',
          padding: '0 16px',
          gap: 8,
          flexShrink: 0,
        }}
      >
        {/* Logo — Geist 700 reads visually as 800 in the system font, plus
            tighter letterSpacing because Geist sets generous spacing by default. */}
        <span style={{ color: t.accent, fontSize: 18, fontWeight: 700, letterSpacing: -0.2, marginRight: 8 }}>
          AxiaOps
        </span>

        {/* Nav links — color + weight signal active state; bg is reserved
            for hover feedback so inactive items aren't dead targets. */}
        <nav aria-label="Main navigation" style={{ display: 'flex', alignItems: 'center', gap: 2, flex: 1 }}>
          {visibleNavItems.map(({ label, path, Icon }) => {
            const isActive = path === '/' ? location.pathname === '/' : location.pathname.startsWith(path);
            const hoverBg = isDark ? 'rgba(255,255,255,0.05)' : 'rgba(0,0,0,0.04)';
            return (
              <button
                key={path}
                onClick={() => navigate(path)}
                aria-current={isActive ? 'page' : undefined}
                onMouseEnter={(e) => { e.currentTarget.style.backgroundColor = hoverBg; }}
                onMouseLeave={(e) => { e.currentTarget.style.backgroundColor = 'transparent'; }}
                style={{
                  display: 'flex',
                  alignItems: 'center',
                  gap: 6,
                  padding: '5px 10px',
                  borderRadius: 7,
                  border: 'none',
                  backgroundColor: 'transparent',
                  cursor: 'pointer',
                  transition: 'background-color 120ms ease',
                }}
              >
                <Icon color={isActive ? t.accent : t.text} />
                <span style={{
                  fontSize: 13,
                  fontWeight: isActive ? 700 : 550,
                  color: isActive ? t.accent : t.text,
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

          {/* Org badge / switcher. OrgSwitcher renders the same static
              badge shape for single-membership users (no UI change vs
              B1) and an interactive dropdown for multi-membership
              users that rotates the session via /v1/auth/switch-org.
              The `fallbackName` fills the navbar slot during the
              initial /v1/me round-trip so it isn't blank on first
              paint. Under DEV_MODE it's the parseJwt-extracted
              DEV_ORG_NAME; otherwise empty until /v1/me lands. */}
          <OrgSwitcher fallbackName={orgName} />

          <AvatarMenu />
        </div>
      </header>

      {/* ── License banner (B1.6 slice 8) ── */}
      {/* Owners only; renders nothing when license is valid + ≥14 days out. */}
      <LicenseBanner />

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
          fontFamily: '"Geist Mono Variable", monospace',
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
