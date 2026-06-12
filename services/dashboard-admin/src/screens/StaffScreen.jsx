import { useCallback, useEffect, useState } from 'react';
import { adminApi, ApiError, STAFF_ROLES } from '../api/admin.js';
import { hasRole, useAdminAuth } from '../auth/AdminAuth.jsx';

// StaffScreen — superadmin-only staff management. The backend enforces the
// superadmin gate on every mutation regardless; this screen also guards the
// view so a non-superadmin who hits the URL gets a clear message, not raw 403s.
export default function StaffScreen() {
  const { staff: me } = useAdminAuth();
  const [members, setMembers] = useState(null);
  const [error, setError] = useState('');

  const load = useCallback(async () => {
    setError(''); // clear any prior banner so a successful retry doesn't keep it
    try {
      const data = await adminApi.listStaff();
      setMembers(data?.staff || []);
    } catch (err) {
      setError(err.message || 'Failed to load staff');
    }
  }, []);

  // Key on the resolved boolean, not the `me` object reference, so a routine
  // auth-context refresh that returns an equivalent staff principal doesn't
  // re-fire the staff list load.
  const isSuperadmin = hasRole(me, 'superadmin');
  useEffect(() => {
    if (isSuperadmin) load();
  }, [isSuperadmin, load]);

  if (!isSuperadmin) {
    return (
      <>
        <h1>Staff</h1>
        <p className="muted">Only superadmins can manage staff.</p>
      </>
    );
  }

  return (
    <>
      <h1>Staff</h1>
      {error && <p className="error">{error}</p>}

      <CreateStaffForm onCreated={load} />

      <h2>Existing staff</h2>
      {!members ? (
        <p className="muted">Loading…</p>
      ) : (
        <div className="card" style={{ padding: 0 }}>
          <table>
            <thead>
              <tr>
                <th>Email</th>
                <th>Name</th>
                <th>Status</th>
                <th>Roles</th>
              </tr>
            </thead>
            <tbody>
              {members.map((s) => (
                <StaffRow key={s.staff_user_id} staff={s} onChange={load} />
              ))}
            </tbody>
          </table>
        </div>
      )}
    </>
  );
}

function CreateStaffForm({ onCreated }) {
  const [email, setEmail] = useState('');
  const [name, setName] = useState('');
  const [password, setPassword] = useState('');
  const [roles, setRoles] = useState(['support']);
  const [error, setError] = useState('');
  const [busy, setBusy] = useState(false);

  function toggleRole(role) {
    setRoles((cur) => (cur.includes(role) ? cur.filter((r) => r !== role) : [...cur, role]));
  }

  async function onSubmit(e) {
    e.preventDefault();
    setError('');
    if (roles.length === 0) {
      setError('Pick at least one role.');
      return;
    }
    setBusy(true);
    try {
      await adminApi.createStaff({ email, name, password, roles });
      setEmail('');
      setName('');
      setPassword('');
      setRoles(['support']);
      await onCreated(); // keep the form disabled until the list reload settles
    } catch (err) {
      setError(messageFor(err));
    } finally {
      setBusy(false);
    }
  }

  return (
    <div className="card">
      <h2 style={{ marginTop: 0 }}>Add staff</h2>
      <form onSubmit={onSubmit}>
        <label htmlFor="s-email">Email</label>
        <input id="s-email" type="email" value={email} onChange={(e) => setEmail(e.target.value)} required />
        <label htmlFor="s-name">Name</label>
        <input id="s-name" value={name} onChange={(e) => setName(e.target.value)} />
        <label htmlFor="s-pass">Password</label>
        <input
          id="s-pass"
          type="password"
          value={password}
          onChange={(e) => setPassword(e.target.value)}
          required
          minLength={12}
        />
        <label>Roles</label>
        <div className="row-actions" style={{ flexWrap: 'wrap' }}>
          {STAFF_ROLES.map((role) => (
            <label key={role} style={{ display: 'flex', gap: 6, margin: 0, width: 'auto' }}>
              <input
                type="checkbox"
                style={{ width: 'auto' }}
                checked={roles.includes(role)}
                onChange={() => toggleRole(role)}
              />
              {role}
            </label>
          ))}
        </div>
        {error && <p className="error">{error}</p>}
        <div style={{ marginTop: 14 }}>
          <button type="submit" disabled={busy}>
            {busy ? 'Creating…' : 'Create staff user'}
          </button>
        </div>
      </form>
    </div>
  );
}

function StaffRow({ staff, onChange }) {
  const [error, setError] = useState('');
  const [busy, setBusy] = useState(false);
  const unheld = STAFF_ROLES.filter((r) => !staff.roles.includes(r));

  async function grant(role) {
    if (!role) return;
    setBusy(true);
    setError('');
    try {
      await adminApi.grantRole(staff.staff_user_id, role);
      // Await the list reload so the row's controls stay disabled until the
      // refetch settles — otherwise a second click could act on stale roles.
      await onChange();
    } catch (err) {
      setError(messageFor(err));
    } finally {
      setBusy(false);
    }
  }

  async function revoke(role) {
    setBusy(true);
    setError('');
    try {
      await adminApi.revokeRole(staff.staff_user_id, role);
      await onChange();
    } catch (err) {
      setError(messageFor(err));
    } finally {
      setBusy(false);
    }
  }

  return (
    <tr>
      <td>{staff.email}</td>
      <td>{staff.name || <span className="muted">—</span>}</td>
      <td>{staff.status}</td>
      <td>
        <div className="row-actions" style={{ flexWrap: 'wrap' }}>
          {staff.roles.map((role) => (
            <span key={role} className={`badge ${role === 'superadmin' ? 'superadmin' : ''}`}>
              {role}{' '}
              <button
                type="button"
                className="badge-x"
                aria-label={`revoke ${role}`}
                disabled={busy}
                onClick={() => revoke(role)}
              >
                ×
              </button>
            </span>
          ))}
          {unheld.length > 0 && (
            <select
              aria-label={`grant role to ${staff.email}`}
              value=""
              disabled={busy}
              onChange={(e) => grant(e.target.value)}
              style={{ width: 'auto' }}
            >
              <option value="">+ role…</option>
              {unheld.map((r) => (
                <option key={r} value={r}>
                  {r}
                </option>
              ))}
            </select>
          )}
        </div>
        {error && <p className="error" style={{ margin: '6px 0 0' }}>{error}</p>}
      </td>
    </tr>
  );
}

// messageFor turns the backend's stable error codes into friendly copy.
function messageFor(err) {
  if (!(err instanceof ApiError)) return err.message || 'Something went wrong';
  switch (err.code) {
    case 'staff_email_taken':
      return 'A staff user with that email already exists.';
    case 'weak_password':
      return 'Password must be at least 12 characters.';
    case 'last_superadmin':
      return 'Cannot revoke the last superadmin.';
    case 'invalid_role':
      return 'Unknown role.';
    default:
      return err.message || err.code || 'Request failed';
  }
}
