# Plan — Playwright link-check E2E as its own pipeline stage

## Motivation

Issue #130 converted every in-app navigation from `<button onClick={() => navigate()}>`
to a real `<a href>` (react-router `<Link>`). A side effect is that the dashboard
is now **crawlable** — a headless browser can follow every link. A one-off crawl
already paid off: it found a zombie detail row on the org summary that 404'd
(`Detail.jsx` looked the resource up in `/v1/resources`, which the seed never
populated for Kinesis — fixed separately). We want that check to run on every
pipeline instead of by hand.

**Goal:** automatically fail the pipeline when an in-app link leads to the 404
page, throws an uncaught render error, or returns HTTP ≥ 400 — and when an
external link is dead.

## Scope

In scope:
- Crawl from `/`, following `<a href>` breadth-first, including links revealed by
  opening menus/dropdowns (`aria-haspopup`).
- Assert every **internal** route resolves: not the NotFound page, no `pageerror`,
  navigation response `< 400`.
- Assert every **external** link returns 2xx/3xx.
- Run against the seeded dev data so dynamic deep-links (account / zombie /
  service rows) are exercised.

Out of scope (later):
- Visual regression / full interaction flows (forms, mutations).
- Authenticated crawl via the real native login (we use DEV_MODE auth-bypass; see
  "Auth" below).
- Post-deploy smoke against a live preview env (noted as a future variant).

## Where it runs — stage placement

Current stages (`.gitlab-ci.yml`):

```
test → integration → build → deploy
```

**Recommendation: add an `e2e` stage between `integration` and `build`:**

```
test → integration → e2e → build → deploy
```

Rationale:
- The check needs a **full running stack** (postgres + api + ingestion + dashboard
  + seeded data) — the same shape the `integration` stage already stands up via
  `make test-integration-*`. `e2e` is its natural neighbour.
- Placing it **before `build`/`deploy`** makes a broken UI a **hard gate**: no
  images are built and nothing is deployed when a link is broken.
- It matches the instinct "probably after integration."

Alternatives considered:
| Placement | Pro | Con |
|---|---|---|
| **after `integration`** (recommended) | Gates build+deploy; reuses integration's stack pattern; fails fast/cheap | Builds the dashboard from source in-job (not the published image) |
| after `build` | Tests the exact published nginx image (highest fidelity) | Wastes a build on a UI that's about to fail; later feedback |
| after `deploy` (preview smoke) | Tests a real deployed env end-to-end | Needs real login/storageState; only catches issues post-deploy; flakier |

Start with "after integration"; we can add a thin post-deploy smoke variant later
without moving this one.

## Job architecture (recommended: self-contained, after integration)

Mirror the existing integration jobs (`docker:24` + docker-compose). Add a
dedicated compose file and a Make target so it runs identically locally and in CI.

```
test-infra/e2e/docker-compose.yml   # postgres + api(DEV_MODE) + ingestion(DEV_MODE) + dashboard(nginx)
```

- **dashboard** service: built from `services/dashboard/Dockerfile` (nginx). Its
  `nginx.conf` already proxies `/api/*` → `api:8080`, so the SPA's relative API
  calls resolve with no extra wiring.
- **api / ingestion**: `DEV_MODE=true` so the crawler reaches authenticated routes
  without a login flow (see Auth).
- **seed**: run `scripts/seed_test_data.sh` against the compose postgres after the
  stack is healthy, so account/zombie/service deep-links render.
- **playwright runner**: image `mcr.microsoft.com/playwright:v1.<x>-jammy`
  (Chromium preinstalled — no `npx playwright install` download in CI), with
  `BASE_URL=http://dashshboard` (the compose service), runs the spec, exits
  non-zero on any broken link.

`make test-e2e` wraps: `compose up -d --build` → wait-for-healthy → seed →
`compose run playwright npm run e2e` → (always) `compose down -v`.

### Auth
DEV_MODE bypasses auth, so the crawler isn't stuck at `/login` and can reach every
route. This is exactly how `make start-dev` runs locally. If we later want to crawl
the **real** auth surface, capture a `storageState` once via the native login and
reuse it — deferred, since DEV_MODE covers route/link health, which is the point.

## Test code layout

```
services/dashboard/
  e2e/
    playwright.config.ts     # baseURL from $BASE_URL; chromium project; retries=1;
                             # trace 'on-first-retry'; html + list reporters
    link-crawl.spec.ts       # the crawler, as a real spec with expect()
  package.json               # devDep @playwright/test; script "e2e": "playwright test e2e"
```

Keep this a **separate Playwright install scoped to the dashboard** (devDependency
`@playwright/test`), independent of the vitest unit suite. It is NOT installed into
the default dev flow — only the e2e job (and devs running it on purpose) pull it.

