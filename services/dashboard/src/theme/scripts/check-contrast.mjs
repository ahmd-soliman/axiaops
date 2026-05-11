#!/usr/bin/env node
// WCAG contrast ratio checker for AxiaOps theme tokens.
//
// Parses the `:root` and `:root[data-theme="dark"]` blocks in
// ../../styles/tokens.css and prints the contrast ratio for every
// text-on-background pairing that is actually rendered by the app.
//
// Issue #88 moved the tokens out of `ThemeContext.jsx`'s JS objects into
// CSS custom properties; this script tracked the rename.
//
// Usage:
//   node services/dashboard/src/theme/scripts/check-contrast.mjs
//
// Exit code 0 if all pairings meet their target, 1 otherwise.

import fs from 'node:fs';
import path from 'node:path';
import { fileURLToPath } from 'node:url';

const here = path.dirname(fileURLToPath(import.meta.url));
const tokensFile = path.resolve(here, '..', '..', 'styles', 'tokens.css');

// camelCase token name (the historical schema) ↔ kebab CSS var.
// Keep this in sync with `tokens.css`. Tokens not listed here are
// ignored (e.g. legacy `navy` aliases, the `--color-track` ramp helper,
// and the indexed `--color-viz-ramp-N` ramp stops — none of which are
// involved in a text-on-bg contrast pairing).
const TOKEN_NAMES = [
  'bg', 'bgSecondary',
  'surface', 'surfaceAlt', 'surfaceRaised',
  'accent', 'accentMuted', 'accentLight', 'accentBorder', 'accentText',
  'text', 'textMid', 'textMuted', 'textSub', 'textOnDark', 'white',
  'card', 'border',
  'chipBg', 'chipText',
  'chipProdBg', 'chipProdText',
  'chipStagBg', 'chipStagText',
  'zombieBadgeBg', 'zombieBadgeText',
  'error', 'success', 'warning',
  'alertCritical', 'alertWarning', 'statusOk',
];

function kebab(camel) {
  return camel.replace(/[A-Z]/g, (m) => '-' + m.toLowerCase());
}

// Extract a `--color-X-kebab: #hex;` declaration from a single CSS block.
// `block` is the body between `:root { … }` (or the dark override `{ … }`).
function extractTheme(blockBody) {
  const out = {};
  for (const tok of TOKEN_NAMES) {
    const varName = `--color-${kebab(tok)}`;
    // Match `--color-X: #ABCDEF;` allowing trailing whitespace + comment.
    const re = new RegExp(
      varName.replace('-', '\\-') + '\\s*:\\s*(#[0-9A-Fa-f]{3,8})\\s*;',
    );
    const m = blockBody.match(re);
    if (m) out[tok] = m[1];
  }
  return out;
}

// Pull the `:root { … }` body and the `:root[data-theme="dark"] { … }`
// body out of the file. Brace-counting is naive (no nested rules in
// tokens.css), so a flat regex is enough.
function extractBlocks(src) {
  const blocks = {};
  const lightRe = /:root\s*\{([\s\S]*?)\n\}/;
  const darkRe  = /:root\[data-theme="dark"\]\s*\{([\s\S]*?)\n\}/;
  const lm = src.match(lightRe);
  const dm = src.match(darkRe);
  if (!lm) throw new Error('Could not find :root block in tokens.css');
  if (!dm) throw new Error('Could not find :root[data-theme="dark"] block in tokens.css');
  blocks.light = lm[1];
  blocks.dark  = dm[1];
  return blocks;
}

function rel(hex) {
  const c = hex.replace('#', '');
  const r = parseInt(c.slice(0, 2), 16) / 255;
  const g = parseInt(c.slice(2, 4), 16) / 255;
  const b = parseInt(c.slice(4, 6), 16) / 255;
  const s = v => (v <= 0.03928 ? v / 12.92 : Math.pow((v + 0.055) / 1.055, 2.4));
  return 0.2126 * s(r) + 0.7152 * s(g) + 0.0722 * s(b);
}

function ratio(a, b) {
  const la = rel(a);
  const lb = rel(b);
  const [hi, lo] = la > lb ? [la, lb] : [lb, la];
  return (hi + 0.05) / (lo + 0.05);
}

