# E2E Testing Conventions (Playwright)

How the AxiaOps dashboard end-to-end suite is built and how to extend it.
Scope: `services/dashboard/e2e/` + the stack in `test-infra/e2e/`. For the
original rationale of the link-check crawler see `docs/e2e-link-check-plan.md`.

## The one rule that drives everything: test the config you ship

Customers run `DEV_MODE=false` (real cookie auth). The e2e suite therefore runs
**auth-on** — a single stack with `DEV_MODE=false` against a fresh database. We do
**not** keep a DEV_MODE lane: a green DEV_MODE suite can still ship a broken login
because DEV_MODE removes the entire auth surface. One stack, one faithful config.

> This replaced an earlier two-lane idea (fast DEV_MODE lane + a separate auth-on
> lane). One auth-on lane is fewer moving parts and exercises the real path.

## Architecture: setup project → shared session → specs

We use Playwright's **project-dependency** pattern, not a plain `globalSetup`,
because the bootstrap ceremony *is* a test we want reported:

```
project "setup"  (e2e/auth.setup.ts, no storageState)
  1. drives the REAL /auth/bootstrap ceremony  ← this is our bootstrap coverage
  2. asserts the fresh-org onboarding wizard appears (org is brand new here)
  3. seeds fixture data into the just-created org
  4. completes onboarding
  5. saves the owner's cookie → .auth/owner.json (storageState)

project "flows"  (dependencies: [setup], use.storageState: .auth/owner.json)
  every read/journey spec — runs logged-in, can parallelize

project "no-auth" (dependencies: [setup], no storageState)
  auth-lifecycle specs that must NOT share the session:
  bootstrap-is-sealed, logout, switch-org, invite-redeem, password-reset
```

**Why the split:** auth-lifecycle specs mutate or drop the session; if they ran
with the shared `storageState` they'd corrupt it for everyone. They get fresh
contexts. Everything else reuses the one login.

### The seed-after-bootstrap constraint (important)

In auth-on, **no organization exists until the bootstrap ceremony runs inside the
test.** A startup `seed` compose service can't seed (there's no org yet, and the
token-gated ceremony hasn't happened). So **seeding runs from the `setup` project,
after bootstrap**, against the org the ceremony just created. The setup step has
DB access (owner connection string) and reuses `scripts/seed_test_data.sh` (its
auth-on path resolves the newest org automatically). This is the one piece of
plumbing the single-stack model forces; it's the price of testing the real
ceremony in the same run as the seeded flows.

### Onboarding state

A freshly-bootstrapped org has `onboarding_completed_at = NULL`, so `OnboardingGate`
redirects `/` → the wizard. The bulk flow specs expect the dashboard, so the
`setup` project **marks onboarding complete** after asserting the wizard once.
The wizard is therefore exercised exactly once, in `setup`, while the org is still
fresh — there is no separate fresh-org fixture to maintain.

## Conventions for writing specs

1. **Altitude — e2e is the tip of the pyramid.** Few, high-value, cross-cutting
   *journeys*. Don't re-test what unit/golden/integration already cover. The
   link-crawl is the cheap broad smoke; targeted specs cover real journeys.
2. **Set up state via the fastest reliable path, not the UI.** Build preconditions
   through the API or seed; use the UI only for the thing under test. (Zombies
   can't be API-created — they come from scans — so they stay in the SQL seed.)
3. **Independence.** A spec must pass regardless of order. The shared seed is a
   read-mostly singleton; **mutating specs must restore what they change** (e.g.
   dismiss → assert hidden → restore) so re-runs stay clean. The suite is
   `workers:1`/serial today because of the shared org — treat that as deliberate
   debt, not a target. New mutating journeys should prefer self-owned data.
4. **Stable locators.** Prefer `getByRole` / `getByLabel` / `data-testid` over CSS
   or DOM structure. Add `data-testid` to elements a spec targets rather than
   pinning brittle text/structure. (Several screens lack test-ids — add them with
   the spec.)
5. **Web-first assertions, never `sleep`.** Use auto-retrying assertions
   (`expect(locator).toBeVisible()`, `waitForURL`). Assert on visible elements
   rather than `networkidle` for journey specs (SPAs with polling never idle).
6. **Hermetic + deterministic.** No live AWS / external calls. Fresh stack +
   fresh DB per run; the stack is torn down (`down -v`) even on failure (same
   discipline as the integration stacks — see the cancelled-job reaper).
7. **Flake control.** One retry, `trace: on-first-retry`, artifacts on failure
   only. Triage a flake immediately — a tolerated flaky gate trains people to
   `retry` past real failures.

## CI gating

e2e is slower and flakier than unit, and the suite is young, so the current
posture is **non-blocking everywhere**:

- **`develop` / `main`** — runs **automatically** (`when: on_success`) but
  **`allow_failure: true`**: every integration-branch pipeline reports e2e, but a
  failure does not block build/deploy. `interruptible: true` so a newer push
  cancels the in-flight run (the heaviest job — don't pile it on the shared
  runner).
- **MRs / feature branches** — **`when: manual`** + `allow_failure: true`. Not
  automatic, to keep the heavy 3-image build off every push on a CPU-bound runner;
  trigger on demand when a change warrants it.

**Exit criterion — re-arm the gate.** Flip the `develop`/`main` rule back to
`allow_failure: false` (a hard gate that blocks build/deploy on a red UI) once the
suite is stable and trusted — specifically once the bootstrap + login lifecycle
specs are reliably green over ~2 weeks of runs. Until then, non-blocking-but-
visible buys signal without training people to rerun past real failures.

## What auth-on coverage is still missing (the backlog)

Going auth-on *unlocks* a whole surface DEV_MODE hid. Current real coverage is
link-crawl + onboarding; the rest are `fixme` stubs. Prioritised gaps:

**Auth lifecycle (highest value — DEV_MODE couldn't touch any of these):**
- [ ] **Bootstrap ceremony** — fresh install → create first owner → land in app.
- [ ] **Bootstrap sealed** — after success, `/bootstrap` redirects to `/login`; re-POST 409.
- [ ] **Native login / logout** round-trip (the two-phase email→password form).
- [ ] **Multi-membership org selection** (`/select-org`) and **in-app switch-org**.
- [ ] **Invite redemption** — new-user and existing-user (cross-org) flows.
- [ ] **Password reset** redemption — sets password, all sessions revoked → `/login`.
- [ ] **AuthGuard** — unauthenticated deep link bounces to `/login`, returns post-login.
- [ ] **Session expiry / 401** handling and the 503 → `/service-unavailable` bounce.

**Core journeys (exist as stubs — implement against the seeded org):**
- [ ] **Dismiss / snooze → verify hidden → restore** (the top mutating journey).
- [ ] **Connect AWS account** (role ARN + ExternalId) form validation + list update.
- [ ] **Trend / cost** filtering by account/service/resource-type.
- [ ] **CSV export** download on a list screen.
- [ ] **Org summary / by-account** rollup renders seeded data.

**RBAC / org safety (auth-on makes these testable):**
- [ ] **Member vs admin vs owner** — a viewer can't see admin-only actions.
- [ ] **RLS isolation** — switch to Acme/Globex, confirm no cross-org leakage.

The auth-lifecycle block is the real reason for the refactor — keep it ahead of
adding more read-only crawl breadth.
