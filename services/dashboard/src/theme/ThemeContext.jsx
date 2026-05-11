import React, { createContext, useContext, useState, useEffect } from 'react';

// Theme is a simple two-state preference (light or dark) stored under the
// 'theme' localStorage key. On cold load with no saved value we fall back to
// the OS preference (`prefers-color-scheme: dark`) — but only as the initial
// default; once the user clicks the toggle their choice is persisted and OS
// changes no longer flip the theme. This is the common shape (GitHub, Vercel,
// Linear, etc.) — explicit when the user has expressed a preference, smart
// when they haven't.
const STORAGE_KEY = 'theme';

function readSavedDark() {
  try {
    const saved = localStorage.getItem(STORAGE_KEY);
    if (saved === 'light') return false;
    if (saved === 'dark')  return true;
  } catch { /* ignore */ }
  // No saved choice — follow OS preference.
  if (typeof window === 'undefined') return false;
  return window.matchMedia?.('(prefers-color-scheme: dark)').matches ?? false;
}

function writeSavedDark(isDark) {
  try { localStorage.setItem(STORAGE_KEY, isDark ? 'dark' : 'light'); } catch { /* ignore */ }
}

// ─── Light Theme ──────────────────────────────────────────────────────────────
// Palette philosophy
//   Neutral : Tailwind stone for the four large-surface tokens (bg, bgSecondary,
//             text, border) — warmer / GitLab-Notion-leaning. Other neutrals
//             stay slate (cool/warm cast is imperceptible at those sizes).
//             Surfaces stay pure white. Dark mode stays cool slate.
//   Brand   : Orange (bold, action-oriented, distinctive) — AA on white.
//   Warning : Yellow-700, deliberately distinct hue family from the orange
//             brand so the two never read as the same colour at a glance.
const lightTheme = {
  // Backgrounds — 4-level hierarchy (page → section → card → raised)
  bg: '#FAFAF9',                // page background, stone-50 (warm off-white)
  bgSecondary: '#F5F5F4',       // sidebars, headers, grouped sections (stone-100)
  surface: '#FFFFFF',           // cards, panels
  surfaceAlt: '#FAFAF9',        // alternate rows, subtle hover fills
  surfaceRaised: '#FAFBFC',     // modals, dropdowns, badges (slight elevation)

  // Legacy navy aliases — preserved so existing callers keep working
  navy: '#0F172A',
  navyMid: '#1E293B',
  navyLight: '#334155',

  // Brand accent — orange (bold, action-oriented, AAA accessible)
  accent: '#EA580C',            // orange-600, 4.6:1 on white (AA+), vibrant
  accentMuted: '#92400E',       // copper/amber-800, warm — inactive clickable items
  accentLight: '#FFF7ED',       // orange-50, tinted backgrounds
  accentBorder: '#FDBA74',      // orange-300, soft brand borders
  accentText: '#9A3412',        // orange-800, high contrast on accentLight

  // Text hierarchy — text shifts warm; mid/muted/sub stay slate
  text: '#1C1917',              // stone-900, 17:1 on surface (AAA)
  textMid: '#334155',           // slate-700, 9.6:1 (AAA)
  textMuted: '#64748B',         // slate-500, 4.6:1 on bg (AA) — non-interactive labels
  textSub: '#94A3B8',           // slate-400, 3.2:1 — non-interactive hints
  textOnDark: '#FFFFFF',
  white: '#FFFFFF',

  // Surfaces & chrome
  card: '#FFFFFF',
  border: '#E7E5E4',            // stone-200, soft warm border

  // Chips / badges — slate, matches established hierarchy
  chipBg: '#F1F5F9',            // slate-100
  chipText: '#475569',          // slate-600, 7.5:1 on chipBg
  chipProdBg: '#FEE2E2',        // red-100
  chipProdText: '#991B1B',      // red-800, 7.5:1
  chipStagBg: '#FEF3C7',        // amber-100
  chipStagText: '#92400E',      // amber-800
  zombieBadgeBg: '#FEF2F2',      // red-50 (softer danger wash)
  zombieBadgeText: '#DC2626',    // red-600

  // Semantic status — clear, accessible
  error: '#DC2626',             // red-600
  success: '#059669',           // emerald-600
  warning: '#A16207',           // yellow-700 — distinct hue from orange brand;
                                // yellow-500 on white fails AA at 1.9:1, so we
                                // push darker.

  // Dashboard alert / status tokens (UI color system review, 2026-05-11).
  // Distinct from the generic error/warning above so callers signal "this is
  // a FinOps alert" rather than "this is a form-validation error".
  //
  // WCAG AA contrast on #FFFFFF surface (measured, not asserted):
  //   alertCritical  4.83:1  ✓ body + large
  //   alertWarning   3.19:1  ✓ large only — DO NOT use for body text (<24px,
  //                          or <18.66px bold). Currently used only on the
  //                          28px-bold Monthly Waste headline.
  //   statusOk       5.48:1  ✓ body + large (emerald-700 — green-600 #16A34A
  //                          fails body at 3.30:1, so we use the darker stop).
  alertCritical: '#DC2626',     // red-600  — waste-ratio bar
  alertWarning:  '#D97706',     // amber-600 — Monthly Waste headline (large only)
  statusOk:      '#047857',     // emerald-700 — onboarding ticks, +/- deltas

  // Data-viz sequential ramp (light theme). 5-bucket cyan/teal scale, dark
  // end = biggest waste. Bar fills are non-text content so the 3:1 AA bar
  // applies to the fill-vs-track contrast, not fill-vs-white. The track is
  // slate-100, deliberately dimmer than #fff so the lightest ramp stop is
  // still distinguishable.
  vizRamp: ['#0E7490', '#0891B2', '#06B6D4', '#22D3EE', '#7DD3FC'],
  track: '#F1F5F9',             // slate-100 — bar track for unfilled portion
};

