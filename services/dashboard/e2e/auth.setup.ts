import { test as setup, expect } from '@playwright/test';
import { execSync } from 'child_process';
import path from 'path';

// Setup project (runs before everything via project dependencies). It drives the
// REAL first-run /auth/bootstrap ceremony against a fresh, un-bootstrapped DB —
// which doubles as our bootstrap coverage AND provisions the logged-in session
// the rest of the suite reuses. See docs/e2e-conventions.md.
//
// The install token is injected into the api as BOOTSTRAP_INSTALL_TOKEN and given
// to this runner under the same env var, so we type the exact value the server
// expects.

const OWNER_FILE = path.join(__dirname, '.auth', 'owner.json');
const INSTALL_TOKEN = process.env.BOOTSTRAP_INSTALL_TOKEN ?? '';

const OWNER = {
  email: 'e2e-owner@axiaops.io',
  name: 'E2E Owner',
  org: 'E2E Org',
  password: 'e2e-bootstrap-pw-123', // ≥ 12 chars (BootstrapScreen rejects shorter)
};

setup('bootstrap the first owner and persist the session', async ({ page }) => {
  expect(
    INSTALL_TOKEN,
    'BOOTSTRAP_INSTALL_TOKEN must be set on the playwright service (must match the api)',
  ).not.toBe('');

  // Fresh install: no org exists, so App.jsx probes /auth/bootstrap/state and
  // bounces a newcomer from "/" to /bootstrap (Tasks.md row 2.7.16).
  await page.goto('/');
  await page.waitForURL('**/bootstrap', { timeout: 30_000 });

  // Single-use install form (selectors from BootstrapScreen.jsx).
  await page.locator('#bs-token').fill(INSTALL_TOKEN);
  await page.locator('#bs-org').fill(OWNER.org);
  await page.locator('#bs-name').fill(OWNER.name);
  await page.locator('#bs-email').fill(OWNER.email);
  await page.locator('#bs-password').fill(OWNER.password);
  await page.getByRole('button', { name: 'Create owner account' }).click();

  // Success: cookie set, BootstrapScreen navigates to "/". The new org has
  // onboarding_completed_at = NULL, so OnboardingGate steers an owner to the
  // wizard. Asserting we land on /onboarding both proves the bootstrap landed
  // authenticated AND exercises the fresh-org gate (the onboarding-wizard case
  // the old suite could only `fixme`).
  await page.waitForURL('**/onboarding', { timeout: 30_000 });
  await expect(page.locator('#bs-token')).toHaveCount(0);

  // Complete onboarding via the API (same call the wizard's "skip & finish"
  // makes) so the dependent flow specs can reach the dashboard instead of being
  // bounced back to the wizard by OnboardingGate. The session cookie is shared
  // with page.request, so this is authenticated as the owner.
  const done = await page.request.post('/api/v1/organizations/me/onboarding/complete', {
    data: { steps_skipped: ['invite', 'aws-account'] },
  });
  expect(done.ok(), `complete-onboarding failed: ${done.status()}`).toBeTruthy();

  // Seed fixture data into the just-bootstrapped org so the data-dependent specs
  // (zombies, accounts, trend) have something to assert. Zombies can't be
  // API-created (they come from scans), so we run the canonical SQL seeder
  // against the org bootstrap just made. --seed-existing-org targets the newest
  // org via MIGRATION_DATABASE_URL (set on the playwright service). See
  // docs/e2e-conventions.md §"seed-after-bootstrap".
  execSync('bash /repo/scripts/seed_test_data.sh --seed-existing-org', {
    stdio: 'inherit',
    env: process.env,
  });

  // Persist the owner's session for the dependent projects.
  await page.context().storageState({ path: OWNER_FILE });
});
