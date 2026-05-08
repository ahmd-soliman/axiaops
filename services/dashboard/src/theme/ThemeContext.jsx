import React, { createContext, useContext, useState, useEffect } from 'react';
import { palettes, PALETTE_IDS, DEFAULT_PALETTE_ID } from './palettes';

const storage = {
  async getItem(key) {
    try { return localStorage.getItem(key); } catch { return null; }
  },
  async setItem(key, value) {
    try { localStorage.setItem(key, value); } catch { /* ignore */ }
  },
};

// ─── Light Theme — neutrals only ──────────────────────────────────────────────
// Brand tokens (accent / accentMuted / accentLight / accentBorder / accentText)
// are merged in at runtime from the selected palette in `palettes.js`. Edit
// neutrals here; edit brand colors there.
//
// Minimal warm-shift: the four large-surface neutrals (bg, bgSecondary, text,
// border) move from Tailwind slate (cool) to stone (warm) for a softer,
// GitLab/Notion-leaning feel. Text mid/muted/sub and chip neutrals stay slate
// — at those tiers the cool/warm cast is imperceptible and slate retains the
// established hierarchy. Surfaces stay pure white. Dark mode is unchanged.
const lightBase = {
  // Backgrounds — 4-level hierarchy (page → section → card → raised)
  bg: '#FAFAF9',                // page background, stone-50 (warm off-white)
  bgSecondary: '#F5F5F4',       // sidebars, headers, grouped sections (stone-100)
  surface: '#FFFFFF',           // cards, panels
  surfaceAlt: '#FAFAF9',        // alternate rows, subtle hover fills (matches bg)
  surfaceRaised: '#FAFBFC',     // modals, dropdowns, badges (slight elevation)

  // Legacy navy aliases — preserved so existing callers keep working
  navy: '#0F172A',
  navyMid: '#1E293B',
  navyLight: '#334155',

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
  chipStagText: '#92400E',      // amber-800, stronger
  zombieBadgeBg: '#FEF2F2',      // red-50 (softer danger wash)
  zombieBadgeText: '#DC2626',    // red-600 (more vibrant alert)

  // Semantic status — clear, accessible
  error: '#DC2626',             // red-600
  success: '#059669',           // emerald-600 (more professional than green)
  warning: '#A16207',           // yellow-700 — distinct hue from orange brand,
                                // pushed darker than yellow-500 because yellow
                                // on white is famously low-contrast (1.9:1 AA fail)
};

// ─── Dark Theme — neutrals only ───────────────────────────────────────────────
const darkBase = {
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
};

function buildTheme(isDark, paletteId) {
  const base = isDark ? darkBase : lightBase;
  const palette = palettes[paletteId] || palettes[DEFAULT_PALETTE_ID];
  const brand = isDark ? palette.dark : palette.light;
  return { ...base, ...brand };
}

const ThemeContext = createContext();

export function ThemeProvider({ children }) {
  const [isDark, setIsDark] = useState(true);
  const [paletteId, setPaletteIdState] = useState(DEFAULT_PALETTE_ID);
  const [isLoading, setIsLoading] = useState(true);

  useEffect(() => {
    Promise.all([storage.getItem('theme'), storage.getItem('palette')]).then(
      ([savedTheme, savedPalette]) => {
        if (savedTheme) setIsDark(savedTheme === 'dark');
        if (savedPalette && PALETTE_IDS.includes(savedPalette)) {
          setPaletteIdState(savedPalette);
        }
        setIsLoading(false);
      },
    );
  }, []);

  const toggleTheme = async () => {
    const newTheme = !isDark;
    setIsDark(newTheme);
    await storage.setItem('theme', newTheme ? 'dark' : 'light');
  };

  const setPaletteId = async (id) => {
    if (!PALETTE_IDS.includes(id)) return;
    setPaletteIdState(id);
    await storage.setItem('palette', id);
  };

  if (isLoading) return null;

  const theme = buildTheme(isDark, paletteId);

  return (
    <ThemeContext.Provider
      value={{
        theme,
        isDark,
        toggleTheme,
        paletteId,
        setPaletteId,
        palettes,
      }}
    >
      {children}
    </ThemeContext.Provider>
  );
}

export function useTheme() {
  const context = useContext(ThemeContext);
  if (!context) throw new Error('useTheme must be used within ThemeProvider');
  return context;
}
