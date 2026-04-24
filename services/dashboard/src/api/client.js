// API client.
//
// VITE_API_URL is set at build time:
//   - Docker:   /api  (nginx proxies to the api container — no CORS issues)
//   - Local dev: not set → falls back to http://localhost:8080 (direct)
const BASE_URL = import.meta.env.VITE_API_URL || 'http://localhost:8080';

let authToken = null;

export function setAuthToken(token) {
  authToken = token;
}

function authHeaders() {
  return authToken ? { Authorization: `Bearer ${authToken}` } : {};
}

export async function fetchSummary(accountId) {
  const url = accountId
    ? `${BASE_URL}/v1/summary?account_id=${encodeURIComponent(accountId)}`
    : `${BASE_URL}/v1/summary`;
  const res = await fetch(url, { headers: authHeaders() });
  if (!res.ok) throw new Error('Failed to fetch summary');
  return res.json();
}

export async function fetchZombies(accountId) {
  const url = accountId
    ? `${BASE_URL}/v1/zombies?account_id=${encodeURIComponent(accountId)}`
    : `${BASE_URL}/v1/zombies`;
  const res = await fetch(url, { headers: authHeaders() });
  if (!res.ok) throw new Error('Failed to fetch zombies');
  return res.json();
}

export async function fetchResources(accountId) {
  const url = accountId
    ? `${BASE_URL}/v1/resources?account_id=${encodeURIComponent(accountId)}`
    : `${BASE_URL}/v1/resources`;
  const res = await fetch(url, { headers: authHeaders() });
  if (!res.ok) throw new Error('Failed to fetch resources');
  return res.json();
}

export async function fetchAccounts() {
  const res = await fetch(`${BASE_URL}/v1/accounts`, { headers: authHeaders() });
  if (!res.ok) throw new Error('Failed to fetch accounts');
  return res.json();
}

export async function connectAccount({ provider, label, accessKeyId, secretKey, region }) {
  const res = await fetch(`${BASE_URL}/v1/accounts`, {
    method: 'POST',
    headers: { ...authHeaders(), 'Content-Type': 'application/json' },
    body: JSON.stringify({ provider, label, access_key_id: accessKeyId, secret_key: secretKey, region }),
  });
  if (!res.ok) throw new Error('Failed to connect account');
  return res.json();
}

export async function updateAccount(id, { label, accessKeyId, secretKey, region, scan_interval_hours }) {
  const body = {};
  if (label !== undefined) body.label = label;
  if (accessKeyId !== undefined) body.access_key_id = accessKeyId;
  if (secretKey !== undefined && secretKey !== '') body.secret_key = secretKey;
  if (region !== undefined) body.region = region;
  if (scan_interval_hours !== undefined) body.scan_interval_hours = scan_interval_hours;

  const res = await fetch(`${BASE_URL}/v1/accounts/${id}`, {
    method: 'PATCH',
    headers: { ...authHeaders(), 'Content-Type': 'application/json' },
    body: JSON.stringify(body),
  });
  if (!res.ok) throw new Error('Failed to update account');
  return res.json();
}

export async function deleteAccount(id) {
  const res = await fetch(`${BASE_URL}/v1/accounts/${id}`, {
    method: 'DELETE',
    headers: authHeaders(),
  });
  if (!res.ok) throw new Error('Failed to delete account');
}

export async function scanAccount(id) {
  const res = await fetch(`${BASE_URL}/v1/accounts/${id}/scan`, {
    method: 'POST',
    headers: authHeaders(),
  });
  if (res.status === 409) {
    const err = new Error('Scan already in progress');
    err.code = 'already_scanning';
    throw err;
  }
  if (!res.ok) throw new Error('Failed to trigger scan');
  return res.json();
}

export async function fetchTrend(accountId, service, resourceType) {
  const params = new URLSearchParams();
  if (accountId) params.set('account_id', accountId);
  if (service) params.set('service', service);
  if (resourceType) params.set('resource_type', resourceType);
  const qs = params.toString();
  const url = qs ? `${BASE_URL}/v1/trend?${qs}` : `${BASE_URL}/v1/trend`;
  const res = await fetch(url, { headers: authHeaders() });
  if (!res.ok) throw new Error('Failed to fetch trend');
  return res.json();
}

