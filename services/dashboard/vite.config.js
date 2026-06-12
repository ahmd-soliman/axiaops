// defineConfig comes from vitest/config (a re-export of vite's defineConfig
// with proper types for the `test` block). Using `vite`'s defineConfig
// would also work at runtime but doesn't type-check the test config.
import { defineConfig } from 'vitest/config';
import react from '@vitejs/plugin-react';

export default defineConfig({
  plugins: [react()],
  server: {
    port: 5173,
    // Respect VITE_NO_OPEN=true to suppress auto-open (used by VS Code debug
    // tasks, where Chrome is launched separately by the debugger).
    open: process.env.VITE_NO_OPEN !== 'true',
    proxy: {
      '/api': {
        target: 'http://localhost:8080',
        rewrite: (path) => path.replace(/^\/api/, ''),
      },
    },
  },
  // Vitest config — see https://vitest.dev/config/.
  // `environment: 'jsdom'` provides a browser-ish DOM so React Testing
  // Library's render() works. `setupFiles` wires the jest-dom matchers + an
  // afterEach cleanup so DOM doesn't leak across tests. `globals` is left
  // off — tests import describe/it/expect/etc explicitly from 'vitest',
  // which is more reviewable than relying on ambient globals.
  test: {
    environment: 'jsdom',
    setupFiles: ['./src/test-setup.js'],
    // Vitest unit tests live under src/. Scope collection there so the
    // Playwright end-to-end specs in e2e/*.spec.ts (which import
    // @playwright/test and call test.describe at module load) are NOT picked
    // up by Vitest — otherwise `npm run test:run` errors with "Playwright Test
    // did not expect test.describe() to be called here". Playwright runs e2e/
    // via its own runner (npm run e2e).
    include: ['src/**/*.{test,spec}.{js,jsx,ts,tsx}'],
  },
});
