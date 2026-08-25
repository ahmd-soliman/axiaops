# UI Performance & State Review — tenant dashboard

**Date:** 2026-06-11
**Scope:** `services/dashboard/src` (tenant dashboard, ~17.7k lines). Review
focused on performance and state-management defects plus minor easy wins —
not style. (Originally also covered `services/dashboard-admin` — that section
is removed along with the admin plane itself.)
**Method:** four parallel review passes (core infra / large screens /
settings+auth screens / shared components), every High and Medium finding
re-verified against the source before inclusion. Line numbers are as of
commit `5584ec19` (develop).

Severity legend:

- **High** — user-visible breakage or jank under normal use.
- **Medium** — real state/perf defect; felt under realistic data sizes or flows.
- **Low** — easy win; minor waste, dead code, or fragility worth cleaning when touching the file.

---

## High

### H-1. `MobileSheet` releases the body scroll-lock and steals focus mid-interaction

`components/primitives/MobileSheet.jsx:58` (effect deps) + `components/MobileNav.jsx:47`.

The sheet's only effect — scroll lock, Escape listener, focus management —
has `[visible, onClose]` as its dependency array. `MobileNav` passes
`const close = () => setOpen(false)`, a new function identity on every
render. Any re-render of `MobileNav` while the sheet is open (e.g.
`setBusy(true)` at the start of an org switch, a `me` refresh, a theme
toggle) runs the previous effect's cleanup: body scroll is unlocked, focus
jumps back to the hamburger button, and the keydown listener is removed —
then the effect re-runs and re-locks. The user sees focus flicker and can
scroll the page behind the sheet during the org-switch busy window.

**Fix:** in `MobileNav`, wrap `close` in `useCallback`; or in `MobileSheet`,
keep `onClose` in a ref and depend only on `[visible]`.

### H-2. `AreaChart` recomputes all geometry on every mousemove

`components/AreaChart.jsx:79–134`.

`handleMouseMove` calls `setHoverIdx` per pointer event. Every resulting
render recomputes `values`, `rawMax`/`rawMin` (spread over N points),
`points`, `linePath`/`areaPath` string joins, `yTicks`, the
`monthBoundaries` loop (one `new Date` per data point), and `xLabels` —
even though none of these depend on `hoverIdx`. On the 1-year trend view
(365 points) this is hundreds of allocations and date-parses at
pointer-move rate, on both the Trends and Cost Analytics screens. This is
the single biggest interaction-jank source in the app.

**Fix:** wrap the geometry block in one `useMemo` keyed on
`[data, measuredWidth]`; leave only the hover crosshair/tooltip derivation
in the render body.

---

## Medium

### M-1. `OverviewScreen` rebuilds and re-sorts the full resource list on every render

`screens/OverviewScreen.jsx:1490–1522` (the `listData` IIFE), plus the
`visibleIds` / `allSelected` / `zombieSelectedItems` derivations at
`1527–1535` and the `activeFilters` array at `~1436`.

The whole filter+search+sort pipeline (multiple `.filter()` chains and
`sortResources`, which copies and sorts) runs unmemoized in the render
body. Every unrelated state change — selecting a row, opening the bulk
bar, typing in search (per keystroke, on the *unfiltered* full list) —
re-runs the pipeline. With a few hundred resources this is felt as input
lag in the search box. The snoozed-view sort also constructs two `Date`
objects per comparison (`:1501`).

**Fix:** wrap `listData` in `useMemo` keyed on the filter inputs
(`resources.data`, `dismissals.data`, `dismissedSet`, `zombieOnly`,
`showDismissed`, `hiddenFilter`, `filterSvcs`, `filterResourceTypes`,
`filterOwner`, `search`, `sortBy`); derive the selection helpers from the
memoized value; pre-compute `getTime()` before the snoozed sort.

### M-2. Every `useWindowWidth`/`useBreakpoint` call-site is its own unthrottled resize listener

`components/primitives/useWindowWidth.js:4–9`.

Each hook instance attaches its own `window` resize listener that calls
`setWidth` on every event. ~12 components use these hooks concurrently, so
a window drag produces ~12 × 60 Hz state updates and re-renders across the
tree (which also re-runs M-1, M-4, etc. since those are unmemoized).

**Fix:** back the hook with a single module-level subscription
(`useSyncExternalStore` or a shared listener + rAF coalescing) so all
consumers share one DOM read per frame.

### M-3. `Overlay` has no Escape handling, no scroll lock, no focus containment

`components/primitives/Overlay.jsx` (whole file, 19 lines).

`Overlay` backs `DestructiveConfirm` (delete account / delete org) and
`AccountSelector`. Escape does not dismiss, the page behind stays
scrollable, and Tab walks into the background page. `MobileSheet` already
implements all three correctly and is the template.

