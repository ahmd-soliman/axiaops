import { createContext, useContext, useState, useCallback } from 'react';
import { useTheme } from '../theme/ThemeContext';

const ToastContext = createContext(null);

const ICONS = {
  success: '✓',
  error: '✕',
  info: 'ℹ',
  warning: '⚠',
};

// Light mode: saturated bg + white text (loud, classic notification look),
// no border — same-as-bg outline was decorative and read as a 1px lip.
// Dark mode: surfaceRaised bg + colored border + colored text so the toast
// reads against the dark UI ladder instead of slapping a saturated patch
// over it; the border carries the semantic colour. theme.success/error/
// warning shift hues per mode (darker for AA-on-white in light, lighter
// for legibility on dark surfaces) so each branch picks the right shade.
function toastPalette(type, theme, isDark) {
  const color = ({
    success: theme.success,
    error:   theme.error,
    warning: theme.warning,
    // No semantic blue token in the palette — keep tailwind blue-500 here
    // until we add one. Tracked alongside the other audit-log info chip.
    info:    '#3B82F6',
  })[type] || '#3B82F6';
  if (isDark) {
    return { bg: theme.surfaceRaised, border: color, fg: color };
  }
  return { bg: color, border: null, fg: theme.textOnDark };
}

function ToastItem({ toast, onDismiss }) {
  const { theme, isDark } = useTheme();
  const c = toastPalette(toast.type, theme, isDark);
  return (
    <div
      role="alert"
      aria-live="polite"
      onClick={() => onDismiss(toast.id)}
      style={{
        backgroundColor: c.bg,
        border: c.border ? `1px solid ${c.border}` : 'none',
        color: c.fg,
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

  const toast = useCallback((message, type = 'success', duration = 4000) => {
    const id = Date.now() + Math.random();
    setToasts(prev => [...prev, { id, message, type }]);
    setTimeout(() => setToasts(prev => prev.filter(t => t.id !== id)), duration);
  }, []);

  const dismiss = useCallback((id) => {
    setToasts(prev => prev.filter(t => t.id !== id));
  }, []);

  return (
    <ToastContext.Provider value={{ toast }}>
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
