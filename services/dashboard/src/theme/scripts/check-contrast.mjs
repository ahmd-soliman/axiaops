#!/usr/bin/env node
// WCAG contrast ratio checker for AxiaOps theme tokens.
//
// Extracts the lightTheme and darkTheme objects from ../ThemeContext.jsx
// and prints the contrast ratio for every text-on-background pairing that
// is actually rendered by the app.
//
// Usage:
//   node services/dashboard/src/theme/scripts/check-contrast.mjs
//
// Exit code 0 if all pairings meet their target, 1 otherwise.

import fs from 'node:fs';
import path from 'node:path';
import { fileURLToPath } from 'node:url';

const here = path.dirname(fileURLToPath(import.meta.url));
const themeFile = path.resolve(here, '..', 'ThemeContext.jsx');
const palettesFile = path.resolve(here, '..', 'palettes.js');

function extractObject(src, marker) {
  // Pull a `{ ... }` block keyed by `marker` (e.g. `const lightBase = {`,
  // `light: {`). Greedy-match to the *next* `},` at column 0/2 so nested
  // objects don't fool us. Returns a flat key→hex map.
  const re = new RegExp(`${marker}\\s*\\{([\\s\\S]*?)\\n\\s*\\}[,;]`);
  const m = src.match(re);
  if (!m) return null;
  const out = {};
  for (const line of m[1].split('\n')) {
    const kv = line.match(/^\s*([a-zA-Z]+)\s*:\s*'(#[0-9A-Fa-f]{3,8})'\s*,?/);
    if (kv) out[kv[1]] = kv[2];
  }
  return out;
}

function extractPalettes(src) {
  // Scan for every `<id>: { ... light: {...} ... dark: {...} ... }` entry.
  const out = {};
  const idRe = /^\s{2}([a-z]+):\s*\{$/gm;
  let m;
  while ((m = idRe.exec(src))) {
    const id = m[1];
    const block = src.slice(m.index);
    const light = extractObject(block, `light:`);
    const dark  = extractObject(block, `dark:`);
    if (light && dark) out[id] = { light, dark };
  }
  return out;
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

const themeSrc = fs.readFileSync(themeFile, 'utf8');
const palettesSrc = fs.readFileSync(palettesFile, 'utf8');

const bases = {
  light: extractObject(themeSrc, 'const lightBase ='),
  dark:  extractObject(themeSrc, 'const darkBase ='),
};
if (!bases.light || !bases.dark) {
  throw new Error('Could not find lightBase/darkBase in ThemeContext.jsx');
}

const palettes = extractPalettes(palettesSrc);
if (!Object.keys(palettes).length) {
  throw new Error('No palettes parsed from palettes.js');
}

let failures = 0;

// Brand-token parity check across palettes — keys must match between
// light/dark within each palette.
for (const [id, p] of Object.entries(palettes)) {
  const lk = Object.keys(p.light).sort();
  const dk = Object.keys(p.dark).sort();
  const onlyL = lk.filter(k => !dk.includes(k));
  const onlyD = dk.filter(k => !lk.includes(k));
  if (onlyL.length || onlyD.length) {
    failures++;
    console.log(`── Palette parity (${id}) ── FAIL`);
    if (onlyL.length) console.log('  only in light:', onlyL);
    if (onlyD.length) console.log('  only in dark :', onlyD);
  }
}

for (const [id, p] of Object.entries(palettes)) {
  for (const themeName of ['light', 'dark']) {
    console.log(`\n── ${id.toUpperCase()} / ${themeName.toUpperCase()} ──`);
    const t = { ...bases[themeName], ...p[themeName] };
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
}

console.log('');
if (failures) {
  console.log(`FAIL — ${failures} issue(s)`);
  process.exit(1);
} else {
  console.log('OK — all pairings meet target');
}
