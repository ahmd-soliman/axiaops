.PHONY: start-dev start-staging stop seed inspect-db clean-db test test-shared test-api test-ingestion test-postgres test-all test-smoke

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

inspect-db:
	./scripts/inspect_db.sh

clean-db:
	docker compose exec -T postgres psql -U axiaops_owner -d axiaops -c \
		"TRUNCATE TABLE axiaops.ghost_snapshots, axiaops.resource_records, axiaops.ghost_records, axiaops.cost_records, axiaops.accounts, axiaops.users, axiaops.tenants CASCADE;" \
		2>/dev/null || true

# Per-service test targets — each mirrors the matching CI job (test:shared, test:api, test:ingestion).
# Running one target locally reproduces exactly what CI runs for that job.

# Shared module: business logic, models, crypto, analyzer (excludes Postgres integration tests).
test-shared:
	cd services/shared && go test ./... -count=1 -parallel 4 -skip=Postgres $(ARGS)

# API service: handler unit tests (MockStore) + lifecycle integration tests.
test-api:
	cd services/api && go test ./... -count=1 -parallel 4 $(ARGS)

# Ingestion service: unit tests.
test-ingestion:
	cd services/ingestion && go test ./... -count=1 -parallel 4 $(ARGS)

# Rollup: all unit/integration tests without a database (mirrors running all three CI jobs above).
test: test-shared test-api test-ingestion

# PostgreSQL storage layer tests (database required, runs sequentially).
# Tests:
#   • Real PostgreSQL database operations
#   • Row-Level Security (RLS) policies
#   • Schema migrations
test-postgres:
	docker compose up -d postgres
	sleep 2
	$(MAKE) clean-db
	cd services/shared && TEST_DATABASE_URL="$(TEST_DATABASE_URL)" TEST_STORE_URL="$(TEST_STORE_URL)" go test -count=1 -v -p=1 ./storage/postgres/...

# Full test suite: unit tests + API integration tests + storage tests.
test-all: test test-postgres

# Smoke tests: requires a running stack. Run 'make start-dev' first.
# Override the URL with: SMOKE_API_URL=https://staging.example.com make test-smoke
SMOKE_API_URL ?= http://localhost:8080
test-smoke:
	cd test/smoke && SMOKE_API_URL=$(SMOKE_API_URL) go test -v ./... -count=1 $(ARGS)

# Test graceful shutdown: start services, send SIGTERM, verify clean exit.
test-shutdown:
	@echo "Starting services..."
	./scripts/start.sh &
	SERVICE_PID=$$!
	sleep 3
	@echo "Services running. Press Ctrl+C or wait 10 seconds for automatic SIGTERM..."
	sleep 10
	@echo "Sending SIGTERM to services..."
	kill -SIGTERM $$SERVICE_PID 2>/dev/null || true
	wait $$SERVICE_PID 2>/dev/null || true
	@echo "Graceful shutdown test complete. Check logs above for 'shutdown complete' message."

.PHONY: lint
lint:
	cd services/api && golangci-lint run ./... --timeout=5m
	cd services/ingestion && golangci-lint run ./... --timeout=5m
	cd services/shared && golangci-lint run ./... --timeout=5m
