.PHONY: start-dev start-staging start-debug stop migrate seed seed-remote-dev-1 seed-remote-dev-2 seed-remote-staging inspect-db clean-db clean-db-drop clean-remote-dev-1 clean-remote-dev-2 clean-remote-staging clean-remote-dev-1-drop clean-remote-dev-2-drop clean-remote-staging-drop test test-shared test-api test-ingestion test-storage test-all test-liveness

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

# Stop local processes, free ports, and stop the Docker stack.
# Kills host-mode Go services from `start-dev` AND brings down any
# docker-compose services from `start-staging`.
stop:
	docker rm -f axiaops-dev-redis 2>/dev/null || true
	docker compose down 2>/dev/null || true
	./scripts/start.sh stop

# Fast dev loop — host-mode Go services (API + ingestion + Vite dashboard)
# against a local Postgres container. DEV_MODE=true → auth bypassed, fixed tenant.
# This is the default for day-to-day coding; use `make start-staging` when you
# need the full containerised stack with Kinde auth and Redis.
# Run `make seed` once after first start to populate dummy data.
start-dev: stop migrate
	./scripts/start.sh

# Full stack in Docker with Kinde JWT auth on and Redis (rate limiting +
# scan queue worker). Mirrors the deployed environment — use this when
# debugging auth flows, Redis-backed features, or container parity issues.
# Dashboard is served by nginx on :8082 (the Vite dev server is not started).
# Always runs `stop` first so host-mode services and stale containers are cleared.
start-staging: stop migrate
	DEV_MODE=false docker compose up --build -d
	@echo ""
	@echo "Full Docker stack starting (DEV_MODE=false, Kinde auth on)."
	@echo "  API:        http://localhost:8080"
	@echo "  Ingestion:  internal only (axiaops-ingestion:8081)"
	@echo "  Dashboard:  http://localhost:8082"
	@echo "  Logs:       docker compose logs -f"
	@echo "  Stop:       make stop"

# Start only the infrastructure (Postgres + migrations) needed to debug
# Go services under VS Code F5 / Delve. Does NOT start API, Ingestion, or
# Vite — F5 launches Go under Delve; run Vite separately for frontend work.
# Clears any process (host or container) bound to :8080/:8081 so Delve can
# bind those ports. Postgres stays up if already running.
start-debug: migrate
	@echo "Clearing Go service ports for F5..."
	@docker rm -f axiaops-api axiaops-ingestion 2>/dev/null || true
	@for port in 8080 8081; do \
		pids=$$(lsof -ti :$$port 2>/dev/null || true); \
		if [ -n "$$pids" ]; then \
			echo "  Killing process(es) on port $$port: $$pids"; \
			echo "$$pids" | xargs kill -9 2>/dev/null || true; \
		fi; \
	done
	@echo ""
	@echo "Postgres up and migrated. Hit F5 in VS Code to debug Go services."
	@echo "For dashboard: cd services/dashboard && npm run dev"

# Run database migrations using dedicated migration container
migrate:
	@echo "Running database migrations..."
	docker-compose up -d postgres
	@echo "Waiting for PostgreSQL to be ready..."
	@until docker-compose exec postgres pg_isready -U axiaops_owner -d axiaops > /dev/null 2>&1; do sleep 1; done
	./scripts/migrate.sh

# Seed the dev tenant with dummy zombie + resource records.
# Safe to re-run — all inserts are idempotent.
# Starts PostgreSQL automatically if not already running.
# Includes 90 days of realistic trend data (upward trend + weekly patterns).
seed:
	./scripts/seed_test_data.sh

# Seed remote dev-1 database (axiaops.local:5432 — axiaops-dev-1-db).
seed-remote-dev-1:
	./scripts/seed_test_data.sh --remote dev-1

# Seed remote dev-2 database (axiaops.local:5433 — axiaops-dev-2-db).
seed-remote-dev-2:
	./scripts/seed_test_data.sh --remote dev-2

# Seed remote staging database (axiaops.local:5442 — axiaops-staging-db).
seed-remote-staging:
	./scripts/seed_test_data.sh --remote staging

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
# Remote ports match the deploy stack/apps/axiaops-dbs/docker-compose.yml:
#   dev-1   → axiaops.local:5432
#   dev-2   → axiaops.local:5433
#   staging → axiaops.local:5442

