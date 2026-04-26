// CSV cell encoder — quote-escape plus OWASP CSV-injection guard.
//
// Cells whose first character is `=`, `+`, `-`, `@`, `\t`, or `\r` are
// prefixed with `'` so Excel / Google Sheets / LibreOffice treat them as
// text instead of evaluating them as formulas. The `'` is hidden on render
// but visible in the raw file.
//
// Use for every cell emitted to a .csv download — headers included.
export function csvCell(value) {
  const s = String(value);
  const escaped = /^[=+\-@\t\r]/.test(s) ? `'${s}` : s;
  return `"${escaped.replace(/"/g, '""')}"`;
}

// Encode a 2D array (headers + rows) into a CSV string.
export function csvEncode(headers, rows) {
  return [headers, ...rows]
    .map(row => row.map(csvCell).join(','))
    .join('\n');
}

// Trigger a browser download of `blob` as `filename`. Revokes the object URL
// after a short delay so large downloads (tens of MB+) have time to start
// streaming before the URL is invalidated.
export function downloadBlob(blob, filename) {
  const url  = URL.createObjectURL(blob);
  const a    = document.createElement('a');
  a.href     = url;
  a.download = filename;
  a.click();
  setTimeout(() => URL.revokeObjectURL(url), 1000);
}

// The Blob is prefixed with a UTF-8 BOM (﻿) so Excel on Windows opens
// it as UTF-8 instead of cp1252 — without this, em-dashes and accented
// characters render as mojibake (e.g. `—` → `‚Äî`).
export function downloadCSV(csv, filename) {
  downloadBlob(new Blob(['﻿', csv], { type: 'text/csv;charset=utf-8' }), filename);
}
