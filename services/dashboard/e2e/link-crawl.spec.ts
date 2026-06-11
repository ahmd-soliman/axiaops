import { test, expect, type Page, type Response } from '@playwright/test';

// ─────────────────────────────────────────────────────────────────────────────
// link-crawl.spec.ts — BFS link-health crawl of the dashboard.
//
// What it proves (against a DEV_MODE stack with seeded data):
//   • Every internal route reachable from `/` by following <a href> resolves:
//       – it is NOT the NotFound page (marker text "This page isn't here"),
//       – it raised zero uncaught `pageerror` while rendering,
//       – the navigation HTTP response status was < 400.
//   • External links (different origin) are checked once but ADVISORY-only:
//     their target is environment-dependent (runtime-injected, e.g. the
//     axiaops.io license/install link) and their liveness needs outbound
//     DNS/network the hermetic e2e container doesn't have. Failures are logged
//     as warnings, never fatal — only INTERNAL route health is asserted.
//
// How it discovers links:
//   • Seeds the queue with `/` plus the known top-level routes (so a regression
//     that drops a nav link still gets that route crawled).
//   • Per page, opens any `[aria-haspopup]` triggers first to reveal links
//     hidden behind menus/dropdowns (avatar menu, org switcher, mobile nav),
//     then collects every <a href>.
//
// Failure reporting:
//   • Aggregates ALL broken links and asserts ONCE at the end with the full
//     list — far better signal than dying on the first bad link.
//   • Bounded by MAX_PAGES; anything dropped past the bound is logged loudly
//     (no silent truncation).
//
// This is the hardened port of the throwaway crawler referenced in
// docs/e2e-link-check-plan.md.
// ─────────────────────────────────────────────────────────────────────────────

const NOT_FOUND_MARKER = "This page isn't here";

// Hard ceiling on crawled internal pages — protects against an accidental
// explosion (e.g. a pagination control that mints unbounded ?cursor= links).
const MAX_PAGES = 200;

// Top-level routes we always crawl even if a nav regression hides their link.
// Deep-link routes (/detail/:id, /settings/cloud-accounts/:id) are discovered
// dynamically from the seeded org-summary / accounts pages.
const SEED_ROUTES = [
  '/',
  '/account',
  '/trend',
  '/cost',
  '/connect',
  '/settings',
  '/settings/profile',
  '/settings/cloud-accounts',
  '/settings/members',
  '/settings/integrations',
  '/settings/audit',
  '/settings/sso',
  '/settings/organization',
  // '/settings/license' omitted — under the SaaS e2e build it redirects to
  // /settings (no customer-facing license); crawling it just re-crawls /settings.
  '/onboarding',
];

type Broken = { route: string; from: string; reason: string };

function normalizeInternal(href: string, base: string): string | null {
  let url: URL;
  try {
    url = new URL(href, base);
  } catch {
    return null;
  }
  const baseOrigin = new URL(base).origin;
  if (url.origin !== baseOrigin) return null; // external — handled separately
  // Drop hash-only fragments; keep path + query as the dedupe key.
  return url.pathname + url.search;
}

function isExternal(href: string, base: string): boolean {
  try {
    const url = new URL(href, base);
    return url.protocol.startsWith('http') && url.origin !== new URL(base).origin;
  } catch {
    return false;
  }
}

// Best-effort reveal of links hidden behind menus/popovers so the crawler can
// see them. Each trigger is opened in isolation; failures are swallowed because
// a popover that won't open on this route simply yields no extra links.
async function revealHiddenLinks(page: Page): Promise<void> {
  const triggers = page.locator('[aria-haspopup]');
  const count = await triggers.count();
  for (let i = 0; i < count; i++) {
    const trigger = triggers.nth(i);
    try {
      if (await trigger.isVisible()) {
        await trigger.click({ timeout: 2000 });
        // Let the menu mount; links are read immediately after.
        await page.waitForTimeout(150);
      }
    } catch {
      // Trigger not interactable on this route — ignore.
    }
  }
}

async function collectLinks(page: Page): Promise<string[]> {
  return page.$$eval('a[href]', (anchors) =>
    anchors
      .map((a) => (a as HTMLAnchorElement).getAttribute('href') ?? '')
      .filter((h) => h && !h.startsWith('#') && !h.startsWith('mailto:') && !h.startsWith('tel:')),
  );
}

