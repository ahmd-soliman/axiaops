import { Navigate, Outlet, useLocation } from 'react-router-dom';
import { useQuery } from '@tanstack/react-query';
import { useTheme } from '../theme/ThemeContext';
import { useMe } from '../context/MeContext';
import { fetchVersion } from '../api/client';
import { LinkButton } from '../components/primitives';
import { useBreakpoint } from '../components/primitives/useBreakpoint';
import { PERM } from '../api/permissions';

// Settings hub: vertical sub-nav (left) + active tab pane (right) on
// desktop. On phones (xs/sm, ≤767px) the aside collapses to a horizontal
// scrollable tab strip pinned under the navbar — the 220px aside left
// 0px of usable content width on a 375px viewport, blocking every
// settings sub-route on mobile.
//
// Tabs are gated by permission; tabs the caller can't see don't render.
//
// Sub-nav fits the SaaS standard (Stripe, Linear, GitHub, Vercel) — keeps
// configuration grouped and out of the top nav, and lets new admin pages
// land here without crowding daily-use routes.

// SSO tab is gated on SSO_MANAGE (owner-only) for now even though the
// backend grants SSO_READ to viewer+. The plan calls for a viewer-facing
// read-only mode of the same panes (per services/shared/authz/roles.go
// comment "the read tier (sso:read) is viewer+ so the dashboard can render
// the SSO settings pane in read-only mode for non-owners"); that mode is
// deferred to a follow-up. Until it lands, exposing the panes to viewers
// would render mutation controls that 403 — worse UX than hiding the tab.
// Tab groups go personal → org-wide. Profile (every user) sits under
// Account; org-shared admin tabs sit under Workspace. `requires` of
// undefined means the item is visible to every authenticated user — the
// visible-filter below handles the absence. Group headers render only on
// desktop; the mobile tab strip flattens them since horizontal scrolling
// + section headers don't compose well at 375px.
//
// License is gated to the same owner-tier as Organization. Licensing is a
// billing/contract concern; non-owners can't act on it and LicenseBanner
// already covers the "scans paused" warning surface for everyone. The pane
// is the affirmative read-only inspector for the healthy + DEV_MODE-bypass
// states the banner is silent on. See pages/settings/License.jsx.
// Workspace before Account: AxiaOps is a workspace tool — users enter
// Settings to manage cloud accounts, members, SSO, etc. far more often
// than to edit their own profile. Surfacing Workspace first matches usage
// frequency and makes the first visible tab (Cloud Accounts) the natural
// landing for /settings without needing a special-case redirect.
const TAB_GROUPS = [
  {
    label: 'Workspace',
    items: [
      { label: 'Cloud Accounts', path: '/settings/cloud-accounts', requires: PERM.ACCOUNTS_READ },
      { label: 'Members',        path: '/settings/members',        requires: PERM.MEMBERS_INVITE },
      // Integrations gated on CHANNELS_MANAGE (admin+) even though the backend
      // grants CHANNELS_READ to viewer+ — same call as the SSO tab above: a
      // viewer-facing read-only mode is deferred, and exposing mutation controls
      // that 403 is worse UX than hiding the tab.
      { label: 'Integrations',   path: '/settings/integrations',   requires: PERM.CHANNELS_MANAGE },
      { label: 'Audit Log',      path: '/settings/audit',          requires: PERM.AUDIT_READ },
      { label: 'SSO',            path: '/settings/sso',            requires: PERM.SSO_MANAGE },
      { label: 'Organization',   path: '/settings/organization',   requires: PERM.ORGANIZATION_DELETE },
      { label: 'License',        path: '/settings/license',        requires: PERM.ORGANIZATION_DELETE },
    ],
  },
  {
    label: 'Account',
    items: [
      { label: 'Profile', path: '/settings/profile' },
    ],
  },
];

// Flat list — used for the visible filter and the Navigate-to-first-tab
// fallback. Order across groups is preserved so visible[0] is still the
// first item on the page (Profile, for any signed-in user).
const TABS = TAB_GROUPS.flatMap((g) => g.items);

