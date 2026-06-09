import { test, expect } from '@playwright/test';
import { gotoSettled, expectNotNotFound, captureErrors } from './helpers';

// Cloud accounts — the connected-account list (`/settings/cloud-accounts`) and
// the per-account settings page (`/settings/cloud-accounts/:id`).

test.describe('cloud accounts', () => {
  test('accounts list loads without error', async ({ page }) => {
    const errors = await captureErrors(page, async () => {
      await gotoSettled(page, '/settings/cloud-accounts');
    });
    await expectNotNotFound(page);
    expect(errors, `cloud accounts list raised pageerror(s): ${errors.join(' | ')}`).toEqual([]);
  });

  test('legacy /cloud-accounts redirects into /settings/cloud-accounts', async ({ page }) => {
    await gotoSettled(page, '/cloud-accounts');
    await expect(page).toHaveURL(/\/settings\/cloud-accounts$/);
    await expectNotNotFound(page);
  });

  // Opening an account row → /settings/cloud-accounts/:id (account settings:
  // label, region, scan interval). GET /accounts populates the list.
  // TODO: click the first seeded account, assert the settings form renders.
  test.fixme('opens account settings from a seeded account row', async ({ page }) => {
    await gotoSettled(page, '/settings/cloud-accounts');
    // e.g. await page.getByRole('link', { name: /seed-account/i }).first().click();
    // await expect(page).toHaveURL(/\/settings\/cloud-accounts\/.+/);
    // await expectNotNotFound(page);
  });
});
