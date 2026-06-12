import { useState } from 'react';
import { Navigate, useNavigate } from 'react-router-dom';
import { ApiError } from '../api/admin.js';
import { useAdminAuth } from '../auth/AdminAuth.jsx';
import { BrandLogo, ThemeToggle } from '../Brand.jsx';

export default function LoginScreen() {
  const { staff, login } = useAdminAuth();
  const navigate = useNavigate();
  const [email, setEmail] = useState('');
  const [password, setPassword] = useState('');
  const [error, setError] = useState('');
  const [submitting, setSubmitting] = useState(false);

  // Already authenticated (e.g. landed here after a refresh) → bounce inward.
  // Declarative redirect (not an imperative navigate() in render, which would
  // fire twice under StrictMode and warn about updating during render).
  if (staff) return <Navigate to="/tenants" replace />;

  async function onSubmit(e) {
    e.preventDefault();
    setError('');
    setSubmitting(true);
    try {
      await login(email, password);
      // Success navigates away and unmounts this screen — leave `submitting`
      // true rather than resetting state on a component that's going away.
      navigate('/tenants', { replace: true });
    } catch (err) {
      // The backend collapses every auth failure to one shape — don't try to
      // distinguish unknown-email from wrong-password (it deliberately can't).
      if (err instanceof ApiError && err.status === 429) {
        setError('Too many attempts — wait a minute and try again.');
      } else {
        setError('Invalid email or password.');
      }
      setSubmitting(false);
    }
  }

  return (
    <div className="login-wrap">
      <div className="card">
        <div className="login-head">
          <BrandLogo />
          <ThemeToggle />
        </div>
        <h1 style={{ marginTop: 16 }}>
          Admin <span className="muted">· staff sign-in</span>
        </h1>
        <p className="muted">This console is internal-only.</p>
        <form onSubmit={onSubmit}>
          <label htmlFor="email">Email</label>
          <input
            id="email"
            type="email"
            autoComplete="username"
            value={email}
            onChange={(e) => setEmail(e.target.value)}
            required
          />
          <label htmlFor="password">Password</label>
          <input
            id="password"
            type="password"
            autoComplete="current-password"
            value={password}
            onChange={(e) => setPassword(e.target.value)}
            required
          />
          {error && (
            <p className="error" role="alert">
              {error}
            </p>
          )}
          <div style={{ marginTop: 16 }}>
            <button type="submit" disabled={submitting}>
              {submitting ? 'Signing in…' : 'Sign in'}
            </button>
          </div>
        </form>
      </div>
    </div>
  );
}
