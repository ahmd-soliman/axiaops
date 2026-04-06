.PHONY: start-dev start-staging stop seed check test

# Start all services in dev mode (DEV_MODE=true, no auth, fixed tenant).
# Run `make seed` once after first start to populate dummy data.
start-dev:
	./scripts/start.sh

# Start all services in staging mode: real Kinde JWT auth + real AWS data (no seed needed).
start-staging:
	DEV_MODE=false ./scripts/start.sh

stop:
	./scripts/start.sh stop

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
