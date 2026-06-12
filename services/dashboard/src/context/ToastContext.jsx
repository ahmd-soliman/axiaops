import { createContext, useContext, useState, useCallback, useRef, useEffect, useMemo } from 'react';

const ToastContext = createContext(null);

// Monotonic toast id — collision-free without depending on Date.now()
// resolution (two toasts enqueued in the same millisecond used to lean on
// Math.random() to disambiguate).
let nextToastId = 0;

const ICONS = {
  success: '✓',
  error: '✕',
  info: 'ℹ',
  warning: '⚠',
};

// Toasts use a fixed dark-saturated palette in both modes — they are
// notification flags, not surface colours. Pulling bg from 'var(--color-success)'/
// error/warning failed AA contrast in dark mode (those tokens are tuned
// as *colored text* on dark surfaces, so emerald-400 / red-400 / yellow-
// 400 leave white toast text at <3:1, sometimes <2:1).
//
// Each shade below clears AA (≥ 4.5:1) against white text; visually still
// reads as the same green / red / amber / blue notification register.
const TOAST_BG = {
  success: '#047857', // emerald-700, 5.56:1 on white
  error:   '#B91C1C', // red-700,     6.41:1 on white
  warning: '#92400E', // amber-800,   6.18:1 on white
  info:    '#1D4ED8', // blue-700,    7.01:1 on white
};

function ToastItem({ toast, onDismiss }) {
  const bg = TOAST_BG[toast.type] || TOAST_BG.info;
  const fg = '#FFFFFF';
  return (
    <div
      role="alert"
      aria-live="polite"
      onClick={() => onDismiss(toast.id)}
      style={{
        backgroundColor: bg,
        border: 'none',
        color: fg,
        padding: '11px 16px',
        borderRadius: 10,
        display: 'flex',
        alignItems: 'center',
        gap: 10,
        minWidth: 260,
        maxWidth: 400,
        boxShadow: '0 4px 16px rgba(0,0,0,0.2)',
        cursor: 'pointer',
        fontSize: 14,
        fontWeight: 500,
        lineHeight: '20px',
        animation: 'toastIn 0.2s ease',
      }}
    >
      <span style={{ fontSize: 16, flexShrink: 0 }}>{ICONS[toast.type] || ICONS.info}</span>
      <span style={{ flex: 1 }}>{toast.message}</span>
      <span style={{ opacity: 0.75, fontSize: 20, lineHeight: 1, marginLeft: 4 }}>×</span>
    </div>
  );
}

export function ToastProvider({ children }) {
  const [toasts, setToasts] = useState([]);
  // id → auto-dismiss timer handle. Tracked so dismiss() can cancel a pending
  // timer (else a manually-dismissed-then-recreated toast could be removed
  // early by its predecessor's orphaned timer) and so the provider can clear
  // every outstanding timer on unmount.
  const timersRef = useRef(new Map());

  const dismiss = useCallback((id) => {
    const timers = timersRef.current;
    const timerId = timers.get(id);
    if (timerId !== undefined) {
      clearTimeout(timerId);
      timers.delete(id);
    }
    setToasts(prev => prev.filter(t => t.id !== id));
  }, []);

  const toast = useCallback((message, type = 'success', duration = 4000) => {
    const id = ++nextToastId;
    setToasts(prev => [...prev, { id, message, type }]);
    const timerId = setTimeout(() => {
      timersRef.current.delete(id);
      setToasts(prev => prev.filter(t => t.id !== id));
    }, duration);
    timersRef.current.set(id, timerId);
    return id;
  }, []);

  // Cancel any pending auto-dismiss timers if the provider ever unmounts.
  useEffect(() => {
    const timers = timersRef.current;
    return () => {
      for (const timerId of timers.values()) clearTimeout(timerId);
      timers.clear();
    };
  }, []);

  const value = useMemo(() => ({ toast }), [toast]);

  return (
    <ToastContext.Provider value={value}>
      {children}
      {toasts.length > 0 && (
        <div
          aria-label="Notifications"
          style={{
            position: 'fixed',
            top: 16,
            right: 16,
            zIndex: 9999,
            display: 'flex',
            flexDirection: 'column',
            gap: 8,
            pointerEvents: 'none',
          }}
        >
          {toasts.map(t => (
            <div key={t.id} style={{ pointerEvents: 'auto' }}>
              <ToastItem toast={t} onDismiss={dismiss} />
            </div>
          ))}
        </div>
      )}
    </ToastContext.Provider>
  );
}

export function useToast() {
  const ctx = useContext(ToastContext);
  if (!ctx) throw new Error('useToast must be used within ToastProvider');
  return ctx;
}