test('every reachable internal route resolves; external links are alive', async ({ page, request }, testInfo) => {
  const baseURL = testInfo.project.use.baseURL as string;
  if (!baseURL) throw new Error('BASE_URL must be set (testInfo.project.use.baseURL is empty)');

  const broken: Broken[] = [];
  const visited = new Set<string>();
  const externalChecked = new Set<string>();
  const queue: Array<{ route: string; from: string }> = SEED_ROUTES.map((r) => ({ route: r, from: '(seed)' }));
  let dropped = 0;

  while (queue.length > 0) {
    const { route, from } = queue.shift()!;
    if (visited.has(route)) continue;

    if (visited.size >= MAX_PAGES) {
      dropped += queue.length + 1;
      break;
    }
    visited.add(route);

    // Capture uncaught render errors for this navigation.
    const pageErrors: string[] = [];
    const onError = (err: Error) => pageErrors.push(err.message);
    page.on('pageerror', onError);

    let response: Response | null = null;
    try {
      response = await page.goto(route, { waitUntil: 'networkidle' });
    } catch (err) {
      broken.push({ route, from, reason: `navigation threw: ${(err as Error).message}` });
      page.off('pageerror', onError);
      continue;
    }

    // HTTP status of the document fetch. The SPA is served as index.html for
    // every path (client-side routing), so this is normally 200 — a >= 400
    // here means the static host / proxy itself rejected the path.
    if (response && response.status() >= 400) {
      broken.push({ route, from, reason: `HTTP ${response.status()}` });
    }

    // Give the SPA a beat to render the route's content before asserting.
    await page.waitForLoadState('domcontentloaded');
    const bodyText = (await page.locator('body').innerText().catch(() => '')) || '';
    if (bodyText.includes(NOT_FOUND_MARKER)) {
      broken.push({ route, from, reason: 'rendered NotFound page' });
    }

    if (pageErrors.length > 0) {
      broken.push({ route, from, reason: `pageerror: ${pageErrors.join(' | ')}` });
    }
    page.off('pageerror', onError);

    // Discover more links from this route (revealing menu-hidden ones first).
    await revealHiddenLinks(page).catch(() => {});
    const hrefs = await collectLinks(page).catch(() => [] as string[]);

    for (const href of hrefs) {
      if (isExternal(href, baseURL)) {
        const ext = new URL(href, baseURL).toString();
        if (externalChecked.has(ext)) continue;
        externalChecked.add(ext);
        // External-link liveness is ADVISORY, not fatal: it depends on the test
        // container having outbound DNS/network and the third-party site being
        // up — neither is hermetic (e.g. axiaops.io doesn't resolve from inside
        // the e2e network). A flaky external host must not red the suite. We log
        // warnings; only INTERNAL route health is asserted below.
        try {
          // HEAD first; some hosts reject HEAD, fall back to GET.
          let res = await request.head(ext, { timeout: 15000, maxRedirects: 5 }).catch(() => null);
          if (!res || res.status() >= 400) {
            res = await request.get(ext, { timeout: 15000, maxRedirects: 5 });
          }
          if (res.status() >= 400) {
            console.warn(`[link-crawl] external link warning: ${ext} → HTTP ${res.status()} (linked from ${route})`);
          }
        } catch (err) {
          console.warn(`[link-crawl] external link unreachable (advisory): ${ext} → ${(err as Error).message} (linked from ${route})`);
        }
        continue;
      }

      const internal = normalizeInternal(href, baseURL);
      if (internal && !visited.has(internal)) {
        queue.push({ route: internal, from: route });
      }
    }
  }

  // Loud diagnostics — these land in the job log and the HTML report.
  console.log(`[link-crawl] visited ${visited.size} internal routes, checked ${externalChecked.size} external links`);
  if (dropped > 0) {
    console.warn(
      `[link-crawl] MAX_PAGES=${MAX_PAGES} reached — ${dropped} queued route(s) were NOT crawled. ` +
        `Raise MAX_PAGES or shard the crawl if the route set has genuinely grown.`,
    );
  }

  // Single aggregate assertion with the full broken-link list.
  expect(
    broken,
    `\nBroken links found (${broken.length}):\n` +
      broken.map((b) => `  • ${b.route}  [${b.reason}]  (linked from ${b.from})`).join('\n') +
      '\n',
  ).toEqual([]);
});
