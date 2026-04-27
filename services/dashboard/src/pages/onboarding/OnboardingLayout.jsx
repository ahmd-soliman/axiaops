import { Outlet, useLocation } from 'react-router-dom';
import { useTheme } from '../../theme/ThemeContext';

const STEPS = [
  { path: '/onboarding/org-name', label: 'Name your org' },
  { path: '/onboarding/invite', label: 'Invite teammates' },
  { path: '/onboarding/aws-account', label: 'Connect AWS' },
];

// OnboardingLayout — wizard chrome. Progress dots top, no sidebar, content
// below. The Skip button is rendered per-step (each step decides whether it
// is skippable). See docs/onboarding-wizard.md §8.
export default function OnboardingLayout() {
  const { theme: t, isDark } = useTheme();
  const location = useLocation();
  const idx = STEPS.findIndex((s) => location.pathname.startsWith(s.path));
  const currentIdx = idx >= 0 ? idx : 0;

  const dotMuted = isDark ? 'rgba(255,255,255,0.18)' : '#e5e7eb';

  return (
    <div
      style={{
        minHeight: '100vh',
        backgroundColor: t.bg,
        display: 'flex',
        flexDirection: 'column',
        alignItems: 'center',
        paddingTop: 80,
      }}
    >
      <div style={{ width: '100%', maxWidth: 600, padding: '0 24px' }}>
        <div style={{ display: 'flex', alignItems: 'center', gap: 8, marginBottom: 8, justifyContent: 'center' }}>
          {STEPS.map((s, i) => (
            <div
              key={s.path}
              style={{
                width: 10,
                height: 10,
                borderRadius: '50%',
                backgroundColor: i <= currentIdx ? t.accent : dotMuted,
                transition: 'background-color 200ms',
              }}
              aria-label={`Step ${i + 1}${i === currentIdx ? ' (current)' : ''}`}
            />
          ))}
        </div>
        <p style={{ textAlign: 'center', color: t.textMuted, fontSize: 12, marginBottom: 28, marginTop: 0 }}>
          Step {currentIdx + 1} of {STEPS.length} — {STEPS[currentIdx].label}
        </p>
        <Outlet />
      </div>
    </div>
  );
}