# Truncate tables (preserve schema).
clean-remote-dev-1:
	./scripts/clean_db.sh --remote dev-1

clean-remote-dev-2:
	./scripts/clean_db.sh --remote dev-2

clean-remote-staging:
	./scripts/clean_db.sh --remote staging

# Drop schema and user (destructive — requires re-running migrations).
clean-remote-dev-1-drop:
	./scripts/clean_db.sh --remote dev-1 --drop-schema

clean-remote-dev-2-drop:
	./scripts/clean_db.sh --remote dev-2 --drop-schema

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
# Uses isolated container (not docker-compose) for hermetic test runs.
test-storage:
	@echo "Running storage tests with isolated PostgreSQL container..."
	$(eval PG_CONTAINER := axiaops-storage-test-pg-$(shell date +%s))
	$(eval TEST_NETWORK := $(if $(RUNNER_NETWORK),$(RUNNER_NETWORK),axiaops-storage-test-net))
	docker rm -f $(PG_CONTAINER) 2>/dev/null || true
	$(if $(RUNNER_NETWORK),,docker network create $(TEST_NETWORK) 2>/dev/null || true)
	docker run -d --name $(PG_CONTAINER) --network $(TEST_NETWORK) -p 5433:5432 \
		-e POSTGRES_DB=axiaops \
		-e POSTGRES_USER=axiaops_owner \
		-e POSTGRES_PASSWORD=$(POSTGRES_OWNER_PASSWORD) \
		postgres:16-alpine
	@echo "Waiting for PostgreSQL to be ready..."
	@timeout=60; elapsed=0; \
	until docker exec $(PG_CONTAINER) pg_isready -U axiaops_owner -d axiaops > /dev/null 2>&1; do \
		if [ $$elapsed -ge $$timeout ]; then \
			echo "PostgreSQL failed to start within $${timeout}s"; \
			docker logs $(PG_CONTAINER); \
			docker rm -f $(PG_CONTAINER); \
			exit 1; \
		fi; \
		sleep 1; \
		elapsed=$$((elapsed + 1)); \
	done
	@echo "PostgreSQL ready"
	cd services/shared && \
		MIGRATION_DATABASE_URL="postgres://axiaops_owner:$(POSTGRES_OWNER_PASSWORD)@localhost:5433/axiaops?sslmode=disable" \
		DATABASE_URL="postgres://axiaops:$(POSTGRES_PASSWORD)@localhost:5433/axiaops?sslmode=disable" \
		go test -count=1 -v -p=1 ./storage/postgres/...
	docker rm -f $(PG_CONTAINER)
	$(if $(RUNNER_NETWORK),,docker network rm $(TEST_NETWORK) 2>/dev/null || true)

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
	cd test-infra/integration && docker-compose build migrate init-organization api ingestion
	cd test-infra/integration && docker-compose up -d postgres redis && \
		docker-compose exec -T postgres pg_isready -U axiaops_owner -d axiaops > /dev/null 2>&1 || \
		(for i in {1..30}; do docker-compose exec -T postgres pg_isready -U axiaops_owner -d axiaops > /dev/null 2>&1 && break; sleep 1; done)
	cd test-infra/integration && docker-compose run --rm api-tests
	cd test-infra/integration && docker-compose down -v --remove-orphans
	cd test-infra/integration && docker-compose rm -f 2>/dev/null || true

# Ingestion integration tests only
test-integration-ingestion:
	cd test-infra/integration && docker-compose down -v --remove-orphans 2>/dev/null || true
	cd test-infra/integration && docker-compose build migrate init-organization ingestion
	cd test-infra/integration && docker-compose up -d postgres redis && \
		docker-compose exec -T postgres pg_isready -U axiaops_owner -d axiaops > /dev/null 2>&1 || \
		(for i in {1..30}; do docker-compose exec -T postgres pg_isready -U axiaops_owner -d axiaops > /dev/null 2>&1 && break; sleep 1; done)
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
