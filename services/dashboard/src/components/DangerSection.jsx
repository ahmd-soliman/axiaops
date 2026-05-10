// Destructive-action card: neutral surface, red title + button as the only
// chromatic cues. Pattern: GitLab/Linear/Vercel settings pages.
export function DangerSection({ t, title, blurb, buttonLabel, onClick, disabled, disabledHint, children }) {
  return (
    <section
      style={{
        border: `1px solid ${t.border}`,
        borderRadius: 8,
        padding: 16,
        marginBottom: 16,
        backgroundColor: t.surface,
      }}
    >
      <h2 style={{ margin: 0, marginBottom: 6, fontSize: 14, fontWeight: 700, color: t.error }}>{title}</h2>
      <p style={{ marginTop: 0, marginBottom: 12, fontSize: 12, color: t.textMid, lineHeight: '18px' }}>{blurb}</p>
      <button
        type="button"
        onClick={onClick}
        disabled={disabled}
        title={disabled ? disabledHint : undefined}
        style={{
          padding: '7px 14px',
          border: 'none',
          borderRadius: 6,
          backgroundColor: t.error,
          color: t.textOnDark,
          fontWeight: 600,
          fontSize: 13,
          cursor: disabled ? 'not-allowed' : 'pointer',
          opacity: disabled ? 0.55 : 1,
        }}
      >
        {buttonLabel}
      </button>
      {disabled && disabledHint && (
        <p style={{ marginTop: 8, marginBottom: 0, fontSize: 12, color: t.textMuted }}>{disabledHint}</p>
      )}
      {children}
    </section>
  );
}
