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

"Waste by Service" uses ~11 distinct hues (orange / blue / teal / cyan / pink / purple / green / orange / red / orange / gray). That palette belongs to consumer dashboards where categories are unordered (Notion DB views, Linear project tags). Here the bars are **ordered by spend**, so the encoding should be sequential, not categorical.

**Why this matters:** rainbow palettes (a) have no perceptual ordering, so the eye can't read magnitude from color, (b) are not colorblind-safe, and (c) signal "playful product" rather than "audit-grade tool." Vantage, CloudHealth, Cloudability, and Apptio all use a small palette (one brand accent + neutrals + 2–3 semantic colors) for exactly this reason.

**Proposal:** one sequential blue or teal ramp where darker = bigger waste. Reserve red/orange entirely for alert semantics.

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
  alertWarning:  '#D97706',     // amber — monthly waste headline
  statusOk:      '#16A34A',     // green — completed onboarding

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
- [x] Migrate "Waste by Service" bars + dots to `rampColorByRank()`.
- [x] Per-service legend chips removed in favour of the rank-based encoding (the dot color now comes from rank, not service identity).
- [x] Change the "Monthly Waste" headline color from `theme.accent` to `theme.alertWarning`.
- [x] Change the waste-ratio bar bands to `theme.alertCritical` / `theme.alertWarning` / `theme.statusOk`.
- [x] Recolor the "What's Next" panel: pending items → neutral text + `theme.accent` only on the arrow; completed items → `theme.statusOk` check + `theme.textMuted` body, no strikethrough.
- [x] Bump "Waste by Service" bar height to 6px, 2px radius, apply `theme.track` to the unfilled portion.
- [x] Apply `font-variant-numeric: tabular-nums` to all currency and percentage values in the Overview hero + ServiceBreakdown.
- [ ] Verify all proposed colors pass WCAG AA contrast against `theme.surface` (4.5:1 for body text, 3:1 for graphics) in **both** themes — partly done by inspection (existing tokens carry AA/AAA notes); a tool-assisted audit pass is still owed.

## Out of scope (track separately)

- Trends / Costs / Cloud Accounts pages — same token system applies but each page needs its own review pass.
- Empty states and error states.
- Mobile / narrow-viewport layout.
- Dark-mode-specific posture work — the toggle stays. See the "Dark mode posture" section below for what's worth a follow-up pass.

## Dark mode posture

Dark mode is a fully-built, deliberately-maintained surface — the toggle stays. This pass also brought the surrounding plumbing up to current best practice:

- [x] **OS-preference default** — replaces the hard-coded `useState(true)`. Cold load with no stored choice resolves via `window.matchMedia('(prefers-color-scheme: dark)')`; a `matchMedia` listener keeps `isDark` in sync if the OS flips while the user is on `system`.
- [x] **Three-state preference** — `light` / `dark` / `system` stored under the `theme` localStorage key. AppShell (desktop) shows a three-icon segmented control; MobileNav (drawer) shows three labelled rows. Picking `system` re-delegates to the OS.
- [x] **FOUC fix** — provider is now synchronous (no `isLoading` blank-screen gate). An inline `<script>` in `index.html` primes `document.documentElement.style.colorScheme` before React mounts so native UA chrome (scrollbars, form controls, focus rings) renders in the right theme on the first paint. The inline script's resolution logic mirrors `ThemeContext.readPreference` + `getSystemDark` — keep them in sync.
- [ ] **CSS custom properties on `:root[data-theme=...]`** — deferred. Tokens are still JS objects distributed via React context. Moving to CSS variables would let descendants inherit theme values without re-rendering on toggle and let the inline boot script eliminate FOUC for React-rendered content too (currently it only handles UA chrome). Bigger refactor; track separately.

## Open questions

- Is the orange→purple gradient still the desired brand direction, or is there appetite to move to a single solid brand color? A solid color is easier to use consistently and is more common in B2B FinOps products.
- Should "What's Next" persist forever once all items are done, or auto-dismiss after the first successful scan?
- Confirm WCAG target: AA (default) or AAA (some enterprise procurement asks for AAA).
