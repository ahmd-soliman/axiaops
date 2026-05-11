// Vitest test-setup — runs once before each test file (see vite.config.js
// `test.setupFiles`).
//
// Three things happen here:
//
// 1. `@testing-library/jest-dom` extends Vitest's `expect` with DOM-aware
//    matchers (toBeInTheDocument, toHaveAttribute, etc). Without this import
//    those assertions don't exist and tests using them throw `expect(...).
//    toBeInTheDocument is not a function`.
//
// 2. `afterEach(cleanup)` unmounts every component rendered by Testing
//    Library between tests. Otherwise DOM nodes accumulate across tests in
//    the same file and queries like getByText match against stale renders
//    from earlier tests.
//
// 3. The design-token stylesheet (`src/styles/tokens.css`) is loaded into
//    JSDOM. Issue #88 migrated every theme token from a JS object to CSS
//    custom properties on `:root`. Vite/Vitest's default test environment
//    does NOT auto-load CSS imports, so without this step JSDOM's
//    computed-style reads return empty strings for `var(--color-X)`
//    references — silently breaking any test that asserts on inline-style
//    colour values or interacts with code that resolves CSS variables.
import '@testing-library/jest-dom/vitest';
import { afterEach } from 'vitest';
import { cleanup } from '@testing-library/react';
import fs from 'node:fs';
import path from 'node:path';
import { fileURLToPath } from 'node:url';

// Inject tokens.css into JSDOM. Vite normally compiles CSS imports into
// HMR-aware stylesheet records; under Vitest's jsdom environment those
// records aren't attached to the document. Reading the raw file and
// dropping a <style> element into <head> gives JSDOM the same :root
// CSS-variable cascade the real browser sees.
const setupDir = path.dirname(fileURLToPath(import.meta.url));
const tokensCss = fs.readFileSync(
  path.join(setupDir, 'styles', 'tokens.css'),
  'utf8',
);
const styleEl = document.createElement('style');
styleEl.setAttribute('data-test-injected', 'tokens.css');
styleEl.textContent = tokensCss;
document.head.appendChild(styleEl);

afterEach(() => {
  cleanup();
});
