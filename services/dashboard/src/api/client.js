// API client.
//
// VITE_API_URL controls where the dashboard fetches the API:
//   - Docker:    "/api"  (nginx proxies /api/* to the api container)
//   - Local dev: "/api"  (Vite's server.proxy in vite.config.js maps it
//                         to http://localhost:8080)
//   - Override:  set VITE_API_URL to an absolute URL when the dashboard
//                lives on a different origin from the API (a true
//                cross-origin deployment). The native-auth session
//                cookie still rides along thanks to credentials:'include'
//                in ifetch, and the API's CORS middleware echoes the
//                origin + emits Allow-Credentials when CORS_ORIGIN is
//                set to that origin.
//
// The default "/api" makes dev same-origin (browser sees :5173, Vite
// hides the proxy hop) which mirrors the production same-origin shape
// (nginx fronts both). Same-origin in both environments means the
// session cookie's domain story is uniform — no separate dev/prod
// CORS or SameSite quirks.
const BASE_URL = import.meta.env.VITE_API_URL || '/api';

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

// UNAUTHORIZED_EVENT fires when a request returns 401 — i.e. the native
// session cookie is missing, expired, or revoked server-side. The app
// shell listens and forces a logout + redirect to /login so the user
// can re-authenticate instead of staring at a stuck UI.
export const UNAUTHORIZED_EVENT = 'axiaops:unauthorized';

// SERVICE_UNAVAILABLE_EVENT fires when a request returns 503 — i.e. the
// API is reachable but degraded (load balancer 503, app boot, planned
// maintenance window). App.jsx listens and bounces to /service-unavailable
// so the user gets a friendly "we'll be back shortly" page instead of
// a half-loaded UI peppered with red error banners. Distinct from the
// static public/maintenance.html, which the edge proxy serves when the
// SPA bundle itself can't be fetched.
export const SERVICE_UNAVAILABLE_EVENT = 'axiaops:service-unavailable';

// notifyForbidden / notifyUnauthorized / notifyServiceUnavailable are
// decoupled from React deliberately — keeping client.js as a plain JS
// module avoids importing React into the data layer. Listeners register
// on window in MeContext / App.
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

function notifyServiceUnavailable(detail) {
  if (typeof window !== 'undefined') {
    window.dispatchEvent(new CustomEvent(SERVICE_UNAVAILABLE_EVENT, { detail }));
  }
}

