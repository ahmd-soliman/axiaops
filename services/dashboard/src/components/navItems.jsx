// Shared nav config — consumed by AppShell (desktop top-bar) and
// MobileNav (xs/sm hamburger drawer). The two surfaces render the same
// list with different chrome, so the source of truth lives here.
//
// Icons are tied to the nav config rather than to a generic icon module
// because they're only ever drawn at this size + stroke for navigation.
// Adding a sixth nav item: add to NAV_ITEMS, drop a matching Icon* below.

export function IconOverview({ color, size = 18 }) {
  return (
    <svg width={size} height={size} viewBox="0 0 24 24" fill="none" stroke={color} strokeWidth="2" strokeLinecap="round" strokeLinejoin="round">
      <rect x="3" y="3" width="7" height="7" />
      <rect x="14" y="3" width="7" height="7" />
      <rect x="3" y="14" width="7" height="7" />
      <rect x="14" y="14" width="7" height="7" />
    </svg>
  );
}

export function IconTrend({ color, size = 18 }) {
  return (
    <svg width={size} height={size} viewBox="0 0 24 24" fill="none" stroke={color} strokeWidth="2" strokeLinecap="round" strokeLinejoin="round">
      <polyline points="22 7 13.5 15.5 8.5 10.5 2 17" />
      <polyline points="16 7 22 7 22 13" />
    </svg>
  );
}

export function IconCost({ color, size = 18 }) {
  return (
    <svg width={size} height={size} viewBox="0 0 24 24" fill="none" stroke={color} strokeWidth="2" strokeLinecap="round" strokeLinejoin="round">
      <line x1="12" y1="1" x2="12" y2="23" />
      <path d="M17 5H9.5a3.5 3.5 0 0 0 0 7h5a3.5 3.5 0 0 1 0 7H6" />
    </svg>
  );
}

export function IconSettings({ color, size = 18 }) {
  return (
    <svg width={size} height={size} viewBox="0 0 24 24" fill="none" stroke={color} strokeWidth="2" strokeLinecap="round" strokeLinejoin="round">
      <circle cx="12" cy="12" r="3" />
      <path d="M19.4 15a1.65 1.65 0 0 0 .33 1.82l.06.06a2 2 0 0 1-2.83 2.83l-.06-.06a1.65 1.65 0 0 0-1.82-.33 1.65 1.65 0 0 0-1 1.51V21a2 2 0 0 1-4 0v-.09a1.65 1.65 0 0 0-1-1.51 1.65 1.65 0 0 0-1.82.33l-.06.06a2 2 0 0 1-2.83-2.83l.06-.06a1.65 1.65 0 0 0 .33-1.82 1.65 1.65 0 0 0-1.51-1H3a2 2 0 0 1 0-4h.09a1.65 1.65 0 0 0 1.51-1 1.65 1.65 0 0 0-.33-1.82l-.06-.06a2 2 0 0 1 2.83-2.83l.06.06a1.65 1.65 0 0 0 1.82.33H9a1.65 1.65 0 0 0 1-1.51V3a2 2 0 0 1 4 0v.09a1.65 1.65 0 0 0 1 1.51 1.65 1.65 0 0 0 1.82-.33l.06-.06a2 2 0 0 1 2.83 2.83l-.06.06a1.65 1.65 0 0 0-.33 1.82V9a1.65 1.65 0 0 0 1.51 1H21a2 2 0 0 1 0 4h-.09a1.65 1.65 0 0 0-1.51 1z" />
    </svg>
  );
}

// `requires` gates the entry on a permission grant from MeContext. Items
// without it are visible to every authenticated user. Filtering happens
// in the consumer's render path, not here, so role changes show up on
// the next render.
export const NAV_ITEMS = [
  { label: 'Overview', path: '/',         Icon: IconOverview },
  { label: 'Trends',   path: '/trend',    Icon: IconTrend },
  { label: 'Costs',    path: '/cost',     Icon: IconCost },
  { label: 'Settings', path: '/settings', Icon: IconSettings },
];

// isNavActive — sibling-aware prefix match. Currently inert: no entry in
// NAV_ITEMS is a path-prefix of another, so the sibling check degenerates
// to a plain `startsWith`. Kept defensively because the bug it guards
// against (naive `startsWith` lighting up both /settings and a future
// /settings/<child> nav item at once) is silent and easy to reintroduce
// the next time a settings sub-route is promoted to the top nav.
export function isNavActive(path, pathname, items = NAV_ITEMS) {
  if (path === '/') return pathname === '/';
  if (!pathname.startsWith(path)) return false;
  // If a sibling has a longer prefix that also matches, that one wins.
  return !items.some(
    (i) => i.path !== path && i.path.startsWith(path) && pathname.startsWith(i.path),
  );
}
