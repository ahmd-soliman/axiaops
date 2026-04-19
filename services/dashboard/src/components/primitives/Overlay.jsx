export function Overlay({ visible, onClose, children }) {
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
      <div onClick={(e) => e.stopPropagation()}>{children}</div>
    </div>
  );
}
