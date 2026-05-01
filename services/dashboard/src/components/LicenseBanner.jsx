import { useQuery } from '@tanstack/react-query';
import { useTheme } from '../theme/ThemeContext';
import { useMe } from '../context/MeContext';
import { fetchVersion } from '../api/client';

// LicenseBanner renders a top-of-page nag for the self-hosted license
// lifecycle (Phase B1.6 slice 8). Shape decisions:
//
//  • Owners only. Licensing is a billing/contract concern; non-owners can't
//    act on it and the banner would just be noise. Same gating shape as
//    OnboardingGate, which also keys on role.
//
//  • Reads the same React Query cache key (['api-version']) AppShell uses,
//    so this component does NOT trigger a second /v1/version request — it
//    piggy-backs on AppShell's already-staleTime:Infinity query.
//
//  • Three rendered states, mapped from the API's license.state +
//    license.days_remaining (slice 6 response shape):
//      · expired                      → red. Scans are paused.
//      · in_grace                     → amber. Grace clock ticking down.
//      · valid && days_remaining < 14 → amber. Lead-time nag.
//    Anything else (valid + ≥14 days, not_loaded, query loading/errored,
//    user not owner) renders nothing — silent in the happy path.
//
//  • days_remaining semantics: per services/shared/license/license.go's
//    DaysRemaining(), this is whole days from now until exp + grace_period
//    (the hard cutoff). NOT days until exp. That's why the in_grace copy
//    says "until scans pause" — the same number is what the scan-gate
//    classifier flips on.
const LEAD_TIME_DAYS = 14;

export default function LicenseBanner() {
  const { theme } = useTheme();
  const { role } = useMe();
  const t = theme;

  // Same key + same staleTime as AppShell — React Query dedupes, so this is
  // a cache read, not a network call.
  const { data } = useQuery({
    queryKey: ['api-version'],
    queryFn: fetchVersion,
    staleTime: Infinity,
    gcTime: Infinity,
    retry: false,
  });

  if (role !== 'owner') return null;

  const lic = data?.license;
  if (!lic) return null;

  const tone = severity(lic);
  if (!tone) return null;

  const palette = paletteFor(tone, t);
  const message = messageFor(lic);

  return (
    <div
      role="alert"
      style={{
        // Sticky under the AppShell header (which is sticky at top: 0 with
        // height: 52). Without this, the banner scrolls out of view on long
        // pages and the user loses the renewal context exactly when they're
        // most likely to click the (now-403ing) Scan Now button. z-index
        // sits below the header (100) so the header always wins on overlap.
        position: 'sticky',
        top: 52,
        zIndex: 99,
        backgroundColor: palette.bg,
        borderBottom: `1px solid ${palette.border}`,
        padding: '10px 16px',
        display: 'flex',
        alignItems: 'center',
        gap: 10,
        flexShrink: 0,
      }}
    >
      <span aria-hidden style={{ fontSize: 16, lineHeight: 1, color: palette.fg }}>
        {tone === 'error' ? '⛔' : '⚠️'}
      </span>
      <span style={{ fontSize: 13, color: palette.fg, lineHeight: '18px', flex: 1 }}>
        {message}
        {' '}
        <a
          href="mailto:sales@axiaops.io"
          style={{ color: palette.fg, textDecoration: 'underline', fontWeight: 600 }}
        >
          sales@axiaops.io
        </a>
        {' to renew.'}
      </span>
    </div>
  );
}

// severity returns 'error' | 'warning' | null. Pure — no theme knowledge,
// just the policy decision.
function severity(lic) {
  if (lic.state === 'expired') return 'error';
  if (lic.state === 'in_grace') return 'warning';
  if (lic.state === 'valid' && typeof lic.days_remaining === 'number' && lic.days_remaining < LEAD_TIME_DAYS) {
    return 'warning';
  }
  return null;
}

function paletteFor(tone, t) {
  if (tone === 'error') {
    return { bg: `${t.error}18`, border: `${t.error}40`, fg: t.error };
  }
  return { bg: `${t.warning}18`, border: `${t.warning}40`, fg: t.warning };
}

function messageFor(lic) {
  if (lic.state === 'expired') {
    return 'License past grace period — scans are paused. Contact';
  }
  const days = lic.days_remaining;
  if (lic.state === 'in_grace') {
    if (typeof days !== 'number') return 'License in grace period — scans will pause soon. Contact';
    if (days <= 0) return 'License grace period ending today — scans will pause. Contact';
    if (days === 1) return 'License in grace period — 1 day until scans pause. Contact';
    return `License in grace period — ${days} days until scans pause. Contact`;
  }
  // valid && days_remaining < 14. Note: days_remaining is until the hard
  // cutoff (exp + grace), so a "valid" state with days <= 0 only happens in a
  // narrow race between the boot-time classifier and our render — copy stays
  // unambiguous about what the customer sees: scans pausing imminently.
  if (typeof days !== 'number') return 'License expires soon — scans will pause. Contact';
  if (days <= 0) return 'License expires today — scans will pause. Contact';
  if (days === 1) return 'License expires soon — 1 day until scans pause. Contact';
  return `License expires soon — ${days} days until scans pause. Contact`;
}
