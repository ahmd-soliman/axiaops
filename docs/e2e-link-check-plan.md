# Plan — Playwright dashboard E2E regression suite (its own pipeline stage)

> Scope note (2026-06): this started as a **link-check-only** plan. It has been
> broadened into a **full dashboard regression suite** that exercises the
> critical user journeys end-to-end (in DEV_MODE), with the link crawl as one
> area among several. The gating model also changed — see "When it runs".

## Motivation

Issue #130 converted every in-app navigation from `<button onClick={() => navigate()}>`
to a real `<a href>` (react-router `<Link>`). A side effect is that the dashboard
is now **crawlable** — a headless browser can follow every link. A one-off crawl
already paid off: it found a zombie detail row on the org summary that 404'd
(`Detail.jsx` looked the resource up in `/v1/resources`, which the seed never
populated for Kinesis — fixed separately). We want that check — and the broader
"do the core journeys actually work end-to-end" check — to run on every pipeline
instead of by hand.

**Goal:** automatically catch UI regressions — broken links/routes, console
render errors, and broken critical journeys (summary, workbench, dismiss/snooze,
accounts, settings, trend/cost, onboarding) — before they reach `build`/`deploy`
on the integration branches.

## Scope

In scope — a regression suite covering the critical user journeys, run against
the seeded dev data in DEV_MODE (one Playwright project, headless chromium):

1. **Link crawl** (`link-crawl.spec.ts`, fully implemented) — BFS from `/`
   following `<a href>` (opening `[aria-haspopup]` menus to reveal hidden links):
   every internal route resolves (not the NotFound page, no `pageerror`,
   navigation HTTP `< 400`); every external link returns 2xx/3xx (checked once).
   Aggregates all failures and asserts once with the full list. Bounded by
   `MAX_PAGES`; anything dropped is logged loudly.
2. **Org summary** (`/`) renders tiles/sections from seeded data (`/summary`,
   `/summary/by-account`) without `pageerror`.
3. **Workbench / resources** (`/account`) loads; filter by service/account;
   open a zombie detail (`/detail/:id`) — the journey the original crawl caught
   404'ing on Kinesis.
4. **Dismiss / snooze** a zombie and see it move to the **Hidden** view, then
   **restore** it (`POST /dismissals` → `GET /dismissals` → `DELETE`). The one
   mutating journey — runs serially and restores what it changed (see Flakiness).
5. **Cloud accounts** list (`/settings/cloud-accounts`) + account settings
   (`/settings/cloud-accounts/:id`); legacy `/cloud-accounts` redirect.
6. **Settings tabs** — every tab under `/settings` reachable and renders clean.
7. **Trend + cost** screens (`/trend`, `/cost`) render seeded data.
8. **Onboarding** — for the completed seed org, `/onboarding*` redirects to `/`
   (the OnboardingGate contract); the wizard steps + "What's next" panel need a
   fresh-org fixture and are scaffolded (see Implementation status).

Out of scope (later):
- Visual regression (pixel diffing).
- Authenticated crawl via the real native login (we use DEV_MODE auth-bypass; see
  "Auth" below).
- Post-deploy smoke against a live preview env (noted as a future variant).
- Cross-browser (firefox/webkit) — this checks behaviour, not rendering.

## Implementation status

| Area | File | Status |
|---|---|---|
| Link crawl | `e2e/link-crawl.spec.ts` | **Fully implemented** (verified locally: 76 routes, 0 broken) |
| Org summary render | `e2e/org-summary.spec.ts` | Render+no-error test live; seeded-tile assertion `test.fixme` |
| Workbench | `e2e/workbench.spec.ts` | Load test live; filter + open-detail `test.fixme` |
| Dismiss/snooze/restore | `e2e/dismiss-snooze.spec.ts` | `test.fixme` (mutating — needs stable selectors / testids) |
| Cloud accounts | `e2e/cloud-accounts.spec.ts` | List + legacy-redirect tests live; open-settings `test.fixme` |
| Settings tabs | `e2e/settings.spec.ts` | **Fully implemented** (per-tab + team→members redirect) |
| Trend + cost | `e2e/trend-cost.spec.ts` | Render tests live; selector-driven assertion `test.fixme` |
| Onboarding | `e2e/onboarding.spec.ts` | Completed-org redirect tests live; fresh-org wizard `test.fixme` |
| Shared helpers | `e2e/helpers.ts` | `gotoSettled`, `expectNotNotFound`, `captureErrors` |

