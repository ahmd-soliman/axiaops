import { test, expect } from '@playwright/test';
import { gotoSettled, expectNotNotFound, captureErrors } from './helpers';

// Onboarding flow (`/onboarding` → invite / aws-account steps), gated by
// OnboardingGate (src/components/OnboardingGate.jsx).
//
// COMPLETED-ORG behaviour: the `setup` project completes onboarding right after
// bootstrap, so the org these specs run against has `onboarding_completed_at`
// set and OnboardingGate bounces any /onboarding* visit back to `/`. That
// redirect IS the contract for a completed org, so these tests assert it.
//
// The FRESH-org wizard case (the old fixme below) is now exercised in
// auth.setup.ts: right after bootstrap, before onboarding is completed, setup
// asserts the new owner is steered to /onboarding. So the wizard gate has
// coverage; the remaining fixme is the richer per-step assertion.

test.describe('onboarding', () => {
  test('completed org is redirected away from /onboarding to the dashboard', async ({ page }) => {
    const errors = await captureErrors(page, async () => {
      await gotoSettled(page, '/onboarding');
    });
    await expect(page).toHaveURL(/localhost(:\d+)?\/$|\/$/);
    await expectNotNotFound(page);
    expect(errors, `onboarding redirect raised pageerror(s): ${errors.join(' | ')}`).toEqual([]);
  });

  test('completed org is redirected away from the aws-account step too', async ({ page }) => {
    await gotoSettled(page, '/onboarding/aws-account');
    await expect(page).toHaveURL(/\/$/);
    await expectNotNotFound(page);
  });

  // Wizard steps for a FRESH org: needs an un-onboarded org fixture (an org
  // with onboarding_completed_at = NULL and an owner). Two ways to get there:
  // a dedicated seed variant, or a second compose org. Until then, fixme.
  // TODO: with a fresh-org fixture, assert /onboarding redirects to
  //   /onboarding/invite, both steps render, and the "What's next" panel on the
  //   org summary shows the expected next-step CTAs.
  test.fixme('fresh org sees the onboarding wizard and "What\'s next" panel', async ({ page }) => {
    void page;
  });
});
