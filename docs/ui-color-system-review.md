# UI Color System Review — Dashboard Overview

**Status:** Proposal / design critique
**Scope:** Light mode (dark mode is also supported; recommendations here apply to both unless noted)
**Surface reviewed:** Dashboard "Overview" page (Total Spend / Monthly Waste / Waste by Service)
**Author:** Design review pass, 2026-05-11

---

## TL;DR

The layout, typography, and information hierarchy are clean and professional. The **color system is the weakest part** and is what makes the dashboard read as "early-stage product" rather than "tool I trust with my cloud spend." Two changes do most of the work:

1. Replace the 11-color categorical palette on "Waste by Service" with a single sequential ramp.
2. Reserve red and orange exclusively for alert semantics (waste ratio, monthly waste headline, anomalies). Stop using orange as a service color, a CTA color, and an alert color simultaneously.

Estimated effort: ~half a day of design-token changes plus a sweep over the chart components.

---

## 1. What's working

- Nav hierarchy is correct: Overview / Trends / Costs / Cloud Accounts / Settings, with org switcher + user chip top-right.
- Typography is restrained and readable; the `USD 986.97` headline is appropriately dominant.
- The `35 zombies ▲ 13.6%` microcopy under "Monthly Waste" is exactly the context a FinOps buyer wants — change direction + magnitude in one glance.
- The `Zombies / All / Hidden (8)` segmented control at the bottom is a nice affordance.
- "Waste ratio" as a single 0–100% bar is a strong headline metric — better than a pie or donut.

## 2. The core problem: categorical rainbow on ordered data

"Waste by Service" uses ~11 distinct hues (orange / blue / teal / cyan / pink / purple / green / orange / red / orange / gray). That palette belongs to consumer dashboards where categories are unordered (Notion DB views, Linear project tags). Here the bars are **ordered by spend**, so the encoding should be sequential, not categorical — at least in theory.

**Why this matters:** rainbow palettes (a) have no perceptual ordering, so the eye can't read magnitude from color, (b) are not colorblind-safe, and (c) signal "playful product" rather than "audit-grade tool." Vantage, CloudHealth, Cloudability, and Apptio all use a small palette (one brand accent + neutrals + 2–3 semantic colors) for exactly this reason.

**Original proposal:** one sequential blue or teal ramp on the chart where darker = bigger waste. Reserve red/orange entirely for alert semantics.

**What we landed on instead:** the rank-based ramp was tried and reverted. Reason: it put the same service under different colors in different surfaces — EC2's chart bar was a cyan ramp stop, but EC2's row dot and filter pill were AWS-orange. That cross-surface inconsistency was a bigger usability problem than the original "rainbow on ordered data" concern. Decision: per-service color (`cfg.color`) is used on the chart bars too, so service identity is consistent everywhere. **Bar length** already encodes magnitude — the color isn't doing the magnitude job, so the "ramps for ordered data" rule doesn't apply once length is doing it. Rainbow on the chart is the cost of cross-surface identity consistency.

## 3. Semantic overload of orange

Orange is currently doing five jobs:

- The headline "Monthly Waste" number (alert).
- The EC2 service bar.
- The Lambda service bar.
- The Secrets service bar.
- The "What's Next" panel links / CTAs.

Because orange is also the alert color, every non-alert use dilutes its meaning. A user glancing at the screen cannot tell whether an orange element means "this is bad" or "this is EC2." Pick one job for orange (alert) and remove it from every other surface.

## 4. Red is overused

Red appears on:

- The waste-ratio bar (37.3%) — **correct, keep.** This is the headline pain.
- The ECR row in "Waste by Service" — **wrong.** ECR is $2.67, the smallest line item, but its red bar reads as "this is critical."

Red should only mark anomalies, threshold breaches, and the waste-ratio bar. Service rows should never be red regardless of position in the list.

## 5. "What's Next" floating panel

- Orange CTAs compete with the headline alert color (see §3).
- The strikethrough on completed items (`Connect AWS account`, `Run your first scan`) reads as "errors / removed" more than "done." Use a muted green check + gray (not strikethrough) text — strikethrough is a destructive affordance.
- Consider docking this into the page rather than floating it; floating panels imply transience but this is onboarding state that persists across visits until dismissed.

## 6. Bar styling

The bars in "Waste by Service" are ~3–4px tall, which makes the row feel anemic and hard to scan at distance. Recommend:

- Bar height: **6–8px** with 2px corner radius.
- Track color: `--color-neutral-100` (very subtle, so the filled portion does the work).
- Label/value typography: tabular numerals (`font-feature-settings: "tnum"`) so digits and decimals align column-wise.

## 7. Brand accent discipline

The AxiaOps logo uses an orange→purple gradient. That gradient is the brand. It should appear **only** on:

- The logo itself.
- Primary CTAs (`Connect account`, `Run scan`).
- Active nav state (currently a flat orange "Overview" underline — fine, keep it consistent).

It should NOT appear on data visualization. Data viz is a separate problem with separate rules (sequential ramps, semantic colors, neutral chrome).

---

## Recommended token system

