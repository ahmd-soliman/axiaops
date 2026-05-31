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
//   - the native <input type="date"> picker, whose display format follows the
//     user's OS locale (correct behaviour for an input control).
//
// Accepts an ISO string or a Date; returns '' for null / empty / unparseable input.
export function formatDate(value) {
  if (!value) return '';
  const d = value instanceof Date ? value : new Date(value);
  if (Number.isNaN(d.getTime())) return '';
  return d.toLocaleDateString('en-GB', { day: 'numeric', month: 'short', year: 'numeric' });
}
