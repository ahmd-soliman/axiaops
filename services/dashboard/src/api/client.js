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

// FORBIDDEN_EVENT fires whenever an authenticated request returns 403. The
// MeContext provider listens and refreshes the cached role/permissions so the
// UI catches up to a server-side role change (or membership removal) without
// needing the user to reload. See docs/rbac-design.md §8 "Role-change propagation".
export const FORBIDDEN_EVENT = 'axiaops:forbidden';

// UNAUTHORIZED_EVENT fires when a request returns 401 — i.e. the Kinde JWT is
// missing, expired, or otherwise rejected by the API auth middleware. The app
// shell listens and forces a logout + redirect to /login so the user can
// re-authenticate instead of staring at a stuck UI with a stale token.
export const UNAUTHORIZED_EVENT = 'axiaops:unauthorized';

// notifyForbidden / notifyUnauthorized are decoupled from React deliberately —
// keeping client.js as a plain JS module avoids importing React into the data
// layer. Listeners register on window in MeContext / App.
function notifyForbidden(detail) {
  if (typeof window !== 'undefined') {
    window.dispatchEvent(new CustomEvent(FORBIDDEN_EVENT, { detail }));
  }
}

function notifyUnauthorized(detail) {
  if (typeof window !== 'undefined') {
    window.dispatchEvent(new CustomEvent(UNAUTHORIZED_EVENT, { detail }));
  }
}

// ifetch wraps the global `fetch` to fire FORBIDDEN_EVENT on 403 and
// UNAUTHORIZED_EVENT on 401 without changing the caller's error semantics.
// The pre-RBAC API methods call ifetch instead of fetch so a 401/403 anywhere
// triggers the right app-level reaction (logout vs MeContext refresh).
async function ifetch(url, opts) {
  const res = await fetch(url, opts);
  if (res.status === 401) notifyUnauthorized({ path: url });
  if (res.status === 403) notifyForbidden({ path: url });
  return res;
}

// request is the single fetch wrapper every API method goes through. It adds
// auth headers, intercepts 403 → dispatches FORBIDDEN_EVENT, and surfaces
// non-2xx responses as Errors with a `.status` property so callers can
// branch on (e.g.) 409 conflicts without parsing message strings.
async function request(path, { method = 'GET', body, headers = {}, raw = false } = {}) {
  const opts = {
    method,
    headers: { ...authHeaders(), ...headers },
  };
  if (body !== undefined) {
    opts.headers['Content-Type'] = 'application/json';
    opts.body = typeof body === 'string' ? body : JSON.stringify(body);
  }
  const res = await ifetch(`${BASE_URL}${path}`, opts);
  if (!res.ok) {
    const err = new Error(`request failed: ${res.status}`);
    err.status = res.status;
    try {
      err.body = await res.text();
    } catch {
      err.body = '';
    }
    throw err;
  }
  // raw=true skips body parsing — for callers that need a Blob, stream, or
  // the response headers (e.g. Content-Disposition for downloads).
  if (raw) return res;
  if (res.status === 204) return null;
  const ctype = res.headers.get('Content-Type') || '';
  if (ctype.includes('application/json')) return res.json();
  return res.text();
}

export async function fetchSummary(accountId) {
  const url = accountId
    ? `${BASE_URL}/v1/summary?account_id=${encodeURIComponent(accountId)}`
    : `${BASE_URL}/v1/summary`;
  const res = await ifetch(url, { headers: authHeaders() });
  if (!res.ok) throw new Error('Failed to fetch summary');
  return res.json();
}

export async function fetchZombies(accountId) {
  const url = accountId
    ? `${BASE_URL}/v1/zombies?account_id=${encodeURIComponent(accountId)}`
    : `${BASE_URL}/v1/zombies`;
  const res = await ifetch(url, { headers: authHeaders() });
  if (!res.ok) throw new Error('Failed to fetch zombies');
  return res.json();
}

export async function fetchResources(accountId) {
  const url = accountId
    ? `${BASE_URL}/v1/resources?account_id=${encodeURIComponent(accountId)}`
    : `${BASE_URL}/v1/resources`;
  const res = await ifetch(url, { headers: authHeaders() });
  if (!res.ok) throw new Error('Failed to fetch resources');
  return res.json();
}

export async function fetchAccounts() {
  const res = await ifetch(`${BASE_URL}/v1/accounts`, { headers: authHeaders() });
  if (!res.ok) throw new Error('Failed to fetch accounts');
  return res.json();
}

