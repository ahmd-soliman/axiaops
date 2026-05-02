import { useRef, useState } from 'react';
import { Spinner } from '../components/primitives';
import { discoverSSO } from '../api/client';
import { authColors as C, authStyles as S } from './_authShell';

// NativeLoginScreen renders the email + password form for AUTH_PROVIDER=native|both.
// onSubmit is called with {email, password}; the parent handles the API call,
// surfaces errors via the `error` prop, and toggles the spinner via `loading`.
//
// Email-blur SSO discovery (Phase B2 slice 5):
//   1. User types an email.
//   2. On blur (or after a 600ms idle debounce on input), GET /v1/sso/discover.
//   3. has_sso=true → redirect to redirect_url ("Redirecting to your IdP…").
//      A "Sign in with password instead" escape hatch is always available so
//      a misconfigured connection can't lock everyone out of the dashboard.
//   4. has_sso=false (or discover error) → reveal the password field.
//
// The password field stays hidden until step 4 so the form doesn't tempt
// users into typing their corporate password into AxiaOps when an SSO IdP
// is the right destination. Discover failures (transport, 5xx, etc.) fall
// back to revealing the password — broken discovery must never block login.
const DISCOVER_DEBOUNCE_MS = 600;

export default function NativeLoginScreen({ onSubmit, loading, error }) {
  const [email, setEmail] = useState('');
  const [password, setPassword] = useState('');
  const [phase, setPhase] = useState('email'); // email | discovering | sso | password
  const [ssoRedirectURL, setSSORedirectURL] = useState('');
  const debounceRef = useRef(null);
  // Monotonic counter to discard responses from superseded discover calls.
  // The debounced timer firing immediately followed by onBlur (Tab-key
  // navigation) can otherwise produce two concurrent in-flight requests
  // whose responses race; whichever resolves last would clobber the phase.
  // Bumped on every state change that should invalidate prior calls:
  // input edits, blur-triggered re-runs, and the runDiscover entry itself.
  const reqGenRef = useRef(0);

  function runDiscover(value) {
    const trimmed = value.trim();
    if (!trimmed || !trimmed.includes('@')) {
      // No discoverable input — make sure phase is back to 'email' so a
      // stale 'password' or 'sso' phase from an earlier successful lookup
      // doesn't survive an empty-email blur.
      reqGenRef.current += 1; // invalidate any in-flight response
      setPhase('email');
      return;
    }
    const myGen = ++reqGenRef.current;
    setPhase('discovering');
    discoverSSO(trimmed)
      .then((res) => {
        if (myGen !== reqGenRef.current) return; // superseded; the user kept typing
        if (res?.has_sso && res.redirect_url) {
          setSSORedirectURL(res.redirect_url);
          setPhase('sso');
          window.location.assign(res.redirect_url);
        } else {
          setPhase('password');
        }
      })
      .catch(() => {
        if (myGen !== reqGenRef.current) return;
        // Discover transport failure — reveal password so a flaky lookup
        // never blocks login.
        setPhase('password');
      });
  }

  function handleEmailChange(e) {
    const value = e.target.value;
    setEmail(value);
    // Any keystroke invalidates an in-flight discover so its late response
    // can't clobber a fresh edit.
    reqGenRef.current += 1;
    if (phase !== 'email') setPhase('email');
    if (debounceRef.current) clearTimeout(debounceRef.current);
    debounceRef.current = setTimeout(() => runDiscover(value), DISCOVER_DEBOUNCE_MS);
  }

  function handleEmailBlur() {
    if (debounceRef.current) {
      clearTimeout(debounceRef.current);
      debounceRef.current = null;
    }
    runDiscover(email);
  }

  function handleSubmit(e) {
    e.preventDefault();
    if (loading) return;
    if (phase !== 'password') return; // safety: button shouldn't be reachable in other phases
    onSubmit({ email: email.trim(), password });
  }

  return (
    <div style={S.container}>
      <form style={S.card} onSubmit={handleSubmit} noValidate>
        <span style={S.logo}>AxiaOps</span>
        <span style={S.tagline}>
          Sign in to find idle cloud resources.
        </span>

        {error && <div style={S.errorBox}>{error}</div>}

        <label style={S.label} htmlFor="email">Email</label>
        <input
          id="email"
          style={S.input}
          type="email"
          autoComplete="username"
          required
          value={email}
          onChange={handleEmailChange}
          onBlur={handleEmailBlur}
          disabled={loading || phase === 'sso'}
        />

        {phase === 'discovering' && (
          <div style={{ ...S.hint, display: 'flex', alignItems: 'center', gap: 8 }}>
            <Spinner size={14} color={C.textMuted} />
            <span>Checking SSO…</span>
          </div>
        )}

        {phase === 'sso' && (
          <>
            <div style={{ ...S.hint, color: C.white }}>
              Redirecting to your single sign-on provider…
            </div>
            <a
              href={ssoRedirectURL}
              style={{ ...S.button, ...(loading ? S.buttonDisabled : {}), textDecoration: 'none', textAlign: 'center' }}
            >
              Continue to SSO
            </a>
            <button
              type="button"
              onClick={() => setPhase('password')}
              style={{
                background: 'none',
                border: 'none',
                color: C.textMuted,
                fontSize: 12,
                cursor: 'pointer',
                padding: 0,
                marginTop: 4,
                textDecoration: 'underline',
              }}
            >
              Sign in with password instead
            </button>
          </>
        )}

        {phase === 'password' && (
          <>
            <label style={S.label} htmlFor="password">Password</label>
            <input
              id="password"
              style={S.input}
              type="password"
              autoComplete="current-password"
              required
              autoFocus
              value={password}
              onChange={(e) => setPassword(e.target.value)}
              disabled={loading}
            />

            <button
              type="submit"
              style={{ ...S.button, ...(loading ? S.buttonDisabled : {}) }}
              disabled={loading}
            >
              {loading ? <Spinner size={20} color={C.white} /> : 'Sign in'}
            </button>
          </>
        )}

        <span style={S.hint}>
          New here? Ask your administrator for an invitation link.
        </span>
      </form>
    </div>
  );
}