export async function fetchTrendServices() {
  const res = await fetch(`${BASE_URL}/v1/trend/services`, { headers: authHeaders() });
  if (!res.ok) throw new Error('Failed to fetch trend services');
  return res.json();
}

export async function fetchTrendResourceTypes(service) {
  const res = await fetch(`${BASE_URL}/v1/trend/resource-types?service=${encodeURIComponent(service)}`, { headers: authHeaders() });
  if (!res.ok) throw new Error('Failed to fetch trend resource types');
  return res.json();
}

export async function fetchDismissals(accountId) {
  const url = accountId
    ? `${BASE_URL}/v1/dismissals?account_id=${encodeURIComponent(accountId)}`
    : `${BASE_URL}/v1/dismissals`;
  const res = await fetch(url, { headers: authHeaders() });
  if (!res.ok) throw new Error('Failed to fetch dismissals');
  return res.json();
}

export async function dismissZombie({ accountId, provider, service, region, resourceId, action, reason, note, snoozeUntil }) {
  const body = {
    account_id: accountId,
    provider,
    service,
    region,
    resource_id: resourceId,
    action,
    reason,
    note: note || '',
  };
  if (snoozeUntil) body.snooze_until = snoozeUntil;

  const res = await fetch(`${BASE_URL}/v1/dismissals`, {
    method: 'POST',
    headers: { ...authHeaders(), 'Content-Type': 'application/json' },
    body: JSON.stringify(body),
  });
  if (res.status === 409) throw new Error('already_dismissed');
  if (!res.ok) throw new Error('Failed to dismiss zombie');
  return res.json();
}

export async function revokeDismissal(dismissalId) {
  const res = await fetch(`${BASE_URL}/v1/dismissals/${dismissalId}`, {
    method: 'DELETE',
    headers: authHeaders(),
  });
  if (!res.ok) throw new Error('Failed to revoke dismissal');
}

export async function fetchZombiesWithDismissed(accountId) {
  const params = new URLSearchParams({ include_dismissed: 'true' });
  if (accountId) params.set('account_id', accountId);
  const res = await fetch(`${BASE_URL}/v1/zombies?${params}`, { headers: authHeaders() });
  if (!res.ok) throw new Error('Failed to fetch zombies');
  return res.json();
}

// Fetch a page of audit events. All fields are optional; unset values are
// omitted from the query string so the server applies its defaults.
// Returns `{ events: [...], next_cursor: '' | '<opaque>' }`.
//
// Example:
//   fetchAuditEvents({ action: 'dismiss_zombie', limit: 50 })
//   fetchAuditEvents({ cursor: prev.next_cursor })
export async function fetchAuditEvents({
  userId, resourceType, resourceId, action, since, until, limit, cursor,
} = {}) {
  const params = new URLSearchParams();
  if (userId)       params.set('user_id', userId);
  if (resourceType) params.set('resource_type', resourceType);
  if (resourceId)   params.set('resource_id', resourceId);
  if (action)       params.set('action', action);
  if (since)        params.set('since', since);       // RFC3339 string
  if (until)        params.set('until', until);       // RFC3339 string
  if (limit)        params.set('limit', String(limit));
  if (cursor)       params.set('cursor', cursor);

  const qs = params.toString();
  const url = qs ? `${BASE_URL}/v1/audit?${qs}` : `${BASE_URL}/v1/audit`;
  const res = await fetch(url, { headers: authHeaders() });
  if (!res.ok) throw new Error('Failed to fetch audit events');
  return res.json();
}

export async function fetchCosts(accountId, service, days = 30) {
  const params = new URLSearchParams();
  if (accountId) params.set('account_id', accountId);
  if (service) params.set('service', service);
  params.set('days', String(days));
  const qs = params.toString();
  const url = qs ? `${BASE_URL}/v1/costs?${qs}` : `${BASE_URL}/v1/costs`;
  const res = await fetch(url, { headers: authHeaders() });
  if (!res.ok) throw new Error('Failed to fetch costs');
  return res.json();
}
