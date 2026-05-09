import { useTheme } from '../../theme/ThemeContext';

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
  onClick,
  selected = false,
  style,
}) {
  const { theme: t, isDark } = useTheme();
  const interactive = typeof onClick === 'function';

  const hoverBg = isDark ? 'rgba(255,255,255,0.04)' : 'rgba(0,0,0,0.03)';
  const selectedBorder = selected ? t.accent : t.border;

  return (
    <div
      role={interactive ? 'button' : undefined}
      tabIndex={interactive ? 0 : undefined}
      onClick={onClick}
      onKeyDown={interactive ? (e) => {
        if (e.key === 'Enter' || e.key === ' ') {
          e.preventDefault();
          onClick(e);
        }
      } : undefined}
      onMouseEnter={interactive ? (e) => { e.currentTarget.style.backgroundColor = hoverBg; } : undefined}
      onMouseLeave={interactive ? (e) => { e.currentTarget.style.backgroundColor = 'transparent'; } : undefined}
      style={{
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
      {header && (
        <div style={{ display: 'flex', alignItems: 'center', justifyContent: 'space-between', gap: 8, minWidth: 0 }}>
          {header}
        </div>
      )}
      {body && (
        <div style={{ display: 'flex', flexDirection: 'column', gap: 4, fontSize: 12, color: t.textMid, minWidth: 0 }}>
          {body}
        </div>
      )}
      {actions && (
        <div style={{ display: 'flex', alignItems: 'center', gap: 8, marginTop: 4 }}>
          {actions}
        </div>
      )}
    </div>
  );
}
