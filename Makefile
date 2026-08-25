.PHONY: start-dev start-staging start-debug stop migrate seed inspect-db clean-db clean-db-drop clean-db-files test test-shared test-api test-ingestion test-storage test-all test-liveness

# Postgres credentials — override via env vars for non-dev environments.
POSTGRES_PASSWORD ?= axiaops
POSTGRES_OWNER_PASSWORD ?= axiaops_owner
POSTGRES_RUNTIME_PASSWORD ?= axiaops_runtime

# Postgres integration test URLs (used by services/shared/storage/postgres tests).
MIGRATION_DATABASE_URL ?= postgres://axiaops_owner:$(POSTGRES_OWNER_PASSWORD)@localhost:5432/axiaops?sslmode=disable
DATABASE_URL ?= postgres://axiaops:$(POSTGRES_PASSWORD)@localhost:5432/axiaops?sslmode=disable

# Integration test URLs
INTEGRATION_API_URL ?= http://localhost:8080
INTEGRATION_REDIS_URL ?= redis://localhost:6379
INTEGRATION_INGESTION_URL ?= http://localhost:8081

# Stop local processes, free ports, and stop the Docker stack.
# Kills host-mode Go services from `start-dev` AND brings down any
# docker compose services from `start-staging`.
# Both axiaops-dev-redis (legacy name) and axiaops-dev-valkey (post-migration
# name) are listed so a checkout that straddles the Redis→Valkey swap leaves
# no stranded container behind. Drop the redis-named line after two release
# cycles per the migration plan rollback window.
stop:
	docker rm -f axiaops-dev-redis 2>/dev/null || true
	docker rm -f axiaops-dev-valkey 2>/dev/null || true
	docker compose down 2>/dev/null || true
	./scripts/start.sh stop

# Fast dev loop — host-mode Go services (API + ingestion + Vite dashboard)
# against a local Postgres container. DEV_MODE=true → auth bypassed, fixed tenant.
# This is the default for day-to-day coding; use `make start-staging` when you
# need the full containerised stack with native auth and Redis.
# Run `make seed` once after first start to populate dummy data.
start-dev: stop migrate
	./scripts/start.sh

# Full stack in Docker with native auth on and Redis (rate limiting +
# scan queue worker). Mirrors the deployed environment — use this when
# debugging auth flows, Redis-backed features, or container parity issues.
# Dashboard is served by nginx on plain HTTP at http://localhost:8082;
# TLS termination is the edge proxy's job in real deployments (CloudFront in
# front of the prod ECS Express ALB, customer ingress, an edge proxy in front of
# dev/staging) and is intentionally absent locally. The session cookie is
# non-Secure when accessed over plain HTTP, which is correct for
# direct-port access.
# Always runs `stop` first so host-mode services and stale containers are cleared.
start-staging: stop migrate
	DEV_MODE=false docker compose up --build -d
	@echo ""
	@echo "Full Docker stack starting (DEV_MODE=false, native auth on)."
	@echo "  API:        http://localhost:8080  (direct, for curl)"
	@echo "  Ingestion:  internal only (axiaops-ingestion:8081)"
	@echo "  Dashboard:  http://localhost:8082  ← use this in the browser"
	@echo "  Bootstrap:  http://localhost:8082/bootstrap"
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
	docker compose up -d postgres
	@echo "Waiting for PostgreSQL to be ready..."
	@until docker compose exec postgres pg_isready -U axiaops_owner -d axiaops > /dev/null 2>&1; do sleep 1; done
	./scripts/migrate.sh

# Seed the dev tenant with dummy zombie + resource records.
# Safe to re-run — all inserts are idempotent.
# Starts PostgreSQL automatically if not already running.
# Includes 90 days of realistic trend data (upward trend + weekly patterns).
seed:
	./scripts/seed_test_data.sh

# Multi-org demo seed. On top of `seed`, populates the Acme + Globex orgs
# with copies of the dev seed data and wires the dev user as owner of all
# three so the org switcher exercises end-to-end against the local stack.
seed-demo:
	./scripts/seed_test_data.sh --demo

