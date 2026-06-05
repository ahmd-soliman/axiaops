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

// Sun / moon icons — copied from the tenant dashboard's AppShell so the toggle
// is visually identical across the two planes.
function IconSun({ size = 17 }) {
  return (
    <svg
      width={size}
      height={size}
      viewBox="0 0 24 24"
      fill="none"
      stroke="var(--color-accent-muted)"
      strokeWidth="2"
      strokeLinecap="round"
      strokeLinejoin="round"
    >
      <circle cx="12" cy="12" r="4" />
      <line x1="12" y1="2" x2="12" y2="4" />
      <line x1="12" y1="20" x2="12" y2="22" />
      <line x1="4.22" y1="4.22" x2="5.64" y2="5.64" />
      <line x1="18.36" y1="18.36" x2="19.78" y2="19.78" />
      <line x1="2" y1="12" x2="4" y2="12" />
      <line x1="20" y1="12" x2="22" y2="12" />
      <line x1="4.22" y1="19.78" x2="5.64" y2="18.36" />
      <line x1="18.36" y1="5.64" x2="19.78" y2="4.22" />
    </svg>
  );
}

function IconMoon({ size = 17 }) {
  return (
    <svg
      width={size}
      height={size}
      viewBox="0 0 24 24"
      fill="none"
      stroke="var(--color-accent-muted)"
      strokeWidth="2"
      strokeLinecap="round"
      strokeLinejoin="round"
    >
      <path d="M12 3c.132 0 .263 0 .393 0a7.5 7.5 0 0 0 7.92 12.446a9 9 0 1 1 -8.313 -12.454z" />
    </svg>
  );
}

// ThemeToggle flips light/dark. Shows the sun while dark (→ light) and the moon
// while light (→ dark), matching the tenant dashboard's single-button toggle.
export function ThemeToggle() {
  const { isDark, toggleTheme } = useTheme();
  return (
    <button
      type="button"
      className="theme-toggle"
      onClick={toggleTheme}
      aria-label={isDark ? 'Switch to light mode' : 'Switch to dark mode'}
      title={isDark ? 'Switch to light mode' : 'Switch to dark mode'}
    >
      {isDark ? <IconSun /> : <IconMoon />}
    </button>
  );
}
