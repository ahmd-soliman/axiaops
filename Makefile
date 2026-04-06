.PHONY: start start-prod stop seed check test

# Start all services in dev mode (DEV_MODE=true, no auth, fixed tenant).
# Run `make seed` once after first start to populate dummy data.
start:
	./scripts/dev.sh

# Start all services in production mode (Kinde JWT auth enabled).
start-prod:
	DEV_MODE=false ./scripts/dev.sh

stop:
	./scripts/dev.sh stop

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
