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
      await gotoSettled(page, '/cost');
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
});
