import { useTheme } from './theme.jsx';

// BrandLogo renders both logo variants and lets CSS show the right one for the
// active data-theme. Doing the swap in CSS (not JS) means it's correct on the
// very first paint — the boot script sets data-theme before React mounts — and
// keeps leaf screens free of theme wiring.
export function BrandLogo() {
  return (
    <span className="brand-logo-wrap">
      <img className="brand-logo brand-logo-light" src="/axiaops-logo.svg" alt="AxiaOps" />
      <img className="brand-logo brand-logo-dark" src="/axiaops-logo-dark.svg" alt="AxiaOps" />
    </span>
  );
}

// ThemeToggle flips light/dark. The glyph shows the mode you'll switch TO.
export function ThemeToggle() {
  const { isDark, toggleTheme } = useTheme();
  return (
    <button
      type="button"
      className="secondary icon-btn"
      onClick={toggleTheme}
      aria-label={isDark ? 'Switch to light theme' : 'Switch to dark theme'}
      title={isDark ? 'Switch to light theme' : 'Switch to dark theme'}
    >
      {isDark ? '☀' : '☾'}
    </button>
  );
}
