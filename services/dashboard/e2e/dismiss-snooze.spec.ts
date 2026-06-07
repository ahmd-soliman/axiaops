import { test } from '@playwright/test';

// Dismiss / snooze lifecycle — the highest-value mutating journey.
//
// Flow (POST /dismissals → GET /dismissals → DELETE /dismissals/{id}):
//   1. From the workbench, select a zombie and Dismiss (or Snooze 7d) it.
//   2. It disappears from the active list and appears in the Hidden view.
//   3. Restore it from the Hidden view; it returns to the active list.
//
// MUTATING + SHARED SEED: this spec changes server state for the one seeded
// org. The suite runs workers:1 / fullyParallel:false (see playwright.config.ts)
// so it can't race other specs, and the flow restores what it dismissed so the
// seed stays clean for re-runs. Implement with that round-trip discipline.
//
// The UI is bulk-select driven (checkbox per row → BulkActionBar with
// Dismiss / Snooze 7d / Restore buttons, then a confirm modal) — see
// src/screens/OverviewScreen.jsx. No data-testids exist yet; either add stable
// testids to the row checkbox + bulk buttons, or pin role/text selectors here.
//
// TODO: implement the three steps below.

test.fixme('dismiss a zombie → it moves to Hidden → restore it back', async ({ page }) => {
  void page;
  // 1. await gotoSettled(page, '/account');
  //    select first zombie row checkbox; click Dismiss; confirm "Dismiss All".
  // 2. assert the row is gone from the active list;
  //    open the Hidden view; assert the row is present and marked "dismissed".
  // 3. select it in Hidden; click Restore; assert it returns to the active list.
});

test.fixme('snooze a zombie for 7 days → it shows as snoozed in Hidden', async ({ page }) => {
  void page;
  // Same as above but via "Snooze 7d"; assert the Hidden card shows "snoozed"
  // with a snoozed_until date; restore to clean up.
});
