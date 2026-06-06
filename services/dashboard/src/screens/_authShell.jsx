// _authShell.jsx — shared card layout for the five pre-auth screens
// (NativeLogin, Bootstrap, AcceptInvite, OrgPicker, PasswordReset). Lives
// outside the screens index export so the underscore prefix signals
// "internal helper, not a route".
//
// Two consumption shapes:
//
//   * `authStyles` / `authColors` / `useAuthStyles()` — the original
//     dark-only palette. Four of the pre-auth screens still consume this
//     unchanged. The palette mirrors the dark tokens in
//     src/styles/tokens.css so the strangler transition stays visually
//     invisible.
//
//   * `useAuthTheme()` — theme-aware variant. ThemeProvider wraps the whole
//     app (src/main.jsx) and the boot script primes `data-theme` before
//     React mounts, so a pre-auth screen *can* follow light/dark like the
//     rest of the app. NativeLoginScreen uses this to host a light/dark
//     toggle on the sign-in card (parity with the admin console's login).

import { useBreakpoint } from '../components/primitives/useBreakpoint';
import { useTheme } from '../theme/ThemeContext';

// Dark palette — mirrors :root[data-theme="dark"] in tokens.css. `white` is
// the high-emphasis text colour; `onAccent` is text/spinner colour sitting on
// the orange button (white in BOTH themes — the button stays orange).
const darkColors = {
  bg: '#0B1220',
  navyMid: '#182031',
  accent: '#FB923C',
  textMuted: '#8497B2',
  white: '#FFFFFF',
  onAccent: '#FFFFFF',
  inputBg: '#0F1828',
  inputBorder: '#243049',
  errorBg: '#3B1A1F',
  errorText: '#FCA5A5',
  successBg: '#0F2D24',
  successText: '#86EFAC',
};

// Light palette — mirrors the default :root block in tokens.css. Same keys as
// darkColors so buildAuthStyles() is palette-agnostic.
const lightColors = {
  bg: '#FAFAF9',
  navyMid: '#FFFFFF',
  accent: '#EA580C',
  textMuted: '#64748B',
  white: '#1C1917',
  onAccent: '#FFFFFF',
  inputBg: '#FAFAF9',
  inputBorder: '#E7E5E4',
  errorBg: '#FEE2E2',
  errorText: '#991B1B',
  successBg: '#ECFDF5',
  successText: '#047857',
};

export const authColors = darkColors;

// buildAuthStyles — derive the style object from a palette so light/dark share
// one source of truth. `button`/`buttonDisabled` keep white text via `onAccent`
// (the orange button reads white in both themes); `title`/`input` use `white`
// (high-emphasis text, which flips dark on the light palette).
function buildAuthStyles(C) {
  return {
    container: {
      flex: 1,
      minHeight: '100vh',
      backgroundColor: C.bg,
      display: 'flex',
      alignItems: 'center',
      justifyContent: 'center',
      padding: 24,
    },
    card: {
      width: '100%',
      maxWidth: 440,
      backgroundColor: C.navyMid,
      borderRadius: 16,
      padding: 40,
      display: 'flex',
      flexDirection: 'column',
      alignItems: 'stretch',
      gap: 14,
    },
    logo: {
      fontSize: 28,
      fontWeight: 800,
      color: C.accent,
      letterSpacing: -1,
      textAlign: 'center',
      marginBottom: 4,
    },
    // SVG lockup variant. Callers pick the light/dark file by theme.
    // Centered via auto margins; height locks proportions.
    logoImg: {
      height: 88,
      width: 'auto',
      display: 'block',
      margin: '0 auto 12px',
    },
    title: {
      fontSize: 18,
      fontWeight: 700,
      color: C.white,
      textAlign: 'center',
    },
    tagline: {
      fontSize: 14,
      color: C.textMuted,
      textAlign: 'center',
      lineHeight: '20px',
      marginBottom: 8,
    },
    label: {
      fontSize: 12,
      color: C.textMuted,
      fontWeight: 600,
      letterSpacing: 0.4,
      textTransform: 'uppercase',
    },
    input: {
      backgroundColor: C.inputBg,
      border: `1px solid ${C.inputBorder}`,
      borderRadius: 8,
      color: C.white,
      fontSize: 14,
      padding: '10px 12px',
      width: '100%',
      boxSizing: 'border-box',
      fontFamily: 'inherit',
    },
    inputMono: {
      fontFamily: '"Geist Mono Variable", ui-monospace, SFMono-Regular, Menlo, monospace',
      fontSize: 12,
    },
    button: {
      backgroundColor: C.accent,
      borderRadius: 10,
      padding: '12px 24px',
      width: '100%',
      border: 'none',
      cursor: 'pointer',
      color: C.onAccent,
      fontSize: 16,
      fontWeight: 700,
      display: 'flex',
      alignItems: 'center',
      justifyContent: 'center',
      marginTop: 8,
    },
    buttonDisabled: { opacity: 0.6, cursor: 'not-allowed' },
    errorBox: {
      backgroundColor: C.errorBg,
      color: C.errorText,
      borderRadius: 8,
      padding: '10px 12px',
      fontSize: 13,
      lineHeight: '18px',
    },
    successBox: {
      backgroundColor: C.successBg,
      color: C.successText,
      borderRadius: 8,
      padding: '10px 12px',
      fontSize: 13,
      lineHeight: '18px',
    },
    hint: {
      fontSize: 12,
      color: C.textMuted,
      textAlign: 'center',
      marginTop: 4,
      lineHeight: '16px',
    },
  };
}

export const authStyles = buildAuthStyles(darkColors);

// Apply the phone-friendly container/card padding overrides to a style object.
// Shared by useAuthStyles() and useAuthTheme() so the responsive shape stays
// identical regardless of which entry point a screen uses.
function withMobilePadding(styles) {
  return {
    ...styles,
    container: { ...styles.container, padding: 16 },
    card: { ...styles.card, padding: 24 },
    logoImg: { ...styles.logoImg, height: 64, margin: '0 auto 8px' },
  };
}

// useAuthStyles — dark-only styles with mobile-friendly container/card padding
// on phones. The static `authStyles` export above stays unchanged for any
// caller that wants the desktop-only shape; the four non-login pre-auth screens
// consume this hook so the card stops cramping its 247px-content shape on a
// 375px viewport (375 − 48 container − 80 card = 247). On phones the container
// drops to 16px each side and the card's 40px padding drops to 24, buying back
// ~48px of usable form width.
export function useAuthStyles() {
  const { isAtMost } = useBreakpoint();
  if (!isAtMost('xs')) return authStyles;
  return withMobilePadding(authStyles);
}

// useAuthTheme — theme-aware sibling of useAuthStyles. Returns the active
// palette + derived styles (light or dark, following the app theme) plus the
// `isDark` / `toggleTheme` handles a screen needs to render a light/dark
// toggle. The mobile padding override matches useAuthStyles exactly.
export function useAuthTheme() {
  const { isDark, toggleTheme } = useTheme();
  const { isAtMost } = useBreakpoint();
  const colors = isDark ? darkColors : lightColors;
  const styles = isAtMost('xs')
    ? withMobilePadding(buildAuthStyles(colors))
    : buildAuthStyles(colors);
  return { colors, styles, isDark, toggleTheme };
}
