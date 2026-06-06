// formatDate renders an ISO timestamp as YYYY-MM-DD, or '—' when absent.
// Falls back to the raw string if it doesn't parse.
export function formatDate(iso) {
  if (!iso) return '—';
  const d = new Date(iso);
  if (Number.isNaN(d.getTime())) return iso;
  return d.toISOString().slice(0, 10);
}