inspect-db:
	./scripts/inspect_db.sh

# ── Local Database Cleanup ────────────────────────────────────────────────────

# Clean local dev database (truncate tables, preserve schema).
clean-db:
	./scripts/clean_db.sh

# Clean local dev database (drop schema and user — destructive).
clean-db-drop:
	./scripts/clean_db.sh --drop-schema

# Wipe pg_data on disk so Postgres creates a fresh database on next start.
# Use this when clean-db / clean-db-drop aren't enough — e.g. to re-arm the
# bootstrap install token, or after a botched migration that left the cluster
# in an unrecoverable state. The `stop` dependency guarantees no container is
# still holding the volume open at delete time.
#
# No sudo on macOS: Docker Desktop virtualises file permissions, so files the
# container's postgres uid wrote round-trip as the host user. On native Linux
# Docker the files come back root-owned and you'd need sudo — re-add if/when
# the contributor base broadens beyond macOS.
clean-db-files: stop
	@echo "Deleting pg_data… Postgres will create a fresh database on next start."
	rm -rf pg_data

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
		postgres:17.5-alpine
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
		RUNTIME_ADMIN_DATABASE_URL="postgres://axiaops_runtime:$(POSTGRES_RUNTIME_PASSWORD)@localhost:5433/axiaops?sslmode=disable" \
		go test -count=1 -v -p=1 ./storage/postgres/...
	docker rm -f $(PG_CONTAINER)
	$(if $(RUNNER_NETWORK),,docker network rm $(TEST_NETWORK) 2>/dev/null || true)

# Full test suite: unit tests + API integration tests + storage tests.
test-all: test test-storage

# Integration tests - self-contained (starts Docker Compose stack)
test-integration:
	cd test-infra/integration && docker compose up --build --exit-code-from tests tests
	cd test-infra/integration && docker compose down -v --remove-orphans
	cd test-infra/integration && docker compose rm -f 2>/dev/null || true

# API integration tests only
test-integration-api:
	cd test-infra/integration && docker compose down -v --remove-orphans 2>/dev/null || true
	cd test-infra/integration && docker compose build migrate init-organization api ingestion api-tests
	cd test-infra/integration && docker compose up -d postgres redis && \
		docker compose exec -T postgres pg_isready -U axiaops_owner -d axiaops > /dev/null 2>&1 || \
		(for i in {1..30}; do docker compose exec -T postgres pg_isready -U axiaops_owner -d axiaops > /dev/null 2>&1 && break; sleep 1; done)
	cd test-infra/integration && docker compose run --rm api-tests
	cd test-infra/integration && docker compose down -v --remove-orphans
	cd test-infra/integration && docker compose rm -f 2>/dev/null || true

# SSO OIDC ceremony integration tests (services/api/internal/sso/oidc_integration_test.go).
# Drives a full authorization-code + PKCE round-trip against an in-process
# minimal OIDC issuer, asserting JIT membership + JWKS auto-refresh-on-rotation.
# Uses the lightweight docker-compose.test.yml stack — Postgres only, no api
# or mock-IdP container needed (the test stands the API up in-process for
# httptest control over signing keys).
test-integration-sso:
	cd test-infra/integration && docker compose -f docker-compose.test.yml down -v --remove-orphans 2>/dev/null || true
	cd test-infra/integration && docker compose -f docker-compose.test.yml up -d postgres
	cd test-infra/integration && for i in {1..30}; do \
		docker compose -f docker-compose.test.yml exec -T postgres pg_isready -U axiaops_owner -d axiaops > /dev/null 2>&1 && break; \
		sleep 1; \
	done
	cd services/api && \
		INTEGRATION_DATABASE_URL='postgres://axiaops:axiaops@localhost:5532/axiaops?sslmode=disable' \
		INTEGRATION_DATABASE_OWNER_URL='postgres://axiaops_owner:axiaops_owner@localhost:5532/axiaops?sslmode=disable' \
		go test -tags=integration -count=1 -run TestOIDC -v ./internal/sso/...
	cd test-infra/integration && docker compose -f docker-compose.test.yml down -v --remove-orphans

