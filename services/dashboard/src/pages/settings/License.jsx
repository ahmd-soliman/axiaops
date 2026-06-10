import { useQuery } from '@tanstack/react-query';
import { useTheme } from '../../theme/ThemeContext';
import { fetchVersion } from '../../api/client';
import { shouldNagRenewal } from '../../utils/license';

// Settings → License — owner-only read-only inspector for the self-hosted
// license state (Phase B1.6 amendment + B1.7 follow-up). Complements
// LicenseBanner.jsx, which is deliberately silent on the happy path
// (state="valid" + days_remaining ≥ 14): the banner is a nag, this is the
// affirmative "what's loaded right now" surface. Operators reaching for
// "is the license OK?" via the dashboard land here.
//
// Owner-gating happens at the parent Settings.jsx tab filter via
// PERM.ORGANIZATION_DELETE (same gate as the Organization tab — license
// is a billing/contract concern parallel to org-level controls). The page
// itself does no in-page perm check; non-owners never reach this route.
//
// Reads the same React Query cache key `['api-version']` AppShell uses,
// so this component does NOT trigger a second /v1/version request — it
// piggy-backs on AppShell's already-staleTime:Infinity query. A "Refresh"
// button explicitly refetches when the operator wants the latest state
// (e.g. after a license renewal restart).
//
// State semantics map 1:1 to the api's licenseSummary() in handler.go:
//   valid       → green badge, claim sub-object rendered. In dev builds
//                 customer_id="axiaops-dev-fixture" identifies the embedded
//                 100-year dev license (B1.7 layer 4 / issue #75) — same
//                 chip copy as a real customer license, with the fixture
//                 identity visible in the claim sub-object below.
//   in_grace    → amber, claim sub-object + grace-period explainer
//   expired     → red, claim sub-object + renewal contact
//   not_loaded  → red, only reachable in production deployments without a
//                 license installed. If a regression ever did make this branch
//                 fire, we want the operator to see "scans are blocked" and
//                 investigate, not a misleading message.

const INSTALL_URL = 'https://axiaops.io/install';
const RENEWAL_EMAIL = 'sales@axiaops.io';

export default function License() {
  const { isDark } = useTheme();

  const { data, isLoading, isError, refetch, isFetching } = useQuery({
    queryKey: ['api-version'],
    queryFn: fetchVersion,
    staleTime: Infinity,
    gcTime: Infinity,
    retry: false,
  });

  return (
    <div style={{ padding: 24, color: 'var(--color-text-mid)' }}>
      <header style={{ display: 'flex', alignItems: 'baseline', justifyContent: 'space-between', marginBottom: 24 }}>
        <div>
          <h1 style={{ margin: 0, color: 'var(--color-text)', fontSize: 22, fontWeight: 700 }}>License</h1>
          <p style={{ marginTop: 4, marginBottom: 0, color: 'var(--color-text-muted)', fontSize: 13 }}>
            Self-hosted license state. Read-only — license issuance is operator-side via the offline issuance CLI.
          </p>
        </div>
        <button
          type="button"
          onClick={() => refetch()}
          disabled={isFetching}
          style={{
            padding: '6px 12px',
            border: `1px solid var(--color-border)`,
            borderRadius: 6,
            backgroundColor: 'transparent',
            color: 'var(--color-text)',
            fontSize: 12,
            cursor: isFetching ? 'wait' : 'pointer',
            opacity: isFetching ? 0.6 : 1,
          }}
        >
          {isFetching ? 'Refreshing…' : 'Refresh'}
        </button>
      </header>

      {isLoading && <p style={{ color: 'var(--color-text-muted)', fontSize: 13 }}>Loading…</p>}
      {isError && (
        <p style={{ color: 'var(--color-error)', fontSize: 13 }}>
          Could not load license state. Check the API health and retry.
        </p>
      )}
      {data?.license?.state === 'managed' ? (
        // SaaS (the `saashosted` build): there is no customer-facing license under
        // SaaS (design §7.4). The License tab is hidden in Settings.jsx; this
        // note is the defensive fallback for a direct /settings/license URL.
        // The plan/usage view (#131) is the eventual replacement surface.
        <p style={{ color: 'var(--color-text-muted)', fontSize: 13 }}>
          Your plan is managed by AxiaOps Cloud — there is no license to install or renew.
        </p>
      ) : (
        data?.license && <LicensePane lic={data.license} version={data} isDark={isDark} />
      )}
    </div>
  );
}