export async function connectAccount({ provider, label, accessKeyId, secretKey, region }) {
  const res = await ifetch(`${BASE_URL}/v1/accounts`, {
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

  const res = await ifetch(`${BASE_URL}/v1/accounts/${id}`, {
    method: 'PATCH',
    headers: { ...authHeaders(), 'Content-Type': 'application/json' },
    body: JSON.stringify(body),
  });
  if (!res.ok) throw new Error('Failed to update account');
  return res.json();
}

// draftAccount kicks off role-based onboarding. Returns the account row with
// external_id populated; the dashboard renders the trust-policy template
// against that ExternalId, then calls verifyAccount once the customer has
// pasted back their role ARN. See docs/cross-account-roles-design.md §4.3.
export async function draftAccount({ provider = 'aws', label, region }) {
  const res = await ifetch(`${BASE_URL}/v1/accounts/draft`, {
    method: 'POST',
    headers: { ...authHeaders(), 'Content-Type': 'application/json' },
    body: JSON.stringify({ provider, label, region }),
  });
  if (!res.ok) throw new Error('Failed to start role onboarding');
  return res.json();
}

// verifyAccount triggers the synchronous AssumeRole probe. On success the
// backend flips the row to status='connected' and resolves account_id from
// GetCallerIdentity. On failure the response carries a structured
// {code, reason, detail} the caller should surface to the user.
export async function verifyAccount(id, { roleArn }) {
  const res = await ifetch(`${BASE_URL}/v1/accounts/${id}`, {
    method: 'PATCH',
    headers: { ...authHeaders(), 'Content-Type': 'application/json' },
    body: JSON.stringify({ role_arn: roleArn }),
  });
  if (res.status === 400) {
    let body;
    try { body = await res.json(); } catch (_) { body = {}; }
    // Use a generic top-line message — the structured `reason` drives the
    // dashboard's user-facing copy via reasonToHint(). The API deliberately
    // does not return raw AWS error strings (they carry ARNs and request IDs).
    const err = new Error('Verification failed');
    err.code = body.code;
    err.reason = body.reason;
    throw err;
  }
  if (!res.ok) throw new Error('Failed to verify role');
  return res.json();
}

export async function deleteAccount(id) {
  const res = await ifetch(`${BASE_URL}/v1/accounts/${id}`, {
    method: 'DELETE',
    headers: authHeaders(),
  });
  if (!res.ok) throw new Error('Failed to delete account');
}

export async function scanAccount(id) {
  const res = await ifetch(`${BASE_URL}/v1/accounts/${id}/scan`, {
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
  const res = await ifetch(url, { headers: authHeaders() });
  if (!res.ok) throw new Error('Failed to fetch trend');
  return res.json();
}

export async function fetchTrendServices() {
  const res = await ifetch(`${BASE_URL}/v1/trend/services`, { headers: authHeaders() });
  if (!res.ok) throw new Error('Failed to fetch trend services');
  return res.json();
}

export async function fetchTrendResourceTypes(service) {
  const res = await ifetch(`${BASE_URL}/v1/trend/resource-types?service=${encodeURIComponent(service)}`, { headers: authHeaders() });
  if (!res.ok) throw new Error('Failed to fetch trend resource types');
  return res.json();
}

export async function fetchDismissals(accountId) {
  const url = accountId
    ? `${BASE_URL}/v1/dismissals?account_id=${encodeURIComponent(accountId)}`
    : `${BASE_URL}/v1/dismissals`;
  const res = await ifetch(url, { headers: authHeaders() });
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

  const res = await ifetch(`${BASE_URL}/v1/dismissals`, {
    method: 'POST',
    headers: { ...authHeaders(), 'Content-Type': 'application/json' },
    body: JSON.stringify(body),
  });
  if (res.status === 409) throw new Error('already_dismissed');
  if (!res.ok) throw new Error('Failed to dismiss zombie');
  return res.json();
}

export async function revokeDismissal(dismissalId) {
  const res = await ifetch(`${BASE_URL}/v1/dismissals/${dismissalId}`, {
    method: 'DELETE',
    headers: authHeaders(),
  });
  if (!res.ok) throw new Error('Failed to revoke dismissal');
}

export async function fetchZombiesWithDismissed(accountId) {
  const params = new URLSearchParams({ include_dismissed: 'true' });
  if (accountId) params.set('account_id', accountId);
  const res = await ifetch(`${BASE_URL}/v1/zombies?${params}`, { headers: authHeaders() });
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
  // Server validates limit ∈ [1, 500] and 400s outside that range. Falsy
  // guard intentionally drops 0 alongside null/undefined — passing 0 would
  // be a guaranteed 400, and there's no caller intent that 0 expresses
  // (use null/undefined to mean "server default").
  if (limit) params.set('limit', String(limit));
  if (cursor)       params.set('cursor', cursor);

  const qs = params.toString();
  const url = qs ? `${BASE_URL}/v1/audit?${qs}` : `${BASE_URL}/v1/audit`;
  const res = await ifetch(url, { headers: authHeaders() });
  if (!res.ok) throw new Error('Failed to fetch audit events');
  return res.json();
}

// Fetch the API service's build identifier. The dashboard footer pairs this
// with its own build-time identifier so support tickets carry both versions.
// Returns `{ service, version, commit, env }`.
export async function fetchVersion() {
  const res = await ifetch(`${BASE_URL}/v1/version`, { headers: authHeaders() });
  if (!res.ok) throw new Error('Failed to fetch version');
  return res.json();
}

export async function fetchCosts(accountId, service, days = 30) {
  const params = new URLSearchParams();
  if (accountId) params.set('account_id', accountId);
  if (service) params.set('service', service);
  params.set('days', String(days));
  const qs = params.toString();
  const url = qs ? `${BASE_URL}/v1/costs?${qs}` : `${BASE_URL}/v1/costs`;
  const res = await ifetch(url, { headers: authHeaders() });
  if (!res.ok) throw new Error('Failed to fetch costs');
  return res.json();
}

// ── RBAC (Phase 1) ──────────────────────────────────────────────────────────

// fetchMe returns the authenticated user's role + permission set. Goes
// through `request()` so a 403 here (e.g. membership removed mid-session)
// cascades to MeContext just like any other 403.
export async function fetchMe() {
  return request('/v1/me');
}

export async function listMemberships() {
  return request('/v1/memberships');
}

// addMember adds an existing AxiaOps user (looked up server-side by email)
// to the calling org's memberships. Backend returns 404 if no user matches —
// today there's no email-out invitation flow, the user must have signed in
// at least once. See Tasks.md "Multi-organization UX" for the future
// invite-by-email flow that supersedes this constraint.
export async function addMember(email, role) {
  return request('/v1/memberships', { method: 'POST', body: { email, role } });
}

export async function updateMemberRole(membershipId, role) {
  return request(`/v1/memberships/${encodeURIComponent(membershipId)}/role`, {
    method: 'PATCH',
    body: { role },
  });
}

export async function removeMember(membershipId) {
  return request(`/v1/memberships/${encodeURIComponent(membershipId)}`, { method: 'DELETE' });
}

export async function transferOwnership(toUserID) {
  return request('/v1/organizations/transfer-ownership', {
    method: 'POST',
    body: { to_user_id: toUserID },
  });
}

// ── Email invitations (Phase 2) ─────────────────────────────────────────────

export async function listInvitations(status) {
  const qs = status ? `?status=${encodeURIComponent(status)}` : '';
  return request(`/v1/invitations${qs}`);
}

// createInvitation sends an email invitation. Returns 201 on first invite,
// 200 on re-invite (refreshed pending row), 409 on already-member or
// existing-user-no-membership (with structured error code in body).
export async function createInvitation(email, role, name) {
  return request('/v1/invitations', {
    method: 'POST',
    body: { email, role, name },
  });
}

export async function revokeInvitation(id) {
  return request(`/v1/invitations/${encodeURIComponent(id)}`, { method: 'DELETE' });
}

// ── Organization rename + onboarding (Phase 2) ─────────────────────────────

export async function patchOrganization(name) {
  return request('/v1/organizations/me', { method: 'PATCH', body: { name } });
}

export async function completeOnboarding(stepsSkipped = []) {
  return request('/v1/organizations/me/onboarding/complete', {
    method: 'POST',
    body: { steps_skipped: stepsSkipped },
  });
}

// ── GDPR right-to-erasure ───────────────────────────────────────────────────
//
// Both go through `request()` so a 409 (sole-owner refusal on /users/me)
// surfaces as `err.status === 409` with `err.body` carrying the API's
// human-readable explanation.

export async function deleteCurrentUser() {
  return request('/v1/users/me', { method: 'DELETE' });
}

export async function deleteCurrentOrganization() {
  return request('/v1/organizations/me', { method: 'DELETE' });
}

// exportOrganizationData fetches GET /v1/export and returns { blob, filename } so
// the caller can wire it into a browser download.
export async function exportOrganizationData() {
  const res = await request('/v1/export', { raw: true });
  const cd = res.headers.get('Content-Disposition') || '';
  const match = cd.match(/filename="([^"]+)"/);
  return {
    blob: await res.blob(),
    filename: match ? match[1] : 'axiaops-export.json',
  };
}
