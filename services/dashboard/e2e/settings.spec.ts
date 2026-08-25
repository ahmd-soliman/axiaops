import { test, expect } from '@playwright/test';
import { gotoSettled, expectNotNotFound, captureErrors } from './helpers';

// Settings tabs — every tab under /settings must be reachable and render
// cleanly. These are largely covered by the link crawl too, but having them as
// explicit per-tab tests gives faster, named signal on a regression.

const SETTINGS_TABS = [
  '/settings/profile',
  '/settings/cloud-accounts',
  '/settings/members',
  '/settings/integrations',
  '/settings/audit',
  '/settings/sso',
  '/settings/organization',
];

test.describe('settings tabs', () => {
  for (const tab of SETTINGS_TABS) {
    test(`${tab} loads without error`, async ({ page }) => {
      const errors = await captureErrors(page, async () => {
        await gotoSettled(page, tab);
      });
      await expectNotNotFound(page);
      expect(errors, `${tab} raised pageerror(s): ${errors.join(' | ')}`).toEqual([]);
    });
  }

  test('/settings/team redirects to /settings/members', async ({ page }) => {
    await gotoSettled(page, '/settings/team');
    await expect(page).toHaveURL(/\/settings\/members$/);
    await expectNotNotFound(page);
  });
});