function LicensePane({ lic, version, isDark }) {
  const tone = toneFor(lic);
  const detail = detailFor(lic);
  const sectionBg = isDark ? 'rgba(255,255,255,0.03)' : '#fff';
  const border = isDark ? 'rgba(255,255,255,0.08)' : '#e5e7eb';

  return (
    <>
      {/* Status card: neutral surface and border, matching the cards below.
          Tone is carried by the Chip alone. */}
      <section
        style={{
          border: `1px solid ${border}`,
          borderRadius: 8,
          padding: 16,
          marginBottom: 16,
          backgroundColor: sectionBg,
        }}
      >
        <div style={{ display: 'flex', alignItems: 'center', gap: 10, marginBottom: detail ? 8 : 0 }}>
          <Chip tone={tone} label={chipLabel(lic)} />
          <span style={{ fontSize: 13, color: 'var(--color-text)', fontWeight: 600 }}>
            {headlineFor(lic)}
          </span>
        </div>
        {detail && (
          <p style={{ margin: 0, fontSize: 12, color: 'var(--color-text-mid)', lineHeight: '18px' }}>
            {detail}
          </p>
        )}
      </section>

      {hasClaims(lic) && (
        <section
          style={{
            border: `1px solid ${border}`,
            borderRadius: 8,
            padding: 16,
            marginBottom: 16,
            backgroundColor: sectionBg,
          }}
        >
          <h2 style={{ margin: 0, marginBottom: 12, fontSize: 14, fontWeight: 700, color: 'var(--color-text)' }}>
            License details
          </h2>
          <ClaimsGrid lic={lic} />
        </section>
      )}

      <section
        style={{
          border: `1px solid ${border}`,
          borderRadius: 8,
          padding: 16,
          backgroundColor: sectionBg,
        }}
      >
        <h2 style={{ margin: 0, marginBottom: 6, fontSize: 14, fontWeight: 700, color: 'var(--color-text)' }}>
          About AxiaOps
        </h2>
        {/* Customer-facing prod view is intentionally minimal — just the
            version, which is the single useful identifier for a support
            ticket. The "Service: api" row was always meta noise (which
            backend served the request — always the api service in this
            app). Commit + env are operator-only signals; they distract on
            a customer surface but are essential for debugging dev/staging,
            so they render only when env != production. */}
        <ClaimRow k="Version" v={version.version || '—'} />
        {version.env && version.env !== 'production' && (
          <>
            <ClaimRow k="Commit" v={version.commit || '—'} />
            <ClaimRow k="Env"    v={version.env} />
          </>
        )}
      </section>
    </>
  );
}

// hasClaims — true when the license sub-object carries the full claim set.
// Per handler.go's licenseSummary(): only state="not_loaded" omits the
// sub-claims; valid / in_grace / expired all carry customer_id, expires_at,
// days_remaining, max_organizations.
function hasClaims(lic) {
  return lic.state !== 'not_loaded';
}

function ClaimsGrid({ lic }) {
  // Two-column auto-fit grid handles tablet+ down to ~360px without a JS
  // breakpoint hook. Order: most-actionable first.
  return (
    <div style={{ display: 'grid', gridTemplateColumns: 'repeat(auto-fit, minmax(180px, 1fr))', gap: 12 }}>
      <ClaimRow k="Days remaining"  v={formatDays(lic.days_remaining)} />
      <ClaimRow k="Expires at"      v={formatDate(lic.expires_at)} mono />
      <ClaimRow k="Max organizations" v={lic.max_organizations ?? '—'} />
      <ClaimRow k="Customer ID"     v={lic.customer_id || '—'} mono />
    </div>
  );
}

function ClaimRow({ k, v, mono }) {
  return (
    <div style={{ display: 'flex', flexDirection: 'column', gap: 2, marginBottom: 8, minWidth: 0 }}>
      <span style={{ fontSize: 11, color: 'var(--color-text-muted)', textTransform: 'uppercase', letterSpacing: 0.4 }}>
        {k}
      </span>
      <span
        style={{
          fontSize: 13,
          color: 'var(--color-text)',
          fontFamily: mono ? '"Geist Mono Variable", ui-monospace, SFMono-Regular, Menlo, Consolas, monospace' : undefined,
          // Customer IDs are arbitrarily long; without break-all they
          // overflow the grid cell on phones.
          wordBreak: mono ? 'break-all' : undefined,
        }}
      >
        {v}
      </span>
    </div>
  );
}

