import { useEffect, useState } from 'react';
import { useNavigate, useSearchParams } from 'react-router-dom';
import { Spinner } from '../components/primitives';
import { authPreviewInvitation, authRedeemInvitation } from '../api/client';
import { authColors as C, authStyles as S } from './_authShell';

// AcceptInviteScreen handles the OOB redemption URL admins share with
// invitees: /accept-invite?token=<plaintext>. The token comes from the
// query string; the screen first POSTs /v1/auth/invitations/preview to
// learn whether the email is already a known user (B1.5 cross-org
// invitation) or fresh. Two distinct UIs:
//
//   - Fresh user → "Set a password" form, with name + password inputs.
//   - Existing user → "Welcome back" form, with a password input only
//     (their existing password is verified server-side; we never touch
//     their display name from this screen).
//
// On success the server creates / extends the membership AND mints a
// session cookie, so we land the user straight in the dashboard.
export default function AcceptInviteScreen() {
  const navigate = useNavigate();
  const [params] = useSearchParams();
  const tokenFromUrl = params.get('token') || '';

  const [token, setToken] = useState(tokenFromUrl);
  const [preview, setPreview] = useState(null); // { email, organization_name, role, existing_user, existing_user_name? }
  const [previewState, setPreviewState] = useState('idle'); // 'idle' | 'loading' | 'ready' | 'error'
  const [name, setName] = useState('');
  const [password, setPassword] = useState('');
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState('');

  // Auto-preview the token from the URL on mount. We only run this when
  // tokenFromUrl is present — if the user is typing a token by hand,
  // they trigger the preview manually via the inline button below.
  useEffect(() => {
    if (!tokenFromUrl) {
      setError('This invite link is missing its token. Ask the person who invited you to re-send the URL.');
      return;
    }
    runPreview(tokenFromUrl);
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [tokenFromUrl]);

  async function runPreview(t) {
    setPreviewState('loading');
    setError('');
    try {
      const p = await authPreviewInvitation(t.trim());
      setPreview(p);
      setPreviewState('ready');
    } catch (e) {
      if (e.status === 410 || e.code === 'invitation_invalid') {
        setError('This invitation link is invalid, expired, or has already been used. Ask for a fresh invite.');
      } else {
        setError('Could not load the invitation. Please try again.');
      }
      setPreviewState('error');
    }
  }

  async function onSubmit(e) {
    e.preventDefault();
    if (busy || previewState !== 'ready') return;
    setError('');

    // Existing-user flow: name is ignored server-side. New-user flow:
    // the policy gate is enforced server-side too (CheckPolicy), but we
    // pre-check here for snappier feedback.
    if (!preview.existing_user && password.length < 12) {
      setError('Password must be at least 12 characters.');
      return;
    }
    if (!preview.existing_user && !name.trim()) {
      setError('Please enter your name.');
      return;
    }

    setBusy(true);
    try {
      await authRedeemInvitation({
        token: token.trim(),
        // Name is sent only for the new-user flow; the existing-user
        // flow doesn't need it but the server ignores it either way.
        name: preview.existing_user ? '' : name.trim(),
        password,
      });
      navigate('/', { replace: true });
    } catch (e2) {
      if (e2.code === 'invitation_invalid') {
        setError('This invitation link is invalid, expired, or has already been used. Ask for a fresh invite.');
      } else if (e2.code === 'invalid_credentials') {
        // Existing-user flow only — wrong password against their
        // existing account.
        setError("That password doesn't match your existing account. Try again, or use the password-reset flow if you've forgotten it.");
      } else if (e2.code === 'weak_password') {
        setError(e2.message || 'Choose a stronger password.');
      } else if (e2.code === 'email_taken') {
        // Race: another path created a user with this email between
        // preview and redeem. Tell the user to refresh — the next
        // preview will route them through the existing-user flow.
        setError('Another account with this email was just created. Please refresh the page and try again.');
      } else {
        setError('Could not redeem this invitation — please retry, or ask for a fresh link.');
      }
      setBusy(false);
    }
  }

  // The token-from-URL path waits for the preview to land before showing
  // any inputs. The hand-typed token path shows an input + "Continue"
  // button to trigger the preview, then morphs into the appropriate form.
  const showInputs = previewState === 'ready';
  const isExisting = preview?.existing_user === true;

  return (
    <div style={S.container}>
      <form style={S.card} onSubmit={onSubmit} noValidate>
        <span style={S.logo}>AxiaOps</span>
        <span style={S.title}>
          {isExisting
            ? `Welcome back${preview?.existing_user_name ? `, ${preview.existing_user_name}` : ''}`
            : 'Accept your invitation'}
        </span>
        <span style={S.tagline}>
          {isExisting
            ? `You've been invited to join ${preview.organization_name || 'a new organisation'}. Enter your existing password to confirm and join.`
            : preview
              ? `You've been invited to join ${preview.organization_name || 'an organisation'}. Choose a password to finish setting up your account.`
              : 'Loading invitation…'}
        </span>

        {error && <div style={S.errorBox}>{error}</div>}

        {/* Hand-typed token fallback: only when the URL had no token. */}
        {!tokenFromUrl && previewState !== 'ready' && (
          <>
            <label style={S.label} htmlFor="inv-token">Invitation token</label>
            <input
              id="inv-token"
              style={{ ...S.input, ...S.inputMono }}
              type="text"
              autoComplete="off"
              spellCheck="false"
              required
              value={token}
              onChange={(e) => setToken(e.target.value)}
              disabled={previewState === 'loading'}
            />
            <button
              type="button"
              onClick={() => runPreview(token)}
              disabled={!token || previewState === 'loading'}
              style={{ ...S.button, ...(!token || previewState === 'loading' ? S.buttonDisabled : {}) }}
            >
              {previewState === 'loading' ? <Spinner size={20} color={C.white} /> : 'Continue'}
            </button>
          </>
        )}

        {showInputs && !isExisting && (
          <>
            <label style={S.label} htmlFor="inv-name">Your name</label>
            <input
              id="inv-name"
              style={S.input}
              type="text"
              autoComplete="name"
              required
              value={name}
              onChange={(e) => setName(e.target.value)}
              disabled={busy}
            />

            <label style={S.label} htmlFor="inv-password">Choose a password (min 12 chars)</label>
            <input
              id="inv-password"
              style={S.input}
              type="password"
              autoComplete="new-password"
              required
              minLength={12}
              value={password}
              onChange={(e) => setPassword(e.target.value)}
              disabled={busy}
            />
          </>
        )}

        {showInputs && isExisting && (
          <>
            {preview?.email && (
              <div style={{ fontSize: 13, color: C.textMuted, textAlign: 'center' }}>
                Signing in as <strong style={{ color: C.white }}>{preview.email}</strong>
              </div>
            )}
            <label style={S.label} htmlFor="inv-password">Your existing password</label>
            <input
              id="inv-password"
              style={S.input}
              type="password"
              autoComplete="current-password"
              required
              value={password}
              onChange={(e) => setPassword(e.target.value)}
              disabled={busy}
            />
          </>
        )}

        {showInputs && (
          <button
            type="submit"
            style={{ ...S.button, ...(busy ? S.buttonDisabled : {}) }}
            disabled={busy}
          >
            {busy ? <Spinner size={20} color={C.white} /> : (isExisting ? 'Join organisation' : 'Accept invitation')}
          </button>
        )}
      </form>
    </div>
  );
}
