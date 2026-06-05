import { useEffect, useState } from 'react';
import { Link } from 'react-router-dom';
import { adminApi } from '../api/admin.js';
import { formatDate } from '../utils.js';

// TenantsScreen lists every org. Metadata only — NOT tenant FinOps data
// (design §7.5: this is not a break-glass read).
export default function TenantsScreen() {
  const [tenants, setTenants] = useState(null);
  const [error, setError] = useState('');

  useEffect(() => {
    let live = true;
    adminApi
      .listTenants()
      .then((data) => live && setTenants(data?.tenants || []))
      .catch((err) => live && setError(err.message || 'Failed to load tenants'));
    return () => {
      live = false;
    };
  }, []);

  if (error) return <p className="error">{error}</p>;
  if (!tenants) return <p className="muted">Loading tenants…</p>;

  return (
    <>
      <h1>Tenants</h1>
      {tenants.length === 0 ? (
        <p className="muted">No organizations yet.</p>
      ) : (
        <div className="card" style={{ padding: 0 }}>
          <table>
            <thead>
              <tr>
                <th>Name</th>
                <th>Org code</th>
                <th>Created</th>
                <th>Onboarded</th>
              </tr>
            </thead>
            <tbody>
              {tenants.map((t) => (
                <tr key={t.organization_id}>
                  <td>
                    <Link to={`/tenants/${encodeURIComponent(t.organization_id)}`}>
                      {t.name || <span className="muted">(unnamed)</span>}
                    </Link>
                  </td>
                  <td className="muted">{t.org_code}</td>
                  <td className="muted">{formatDate(t.created_at)}</td>
                  <td className="muted">{t.onboarded_at ? formatDate(t.onboarded_at) : '—'}</td>
                </tr>
              ))}
            </tbody>
          </table>
        </div>
      )}
    </>
  );
}