// ifetch wraps the global `fetch` to fire FORBIDDEN_EVENT on 403 and
// UNAUTHORIZED_EVENT on 401 without changing the caller's error semantics.
// The pre-RBAC API methods call ifetch instead of fetch so a 401/403 anywhere
// triggers the right app-level reaction (logout vs MeContext refresh).
//
// `credentials: 'include'` is forced on every call so the native session
// cookie (axiaops_session) is sent cross-origin during local dev (Vite at
// :5173 → API at :8080). In Docker prod the dashboard is same-origin so
// the flag is a no-op.
async function ifetch(url, opts) {
  const merged = { ...(opts || {}), credentials: 'include' };
  const res = await fetch(url, merged);
  // /v1/me is the auth check itself — a 401 there is the *answer*, not a
  // "session lost mid-action" signal. Firing UNAUTHORIZED_EVENT for it
  // can re-enter components that re-call /v1/me (Login mount-probe,
  // MeContext refresh on remount), and the resulting tight loop is what
  // Chrome's navigation throttle catches.
  if (res.status === 401 && !url.endsWith('/v1/me')) notifyUnauthorized({ path: url });
  if (res.status === 503) notifyServiceUnavailable({ path: url });
  // Capability/role 403 (membership removed, role demoted) — fire
  // FORBIDDEN_EVENT so MeContext re-fetches /v1/me and the UI re-gates.
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

export async function fetchSummaryByAccount() {
  const res = await ifetch(`${BASE_URL}/v1/summary/by-account`, { headers: authHeaders() });
  if (!res.ok) throw new Error('Failed to fetch per-account summary');
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
    try { body = await res.json(); } catch { body = {}; }
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

// When `since`/`until` (ISO YYYY-MM-DD) are provided they select an absolute
// calendar window and the server ignores `days`; otherwise `days` is a trailing
// "last N days" window. The absolute path backs the Custom… date-range picker —
// without it a custom range silently degrades to a trailing window.
export async function fetchCosts(accountId, service, days = 30, since = null, until = null) {
  const params = new URLSearchParams();
  if (accountId) params.set('account_id', accountId);
  if (service) params.set('service', service);
  if (since && until) {
    params.set('since', since);
    params.set('until', until);
  } else {
    params.set('days', String(days));
  }
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
//
// Hard in-flight de-dupe: multiple concurrent callers share a single
// network request, and a 401/403 result is cached for 250ms. Belt and
// suspenders against any render-loop regression — if components remount
// in a tight cycle (which is what produced the navigation-throttle
// flood), they all see the same in-flight Promise instead of stampeding
// the API with /v1/me requests.
let _meInFlight = null;
let _meLastError = null;
let _meLastErrorAt = 0;
export async function fetchMe() {
  if (_meInFlight) return _meInFlight;
  if (_meLastError && Date.now() - _meLastErrorAt < 250) {
    throw _meLastError;
  }
  // Capture a local reference to the new promise so the .finally below
  // can identity-compare before nulling. Without this guard, an old
  // promise's .finally — running after resetFetchMeCache + a new
  // fetchMe started a fresh in-flight — would clobber the new pointer
  // and break in-flight coalescing. Identity check ensures only the
  // promise that "owns" the pointer is allowed to clear it.
  const promise = request('/v1/me')
    .then((data) => {
      _meLastError = null;
      return data;
    })
    .catch((err) => {
      _meLastError = err;
      _meLastErrorAt = Date.now();
      throw err;
    })
    .finally(() => {
      if (_meInFlight === promise) _meInFlight = null;
    });
  _meInFlight = promise;
  return promise;
}

// resetFetchMeCache clears the 250ms error cache AND drops any in-flight
// /v1/me promise pointer. Called from every auth ceremony entry point
// (login, bootstrap, redeem-invitation, select-org) — a fresh cookie has
// just been minted, any cached 401 OR any in-flight pre-auth /v1/me
// promise is stale and would otherwise briefly block (or actively poison)
// the post-login MeContext.refresh().
//
// Nulling _meInFlight does NOT cancel the underlying fetch — that one
// resolves to wherever the network goes, but its result is discarded
// because no caller awaits it. The next fetchMe() call gets a fresh
// promise tied to the current cookie.
function resetFetchMeCache() {
  _meLastError = null;
  _meLastErrorAt = 0;
  _meInFlight = null;
}

// patchMe updates the authenticated user's display name. Server trims and
// caps at 120 runes; empty is allowed (unset). On success the API returns
// the updated /v1/me shape so the caller can hand it to MeContext.refresh()
// — though refresh() pulling a fresh /v1/me is the simpler, dedupe-aware
// path the dashboard uses today.
export async function patchMe({ name }) {
  return request('/v1/users/me', { method: 'PATCH', body: { name } });
}

// ── Multi-org picker handoff ────────────────────────────────────────────────
// Login → /select-org needs to ferry the user's email + password + orgs
// list across the route boundary so the picker can re-POST to
// /v1/auth/select-org. Three options were considered:
//
//   1. React Router state (navigate(path, { state })). REJECTED — react-router
//      v6 stores this in window.history.state, which the browser session-
//      history manager persists across hard refreshes within the tab. The
//      password would survive a refresh on /select-org. Not catastrophic
//      (session-history is tab-scoped), but unnecessary durability.
//   2. sessionStorage. REJECTED — survives refresh too, and is enumerable
//      from any same-origin code (DevTools, browser extensions).
//   3. Module-level variable. ACCEPTED — wiped when the JS bundle re-inits
//      (hard refresh, navigation that reloads the SPA). Lives in the same
//      tab's runtime memory only. Genuinely transient.
//
// Lifecycle:
//   - Login.jsx sets it on the multi-org branch of authLogin.
//   - OrgPickerScreen.jsx reads it on mount (idempotent — a re-read returns
//     the same value, important for React StrictMode double-render).
//   - OrgPickerScreen calls clearPendingOrgPick on successful pick, on
//     cancel, and on the 401 bounce. After clear: a refresh on /select-org
//     finds null and the route bounces to /login.
let _pendingOrgPick = null;

// setPendingOrgPick: ONLY called by `pages/Login.jsx` after authLogin
// returns the multi-org branch. Exported because module-private symbols
// can't cross file boundaries in JS without a build-step trick we don't
// want; treat this as package-private. Any future caller must justify
// why they're stuffing a password into module state.
export function setPendingOrgPick(payload) {
  _pendingOrgPick = payload;
}
export function getPendingOrgPick() {
  return _pendingOrgPick;
}
export function clearPendingOrgPick() {
  _pendingOrgPick = null;
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

// ── SSO connections (Phase B2 slice 5) ─────────────────────────────────────
//
// Owner-only writes (sso:manage); viewer+ reads (sso:read). The server
// encrypts oidc_client_secret at the API boundary — clients send plaintext
// in `oidc_client_secret`, the response strips ciphertext entirely.

// discoverSSO is the pre-auth email-blur lookup powering LoginScreen.
// Returns { has_sso, redirect_url, ... } with constant response shape — the
// server never 4xx's on bad input, so callers don't need to fall back on
// errors caused by the email value (only on transport errors).
export async function discoverSSO(email) {
  const qs = `?email=${encodeURIComponent(email)}`;
  return request(`/v1/sso/discover${qs}`);
}

export async function listSSOConnections() {
  return request('/v1/sso/connections');
}

export async function createSSOConnection(payload) {
  return request('/v1/sso/connections', { method: 'POST', body: payload });
}

export async function updateSSOConnection(id, payload) {
  return request(`/v1/sso/connections/${encodeURIComponent(id)}`, {
    method: 'PATCH',
    body: payload,
  });
}

export async function deleteSSOConnection(id) {
  return request(`/v1/sso/connections/${encodeURIComponent(id)}`, { method: 'DELETE' });
}

export async function listSSODomains() {
  return request('/v1/sso/domains');
}

// Returns the freshly-created row including `verification_token` — the
// caller MUST surface this immediately because the token is stripped from
// every subsequent list response.
export async function createSSODomain({ ssoConnectionId, domain }) {
  return request('/v1/sso/domains', {
    method: 'POST',
    body: { sso_connection_id: ssoConnectionId, domain },
  });
}

// Returns either {verified:true, expires_at} or {verified:false, reason}.
export async function verifySSODomain(id) {
  return request(`/v1/sso/domains/${encodeURIComponent(id)}/verify`, { method: 'POST' });
}

export async function deleteSSODomain(id) {
  return request(`/v1/sso/domains/${encodeURIComponent(id)}`, { method: 'DELETE' });
}

export async function listSSOGroupMappings(connectionId) {
  return request(`/v1/sso/connections/${encodeURIComponent(connectionId)}/group-mappings`);
}

// Replace the FULL set of mappings for the connection. Server-side this is
// a transactional delete-then-insert per replaceSSOGroupMappings handler.
export async function replaceSSOGroupMappings(connectionId, mappings) {
  return request(`/v1/sso/connections/${encodeURIComponent(connectionId)}/group-mappings`, {
    method: 'PUT',
    body: { mappings },
  });
}

// ── Notification channels ─────────────────────────────────────────────────────
// Secret config fields (smtp_pass, webhook_url) come back masked as "***" on
// read; a PATCH that sends "***" (or empty) keeps the stored secret.

export async function listChannels() {
  return request('/v1/channels');
}

export async function createChannel({ kind, label, enabled, triggerRule, config }) {
  return request('/v1/channels', {
    method: 'POST',
    body: { kind, label, enabled, trigger_rule: triggerRule, config },
  });
}

export async function updateChannel(id, { label, enabled, triggerRule, config }) {
  const body = {};
  if (label !== undefined) body.label = label;
  if (enabled !== undefined) body.enabled = enabled;
  if (triggerRule !== undefined) body.trigger_rule = triggerRule;
  if (config !== undefined) body.config = config;
  return request(`/v1/channels/${encodeURIComponent(id)}`, { method: 'PATCH', body });
}

export async function deleteChannel(id) {
  return request(`/v1/channels/${encodeURIComponent(id)}`, { method: 'DELETE' });
}

// Returns {status: "sent"|"failed", error?: string}.
export async function testChannel(id) {
  return request(`/v1/channels/${encodeURIComponent(id)}/test`, { method: 'POST' });
}

export async function listChannelDispatches(id) {
  return request(`/v1/channels/${encodeURIComponent(id)}/dispatches`);
}

// ── Native auth ─────────────────────────────────────────────────────────────
//
// All four endpoints below work via the `axiaops_session` HttpOnly cookie —
// the browser sends and stores it automatically (`credentials: 'include'`
// is forced in ifetch). No Bearer token, no localStorage, no token
// handling on the JS side.

// Maps a 4xx error from a native-auth POST to a structured object the
// caller can switch on. Falls back to a generic error when the body
// isn't a JSON envelope.
async function decodeAuthError(res) {
  try {
    const body = await res.json();
    const err = new Error(body.message || `request failed: ${res.status}`);
    err.status = res.status;
    err.code = body.error || '';
    err.body = body;
    return err;
  } catch {
    const err = new Error(`request failed: ${res.status}`);
    err.status = res.status;
    return err;
  }
}

// authLogin posts email + password to /v1/auth/login. The server returns
// 200 in two distinct shapes:
//   - Single-membership: {user, organization} — session cookie minted.
//   - Multi-membership:  {needs_org_selection: true, orgs: [{id, name}]} —
//                        no cookie; caller must POST /v1/auth/select-org
//                        with the same creds + the chosen org_id.
//
// We only reset the in-memory /v1/me cache on the single-org branch where
// a fresh cookie was actually minted. The picker branch leaves the prior
// auth state untouched (no cookie, no me-cache invalidation needed).
//
// Error: 401 invalid_credentials — wrong email or password.
export async function authLogin(email, password) {
  const res = await ifetch(`${BASE_URL}/v1/auth/login`, {
    method: 'POST',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify({ email, password }),
  });
  if (!res.ok) throw await decodeAuthError(res);
  const body = await res.json();
  if (!body.needs_org_selection) resetFetchMeCache();
  return body;
}

// authSelectOrg is the picker step that pairs with authLogin's multi-org
// branch. Re-posts {email, password, organization_id} so the server can
// re-validate the password from scratch (defence in depth — never trust
// the frontend to remember step 1) and mint a session bound to the
// chosen org. Returns {user, organization} on success; the cookie is
// set by the server.
//
// Error shapes the caller must handle:
//   - 401 invalid_credentials — wrong password OR org_id not in the
//     user's membership set (server collapses both to one shape so a
//     no-creds attacker can't probe org existence).
//   - 429 rate_limited — shared budget with /v1/auth/login.
export async function authSelectOrg(email, password, organizationId) {
  const res = await ifetch(`${BASE_URL}/v1/auth/select-org`, {
    method: 'POST',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify({ email, password, organization_id: organizationId }),
  });
  if (!res.ok) throw await decodeAuthError(res);
  resetFetchMeCache();
  return res.json();
}

// authSwitchOrg is the in-app org-switcher step (B1.5 §4.7.1). The caller
// is already authenticated — we just rebind the session to a different
// organisation they're a member of. No password re-check; the existing
// cookie carries authn. Body: {organization_id}.
//
// Server semantics on success: revokes the current session in PG + cache,
// mints a new one bound to the target org, sets a fresh cookie. Audit row
// `session_org_switched` written to the FROM org.
//
// Error shapes the caller must handle:
//   - 401 unauthorized — cookie missing or session already revoked
//     elsewhere. Caller should bounce to /login.
//   - 403 not_a_member — caller doesn't have a membership in the target.
//     Stale dropdown state — caller should refresh.
export async function authSwitchOrg(organizationId) {
  const res = await ifetch(`${BASE_URL}/v1/auth/switch-org`, {
    method: 'POST',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify({ organization_id: organizationId }),
  });
  if (!res.ok) throw await decodeAuthError(res);
  // Reset fetchMe's caches because the session token rotated and any
  // stale 401 (or in-flight pre-rotate /v1/me) now points at the wrong
  // identity. Caller should also re-fetch /v1/me and clear the query
  // cache because all org-bound query results are now wrong-org.
  resetFetchMeCache();
  return res.json();
}

// authLogout clears the server-side session and the cookie. Tolerant —
// 204 (native sessions, no cookie, double-logout) or 200 with body
// `{logout_url: "..."}` (SSO sessions where the API resolved an OIDC
// end_session_endpoint for the IdP). Returns the parsed body (or null)
// so the caller can navigate to the IdP logout URL when present —
// without that step, the IdP session outlives our session and the next
// sign-in on this browser inherits the previous user's identity.
export async function authLogout() {
  const res = await ifetch(`${BASE_URL}/v1/auth/logout`, { method: 'POST' });
  if (res.status === 204) return null;
  try {
    return await res.json();
  } catch {
    return null;
  }
}

// authBootstrapState probes whether the install is still in its
// first-run window. Returns true iff `/v1/auth/bootstrap` would succeed
// with the right token. Best-effort: any transport / 5xx error resolves
// to false so the caller falls through to the normal login flow rather
// than freezing the dashboard on a degraded api. Tasks.md row 2.7.16.
//
// No `credentials: 'include'` — the endpoint is public and ignores any
// session cookie; sending one only triggers a CORS pre-flight on
// cross-origin deploys with a non-wildcard CORS_ORIGIN.
export async function authBootstrapState() {
  try {
    const res = await fetch(`${BASE_URL}/v1/auth/bootstrap/state`);
    if (!res.ok) return false;
    const body = await res.json();
    return body.available === true;
  } catch {
    return false;
  }
}

// authBootstrap consumes the install token and creates the first owner.
// Body fields per plan §4.2: token, email, password, name,
// organization_name (optional, defaults to "AxiaOps" server-side).
// Returns {user, organization} and sets the session cookie.
export async function authBootstrap({ token, email, name, password, organizationName }) {
  const res = await ifetch(`${BASE_URL}/v1/auth/bootstrap`, {
    method: 'POST',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify({
      token,
      email,
      name,
      password,
      organization_name: organizationName || '',
    }),
  });
  if (!res.ok) throw await decodeAuthError(res);
  resetFetchMeCache();
  return res.json();
}

// authPreviewInvitation peeks at an invitation token without consuming
// it. Drives the AcceptInviteScreen UI variation: when `existing_user`
// is true, the form prompts for the user's existing password (verified
// server-side against the global users table); when false, it prompts
// for a new password + name.
//
// Errors:
//   - 410 invitation_invalid: token unknown / expired / already redeemed.
export async function authPreviewInvitation(token) {
  const res = await ifetch(`${BASE_URL}/v1/auth/invitations/preview`, {
    method: 'POST',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify({ token }),
  });
  if (!res.ok) throw await decodeAuthError(res);
  return res.json();
}

// authRedeemInvitation accepts an invite token. Two flows the server
// disambiguates from the email on the token:
//   - New user: pass {token, password, name}. Server hashes the
//     password, creates the user, inserts the membership, mints a session.
//   - Existing user (B1.5): pass {token, password}. Name is ignored
//     server-side. Server verifies the password against the user's
//     existing argon2id hash and only inserts the membership.
// Returns {user, organization} on success.
export async function authRedeemInvitation({ token, name, password }) {
  const res = await ifetch(`${BASE_URL}/v1/auth/invitations/redeem`, {
    method: 'POST',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify({ token, name, password }),
  });
  if (!res.ok) throw await decodeAuthError(res);
  resetFetchMeCache();
  return res.json();
}

// authRedeemPasswordReset sets a new password from an admin-issued
// token. Returns 204 on success. The server revokes every live session
// for the user, so any open tab the user had stays logged out — the
// dashboard should redirect to /login after this completes.
export async function authRedeemPasswordReset({ token, newPassword }) {
  const res = await ifetch(`${BASE_URL}/v1/auth/password-reset/redeem`, {
    method: 'POST',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify({ token, new_password: newPassword }),
  });
  if (!res.ok) throw await decodeAuthError(res);
}

// issuePasswordReset is the admin counterpart — POSTs to /v1/users/{id}/
// password-reset to mint a one-time URL the admin shares OOB with the
// user. Permission gate enforced server-side (admin+; owners-only when
// target is owner). Returns {user_id, redemption_url, expires_at}.
export async function issuePasswordReset(userId) {
  return request(`/v1/users/${encodeURIComponent(userId)}/password-reset`, { method: 'POST' });
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
