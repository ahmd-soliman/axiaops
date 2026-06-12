import { useState } from 'react';
import { useMutation } from '@tanstack/react-query';
import { Overlay } from './primitives';

// Type-to-confirm hook + modal, shared by every page that calls a
// destructive endpoint. The hook owns the open/typed/error state and the
// react-query mutation; the modal owns the markup. Splitting them lets a
// caller place the openModal trigger in a non-trivial layout (a danger-zone
// section card, a kebab menu, a row action) without re-implementing the
// state machine.

export function useDestructiveConfirm({ target, mutationFn, successMessage, onSuccess, toast, on409 }) {
  const [open, setOpen] = useState(false);
  const [confirmText, setConfirmText] = useState('');
  const [error, setError] = useState('');

  const reset = () => { setOpen(false); setConfirmText(''); setError(''); };

  const mutation = useMutation({
    mutationFn,
    onSuccess: () => {
      // Auto-close on success so callers don't have to call close() from
      // their own onSuccess (which forced a fragile self-reference to the
      // very ctrl const being assigned).
      reset();
      toast(successMessage, 'success');
      onSuccess?.();
    },
    onError: (err) => {
      if (err.status === 409 && on409) {
        setError(on409(err));
        return;
      }
      setError(err.body || err.message || 'Action failed.');
    },
  });

  return {
    target,
    open,
    openModal: () => setOpen(true),
    close: reset,
    confirmText,
    setConfirmText,
    matches: target !== '' && confirmText === target,
    error,
    isPending: mutation.isPending,
    confirm: () => { setError(''); mutation.mutate(); },
  };
}

export function DestructiveConfirmModal({ ctrl, title, warning, targetLabel, confirmLabel }) {
  const targetMissing = ctrl.target === '';

  return (
    <Overlay visible={ctrl.open} onClose={ctrl.close}>
      <div
        role="dialog"
        aria-modal="true"
        aria-label={title}
        style={{
          backgroundColor: 'var(--color-surface)',
          borderRadius: 10,
          padding: 20,
          maxWidth: 480,
          width: '100%',
          boxShadow: '0 12px 32px rgba(0,0,0,0.3)',
        }}
      >
        <h3 style={{ margin: 0, marginBottom: 12, fontSize: 16, fontWeight: 700, color: 'var(--color-error)' }}>{title}</h3>
        <p style={pText}>{warning}</p>
        {targetMissing ? (
          <Banner color="#fbbf24" bg="rgba(251,191,36,0.15)">
            {targetLabel} is unavailable; cannot proceed safely. Reload and try again.
          </Banner>
        ) : (
          <>
            <p style={pText}>
              Type the {targetLabel} <strong style={{ color: 'var(--color-text)' }}>{ctrl.target}</strong> to confirm:
            </p>
            <input
              type="text"
              autoFocus
              value={ctrl.confirmText}
              onChange={(e) => ctrl.setConfirmText(e.target.value)}
              placeholder={ctrl.target}
              style={{
                padding: '8px 10px',
                border: '1px solid var(--color-border)',
                borderRadius: 6,
                fontSize: 13,
                backgroundColor: 'var(--color-bg)',
                color: 'var(--color-text)',
                width: '100%',
                boxSizing: 'border-box',
              }}
            />
          </>
        )}
        {ctrl.error && <Banner color={'var(--color-error)'} bg="rgba(239,68,68,0.15)">{ctrl.error}</Banner>}
        <div style={{ display: 'flex', justifyContent: 'flex-end', gap: 8, marginTop: 16 }}>
          <button type="button" onClick={ctrl.close} style={ghostBtn}>Cancel</button>
          <button
            type="button"
            onClick={ctrl.confirm}
            disabled={!ctrl.matches || ctrl.isPending}
            style={dangerBtn(!ctrl.matches || ctrl.isPending)}
          >
            {ctrl.isPending ? 'Working…' : confirmLabel}
          </button>
        </div>
      </div>
    </Overlay>
  );
}

function Banner({ children, color, bg }) {
  return (
    <div style={{ padding: '8px 12px', marginTop: 12, borderRadius: 6, color, backgroundColor: bg, fontSize: 12 }}>
      {children}
    </div>
  );
}

const pText = { margin: '0 0 10px 0', fontSize: 13, color: 'var(--color-text-mid)', lineHeight: '20px' };

const ghostBtn = {
  padding: '7px 14px',
  border: '1px solid var(--color-border)',
  borderRadius: 6,
  backgroundColor: 'transparent',
  color: 'var(--color-text)',
  fontWeight: 600,
  fontSize: 13,
  cursor: 'pointer',
};

function dangerBtn(disabled) {
  return {
    padding: '7px 14px',
    border: 'none',
    borderRadius: 6,
    backgroundColor: 'var(--color-error)',
    color: 'var(--color-text-on-dark)',
    fontWeight: 600,
    fontSize: 13,
    cursor: disabled ? 'not-allowed' : 'pointer',
    opacity: disabled ? 0.55 : 1,
  };
}
