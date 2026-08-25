# Test Strategy — AxiaOps

Testing architecture across four layers: unit, integration, end-to-end (Playwright),
and AWS-specific.

---

## 1. Unit tests

**What**: business logic (analyzer, crypto, models), HTTP handlers/middleware,
provider interfaces (mocked), serialization. **Database**: none — mocks only.
**Parallel**: yes (`-parallel 4`). **Speed**: ~1–2s per service, no network/container
startup.

```bash
make test
# or: cd services/shared && go test ./... -count=1 -parallel 4
```

## 2. Integration tests (PostgreSQL)

**What**: the Postgres storage layer, RLS org isolation, migrations, concurrent
access. **Database**: PostgreSQL 17, `TRUNCATE CASCADE` before and after each test;
each test creates its own org for isolation. **Parallel**: no — tests share one DB
instance, so a `TRUNCATE` mid-flight would wipe another test's data. Run with `-p=1`.

```bash
make test-storage
# or: docker compose up -d postgres && TEST_DATABASE_URL=... go test ./storage/postgres/... -p=1
```

```go
func newTestStore(t *testing.T) *postgres.Store {
    setup(t) // TRUNCATE before
    s, _ := postgres.New(ctx, TEST_DATABASE_URL)
    return s
}
func setup(t *testing.T) {
    db.Exec("TRUNCATE TABLE ... CASCADE")
    t.Cleanup(func() { db.Exec("TRUNCATE TABLE ... CASCADE"); db.Close() })
}
```

**Do**: use `t.Cleanup()`, create a fresh org per test (`newOrgCtx(t, store)`), run
with `-count=1` to disable caching. **Don't**: parallelize Postgres tests; assume
test order; rely on global state.

```bash
make test-all   # unit + integration, ~20s
```

---

## 3. End-to-end tests (Playwright)

Scope: `services/dashboard/e2e/` + the stack in `test-infra/e2e/`.

### The rule that drives everything: test the config you ship

Customers run `DEV_MODE=false` (real cookie auth). The suite runs **auth-on** — one
stack, `DEV_MODE=false`, fresh database — not a separate faster DEV_MODE lane. A
green DEV_MODE suite can still ship a broken login, because DEV_MODE removes the
entire auth surface.

### Architecture: setup project → shared session → specs

Playwright's project-dependency pattern (not `globalSetup`), because the bootstrap
ceremony *is* a test we want reported:

```
project "setup" (auth.setup.ts, no storageState)
  1. drives the REAL /auth/bootstrap ceremony    ← bootstrap coverage
  2. asserts the fresh-org onboarding wizard appears
  3. seeds fixture data into the just-created org (no org exists before bootstrap,
     so seeding runs from here, after bootstrap, via scripts/seed_test_data.sh)
  4. completes onboarding
  5. saves the owner's cookie → .auth/owner.json

project "flows" (deps: [setup], storageState: owner.json)
  every read/journey spec — logged in, parallelizable

project "no-auth" (deps: [setup], no storageState)
  auth-lifecycle specs that must NOT share the session (they mutate/drop it):
  bootstrap-is-sealed, logout, switch-org, invite-redeem, password-reset
```

### Writing specs

1. **e2e is the tip of the pyramid** — few, high-value, cross-cutting journeys. Don't
   re-test what unit/integration already covers.
2. **Build preconditions via the fastest reliable path, not the UI** — API or SQL
   seed; use the UI only for the thing under test. (Zombies specifically can't be
   API-created — they come from scans — so they stay in the SQL seed.)
3. **Independence** — a spec must pass regardless of order; mutating specs restore
   what they change. The suite runs serially (`workers:1`) because of the shared
   seeded org — treat that as deliberate debt, not a target; new mutating journeys
   should prefer self-owned data.
4. **Stable locators** — `getByRole`/`getByLabel`/`data-testid` over CSS/DOM structure.
5. **Web-first assertions, never `sleep`** — auto-retrying assertions
   (`toBeVisible()`, `waitForURL`); avoid `networkidle` on SPAs that poll.
