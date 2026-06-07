import { useTheme } from '../../theme/ThemeContext';
import { StretchedRowLink } from './RouterLink';

// CardRow — vertical card layout for what would otherwise be a table row
// on desktop. Phase-6+ work converts <table> elements (Team, CloudAccounts,
// AuditScreen) into a stack of these on xs/sm.
//
// Three slots: header (top line — usually a primary identifier + a chip),
// body (mid section — secondary fields, can be a Stack of label+value
// pairs), actions (bottom row — buttons, full-width on phones). The
// primitive is dumb on purpose: callers compose whatever they want into
// each slot. The point is to standardise spacing, padding, and the
// 1-pixel hairline border so screens don't drift apart visually.
export function CardRow({
  header,
  body,
  actions,
  to,
  linkLabel,
  onClick,
  selected = false,
  style,
}) {
  const { isDark } = useTheme();
  // `to` makes the whole card a real link (issue #130: middle/Ctrl-click opens
  // the drill-down in a new tab) via a stretched anchor overlay, so the card's
  // action buttons (which can't legally nest inside an <a>) stay siblings,
  // raised above the overlay. `onClick` is the legacy imperative path for
  // cards that toggle in-page state rather than navigate.
  const interactive = !!to || typeof onClick === 'function';

  const hoverBg = isDark ? 'rgba(255,255,255,0.04)' : 'rgba(0,0,0,0.03)';
  const selectedBorder = selected ? 'var(--color-accent)' : 'var(--color-border)';

  return (
    <div
      role={!to && onClick ? 'button' : undefined}
      tabIndex={!to && onClick ? 0 : undefined}
      onClick={!to ? onClick : undefined}
      onKeyDown={!to && onClick ? (e) => {
        if (e.key === 'Enter' || e.key === ' ') {
          e.preventDefault();
          onClick(e);
        }
      } : undefined}
      onMouseEnter={interactive ? (e) => { e.currentTarget.style.backgroundColor = hoverBg; } : undefined}
      onMouseLeave={interactive ? (e) => { e.currentTarget.style.backgroundColor = 'transparent'; } : undefined}
      style={{
        position: to ? 'relative' : undefined,
        display: 'flex',
        flexDirection: 'column',
        gap: 8,
        padding: '14px 16px',
        backgroundColor: 'transparent',
        border: `1px solid ${selectedBorder}`,
        borderRadius: 10,
        cursor: interactive ? 'pointer' : 'default',
        transition: 'background-color 120ms ease, border-color 120ms ease',
        ...style,
      }}
    >
      {to && <StretchedRowLink to={to} label={linkLabel} />}
      {header && (
        <div style={{ display: 'flex', alignItems: 'center', justifyContent: 'space-between', gap: 8, minWidth: 0 }}>
          {header}
        </div>
      )}
      {body && (
        <div style={{ display: 'flex', flexDirection: 'column', gap: 4, fontSize: 12, color: 'var(--color-text-mid)', minWidth: 0 }}>
          {body}
        </div>
      )}
      {actions && (
        // Raised above the stretched link so the buttons stay clickable.
        <div style={{ position: to ? 'relative' : undefined, zIndex: to ? 1 : undefined, display: 'flex', alignItems: 'center', gap: 8, marginTop: 4 }}>
          {actions}
        </div>
      )}
    </div>
  );
}
