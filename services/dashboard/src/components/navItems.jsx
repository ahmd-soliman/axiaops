// Shared nav config — consumed by AppShell (desktop top-bar) and
// MobileNav (xs/sm hamburger drawer). The two surfaces render the same
// list with different chrome, so the source of truth lives here.

// `requires` gates the entry on a permission grant from MeContext. Items
// without it are visible to every authenticated user. Filtering happens
// in the consumer's render path, not here, so role changes show up on
// the next render.
export const NAV_ITEMS = [
  { label: 'Overview',         path: '/' },         // org summary
  { label: 'Zombie Resources', path: '/zombies' },  // the account workbench (zombie list + bulk actions)
  { label: 'Trends',           path: '/trend' },
  { label: 'Cloud Spend',      path: '/cost' },
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