The dashboard distributes design tokens as JS objects via `useTheme()` (a React context in `services/dashboard/src/theme/ThemeContext.jsx`) — there is no `tokens.css` and no Tailwind. The tokens below are added to **both** the light and dark `theme` objects in that file:

```js
// services/dashboard/src/theme/ThemeContext.jsx
const lightTheme = {
  // … existing tokens (accent, text, surface, border, error, success, warning) …

  // Dashboard alert / status tokens — distinct from generic error/warning so
  // callers signal "this is a FinOps alert" rather than "form validation".
  alertCritical: '#DC2626',     // red — waste ratio, anomalies
  alertWarning:  '#D97706',     // amber — monthly waste headline (large only)
  statusOk:      '#047857',     // emerald-700 — completed onboarding / +/- deltas

  // Data-viz sequential ramp, applied by rank. Light theme: dark teal end =
  // biggest waste; light cyan end = smallest. Dark theme has a complementary
  // inverted ramp where the brightest stop maps to biggest waste.
  vizRamp: ['#0E7490', '#0891B2', '#06B6D4', '#22D3EE', '#7DD3FC'],
  track:   '#F1F5F9',           // bar track — slate-100
};
```

The bar component picks a ramp color by **rank**, not by service name:

```jsx
function rampColorByRank(rank, total, vizRamp) {
  if (total <= vizRamp.length) return vizRamp[Math.min(rank, vizRamp.length - 1)];
  const idx = Math.min(vizRamp.length - 1, Math.floor((rank / total) * vizRamp.length));
  return vizRamp[idx];
}
```

This way EC2 at #1 is always the most prominent stop, CloudWatch at #11 is always the dimmest, and the eye reads magnitude from color saturation — same as it already reads it from bar length. Redundant encoding is good in data viz.

> **Why JS tokens instead of `tokens.css`?** The codebase is CSS-in-JS via React context — every component already reads colors via `useTheme().theme.x`. Introducing CSS custom properties alongside the existing JS object would force a parallel system. Migrating fully to `:root[data-theme]` + CSS variables is the long-term cleanup (see "Dark mode posture" §10 below) but is a larger refactor than this review's scope.

---

## Change checklist

