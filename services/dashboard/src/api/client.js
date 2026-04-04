// API client — points at the Go ingestion service.
// Change BASE_URL if the service runs on a different port or host.
const BASE_URL = 'http://localhost:8080';

export async function fetchSummary() {
  const res = await fetch(`${BASE_URL}/summary`);
  if (!res.ok) throw new Error('Failed to fetch summary');
  return res.json();
}

export async function fetchGhosts() {
  const res = await fetch(`${BASE_URL}/ghosts`);
  if (!res.ok) throw new Error('Failed to fetch ghosts');
  return res.json();
}