# Ingestion integration tests only
test-integration-ingestion:
	cd test-infra/integration && docker compose down -v --remove-orphans 2>/dev/null || true
	cd test-infra/integration && docker compose build migrate init-organization ingestion
	cd test-infra/integration && docker compose up -d postgres redis && \
		docker compose exec -T postgres pg_isready -U axiaops_owner -d axiaops > /dev/null 2>&1 || \
		(for i in {1..30}; do docker compose exec -T postgres pg_isready -U axiaops_owner -d axiaops > /dev/null 2>&1 && break; sleep 1; done)
	cd test-infra/integration && docker compose run --rm ingestion-tests
	cd test-infra/integration && docker compose down -v --remove-orphans
	cd test-infra/integration && docker compose rm -f 2>/dev/null || true

# E2E regression suite — Playwright against a full DEV_MODE stack.
#
# Brings up postgres + migrate + api + ingestion (DEV_MODE) + the dashboard
# (nginx image, /api proxied to api:8080), seeds dummy data, then runs the
# Playwright suite (services/dashboard/e2e) against the nginx-served dashboard
# at BASE_URL=http://dashboard. The compose dependency graph orders it all:
# playwright waits on dashboard (healthy) + seed (completed). `run --rm`
# starts those dependencies first, then runs the suite in the foreground and
# surfaces its exit code — the same shape as test-integration-*. Using `run`
# (not `up`) avoids the one-shot migrate/seed containers exiting 0 from tearing
# the stack down mid-suite (which `--abort-on-container-exit` would do).
#
# Always tears the stack down (`down -v`) even on failure so a flaky run
# doesn't leave volumes behind. Locally you can instead point Playwright at
# `make start-dev` after `make seed`:
#   cd services/dashboard && BASE_URL=http://localhost:5173 npm run e2e
test-e2e:
	cd test-infra/e2e && docker compose down -v --remove-orphans 2>/dev/null || true
	@# DOCKER_BUILDKIT=0: the playwright image's one network RUN step (the pinned
	@# `npm install` — psql is now COPY-only, no apt) needs DNS, and the legacy
	@# builder resolves via the docker daemon's network (works when the LAN resolver
	@# is up) — buildkit's separate build sandbox has been seen DNS-less on the CI
	@# runner. Local dev is unaffected (it has DNS either way).
	cd test-infra/e2e && DOCKER_BUILDKIT=0 docker compose build
	cd test-infra/e2e && docker compose run --rm playwright; \
		status=$$?; \
		if [ $$status -ne 0 ]; then \
			echo "=== e2e failed (exit $$status) — container state + logs before teardown ==="; \
			docker compose ps || true; \
			for svc in postgres migrate api ingestion dashboard playwright; do \
				echo "--- logs $$svc ---"; docker compose logs --no-color --tail=120 $$svc 2>&1 || true; \
			done; \
		fi; \
		docker compose down -v --remove-orphans 2>/dev/null || true; \
		docker compose rm -f 2>/dev/null || true; \
		exit $$status

