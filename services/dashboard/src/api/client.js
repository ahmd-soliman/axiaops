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

export async function fetchAccounts() {
  const res = await fetch(`${BASE_URL}/accounts`, { headers: authHeaders() });
  if (!res.ok) throw new Error('Failed to fetch accounts');
  return res.json();
}

export async function connectAccount({ provider, label, accessKeyId, secretKey, region }) {
  const res = await fetch(`${BASE_URL}/accounts`, {
    method: 'POST',
    headers: { ...authHeaders(), 'Content-Type': 'application/json' },
    body: JSON.stringify({ provider, label, access_key_id: accessKeyId, secret_key: secretKey, region }),
  });
  if (!res.ok) throw new Error('Failed to connect account');
  return res.json();
}

export async function deleteAccount(id) {
  const res = await fetch(`${BASE_URL}/accounts/${id}`, {
    method: 'DELETE',
    headers: authHeaders(),
  });
  if (!res.ok) throw new Error('Failed to delete account');
}

export async function scanAccount(id) {
  const res = await fetch(`${BASE_URL}/accounts/${id}/scan`, {
    method: 'POST',
    headers: authHeaders(),
  });
  if (!res.ok) throw new Error('Failed to trigger scan');
  return res.json();
}
