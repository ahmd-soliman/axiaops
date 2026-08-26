import { useEffect, useState } from 'react';
import { useNavigate } from 'react-router-dom';
import { Spinner } from '../components/primitives';
import { authBootstrap, authBootstrapState } from '../api/client';
import { authColors as C, useAuthStyles } from './_authShell';

// BootstrapScreen renders the first-run install form.
//
// Plan §4.4 / D5: the install token lives only in the form body, never
// in the URL — operators retrieve it from whichever channel their deploy
// enabled: the API server logs (BOOTSTRAP_PRINT_BANNER=true → stderr, the
// only channel on cloud/Fargate where there's no shell into the task) or
// the token file at BOOTSTRAP_TOKEN_FILE_PATH (on-prem/compose; empty path
// disables the file). The token field
// is `type="text"` (not "password") because operators need to verify
// they pasted the right value; a show/hide toggle is overkill for a
// one-time setup screen.
export default function BootstrapScreen() {
  const S = useAuthStyles();
  const navigate = useNavigate();
  const [token, setToken] = useState('');
  const [email, setEmail] = useState('');
  const [name, setName] = useState('');
  const [orgName, setOrgName] = useState('');
  const [password, setPassword] = useState('');
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState('');

  // Probe the install posture on mount. If the install is sealed (which
  // is the post-consume reality for ~every running deployment), bounce
  // the visitor to /login with a flash hint instead of letting them fill
  // out a form that will 409. Best-effort: any probe failure leaves the
  // form rendered as a fallback.
  useEffect(() => {
    let cancelled = false;
    authBootstrapState().then((available) => {
      if (cancelled || available) return;
      navigate('/login', {
        replace: true,
        state: { error: 'Bootstrap is already complete on this installation. Please sign in.' },
      });
    });
    return () => { cancelled = true; };
  }, [navigate]);

  async function onSubmit(e) {
    e.preventDefault();
    if (busy) return;
    setError('');

    // noValidate on the <form> below means the fields' own type="email" /
    // pattern / required / minLength attributes are never enforced by the
    // browser -- every field needs its own explicit check here instead.
    if (!/^[^\s@]+@[^\s@]+\.[^\s@]+$/.test(email.trim())) {
      setError('Enter a valid email address, like alice@example.com.');
      return;
    }
    if (password.length < 12) {
      setError('Password must be at least 12 characters.');
      return;
    }

    setBusy(true);
    try {
      await authBootstrap({
        token: token.trim(),
        email: email.trim(),
        name: name.trim(),
        password,
        organizationName: orgName.trim(),
      });
      // Cookie set; land in the dashboard.
      navigate('/', { replace: true });
    } catch (e2) {
      if (e2.code === 'bootstrap_already_done') {
        setError('Bootstrap is already complete on this installation. Please sign in instead.');
      } else if (e2.code === 'invalid_token') {
        setError('That install token is incorrect. Re-check the value from your API server logs, or the token file if your deployment writes one.');
      } else if (e2.code === 'weak_password') {
        setError(e2.message || 'Choose a stronger password.');
      } else if (e2.code === 'email_taken') {
        setError('That email is already registered.');
      } else if (e2.code === 'invalid_email') {
        setError(e2.message || 'Enter a valid email address, like alice@example.com.');
      } else if (e2.code === 'invalid_name' || e2.code === 'invalid_organization_name') {
        setError(e2.message || 'Check the name fields and try again.');
      } else {
        setError('Setup failed — please retry. If the issue persists check the API logs.');
      }
      setBusy(false);
    }
  }

  return (
    <div style={S.container}>
      <form style={S.card} onSubmit={onSubmit} noValidate>
        <img src="/axiaops-logo-dark.svg" alt="AxiaOps" style={S.logoImg} />
        <span style={S.title}>First-run setup</span>
        <span style={S.tagline}>
          Create the first owner account. Find the install token in your API
          server&apos;s logs, or in the token file at{' '}
          <code>{'/var/run/axiaops/initial_setup_token'}</code> if your deployment
          writes one (cloud/Fargate deploys log it only).
        </span>

        {error && <div style={S.errorBox}>{error}</div>}

        <label style={S.label} htmlFor="bs-token">Install token</label>
        <input
          id="bs-token"
          style={{ ...S.input, ...S.inputMono }}
          type="text"
          autoComplete="off"
          spellCheck="false"
          required
          value={token}
          onChange={(e) => setToken(e.target.value)}
          disabled={busy}
        />

        <label style={S.label} htmlFor="bs-org">Organisation name (optional)</label>
        <input
          id="bs-org"
          style={S.input}
          type="text"
          value={orgName}
          onChange={(e) => setOrgName(e.target.value)}
          disabled={busy}
          placeholder="AxiaOps"
        />

        <label style={S.label} htmlFor="bs-name">Your name</label>
        <input
          id="bs-name"
          style={S.input}
          type="text"
          autoComplete="name"
          required
          value={name}
          onChange={(e) => setName(e.target.value)}
          disabled={busy}
        />

        <label style={S.label} htmlFor="bs-email">Email</label>
        <input
          id="bs-email"
          style={S.input}
          type="email"
          autoComplete="email"
          required
          // type="email" alone accepts "alice@example" (no TLD) per HTML5.
          // The pattern requires a "." somewhere in the domain to reject
          // typos like "alice@test.com" → "alice@test". Backend enforces
          // the same rule via model.ValidateInvitableEmail.
          pattern="[^\s@]+@[^\s@]+\.[^\s@]+"
          title="Enter an email like alice@example.com"
          value={email}
          onChange={(e) => setEmail(e.target.value)}
          disabled={busy}
        />

        <label style={S.label} htmlFor="bs-password">Password (min 12 chars)</label>
        <input
          id="bs-password"
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
          style={{ ...S.button, ...(busy ? S.buttonDisabled : {}) }}
          disabled={busy}
        >
          {busy ? <Spinner size={20} color={C.white} /> : 'Create owner account'}
        </button>

        <span style={S.hint}>
          This screen is single-use. Once setup completes the install token is
          deleted and this URL returns 409 forever.
        </span>
      </form>
    </div>
  );
}
