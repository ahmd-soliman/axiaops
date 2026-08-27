import { test, expect } from '@playwright/test';

// Auth-lifecycle specs — run in the `no-auth` project (no shared session), AFTER
// the setup project has consumed bootstrap. These exercise posture that the
// DEV_MODE suite could never see. See docs/e2e-conventions.md.
//
// More to add here over time (login/logout, /select-org, switch-org, invite
// redeem, password reset) — see the backlog in docs/e2e-conventions.md.

test.describe('post-bootstrap auth posture', () => {
  test('bootstrap is sealed — a newcomer at /bootstrap is redirected to /login', async ({ page }) => {
    // No stored session (this project carries none) and the setup project has
    // already consumed bootstrap, so /auth/bootstrap/state → available:false and
    // BootstrapScreen redirects to /login instead of showing the now-409 form.
    await page.goto('/bootstrap');
    await page.waitForURL('**/login', { timeout: 30_000 });
    await expect(page.locator('#bs-token')).toHaveCount(0);
  });

  test('an unauthenticated deep link is bounced to /login (AuthGuard)', async ({ page }) => {
    // A protected route with no session must not render — AuthGuard sends it to
    // /login. (Lane A / DEV_MODE bypassed this entirely.)
    await page.goto('/zombies');
    await page.waitForURL('**/login', { timeout: 30_000 });
  });
});
