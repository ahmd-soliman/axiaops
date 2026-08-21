import { useEffect, useState } from 'react';
import { Link, useParams } from 'react-router-dom';
import { adminApi, ApiError } from '../api/admin.js';
import { formatDate } from '../utils.js';

// TenantDetailScreen shows one org's summary: metadata + account count +
// last-scan aggregates. It deliberately surfaces NO zombie/cost detail rows —
// that is the break-glass surface (design §5), deferred.
export default function TenantDetailScreen() {
  const { id } = useParams();
  const [tenant, setTenant] = useState(null);
  const [error, setError] = useState('');

  useEffect(() => {
    let live = true;
    adminApi
      .getTenant(id)
      .then((data) => live && setTenant(data))
      .catch((err) => {
        if (!live) return;
        const notFound = err instanceof ApiError && err.status === 404;
        setError(notFound ? 'No such tenant.' : err.message || 'Failed to load tenant');
      });
    return () => {
      live = false;
    };
  }, [id]);

  if (error) return <p className="error">{error}</p>;
  if (!tenant) return <p className="muted">Loading…</p>;

  return (
    <>
      <p>
        <Link to="/tenants">← Tenants</Link>
      </p>
      <h1>{tenant.name || tenant.org_code}</h1>

      <div className="card">
        <dl className="kv">
          <dt>Organization ID</dt>
          <dd>{tenant.organization_id}</dd>
          <dt>Org code</dt>
          <dd>{tenant.org_code}</dd>
          <dt>Created</dt>
          <dd>{formatDate(tenant.created_at)}</dd>
          <dt>Onboarded</dt>
          <dd>{tenant.onboarded_at ? formatDate(tenant.onboarded_at) : 'not yet'}</dd>
          <dt>Connected accounts</dt>
          <dd>{tenant.account_count}</dd>
          <dt>Last scan</dt>
          <dd>{tenant.last_scan_at ? formatDate(tenant.last_scan_at) : 'never scanned'}</dd>
          <dt>Latest zombies</dt>
          <dd>{tenant.latest_total_zombies}</dd>
          <dt>Latest potential savings</dt>
          <dd>{tenant.latest_potential_savings}</dd>
        </dl>
      </div>

      <h2>Tenant data</h2>
      <div className="card">
        <p className="muted">
          Zombie/cost detail is not shown here. Reading a tenant&apos;s FinOps data requires an
          audited break-glass grant (deferred), which is a separate, higher-privilege action than
          viewing this summary.
        </p>
      </div>
    </>
  );
}
