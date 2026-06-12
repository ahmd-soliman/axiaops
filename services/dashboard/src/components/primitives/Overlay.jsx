import { useEffect, useRef } from 'react';

// Overlay — centred modal backdrop (z 1000) used by dialogs like
// DestructiveConfirm and AccountSelector. While visible it locks body
// scroll, closes on Escape, and moves focus into the dialog (restoring it
// on close), mirroring MobileSheet's behaviour so both surfaces are
// keyboard- and scroll-correct.
export function Overlay({ visible, onClose, children }) {
  const dialogRef = useRef(null);
  const previousActiveRef = useRef(null);

  // Hold onClose in a ref so the lifecycle effect depends on [visible] alone —
  // callers pass a fresh closure each render, which would otherwise tear down
  // and rebuild the scroll-lock + focus management on every parent re-render.
  const onCloseRef = useRef(onClose);
  onCloseRef.current = onClose;

  useEffect(() => {
    if (!visible) return undefined;

    previousActiveRef.current = document.activeElement;

    const previousBodyOverflow = document.body.style.overflow;
    document.body.style.overflow = 'hidden';

    const onKey = (e) => { if (e.key === 'Escape') onCloseRef.current?.(); };
    document.addEventListener('keydown', onKey);

    const first = dialogRef.current?.querySelector(
      'a, button, input, select, textarea, [tabindex]:not([tabindex="-1"])',
    );
    (first || dialogRef.current)?.focus?.();

    return () => {
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
        display: 'flex',
        alignItems: 'center',
        justifyContent: 'center',
        zIndex: 1000,
      }}
      onClick={onClose}
    >
      <div ref={dialogRef} tabIndex={-1} onClick={(e) => e.stopPropagation()}>
        {children}
      </div>
    </div>
  );
}
