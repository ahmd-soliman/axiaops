import { type Page, expect } from '@playwright/test';

// Shared helpers for the dashboard regression suite.
//
// The whole suite runs in DEV_MODE (auth bypassed), so there is no login step —
// `page.goto('/')` lands straight on the org summary with seeded data.

export const NOT_FOUND_MARKER = "This page isn't here";

// Assert the current route did not fall through to the SPA's NotFound page.
// Cheap guard to put at the top of any flow spec so a routing regression fails
// here with a clear message rather than as a confusing downstream selector miss.
export async function expectNotNotFound(page: Page): Promise<void> {
  const body = (await page.locator('body').innerText().catch(() => '')) || '';
  expect(body, `route ${page.url()} rendered the NotFound page`).not.toContain(NOT_FOUND_MARKER);
}

// Navigate and wait for the SPA to settle (network idle + DOM ready). Returns
// nothing; throws on navigation failure so callers don't have to null-check.
export async function gotoSettled(page: Page, route: string): Promise<void> {
  await page.goto(route, { waitUntil: 'networkidle' });
  await page.waitForLoadState('domcontentloaded');
}

// Collect every uncaught pageerror raised while `fn` runs. Use to assert a
// journey rendered cleanly: `expect(await captureErrors(page, () => ...)).toEqual([])`.
export async function captureErrors(page: Page, fn: () => Promise<void>): Promise<string[]> {
  const errors: string[] = [];
  const onError = (err: Error) => errors.push(err.message);
  page.on('pageerror', onError);
  try {
    await fn();
  } finally {
    page.off('pageerror', onError);
  }
  return errors;
}
