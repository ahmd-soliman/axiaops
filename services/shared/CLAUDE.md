# CLAUDE.md — Shared Module

## Purpose

Core library shared between API and ingestion services. Contains domain models, storage
interface, PostgreSQL/SQLite implementations, analyzer (detection logic), crypto, and logging.
No AWS SDK dependency — cloud-specific code lives in the ingestion service.

## Module

`axiaops.io/shared` — Go module at `services/shared/`. Imported by both `api` and `ingestion`.

## Package Map

| Package | Responsibility |
|---------|---------------|
| `model/` | Domain types: Tenant, User, Account, CostRecord, GhostResource, ResourceRecord |
| `storage/` | `Store` interface + `WithTenantID()` / `TenantIDFromCtx()` context helpers |
| `storage/postgres/` | Production Store impl — PostgreSQL with RLS, migrations |
| `storage/sqlite/` | Test-only Store impl — throwaway per-test databases |
| `analyzer/` | `Detect()`, `Summarize()`, `AnnotateAll()` — pure functions, no I/O |
| `crypto/` | AES-256-GCM encrypt/decrypt for account secrets |
| `logging/` | `Init(service)` — configures `log/slog` with JSON/text output, Sentry |

## Store Interface

The `Store` interface in `storage/storage.go` is the single contract for data access.
All methods accept `context.Context` — tenant ID must be set via `WithTenantID()` before
any call. PostgreSQL RLS enforces this at the DB level.

Key methods: `SaveCostRecords`, `SaveGhostRecords`, `LoadGhosts`, `Summary`,
`SaveAccount`, `ListAccounts`, `GetAccount`, `DeleteAccount`, `TryMarkAccountScanning`,
`UpsertTenant`, `UpsertUser`.

When adding new data access, add to this interface first, then implement in both
`postgres/` and `sqlite/` (sqlite can stub with `return nil, nil` if not needed for tests).

## Analyzer

Pure functions in `analyzer/detector.go`:

- `Detect(costs, usage)` — joins on `resource_id`, applies `serviceRules` thresholds
- `Summarize(ghosts)` — total savings + per-service breakdown
- `AnnotateAll(costs, usage, ghosts)` — marks each cost record as ghost or active

Detection rules are a module-level map `serviceRules`. Owner is derived from the `team` tag.
Resources with no matching rule or no usage data are skipped (not flagged).

## PostgreSQL Conventions

- Migrations in `storage/postgres/migrations/` — `NNN_name.up.sql` / `NNN_name.down.sql`
- Two DB users: `axiaops_owner` (runs migrations, creates schema) and `axiaops` (app user, RLS-limited)
- Schema: `axiaops` (not public) — set via `SET search_path TO axiaops`
- RLS policy: `tenant_id = current_setting('app.tenant_id', true)` on all data tables
- Connection pool: `pgxpool.Pool` — pass `DATABASE_URL` for app, `MIGRATION_DATABASE_URL` for migrations
- Transactions: `BEGIN` → `SET app.tenant_id` → operations → `COMMIT`. Always `defer tx.Rollback()`.

## Adding New Tables

1. Create migration files: `NNN_description.up.sql` and `NNN_description.down.sql`
2. Add RLS policy: `CREATE POLICY ... USING (tenant_id = current_setting('app.tenant_id', true))`
3. Add methods to `Store` interface in `storage/storage.go`
4. Implement in `storage/postgres/postgres.go`
5. Add stub in `storage/sqlite/sqlite.go`
6. Write integration test in `storage/postgres/postgres_test.go`

## Crypto

`crypto.Encrypt(key, plaintext)` / `crypto.Decrypt(key, ciphertext)` — AES-256-GCM.
Key is a 32-byte hex string from `ENCRYPTION_KEY` env var.
Generate with: `openssl rand -hex 32`

## Logging

`logging.Init(service)` must be called once at service startup. Configures:
- JSON output (production) or text (dev) — via `LOG_OUTPUT` or `DEV_MODE`
- Log level via `LOG_LEVEL` (debug/info/warn/error, default: info)
- Sentry integration via `SENTRY_DSN` (disabled when empty)
- Auto-attaches `service`, `env` (`APP_ENV`), `version` (`APP_VERSION`) to all logs

## Testing

```bash
cd services/shared && go test ./...                              # unit tests (SQLite)
cd services/shared && go test ./storage/postgres/... -count=1    # integration (needs running PostgreSQL)
```

Integration tests require env vars: `TEST_DATABASE_URL` and `TEST_STORE_URL`.
The Makefile handles this: `make test-postgres`.