function grade(r) {
  if (r >= 7) return 'AAA';
  if (r >= 4.5) return 'AA';
  if (r >= 3) return 'AA-lg';
  return 'FAIL';
}

// Text-on-background pairings actually used by the app.
// `min` is the smallest acceptable ratio. Use 3 for large-text-only tokens.
const PAIRS = [
  { fg: 'text',         bg: 'surface',     min: 4.5 },
  { fg: 'text',         bg: 'bg',          min: 4.5 },
  { fg: 'textMid',      bg: 'surface',     min: 4.5 },
  { fg: 'textMid',      bg: 'bg',          min: 4.5 },
  { fg: 'textMuted',    bg: 'surface',     min: 4.5 },
  { fg: 'textMuted',    bg: 'bg',          min: 4.5 },
  { fg: 'textSub',      bg: 'surface',     min: 3.0 },
  { fg: 'textSub',      bg: 'bg',          min: 3.0 },
  { fg: 'accent',       bg: 'surface',     min: 3.0 },
  { fg: 'accent',       bg: 'bg',          min: 3.0 },
  { fg: 'accentText',   bg: 'accentLight', min: 4.5 },
  { fg: 'chipText',     bg: 'chipBg',      min: 4.5 },
  { fg: 'chipProdText', bg: 'chipProdBg',  min: 4.5 },
  { fg: 'chipStagText', bg: 'chipStagBg',  min: 4.5 },
  { fg: 'zombieBadgeText', bg: 'zombieBadgeBg', min: 4.5 },
  { fg: 'error',        bg: 'surface',     min: 3.0 },
  { fg: 'success',      bg: 'surface',     min: 3.0 },
  { fg: 'warning',      bg: 'surface',     min: 3.0 },
  { fg: 'error',        bg: 'bg',          min: 3.0 },
  { fg: 'success',      bg: 'bg',          min: 3.0 },
  { fg: 'warning',      bg: 'bg',          min: 3.0 },
];

const src = fs.readFileSync(tokensFile, 'utf8');
const blocks = extractBlocks(src);
const themes = {
  light: extractTheme(blocks.light),
  dark:  extractTheme(blocks.dark),
};

let failures = 0;

// Token parity check — every name in TOKEN_NAMES must resolve in both
// themes. (We can't trivially detect tokens defined only in dark, since
// the regex is keyed on the canonical list. Run a quick eyeball over
// tokens.css when adding a new token.)
const lightKeys = Object.keys(themes.light).sort();
const darkKeys  = Object.keys(themes.dark).sort();
const onlyL = lightKeys.filter(k => !darkKeys.includes(k));
const onlyD = darkKeys.filter(k => !lightKeys.includes(k));
if (onlyL.length || onlyD.length) {
  failures++;
  console.log('── Token parity ── FAIL');
  if (onlyL.length) console.log('  only in light:', onlyL);
  if (onlyD.length) console.log('  only in dark :', onlyD);
} else {
  console.log(`── Token parity ── OK (${lightKeys.length} tokens in each theme)`);
}

for (const themeName of ['light', 'dark']) {
  console.log(`\n── ${themeName.toUpperCase()} ── (${tokensFile})`);
  const t = themes[themeName];
  for (const { fg, bg, min } of PAIRS) {
    if (!t[fg] || !t[bg]) {
      console.log(`  SKIP  ${fg} on ${bg}  (missing token)`);
      continue;
    }
    const r = ratio(t[fg], t[bg]);
    const tag = grade(r);
    const pass = r >= min;
    if (!pass) failures++;
    const pad = `${fg} on ${bg}`.padEnd(36);
    const marker = pass ? ' ' : '✗';
    console.log(
      `  ${marker} ${pad} ${t[fg]} on ${t[bg]}  →  ${r.toFixed(2)}:1  [${tag}]  (min ${min})`
    );
  }
}

console.log('');
if (failures) {
  console.log(`FAIL — ${failures} issue(s)`);
  process.exit(1);
} else {
  console.log('OK — all pairings meet target');
}
