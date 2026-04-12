# Implementation Summary: 2.12 Scheduled Auto-Scan (Phase 1)

**Date:** April 12, 2026  
**Status:** ✅ Steps 1 & 2 Complete

---

## What Was Implemented

### Step 1: Database Migration ✅

**Files created:**
- `services/shared/storage/postgres/migrations/004_add_scan_interval.up.sql`
- `services/shared/storage/postgres/migrations/004_add_scan_interval.down.sql`

**Change:** Added `scan_interval_hours` column to `accounts` table
- Data type: `INTEGER NOT NULL DEFAULT 24`
- Default value: 24 hours (daily auto-scan)
- Migration follows versioned pattern with up/down migrations
- RLS grants preserved for app user

**Migration behavior:**
- Forward (up): Adds column with default value of 24
- Backward (down): Removes column safely

---

### Step 2: Account Model Update ✅

**File:** `services/shared/model/account.go`

**Changes:**
```go
type Account struct {
    // ... existing fields ...
    ScanIntervalHours int        `json:"scan_interval_hours"` // auto-scan interval in hours; default 24
    // ... rest of struct ...
}
```

**Key details:**
- Field is public, exported in JSON responses
- Default value of 24 hours (set by DB migration)
- Ready for PATCH endpoint to update per-account intervals

---

### Step 3: PostgreSQL Store Layer Updates ✅

**File:** `services/shared/storage/postgres/postgres.go`

**Methods updated:**
1. **SaveAccount** — Includes `scan_interval_hours` in INSERT and upsert logic
2. **ListAccounts** — Fetches `scan_interval_hours` in SELECT
3. **GetAccount** — Retrieves `scan_interval_hours` by account ID

**All three methods now:**
- SELECT/insert the `scan_interval_hours` column
- Properly scan/bind the value to the `Account` struct
- Support updates via the upsert ON CONFLICT clause

---

### Step 4: Test Suite Updates ✅

**File:** `services/shared/storage/postgres/postgres_test.go`

**Changes:**

1. **testAccount helper function** — Updated to include:
   ```go
   ScanIntervalHours: 24,  // default
   ```

2. **New test function: TestAccount_ScanIntervalHours**
   - Tests saving account with custom `scan_interval_hours` (12 hours)
   - Verifies GetAccount retrieves correct value
   - Tests upsert: updating same account with new interval (6 hours)
   - Confirms update is persisted correctly

**Test coverage:**
- Default interval (24 hours) from helper
- Custom intervals (12, 6 hours) in new test
- Round-trip persistence (save → get)
- Update via SaveAccount (upsert)

---

## Files Modified/Created

| File | Change | Lines |
|------|--------|-------|
| `migrations/004_add_scan_interval.up.sql` | ✨ NEW | ~12 |
| `migrations/004_add_scan_interval.down.sql` | ✨ NEW | ~7 |
| `model/account.go` | Updated | +1 field |
| `storage/postgres/postgres.go` | Updated | SaveAccount, ListAccounts, GetAccount |
| `storage/postgres/postgres_test.go` | Updated | testAccount helper, +1 new test |

---

## Next Steps (Phase 2 of 2.12)

### Step 3: PATCH /v1/accounts/{id} Endpoint
- Update `label`, `region`, `scan_interval_hours`
- Store service to update Account fields
- Handler in `services/api/internal/api/accounts.go`

### Step 4: Background Ticker
- Add ticker in `services/api/cmd/main.go`
- Runs every 60 minutes
- Query accounts where `last_scanned_at < NOW() - INTERVAL '{scan_interval_hours} hours'`
- Skip accounts already in `scanning` status
- Fire HTTP POST to `http://ingestion:8081/scan`
- Log `scan.scheduled` and `scan.skipped_already_running`

### Step 5: Dashboard Updates
- Show next scheduled scan time per account in accounts bar
- Calculate: `last_scanned_at + INTERVAL '{scan_interval_hours} hours'`

### Step 6: Testing
- Integration test with `scan_interval_hours=0` (immediate)
- Verify ticker triggers scan within 60 seconds

---

## Database Schema

### Before
```sql
CREATE TABLE accounts (
    id                TEXT        PRIMARY KEY,
    tenant_id         TEXT        NOT NULL REFERENCES tenants(id),
    provider          TEXT        NOT NULL DEFAULT 'aws',
    label             TEXT        NOT NULL DEFAULT '',
    access_key_id     TEXT        NOT NULL DEFAULT '',
    secret_encrypted  TEXT        NOT NULL DEFAULT '',
    region            TEXT        NOT NULL DEFAULT 'us-east-1',
    status            TEXT        NOT NULL DEFAULT 'connected',
    last_scanned_at   TIMESTAMPTZ,
    created_at        TIMESTAMPTZ NOT NULL
);
```

### After
```sql
CREATE TABLE accounts (
    id                TEXT        PRIMARY KEY,
    tenant_id         TEXT        NOT NULL REFERENCES tenants(id),
    provider          TEXT        NOT NULL DEFAULT 'aws',
    label             TEXT        NOT NULL DEFAULT '',
    access_key_id     TEXT        NOT NULL DEFAULT '',
    secret_encrypted  TEXT        NOT NULL DEFAULT '',
    region            TEXT        NOT NULL DEFAULT 'us-east-1',
    status            TEXT        NOT NULL DEFAULT 'connected',
    last_scanned_at   TIMESTAMPTZ,
    scan_interval_hours INTEGER   NOT NULL DEFAULT 24,  -- ← NEW
    created_at        TIMESTAMPTZ NOT NULL
);
```

---

## Verification

✅ Migration files created and follow versioned pattern  
✅ Account model struct includes new field with JSON tag  
✅ All PostgreSQL store methods updated (SaveAccount, ListAccounts, GetAccount)  
✅ Test helper includes new field  
✅ New test covers custom intervals and upsert behavior  
✅ Ready for migration runner to apply on next service startup

---

## Notes

- Default interval of 24 hours ensures backward compatibility (no disruption to existing accounts)
- Field is exposed in JSON responses for dashboard to display
- Migration is idempotent (safe to re-run if migration runner restarts)
- RLS policies remain in place — `app.tenant_id` still enforced
