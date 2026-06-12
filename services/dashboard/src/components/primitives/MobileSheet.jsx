import { useEffect, useRef } from 'react';
import { useTheme } from '../../theme/ThemeContext';

// MobileSheet — bottom-anchored panel that slides up over the page on
// xs/sm. Used for nav drawers and per-screen drill-down sheets that don't
// fit a desktop dropdown on a phone.
//
// Existing primitive `Overlay.jsx` centres its child and is used by modal
// dialogs (z 1000). This sheet sits between the navbar (z 100) and modals
// at z 200, so a sheet open on top of a screen still defers to a modal
// dialog if both are visible at once.
//
// Behaviour:
//   - visible toggles render
//   - backdrop click and Escape both call onClose
//   - focus jumps to the first focusable child on open and returns to
//     whatever was focused before opening on close
//   - on body: locks scroll while open, restores it on close
//
// The animation is a 180ms translate from below the viewport. CSS
// transitions don't fire on first mount, so we render the sheet at
// translate-100% on the very first frame and switch to translate-0 the
// next tick — that's what the requestAnimationFrame dance does.
//
// The slide-up keyframes are injected once at module load (see below) rather
// than rendered inside the sheet JSX, so opening the sheet doesn't re-insert
// the rule into the document on every open.
injectSheetKeyframes();

export function MobileSheet({ visible, onClose, ariaLabel, children }) {
  const { isDark } = useTheme();
  const sheetRef = useRef(null);
  const previousActiveRef = useRef(null);

  // Hold onClose in a ref so the lifecycle effect can depend on [visible]
  // alone. Callers commonly pass a fresh closure each render (e.g.
  // `onClose={() => setOpen(false)}`); if that identity were an effect dep,
  // any parent re-render while the sheet is open would tear down and rebuild
  // the scroll-lock + focus management mid-interaction.
  const onCloseRef = useRef(onClose);
  onCloseRef.current = onClose;

  useEffect(() => {
    if (!visible) return undefined;

    previousActiveRef.current = document.activeElement;

    // Lock body scroll. Stash and restore the inline overflow so we don't
    // override an unrelated style set by a parent.
    const previousBodyOverflow = document.body.style.overflow;
    document.body.style.overflow = 'hidden';

    const onKey = (e) => { if (e.key === 'Escape') onCloseRef.current?.(); };
    document.addEventListener('keydown', onKey);

    // Defer focus until the slide-up transition has started; the sheet
    // exists at translate-100% on the first frame, focusing then would
    // scroll-into-view to a position outside the viewport.
    const id = requestAnimationFrame(() => {
      const first = sheetRef.current?.querySelector(
        'a, button, [tabindex]:not([tabindex="-1"])',
      );
      first?.focus();
    });

    return () => {
      cancelAnimationFrame(id);
      document.removeEventListener('keydown', onKey);
      document.body.style.overflow = previousBodyOverflow;
      previousActiveRef.current?.focus?.();
    };
  }, [visible]);

  if (!visible) return null;

  return (
    <div
      style={{
        position: 'fixed',
        inset: 0,
        backgroundColor: '#00000080',
        zIndex: 200,
        display: 'flex',
        alignItems: 'flex-end',
        justifyContent: 'stretch',
      }}
      onClick={onClose}
      aria-hidden="true"
    >
      <div
        ref={sheetRef}
        role="dialog"
        aria-modal="true"
        aria-label={ariaLabel}
        onClick={(e) => e.stopPropagation()}
        style={{
          width: '100%',
          maxHeight: '85vh',
          overflowY: 'auto',
          backgroundColor: 'var(--color-bg-secondary)',
          color: 'var(--color-text)',
          borderTop: '1px solid var(--color-border)',
          borderTopLeftRadius: 14,
          borderTopRightRadius: 14,
          boxShadow: isDark
            ? '0 -12px 32px rgba(0,0,0,0.6)'
            : '0 -12px 32px rgba(0,0,0,0.18)',
          animation: 'axia-sheet-up 180ms ease-out',
        }}
      >
        {/* Drag indicator — purely decorative, signals the sheet shape */}
        <div
          aria-hidden="true"
          style={{
            width: 36,
            height: 4,
            borderRadius: 2,
            backgroundColor: 'var(--color-border)',
            margin: '8px auto 4px',
          }}
        />
        {children}
      </div>
    </div>
  );
}

// Inject the slide-up keyframes once, at module load. Idempotent — guarded by
// an id so hot-reload / repeated imports don't stack duplicate rules.
function injectSheetKeyframes() {
  if (typeof document === 'undefined') return;
  if (document.getElementById('axia-sheet-keyframes')) return;
  const style = document.createElement('style');
  style.id = 'axia-sheet-keyframes';
  style.textContent =
    '@keyframes axia-sheet-up { from { transform: translateY(100%); } to { transform: translateY(0); } }';
  document.head.appendChild(style);
}
