// Brand-color catalog for the dashboard's runtime palette switcher.
// Neutrals (bg / surface / text / border / chips / status) live in
// ThemeContext.jsx and are shared across palettes — only the five brand
// tokens (`accent`, `accentMuted`, `accentLight`, `accentBorder`,
// `accentText`) change per palette. Each entry is tuned so both modes
// clear WCAG AA on the surfaces they actually render against.

export const palettes = {
  orange: {
    id: 'orange',
    name: 'Orange',
    swatch: { light: '#EA580C', dark: '#FB923C' },
    light: {
      accent: '#EA580C',
      accentMuted: '#92400E',
      accentLight: '#FFF7ED',
      accentBorder: '#FDBA74',
      accentText: '#9A3412',
    },
    dark: {
      accent: '#FB923C',
      accentMuted: '#D97706',
      accentLight: '#2D1200',
      accentBorder: '#7C3200',
      accentText: '#FBBF24',
    },
  },
  indigo: {
    id: 'indigo',
    name: 'Indigo',
    swatch: { light: '#4F46E5', dark: '#818CF8' },
    light: {
      accent: '#4F46E5',
      accentMuted: '#4338CA',
      accentLight: '#EEF2FF',
      accentBorder: '#C7D2FE',
      accentText: '#3730A3',
    },
    dark: {
      accent: '#818CF8',
      accentMuted: '#6366F1',
      accentLight: '#1E1B4B',
      accentBorder: '#4338CA',
      accentText: '#C7D2FE',
    },
  },
  teal: {
    id: 'teal',
    name: 'Teal',
    swatch: { light: '#0D9488', dark: '#2DD4BF' },
    light: {
      accent: '#0D9488',
      accentMuted: '#115E59',
      accentLight: '#CCFBF1',
      accentBorder: '#99F6E4',
      accentText: '#115E59',
    },
    dark: {
      accent: '#2DD4BF',
      accentMuted: '#14B8A6',
      accentLight: '#042F2E',
      accentBorder: '#0F766E',
      accentText: '#99F6E4',
    },
  },
  emerald: {
    id: 'emerald',
    name: 'Emerald',
    swatch: { light: '#059669', dark: '#34D399' },
    light: {
      accent: '#059669',
      accentMuted: '#047857',
      accentLight: '#ECFDF5',
      accentBorder: '#A7F3D0',
      accentText: '#065F46',
    },
    dark: {
      accent: '#34D399',
      accentMuted: '#10B981',
      accentLight: '#022C22',
      accentBorder: '#047857',
      accentText: '#A7F3D0',
    },
  },
  violet: {
    id: 'violet',
    name: 'Violet',
    swatch: { light: '#7C3AED', dark: '#A78BFA' },
    light: {
      accent: '#7C3AED',
      accentMuted: '#6D28D9',
      accentLight: '#F5F3FF',
      accentBorder: '#DDD6FE',
      accentText: '#5B21B6',
    },
    dark: {
      accent: '#A78BFA',
      accentMuted: '#8B5CF6',
      accentLight: '#2E1065',
      accentBorder: '#6D28D9',
      accentText: '#DDD6FE',
    },
  },
  // Distinct from generic-SaaS indigo — deeper, crisper, "Bloomberg-modern".
  sapphire: {
    id: 'sapphire',
    name: 'Sapphire',
    swatch: { light: '#1D4ED8', dark: '#60A5FA' },
    light: {
      accent: '#1D4ED8',
      accentMuted: '#1E40AF',
      accentLight: '#EFF6FF',
      accentBorder: '#BFDBFE',
      accentText: '#1E3A8A',
    },
    dark: {
      accent: '#60A5FA',
      accentMuted: '#3B82F6',
      accentLight: '#172554',
      accentBorder: '#1E40AF',
      accentText: '#BFDBFE',
    },
  },
  // Cooler/sharper than teal — already CloudZero/Finout/Antimetal territory,
  // included for direct comparison.
  cyan: {
    id: 'cyan',
    name: 'Cyan',
    swatch: { light: '#0E7490', dark: '#22D3EE' },
    light: {
      accent: '#0E7490',
      accentMuted: '#155E75',
      accentLight: '#ECFEFF',
      accentBorder: '#A5F3FC',
      accentText: '#155E75',
    },
    dark: {
      accent: '#22D3EE',
      accentMuted: '#06B6D4',
      accentLight: '#083344',
      accentBorder: '#0E7490',
      accentText: '#A5F3FC',
    },
  },
  // Linear/Vercel/Mercury-style: brand is essentially monochrome. Color is
  // spent only on semantic state and chart accents.
  slatemono: {
    id: 'slatemono',
    name: 'Slate-mono',
    swatch: { light: '#1E293B', dark: '#E2E8F0' },
    light: {
      accent: '#1E293B',
      accentMuted: '#475569',
      accentLight: '#F1F5F9',
      accentBorder: '#CBD5E1',
      accentText: '#0F172A',
    },
    dark: {
      accent: '#E2E8F0',
      accentMuted: '#94A3B8',
      accentLight: '#1F2937',
      accentBorder: '#334155',
      accentText: '#F1F5F9',
    },
  },
  // Deeper than Vantage's bright violet; distinguishably "premium-purple"
  // without sliding into consumer-AI territory.
  plum: {
    id: 'plum',
    name: 'Plum',
    swatch: { light: '#6B21A8', dark: '#C084FC' },
    light: {
      accent: '#6B21A8',
      accentMuted: '#581C87',
      accentLight: '#FAF5FF',
      accentBorder: '#E9D5FF',
      accentText: '#4C1D95',
    },
    dark: {
      accent: '#C084FC',
      accentMuted: '#A855F7',
      accentLight: '#3B0764',
      accentBorder: '#7E22CE',
      accentText: '#E9D5FF',
    },
  },
};

export const PALETTE_IDS = Object.keys(palettes);
export const DEFAULT_PALETTE_ID = 'orange';
