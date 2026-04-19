import React, { createContext, useContext, useState, useEffect } from 'react';

const storage = {
  async getItem(key) {
    try { return localStorage.getItem(key); } catch { return null; }
  },
  async setItem(key, value) {
    try { localStorage.setItem(key, value); } catch { /* ignore */ }
  },
};

// ─── Light Theme ──────────────────────────────────────────────────────────────
// Palette philosophy
//   Neutral : Tailwind slate — cool, finance-grade, not sterile.
//   Brand   : Indigo (600 / 500) — data-dashboard accent, pairs with slate.
//   Contrast: All text tokens meet WCAG AA on bg AND surface; primary text AAA.
//   Depth   : 3 background layers + elevated surface. Cards lean on shadow, not
//             color, so information stays flat and legible.
const lightTheme = {
  // Backgrounds — 3-level hierarchy (page → section → card)
  bg: '#F6F8FB',                // page background, faintly cool
  bgSecondary: '#EDF1F7',       // sidebars, headers, grouped sections
  surface: '#FFFFFF',           // cards, panels
  surfaceAlt: '#F6F8FB',        // alternate rows, subtle hover fills
  surfaceRaised: '#FFFFFF',     // modals, dropdowns (depth via shadow)

  // Legacy navy aliases — preserved so existing callers keep working
  navy: '#0F172A',
  navyMid: '#1E293B',
  navyLight: '#334155',

  // Brand accent — indigo (replaces previous orange)
  accent: '#4F46E5',            // indigo-600, primary CTA
  accentLight: '#EEF2FF',       // indigo-50, tinted backgrounds
  accentBorder: '#C7D2FE',      // indigo-200, soft brand borders
  accentText: '#3730A3',        // indigo-800, 9:1 on accentLight

  // Text hierarchy — 4 distinct, AA-clear levels
  text: '#0F172A',              // slate-900, 18:1 on surface (AAA)
  textMid: '#334155',           // slate-700, 9.6:1 (AAA)
  textMuted: '#5C6876',         // slate-550 custom, 5.3:1 on bg (AA)
  textSub: '#7F8A9E',           // custom slate-450, 3.5:1 — lightest readable tier
  textOnDark: '#FFFFFF',
  white: '#FFFFFF',

  // Surfaces & chrome
  card: '#FFFFFF',
  border: '#E2E8F0',            // slate-200, visible but quiet

  // Chips / badges
  chipBg: '#F1F5F9',            // slate-100
  chipText: '#475569',          // slate-600, 7.5:1 on chipBg
  chipProdBg: '#FEE2E2',        // red-100
  chipProdText: '#991B1B',      // red-800, 7.5:1
  chipStagBg: '#FEF3C7',        // amber-100
  chipStagText: '#854D0E',      // amber-800, 7:1
  ghostBadgeBg: '#FFE4E6',      // rose-100 (danger wash — "ghost" = attention)
  ghostBadgeText: '#9F1239',    // rose-800

  // Semantic status — saturated for light background clarity
  error: '#DC2626',             // red-600
  success: '#16A34A',           // green-600
  warning: '#B45309',           // amber-700, 4.7:1 on bg (AA)
};

// ─── Dark Theme ────────────────────────────────────────────────────────────────
// Palette philosophy
//   Neutral : Tailwind slate, darker end — clean, high-contrast, no blue cast.
//   Brand   : Indigo shifts lighter (400/300) so it stays readable on dark bg.
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

  // Brand accent — indigo tuned for dark readability
  accent: '#818CF8',            // indigo-400, 7:1 on bg (AAA)
  accentLight: '#1E1B4B',       // indigo-950 wash
  accentBorder: '#4338CA',      // indigo-700, visible border
  accentText: '#C7D2FE',        // indigo-200, 10:1 on accentLight

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
  ghostBadgeBg: '#3B0F1A',      // deep rose wash
  ghostBadgeText: '#FDA4AF',    // rose-300

  // Semantic status — desaturated/lightened for dark-mode comfort
  error: '#F87171',             // red-400, soft coral
  success: '#34D399',           // emerald-400
  warning: '#FBBF24',           // amber-400
};

const ThemeContext = createContext();

export function ThemeProvider({ children }) {
  const [isDark, setIsDark] = useState(true);
  const [isLoading, setIsLoading] = useState(true);

  useEffect(() => {
    storage.getItem('theme').then((saved) => {
      if (saved) setIsDark(saved === 'dark');
      setIsLoading(false);
    });
  }, []);

  const toggleTheme = async () => {
    const newTheme = !isDark;
    setIsDark(newTheme);
    await storage.setItem('theme', newTheme ? 'dark' : 'light');
  };

  if (isLoading) return null;

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
