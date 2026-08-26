---
title: Contributing
description: Local dev setup, code conventions, and how to submit a change.
---

How to be productive in this codebase. Read [Architecture](architecture/) first
if you haven't — this guide assumes you understand the service layout.

## Prerequisites

| Tool | Version | Why |
|---|---|---|
| Go | 1.25+ | Backend services |
| Node | 20+ | Dashboard build (Vite + React) |
| Docker + Compose | recent | Postgres, Redis, full-stack `start-staging` |
| `make` | any | Workflow entry point |
| AWS account | — | For real scans — a personal sandbox account or read-only role |

## First-day setup

```bash
git clone https://github.com/ahmd-soliman/axiaops.git
cd axiaops

cp services/ingestion/.env.example services/ingestion/.env
$EDITOR services/ingestion/.env   # AWS credentials — make start-dev refuses without them

# 32-byte hex encryption key, shared between api and ingestion
openssl rand -hex 32   # paste into both .env files as ENCRYPTION_KEY

make start-dev
```

This brings up Postgres in Docker plus `api`/`ingestion` as host-mode Go
processes and the Vite dev server for the dashboard on `:5173`. With
`DEV_MODE=true` you skip auth entirely — the dev user lands directly in the
dashboard. `make stop` tears everything down.

### `start-dev` vs `start-staging`

|  | `start-dev` | `start-staging` |
|---|---|---|
| Mode | Host-mode Go binaries | Full docker-compose stack |
| Auth | Bypassed (`DEV_MODE=true`) | Native cookie auth |
| Redis | Not started | Sessions, queue, rate-limit |
| Use when | Day-to-day coding, tight feedback loop | Auth flows, Redis features, container parity, OIDC ceremony |

Spend most of your time in `start-dev`. In `DEV_MODE=true`, ingestion runs
against a fake AWS provider instead of real AWS calls — pick a scenario with
`DEV_SCENARIO` (`startup` default, `enterprise` for a realistic multi-service
mix, `all-zombies`/`no-zombies` for testing dismiss/empty-state flows).

## Code conventions

**Go** — always wrap errors with `fmt.Errorf("context: %w", err)`; `slog`
for logging, never `log.Printf`; explicit names, no abbreviations beyond
`ctx`/`err`/`tx`/`mux`; a `Handler` struct with `New(store) → Register(mux) →
method-per-route`; always `writeJSON(w, data)` for responses, never raw
`json.NewEncoder`; `storage.WithOrganizationID(ctx, id)` before any DB call —
the Postgres store errors fast if it's missing; `defer tx.Rollback(ctx)`
immediately after `Begin()`.

**React/Dashboard** — every color through `useTheme()`, never a hardcoded
hex; one API client seam (`api/client.js`), no direct `fetch`; one
screen = one file under `src/screens/` or `src/pages/`.

**Comments** — only when the *why* is non-obvious. Well-named identifiers
already say what the code does.

## Common workflows

**Adding an API endpoint**: add a handler method on `Handler`
(`services/api/internal/api/handler.go`), register it in `Register(mux)`,
pull the org from context, add a `Store` interface method if you need new
data access, write a test with `httptest.NewRecorder` + the mock Store.

**Adding a DB migration**: new `NNN_description.{up,down}.sql` pair under
`services/shared/storage/postgres/migrations/`. Per-organization tables need
`organization_id` + a row-level-security policy. Migrations run on service
startup and are recorded in `axiaops.migration_history` with a forensic row
per event (file checksum, build identity, timing) — editing an
already-applied migration triggers drift detection.

**Adding a detection rule**: add the service's metric to
`serviceRules` in the analyzer, write a golden fixture under
`services/shared/analyzer/testdata/golden/`, generate the expected output
with `UPDATE_GOLDEN=1 go test ./services/shared/analyzer/...`, and review
before committing — the generated file *is* the spec.

## Testing

```bash
make test                # unit tests across the workspace
make test-storage        # Postgres integration (RLS, migrations)
make test-integration    # isolated docker-compose stack, full API/ingestion suites
make build-production    # catches DEV_MODE-leak regressions
```

Standard `testing` package only, no third-party assertion libraries;
black-box tests (`package foo_test`); mock external services via interfaces.
Full strategy — including why AWS calls are tested against a real dedicated
test account rather than an emulator — in [Testing](testing/).

## Submitting work

```bash
git checkout -b feat/short-name        # or fix/, chore/, docs/
# ... commit work ...
git push -u origin feat/short-name
gh pr create --base main --title "feat(scope): tight summary"
```

Commit messages: `type(scope): subject`, body explains *why* not *what*.
PR description: summary + test plan.

## Versioning & releases

[Semantic Versioning 2.0.0](https://semver.org/spec/v2.0.0.html), no `v`
prefix. Pre-release suffixes: `-alpha.N` (may change without notice),
`-beta.N`, `-rc.N`, no suffix for a stable cut. `1.0.0` is reserved for the
first stable release.

Cutting one: update `CHANGELOG.md` (move `[Unreleased]` into a dated
section), tag `main` with an **annotated** tag (`git tag -a X.Y.Z -m "..."`),
push the tag. CI validates it, waits for the commit's images to finish
building/testing, then promotes the already-tested `main-<shortsha>` images
to the release tag with a manifest copy (not a rebuild) and cuts a GitHub
Release. Never retag — if a tag is wrong, cut the next one and note the skip
in the CHANGELOG.
