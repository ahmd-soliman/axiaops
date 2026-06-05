// Resolves the theme synchronously before React mounts so the first paint is
// already in the right mode (no flash). Mirrors src/theme.jsx — same 'theme'
// localStorage key + OS fallback; keep the two in sync. External file (not
// inline) so a strict script-src CSP doesn't need to allow inline scripts.
(function () {
  try {
    var saved = localStorage.getItem('theme');
    var explicit = saved === 'light' || saved === 'dark';
    var systemDark =
      window.matchMedia && window.matchMedia('(prefers-color-scheme: dark)').matches;
    var isDark = explicit ? saved === 'dark' : systemDark;
    var root = document.documentElement;
    root.dataset.theme = isDark ? 'dark' : 'light';
    root.style.colorScheme = isDark ? 'dark' : 'light';
  } catch (e) {
    /* ignore — defaults to light */
  }
})();
