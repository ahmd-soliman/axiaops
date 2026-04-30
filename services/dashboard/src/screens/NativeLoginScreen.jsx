import { useState } from 'react';
import { Spinner } from '../components/primitives';
import { authColors as C, authStyles as S } from './_authShell';

// NativeLoginScreen renders the email + password form for AUTH_PROVIDER=native|both.
// onSubmit is called with {email, password}; the parent handles the API call,
// surfaces errors via the `error` prop, and toggles the spinner via `loading`.
export default function NativeLoginScreen({ onSubmit, loading, error }) {
  const [email, setEmail] = useState('');
  const [password, setPassword] = useState('');

  function handleSubmit(e) {
    e.preventDefault();
    if (loading) return;
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
          onChange={(e) => setEmail(e.target.value)}
          disabled={loading}
        />

        <label style={S.label} htmlFor="password">Password</label>
        <input
          id="password"
          style={S.input}
          type="password"
          autoComplete="current-password"
          required
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

        <span style={S.hint}>
          New here? Ask your administrator for an invitation link.
        </span>
      </form>
    </div>
  );
}
