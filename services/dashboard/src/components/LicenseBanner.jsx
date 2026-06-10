import { useQuery } from '@tanstack/react-query';
import { useMe } from '../context/MeContext';
import { fetchVersion } from '../api/client';
import { shouldNagRenewal } from '../utils/license';

// LicenseBanner renders a top-of-page nag for the self-hosted license
// lifecycle (Phase B1.6 slice 8, B1.6 amendment). Shape decisions:
//
//  • Owners only. Licensing is a billing/contract concern; non-owners can't
//    act on it and the banner would just be noise. Same gating shape as
//    OnboardingGate, which also keys on role.
//
//  • Reads the same React Query cache key (['api-version']) AppShell uses,
//    so this component does NOT trigger a second /v1/version request — it
//    piggy-backs on AppShell's already-staleTime:Infinity query.
//
//  • Four rendered states, mapped from the API's license.state +
//    license.days_remaining (slice 6 response shape):
//      · expired                      → red. Scans paused, renewal CTA.
//      · not_loaded                   → red. Scans paused, install CTA.
//      · in_grace                     → amber. Grace clock ticking down.
//      · valid && days_remaining < 14 → amber. Lead-time nag.
//    Anything else (valid + ≥14 days, query loading/errored, user not
//    owner) renders nothing — silent in the happy path.
//
//  • not_loaded surfaces the install URL not the renewal contact: the
//    operator hasn't even tried yet, so "renew" is the wrong CTA. This
//    matches the api's distinct error-code shape (license_not_loaded vs
//    license_expired) — the dashboard reads the state, not the code.
//
//  • Three non-self-hosted-license states the banner stays silent for:
//    (a) DEV_MODE — post-B1.7-layer-4 the api emits state="valid" via the
//    embedded dev fixture, so severity() returns null. (b) SaaS (saashosted
//    build) — the api emits state="managed" and the explicit guard below
//    returns null (there is no customer-facing license under SaaS). (c) true
//    production-without-a-license — state="not_loaded", which DOES show (red,
//    install CTA). The VITE_DEV_MODE build-time bake below is a legacy belt-
//    and-braces for not_loaded in local dev; with the dev fixture it rarely
//    fires, but it's harmless.
//
//  • days_remaining semantics: per services/shared/license/license.go's
//    DaysRemaining(), this is whole days from now until exp + grace_period
//    (the hard cutoff). NOT days until exp. That's why the in_grace copy
//    says "until scans pause" — the same number is what the scan-gate
//    classifier flips on.
const INSTALL_URL = 'https://axiaops.io/install';
const IS_DEV_MODE = (import.meta.env?.VITE_DEV_MODE ?? 'false') === 'true';

export default function LicenseBanner() {
  const { role } = useMe();

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

  // SaaS (the `saashosted` build) reports state="managed" — there is no customer-
  // facing license under SaaS (design §7.4). Hide the banner entirely; the
  // plan/usage view replaces it (#131). severity() would already return null
  // for an unknown state, but this is the explicit, intent-revealing guard.
  if (lic.state === 'managed') return null;

  const tone = severity(lic);
  if (!tone) return null;

  const palette = paletteFor(tone);
  const message = messageFor(lic);

  const cta = ctaFor(lic);

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
          href={cta.href}
          {...(cta.external ? { target: '_blank', rel: 'noreferrer noopener' } : {})}
          style={{ color: palette.fg, textDecoration: 'underline', fontWeight: 600 }}
        >
          {cta.label}
        </a>
        {cta.suffix}
      </span>
    </div>
  );
}

// severity returns 'error' | 'warning' | null. Pure — no theme knowledge,
// just the policy decision.
function severity(lic) {
  if (lic.state === 'expired') return 'error';
  if (lic.state === 'not_loaded') {
    // DEV_MODE / SaaS also surface as not_loaded; the build-time bake of
    // VITE_DEV_MODE keeps the banner from firing in `npm run dev`.
    return IS_DEV_MODE ? null : 'error';
  }
  if (lic.state === 'in_grace') return 'warning';
  if (shouldNagRenewal(lic)) {
    return 'warning';
  }
  return null;
}

// color-mix is the CSS-variable-friendly substitute for the old
// "hex + 8-bit alpha suffix" pattern. The pre-#88 shape concatenated
// strings (e.g. `${t.error}18` → "#DC262618" → 9%-opaque red); CSS vars
// can't be string-concatenated like that, but color-mix(...) interpolates
// the resolved value against a transparent stop and gets the same result.
// Percentages 9% / 25% reproduce the previous 0x18 (24/255 ≈ 9%) and
// 0x40 (64/255 ≈ 25%) alpha levels.
function paletteFor(tone) {
  const fg = tone === 'error' ? 'var(--color-error)' : 'var(--color-warning)';
  return {
    bg:     `color-mix(in srgb, ${fg} 9%, transparent)`,
    border: `color-mix(in srgb, ${fg} 25%, transparent)`,
    fg,
  };
}

function messageFor(lic) {
  if (lic.state === 'expired') {
    return 'License past grace period — scans are paused. Contact';
  }
  if (lic.state === 'not_loaded') {
    return 'No license installed — scans are paused. Install a license at';
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

// ctaFor picks the call-to-action for the link element — install URL for
// not_loaded, sales mailto everywhere else. Kept pure so the JSX stays a
// single render path.
function ctaFor(lic) {
  if (lic.state === 'not_loaded') {
    return { href: INSTALL_URL, label: INSTALL_URL, external: true, suffix: '.' };
  }
  return { href: 'mailto:sales@axiaops.io', label: 'sales@axiaops.io', external: false, suffix: ' to renew.' };
}
