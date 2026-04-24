# PostgreSQL Integration Tests

Tests in `postgres_test.go` run against a real PostgreSQL instance and cover the full `Store` interface.

## Environment Variables

| Variable | Description |
|---|---|
| `MIGRATION_DATABASE_URL` | Owner/admin connection URL used for migrations (`axiaops_owner`). Required to run any test. |
| `DATABASE_URL` | App user connection URL used for the store (`axiaops`). Optional — enables RLS isolation tests. |

If `MIGRATION_DATABASE_URL` is not set, all tests are skipped.  
If `DATABASE_URL` is not set, RLS isolation tests are skipped (see below).

## Running Locally

Start PostgreSQL:

```bash
docker compose up -d postgres
```

Run all tests including RLS isolation:

```bash
MIGRATION_DATABASE_URL="postgres://axiaops_owner:axiaops_owner@localhost:5432/axiaops?sslmode=disable" \
DATABASE_URL="postgres://axiaops:axiaops@localhost:5432/axiaops?sslmode=disable" \
go test ./storage/postgres/...
```

Run without RLS isolation tests (superuser only):

```bash
MIGRATION_DATABASE_URL="postgres://axiaops_owner:axiaops_owner@localhost:5432/axiaops?sslmode=disable" \
go test ./storage/postgres/...
```

## Two-URL Design

Migrations require DDL permissions (`CREATE TABLE`, `ALTER`, etc.) so they run as `axiaops_owner` (a PostgreSQL superuser). The store at runtime connects as `axiaops`, a non-superuser, which means Row-Level Security (RLS) is enforced.

**Why superusers bypass RLS**: PostgreSQL superusers always skip RLS policies regardless of `FORCE ROW LEVEL SECURITY`. If the store connected as `axiaops_owner`, one tenant could read another tenant's data.

## Test Isolation

Each test creates a unique tenant via `uuid.New()` and uses a context carrying that tenant ID. RLS then filters all queries to that tenant's data automatically — no table truncation needed between tests.

## What Is Tested

| Group | Tests | RLS required |
|---|---|---|
| `Save` (cost records) | Insert, deduplication, empty batch, region uniqueness, missing tenant | No |
| `SaveZombies` / `LoadZombies` | Roundtrip, replace-on-rerun, empty for new tenant | Partial |
| Tenant isolation (zombies) | Tenant B cannot see Tenant A's zombies | Yes |
| `UpsertTenant` | Create, idempotent ID, name update | No |
| `UpsertUser` | Create on first login, same ID on second login | No |
| Account CRUD | Save+list, get by ID, delete, status update, tenant isolation | Partial |
| `SaveResources` / `LoadResources` | Roundtrip, replace-on-rerun | No |
