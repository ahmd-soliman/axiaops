# Cost records: upsert on conflict (fix late-settled day-1 amounts)

## Status

Planning. Implementation MR to follow once this design lands.

## Reviewer questions

Three items the design fixes a position on but worth confirming on review:

1. **`internal_account_id` update direction.** Recommendation: `COALESCE(EXCLUDED.internal_account_id, cost_records.internal_account_id)` so a fresh re-fetch never clobbers a populated legacy value with NULL. Confirm.
2. **One-shot `DAYS_BACK` bump for backfill.** Recommendation: temporarily set `DAYS_BACK=90` on staging/preview/dev for the first post-deploy scan, then revert. Production skipped (pre-revenue). Confirm.
3. **`fetched_at` semantics.** Assumed to track scan freshness — always overwritten. Grep did not surface any "first seen" reader of this column; flag if one exists.

## Problem

Vantage reports $23.93 for May 2026; AxiaOps reports $20.94 — a -12.5% gap concentrated on day 1 of the month. AWS posts late-arriving recurring/amortized charges (RDS, ELB, ECS, ElastiCache reservations and SP true-ups) to the first day of the billing period for several days after month-rollover. AxiaOps re-fetches a rolling 30-day window every scan, but `SaveCostRecords` (`services/shared/storage/postgres/postgres.go:134-150`) uses `ON CONFLICT … DO NOTHING`, so the freshest amount from AWS is silently discarded in favour of the row written by the first scan that touched that day.

The metric (`NetAmortizedCost`) and the negative-amount skip at `aws.go:369` are correct and unchanged. The bug is exclusively in how repeated fetches are reconciled.

## Decision

Change the conflict clause to `DO UPDATE`. The conflict key — `(organization_id, provider, account_id, service, region, resource_id, period_start, period_end)` — stays frozen. Update on conflict:

- `amount` — the whole point of the fix.
- `currency` — refreshed for symmetry; AWS will not change it but treating it as part of the same "value pair" as `amount` avoids a stale-currency edge case if a CE account ever flips display unit.
- `tags` — late tag mutations on resource-level rows should be reflected; cheap to overwrite.
- `fetched_at` — must update so operators can see scan freshness on a given row.
- `internal_account_id` — update with `COALESCE(EXCLUDED.internal_account_id, cost_records.internal_account_id)`: every re-fetched row populates it (`main.go:427`), but legacy rows may have NULL and we never want a new NULL to clobber a populated value.

