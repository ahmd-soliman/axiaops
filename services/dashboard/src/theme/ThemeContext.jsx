import React, { createContext, useContext, useState, useEffect } from 'react';

const storage = {
  async getItem(key) {
    try { return localStorage.getItem(key); } catch { return null; }
  },
  async setItem(key, value) {
    try { localStorage.setItem(key, value); } catch { /* ignore */ }
  },
};

const lightTheme = {
  bg: '#F8FAFC',
  bgSecondary: '#F1F5F9',
  surface: '#FFFFFF',
  surfaceAlt: '#F1F5F9',
  surfaceRaised: '#E2E8F0',
  navy: '#0F172A',
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
  textOnDark: '#FFFFFF',
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
