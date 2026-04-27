import { Spinner } from '../components/primitives';

// Mirror of darkTheme (src/theme/ThemeContext.jsx) — Login is pre-auth and
// renders before ThemeProvider, so it can't use useTheme().
const C = {
  bg: '#0B1220',
  navyMid: '#182031',
  accent: '#FB923C',        // orange-400, matches dark brand accent
  textMuted: '#8497B2',
  white: '#FFFFFF',
};

const styles = {
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
    maxWidth: 400,
    backgroundColor: C.navyMid,
    borderRadius: 16,
    padding: 40,
    display: 'flex',
    flexDirection: 'column',
    alignItems: 'center',
    gap: 16,
  },
  logo: {
    fontSize: 32,
    fontWeight: 800,
    color: C.accent,
    letterSpacing: -1,
    marginBottom: 4,
  },
  tagline: {
    fontSize: 16,
    color: C.white,
    textAlign: 'center',
    lineHeight: '24px',
    marginBottom: 8,
  },
  button: {
    backgroundColor: C.accent,
    borderRadius: 10,
    paddingTop: 14,
    paddingBottom: 14,
    paddingLeft: 48,
    paddingRight: 48,
    width: '100%',
    display: 'flex',
    alignItems: 'center',
    justifyContent: 'center',
    marginTop: 8,
    border: 'none',
    cursor: 'pointer',
  },
  buttonSecondary: {
    backgroundColor: 'transparent',
    border: `1px solid ${C.accent}`,
  },
  buttonSecondaryText: { color: C.accent, fontSize: 16, fontWeight: 600 },
  buttonDisabled: { opacity: 0.6 },
  buttonText: { color: C.white, fontSize: 16, fontWeight: 700 },
  hint: { fontSize: 12, color: C.textMuted, textAlign: 'center', marginTop: 4 },
};

export default function LoginScreen({ onLogin, onSignUp, loading }) {
  return (
    <div style={styles.container}>
      <div style={styles.card}>
        <span style={styles.logo}>AxiaOps</span>
        <span style={styles.tagline}>Find idle cloud resources.{'\n'}Stop paying for nothing.</span>

        <button
          style={{ ...styles.button, ...(loading ? styles.buttonDisabled : {}) }}
          onClick={onLogin}
          disabled={loading}
        >
          {loading ? <Spinner size={20} color={C.white} /> : <span style={styles.buttonText}>Sign in</span>}
        </button>

        {onSignUp && (
          <button
            style={{ ...styles.button, ...styles.buttonSecondary, ...(loading ? styles.buttonDisabled : {}) }}
            onClick={onSignUp}
            disabled={loading}
          >
            <span style={styles.buttonSecondaryText}>Sign up</span>
          </button>
        )}

        <span style={styles.hint}>You will be redirected to Kinde to authenticate.</span>
      </div>
    </div>
  );
}