Stay frozen: the eight conflict-key columns (by definition) and `id` (PostgreSQL keeps the original row's primary key under `ON CONFLICT DO UPDATE` — important because any future FK from another table to `cost_records.id` would survive). No table currently references `cost_records.id`; `dismissed_zombies` keys on a resource-identity fingerprint, not a `cost_records` row.

## Side-effects audit

- **`zombie_records` / `resource_records`** — replaced wholesale every scan from the latest cost+usage join (`SaveZombies` deletes per-account then inserts). The fix unblocks them: today they are computed from stale amounts. No change needed in their writers.
- **`zombie_snapshots` / `zombie_snapshot_services`** — append-only by design (`SaveSnapshot` never updates). They are point-in-time records of "what we believed on scan day X" and should NOT retroactively change. After the fix, fresh snapshots will be accurate; past snapshots stay as they were. The trend chart shows history-as-believed, not history-as-corrected.
- **`dismissed_zombies`** — keys on a `(organization, account, service, resource_id)`-style fingerprint, no `cost_records.id` reference. A retroactive amount change does not affect a dismissal's identity or expiry. The dismissed-savings figure surfaced through `enrichWithDismissals` updates naturally on next scan because it is recomputed from current zombie rows.
- **API endpoints (`/summary`, `/trend`, `/resources`, `/costs`)** — all read live. The only API caches (`internal/auth/session_cache.go`, `middleware/ratelimit.go`) cover sessions and rate-limit counters, not derived cost data. No staleness window to manage.
- **RLS** — `SaveCostRecords` already runs inside a tx with `SET app.organization_id`; `DO UPDATE` inherits the `USING` clause from the per-table policy, and migration 011 added the `WITH CHECK` (migration 016 renamed `tenant_id` to `organization_id`) so updates cannot cross-write rows in a different org. Verify in the integration test.

## Backfill strategy

Three options for the existing stale rows in dev-1 / dev-2 / staging / preview (production has no customer data yet — pre-revenue):

- **(a) Do nothing.** Next scan's `DAYS_BACK=30` window overwrites the most recent 30 days. Days 31+ stay stale forever. Acceptable because the primary user-visible aggregate (`/summary`) is a current-state savings figure derived from `zombie_records` (which gets fully rebuilt every scan); the older `cost_records` rows mostly back the historical `/costs` view.
- **(b) One-shot env bump.** Temporarily set `DAYS_BACK=90` on the next scheduled or manual scan per env, then revert. One extra Cost Explorer call per service-day in the extended window — bounded, cheap, no code change. AWS CE history limit is 12 months so 90 days is well inside it.
- **(c) SQL backfill.** Not viable — values must be re-fetched from CE.

**Recommendation: (b)** for staging / preview / dev to validate the fix produces the expected Vantage-aligned numbers. Production: skip — no customer data exists yet, and (a) covers everything from the moment the fix ships. Document the one-shot in the implementation MR description; do not add a permanent admin endpoint.

## Risk assessment

- **RLS:** `DO UPDATE` honours the policy's `USING` and the `WITH CHECK` added by migration 011. The integration test must assert that an UPDATE attempted from a different org's session does not mutate the target row.
- **Transactional consistency:** The existing per-record loop inside one tx is preserved; semantics are unchanged at the tx boundary.
- **Validation invariants:** `model.CostRecord.Validate()` is called by callers, not by `Save`. The upsert can only persist rows that round-tripped through `FetchCosts` (which never produces invalid records today and skips `amount ≤ 0`). Strict-validation invariants do not interact with the conflict path.
- **Idempotency:** Two identical re-runs of the same fetch produce the same final row. A re-run where AWS has revised the amount produces the latest AWS value. Both behaviours are intended.
- **Metric integrity:** `axiaops_ingestion_records_saved_total{status="inserted"}` currently counts `RowsAffected` from `INSERT … ON CONFLICT DO NOTHING`, which is 0 on conflict. Under `DO UPDATE` it returns 1 for both insert and update. Decide either (i) rename the `"inserted"` label to `"upserted"` (one-line change at `main.go:437` + `:466`), or (ii) split into `INSERT … RETURNING (xmax = 0)` to keep insert/update counts distinct. Recommend (i): the split is over-engineering for current needs and no Grafana dashboard is committed to the repo that would key on the old label.
- **Operator log line:** `slog.Info("fetched records", …, "inserted", inserted, "skipped", skipped)` at `main.go:435` (mirrored at `:464`) derives `skipped = len(records) - inserted`. Under `DO UPDATE` every conflict counts as `inserted`, so `skipped` will read `0` on every re-scan even when most rows were updates rather than first-time inserts. Rename the slog keys in the same patch as the metric label rename (`"inserted" → "upserted"`, drop the now-meaningless `"skipped"`) so operators reading scan logs are not misled.

## Test strategy

Add to `services/shared/storage/postgres/postgres_test.go` (integration tier, `make test-storage`):

1. **`TestSaveCostRecords_UpsertWinsLatest`** — call `Save` with one record, call again with the same conflict key but a different `amount`, assert `ListCostRecords` returns the second amount and one row (no duplicate).
2. **`TestSaveCostRecords_UpsertPreservesID`** — capture `id` after first insert (via direct SELECT), upsert, re-read, assert id unchanged.
3. **`TestSaveCostRecords_UpsertPreservesInternalAccountID`** — first row has `internal_account_id` set, second-write payload has it NULL; assert the stored value remains the original (COALESCE behaviour).
4. **`TestSaveCostRecords_UpsertRLSIsolation`** — write a row in org A, attempt `Save` of the same conflict-key shape from a tx bound to org B; assert org A's row is unchanged and org B sees its own row independently.
5. Adjust whatever existing assertion expects the duplicate-skip path to return `RowsAffected = 0`; under `DO UPDATE` it will return `1` with the same final amount.

Unit tests in `services/ingestion/cmd/` do not touch SQL semantics and need no changes.

## Rollout

No feature flag. Ship straight to dev-1 → preview → staging → production via the existing manual `deploy:*` gates. Reasons:

- The change is one SQL clause; rollback is trivial (revert one commit, redeploy).
- No customer data on production (pre-revenue).
- The change strictly increases data accuracy; no failure mode produces worse output than today's silent stale-row retention.
- No schema migration — the conflict key was finalised in migration 020.

Sequence: docs MR (this doc) → review → implementation MR (one `postgres.go` change + the metric label rename + integration tests) → land on `develop` → click `deploy:dev-1`, verify a manual scan shows the updated amounts on the day-1 rows of the current month → roll forward through preview and staging → production. On staging / preview, run the (b) one-shot `DAYS_BACK=90` scan before declaring done and capture a before / after comparison against the user's Vantage screenshot in the implementation MR.

## Out of scope

- **CUR (Cost and Usage Reports) integration.** Separate, much larger initiative. Stays in Cost Explorer land.
- **Per-resource backfill via `FetchResourceCosts`.** Same upsert change applies transitively because both fetchers share the same `Save` path; no extra work needed.
- **Splitting insert vs update counters.** Label rename only.
- **Changing the conflict-key composition.** Migration 020 is correct and stable.
- **Negative-amount handling.** `aws.go:369` stays as-is per its existing comment.
