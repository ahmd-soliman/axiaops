import { test, expect } from '@playwright/test';
import { gotoSettled, expectNotNotFound, captureErrors } from './helpers';

// Org summary (`/`) — the landing screen. With seeded data it renders savings
// tiles and a per-account / per-service breakdown.

test.describe('org summary', () => {
  test('renders without error and is not the NotFound page', async ({ page }) => {
    const errors = await captureErrors(page, async () => {
      await gotoSettled(page, '/');
    });
    await expectNotNotFound(page);
    expect(errors, `org summary raised pageerror(s): ${errors.join(' | ')}`).toEqual([]);
    // The summary always renders the org name / app chrome — body has content.
    await expect(page.locator('body')).not.toBeEmpty();
  });

  // The seed populates potential-savings figures and a per-account rollup
  // (GET /summary + /summary/by-account). Pin a stable visible marker once the
  // exact tile copy/structure is confirmed against the running seeded app.
  // TODO: replace with a concrete assertion on a savings tile + an account row.
  test.fixme('shows savings tiles and the per-account waste rollup from seeded data', async ({ page }) => {
    await gotoSettled(page, '/');
    // e.g. await expect(page.getByText(/potential monthly savings/i)).toBeVisible();
    // e.g. await expect(page.getByRole('link', { name: /seed-account/i }).first()).toBeVisible();
  });
});