`link-crawl.spec.ts` (ported from the throwaway crawler, hardened):
- Seed queue with `/` + known top-level routes; BFS internal routes by collecting
  `<a href>` per page; best-effort open `[aria-haspopup]` menus first so hidden
  links (avatar "Settings", account selector, mobile nav) are discovered.
- Per route: `page.goto`, settle, assert NOT the NotFound marker
  (`"This page isn't here"`), assert zero `pageerror`, assert `response.status() < 400`.
- External links: one `request.get(...)` each, expect `ok`.
- **Aggregate, then assert once** at the end with the full list of broken links —
  better signal than failing on the first one.
- Bound by `MAX_PAGES`; `log()` anything dropped (no silent truncation).
- Emit Playwright `trace`/screenshots on failure for triage.

## CI job (sketch)

```yaml
stages: [test, integration, e2e, build, deploy]

e2e:link-check:
  stage: e2e
  image: docker:24
  needs: ["test:integration:api", "test:integration:ingestion"]
  before_script:
    - apk add --no-cache make docker-cli-compose
  script:
    - make test-e2e        # compose up --build, wait-healthy, seed, run playwright
  after_script:
    - |
      if [ "$CI_JOB_STATUS" = "failed" ] && [ -d test-infra/e2e ]; then
        cd test-infra/e2e && docker-compose logs --no-color --tail=200 dashboard api 2>&1 || true
        docker-compose down -v --remove-orphans 2>/dev/null || true
      fi
  artifacts:
    when: on_failure
    paths: [services/dashboard/playwright-report/, services/dashboard/test-results/]
    expire_in: 1 week
  rules:
    # Link health depends on routes AND the data behind them — run when the
    # dashboard, the API/ingestion contract, or the seed changes.
    - if: '$CI_PIPELINE_SOURCE == "merge_request_event"'
      changes:
        paths:
          - services/dashboard/**/*
          - services/api/**/*
          - services/ingestion/**/*
          - scripts/seed_test_data.sh
          - test-infra/e2e/**/*
    - if: '$CI_COMMIT_BRANCH == "main" || $CI_COMMIT_BRANCH == "develop"'
    - if: '$CI_COMMIT_BRANCH =~ /^(feature|feat|fix|chore|bugfix|hotfix)\//'
```

### Enabled for all pipelines? No.
Running the full stack on every pipeline is wasteful and adds noise to changes
that can't affect link health. The rules above scope it:
- **Merge-request pipelines:** only when something that can change routes or the
  data behind them changes — `services/dashboard/**`, `services/api/**`,
  `services/ingestion/**`, `scripts/seed_test_data.sh`, or `test-infra/e2e/**`.
  A docs-only or unrelated-backend MR skips it entirely.
- **`develop` / `main`:** always (post-merge safety net, regardless of paths).
- **`feat|fix|chore|…` branches:** run so authors get the signal pre-MR.

So: not all pipelines — change-gated on MRs, always on the integration branches.

## Flakiness controls
- Wait on `networkidle` + an explicit settle per route; per-route timeout.
- Job-level `retry: 1` (reuse the repo's `*flaky_retry` anchor) + Playwright
  `retries: 1`, `trace: 'on-first-retry'`.
- Deterministic seed → stable crawl set.
- Roll out as `allow_failure: true` for the first 1–2 pipelines to shake out flake,
  then flip to a hard gate (`allow_failure: false`).

## Runtime budget
Compose up (layers cached) ~30–60s + seed ~10s + crawl (~55 routes) ~1–2 min →
target **< 4 min**. If the route set grows, shard the spec across parallel jobs
(`parallel:` + Playwright `--shard`).

## Rollout order

1. **Land #130 (anchors) + the seed fix first** — already in flight (MR !319, !320).
   The e2e harness is only meaningful once links are real anchors and the seed data
   is internally consistent.
2. **MR A — test code:** add `@playwright/test` + `services/dashboard/e2e/`
   (config + spec). Prove it locally against `make start-dev` + `make seed`.
3. **MR B — harness:** add `test-infra/e2e/docker-compose.yml` + `make test-e2e`
   (mirrors `make test-integration-*`).
4. **MR C — pipeline:** add the `e2e` stage + `e2e:link-check` job. Ship with
   `allow_failure: true`, watch two pipelines, then flip to `false`.
5. Document in `CLAUDE.md` (Testing Conventions) + this file; optionally add the
   post-deploy preview smoke variant later.

## Suggested orders (summary)
- **Stage order:** `test → integration → e2e → build → deploy` (e2e gates build/deploy).
- **Within the job:** stack up → seed → crawl → teardown.
- **Delivery order:** anchors + seed fix → test code → compose/make harness →
  pipeline wiring (soft-gate → hard-gate).
