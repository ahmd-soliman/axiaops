import { Outlet, useLocation } from 'react-router-dom';
import { useTheme } from '../../theme/ThemeContext';
import { useBreakpoint } from '../../components/primitives/useBreakpoint';

const STEPS = [
  { path: '/onboarding/invite', label: 'Invite members' },
  { path: '/onboarding/aws-account', label: 'Connect AWS' },
];

// OnboardingLayout — wizard chrome. Progress dots top, no sidebar, content
// below. The Skip button is rendered per-step (each step decides whether it
// is skippable). See docs/onboarding-wizard.md §8.
export default function OnboardingLayout() {
  const { isDark } = useTheme();
  const location = useLocation();
  const { isAtMost } = useBreakpoint();
  const isMobile = isAtMost('sm');
  const idx = STEPS.findIndex((s) => location.pathname.startsWith(s.path));
  const currentIdx = idx >= 0 ? idx : 0;

  const dotMuted = isDark ? 'rgba(255,255,255,0.18)' : '#e5e7eb';

  return (
    <div
      style={{
        minHeight: '100vh',
        backgroundColor: 'var(--color-bg)',
        display: 'flex',
        flexDirection: 'column',
        alignItems: 'center',
        // Desktop wants the wizard pinned ~80px below the navbar; phones
        // have far less vertical space (and no navbar at all on these
        // routes), so the same gap eats the area above the form.
        paddingTop: isMobile ? 32 : 80,
      }}
    >
      <div style={{ width: '100%', maxWidth: 600, padding: isMobile ? '0 16px' : '0 24px' }}>
        <div style={{ display: 'flex', alignItems: 'center', gap: 8, marginBottom: 8, justifyContent: 'center' }}>
          {STEPS.map((s, i) => (
            <div
              key={s.path}
              style={{
                width: 10,
                height: 10,
                borderRadius: '50%',
                backgroundColor: i <= currentIdx ? 'var(--color-accent)' : dotMuted,
                transition: 'background-color 200ms',
              }}
              aria-label={`Step ${i + 1}${i === currentIdx ? ' (current)' : ''}`}
            />
          ))}
        </div>
        <p style={{ textAlign: 'center', color: 'var(--color-text-muted)', fontSize: 12, marginBottom: 28, marginTop: 0 }}>
          Step {currentIdx + 1} of {STEPS.length} — {STEPS[currentIdx].label}
        </p>
        <Outlet />
      </div>
    </div>
  );
}
