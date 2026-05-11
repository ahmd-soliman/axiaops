# UI Color System Review — Dashboard Overview

**Status:** Proposal / design critique
**Scope:** Light mode only (dark mode toggle to be removed — see §6)
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

## 6. Dead control: dark mode toggle

The moon icon in the top right is shown, but per the current product decision this is a **light-mode-only** product. Remove the toggle rather than leave a non-functional or half-supported control.

## 7. Bar styling

The bars in "Waste by Service" are ~3–4px tall, which makes the row feel anemic and hard to scan at distance. Recommend:

- Bar height: **6–8px** with 2px corner radius.
- Track color: `--color-neutral-100` (very subtle, so the filled portion does the work).
- Label/value typography: tabular numerals (`font-feature-settings: "tnum"`) so digits and decimals align column-wise.

## 8. Brand accent discipline

The AxiaOps logo uses an orange→purple gradient. That gradient is the brand. It should appear **only** on:

- The logo itself.
- Primary CTAs (`Connect account`, `Run scan`).
- Active nav state (currently a flat orange "Overview" underline — fine, keep it consistent).

It should NOT appear on data visualization. Data viz is a separate problem with separate rules (sequential ramps, semantic colors, neutral chrome).

---

## Recommended token system

Proposed CSS custom properties for the dashboard (`services/dashboard/src/styles/tokens.css` or equivalent):

```css
:root {
  /* Brand — logo, primary CTAs, active nav only */
  --color-brand-500: #f97316;          /* orange CTA */
  --color-brand-gradient: linear-gradient(135deg, #f97316, #a855f7);

  /* Semantic — alerts, status */
  --color-alert-critical: #dc2626;     /* red — waste ratio, anomalies */
  --color-alert-warning:  #f59e0b;     /* amber — monthly waste headline */
  --color-status-ok:      #16a34a;     /* green — completed onboarding */

  /* Data viz — sequential ramp, applied by rank */
  --viz-ramp-1: #0e7490;               /* darkest = biggest waste */
  --viz-ramp-2: #0891b2;
  --viz-ramp-3: #06b6d4;
  --viz-ramp-4: #22d3ee;
  --viz-ramp-5: #67e8f9;               /* lightest = smallest waste */

  /* Neutrals */
  --color-text-primary:   #0f172a;
  --color-text-secondary: #475569;
  --color-text-muted:     #94a3b8;
  --color-surface:        #ffffff;
  --color-surface-alt:    #f8fafc;
  --color-border:         #e2e8f0;
  --color-track:          #f1f5f9;     /* bar track */
}
```

The bar component then picks a ramp color by **rank**, not by service name:

```tsx
function rampColorByRank(rank: number, total: number): string {
  const ramps = [
    'var(--viz-ramp-1)', 'var(--viz-ramp-2)', 'var(--viz-ramp-3)',
    'var(--viz-ramp-4)', 'var(--viz-ramp-5)',
  ];
  const bucket = Math.min(
    ramps.length - 1,
    Math.floor((rank / total) * ramps.length),
  );
  return ramps[bucket];
}
```

This way EC2 at #1 is always the darkest, CloudWatch at #11 is always the lightest, and the eye reads magnitude from color saturation — same as it already reads it from bar length. Redundant encoding is good in data viz.

---

## Change checklist

- [ ] Define design tokens (see above) in `services/dashboard/src/styles/tokens.css`.
- [ ] Migrate "Waste by Service" bars + chips to `rampColorByRank()`.
- [ ] Remove per-service hardcoded colors from the legend chips at the bottom — they should match the bar above them, which now comes from rank, not service identity.
- [ ] Change the "Monthly Waste" headline color from raw orange to `--color-alert-warning`.
- [ ] Change the waste-ratio bar from raw red to `--color-alert-critical`.
- [ ] Recolor the "What's Next" panel: links → neutral text + `--color-brand-500` only on the active step's arrow; completed items → `--color-status-ok` check + `--color-text-muted` body, no strikethrough.
- [ ] Bump bar height to 6–8px, add 2px radius, apply `--color-track` to the unfilled portion.
- [ ] Apply `font-variant-numeric: tabular-nums` to all currency and percentage values.
- [ ] Remove the dark-mode toggle from the top-right of the header.
- [ ] Verify all proposed colors pass WCAG AA contrast against `--color-surface` (4.5:1 for body text, 3:1 for graphics).

## Out of scope (track separately)

- Trends / Costs / Cloud Accounts pages — same token system applies but each page needs its own review pass.
- Empty states and error states.
- Mobile / narrow-viewport layout.
- Dark mode (per current decision, dropped).

## Open questions

- Is the orange→purple gradient still the desired brand direction, or is there appetite to move to a single solid brand color? A solid color is easier to use consistently and is more common in B2B FinOps products.
- Should "What's Next" persist forever once all items are done, or auto-dismiss after the first successful scan?
- Confirm WCAG target: AA (default) or AAA (some enterprise procurement asks for AAA).
