# Cost records: upsert on conflict (fix late-settled day-1 amounts)

## Status

Planning. Implementation MR to follow once this design lands.

## Reviewer questions

One item worth confirming on review:

1. **`fetched_at` semantics.** Assumed to track scan freshness — always overwritten. Grep did not surface any "first seen" reader of this column; flag if one exists.

(Earlier drafts asked about a one-shot `DAYS_BACK=90` backfill and the `internal_account_id` COALESCE direction. Both are resolved below — the backfill is dropped as cosmetic, the COALESCE is kept as defence for migration-010-era NULL rows only.)

## Problem

Vantage reports $23.93 for May 2026; AxiaOps reports $20.94 — a -12.5% gap concentrated on day 1 of the month. AWS posts late-arriving recurring/amortized charges (RDS, ELB, ECS, ElastiCache reservations and SP true-ups) to the first day of the billing period for several days after month-rollover. AxiaOps re-fetches a rolling 30-day window every scan, but `SaveCostRecords` (`services/shared/storage/postgres/postgres.go:134-150`) uses `ON CONFLICT … DO NOTHING`, so the freshest amount from AWS is silently discarded in favour of the row written by the first scan that touched that day.

The metric (`NetAmortizedCost`) and the negative-amount skip at `aws.go:369` are correct and unchanged. The bug is exclusively in how repeated fetches are reconciled.

## Alternatives considered

| Approach | What it gives | Why rejected |
|---|---|---|
| **`DO UPDATE`** (chosen) | One SQL clause; freshest amount always wins. | Loses the "what did we think on day X" question — but `zombie_snapshots` covers that for aggregates, and `/costs` is conceptually "current best estimate" (industry-standard for cost data). |
| **Append-with-`fetched_at`-in-conflict-key** | Full settlement curve queryable per row; full audit. | Every `/costs` SELECT becomes `DISTINCT ON (conflict_key) ORDER BY fetched_at DESC`. Storage grows ~30×. The "what did we think on day X" use case is already covered by snapshots. Cost outweighs benefit at this stage. |
| **Delete + reinsert last N days each scan** | Simple, no upsert semantics. | Loses `created_at` / first-seen metadata. Indistinguishable from upsert from the outside, with worse RLS posture (DELETE policies are more permissive than UPDATE in some configs). |
| **Permanent admin reconcile endpoint** | User-driven freshness on demand. | Pushes a policy decision (refresh frequency) onto the user; the scan loop already re-fetches a 30-day window — solving it server-side is the right layer. |
| **Do nothing + disclaim** | Zero code change. | A footnote does not fix the underlying numbers in `/costs`. The user diff-checks against Vantage; a disclaimer does not close that loop. |

## Decision

Change the conflict clause to `DO UPDATE`. The conflict key — `(organization_id, provider, account_id, service, region, resource_id, period_start, period_end)` — stays frozen. Update on conflict:

- `amount` — the whole point of the fix.
- `currency` — refreshed for symmetry; AWS will not change it but treating it as part of the same "value pair" as `amount` avoids a stale-currency edge case if a CE account ever flips display unit.
- `tags` — late tag mutations on resource-level rows should be reflected; cheap to overwrite.
- `fetched_at` — must update so operators can see scan freshness on a given row.
- `internal_account_id` — update with `COALESCE(EXCLUDED.internal_account_id, cost_records.internal_account_id)`. Steady-state writes always populate it (`main.go:427` sets the pointer for every fresh fetch), so the COALESCE branch never fires on those. It is load-bearing for **migration-010-era rows** (column added without `NOT NULL` and no default → pre-010 rows are NULL): an upsert against one of those rows must not clobber the legacy NULL with a different NULL on a code path that ever omits the field. Not cargo-cult defence; it protects exactly that backfill case.

