import { useEffect } from 'react';

// Inject keyframes once at module load — avoids re-creating <style> on every render.
if (typeof document !== 'undefined' && !document.getElementById('toast-keyframes')) {
  const style = document.createElement('style');
  style.id = 'toast-keyframes';
  style.textContent = `@keyframes toastSlideIn{from{transform:translateX(400px);opacity:0}to{transform:translateX(0);opacity:1}}`;
  document.head.appendChild(style);
}

export default function Toast({ message, type = 'info', onDismiss, duration = 3000 }) {
  useEffect(() => {
    if (!message) return;
    const timer = setTimeout(() => onDismiss?.(), duration);
    return () => clearTimeout(timer);
  }, [message, duration, onDismiss]);

  if (!message) return null;

  const colors = {
    success: { bg: '#10b981', text: '#fff' },
    error: { bg: '#ef4444', text: '#fff' },
    info: { bg: '#3b82f6', text: '#fff' },
    warning: { bg: '#f59e0b', text: '#fff' },
  };

  const color = colors[type] || colors.info;

  return (
    <div
      style={{
        position: 'fixed',
        top: 16,
        right: 16,
        backgroundColor: color.bg,
        color: color.text,
        padding: '12px 16px',
        borderRadius: 8,
        fontSize: 14,
        fontWeight: 500,
        zIndex: 9999,
        boxShadow: '0 4px 12px rgba(0,0,0,0.15)',
        animation: 'toastSlideIn 0.3s ease-out',
      }}
    >
      {message}
    </div>
  );
}
