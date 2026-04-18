.PHONY: start-dev start-dev-redis start-staging stop seed seed-dev seed-staging seed-remote-dev seed-remote-staging inspect-db clean-db test test-shared test-api test-ingestion test-postgres test-all test-smoke test-smoke-api test-smoke-redis test-liveness

# Postgres credentials — override via env vars for non-dev environments.
POSTGRES_PASSWORD ?= axiaops
POSTGRES_OWNER_PASSWORD ?= axiaops_owner

# Postgres integration test URLs (used by services/shared/storage/postgres tests).
MIGRATION_DATABASE_URL ?= postgres://axiaops_owner:$(POSTGRES_OWNER_PASSWORD)@localhost:5432/axiaops?sslmode=disable
DATABASE_URL ?= postgres://axiaops:$(POSTGRES_PASSWORD)@localhost:5432/axiaops?sslmode=disable

# Integration test URLs
INTEGRATION_API_URL ?= http://localhost:8080
INTEGRATION_REDIS_URL ?= redis://localhost:6379
INTEGRATION_INGESTION_URL ?= http://localhost:8081

# Integration test URLs
INTEGRATION_API_URL ?= http://localhost:8080
INTEGRATION_REDIS_URL ?= redis://localhost:6379
INTEGRATION_INGESTION_URL ?= http://localhost:8081

# Stop local processes, free ports, and stop Postgres if running.
stop:
	docker rm -f axiaops-dev-redis 2>/dev/null || true
	./scripts/start.sh stop

# Start all services in dev mode (bypass auth with fixed tenant).
# Always runs `stop` first so ports and stale processes are cleared.
# Run `make seed` once after first start to populate dummy data.
start-dev: stop
	./scripts/start.sh

# Like start-dev but also spins up a local Redis container and passes REDIS_URL.
# Enables rate limiting and the scan queue worker locally.
start-dev-redis: stop
	docker run -d --rm --name axiaops-dev-redis -p 6379:6379 redis:7-alpine 2>/dev/null || true
	REDIS_URL=redis://localhost:6379 ./scripts/start.sh

# Staging: real Kinde JWT auth + real AWS data (no seed needed).
# Always runs `stop` first.
start-staging: stop
	docker run -d --rm --name axiaops-dev-redis -p 6379:6379 redis:7-alpine 2>/dev/null || true
	DEV_MODE=false REDIS_URL=redis://localhost:6379 ./scripts/start.sh

# Seed the dev tenant with dummy ghost + resource records.
# Safe to re-run — all inserts are idempotent.
# Starts PostgreSQL automatically if not already running.
seed:
	./scripts/seed_test_data.sh

# Seed dev environment via exposed port (localhost:5432).
# Uses direct psql connection instead of docker exec.
seed-dev:
	DATABASE_URL="postgres://axiaops_owner:$(POSTGRES_OWNER_PASSWORD)@localhost:5432/axiaops?sslmode=disable" \
	./scripts/seed_test_data.sh dev

# Seed staging environment via exposed port (localhost:5432).
# Uses direct psql connection instead of docker exec.
seed-staging:
	DATABASE_URL="postgres://axiaops_owner:$(POSTGRES_OWNER_PASSWORD)@localhost:5432/axiaops?sslmode=disable" \
	./scripts/seed_test_data.sh staging

# Seed remote dev database (via SSH) - copies script to remote and executes it.
# Seeds the same comprehensive data as local seed-dev but on NAS.local axiaops-dev-db.
seed-remote-dev:
	@echo "Seeding remote dev database on NAS.local..."
	./scripts/seed_remote_dbs.sh dev

# Seed remote staging database (via SSH) - copies script to remote and executes it.
# Seeds the same comprehensive data as local seed-staging but on NAS.local axiaops-staging-db.
seed-remote-staging:
	@echo "Seeding remote staging database on NAS.local..."
	./scripts/seed_remote_dbs.sh staging

inspect-db:
	./scripts/inspect_db.sh

# ── Local Database Cleanup ────────────────────────────────────────────────────

