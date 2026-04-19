.PHONY: start-dev start-dev-redis start-staging stop migrate test-migrate seed seed-trends seed-remote-dev seed-remote-staging seed-remote-dev-trends seed-remote-staging-trends inspect-db clean-db test test-shared test-api test-ingestion test-storage test-all test-liveness

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
start-dev: stop migrate
	./scripts/start.sh

# Like start-dev but also spins up a local Redis container and passes REDIS_URL.
# Enables rate limiting and the scan queue worker locally.
start-dev-redis: stop migrate
	docker run -d --rm --name axiaops-dev-redis -p 6379:6379 redis:7-alpine 2>/dev/null || true
	REDIS_URL=redis://localhost:6379 ./scripts/start.sh

# Staging: real Kinde JWT auth + real AWS data (no seed needed).
# Always runs `stop` first.
start-staging: stop migrate
	docker run -d --rm --name axiaops-dev-redis -p 6379:6379 redis:7-alpine 2>/dev/null || true
	DEV_MODE=false REDIS_URL=redis://localhost:6379 ./scripts/start.sh

# Run database migrations using dedicated migration container
migrate:
	@echo "Running database migrations..."
	docker-compose up -d postgres
	@echo "Waiting for PostgreSQL to be ready..."
	@until docker-compose exec postgres pg_isready -U axiaops_owner -d axiaops > /dev/null 2>&1; do sleep 1; done
	./scripts/migrate.sh

# Test the migration container (uses test-infra compose — no host port binding)
test-migrate:
	@echo "Testing migration container..."
	$(eval PG_CONTAINER := axiaops-migrate-test-pg-$(shell date +%s))
	$(eval TEST_NETWORK := $(if $(RUNNER_NETWORK),$(RUNNER_NETWORK),axiaops-migrate-test-net))
	docker rm -f $(PG_CONTAINER) 2>/dev/null || true
	$(if $(RUNNER_NETWORK),,docker network create $(TEST_NETWORK) 2>/dev/null || true)
	docker run -d --name $(PG_CONTAINER) --network $(TEST_NETWORK) \
		-e POSTGRES_DB=axiaops \
		-e POSTGRES_USER=axiaops_owner \
		-e POSTGRES_PASSWORD=$(POSTGRES_OWNER_PASSWORD) \
		postgres:16-alpine
	@until docker exec $(PG_CONTAINER) pg_isready -U axiaops_owner -d axiaops > /dev/null 2>&1; do sleep 1; done
	docker build -t axiaops-migrate-test -f services/migrate/Dockerfile .
	docker run --rm --network $(TEST_NETWORK) \
		-e MIGRATION_DATABASE_URL="postgres://axiaops_owner:$(POSTGRES_OWNER_PASSWORD)@$(PG_CONTAINER):5432/axiaops?sslmode=disable" \
		-e DATABASE_URL="postgres://axiaops:$(POSTGRES_PASSWORD)@$(PG_CONTAINER):5432/axiaops?sslmode=disable" \
		axiaops-migrate-test
	docker rm -f $(PG_CONTAINER)
	$(if $(RUNNER_NETWORK),,docker network rm $(TEST_NETWORK) 2>/dev/null || true)

# Seed the dev tenant with dummy ghost + resource records.
# Safe to re-run — all inserts are idempotent.
# Starts PostgreSQL automatically if not already running.
seed:
	./scripts/seed_test_data.sh

# Seed with realistic trend data for chart development (90 days with gradual trends + weekly patterns).
# Use this when developing time-series charts and graphs.
seed-trends:
	./scripts/seed_test_data.sh --with-trends

# Seed remote dev database (192.168.1.100:5432)
seed-remote-dev:
	./scripts/seed_test_data.sh --remote dev

# Seed remote staging database (192.168.1.100:5433)
seed-remote-staging:
	./scripts/seed_test_data.sh --remote staging

# Seed remote dev with trends
seed-remote-dev-trends:
	./scripts/seed_test_data.sh --remote dev --with-trends

# Seed remote staging with trends
seed-remote-staging-trends:
	./scripts/seed_test_data.sh --remote staging --with-trends

inspect-db:
	./scripts/inspect_db.sh

# ── Local Database Cleanup ────────────────────────────────────────────────────

# Clean local dev database (truncate tables, preserve schema).
clean-db:
	./scripts/clean_db.sh

# Clean local dev database (drop schema and user — destructive).
clean-db-drop:
	./scripts/clean_db.sh --drop-schema

# ── Remote Database Cleanup ───────────────────────────────────────────────────

# Clean remote dev database (truncate tables, preserve schema).
clean-remote-dev:
	./scripts/clean_db.sh --remote dev

# Clean remote staging database (truncate tables, preserve schema).
clean-remote-staging:
	./scripts/clean_db.sh --remote staging

# Clean remote dev database (drop schema and user — destructive).
clean-remote-dev-drop:
	./scripts/clean_db.sh --remote dev --drop-schema

# Clean remote staging database (drop schema and user — destructive).
clean-remote-staging-drop:
	./scripts/clean_db.sh --remote staging --drop-schema

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
test-storage:
	docker-compose up -d --wait postgres
	$(MAKE) clean-db
	cd services/shared && MIGRATION_DATABASE_URL="$(MIGRATION_DATABASE_URL)" DATABASE_URL="$(DATABASE_URL)" go test -count=1 -v -p=1 ./storage/postgres/...

# Full test suite: unit tests + API integration tests + storage tests.
test-all: test test-storage

# Integration tests - self-contained (starts Docker Compose stack)
test-integration:
	cd test-infra/integration && docker-compose up --build --exit-code-from tests tests
	cd test-infra/integration && docker-compose down -v --remove-orphans
	cd test-infra/integration && docker-compose rm -f 2>/dev/null || true

# API integration tests only
test-integration-api:
	cd test-infra/integration && docker-compose down -v --remove-orphans 2>/dev/null || true
	cd test-infra/integration && docker-compose build migrate api ingestion
	cd test-infra/integration && docker-compose up -d postgres redis
	cd test-infra/integration && docker-compose run --rm api-tests
	cd test-infra/integration && docker-compose down -v --remove-orphans
	cd test-infra/integration && docker-compose rm -f 2>/dev/null || true

# Ingestion integration tests only  
test-integration-ingestion:
	cd test-infra/integration && docker-compose down -v --remove-orphans 2>/dev/null || true
	cd test-infra/integration && docker-compose build migrate ingestion
	cd test-infra/integration && docker-compose up -d postgres redis
	cd test-infra/integration && docker-compose run --rm ingestion-tests
	cd test-infra/integration && docker-compose down -v --remove-orphans
	cd test-infra/integration && docker-compose rm -f 2>/dev/null || true

# Clean up Docker resources from integration tests and other AxiaOps containers
clean-docker:
	@echo "Cleaning up AxiaOps Docker resources..."
	./scripts/force_clean_docker_resources.sh

# Clean up integration test resources specifically
clean-integration:
	@echo "Cleaning up integration test resources..."
	cd test-infra/integration && docker-compose down -v --remove-orphans 2>/dev/null || true
	docker network prune -f --filter label=com.docker.compose.project=integration-test 2>/dev/null || true

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