export default function Settings() {
  const { isDark } = useTheme();
  const { can, loading, me } = useMe();
  const location = useLocation();
  const { isAtMost } = useBreakpoint();
  const isMobile = isAtMost('sm');

  // Cached ['api-version'] read (same key/fn as AppShell + LicenseBanner →
  // deduped, no extra request) so we can hide the License tab under SaaS.
  const { data: version } = useQuery({
    queryKey: ['api-version'],
    queryFn: fetchVersion,
    staleTime: Infinity,
    gcTime: Infinity,
    retry: false,
  });
  const licenseState = version?.license?.state;

  // Wait for /v1/me before deciding what to render. On first paint
  // `loading` is true and every `can()` returns false — without this gate
  // the user sees the empty-state flash before the redirect can fire.
  //
  // Gated on `!me`: MeContext.refresh() flips loading=true on every
  // refresh (incl. the one mutations fire via invalidate()), not just
  // the initial /v1/me. A naked `if (loading)` would unmount the entire
  // Settings tree on every mutation, taking sub-page local state with it
  // (e.g. the just-set lastInvite state on the Members tab, which makes
  // the post-invite success card never render). Mirrors AuthGuard and
  // OnboardingGate.
  if (loading && !me) return null;

  // SaaS (saashosted build) reports license.state="managed" — there is no
  // customer-facing license under SaaS (design §7.4), so hide the License tab.
  // Same cached ['api-version'] key/fn the LicenseBanner + License page use →
  // React Query dedupes, so this is a cache read, not an extra request.
  const licenseManaged = licenseState === 'managed';
  const visible = TABS.filter((tab) => {
    if (tab.path === '/settings/license' && licenseManaged) return false;
    return !tab.requires || can(tab.requires);
  });

  // Land on first visible tab. Workspace sits above Account in TAB_GROUPS,
  // so for every current role this resolves to Cloud Accounts (or whichever
  // Workspace tab the caller's perms allow) and falls through to Profile
  // only for a hypothetical future role with no Workspace perms. <Navigate>
  // is the declarative form; imperative navigate() during render fights
  // React's render cycle.
  if (
    (location.pathname === '/settings' || location.pathname === '/settings/') &&
    visible.length > 0
  ) {
    return <Navigate to={visible[0].path} replace />;
  }

  return (
    <div style={{
      display: 'flex',
      flexDirection: isMobile ? 'column' : 'row',
      // Pin to viewport (minus the AppShell navbar) so the sidebar bg +
      // right border extend to the bottom of the page even when the right
      // pane has less content. `minHeight: 100%` was unreliable —
      // AppShell's <main> has `flex: 1, overflowY: auto` and no definite
      // height, so the percentage resolved to content-height in some
      // browsers and stub-cut the sidebar mid-page. `--navbar-height` is
      // declared on AppShell's root and tracks the navbar's actual height
      // — single source of truth.
      minHeight: 'calc(100vh - var(--navbar-height))',
      backgroundColor: 'var(--color-bg)',
    }}>
      {isMobile ? (
        <MobileTabs visible={visible} location={location} isDark={isDark} />
      ) : (
        <DesktopAside can={can} location={location} isDark={isDark} />
      )}
      {/* No width cap — settings tabs are full-width like the rest of the
          app; each tab's own padding governs its content inset. */}
      <main style={{ flex: 1, minWidth: 0 }}>
        {visible.length === 0 ? (
          <div style={{ padding: 24, color: 'var(--color-text-muted)', fontSize: 13 }}>
            No settings available for your role.
          </div>
        ) : (
          <Outlet />
        )}
      </main>
    </div>
  );
}

