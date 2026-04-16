import React, { createContext, useContext, useState, useEffect } from 'react';

// Simple storage fallback for web
const storage = {
  async getItem(key) {
    try {
      return localStorage.getItem(key);
    } catch {
      return null;
    }
  },
  async setItem(key, value) {
    try {
      localStorage.setItem(key, value);
    } catch {
      // Ignore errors
    }
  }
};

const lightTheme = {
  bg: '#F8FAFC',
  bgSecondary: '#F1F5F9',
  surface: '#FFFFFF',        // navbar, hero, cards
  surfaceAlt: '#F1F5F9',     // hero section, connect prompt
  surfaceRaised: '#E2E8F0',  // pills, toggle inactive
  navy: '#0F172A',           // always-dark (used for active toggle bg, etc.)
  navyMid: '#1E293B',
  navyLight: '#334155',
  accent: '#F97316',
  accentLight: '#FFF7ED',
  accentBorder: '#FDE68A',
  accentText: '#78350F',
  text: '#0F172A',
  textMid: '#1E293B',
  textMuted: '#475569',
  textSub: '#334155',
  textOnDark: '#FFFFFF',     // text on always-dark surfaces (active toggle)
  white: '#FFFFFF',
  card: '#FFFFFF',
  border: '#E2E8F0',
  chipBg: '#F1F5F9',
  chipText: '#475569',
  chipProdBg: '#FEF2F2',
  chipProdText: '#B91C1C',
  chipStagBg: '#FEFCE8',
  chipStagText: '#A16207',
  ghostBadgeBg: '#FEF2F2',
  ghostBadgeText: '#B91C1C',
  error: '#B91C1C',
  success: '#059669',
  warning: '#F59E0B',
};

const darkTheme = {
  bg: '#0F172A',
  bgSecondary: '#1E293B',
  surface: '#0F172A',
  surfaceAlt: '#1E293B',
  surfaceRaised: '#334155',
  navy: '#0F172A',
  navyMid: '#1E293B',
  navyLight: '#334155',
  accent: '#F97316',
  accentLight: '#2D1A0A',
  accentBorder: '#92400E',
  accentText: '#FED7AA',
  text: '#F8FAFC',
  textMid: '#CBD5E1',
  textMuted: '#94A3B8',
  textSub: '#94A3B8',
  textOnDark: '#FFFFFF',
  white: '#FFFFFF',
  card: '#1E293B',
  border: '#334155',
  chipBg: '#334155',
  chipText: '#CBD5E1',
  chipProdBg: '#450A0A',
  chipProdText: '#FCA5A5',
  chipStagBg: '#422006',
  chipStagText: '#FCD34D',
  ghostBadgeBg: '#450A0A',
  ghostBadgeText: '#FCA5A5',
  error: '#EF4444',
  success: '#10B981',
  warning: '#F59E0B',
};

const ThemeContext = createContext();

export function ThemeProvider({ children }) {
  const [isDark, setIsDark] = useState(true); // Dark is default
  const [isLoading, setIsLoading] = useState(true);

  useEffect(() => {
    loadTheme();
  }, []);

  const loadTheme = async () => {
    try {
      const saved = await storage.getItem('theme');
      if (saved) {
        setIsDark(saved === 'dark');
      }
    } catch (e) {
      // Ignore errors, use default
    } finally {
      setIsLoading(false);
    }
  };

  const toggleTheme = async () => {
    const newTheme = !isDark;
    setIsDark(newTheme);
    try {
      await storage.setItem('theme', newTheme ? 'dark' : 'light');
    } catch (e) {
      // Ignore storage errors
    }
  };

  const theme = isDark ? darkTheme : lightTheme;

  if (isLoading) {
    return null; // Or a loading spinner
  }

  return (
    <ThemeContext.Provider value={{ theme, isDark, toggleTheme }}>
      {children}
    </ThemeContext.Provider>
  );
}

export function useTheme() {
  const context = useContext(ThemeContext);
  if (!context) {
    throw new Error('useTheme must be used within ThemeProvider');
  }
  return context;
}