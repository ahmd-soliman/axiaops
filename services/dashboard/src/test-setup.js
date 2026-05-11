// Vitest test-setup — runs once before each test file (see vite.config.js
// `test.setupFiles`).
//
// Two things happen here:
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
import '@testing-library/jest-dom/vitest';
import { afterEach } from 'vitest';
import { cleanup } from '@testing-library/react';

afterEach(() => {
  cleanup();
});