function DesktopAside({ can, location, isDark }) {
  // Filter each group's items by permission and drop empty groups so a
  // viewer (no admin tabs) doesn't see a "Workspace" header with nothing
  // beneath it. Same group order, item order within a group is preserved.
  const visibleGroups = TAB_GROUPS
    .map((g) => ({ ...g, items: g.items.filter((tab) => !tab.requires || can(tab.requires)) }))
    .filter((g) => g.items.length > 0);

  const hoverBg = isDark ? 'rgba(255,255,255,0.05)' : 'rgba(0,0,0,0.04)';

  return (
    <aside
      style={{
        width: 220,
        flexShrink: 0,
        borderRight: `1px solid var(--color-border)`,
        backgroundColor: 'var(--color-surface)',
        padding: '24px 12px',
      }}
    >
      {/* Same active-state model as the top navbar: color + weight only,
          bg is reserved for hover so inactive items aren't dead targets. */}
      {visibleGroups.map((group, idx) => (
        <div key={group.label} style={{ marginTop: idx === 0 ? 0 : 16 }}>
          <h3
            style={{
              margin: '0 8px 8px',
              fontSize: 11,
              fontWeight: 700,
              letterSpacing: 0.5,
              textTransform: 'uppercase',
              color: 'var(--color-text-muted)',
            }}
          >
            {group.label}
          </h3>
          <nav>
            {group.items.map((tab) => {
              const active = location.pathname.startsWith(tab.path);
              return (
                <LinkButton
                  key={tab.path}
                  to={tab.path}
                  aria-current={active ? 'page' : undefined}
                  onMouseEnter={(e) => { e.currentTarget.style.backgroundColor = hoverBg; }}
                  onMouseLeave={(e) => { e.currentTarget.style.backgroundColor = 'transparent'; }}
                  style={{
                    display: 'block',
                    width: '100%',
                    textAlign: 'left',
                    padding: '8px 10px',
                    marginBottom: 2,
                    borderRadius: 6,
                    color: active ? 'var(--color-accent)' : 'var(--color-text)',
                    fontSize: 13,
                    fontWeight: active ? 700 : 550,
                    transition: 'background-color 120ms ease',
                  }}
                >
                  {tab.label}
                </LinkButton>
              );
            })}
          </nav>
        </div>
      ))}
    </aside>
  );
}

// MobileTabs — horizontal scrollable strip pinned under the AppShell navbar.
// Each tab is a pill with an underline-on-active visual. The strip
// `overflow-x: auto`s when the tab labels exceed viewport width so an org
// with every permission can still reach all 5 tabs on a 375px screen.
function MobileTabs({ visible, location, isDark }) {
  return (
    <div
      style={{
        position: 'sticky',
        top: 'var(--navbar-height)', // pinned just below the AppShell navbar
        zIndex: 50, // below navbar (100) and modals (1000)
        backgroundColor: 'var(--color-surface)',
        borderBottom: `1px solid var(--color-border)`,
      }}
    >
      <nav
        aria-label="Settings sections"
        style={{
          display: 'flex',
          gap: 4,
          padding: '8px 12px',
          overflowX: 'auto',
          whiteSpace: 'nowrap',
        }}
      >
        {visible.map((tab) => {
          const active = location.pathname.startsWith(tab.path);
          const hoverBg = isDark ? 'rgba(255,255,255,0.05)' : 'rgba(0,0,0,0.04)';
          return (
            <LinkButton
              key={tab.path}
              to={tab.path}
              aria-current={active ? 'page' : undefined}
              onMouseEnter={(e) => { e.currentTarget.style.backgroundColor = active ? hoverBg : hoverBg; }}
              onMouseLeave={(e) => { e.currentTarget.style.backgroundColor = 'transparent'; }}
              style={{
                flexShrink: 0,
                padding: '10px 14px',
                minHeight: 44, // 44px HIG touch-target floor
                borderBottom: active ? `2px solid var(--color-accent)` : '2px solid transparent',
                color: active ? 'var(--color-accent)' : 'var(--color-text)',
                fontSize: 14,
                fontWeight: active ? 700 : 550,
              }}
            >
              {tab.label}
            </LinkButton>
          );
        })}
      </nav>
    </div>
  );
}
