// formatRelative renders an ISO timestamp as a short relative string for
// at-a-glance table cells: "Just now" / "5m ago" / "3h ago" / "2d ago" /
// then falls back to a locale date once the difference exceeds a week.
// Returns `fallback` (default "Never") for null/empty inputs.
export function formatRelative(iso, { fallback = 'Never' } = {}) {
  if (!iso) return fallback;
  try {
    const d = new Date(iso);
    const diff = Date.now() - d.getTime();
    const mins = Math.floor(diff / 60000);
    if (mins < 1)  return 'Just now';
    if (mins < 60) return `${mins}m ago`;
    const hrs = Math.floor(mins / 60);
    if (hrs < 24)  return `${hrs}h ago`;
    const days = Math.floor(hrs / 24);
    if (days < 7)  return `${days}d ago`;
    return d.toLocaleDateString();
  } catch {
    return iso;
  }
}
