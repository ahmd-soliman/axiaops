import { useState } from 'react';
import { Navigate, useLocation, useNavigate } from 'react-router-dom';
import { authSelectOrg } from '../api/client';
import { Spinner } from '../components/primitives';
import { authColors as C, authStyles as S } from './_authShell';

// OrgPickerScreen lands users coming off /v1/auth/login when their account
// belongs to multiple organisations (the server returned 200 with
// `needs_org_selection: true` and an `orgs` array). The user picks one,
// we POST /v1/auth/select-org with the original creds + chosen org_id,
// and the server mints a session bound to that org.
//
// Why creds-via-route-state and not localStorage:
//   - The picker step needs to re-POST email+password (defence in depth —
//     server re-validates from scratch).
//   - Persisting the password anywhere durable is a security regression.
//   - In-memory React Router state is exactly the right scope: lives until
//     the user navigates away or refreshes, then is gone.
//
// Refresh on /select-org with no state → bounce to /login. The user just
// has to sign in again. Documented expected behaviour.
export default function OrgPickerScreen() {
  const navigate = useNavigate();
  const location = useLocation();
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState('');

  const { email, password, orgs } = location.state || {};

  // Refresh / direct-link guard: no creds in state means we can't actually
  // select. Send them back to /login. `replace` so the back button doesn't
  // bounce them right back here.
  if (!email || !password || !orgs || orgs.length === 0) {
    return <Navigate to="/login" replace />;
  }

  async function handlePick(orgID) {
    if (busy) return;
    setBusy(true);
    setError('');
    try {
      await authSelectOrg(email, password, orgID);
      navigate('/', { replace: true });
    } catch (e) {
      if (e.status === 429) {
        setError('Too many sign-in attempts. Please wait a moment and try again.');
      } else if (e.status === 401) {
        // 401 here is unexpected after a successful /login; most likely
        // the limiter fired or something raced. Send the user back to
        // /login rather than retrying — the password they typed has
        // probably been invalidated by something they did elsewhere.
        navigate('/login', {
          replace: true,
          state: { error: 'Sign-in expired. Please try again.' },
        });
        return;
      } else {
        setError('Could not switch organisation. Please try again.');
      }
      setBusy(false);
    }
  }

  return (
    <div style={S.container}>
      <div style={{ ...S.card, gap: 12 }}>
        <span style={S.logo}>AxiaOps</span>
        <span style={S.title}>Choose an organisation</span>
        <span style={S.tagline}>
          You're signed in as <strong style={{ color: C.white }}>{email}</strong> and
          belong to {orgs.length} organisations. Pick the one you'd like to use.
        </span>

        {error && <div style={S.errorBox}>{error}</div>}

        <div style={{ display: 'flex', flexDirection: 'column', gap: 8, marginTop: 4 }}>
          {orgs.map((o) => (
            <button
              key={o.id}
              type="button"
              onClick={() => handlePick(o.id)}
              disabled={busy}
              style={{
                ...orgRowStyle,
                ...(busy ? { opacity: 0.6, cursor: 'not-allowed' } : {}),
              }}
            >
              <span style={{ flex: 1, textAlign: 'left' }}>{o.name || o.id}</span>
              {busy ? <Spinner size={14} color={C.textMuted} /> : <ArrowRight />}
            </button>
          ))}
        </div>

        <button
          type="button"
          onClick={() => navigate('/login', { replace: true })}
          disabled={busy}
          style={cancelStyle}
        >
          Cancel
        </button>

        <span style={S.hint}>
          Your password is re-validated when you pick — never trusted from
          the previous step.
        </span>
      </div>
    </div>
  );
}

function ArrowRight() {
  return (
    <span aria-hidden style={{ color: C.textMuted, fontSize: 18, fontWeight: 700 }}>
      →
    </span>
  );
}

const orgRowStyle = {
  backgroundColor: C.inputBg,
  border: `1px solid ${C.inputBorder}`,
  borderRadius: 10,
  padding: '14px 16px',
  color: C.white,
  fontSize: 14,
  fontWeight: 500,
  cursor: 'pointer',
  display: 'flex',
  alignItems: 'center',
  gap: 12,
  width: '100%',
  textAlign: 'left',
  fontFamily: 'inherit',
};

const cancelStyle = {
  marginTop: 4,
  background: 'transparent',
  border: 'none',
  color: C.textMuted,
  fontSize: 13,
  cursor: 'pointer',
  textAlign: 'center',
  padding: '6px 0',
};
