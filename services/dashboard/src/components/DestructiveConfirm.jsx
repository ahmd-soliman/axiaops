import { useState } from 'react';
import { useMutation } from '@tanstack/react-query';
import { useTheme } from '../theme/ThemeContext';
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

  const mutation = useMutation({
    mutationFn,
    onSuccess: () => {
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
    close: () => { setOpen(false); setConfirmText(''); setError(''); },
    confirmText,
    setConfirmText,
    matches: target !== '' && confirmText === target,
    error,
    isPending: mutation.isPending,
    confirm: () => { setError(''); mutation.mutate(); },
  };
}

export function DestructiveConfirmModal({ ctrl, title, warning, targetLabel, confirmLabel }) {
  const { theme: t, isDark } = useTheme();
  const targetMissing = ctrl.target === '';

  return (
    <Overlay visible={ctrl.open} onClose={ctrl.close}>
      <div
        role="dialog"
        aria-modal="true"
        aria-label={title}
        style={{
          backgroundColor: t.surface,
          border: `1px solid ${isDark ? 'rgba(239,68,68,0.5)' : '#fecaca'}`,
          borderRadius: 10,
          padding: 20,
          maxWidth: 480,
          width: '100%',
          boxShadow: '0 12px 32px rgba(0,0,0,0.3)',
        }}
      >
        <h3 style={{ margin: 0, marginBottom: 12, fontSize: 16, fontWeight: 700, color: t.text }}>{title}</h3>
        <p style={pText(t)}>{warning}</p>
        {targetMissing ? (
          <Banner color="#fbbf24" bg="rgba(251,191,36,0.15)">
            {targetLabel} is unavailable; cannot proceed safely. Reload and try again.
          </Banner>
        ) : (
          <>
            <p style={pText(t)}>
              Type the {targetLabel} <strong style={{ color: t.text }}>{ctrl.target}</strong> to confirm:
            </p>
            <input
              type="text"
              autoFocus
              value={ctrl.confirmText}
              onChange={(e) => ctrl.setConfirmText(e.target.value)}
              placeholder={ctrl.target}
              style={{
                padding: '8px 10px',
                border: `1px solid ${t.border}`,
                borderRadius: 6,
                fontSize: 13,
                backgroundColor: t.bg,
                color: t.text,
                width: '100%',
                boxSizing: 'border-box',
              }}
            />
          </>
        )}
        {ctrl.error && <Banner color="#fca5a5" bg="rgba(239,68,68,0.15)">{ctrl.error}</Banner>}
        <div style={{ display: 'flex', justifyContent: 'flex-end', gap: 8, marginTop: 16 }}>
          <button type="button" onClick={ctrl.close} style={ghostBtn(t)}>Cancel</button>
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

function pText(t) {
  return { margin: '0 0 10px 0', fontSize: 13, color: t.textMid, lineHeight: '20px' };
}

function ghostBtn(t) {
  return {
    padding: '7px 14px',
    border: `1px solid ${t.border}`,
    borderRadius: 6,
    backgroundColor: 'transparent',
    color: t.text,
    fontWeight: 600,
    fontSize: 13,
    cursor: 'pointer',
  };
}

function dangerBtn(disabled) {
  return {
    padding: '7px 14px',
    border: 'none',
    borderRadius: 6,
    backgroundColor: '#ef4444',
    color: '#fff',
    fontWeight: 600,
    fontSize: 13,
    cursor: disabled ? 'not-allowed' : 'pointer',
    opacity: disabled ? 0.55 : 1,
  };
}
