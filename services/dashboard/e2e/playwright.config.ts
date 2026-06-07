import { defineConfig, devices } from '@playwright/test';

// Playwright config for the AxiaOps dashboard end-to-end regression suite.
//
// The suite runs against a *fully running stack* (postgres + api + ingestion
// in DEV_MODE + dashboard served by its nginx image) with seeded dummy data.
// In CI the stack is stood up by `make test-e2e` (test-infra/e2e/docker-
// compose.yml) and BASE_URL points at the compose `dashboard` service. Locally
// you can point it at `make start-dev` (http://localhost:5173) after `make seed`.
//
// DEV_MODE auth bypass means there is no login flow to script: the dashboard
// bakes VITE_DEV_MODE=true and the api/ingestion run with DEV_MODE=true, so
// every authenticated route is reachable directly. See docs/e2e-link-check-plan.md.

const BASE_URL = process.env.BASE_URL ?? 'http://localhost:5173';

export default defineConfig({
  testDir: '.',
  // One global timeout ceiling per test; the link crawl is the long pole and
  // bounds itself via MAX_PAGES, so 5 min is comfortable headroom.
  timeout: 5 * 60 * 1000,
  expect: { timeout: 15 * 1000 },

  // Never allow `test.only` to silently narrow the suite in CI.
  forbidOnly: !!process.env.CI,

  // Flakiness control: one retry. Combined with `trace: 'on-first-retry'` this
  // captures a full trace only when a test flaps, keeping green runs cheap.
  retries: 1,

  // The seeded data is a shared singleton (one org, one set of zombies). The
  // mutating flow specs (dismiss/snooze, account settings) would race each
  // other across workers, so we run serially. The read-only crawl tolerates
  // parallelism but isn't worth special-casing.
  workers: 1,
  fullyParallel: false,

  reporter: [
    ['list'],
    ['html', { outputFolder: 'playwright-report', open: 'never' }],
  ],

  use: {
    baseURL: BASE_URL,
    trace: 'on-first-retry',
    screenshot: 'only-on-failure',
    video: 'off',
    // Headless chromium is the only target — the suite checks route/link
    // health and critical journeys, not cross-browser rendering.
    actionTimeout: 15 * 1000,
    navigationTimeout: 30 * 1000,
  },

  projects: [
    {
      name: 'chromium',
      use: { ...devices['Desktop Chrome'] },
    },
  ],

  outputDir: 'test-results',
});