- [x] Define design tokens on both `lightTheme` and `darkTheme` in `ThemeContext.jsx`.
- [~] ~~Migrate "Waste by Service" bars + dots to `rampColorByRank()`.~~ Reverted — see §2. Chart bars + dots stay on `cfg.color` for cross-surface identity consistency. Bar length carries magnitude.
- [x] Make "Waste by Service" rows clickable to toggle the service filter (`aria-pressed`, active state highlights with `accentLight` bg + `accentBorder`). The chart and the FilterPills row stay in sync via the shared `filterSvcs` state — same multi-select, two visual surfaces.
- [x] Add percentage-of-total-waste next to each row's cost (`$245.30 · 24.7%`) — tabular nums on both numbers so they align column-wise.
- [x] **Collapse the long tail into "Other".** Threshold: `savings < max(1% of total, $5)`. Guards: don't collapse if tail would be 0/1 services; always keep at least top 3 shown. The "Other" row is clickable: it expands inline (chevron flips ▸ → ▾) and renders each constituent service as a normal `ServiceBreakdownRow` indented beneath, with a left border to indicate nesting. Constituents share the same `filterSvcs` / `onToggleSvc` plumbing — clicking AWSGlue inside the expansion filters exactly the same as clicking a top-level row would. (Vantage uses the same expand-inline pattern; Cloudability uses click-toggles-all which has ambiguous semantics when the user has already individually selected one of the tail services.)
- [x] **Pareto 80% divider.** Walk the shown rows, accumulate `savings / totalSavings`, drop a dashed horizontal marker after the row where cumulative first crosses 80%. Label: "N% of waste above". Suppressed when the threshold would land at the very bottom of the chart (degenerate case).
- [ ] **Per-service ▲/▼ vs. last scan.** Deferred — needs backend changes. `/v1/trend` today returns either org-wide aggregates or single-service rollups; no batch shape that gives per-service savings per snapshot in one call. Frontend fan-out (N parallel `?service=` calls) is wasteful and racy. Tracked in [#89](https://gitlab.com/axiaops/axiaops/-/work_items/89).
- [x] Change the "Monthly Waste" headline color from `theme.accent` to `theme.alertWarning`.
- [x] Change the waste-ratio bar bands to `theme.alertCritical` / `theme.alertWarning` / `theme.statusOk`.
- [x] Recolor the "What's Next" panel: pending items → neutral text, decorative arrow in `theme.textMuted` (not brand accent — the row itself is the CTA, the arrow is just an affordance hint); completed items → `theme.statusOk` check + `theme.textMuted` body, no strikethrough.
- [x] Bump "Waste by Service" bar height to 6px, 2px radius, apply `theme.track` to the unfilled portion.
- [x] Apply `font-variant-numeric: tabular-nums` to all currency and percentage values in the Overview hero + ServiceBreakdown.
- [~] Mute `SERVICE_CONFIG.color` to a calmer palette — **tried and reverted**. A muted palette (S~35%, L~45%) compressed 21 hues into a narrower space and made services in the same hue family (the cool-blue cluster: EC2 / ELB / EKS / S3 / Glacier / CloudWatch) indistinguishable. The original saturated palette wins on the functional concern (telling services apart at a glance) even though it loses on the aesthetic concern (visual noise). AWS Console has shipped this pattern for 15 years; users navigate it fine. Decision: keep the original `SERVICE_CONFIG.color` values. The brand-orange / alert-amber discipline is achieved by ensuring **no surface outside `SERVICE_CONFIG` uses orange or amber** — so the EC2-orange dot next to the brand-orange button isn't a collision, it's a service ID next to a CTA.
- [x] Verify all dashboard alert / status / viz-ramp tokens pass WCAG AA contrast against `theme.surface` — see the audit table below. (`SERVICE_CONFIG` colors are not audited here — they're decorative non-text dots inherited from the prior design; if any specific service dot fails 3:1 visibility on either surface, log it as a follow-up.)

### WCAG contrast audit

Computed with the relative-luminance formula on the actual hex values, not by inspection. Targets: **4.5:1** body text, **3:1** large text (≥24px or ≥18.66px bold) and non-text content (bar fills, dots).

**Light theme — surface `#FFFFFF`, track `#F1F5F9`:**

| Token | Hex | Ratio | Verdict |
|---|---|---|---|
| `alertCritical` | `#DC2626` | 4.83:1 | ✓ AA body |
| `alertWarning` | `#D97706` | 3.19:1 | ✓ AA large only — restrict to ≥18.66px bold |
| `statusOk` | `#047857` | 5.48:1 | ✓ AA body (replaces green-600 `#16A34A` which was 3.30:1, large only) |
| `accent` (brand) | `#EA580C` | 3.56:1 | ✓ AA large / graphics |
| `vizRamp[0..4]` | (unused on chart — see §2) | — | retained as a token for future surfaces |

**Dark theme — surface `#182031`, track `#263042`:**

| Token | Hex | Ratio | Verdict |
|---|---|---|---|
| `alertCritical` | `#F87171` | 5.89:1 | ✓ AA body |
| `alertWarning` | `#F59E0B` | 7.58:1 | ✓ AAA — amber-500 (not amber-400; -400 hue drifts to yellow) |
| `statusOk` | `#34D399` | 8.47:1 | ✓ AAA |
| `accent` (brand) | `#FB923C` | 7.19:1 | ✓ AAA |
| `vizRamp[0..4]` | (unused on chart — see §2) | — | retained as a token for future surfaces |

**On `SERVICE_CONFIG.color`:** kept at the original (pre-review) saturated values — a muted palette was tried and rolled back; see the change checklist above for the reasoning. The dots are non-text decoration; per-service WCAG audit is not done here. If any specific service color reads as invisible on either surface in practice, log it as a follow-up and adjust that one entry — don't try to mute the whole palette again.

## Out of scope (track separately)

- Trends / Costs / Cloud Accounts pages — same token system applies but each page needs its own review pass.
- Empty states and error states.
- Mobile / narrow-viewport layout.
- Dark-mode-specific posture work — the toggle stays. See the "Dark mode posture" section below for what's worth a follow-up pass.

## Dark mode posture

Dark mode is a fully-built, deliberately-maintained surface — the toggle stays. This pass also brought the surrounding plumbing up to current best practice:

- [x] **Smart default, simple control.** Cold load with no stored preference resolves via `window.matchMedia('(prefers-color-scheme: dark)')` so the very first paint matches the user's OS. Once the user clicks the toggle their choice is persisted under the `theme` localStorage key (`light` or `dark`) and OS changes no longer flip it. AppShell + MobileNav both expose a single sun/moon toggle button — same control shape as before, smarter default. (A three-state `light` / `dark` / `system` picker was tried but felt heavier than the product needs; tracked in [#88](https://gitlab.com/axiaops/axiaops/-/work_items/88) if anyone wants to revisit alongside the CSS-vars migration.)
- [x] **FOUC fix** — provider is now synchronous (no `isLoading` blank-screen gate). An inline `<script>` in `index.html` primes `document.documentElement.style.colorScheme` before React mounts so native UA chrome (scrollbars, form controls, focus rings) renders in the right theme on the first paint. The inline script's resolution logic mirrors `ThemeContext.readSavedDark` — keep them in sync.
- [ ] **CSS custom properties on `:root[data-theme=...]`** — deferred. Tokens are still JS objects distributed via React context. Moving to CSS variables would let descendants inherit theme values without re-rendering on toggle and let the inline boot script eliminate FOUC for React-rendered content too (currently it only handles UA chrome). Bigger refactor; tracked in [#88](https://gitlab.com/axiaops/axiaops/-/work_items/88).

## Open questions

- Is the orange→purple gradient still the desired brand direction, or is there appetite to move to a single solid brand color? A solid color is easier to use consistently and is more common in B2B FinOps products.
- Should "What's Next" persist forever once all items are done, or auto-dismiss after the first successful scan?
- Confirm WCAG target: AA (default) or AAA (some enterprise procurement asks for AAA).
