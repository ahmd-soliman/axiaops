import { test, expect } from '@playwright/test';
import { gotoSettled, expectNotNotFound, captureErrors } from './helpers';

// Trend (`/trend`) and cost (`/cost`) screens. The seed populates 90 days of
// trend snapshots (GET /trend + /trend/services + /trend/resource-types) and
// cost records, so these charts render real data.

test.describe('trend & cost', () => {
  test('trend screen renders without error', async ({ page }) => {
    const errors = await captureErrors(page, async () => {
      await gotoSettled(page, '/trend');
    });
    await expectNotNotFound(page);
    expect(errors, `trend screen raised pageerror(s): ${errors.join(' | ')}`).toEqual([]);
  });

  test('cost screen renders without error', async ({ page }) => {
    const errors = await captureErrors(page, async () => {
      await gotoSettled(page, '/spend');
    });
    await expectNotNotFound(page);
    expect(errors, `cost screen raised pageerror(s): ${errors.join(' | ')}`).toEqual([]);
  });

  // The trend screen exposes service / resource-type selectors driven by
  // /trend/services and /trend/resource-types. With seeded data those should be
  // populated and switching them should re-render the chart.
  // TODO: assert at least one seeded service appears in the selector and that
  // selecting it updates the chart.
  test.fixme('trend service/resource-type selectors reflect seeded data', async ({ page }) => {
    await gotoSettled(page, '/trend');
  });

  // Regression for a bug where the DateRangeChips "Custom…" popover's date
  // draft was only ever recomputed at mount, on a manual date-input edit, or
  // when a Custom range was actually applied — never when the user merely
  // switched between preset chips (7d/30d/90d/6m/1y). Repro: pick 1y, open
  // Custom (draft happens to reflect 1y), Cancel, pick 6m, reopen Custom —
  // the draft stayed frozen on the 1y-derived dates instead of resetting to
  // 6m. Fixed by re-seeding the draft every time the popover opens, from
  // whichever window (preset or an already-applied custom range) is
  // currently active.
  test('cost screen Custom… draft reflects the currently active window, not a stale one', async ({ page }) => {
    await gotoSettled(page, '/spend');

    const group = page.getByRole('group', { name: 'Select time period' });
    // The popover trigger's accessible name changes once a custom range is
    // applied (it shows the formatted date range instead of "Custom…"), so
    // target it by its stable aria-haspopup attribute rather than by label.
    const customTrigger = group.locator('button[aria-haspopup="dialog"]');
    const fromInput = page.locator('input[type="date"]').first();
    const toInput = page.locator('input[type="date"]').nth(1);

    await group.getByRole('button', { name: '1y', exact: true }).click();
    await customTrigger.click();
    const draftFromAfter1y = await fromInput.inputValue();
    await page.getByRole('button', { name: 'Cancel' }).click();

    await group.getByRole('button', { name: '6m', exact: true }).click();
    await customTrigger.click();
    const draftFromAfter6m = await fromInput.inputValue();

    expect(
      draftFromAfter6m,
      'Custom… draft still showed the 1y window after switching to 6m — it should reset per preset',
    ).not.toBe(draftFromAfter1y);
    await page.getByRole('button', { name: 'Cancel' }).click();

    // Applying a real custom range, then switching to a preset, must also
    // reset the draft — not leave the applied custom dates behind.
    await customTrigger.click();
    await fromInput.fill('2026-06-01');
    await toInput.fill('2026-06-30');
    await page.getByRole('button', { name: 'Apply' }).click();

    await group.getByRole('button', { name: '7d', exact: true }).click();
    await customTrigger.click();
    const draftFromAfterAppliedCustomThen7d = await fromInput.inputValue();

    expect(
      draftFromAfterAppliedCustomThen7d,
      'Custom… draft still showed the applied 1–30 Jun range after switching to 7d',
    ).not.toBe('2026-06-01');
  });
});