**Fix:** add a `visible`-gated effect: body scroll lock, Escape→`onClose`,
initial focus into the dialog; restore on cleanup.

### M-4. `TrendScreen` derives all chart/list data unmemoized, and the scroll effect uses a stale list

`screens/TrendScreen.jsx:236–245, 287–293, 315`.

- `filterBuckets` is rebuilt every render and fed to `useQueries`
  (`:247`), forcing React Query to re-evaluate the query array each time.
- `filteredSnaps` (a `new Date(...)` per snapshot), `chartSnaps`
  (`aggregateToDays`/`downsampleByMonth`), and `reversedSnaps` (copy +
  reverse) all run per render with no `useMemo`.
- The scroll-to-selected effect (`:303–315`) reads `reversedSnaps` but
  suppresses it from the deps (`eslint-disable-line`), so after a period
  or filter change it can index into a stale list and scroll to the wrong
  row. Adding the dep is safe — the effect writes no state except the
  intentional `setListPage`.

**Fix:** `useMemo` for `filterBuckets` (`[filterServices,
filterResourceTypes]`) and for the snaps pipeline (`[mergedSnaps,
period, effectiveGranularity]`, computing the `Date.now()` cutoff inside);
add `reversedSnaps` to the scroll effect deps.

### M-5. `CostAnalyticsScreen` keeps the service filter across account switches

`screens/CostAnalyticsScreen.jsx:322–325`.

The account-change effect resets `selectedService` and
`filterResourceTypes` but not `filterServices`. A service selected on
account A silently filters account B's records; if B lacks that service
the chip for it doesn't even render (chips derive from the new account's
`allServices`), so the user sees an empty chart with no visible active
filter. Recoverable only via the "All Services" chip.

**Fix:** add `setFilterServices(new Set())` to the same effect.

### M-6. `Members` role dropdowns fire concurrent PATCHes with no pending guard

`pages/settings/Members.jsx:556` (desktop) and `:615` (mobile).

`onChange={(e) => updateMutation.mutate({ id: m.id, role: e.target.value })}`
with no per-row disable. Rapid changes fire overlapping PATCHes; the
`onSuccess → invalidate` chain of an intermediate response can flip the
displayed role mid-flight. Same screen, `~:63–143`: the invite form's
submit button is only disabled by `addMutation.isPending`, which flips
after dispatch — a double Enter can create two invitations for one email.

**Fix:** disable the select while `updateMutation.isPending` (or per-row
busy state, as `Integrations.jsx` already does with `busyIds`); add an
immediate submitted-guard on the invite form.

### M-7. SSO `GroupMappings` editor wipes unsaved edits after save-triggered refetch

`pages/settings/sso/GroupMappings.jsx:85–93`.

The editor hydrates local `rows` from `mappings.data` in an effect keyed on
the query data reference. After Save → `invalidateQueries` → background
refetch, the new array reference re-fires the effect and overwrites `rows`,
discarding anything the user typed between pressing Save and the refetch
resolving (a real window on slow connections).

**Fix:** hydrate once (mount / first success) or guard re-hydration behind
a dirty flag.

### M-8. `LicenseBanner` sticks 12px short of the navbar

`components/LicenseBanner.jsx:91`.

`position: sticky; top: 52` against an AppShell header that is
`--navbar-height: 64px` (`components/AppShell.jsx:66`). On scroll the
banner slides under the header by 12px before pinning. The adjacent
comment ("height: 52") is stale. Self-hosted builds only — SaaS hides the
banner — but it's a one-line fix.

**Fix:** `top: 'var(--navbar-height)'`; update the comment.

### M-9. `ToastContext` auto-dismiss timers are untracked

`context/ToastContext.jsx:65–67`.

`setTimeout(() => setToasts(...), duration)` stores no timer ID: nothing
cancels timers on provider unmount, and `dismiss` can't cancel a pending
auto-dismiss (a manually dismissed-and-recreated toast can be removed
early by the orphaned timer of its predecessor).

**Fix:** keep an id→timerId map in a ref; clear on dismiss and on unmount.

---

## Low / easy wins

