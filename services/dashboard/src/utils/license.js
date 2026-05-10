// Frontend-only license-state helpers. The backend has no awareness of these
// thresholds — the API's CheckExpiry only emits valid / in_grace / expired /
// not_loaded, with transitions driven by the JWT exp + grace_period_days.
// The "renewal soon" lead-time nag is a dashboard UX add-on layered on top.
//
// Adjusting the lead time is a pure frontend change; no API alignment needed.

export const LICENSE_RENEWAL_NAG_DAYS = 14;

// shouldNagRenewal — true when the license is still valid but inside the
// lead-time window where the dashboard surfaces a renewal warning.
// Used by both LicenseBanner (global toast at the top of the dashboard)
// and the Settings → License page (chip tone + headline + detail copy)
// so the two surfaces can never disagree on the threshold.
export function shouldNagRenewal(lic) {
  return (
    lic?.state === 'valid' &&
    typeof lic.days_remaining === 'number' &&
    lic.days_remaining < LICENSE_RENEWAL_NAG_DAYS
  );
}