function Chip({ tone, label }) {
  // Filled pill — primary affirmation for the License page state. Different
  // design context from list-row status chips (which are stripped to inline
  // text): this one sits at the top of a focused page, paired with a single
  // headline, so the heavier visual treatment earns its space.
  const palette = paletteFor(tone);
  return (
    <span
      style={{
        padding: '2px 8px',
        borderRadius: 999,
        fontSize: 11,
        fontWeight: 700,
        textTransform: 'uppercase',
        letterSpacing: 0.4,
        backgroundColor: palette.fg,
        color: 'var(--color-text-on-dark)',
      }}
    >
      {label}
    </span>
  );
}

// toneFor returns 'success' | 'warning' | 'error' | 'info'. Pure — no
// theme knowledge, just the policy decision. Mirrors LicenseBanner.jsx's
// severity() but with one extra 'success' bucket since this surface is
// affirmative ("license OK") not just nagging.
function toneFor(lic) {
  if (lic.state === 'valid') {
    if (shouldNagRenewal(lic)) return 'warning';
    return 'success';
  }
  if (lic.state === 'in_grace') return 'warning';
  if (lic.state === 'expired') return 'error';
  return 'error';
}

// paletteFor mirrors LicenseBanner.paletteFor: the pre-#88 shape produced
// alpha-suffixed hex strings (`${t.error}18`); CSS vars can't be string-
// concatenated like that, so color-mix interpolates against transparent
// instead. 9% / 25% reproduce the previous 0x18 / 0x40 alpha levels (8% /
// 25% rounded for the accent stop that used 0x14).
function paletteFor(tone) {
  const fg =
    tone === 'success' ? 'var(--color-success)' :
    tone === 'warning' ? 'var(--color-warning)' :
    tone === 'error'   ? 'var(--color-error)'   :
                         'var(--color-accent)';
  const bgPct = tone === 'success' || tone === 'warning' || tone === 'error' ? '9%' : '8%';
  return {
    bg:     `color-mix(in srgb, ${fg} ${bgPct}, transparent)`,
    border: `color-mix(in srgb, ${fg} 25%, transparent)`,
    fg,
  };
}

function chipLabel(lic) {
  if (lic.state === 'valid') return 'Valid';
  if (lic.state === 'in_grace') return 'In Grace';
  if (lic.state === 'expired') return 'Expired';
  return 'Not loaded';
}

function headlineFor(lic) {
  if (lic.state === 'valid') {
    return shouldNagRenewal(lic)
      ? 'License is active. Renewal due soon.'
      : 'License is active. Scans run normally.';
  }
  if (lic.state === 'in_grace') return 'License has expired and is in grace period.';
  if (lic.state === 'expired') return 'License past grace period — scans are blocked.';
  return 'No license installed — scans are blocked.';
}

function detailFor(lic) {
  if (lic.state === 'valid') {
    if (shouldNagRenewal(lic)) {
      return `Renewal will land via the issuance CLI; restart the API and ingestion services after dropping the new JWT in to pick it up. Contact ${RENEWAL_EMAIL} if a renewal hasn't been arranged.`;
    }
    return '';
  }
  if (lic.state === 'in_grace') {
    return `Reads, dashboard, and member-management remain available. New scans continue to run during the grace window. Contact ${RENEWAL_EMAIL} to renew before the grace period ends.`;
  }
  if (lic.state === 'expired') {
    return `Reads, dashboard, and member-management remain available — only POST /accounts/{id}/scan and the scheduled-scan ticker are gated. Contact ${RENEWAL_EMAIL} to renew; drop the new license in and restart the API + ingestion services.`;
  }
  // not_loaded or unknown state
  return `Install a license JWT to enable scans. The runbook lives in docs/license-issuance.md; the install URL is ${INSTALL_URL}.`;
}

function formatDate(iso) {
  if (!iso) return '—';
  try {
    const d = new Date(iso);
    if (isNaN(d.getTime())) return iso;
    return d.toISOString().slice(0, 10) + ' UTC';
  } catch {
    return iso;
  }
}

function formatDays(n) {
  if (typeof n !== 'number') return '—';
  if (n < 0) return `${n} (past hard cutoff)`;
  if (n === 1) return '1 day';
  return `${n} days`;
}
