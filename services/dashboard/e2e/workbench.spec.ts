import { test, expect } from '@playwright/test';
import { gotoSettled, expectNotNotFound, captureErrors } from './helpers';

// Workbench / resources view (`/account`) — the per-account zombie workbench:
// the resource list, service/account filters, and the row → detail deep-link.

test.describe('workbench', () => {
  test('resources view loads without error', async ({ page }) => {
    const errors = await captureErrors(page, async () => {
      await gotoSettled(page, '/account');
    });
    await expectNotNotFound(page);
    expect(errors, `workbench raised pageerror(s): ${errors.join(' | ')}`).toEqual([]);
  });

  // Filtering re-queries /resources (and /zombies) with ?service / ?account_id.
  // TODO: confirm the filter control selectors against the running app, pick a
  // seeded service (e.g. AmazonEC2), apply it, and assert the list narrows.
  test.fixme('filters the resource list by service and by account', async ({ page }) => {
    await gotoSettled(page, '/account');
    // e.g. await page.getByRole('combobox', { name: /service/i }).selectOption('AmazonEC2');
    // e.g. await expect(page.getByRole('row')).toHaveCount(...);
  });

  // Clicking a zombie row navigates to /detail/:id (GET /resources lookup).
  // This is exactly the journey the original crawl caught 404'ing on Kinesis.
  // TODO: click the first zombie row and assert /detail/:id renders (not 404).
  test.fixme('opens a zombie detail from a workbench row', async ({ page }) => {
    await gotoSettled(page, '/account');
    // e.g. await page.getByRole('link').filter({ hasText: /i-/ }).first().click();
    // await expect(page).toHaveURL(/\/detail\//);
    // await expectNotNotFound(page);
  });
});
