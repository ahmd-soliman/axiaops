import { useState } from 'react';
import { Navigate, useNavigate } from 'react-router-dom';
import {
  authSelectOrg,
  clearPendingOrgPick,
  getPendingOrgPick,
} from '../api/client';
import { Spinner } from '../components/primitives';
import { authColors as C, authStyles as S } from './_authShell';

// OrgPickerScreen lands users coming off /v1/auth/login when their account
// belongs to multiple organisations (the server returned 200 with
// `needs_org_selection: true` and an `orgs` array). The user picks one,
// we POST /v1/auth/select-org with the original creds + chosen org_id,
// and the server mints a session bound to that org.
//
// The credentials handoff from Login → here goes through a JS module-level
// variable in api/client.js (setPendingOrgPick / getPendingOrgPick), NOT
// React Router state. Why: react-router v6 persists `state` to
// window.history.state, which IS rehydrated across hard refreshes within
// the tab session. A module-level let is wiped when the bundle re-inits
// on refresh, which is the property we actually want — passwords must
// not survive a tab reload. Tab close clears it either way.
//
// Refresh on /select-org → module re-init → getPendingOrgPick() returns
// null → guard fires → <Navigate to="/login" />. The user just signs in
// again, which is the documented recovery path.
export default function OrgPickerScreen() {
  const navigate = useNavigate();
  // Read the pending pick once on first render. getPendingOrgPick is
  // idempotent (it doesn't clear), so React StrictMode's double-mount
  // in dev returns the same value both times — no risk of "first render
  // saw the data, second render saw null".
  //
  // ASSUMPTION: /select-org is a top-level route in App.jsx — it
  // unmounts on every navigation. The lazy-init snapshot is therefore
  // refreshed every time the user re-enters the picker. If this route
  // is ever nested under a persistent layout (so the component stays
  // mounted across navigations), this snapshot becomes stale and the
  // initializer must be replaced with a useEffect that re-syncs from
  // the module variable on route change.
  const [pending] = useState(() => getPendingOrgPick());
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState('');

  // No pending pick → either a refresh wiped the module state, or the
  // user typed /select-org directly. Either way, send them to /login.
  // length < 2 (not === 0): a 1-element array reaching this screen is
  // upstream corruption — the server only emits the picker payload for
  // multi-membership users — so treat it the same as missing data.
  if (!pending || !pending.orgs || pending.orgs.length < 2) {
    return <Navigate to="/login" replace />;
  }
  const { email, password, orgs } = pending;

  async function handlePick(orgID) {
    if (busy) return;
    setBusy(true);
    setError('');
    try {
      await authSelectOrg(email, password, orgID);
      // Clear the module state before navigating away — the picker is
      // done with this credential bundle and the next mount of
      // /select-org should bounce to /login.
      clearPendingOrgPick();
      navigate('/', { replace: true });
    } catch (e) {
      if (e.status === 429) {
        setError('Too many sign-in attempts. Please wait a moment and try again.');
      } else if (e.status === 401) {
        // The server collapses wrong-password / org-not-in-set / unknown-
        // user all into 401 invalid_credentials. We just authenticated
        // the same email+password 500ms ago, so this is most likely a
        // race (limiter cascade, password changed elsewhere, session
        // raced). "Sign in again" is the recovery, not "session expired"
        // — we never had a session at this point.
        clearPendingOrgPick();
        navigate('/login', {
          replace: true,
          state: { error: 'Sign-in failed — please sign in again.' },
        });
        return;
      } else {
        setError('Could not switch organisation. Please try again.');
      }
      setBusy(false);
    }
  }

  function handleCancel() {
    if (busy) return;
    clearPendingOrgPick();
    navigate('/login', { replace: true });
  }

  return (
    <div style={S.container}>
      <div style={{ ...S.card, gap: 12 }}>
        <img src="/axiaops-logo-dark.svg" alt="AxiaOps" style={S.logoImg} />
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
          onClick={handleCancel}
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