The `test.fixme` blocks carry concrete TODOs. Most need either stable
`data-testid`s on the relevant controls (none exist yet) or a fresh-org fixture
(onboarding) — adding those is the natural next increment.

## Where it runs — stage placement

Current stages (`.gitlab-ci.yml`):

```
test → integration → build → deploy
```

**An `e2e` stage sits between `integration` and `build`:**

```
test → integration → e2e → build → deploy
```

Rationale:
- The suite needs a **full running stack** (postgres + api + ingestion + dashboard
  + seeded data) — the same shape the `integration` stage already stands up via
  `make test-integration-*`. `e2e` is its natural neighbour.
- Placing it **before `build`/`deploy`** makes a broken UI a **hard gate on the
  integration branches**: no images are built and nothing is deployed when a core
  journey or link is broken (see "When it runs" for how the gate is wired).

## When it runs — gating (CHANGED)

The suite is **mandatory only on `develop` and `main`**, and **never blocks an
MR / feature branch**:

- **`develop` / `main`** → **automatic + `allow_failure: false`** → a **hard
  gate**. A broken route, console `pageerror`, or dead link fails the pipeline
  and blocks `build` + `deploy`.
- **Merge-request pipelines** → **`when: manual` + `allow_failure: true`** →
  devs can trigger it on demand from the pipeline UI, but it **never blocks the
  MR** from merging.
- **`feature|feat|fix|chore|bugfix|hotfix` branches** → same as MRs: manual +
  `allow_failure: true`.

Why this model (vs the earlier change-gated "run on every relevant MR"): the full
stack-up + crawl is the slowest job in the pipeline, and a flaky e2e shouldn't be
able to wall off an MR. Making it a hard gate **only on the post-merge integration
branches** keeps `develop`/`main` honest (every merge is re-verified end-to-end)
while leaving MR authors free to run it on demand without merge-blocking risk.

### How "hard gate on develop/main, never on MRs" is expressed in YAML

Two pieces:

1. **The job's `rules`** set `when` + `allow_failure` per branch, ordered so the
   develop/main rule wins first:

   ```yaml
   e2e:regression:
     stage: e2e
     needs: ["test:integration:api", "test:integration:ingestion"]
     allow_failure: false            # strict default
     rules:
       - if: '$CI_COMMIT_BRANCH == "main" || $CI_COMMIT_BRANCH == "develop"'
         when: on_success
         allow_failure: false        # mandatory, automatic → hard gate
       - if: '$CI_PIPELINE_SOURCE == "merge_request_event"'
         when: manual
         allow_failure: true         # on-demand, never blocks
       - if: '$CI_COMMIT_BRANCH =~ /^(feature|feat|fix|chore|bugfix|hotfix)\//'
         when: manual
         allow_failure: true
   ```

2. **`build:images` lists e2e as an OPTIONAL need** so the gate is wired into the
   DAG without blocking MRs:

   ```yaml
   build:images:
     needs:
       - ...
       - job: "e2e:regression"
         optional: true
   ```

   On `develop`/`main` e2e is automatic + non-allow_failure, so `build` **waits
   for it to pass** before building images → real gate. On MRs/branches e2e is
   `manual` + `allow_failure: true`, which GitLab treats as **skippable**, so
   `build` proceeds without waiting → e2e never blocks the MR build/deploy.

## Job architecture (self-contained, after integration)

Mirrors the existing integration jobs (`docker:24` + docker-compose). A dedicated
compose file + a Make target run it identically locally and in CI.

```
test-infra/e2e/docker-compose.yml
  postgres
  migrate (one-shot)
  ingestion (DEV_MODE)
  api      (DEV_MODE, DEV_ORGANIZATION_ID=dev-organization-axiaops)
  dashboard (nginx image, built with VITE_DEV_MODE=true; /api → api:8080)
  seed     (one-shot: runs scripts/seed_test_data.sh via MIGRATION_DATABASE_URL)
  playwright (mcr.microsoft.com/playwright:v1.60.0-jammy, BASE_URL=http://dashboard)
```

- **dashboard** service: built from `services/dashboard/Dockerfile` (nginx),
  with `VITE_DEV_MODE=true` so the SPA's own DEV_MODE flag is on (AuthGuard
  doesn't bounce to `/login`). Its `nginx.conf` proxies `/api/*` → `api:8080`,
  so the SPA's relative API calls resolve with no extra wiring.
