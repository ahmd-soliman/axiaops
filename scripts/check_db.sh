#!/bin/bash
# check_db.sh — inspect the AxiaOps SQLite database after a test login.
# Usage: ./scripts/check_db.sh [path/to/axiaops.db]

DB=${1:-axiaops.db}

if [ ! -f "$DB" ]; then
  echo "Database not found: $DB"
  echo "Start the ingestion service first: DEV_MODE=true go run ./cmd/main.go"
  exit 1
fi

echo "=== Tenants ==="
sqlite3 "$DB" "SELECT id, org_code, name, created_at FROM tenants;"

echo ""
echo "=== Users ==="
sqlite3 "$DB" "SELECT id, tenant_id, kinde_sub, email, name, last_seen FROM users;"

echo ""
echo "=== Cost Records (count) ==="
sqlite3 "$DB" "SELECT COUNT(*) || ' records' FROM cost_records;"
