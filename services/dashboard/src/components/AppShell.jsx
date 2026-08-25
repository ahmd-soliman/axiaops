import { Outlet, useLocation } from 'react-router-dom';
import { useQuery } from '@tanstack/react-query';
import { useTheme } from '../theme/ThemeContext';
import { fetchVersion } from '../api/client';
import { useApp } from '../context/AppContext';
import { useMe } from '../context/MeContext';
import { LinkButton } from './primitives';
import { useBreakpoint } from './primitives/useBreakpoint';
import { NAV_ITEMS, isNavActive } from './navItems';
import AvatarMenu from './AvatarMenu';
import OrgSwitcher from './OrgSwitcher';
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
  const location  = useLocation();
  const { isAtMost } = useBreakpoint();

  const isMobile = isAtMost('sm');
  const visibleNavItems = NAV_ITEMS.filter((item) => !item.requires || can(item.requires));

  // Backend build identifier — ['api-version'] cache entry (key + fn +
  // staleTime), so React Query dedupes across consumers and this hook adds
  // ZERO network requests beyond the first. Failures are silently absorbed
  // (`build` stays undefined → no footer) so a momentarily unreachable API
  // doesn't break the shell.
  const { data: build } = useQuery({
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
              {visibleNavItems.map(({ label, path }) => {
                const isActive = isNavActive(path, location.pathname);
                const hoverBg = isDark ? 'rgba(255,255,255,0.05)' : 'rgba(0,0,0,0.04)';
                return (
                  <LinkButton
                    key={path}
                    to={path}
                    aria-current={isActive ? 'page' : undefined}
                    onMouseEnter={(e) => { e.currentTarget.style.backgroundColor = hoverBg; }}
                    onMouseLeave={(e) => { e.currentTarget.style.backgroundColor = 'transparent'; }}
                    style={{
                      padding: '5px 10px',
                      borderRadius: 7,
                      transition: 'background-color 120ms ease',
                    }}
                  >
                    <span style={{
                      fontSize: 13,
                      fontWeight: isActive ? 700 : 550,
                      color: isActive ? 'var(--color-accent)' : 'var(--color-text)',
                    }}>
                      {label}
                    </span>
                  </LinkButton>
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

      {/* ── Page content ── */}
      <main id="main-content" style={{ flex: 1, overflowY: 'auto' }}>
        <Outlet />
      </main>

      {/* ── Build footer ── */}
      {/* Tiny, dim, monospace — identity lives here per docs/versioning.md.
          The dashboard and api ship
          from the same release tag/pipeline and the dashboard has no
          independent bundle version (the VITE_APP_VERSION wiring was dropped
          in cc196b2 — traceability is Path-A-only, via the api's
          /v1/version). So this renders ONE product identity, not a fabricated
          "dashboard X · api X" pair.

          A release tag is immutable and 1:1 with its commit (versioning.md:
          "don't retag"), so it fully pins the build on its own — showing the
          commit alongside would be redundant. Hence the rule is simply: use
          the tag when `version` is a real semver tag, and fall back to the
          commit only when there isn't one (branch/local builds, where
          `version` is a meaningless slug like `develop`/`main`/`dev` and the
          commit is the only unique pin). Production always deploys from a tag
          (deploy:production is semver-gated), so this is tag-only there — the
          commit is never surfaced to customers, which is the intended posture
          (no special-casing needed). Non-prod gets a `· <env>` suffix to
          disambiguate staging/dev from a prod tag. No "v" prefix (tags already
          carry their own shape). userSelect:'all' lets one click highlight the
          whole identifier — paste straight into a support ticket. Renders
          nothing until the query resolves: no "undefined" flash. */}
      {build && (() => {
        const isReleaseTag = /^\d+\.\d+\.\d+(-[0-9A-Za-z.-]+)?$/.test(build.version || '');
        const identifier = isReleaseTag
          ? `AxiaOps ${build.version}`
          : `AxiaOps ${build.commit}`;
        const env = build.env && build.env !== 'production' ? ` · ${build.env}` : '';
        return (
          <footer
            aria-label="Build version"
            title="Click to select build identifier"
            style={{
              padding: '6px 12px',
              textAlign: 'center',
              fontSize: 10,
              fontFamily: '"Geist Mono Variable", monospace',
              color: 'var(--color-text-muted)',
              opacity: 0.7,
              flexShrink: 0,
              letterSpacing: 0.3,
              lineHeight: '14px',
            }}
          >
            <span style={{ userSelect: 'all' }}>{identifier + env}</span>
          </footer>
        );
      })()}

    </div>
  );
}