# Clean local dev database (truncate tables, preserve schema).
clean-db:
	docker compose exec -T postgres psql -U axiaops_owner -d axiaops -c \
		"TRUNCATE TABLE axiaops.ghost_snapshots, axiaops.resource_records, axiaops.ghost_records, axiaops.cost_records, axiaops.accounts, axiaops.users, axiaops.tenants CASCADE RESTART IDENTITY;" \
		2>/dev/null || true

# Clean local dev database (drop schema and user — destructive).
clean-db-drop:
	docker compose exec -T postgres psql -U axiaops_owner -d axiaops -c \
		"DROP SCHEMA IF EXISTS axiaops CASCADE; DROP USER IF EXISTS axiaops;" \
		2>/dev/null || true
	@echo "Local dev schema and user dropped. Run migrations to recreate."

# ── Remote Database Cleanup ───────────────────────────────────────────────────

# Clean remote dev database (truncate tables, preserve schema).
clean-remote-dev:
	@echo "Cleaning remote dev database on NAS.local..."
	./scripts/clean_remote_dbs.sh dev

# Clean remote staging database (truncate tables, preserve schema).
clean-remote-staging:
	@echo "Cleaning remote staging database on NAS.local..."
	./scripts/clean_remote_dbs.sh staging

# Clean remote dev database (drop schema and user — destructive).
clean-remote-dev-drop:
	@echo "Dropping remote dev schema on NAS.local..."
	./scripts/clean_remote_dbs.sh dev --drop-schema

# Clean remote staging database (drop schema and user — destructive).
clean-remote-staging-drop:
	@echo "Dropping remote staging schema on NAS.local..."
	./scripts/clean_remote_dbs.sh staging --drop-schema

# Per-service test targets — each mirrors the matching CI job (test:shared, test:api, test:ingestion).
# Running one target locally reproduces exactly what CI runs for that job.

# Shared module: business logic, models, crypto, analyzer (excludes Postgres integration tests).
test-shared:
	cd services/shared && go test ./... -count=1 -parallel 4 -skip=Postgres $(ARGS)

# API service: handler unit tests (MockStore) + lifecycle unit tests (no external deps).
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
	docker compose up -d --wait postgres
	$(MAKE) clean-db
	cd services/shared && MIGRATION_DATABASE_URL="$(MIGRATION_DATABASE_URL)" DATABASE_URL="$(DATABASE_URL)" go test -count=1 -v -p=1 ./storage/postgres/...

# Full test suite: unit tests + API integration tests + storage tests.
test-all: test test-postgres

# Integration tests - require Redis and PostgreSQL
test-integration: start-dev-redis
	cd test/integration && GOWORK=off INTEGRATION_API_URL=$(INTEGRATION_API_URL) INTEGRATION_REDIS_URL=$(INTEGRATION_REDIS_URL) INTEGRATION_INGESTION_URL=$(INTEGRATION_INGESTION_URL) go test -v . -count=1 $(ARGS)

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

# Test that services stay alive on their own for 60 seconds (no signal sent).
# Catches bugs where services self-terminate due to context timeouts or similar.
# Starts a fresh stack, waits 60 seconds, checks both health endpoints, then stops.
test-liveness:
	@echo "Starting services..."
	$(MAKE) start-dev
	@echo "Waiting 60 seconds to verify services do not self-terminate..."
	sleep 60
	@echo "Checking API health..."
	@curl -sf http://localhost:8080/health > /dev/null || (echo "FAIL: API died on its own" && $(MAKE) stop && exit 1)
	@echo "Checking ingestion health..."
	@curl -sf http://localhost:8081/health > /dev/null || (echo "FAIL: ingestion died on its own" && $(MAKE) stop && exit 1)
	@echo "PASS: services still running after 60 seconds"
	$(MAKE) stop

.PHONY: lint
lint:
	cd services/api && golangci-lint run ./... --timeout=5m
	cd services/ingestion && golangci-lint run ./... --timeout=5m
	cd services/shared && golangci-lint run ./... --timeout=5m