Stay frozen: the eight conflict-key columns (by definition) and `id` (PostgreSQL keeps the original row's primary key under `ON CONFLICT DO UPDATE` — important because any future FK from another table to `cost_records.id` would survive). No table currently references `cost_records.id`; `dismissed_zombies` keys on a resource-identity fingerprint, not a `cost_records` row.

## Side-effects audit

- **`zombie_records` / `resource_records`** — replaced wholesale every scan from the latest cost+usage join (`SaveZombies` deletes per-account then inserts). The fix unblocks them: today they are computed from stale amounts. No change needed in their writers.
- **`zombie_snapshots` / `zombie_snapshot_services`** — append-only by design (`SaveSnapshot` never updates). They are point-in-time records of "what we believed on scan day X" and stay frozen. After the fix, `/costs` (live, reads `cost_records`) and the Trend chart (snapshot-backed) will diverge on days AWS late-settles: `/costs` reflects the corrected amount, Trend keeps the as-believed value. The divergence is bounded to the most recent month and to the few days AWS late-settles, and is accepted in this MR's scope. Tracked for follow-up under issue #122 (rolling-window snapshot recompute).
- **`dismissed_zombies`** — keys on a `(organization, account, service, resource_id)`-style fingerprint, no `cost_records.id` reference. A retroactive amount change does not affect a dismissal's identity or expiry. The dismissed-savings figure surfaced through `enrichWithDismissals` updates naturally on next scan because it is recomputed from current zombie rows.
- **API endpoints (`/summary`, `/trend`, `/resources`, `/costs`)** — all read live. The only API caches (`internal/auth/session_cache.go`, `middleware/ratelimit.go`) cover sessions and rate-limit counters, not derived cost data. No staleness window to manage.
- **RLS** — `SaveCostRecords` already runs inside a tx with `SET app.organization_id`; `DO UPDATE` inherits the `USING` clause from the per-table policy, and migration 011 added the `WITH CHECK` (migration 016 renamed `tenant_id` to `organization_id`) so updates cannot cross-write rows in a different org. Verify in the integration test.
- **Resource-level fetch path** (`main.go:453`, `FetchResourceCosts`) — writes through the same `Save`, but with a non-empty `resource_id` so it lands on a different conflict key than the aggregate path's `resource_id=''`. The resource-level rows were never affected by `DO NOTHING` collisions (different rows, different keys). The fix improves their re-fetch behaviour symmetrically; no extra design work needed.
- **Cost Explorer API self-cost** (`main.go:473`, `FetchCostExplorerAPICosts`) — appended to `allRecords` for the analyzer pass but **never persisted via `Save`**. This fix does not touch it. Flag if that absence is intentional vs a pre-existing bug; either way, out of scope here.
- **Retention** (`DeleteCostRecordsOlderThan` at `postgres.go:1188`, runs via the RLS-bypassing `adminPool`) — under upsert semantics, a re-fetched late-settled row could be silently re-inserted after retention has deleted it. Unlikely at current retention windows (well outside the 30-day fetch window) but worth flagging if retention is ever tightened.

## Backfill

**No special backfill step. The next normal scan repairs the visible damage.**

After the implementation MR deploys:

1. Next scan on each account fires (manual click or scheduled — controlled by `scan_interval_hours`).
2. The scan re-fetches the same 30-day window it always does.
3. AWS returns the now-settled `NetAmortizedCost` for each day in that window.
4. The new upsert clause overwrites each existing row's `amount` with the freshly-fetched value.
5. The last 30 days are now accurate. No operator action required.

Days older than 30 stay as first stored. Acceptable because:

- Days 31+ have long since settled at AWS, so the stored values are already close to correct (the under-count primarily affects day-1 of each month within the first ~3 days of that month).
- Internal envs (dev / staging / preview) have no real customers.
- Production has no customer data yet — pre-revenue.

An earlier draft of this plan recommended a one-shot `DAYS_BACK=90` bump to refresh days 31–90 on non-prod envs. Dropped: that bump only refreshes the most-settled, least-likely-to-be-wrong rows. It is cosmetic and conflates "validate the fix is working" (which the `inserted`/`updated` metric split below already gives) with "actually refresh data" (which days 31–90 do not meaningfully need).

## Risk assessment

- **RLS:** `DO UPDATE` honours the policy's `USING` and the `WITH CHECK` added by migration 011. The integration test must assert that an UPDATE attempted from a different org's session does not mutate the target row.
- **Transactional consistency:** The existing per-record loop inside one tx is preserved; semantics are unchanged at the tx boundary.
- **Validation invariants:** `model.CostRecord.Validate()` is called by callers, not by `Save`. The upsert can only persist rows that round-tripped through `FetchCosts` (which never produces invalid records today and skips `amount ≤ 0`). Strict-validation invariants do not interact with the conflict path.
- **Idempotency:** Two identical re-runs of the same fetch produce the same final row. A re-run where AWS has revised the amount produces the latest AWS value. Both behaviours are intended.
- **Metric integrity + customer-Grafana compat:** `axiaops_ingestion_records_saved_total{status="inserted"}` currently counts `RowsAffected` from `INSERT … ON CONFLICT DO NOTHING`, which is 0 on conflict. Under `DO UPDATE` it returns 1 for both insert and update — collapsing two distinct operational signals (fresh discovery vs steady-state refresh) into one. Use `INSERT … RETURNING (xmax = 0)` to discriminate and emit two counters:
  - `axiaops_ingestion_records_saved_total{status="inserted"}` — kept, semantics preserved (the row did not previously exist). Self-hosted customers with their own Grafana keep working.
  - `axiaops_ingestion_records_saved_total{status="updated"}` — added (the row existed and amount/tags/etc were refreshed). Tells operators a steady-state scan is doing real correction work.

  This is the rollout safety mechanism in lieu of a feature flag (see Rollout below).
- **Operator log line:** `slog.Info("fetched records", …, "inserted", inserted, "skipped", skipped)` at `main.go:435` (mirrored at `:464`) derives `skipped = len(records) - inserted`. Under `DO UPDATE` every conflict counts as `inserted` under the old code; with the split, count `inserted` and `updated` distinctly and drop the now-meaningless `skipped` key.
- **Concurrency / cross-worker race:** With Redis-backed scan queueing (`services/shared/queue/`) two ingestion workers can race on the same `(organization, account)` if scheduled-scan enumeration and a manual scan trigger overlap. Under `DO NOTHING` the loser was a no-op. Under `DO UPDATE` the loser blocks on the row lock then re-applies its own payload — last writer by **wall-clock arrival** wins, not by AWS-data freshness. In practice both writers fetched within seconds of each other from the same Cost Explorer endpoint, so their payloads converge; pathological divergence requires one worker to stall mid-`FetchCosts` while AWS late-settles. The existing per-process `sync.Mutex` scan-lock in `services/api/internal/api/handler.go` does NOT cover cross-pod ingestion — gap pre-exists this fix and is out of scope. Add a regression test (below) to pin last-writer-wins as the documented behaviour.

## Test strategy

Add to `services/shared/storage/postgres/postgres_test.go` (integration tier, `make test-storage`):

1. **`TestSaveCostRecords_UpsertWinsLatest`** — call `Save` with one record, call again with the same conflict key but a different `amount`, assert `ListCostRecords` returns the second amount and one row (no duplicate).
2. **`TestSaveCostRecords_UpsertPreservesID`** — capture `id` after first insert (via direct SELECT), upsert, re-read, assert id unchanged.
3. **`TestSaveCostRecords_UpsertPreservesInternalAccountID`** — first row has `internal_account_id` set, second-write payload has it NULL; assert the stored value remains the original (COALESCE behaviour).
4. **`TestSaveCostRecords_UpsertRLSIsolation`** — write a row in org A, attempt `Save` of the same conflict-key shape from a tx bound to org B; assert org A's row is unchanged and org B sees its own row independently.
5. **`TestSaveCostRecords_ConcurrentUpsertLastWriterWins`** — start two goroutines, each opening its own tx with `SET app.organization_id`, each upserting the same conflict-key shape with a different `amount`. Wait for both to commit. Assert `ListCostRecords` returns exactly one row, with whichever amount the later-committing tx wrote. Documents the wall-clock-arrival resolution behaviour explicitly.
6. **`TestSaveCostRecords_UpsertCountersSplit`** — assert that a first-time insert increments `axiaops_ingestion_records_saved_total{status="inserted"}` (and not `updated`), and that a second write with the same conflict key increments `{status="updated"}` (and not `inserted`).
7. Adjust whatever existing assertion expects the duplicate-skip path to return `RowsAffected = 0`; under `DO UPDATE` it will return `1` with the same final amount, but split between the two new label values.

Unit tests in `services/ingestion/cmd/` do not touch SQL semantics and need no changes.

## Rollout

No feature flag. The safety mechanism is **observability via the `inserted` / `updated` metric split** plus the existing per-env manual `deploy:*` gates. Rationale:

- The change is one SQL clause + a `RETURNING` discriminator; rollback is trivial (revert one commit, redeploy).
- No customer data on production (pre-revenue).
- The change strictly increases data accuracy; no failure mode produces worse output than today's silent stale-row retention.
- No schema migration — the conflict key was finalised in migration 020.
- A feature flag adds dead-weight code that is statistically left on indefinitely. The metric split gives the operator the same visibility (steady-state vs anomaly) without the cleanup cost.

Sequence: docs MR (this doc) → review → implementation MR (one `postgres.go` change + the `RETURNING (xmax = 0)` split + the slog key update + integration tests) → land on `develop` → click `deploy:dev-1`, verify a manual scan shows the updated amounts on the day-1 rows of the current month → observe `axiaops_ingestion_records_saved_total{status="updated"}` increments only by the late-settled days (single-digit count per scan; if it spikes into the thousands, something is wrong) → roll forward through preview and staging → production. Capture a before / after Vantage comparison in the implementation MR description as evidence the fix closed the user-reported gap.

## Out of scope

- **CUR (Cost and Usage Reports) integration.** Separate, much larger initiative. Stays in Cost Explorer land.
- **Snapshot rolling-window recompute.** Tracked under issue #122. The Trend/`/costs` divergence is accepted in this MR; the follow-up addresses it properly.
- **Cross-pod ingestion scan-lock.** Pre-existing gap (the per-process `sync.Mutex` does not cover Redis-queued workers in different pods). Surfaced by the concurrency analysis but not introduced by this fix.
- **`FetchCostExplorerAPICosts` persistence.** Currently not saved to `cost_records`; out of scope for this fix, flag as separate question.
- **Changing the conflict-key composition.** Migration 020 is correct and stable.
- **Negative-amount handling.** `aws.go:369` stays as-is per its existing comment.
