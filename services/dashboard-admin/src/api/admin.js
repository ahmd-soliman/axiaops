// Admin-plane API client. Talks to cmd/api-admin (:8090) under /admin/*.
//
// BASE defaults to "/admin" so dev is same-origin (Vite proxies /admin → :8090,
// the browser sees only :5174) — the axiaops_staff_session cookie round-trips
// with credentials:'include' and no CORS. Override with VITE_ADMIN_API_URL for
// a true cross-origin deployment (then the backend must set ADMIN_CORS_ORIGIN).
const BASE = import.meta.env.VITE_ADMIN_API_URL || '/admin';

// ApiError carries the HTTP status + the backend's stable error code so screens
// can branch (e.g. 401 → redirect to login, 409 last_superadmin → inline note).
export class ApiError extends Error {
  constructor(status, code, message) {
    super(message || code || `HTTP ${status}`);
    this.name = 'ApiError';
    this.status = status;
    this.code = code;
  }
}

async function request(method, path, body) {
  let res;
  try {
    res = await fetch(`${BASE}${path}`, {
      method,
      credentials: 'include',
      headers: body ? { 'Content-Type': 'application/json' } : undefined,
      body: body ? JSON.stringify(body) : undefined,
    });
  } catch (networkErr) {
    throw new ApiError(0, 'network_error', networkErr.message);
  }

  if (res.status === 204) return null;

  let data = null;
  const text = await res.text();
  if (text) {
    try {
      data = JSON.parse(text);
    } catch {
      data = null;
    }
  }

  if (!res.ok) {
    throw new ApiError(res.status, data?.error, data?.message);
  }
  return data;
}

export const adminApi = {
  // auth
  login: (email, password) => request('POST', '/auth/login', { email, password }),
  logout: () => request('POST', '/auth/logout'),
  me: () => request('GET', '/me'),

  // read-only tenant console
  listTenants: () => request('GET', '/tenants'),
  getTenant: (id) => request('GET', `/tenants/${encodeURIComponent(id)}`),

  // staff management (superadmin)
  listStaff: () => request('GET', '/staff'),
  createStaff: (payload) => request('POST', '/staff', payload),
  grantRole: (id, role) => request('POST', `/staff/${encodeURIComponent(id)}/roles`, { role }),
  revokeRole: (id, role) =>
    request('DELETE', `/staff/${encodeURIComponent(id)}/roles/${encodeURIComponent(role)}`),
};

export const STAFF_ROLES = ['support', 'ops', 'billing', 'superadmin'];