// ─── Dark Theme ────────────────────────────────────────────────────────────────
// Palette philosophy
//   Neutral : Tailwind slate, darker end — clean, high-contrast, no blue cast.
//   Brand   : Orange shifts lighter (400) so it stays readable on dark bg.
//   Comfort : Text off-whites avoid pure #FFF; status colors desaturated.
//   Depth   : 4-level elevation ladder (bg → bgSecondary → surface → raised).
const darkTheme = {
  // Backgrounds — clear 4-level elevation ladder
  bg: '#0B1220',                // deepest layer (page)
  bgSecondary: '#111827',       // sidebars, grouped sections
  surface: '#182031',           // cards / panels — distinct from bg
  surfaceAlt: '#1F2A3D',        // alt rows, hover states
  surfaceRaised: '#2A3550',     // dropdowns, tooltips, modals

  // Legacy navy aliases
  navy: '#0B1220',
  navyMid: '#111827',
  navyLight: '#2A3550',

  // Brand accent — orange tuned for dark readability
  accent: '#FB923C',            // orange-400, 8:1 on bg (AAA)
  accentMuted: '#D97706',       // amber-600, softer — inactive clickable items
  accentLight: '#2D1200',       // orange-950 wash
  accentBorder: '#7C3200',      // orange-800, visible border
  accentText: '#FBBF24',        // amber-400, 10:1 on accentLight

  // Text hierarchy — warm off-whites, 4 distinct levels
  text: '#E5ECF5',              // warm off-white, 15:1 on bg (AAA)
  textMid: '#B8C4D6',           // medium slate, 9:1 (AAA)
  textMuted: '#8497B2',         // clearly muted, 5.5:1 (AA)
  textSub: '#94A3B8',           // informational, 7:1 — still readable
  textOnDark: '#FFFFFF',
  white: '#FFFFFF',

  // Surfaces & chrome
  card: '#182031',
  border: '#2E3A52',            // visible but not harsh

  // Chips / badges — dark-context washes
  chipBg: '#263042',
  chipText: '#B8C4D6',          // 8:1 on chipBg
  chipProdBg: '#3B0D0D',        // deep red wash
  chipProdText: '#FCA5A5',      // red-300, soft coral
  chipStagBg: '#3A1D00',        // deep amber wash
  chipStagText: '#FCD34D',      // amber-300
  zombieBadgeBg: '#3B0F1A',      // deep rose wash
  zombieBadgeText: '#FDA4AF',    // rose-300

  // Semantic status — desaturated/lightened for dark-mode comfort
  error: '#F87171',             // red-400, soft coral
  success: '#34D399',           // emerald-400
  warning: '#FACC15',           // yellow-400, distinct hue from orange brand

  // Dashboard alert / status tokens (UI color system review, 2026-05-11).
  // Mirror the light-theme additions; FinOps-alert semantics are the same on
  // dark — readable against the deep-navy bg AND a hue distinct from brand
  // orange.
  //
  // alertWarning is amber-500, not amber-400. Walking up Tailwind's amber
  // scale drifts the hue toward yellow; amber-400 #FBBF24 (hue ~43°) reads
  // as "yellow warning" rather than amber and breaks visual continuity with
  // the light theme's amber-600 #D97706 (hue ~32°). amber-500 #F59E0B (hue
  // ~38°) sits between them — clearly amber, ~14° from brand orange
  // (orange-400 #FB923C, hue ~24°), AAA contrast (7.58:1) on dark surface.
  alertCritical: '#F87171',     // red-400, matches error — waste-ratio bar
  alertWarning:  '#F59E0B',     // amber-500 — Monthly Waste headline
  statusOk:      '#34D399',     // emerald-400 — onboarding completion ticks

  // Data-viz sequential ramp (dark theme). Lighter end = biggest waste so the
  // brightest stop pops on the dark page bg (inverse of light theme).
  vizRamp: ['#7DD3FC', '#38BDF8', '#0EA5E9', '#0891B2', '#155E75'],
  track: '#263042',             // matches chipBg — bar track on dark surfaces
};

const ThemeContext = createContext();

export function ThemeProvider({ children }) {
  // Sync init — `readSavedDark` is synchronous (localStorage + matchMedia
  // both are) so the very first render already has the right theme. No
  // `isLoading` blank-screen gate, no cold-load flicker.
  const [isDark, setIsDark] = useState(readSavedDark);

  // Project the theme onto the root `color-scheme` so native UA controls
  // (date pickers, scrollbars, autofill chrome, focus rings) follow the app's
  // theme instead of the OS preference. Without this, Firefox renders the
  // <input type="date"> popup in OS dark mode even when the app is in light
  // mode, and Chrome's calendar-picker-indicator picks the wrong tint. The
  // inline boot script in index.html primes the same property before React
  // mounts to avoid a one-frame flash on cold load.
  useEffect(() => {
    document.documentElement.style.colorScheme = isDark ? 'dark' : 'light';
  }, [isDark]);

  const toggleTheme = () => {
    setIsDark((prev) => {
      const next = !prev;
      writeSavedDark(next);
      return next;
    });
  };

  return (
    <ThemeContext.Provider value={{ theme: isDark ? darkTheme : lightTheme, isDark, toggleTheme }}>
      {children}
    </ThemeContext.Provider>
  );
}

export function useTheme() {
  const context = useContext(ThemeContext);
  if (!context) throw new Error('useTheme must be used within ThemeProvider');
  return context;
}
