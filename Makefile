.PHONY: start-dev start-staging stop seed check test test-postgres test-all

# Postgres integration test URLs (used by services/shared/storage/postgres tests).
TEST_DATABASE_URL ?= postgres://axiaops_owner:axiaops_owner@localhost:5432/axiaops?sslmode=disable
TEST_STORE_URL ?= postgres://axiaops:axiaops@localhost:5432/axiaops?sslmode=disable

# Stop local processes, free ports, and stop Postgres if running.
stop:
	./scripts/start.sh stop

# Start all services in dev mode (DEV_MODE=true, no auth, fixed tenant).
# Always runs `stop` first so ports and stale processes are cleared.
# Run `make seed` once after first start to populate dummy data.
start-dev: stop
	./scripts/start.sh

# Staging: real Kinde JWT auth + real AWS data (no seed needed).
# Always runs `stop` first.
start-staging: stop
	DEV_MODE=false ./scripts/start.sh

# Seed the dev tenant with dummy ghost + resource records.
# Safe to re-run — all inserts are idempotent.
# Starts PostgreSQL automatically if not already running.
seed:
	./scripts/seed_test_data.sh

check:
	./scripts/check_db.sh

test:
	cd services/api && go test ./...
	cd services/ingestion && go test ./...
	cd services/shared && go test ./...

# Run Postgres integration tests (migrations + RLS/tenant isolation tests).
test-postgres:
	docker compose up -d postgres
	cd services/shared && TEST_DATABASE_URL="$(TEST_DATABASE_URL)" TEST_STORE_URL="$(TEST_STORE_URL)" go test -v ./storage/postgres/...

# Full suite: unit tests + Postgres integration tests.
test-all: test test-postgres