# E2E regression suite — CI variant that REUSES the registry images already built
# by `build:images` in the same pipeline (api/ingestion/migrate/dashboard) instead
# of rebuilding all four from source. ~21min → mostly the suite runtime; the four
# from-scratch image builds are gone.
#
# Layers docker-compose.ci.yml over the base file: the override pins each app
# service to ${REGISTRY}/<svc>:${IMAGE_TAG}. We `pull` those four FIRST (which
# fetches + tags them locally under those names), then build ONLY the playwright
# runner (it's not in build:images), then run the suite. Because the four images
# are already present locally after the pull, compose never rebuilds them.
#
# Requires a prior `docker login ${CI_REGISTRY}` and REGISTRY/IMAGE_TAG set — the
# e2e:regression job does both. Do NOT use this locally: it has a hard registry
# dependency. Local devs use `make test-e2e` (builds everything from source).
#
# DOCKER_BUILDKIT=0 is scoped to the playwright build ONLY — its single network
# `npm install` RUN needs the legacy builder's DNS (see test-e2e). The pulled
# images don't build, so they don't need it.
E2E_CI_COMPOSE := docker compose -f docker-compose.yml -f docker-compose.ci.yml
test-e2e-ci:
	cd test-infra/e2e && $(E2E_CI_COMPOSE) down -v --remove-orphans 2>/dev/null || true
	cd test-infra/e2e && $(E2E_CI_COMPOSE) pull migrate ingestion api dashboard
	cd test-infra/e2e && DOCKER_BUILDKIT=0 $(E2E_CI_COMPOSE) build playwright
	cd test-infra/e2e && $(E2E_CI_COMPOSE) run --rm playwright; \
		status=$$?; \
		if [ $$status -ne 0 ]; then \
			echo "=== e2e failed (exit $$status) — container state + logs before teardown ==="; \
			$(E2E_CI_COMPOSE) ps || true; \
			for svc in postgres migrate api ingestion dashboard playwright; do \
				echo "--- logs $$svc ---"; $(E2E_CI_COMPOSE) logs --no-color --tail=120 $$svc 2>&1 || true; \
			done; \
		fi; \
		$(E2E_CI_COMPOSE) down -v --remove-orphans 2>/dev/null || true; \
		$(E2E_CI_COMPOSE) rm -f 2>/dev/null || true; \
		exit $$status

# Clean up Docker resources from integration tests and other AxiaOps containers
clean-docker:
	@echo "Cleaning up AxiaOps Docker resources..."
	./scripts/force_clean_docker_resources.sh

# Clean up integration test resources specifically
clean-integration:
	@echo "Cleaning up integration test resources..."
	cd test-infra/integration && docker compose down -v --remove-orphans 2>/dev/null || true
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

# B1.7 layer 3 (plan §4.10.2): customer-shipping binary build. The
# `production` build tag activates services/{api,ingestion}/cmd/devmode_production.go
# whose devModeEnabled() returns false unconditionally — DEV_MODE is read
# but ignored. The default `make build`-style targets stay tag-less so
# local dev keeps the env-var bypass for fast iteration.
#
# Confirms locally what the CI image build does via Dockerfile's BUILD_TAGS
# arg. Exit code is the only signal — either both binaries compile and the
# layer-3 stripping took effect, or one of the build-tag-gated files is
# malformed and you find out before pushing.
.PHONY: build-production
build-production:
	# Build the package (`./cmd/`), not the single file (`./cmd/main.go`),
	# so sibling files like devmode_production.go are included — without
	# this `go build` would compile main.go in isolation and fail with
	# "undefined: devModeEnabled". The api Dockerfile uses the same shape
	# for the same reason.
	cd services/api && go build -tags production -o /tmp/axiaops-api-production ./cmd/
	cd services/ingestion && go build -tags production -o /tmp/axiaops-ingestion-production ./cmd/
	@echo "production-tagged binaries built — DEV_MODE is no-op in /tmp/axiaops-{api,ingestion}-production"

# axiaopsctl is the operator CLI for migrate up/down/force/drift/history.
# See docs/migration-history-table-design.md §Operator UX.
.PHONY: axiaopsctl migration-history migration-history-drift
axiaopsctl:
	cd services/shared && go build -o ../../bin/axiaopsctl ./cmd/migrate/

migration-history: axiaopsctl
	@MIGRATION_DATABASE_URL=$(MIGRATION_DATABASE_URL) bin/axiaopsctl migrate history $(V)

migration-history-drift: axiaopsctl
	@MIGRATION_DATABASE_URL=$(MIGRATION_DATABASE_URL) bin/axiaopsctl migrate drift