6. **Hermetic** — no live AWS/external calls; fresh stack + DB per run, torn down
   (`down -v`) even on failure.
7. **Flake control** — one retry, `trace: on-first-retry`, artifacts on failure only.
   Triage a flake immediately — a tolerated flaky gate trains people to retry past
   real failures.

### CI gating

Non-blocking everywhere today (the suite is young): `develop`/`main` runs
automatically but `allow_failure: true` (visible, not blocking);
MRs/feature-branches are `when: manual` (keeps the heavy 3-image build off every
push). **Exit criterion**: flip to a hard gate (`allow_failure: false`) once the
bootstrap+login lifecycle specs have been reliably green for ~2 weeks.

---

## 4. AWS integration testing — real AWS, not emulators

> Decision recorded 2026-05-09.

For any test path touching `services/ingestion/internal/provider/aws/` or asserting
end-to-end zombie-detection correctness, AxiaOps tests against **real AWS APIs
against a small dedicated test account** — not a local AWS emulator. Local emulators
may still exist as offline-dev conveniences; they are never load-bearing for QA.

### Why not emulators

For a product whose entire value is "we correctly identify the waste in your AWS
account," subtle wire divergence *is* the cost-of-being-wrong: an emulator that
formats a field slightly differently, or paginates differently, produces tests that
pass and detections that ship subtly wrong. Worse, every emulator evaluated stores
EC2/CloudWatch/Cost-Explorer data as **disjoint fixture islands** with no
cross-service ID validation — a typo in one fixture file causes
`analyzer.Detect()` to silently return zero zombies, and the test still passes (it
just certifies that the analyzer can read empty input). No emulator surveyed closed
this gap, which made the decision unconditional rather than coverage-based.

### The layered strategy

| Layer | Trust source | Speed | Certifies |
|---|---|---|---|
| Unit/handler | Mocks + golden fixtures captured from real AWS | <1s | Our code parses real-shaped responses, builds well-formed requests |
| Integration | Mocked-AWS docker-compose, real Postgres/Redis | ~30s | ingest→analyze→store→API pipeline works end-to-end |
| Acceptance/canary | Real AWS, a dedicated test account, scheduled polling | minutes | We talk to *actual* AWS with no divergence — catches AWS silently changing behavior before customers do |

95% of test volume lives in the unit layer; the acceptance layer is the irreducible
truth source for wire compatibility.

### The real-AWS test fleet

A single dedicated AWS account (provisioned via Terraform from a developer laptop,
never CI) holding one stable resource per detection rule — an idle EC2, a
zero-connection RDS, an unattached EIP/EBS volume, an orphaned snapshot, an idle
ELB, etc. — costing roughly €38/month. The fleet doubles as an AxiaOps dogfood
target: every staging scan should detect it in full.

**Cost containment**: AWS Budgets at €60/mo with an 80% alert + a 100% Budgets Action
that stops EC2/RDS and detaches the test IAM policy; Service Quotas capping max
instance counts; an SCP denying expensive instance families if the account is in an
Organization.

**IAM scope — read-only by construction**: the canary role has no `Create*`/`Run*`/
`Modify*`/`Delete*` permission at all, only `Describe*`/`List*`/`Get*` across the
services AxiaOps scans plus `ce:GetCostAndUsage*` and `sts:GetCallerIdentity`. Worst
case on a credential leak: a few thousand free Describe calls, not a compute storm.

**Captured fixtures**: a one-time capture tool calls every Describe/List/GetCost
endpoint against the test fleet and snapshots the JSON into
`services/ingestion/internal/provider/aws/testdata/` — unit tests replay these
deterministically (wire-compat truthfulness + free, deterministic CI). Re-capture
periodically or after AWS signals a change.

**Staging canary**: the actual ingestion service runs against the test fleet on a
schedule; zombie counts/savings/breakdowns are compared against the previous run's
baseline before each release — the only mechanism that catches AWS silently changing
pagination or field semantics.
