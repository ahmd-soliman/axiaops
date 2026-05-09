// _authShell.jsx — shared dark-themed card layout for the four pre-auth
// screens (NativeLogin, Bootstrap, AcceptInvite, PasswordReset). Lives
// outside the screens index export so the underscore prefix signals
// "internal helper, not a route".
//
// Pre-auth screens render before ThemeProvider mounts, so colours are
// inlined here (mirrors darkTheme in src/theme/ThemeContext.jsx). Same
// palette as the legacy LoginScreen so the strangler transition is
// visually invisible to users.

const C = {
  bg: '#0B1220',
  navyMid: '#182031',
  accent: '#FB923C',
  textMuted: '#8497B2',
  white: '#FFFFFF',
  inputBg: '#0F1828',
  inputBorder: '#243049',
  errorBg: '#3B1A1F',
  errorText: '#FCA5A5',
  successBg: '#0F2D24',
  successText: '#86EFAC',
};

export const authColors = C;

export const authStyles = {
  container: {
    flex: 1,
    minHeight: '100vh',
    backgroundColor: C.bg,
    display: 'flex',
    alignItems: 'center',
    justifyContent: 'center',
    padding: 24,
  },
  card: {
    width: '100%',
    maxWidth: 440,
    backgroundColor: C.navyMid,
    borderRadius: 16,
    padding: 40,
    display: 'flex',
    flexDirection: 'column',
    alignItems: 'stretch',
    gap: 14,
  },
  logo: {
    fontSize: 28,
    fontWeight: 800,
    color: C.accent,
    letterSpacing: -1,
    textAlign: 'center',
    marginBottom: 4,
  },
  // SVG lockup variant — pre-auth screens are always dark, so we ship the
  // dark-on-dark logo file. Centered via auto margins; height locks proportions.
  logoImg: {
    height: 88,
    width: 'auto',
    display: 'block',
    margin: '0 auto 12px',
  },
  title: {
    fontSize: 18,
    fontWeight: 700,
    color: C.white,
    textAlign: 'center',
  },
  tagline: {
    fontSize: 14,
    color: C.textMuted,
    textAlign: 'center',
    lineHeight: '20px',
    marginBottom: 8,
  },
  label: {
    fontSize: 12,
    color: C.textMuted,
    fontWeight: 600,
    letterSpacing: 0.4,
    textTransform: 'uppercase',
  },
  input: {
    backgroundColor: C.inputBg,
    border: `1px solid ${C.inputBorder}`,
    borderRadius: 8,
    color: C.white,
    fontSize: 14,
    padding: '10px 12px',
    outline: 'none',
    width: '100%',
    boxSizing: 'border-box',
    fontFamily: 'inherit',
  },
  inputMono: {
    fontFamily: '"Geist Mono Variable", ui-monospace, SFMono-Regular, Menlo, monospace',
    fontSize: 12,
  },
  button: {
    backgroundColor: C.accent,
    borderRadius: 10,
    padding: '12px 24px',
    width: '100%',
    border: 'none',
    cursor: 'pointer',
    color: C.white,
    fontSize: 16,
    fontWeight: 700,
    display: 'flex',
    alignItems: 'center',
    justifyContent: 'center',
    marginTop: 8,
  },
  buttonDisabled: { opacity: 0.6, cursor: 'not-allowed' },
  errorBox: {
    backgroundColor: C.errorBg,
    color: C.errorText,
    borderRadius: 8,
    padding: '10px 12px',
    fontSize: 13,
    lineHeight: '18px',
  },
  successBox: {
    backgroundColor: C.successBg,
    color: C.successText,
    borderRadius: 8,
    padding: '10px 12px',
    fontSize: 13,
    lineHeight: '18px',
  },
  hint: {
    fontSize: 12,
    color: C.textMuted,
    textAlign: 'center',
    marginTop: 4,
    lineHeight: '16px',
  },
};
