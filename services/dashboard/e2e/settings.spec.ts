import { test, expect } from '@playwright/test';
import { gotoSettled, expectNotNotFound, captureErrors } from './helpers';

// Settings tabs — every tab under /settings must be reachable and render
// cleanly. These are largely covered by the link crawl too, but having them as
// explicit per-tab tests gives faster, named signal on a regression.

// /settings/license is intentionally absent: under the SaaS build the e2e stack
// runs (license.state="managed"), the License tab is hidden and the page redirects
// to /settings — there is no customer-facing license under SaaS. Covered by the
// dedicated redirect test below.
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

  // SaaS posture (the e2e build): there is no customer-facing License page —
  // a direct /settings/license deep-link bounces back to /settings.
  test('/settings/license redirects to /settings under SaaS', async ({ page }) => {
    await gotoSettled(page, '/settings/license');
    await expect(page).toHaveURL(/\/settings$/);
    await expectNotNotFound(page);
  });
});
