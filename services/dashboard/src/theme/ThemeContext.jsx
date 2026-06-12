import React, { createContext, useContext, useState, useEffect, useCallback, useMemo } from 'react';

// Theme is a simple two-state preference (light or dark) stored under the
// 'theme' localStorage key. On cold load with no saved value we fall back to
// the OS preference (`prefers-color-scheme: dark`) — but only as the initial
// default; once the user clicks the toggle their choice is persisted and OS
// changes no longer flip the theme. This is the common shape (GitHub, Vercel,
// Linear, etc.) — explicit when the user has expressed a preference, smart
// when they haven't.
//
// Colour tokens are NOT distributed via this context. Issue #88 moved them
// to CSS custom properties on :root (see src/styles/tokens.css). Every
// component reads `var(--color-X)` directly in inline styles, which means:
//
//   1. Theme toggle = one DOM attribute write (`data-theme` on <html>), no
//      React re-renders for every leaf consumer.
//   2. The inline boot script in index.html sets the same attribute before
//      React mounts, so cold-load has zero FOUC.
//
// This context is preserved for the small set of branches that genuinely
// depend on the boolean in JS — logo swap (<img src={isDark ? darkLogo
// : lightLogo}>) and the theme picker control itself. `toggleTheme` is
// the imperative used by the AppShell button.
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

const ThemeContext = createContext();

export function ThemeProvider({ children }) {
  // Sync init — `readSavedDark` is synchronous (localStorage + matchMedia
  // both are) so the very first render already has the right theme. No
  // `isLoading` blank-screen gate, no cold-load flicker.
  const [isDark, setIsDark] = useState(readSavedDark);

  // Project the theme onto the <html> element as a data attribute. Two
  // jobs:
  //
  //   1. `data-theme="dark"` activates the dark-mode CSS-variable
  //      override block in src/styles/tokens.css. The cascade then
  //      propagates the new token values to every descendant — zero
  //      React re-renders required, since consumers read `var(--color-X)`
  //      strings, not the context value.
  //   2. `color-scheme` keeps native UA controls (date pickers,
  //      scrollbars, autofill chrome, focus rings) following the app's
  //      theme. Without it Firefox renders <input type="date"> popups in
  //      OS mode regardless of app mode.
  //
  // The inline boot script in index.html primes both properties before
  // React mounts to avoid a one-frame flash on cold load — keep both in
  // sync with that script.
  useEffect(() => {
    const root = document.documentElement;
    root.dataset.theme = isDark ? 'dark' : 'light';
    root.style.colorScheme = isDark ? 'dark' : 'light';
  }, [isDark]);

  const toggleTheme = useCallback(() => {
    setIsDark((prev) => {
      const next = !prev;
      writeSavedDark(next);
      return next;
    });
  }, []);

  // Stable value object so the (few) JS consumers of the boolean only
  // re-render when isDark actually flips, not on every provider render.
  const value = useMemo(() => ({ isDark, toggleTheme }), [isDark, toggleTheme]);

  return (
    <ThemeContext.Provider value={value}>
      {children}
    </ThemeContext.Provider>
  );
}

export function useTheme() {
  const context = useContext(ThemeContext);
  if (!context) throw new Error('useTheme must be used within ThemeProvider');
  return context;
}
