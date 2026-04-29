import { useState, useRef, useEffect, useId } from 'react';
import { useTheme } from '../../theme/ThemeContext';

// InfoTooltip — small "i" icon that toggles a popover with explanatory copy.
//
// Open behavior:
//   - Hover opens, mouse-leave closes (transient hint).
//   - Click pins it open until the user clicks outside or presses Escape.
//     Pinned state survives mouse-leave so users can read longer copy.
//
// Placement:
//   - 'right' (default) anchors the popover at the icon's left edge and
//     extends rightward — use when the icon sits in the LEFT half of the
//     viewport.
//   - 'left' anchors at the icon's right edge and extends leftward — use
//     when the icon sits in the RIGHT half of the viewport.
//   In both cases the popover is clamped to the viewport with maxWidth.
export function InfoTooltip({ label, body, width = 280, size = 14, placement = 'right' }) {
  const { theme } = useTheme();
  const [open, setOpen] = useState(false);
  const wrapperRef = useRef(null);
  const pinnedRef = useRef(false);
  const popoverId = useId();

  useEffect(() => {
    if (!open) return;

    const onPointerDown = (e) => {
      if (wrapperRef.current && !wrapperRef.current.contains(e.target)) {
        pinnedRef.current = false;
        setOpen(false);
      }
    };
    const onKey = (e) => {
      if (e.key === 'Escape') {
        pinnedRef.current = false;
        setOpen(false);
      }
    };

    document.addEventListener('pointerdown', onPointerDown);
    document.addEventListener('keydown', onKey);
    return () => {
      document.removeEventListener('pointerdown', onPointerDown);
      document.removeEventListener('keydown', onKey);
    };
  }, [open]);

  return (
    <span
      ref={wrapperRef}
      style={{ position: 'relative', display: 'inline-flex', alignItems: 'center' }}
      onMouseEnter={() => setOpen(true)}
      onMouseLeave={() => {
        if (!pinnedRef.current) setOpen(false);
      }}
    >
      <button
        type="button"
        aria-label={label}
        aria-expanded={open}
        aria-controls={popoverId}
        onClick={(e) => {
          e.stopPropagation();
          // Toggle pinned state: clicking pins-open, clicking again closes.
          pinnedRef.current = !pinnedRef.current;
          setOpen(pinnedRef.current);
        }}
        onKeyDown={(e) => {
          // Don't let Enter/Space bubble — parent containers (e.g. a
          // div role="button" wrapping a clickable card) would otherwise
          // also fire their primary action.
          if (e.key === 'Enter' || e.key === ' ') e.stopPropagation();
        }}
        style={{
          width: size,
          height: size,
          padding: 0,
          margin: 0,
          border: `1px solid ${theme.textMuted}`,
          borderRadius: '50%',
          background: 'transparent',
          color: theme.textMuted,
          fontSize: Math.round(size * 0.7),
          fontWeight: 700,
          fontFamily: 'serif',
          lineHeight: 1,
          cursor: 'help',
          display: 'inline-flex',
          alignItems: 'center',
          justifyContent: 'center',
        }}
      >
        i
      </button>

      {open && (
        <div
          id={popoverId}
          style={{
            position: 'absolute',
            top: `calc(100% + 6px)`,
            ...(placement === 'left' ? { right: 0 } : { left: 0 }),
            width,
            maxWidth: 'calc(100vw - 32px)',
            padding: 12,
            backgroundColor: theme.surfaceRaised || theme.surface,
            border: `1px solid ${theme.border}`,
            borderRadius: 8,
            boxShadow: '0 6px 18px rgba(0,0,0,0.18)',
            color: theme.text,
            fontSize: 12,
            lineHeight: 1.5,
            zIndex: 50,
            whiteSpace: 'normal',
            textAlign: 'left',
            cursor: 'auto',
          }}
        >
          {body}
        </div>
      )}
    </span>
  );
}

export default InfoTooltip;
