// Stack — flex container with a direction that flips at a breakpoint.
//
// The dashboard is committed to inline styles, so the row→column flip every
// screen needs has been copy-pasted in many places. Stack collapses that
// pattern into one component.
//
// API: pass `direction` as a string ('row' | 'column') or as an object that
// maps breakpoint tokens to a direction:
//
//   <Stack direction="row" gap={12}>...</Stack>
//   <Stack direction={{ xs: 'column', md: 'row' }} gap={12}>...</Stack>
//
// The breakpoint object is "smallest-wins, then promote": xs is the
// fallback, sm overrides it for sm+, md overrides for md+, lg overrides for
// lg. Mobile-first by construction — write the small layout first, override
// for wider viewports.
import { useBreakpoint } from './useBreakpoint';

const ORDER = ['xs', 'sm', 'md', 'lg'];

function resolveDirection(direction, breakpoint) {
  if (typeof direction === 'string') return direction;
  if (!direction) return 'row';
  // Walk down from the active breakpoint to xs, picking the first defined
  // override. Means a `{ xs: 'column', md: 'row' }` map gives column for
  // xs/sm and row for md/lg, which is the common case.
  for (let i = ORDER.indexOf(breakpoint); i >= 0; i -= 1) {
    if (direction[ORDER[i]]) return direction[ORDER[i]];
  }
  return 'row';
}

export function Stack({
  direction = 'row',
  gap = 8,
  wrap = false,
  align,
  justify,
  children,
  style,
  as: As = 'div',
  ...rest
}) {
  const { breakpoint } = useBreakpoint();
  const flexDirection = resolveDirection(direction, breakpoint);
  return (
    <As
      style={{
        display: 'flex',
        flexDirection,
        gap,
        flexWrap: wrap ? 'wrap' : 'nowrap',
        alignItems: align,
        justifyContent: justify,
        ...style,
      }}
      {...rest}
    >
      {children}
    </As>
  );
}
