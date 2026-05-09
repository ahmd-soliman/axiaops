import { useWindowWidth } from './useWindowWidth';

// Named viewport tokens. Only used to make conditional layout legible — the
// dashboard has no media-query stylesheet, every breakpoint check goes
// through this hook. If you find yourself adding a sixth token, push back:
// the four below cover phone-portrait, phone-landscape / small tablet,
// tablet, and desktop, which is the granularity the screens actually need.
//
// Ranges (px):
//   xs:    0 –  479   single-column phone
//   sm:  480 –  767   large phone / phone-landscape
//   md:  768 – 1023   tablet portrait
//   lg: 1024+         desktop (current default for every screen)
//
// Touch-target minimum is 44px (Apple HIG); buttons inside the dashboard
// shell skew smaller (24–28px) and need to expand at xs/sm. Audit before
// committing any phase-2+ change.
const ORDER = ['xs', 'sm', 'md', 'lg'];

function tokenFor(width) {
  if (width < 480) return 'xs';
  if (width < 768) return 'sm';
  if (width < 1024) return 'md';
  return 'lg';
}

export function useBreakpoint() {
  const width = useWindowWidth();
  const breakpoint = tokenFor(width);
  const idx = ORDER.indexOf(breakpoint);
  return {
    width,
    breakpoint,
    isAtMost: (bp) => idx <= ORDER.indexOf(bp),
    isAtLeast: (bp) => idx >= ORDER.indexOf(bp),
  };
}
