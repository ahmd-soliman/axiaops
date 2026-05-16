// Resolves the theme synchronously before React mounts so the very first
// paint is already in the right mode — both UA chrome (scrollbars, form
// controls, focus rings) AND app content reading CSS variables from
// src/styles/tokens.css. Issue #88: the old color-scheme-only boot script
// could not prime React-rendered colours because those came from a JS
// theme object, so cold-load briefly flashed the default theme before
// React hydrated.
//
// Mirrors the resolution logic in src/theme/ThemeContext.jsx (same 'theme'
// localStorage key, same OS fallback). Keep the two in sync — drift means
// the first paint disagrees with what React will render a moment later.
//
// Lives as an external file (not inline in index.html) so the strict
// `script-src 'self'` CSP in nginx.conf doesn't have to allow inline
// scripts. Loaded synchronously in <head> before the React module bundle.
(function () {
  try {
    var saved = localStorage.getItem('theme');
    var explicit = saved === 'light' || saved === 'dark';
    var systemDark = window.matchMedia &&
      window.matchMedia('(prefers-color-scheme: dark)').matches;
    var isDark = explicit ? (saved === 'dark') : systemDark;
    var root = document.documentElement;
    root.dataset.theme = isDark ? 'dark' : 'light';
    root.style.colorScheme = isDark ? 'dark' : 'light';
  } catch (e) { /* ignore */ }
})();
