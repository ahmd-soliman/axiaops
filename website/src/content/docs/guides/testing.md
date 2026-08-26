---
title: Testing
description: Unit, integration, end-to-end, and AWS-specific testing strategy.
---

Four layers, from fastest/most numerous to slowest/most authoritative.

## Unit tests

Business logic, HTTP handlers, provider interfaces — all mocked, no
database, run in parallel.

```bash
make test
```

## Integration tests

The Postgres storage layer, row-level-security org isolation, migrations,
concurrent access — against a real PostgreSQL 17 instance, `TRUNCATE
CASCADE` before and after each test. Run serially (`-p=1`) since tests share
one DB instance.

```bash
make test-storage
```

## End-to-end tests (Playwright)

The suite runs **auth-on** — `DEV_MODE=false`, real cookie auth, fresh
database — not a faster DEV_MODE lane. A green DEV_MODE suite can still
ship a broken login, since DEV_MODE removes the entire auth surface.

Structure: a `setup` project drives the real bootstrap ceremony (so it's
covered as a test, not skipped via a fixture) and saves a session; a
`flows` project reuses that session for parallelizable read/journey specs;
a `no-auth` project covers auth-lifecycle specs that mutate the session
(logout, invite-redeem, password-reset) and can't share it.

Ground rules: build preconditions via the fastest reliable path (API or SQL
seed), not the UI — use the UI only for the thing under test. Stable
locators (`getByRole`/`getByLabel`) over CSS structure. Web-first,
auto-retrying assertions, never `sleep`. Hermetic — no live AWS/external
calls, fresh stack + DB per run.

```bash
make test-integration
```

## AWS integration testing — real AWS, not emulators

For anything touching the AWS provider or asserting end-to-end
zombie-detection correctness, AxiaOps tests against **real AWS APIs against
a small dedicated test account** — not a local emulator.

**Why not emulators**: for a product whose entire value proposition is
correctly identifying waste in your AWS account, subtle wire-format
divergence *is* the cost of being wrong. Every emulator surveyed stores
service data as disjoint fixture islands with no cross-service ID
validation — a typo in one fixture file makes the analyzer silently return
zero zombies, and the test still passes.

**The layered strategy**:

| Layer | Trust source | Speed | Certifies |
|---|---|---|---|
| Unit/handler | Mocks + golden fixtures captured from real AWS | <1s | Code parses real-shaped responses correctly |
| Integration | Mocked-AWS docker-compose, real Postgres/Redis | ~30s | The ingest→analyze→store→API pipeline works end-to-end |
| Acceptance/canary | Real AWS, a dedicated test account, scheduled polling | minutes | AxiaOps talks to *actual* AWS with no divergence |

A single dedicated AWS account holds one stable resource per detection rule
(an idle EC2, a zero-connection RDS, an unattached EIP, an orphaned
snapshot, ...) and doubles as a dogfood target — every scan against it
should detect the full set. Cost is contained with AWS Budgets alerts +
an auto-stop action, Service Quotas capping instance counts, and an IAM role
that's read-only by construction (`Describe*`/`List*`/`Get*` only — no
`Create*`/`Run*`/`Modify*`/`Delete*` permission exists on the role at all).

Responses are captured once into
`services/ingestion/internal/provider/aws/testdata/` so unit tests replay
them deterministically, and a staging canary runs the real ingestion
pipeline against the fleet on a schedule, comparing zombie counts/savings
against the previous run's baseline before each release — the mechanism
that catches AWS silently changing pagination or field semantics before a
real customer's scan would.
