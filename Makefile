.PHONY: start stop seed check test

start:
	./scripts/dev.sh

start-aws:
	./scripts/dev.sh --aws

stop:
	./scripts/dev.sh stop

seed:
	./scripts/seed_test_data.sh

check:
	./scripts/check_db.sh

test:
	cd services/api && go test ./...
	cd services/ingestion && go test ./...
	cd services/shared && go test ./...
