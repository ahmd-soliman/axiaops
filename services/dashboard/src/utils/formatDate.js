// Shared human-facing date formatter — the single source of truth for how a
// displayed calendar date looks across the app.
//
// Renders an abbreviated month NAME ("29 May 2026") rather than a numeric
// dd.mm.yyyy / mm.dd.yyyy form. A numeric date is locale-ambiguous — "05.06.2026"
// is 5 June to a European and 6 May to an American — whereas a month name reads
// the same to everyone. (This is why design systems like GOV.UK mandate the
// month-name form for displayed dates.)
//
// Use this for any full calendar date shown to a user. Two things intentionally
// do NOT route through here:
//   - compact chart-axis ticks that omit the year ("29 May"), where space is tight;
//   - the native <input type="date"> picker, which is pinned to `lang="en-GB"`
//     (dd/mm/yyyy) at each call site instead, so its own OS-locale-driven
//     rendering can't disagree with the day/month order used everywhere else.
//
// Accepts an ISO string or a Date; returns '' for null / empty / unparseable input.
export function formatDate(value) {
  if (!value) return '';
  const d = value instanceof Date ? value : new Date(value);
  if (Number.isNaN(d.getTime())) return '';
  return d.toLocaleDateString('en-GB', { day: 'numeric', month: 'short', year: 'numeric' });
}

// Compact, locale-unambiguous label for a date range — collapses the redundant
// month/year. Used by the date-range picker chip so a Custom selection reads as
// "1–15 May 2026" instead of the native input's locale-driven MM/DD or DD/MM.
//   same month+year → "1–15 May 2026"
//   same year       → "1 May – 15 Jun 2026"
//   otherwise       → "29 Dec 2026 – 2 Jan 2027"
export function formatDateRange(sinceValue, untilValue) {
  if (!sinceValue || !untilValue) return '';
  const a = sinceValue instanceof Date ? sinceValue : new Date(sinceValue);
  const b = untilValue instanceof Date ? untilValue : new Date(untilValue);
  if (Number.isNaN(a.getTime()) || Number.isNaN(b.getTime())) return '';
  const full = { day: 'numeric', month: 'short', year: 'numeric' };
  const dayMonth = { day: 'numeric', month: 'short' };
  if (a.getFullYear() === b.getFullYear() && a.getMonth() === b.getMonth()) {
    return `${a.getDate()}–${b.toLocaleDateString('en-GB', full)}`;
  }
  if (a.getFullYear() === b.getFullYear()) {
    return `${a.toLocaleDateString('en-GB', dayMonth)} – ${b.toLocaleDateString('en-GB', full)}`;
  }
  return `${a.toLocaleDateString('en-GB', full)} – ${b.toLocaleDateString('en-GB', full)}`;
}
