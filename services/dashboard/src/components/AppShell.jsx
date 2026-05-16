import { Outlet, useNavigate, useLocation } from 'react-router-dom';
import { useQuery } from '@tanstack/react-query';
import { useTheme } from '../theme/ThemeContext';
import { useApp } from '../context/AppContext';
import { useMe } from '../context/MeContext';
import { fetchVersion } from '../api/client';
import { APP_VERSION, APP_COMMIT_SHA } from '../config';
import { useBreakpoint } from './primitives/useBreakpoint';
import { NAV_ITEMS, isNavActive } from './navItems';
import AvatarMenu from './AvatarMenu';
import OrgSwitcher from './OrgSwitcher';
import LicenseBanner from './LicenseBanner';
import MobileNav from './MobileNav';

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

// AppShell — sticky top navbar + page content + build footer.
//
// Two presentation modes pivoting on viewport width:
//
//   xs/sm (≤767px): logo + hamburger (MobileNav) on the left, AvatarMenu
//     on the right in compact form. Nav links, OrgSwitcher, and theme
//     toggle live inside the MobileNav drawer.
//
//   md/lg (≥768px): the original desktop layout — logo, inline nav links,
//     theme toggle, OrgSwitcher, AvatarMenu (full width with email).
//
// 768px is the cut-over because at sm (480–767px) there's no room for
// 5 nav links + OrgSwitcher (max 180px) + AvatarMenu (max 160px + chrome)
// alongside the 48px logo. At md (768+) the desktop row fits with margin.
export default function AppShell() {
  const { isDark, toggleTheme } = useTheme();
  const { orgName } = useApp();
  const { can } = useMe();
  const navigate  = useNavigate();
  const location  = useLocation();
  const { isAtMost } = useBreakpoint();

  const isMobile = isAtMost('sm');
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
    // --navbar-height: read by descendant pages that need to size relative
    // to the viewport minus the sticky navbar (e.g. Settings sidebar's
    // full-height calc). Single source of truth — when this value changes,
    // every consumer using var(--navbar-height) tracks automatically.
    <div style={{ display: 'flex', flexDirection: 'column', minHeight: '100vh', backgroundColor: 'var(--color-bg)', '--navbar-height': '64px' }}>

      {/* ── Sticky top navbar ── */}
      <header
        style={{
          position: 'sticky',
          top: 0,
          zIndex: 100,
          backgroundColor: 'var(--color-bg-secondary)',
          borderBottom: '1px solid var(--color-border)',
          height: 'var(--navbar-height)',
          display: 'flex',
          alignItems: 'center',
          padding: isMobile ? '0 12px' : '0 16px',
          gap: isMobile ? 8 : 8,
          flexShrink: 0,
        }}
      >
        {/* Logo — SVG lockup. Source picked by isDark so the wordmark's
            "Axia" text contrasts the navbar bg (dark text on light, light on
            dark). The "Ops" wordmark and sonar mark stay constant across modes.
            Slightly smaller on mobile to leave room for the hamburger. */}
        <img
          src={isDark ? '/axiaops-logo-dark.svg' : '/axiaops-logo.svg'}
          alt="AxiaOps"
          style={{
            height: isMobile ? 36 : 48,
            width: 'auto',
            marginRight: isMobile ? 4 : 12,
            display: 'block',
          }}
        />

        {isMobile ? (
          <>
            <div style={{ flex: 1 }} />
            <AvatarMenu compact />
            <MobileNav />
          </>
        ) : (
          <>
            {/* Nav links — color + weight signal active state; bg is reserved
                for hover feedback so inactive items aren't dead targets. */}
            <nav aria-label="Main navigation" style={{ display: 'flex', alignItems: 'center', gap: 2, flex: 1 }}>
              {visibleNavItems.map(({ label, path, Icon }) => {
                const isActive = isNavActive(path, location.pathname);
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
                    <Icon color={isActive ? 'var(--color-accent)' : 'var(--color-text)'} />
                    <span style={{
                      fontSize: 13,
                      fontWeight: isActive ? 700 : 550,
                      color: isActive ? 'var(--color-accent)' : 'var(--color-text)',
                    }}>
                      {label}
                    </span>
                  </button>
                );
              })}
            </nav>

            {/* Right-side actions */}
            <div style={{ display: 'flex', alignItems: 'center', gap: 6 }}>
              {/* Theme toggle — single sun/moon button that flips light↔dark.
                  Cold-load default follows the OS preference (see ThemeContext);
                  once the user clicks the button their choice is persisted. */}
              <button
                onClick={toggleTheme}
                aria-label={isDark ? 'Switch to light mode' : 'Switch to dark mode'}
                style={{
                  padding: '6px 7px',
                  borderRadius: 7,
                  border: '1px solid var(--color-border)',
                  backgroundColor: 'transparent',
                  cursor: 'pointer',
                  display: 'flex',
                  alignItems: 'center',
                }}
              >
                {isDark ? <IconSun color="var(--color-accent-muted)" /> : <IconMoon color="var(--color-accent-muted)" />}
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
          </>
        )}
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

          Version values are rendered verbatim — no "v" prefix wrapper. Per
          docs/versioning.md, release tags are bare semver (e.g. "0.1.0-alpha.1",
          no "v" prefix); branch builds set them to the branch slug ("develop",
          "feature/foo"); local dev shows "dev". Keeping this footer free of a
          hard-coded "v" prevents a future tag-format flip from doubling up.

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
          color: 'var(--color-text-muted)',
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
