import { useEffect, useState } from 'react';
import { useNavigate, useSearchParams } from 'react-router-dom';
import { Spinner } from '../components/primitives';
import { authRedeemInvitation } from '../api/client';
import { authColors as C, authStyles as S } from './_authShell';

// AcceptInviteScreen handles the OOB redemption URL admins share with
// invitees: /accept-invite?token=<plaintext>. The token comes from the
// query string (the admin posted it into Slack/email/wherever); the
// invitee fills in their name + chosen password.
//
// On success the server creates the membership AND mints a session
// cookie, so we can land the user straight in the dashboard.
export default function AcceptInviteScreen() {
  const navigate = useNavigate();
  const [params] = useSearchParams();
  const tokenFromUrl = params.get('token') || '';

  const [token, setToken] = useState(tokenFromUrl);
  const [name, setName] = useState('');
  const [password, setPassword] = useState('');
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState('');

  // If the URL is missing the token entirely, surface that immediately
  // — don't make the user fill out the form before learning the link
  // is broken.
  useEffect(() => {
    if (!tokenFromUrl) {
      setError('This invite link is missing its token. Ask the person who invited you to re-send the URL.');
    }
  }, [tokenFromUrl]);

  async function onSubmit(e) {
    e.preventDefault();
    if (busy) return;
    setError('');

    if (password.length < 12) {
      setError('Password must be at least 12 characters.');
      return;
    }

    setBusy(true);
    try {
      await authRedeemInvitation({
        token: token.trim(),
        name: name.trim(),
        password,
      });
      navigate('/', { replace: true });
    } catch (e2) {
      if (e2.code === 'invitation_invalid') {
        setError('This invitation link is invalid, expired, or has already been used. Ask for a fresh invite.');
      } else if (e2.code === 'weak_password') {
        setError(e2.message || 'Choose a stronger password.');
      } else if (e2.code === 'email_taken') {
        setError('An account with this email already exists. Try signing in instead.');
      } else {
        setError('Could not redeem this invitation — please retry, or ask for a fresh link.');
      }
      setBusy(false);
    }
  }

  return (
    <div style={S.container}>
      <form style={S.card} onSubmit={onSubmit} noValidate>
        <span style={S.logo}>AxiaOps</span>
        <span style={S.title}>Accept your invitation</span>
        <span style={S.tagline}>
          Choose a password to finish setting up your account.
        </span>

        {error && <div style={S.errorBox}>{error}</div>}

        {!tokenFromUrl && (
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
              disabled={busy}
            />
          </>
        )}

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

        <button
          type="submit"
          style={{ ...S.button, ...(busy || !token ? S.buttonDisabled : {}) }}
          disabled={busy || !token}
        >
          {busy ? <Spinner size={20} color={C.white} /> : 'Accept invitation'}
        </button>
      </form>
    </div>
  );
}
