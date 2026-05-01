import { useState } from 'react';
import { useNavigate } from 'react-router-dom';
import { Spinner } from '../components/primitives';
import { authBootstrap } from '../api/client';
import { authColors as C, authStyles as S } from './_authShell';

// BootstrapScreen renders the first-run install form.
//
// Plan §4.4 / D5: the install token lives only in the form body, never
// in the URL — operators paste it from `cat /var/run/axiaops/initial_setup_token`
// (or from the BOOTSTRAP_PRINT_BANNER stdout banner). The token field
// is `type="text"` (not "password") because operators need to verify
// they pasted the right value; a show/hide toggle is overkill for a
// one-time setup screen.
export default function BootstrapScreen() {
  const navigate = useNavigate();
  const [token, setToken] = useState('');
  const [email, setEmail] = useState('');
  const [name, setName] = useState('');
  const [orgName, setOrgName] = useState('');
  const [password, setPassword] = useState('');
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState('');

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
        setError('That install token is incorrect. Re-check the value from the token file or banner.');
      } else if (e2.code === 'weak_password') {
        setError(e2.message || 'Choose a stronger password.');
      } else if (e2.code === 'email_taken') {
        setError('That email is already registered.');
      } else {
        setError('Setup failed — please retry. If the issue persists check the API logs.');
      }
      setBusy(false);
    }
  }

  return (
    <div style={S.container}>
      <form style={S.card} onSubmit={onSubmit} noValidate>
        <span style={S.logo}>AxiaOps</span>
        <span style={S.title}>First-run setup</span>
        <span style={S.tagline}>
          Create the first owner account. The install token was printed to the API
          server&apos;s log and written to <code>{'/var/run/axiaops/initial_setup_token'}</code>.
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
