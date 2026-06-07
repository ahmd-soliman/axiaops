import { defineConfig, devices } from '@playwright/test';

// Playwright config for the AxiaOps dashboard end-to-end regression suite.
//
// AUTH-ON: the suite runs against a prod-like stack — postgres + api + ingestion
// with DEV_MODE=false + the dashboard's nginx image (no VITE_DEV_MODE) — i.e. the
// real config customers run, with real cookie auth. See docs/e2e-conventions.md.
//
// Session model (project-dependency pattern):
//   • the `setup` project (auth.setup.ts) drives the REAL /auth/bootstrap
//     ceremony, then saves the owner's session to e2e/.auth/owner.json.
//   • the `chromium` project depends on `setup` and loads that storageState, so
//     every read/journey spec runs logged-in without scripting login each time.
//   • the `no-auth` project depends on `setup` but uses NO stored session — for
//     auth-lifecycle specs (e.g. "bootstrap is sealed") that need a clean visitor.
//
// In CI the stack is stood up by `make test-e2e` (test-infra/e2e/docker-
// compose.yml) and BASE_URL points at the compose `dashboard` service. Locally:
// `make start-staging` (auth-on, dashboard on :8082) against a fresh DB.

const BASE_URL = process.env.BASE_URL ?? 'http://localhost:8082';

// Saved session from the `setup` project. Relative to this config's dir (e2e/),
// matching __dirname/.auth/owner.json written in auth.setup.ts.
const OWNER_STORAGE = '.auth/owner.json';

export default defineConfig({
  testDir: '.',
  timeout: 5 * 60 * 1000,
  expect: { timeout: 15 * 1000 },

  // Never allow `test.only` to silently narrow the suite in CI.
  forbidOnly: !!process.env.CI,

  // One retry; with trace:'on-first-retry' a full trace is captured only on a
  // flap, keeping green runs cheap.
  retries: 1,

  // Shared bootstrapped org + seeded singleton → mutating specs would race, so
  // run serially. Deliberate debt (see docs/e2e-conventions.md §Independence).
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
    actionTimeout: 15 * 1000,
    navigationTimeout: 30 * 1000,
  },

  projects: [
    // Runs first: the bootstrap ceremony, and writes the owner's storageState.
    { name: 'setup', testMatch: /auth\.setup\.ts/ },

    // The bulk suite — logged-in via the saved session. Excludes the setup file
    // and the auth-lifecycle specs (the latter run unauthenticated below).
    {
      name: 'chromium',
      use: { ...devices['Desktop Chrome'], storageState: OWNER_STORAGE },
      dependencies: ['setup'],
      testIgnore: [/auth\.setup\.ts/, /auth-lifecycle\.spec\.ts/],
    },

    // Auth-lifecycle specs that must NOT inherit the owner session.
    {
      name: 'no-auth',
      use: { ...devices['Desktop Chrome'] },
      dependencies: ['setup'],
      testMatch: /auth-lifecycle\.spec\.ts/,
    },
  ],

  outputDir: 'test-results',
});
