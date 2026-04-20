// Default color matches the indigo brand accent (ThemeContext.jsx).
// Callers that need theme-aware spinners should pass `color={theme.accent}`.
export function Spinner({ size = 24, color = '#4F46E5' }) {
  return (
    <div
      style={{
        width: size,
        height: size,
        border: `2px solid ${color}33`,
        borderTopColor: color,
        borderRadius: '50%',
        animation: 'spin 0.7s linear infinite',
      }}
    />
  );
}
