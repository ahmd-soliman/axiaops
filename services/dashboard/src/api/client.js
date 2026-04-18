// API client.
//
// EXPO_PUBLIC_API_URL is set at build time:
//   - Docker:   /api  (nginx proxies to the api container — no CORS issues)
//   - Local dev: not set → falls back to http://localhost:8080 (direct)
const BASE_URL = process.env.EXPO_PUBLIC_API_URL || 'http://localhost:8080';

let authToken = null;

// setAuthToken is called by App.js after login / on startup token restore.
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

export async function fetchGhosts(accountId) {
  const url = accountId
    ? `${BASE_URL}/v1/ghosts?account_id=${encodeURIComponent(accountId)}`
    : `${BASE_URL}/v1/ghosts`;
  const res = await fetch(url, { headers: authHeaders() });
  if (!res.ok) throw new Error('Failed to fetch ghosts');
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
  if (!res.ok) throw new Error('Failed to trigger scan');
  return res.json();
}

export async function fetchTrend(accountId) {
  const url = accountId
    ? `${BASE_URL}/v1/trend?account_id=${encodeURIComponent(accountId)}`
    : `${BASE_URL}/v1/trend`;
  const res = await fetch(url, { headers: authHeaders() });
  if (!res.ok) throw new Error('Failed to fetch trend');
  return res.json();
}

// ── Track C: Dismiss / Snooze ─────────────────────────────────────────────────

/**
 * Fetch active dismissals for a tenant (optionally filtered by account).
 * @param {string|null} accountId — internal account UUID, or null for all accounts
 */
export async function fetchDismissals(accountId) {
  const url = accountId
    ? `${BASE_URL}/v1/dismissals?account_id=${encodeURIComponent(accountId)}`
    : `${BASE_URL}/v1/dismissals`;
  const res = await fetch(url, { headers: authHeaders() });
  if (!res.ok) throw new Error('Failed to fetch dismissals');
  return res.json();
}

/**
 * Dismiss or snooze a ghost resource.
 * @param {object} opts
 * @param {string} opts.accountId   — internal account UUID
 * @param {string} opts.provider    — e.g. "aws"
 * @param {string} opts.service     — e.g. "AmazonEC2"
 * @param {string} opts.region      — e.g. "eu-central-1"
 * @param {string} opts.resourceId  — resource identifier
 * @param {string} opts.action      — "dismiss" | "snooze"
 * @param {string} opts.reason      — reason code
 * @param {string} [opts.note]      — required when reason="other"
 * @param {string} [opts.snoozeUntil] — ISO 8601 timestamp, required for snooze
 */
export async function dismissGhost({ accountId, provider, service, region, resourceId, action, reason, note, snoozeUntil }) {
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
  if (!res.ok) throw new Error('Failed to dismiss ghost');
  return res.json();
}

/**
 * Revoke an active dismissal by its ID (restores the ghost to the active list).
 * @param {number} dismissalId
 */
export async function revokeDismissal(dismissalId) {
  const res = await fetch(`${BASE_URL}/v1/dismissals/${dismissalId}`, {
    method: 'DELETE',
    headers: authHeaders(),
  });
  if (!res.ok) throw new Error('Failed to revoke dismissal');
}

/**
 * Fetch ghosts including dismissed/snoozed ones.
 * @param {string|null} accountId
 */
export async function fetchGhostsWithDismissed(accountId) {
  const params = new URLSearchParams({ include_dismissed: 'true' });
  if (accountId) params.set('account_id', accountId);
  const res = await fetch(`${BASE_URL}/v1/ghosts?${params}`, { headers: authHeaders() });
  if (!res.ok) throw new Error('Failed to fetch ghosts');
  return res.json();
}