- **api / ingestion**: `DEV_MODE=true` so the suite reaches authenticated routes
  without a login flow (see Auth). The api's `DEV_ORGANIZATION_ID` is pinned to
  the same id the seed script writes (`dev-organization-axiaops`).
- **seed**: `scripts/seed_test_data.sh` in its remote/direct-psql mode (set
  `MIGRATION_DATABASE_URL`), so account/zombie/service/trend deep-links render.
- **playwright runner**: image with Chromium preinstalled (no `npx playwright
  install` download in CI). Mounts the dashboard source, `npm ci`, `npm run e2e`.
  Depends on `dashboard` (healthy) + `seed` (completed).

`make test-e2e` wraps: `compose build` → `compose run --rm playwright` (which
starts the dependency chain, seeds, runs the suite, returns its exit code) →
(always) `compose down -v`. `run --rm` (not `up`) is used so the one-shot
`migrate`/`seed` containers exiting 0 don't tear the stack down mid-suite.

### Auth
DEV_MODE bypasses auth, so the suite isn't stuck at `/login` and can reach every
route. This is exactly how `make start-dev` runs locally. If we later want to
exercise the **real** auth surface, capture a `storageState` once via the native
login and reuse it — deferred, since DEV_MODE covers route/journey health.

## Test code layout

```
services/dashboard/
  e2e/
    playwright.config.ts     # baseURL from $BASE_URL; chromium project; retries=1;
                             # trace 'on-first-retry'; html + list reporters; workers:1
    helpers.ts               # gotoSettled / expectNotNotFound / captureErrors
    link-crawl.spec.ts       # the crawler (fully implemented)
    org-summary.spec.ts
    workbench.spec.ts
    dismiss-snooze.spec.ts
    cloud-accounts.spec.ts
    settings.spec.ts
    trend-cost.spec.ts
    onboarding.spec.ts
  package.json               # devDep @playwright/test; script "e2e"
```

Keep this a **separate Playwright install scoped to the dashboard** (devDependency
`@playwright/test`), independent of the vitest unit suite. It is NOT installed
into the default dev flow — only the e2e job (and devs running it on purpose)
pull it. ESLint ignores `e2e/` (TypeScript, Node runtime, own toolchain).

`link-crawl.spec.ts` specifics:
- Seeds the queue with `/` + known top-level routes; BFS internal routes by
  collecting `<a href>` per page; best-effort opens `[aria-haspopup]` menus first
  so hidden links (avatar menu, org switcher, mobile nav) are discovered.
- Per route: `page.goto` (networkidle), assert NOT the NotFound marker
  (`"This page isn't here"`), assert zero `pageerror`, assert `response.status() < 400`.
- External links: one `request.head` (falling back to `get`) each, expect `< 400`.
- **Aggregate, then assert once** at the end with the full list of broken links.
- Bounded by `MAX_PAGES` (200); logs anything dropped (no silent truncation).
- Emits Playwright `trace`/screenshots on first retry / failure for triage.

## Flakiness controls
- `page.goto(..., { waitUntil: 'networkidle' })` + an explicit settle per route.
- Playwright `retries: 1`, `trace: 'on-first-retry'`; job-level `retry: *flaky_retry`.
- `workers: 1` + `fullyParallel: false` — the seeded data is a shared singleton,
  so the mutating flows (dismiss/snooze) can't race read-only specs.
- Mutating specs **restore what they change** so re-runs stay deterministic.
- Deterministic seed → stable crawl set.

## Runtime budget
Compose up (layers cached) ~30–60s + seed ~10s + suite (~26 tests, link crawl is
the long pole at ~1–2 min) → target **< 5 min**. If the route set / suite grows,
shard with Playwright `--shard` across `parallel:` jobs.

## How to run it locally

```bash
# Full containerised stack (what CI runs):
make test-e2e

# Or against an already-running dev stack:
make start-dev && make seed
cd services/dashboard && BASE_URL=http://localhost:5173 npm run e2e
# (first time: npx playwright install chromium)
```

## Suggested orders (summary)
- **Stage order:** `test → integration → e2e → build → deploy` (e2e gates
  build/deploy on develop/main).
- **Within the job:** stack up → seed → run suite → teardown.
- **Gating:** mandatory+automatic on develop/main; manual+allow_failure on MRs.
