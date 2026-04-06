// API client — points at the Go ingestion service.
//
// In local dev (npm run web) the Go service runs directly on :8080.
// In Docker the nginx proxy rewrites /api/* → ingestion:8080, so no
// cross-origin requests and no CORS headers needed in production.
const BASE_URL =
  process.env.NODE_ENV === 'production' ? '/api' : 'http://localhost:8080';

let authToken = null;

// setAuthToken is called by App.js after login / on startup token restore.
export function setAuthToken(token) {
  authToken = token;
}

function authHeaders() {
  return authToken ? { Authorization: `Bearer ${authToken}` } : {};
}

export async function fetchMe() {
  const res = await fetch(`${BASE_URL}/me`, { headers: authHeaders() });
  if (!res.ok) throw new Error('Failed to fetch me');
  return res.json();
}

export async function fetchSummary() {
  const res = await fetch(`${BASE_URL}/summary`, { headers: authHeaders() });
  if (!res.ok) throw new Error('Failed to fetch summary');
  return res.json();
}

export async function fetchGhosts() {
  const res = await fetch(`${BASE_URL}/ghosts`, { headers: authHeaders() });
  if (!res.ok) throw new Error('Failed to fetch ghosts');
  return res.json();
}
