import { createContext, useCallback, useContext, useEffect, useMemo, useState } from 'react';

// Lightweight theme state for the admin console. The palette (light + dark)
// lives in tokens.css under :root / :root[data-theme="dark"]; this just toggles
// the data-theme attribute on <html> and persists the choice. public/theme-boot.js
// applies the same resolution synchronously pre-React to avoid a first-paint flash.
const STORAGE_KEY = 'theme';
const ThemeContext = createContext(null);

function readSavedDark() {
  try {
    const saved = localStorage.getItem(STORAGE_KEY);
    if (saved === 'dark') return true;
    if (saved === 'light') return false;
  } catch {
    /* ignore */
  }
  return window.matchMedia?.('(prefers-color-scheme: dark)').matches ?? false;
}

export function ThemeProvider({ children }) {
  const [isDark, setIsDark] = useState(readSavedDark);

  useEffect(() => {
    const root = document.documentElement;
    root.dataset.theme = isDark ? 'dark' : 'light';
    root.style.colorScheme = isDark ? 'dark' : 'light';
    try {
      localStorage.setItem(STORAGE_KEY, isDark ? 'dark' : 'light');
    } catch {
      /* ignore */
    }
  }, [isDark]);

  const toggleTheme = useCallback(() => setIsDark((d) => !d), []);
  const value = useMemo(() => ({ isDark, toggleTheme }), [isDark, toggleTheme]);
  return <ThemeContext.Provider value={value}>{children}</ThemeContext.Provider>;
}

// useTheme returns the theme state. Tolerant of being called outside a provider
// (returns a no-op light default) so leaf screens can read it in isolation —
// notably in unit tests that render a single screen without the app shell.
export function useTheme() {
  return useContext(ThemeContext) ?? { isDark: false, toggleTheme: () => {} };
}