| # | Location | Issue | Fix |
|---|----------|-------|-----|
| L-1 | `App.jsx:203–210` | 503-redirect listener is removed/re-added on every navigation because `location.pathname` is a dep (only needed for the in-handler guard). No missed-event window (cleanup+setup are contiguous), just churn. | Track the path in a ref; depend on `[navigate]`. |
| L-2 | `App.jsx:63` | `parseJwt(getToken() ?? '')` runs localStorage read + base64 decode every render; token never changes after mount (always null under native auth). | Compute once via `useMemo`/`useState` initializer. |
| L-3 | `context/AppContext.jsx:9`, `theme/ThemeContext.jsx:80`, `hooks/useScanStatus.js:57–88` | Context values / returned callbacks recreated per render (`{orgName, onLogout}`, `{isDark, toggleTheme}`, `watch`), fanning re-renders to all consumers when the provider re-renders. | `useCallback` + `useMemo` the values. |
| L-4 | `screens/PasswordResetScreen.jsx:56–57` | Post-success `setTimeout(() => navigate(...), delay)` never cleared; fires `navigate` on an unmounted screen if the user navigates first. | Store the timer; clear in effect cleanup. |
| L-5 | `pages/settings/Organization.jsx:42–44` | `useState(currentName)` never resyncs after rename+`refresh()` while mounted; dirty-check drifts. | Sync from prop when not dirty, or key the section by org. |
| L-6 | `screens/AuditScreen.jsx:355` | `pages.flatMap(...)` over the full cursor-paginated result on every render. | `useMemo([query.data])`. |
| L-7 | `screens/OrgSummaryScreen.jsx:118` | `Object.entries(...).sort(...)` per render. | `useMemo([summary.data])`. |
| L-8 | `components/primitives/Toast.jsx` + `primitives/index.js:16` | Dead code — the `Toast` primitive is exported but never imported anywhere (real toasts live in `ToastContext`). | Delete the file and the export. |
| L-9 | `components/primitives/MobileSheet.jsx:110–116` | `@keyframes` `<style>` tag rendered inside the sheet JSX — re-inserted into the document on every open. | Hoist to module-load injection like `Toast.jsx`/global CSS. |
| L-10 | `components/AccountSelector.jsx:24–46`, `components/DateRangeChips.jsx:77–88` | Static style objects / pure helpers rebuilt per render. | Hoist to module scope. |
| L-11 | `screens/TrendScreen.jsx:647` | History row key `item.snapshot_at + idx` — the `idx` suffix is redundant post-aggregation and masks duplicates instead of surfacing them. | Key on `item.snapshot_at`. |
| L-12 | `screens/DetailScreen.jsx:179–214` | Restore-from-conflict calls `fetchDismissals` directly, bypassing the query cache the parent already holds (duplicate network fetch). | Read from / invalidate via React Query. |
| L-13 | `context/ToastContext.jsx:65` | Toast IDs from `Date.now() + Math.random()`. | `crypto.randomUUID()` or a module counter. |
| L-14 | `components/onboarding/WhatsNextPanel.jsx:30–32` | `dismissed` initializer reads the per-user localStorage flag with `userID` possibly `''` (403-recovery edge); dismissed checklist can reappear. | Re-read the flag when `userID` transitions empty→real. |
| L-15 | `pages/settings/Integrations.jsx:168–169` | `ChannelModal` state-from-props is safe today only because `{editing && ...}` unmounts between edits. | Harden with `key={existing?.id ?? 'new'}`. |
| L-16 | `pages/settings/Members.jsx:167–177` | `removeCtrl.close()` self-reference inside its own `onSuccess` — works because the callback fires async, but fragile. | Have `useDestructiveConfirm` auto-close on success. |
| L-17 | `screens/CostAnalyticsScreen.jsx:165–167` | `new Date(r.period_start).toISOString().slice(...)` per record inside the (memoized) chart aggregation; records are ISO strings already. | `r.period_start.slice(0, 10)`. |

---

## Claims investigated and rejected

For the record, three findings surfaced during review did **not** hold up
and are excluded above:

1. *"`ChannelModal` shows stale data when editing a different channel"* —
   the modal is conditionally rendered (`{editing && ...}`) and unmounts on
   close, so state re-initializes per edit. Kept only as hardening note L-15.
2. *"503 events can be silently dropped during the `App.jsx` listener
   re-registration window"* — cleanup and re-setup run contiguously in the
   same effect flush; no event can be dispatched between them. Kept as
   churn-only L-1.
3. *"`ConnectScreen` validation `return` inside `try` skips
   `setLoading(false)`"* — `finally` runs on early `return`; no bug.

## Suggested fix order

1. **H-1 + H-2** — one-file fixes, immediately felt (mobile sheet, chart hover jank).
2. **M-1, M-2, M-4** — the memoization batch; M-2 first since resize churn amplifies the others.
3. **M-5, M-6, M-7** — state-correctness fixes (stale filter, double-submit, lost edits).
4. **M-3, M-8, M-9** — overlay behavior, banner offset, toast timers.
5. Low items opportunistically when touching the files; L-8 (dead code) any time.
