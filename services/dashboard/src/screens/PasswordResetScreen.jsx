import { useEffect, useRef, useState } from 'react';
import { useNavigate, useSearchParams } from 'react-router-dom';
import { Spinner } from '../components/primitives';
import { authRedeemPasswordReset } from '../api/client';
import { authColors as C, useAuthStyles } from './_authShell';

// loginRedirectDelayMs is how long the success message stays visible
// before we redirect to /login. Long enough to register, short enough
// to feel snappy. Same envelope as a typical toast.
const loginRedirectDelayMs = 1500;

// PasswordResetScreen redeems an admin-issued reset URL:
// /password-reset?token=<plaintext>. After the password change, every
// live session for the user is revoked server-side (defence: a reset
// implies all existing cookies are potentially compromised). The
// dashboard therefore lands the user on /login, not /dashboard, so
// they pick the new password up via a fresh login.
export default function PasswordResetScreen() {
  const S = useAuthStyles();
  const navigate = useNavigate();
  const [params] = useSearchParams();
  const tokenFromUrl = params.get('token') || '';

  const [token, setToken] = useState(tokenFromUrl);
  const [password, setPassword] = useState('');
  const [confirm, setConfirm] = useState('');
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState('');
  const [done, setDone] = useState(false);
  const redirectTimerRef = useRef(null);

  useEffect(() => {
    if (!tokenFromUrl) {
      setError('This reset link is missing its token. Ask your administrator to re-issue it.');
    }
  }, [tokenFromUrl]);

  // Cancel the post-success redirect timer if the user navigates away before
  // it fires, so navigate() never runs against an unmounted screen.
  useEffect(() => () => clearTimeout(redirectTimerRef.current), []);

  async function onSubmit(e) {
    e.preventDefault();
    if (busy) return;
    setError('');

    if (password.length < 12) {
      setError('Password must be at least 12 characters.');
      return;
    }
    if (password !== confirm) {
      setError('Passwords do not match.');
      return;
    }

    setBusy(true);
    try {
      await authRedeemPasswordReset({ token: token.trim(), newPassword: password });
      setDone(true);
      // Brief pause so the user sees the success message before redirect.
      redirectTimerRef.current = setTimeout(() => navigate('/login', { replace: true }), loginRedirectDelayMs);
    } catch (e2) {
      if (e2.code === 'reset_invalid') {
        setError('This reset link is invalid, expired, or has already been used. Ask for a fresh one.');
      } else if (e2.code === 'weak_password') {
        setError(e2.message || 'Choose a stronger password.');
      } else {
        setError('Could not reset password — please retry, or ask for a fresh link.');
      }
      setBusy(false);
    }
  }

  return (
    <div style={S.container}>
      <form style={S.card} onSubmit={onSubmit} noValidate>
        <img src="/axiaops-logo-dark.svg" alt="AxiaOps" style={S.logoImg} />
        <span style={S.title}>Reset your password</span>
        <span style={S.tagline}>
          Choose a new password. All existing sessions will be signed out for
          security.
        </span>

        {error && <div style={S.errorBox}>{error}</div>}
        {done && (
          <div style={S.successBox}>
            Password updated. Redirecting you to sign in…
          </div>
        )}

        {!tokenFromUrl && !done && (
          <>
            <label style={S.label} htmlFor="rst-token">Reset token</label>
            <input
              id="rst-token"
              style={{ ...S.input, ...S.inputMono }}
              type="text"
              autoComplete="off"
              spellCheck="false"
              required
              value={token}
              onChange={(e) => setToken(e.target.value)}
              disabled={busy}
            />
          </>
        )}

        <label style={S.label} htmlFor="rst-password">New password (min 12 chars)</label>
        <input
          id="rst-password"
          style={S.input}
          type="password"
          autoComplete="new-password"
          required
          minLength={12}
          value={password}
          onChange={(e) => setPassword(e.target.value)}
          disabled={busy || done}
        />

        <label style={S.label} htmlFor="rst-confirm">Confirm new password</label>
        <input
          id="rst-confirm"
          style={S.input}
          type="password"
          autoComplete="new-password"
          required
          minLength={12}
          value={confirm}
          onChange={(e) => setConfirm(e.target.value)}
          disabled={busy || done}
        />

        <button
          type="submit"
          style={{ ...S.button, ...(busy || done || !token ? S.buttonDisabled : {}) }}
          disabled={busy || done || !token}
        >
          {busy ? <Spinner size={20} color={C.white} /> : 'Set new password'}
        </button>
      </form>
    </div>
  );
}
