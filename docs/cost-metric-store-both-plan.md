# Cost records: store both AmortizedCost and NetAmortizedCost (cost-vs-savings split)

## Status

Planning — parked until a customer has SP/RI/credits (no numeric effect on current internal account; verified identical metrics 2026-05-31). Implementation MR deferred until a connected account actually carries Savings Plans, Reserved Instances, or credits. No code lands on the strength of this doc alone.

## Reviewer questions

All design questions resolved at planning time. For the record:

1. **Does the second metric need its own validation posture? — resolved.** Yes. `model.CostRecord.Validate()` rejects negative `Amount`, but `NetAmortizedCost` is legitimately negative (credits/refunds/SP true-ups). The new net field is validated with a *floor-only-on-sign-mismatch* rule, not the strict non-negative rule — see [Model](#3-model-changes-modelcostrecordgo). It is the field that the existing `amount <= 0` skip already protected; we move that protection into the field's own posture rather than dropping the row.
2. **Backfill of existing single-metric rows? — resolved.** No. Next-scan repair populates the new column over the rolling `DAYS_BACK` window, consistent with the upsert plan's philosophy (`docs/cost-records-upsert-plan.md` "Backfill"). Pre-window rows keep `net_amount = NULL`; `Detect()` and `Summarize()` COALESCE `NULL → amount` so old rows behave exactly as today.
3. **Does this change the migration-020 conflict key? — resolved.** No. The conflict key stays frozen. The new column is a *payload* column, refreshed in the existing `DO UPDATE` clause exactly like `amount`.
4. **Dashboard surface now or later? — resolved.** Backend-only initially. The displayed cost silently becomes more bill-accurate; no UI toggle ships in the first cut. See [Dashboard](#6-dashboard-backend-only-initially).

## Problem

AxiaOps fetches AWS cost via Cost Explorer at three sites in `services/ingestion/internal/provider/aws/aws.go` — `FetchCosts` (`aws.go:330`), `FetchResourceCosts` (`aws.go:488`), `FetchCostExplorerAPICosts` (`aws.go:578`) — all three requesting `Metrics: []string{"NetAmortizedCost"}` and skipping rows with `amount <= 0` (`aws.go:369`, `:528`, `:611`).

The single `cost_records.amount` column (`services/shared/model/cost.go:20`, persisted at `services/shared/storage/postgres/postgres.go:139-159`) feeds **two consumers with opposing requirements**:

- **The "real cost" view.** `ListCostRecords` (`postgres.go:844`) → `GET /v1/costs`, the dashboard CSV export, and the GDPR data export (`services/api/internal/api/export.go:188`). Users diff this against Vantage and the AWS Bills view.
- **The "savings" calc.** `analyzer.Detect` (`services/shared/analyzer/detector.go:79`) sets `MonthlyCost = c.Amount`; `analyzer.Summarize` (`detector.go:115`) accumulates `PotentialMonthlySave += z.MonthlyCost`; `GET /v1/summary` is `analyzer.Summarize(zombies)` (`services/api/internal/api/handler.go:390`).

`NetAmortizedCost` was chosen deliberately for the **savings** side (`docs/feature-raw-cost-view.md` "Cost Metric: NetAmortizedCost"): killing a resource already covered by a paid-for Savings Plan saves nothing, so net-amortized is the honest projection. But the *same* metric drives the **cost** side, where it reads *below* the actual invoice and below Vantage's default for any account with credits or commitments — NetAmortizedCost subtracts credits and reflects amortized commitment value rather than what the line item contributes to the bill.

Today this is moot: the current internal account (123456789012) has no credits, SP, or RI, so AWS returns identical values for `AmortizedCost`, `NetAmortizedCost`, and `UnblendedCost` (verified 2026-05-31). The conflict is latent. It becomes real the first time a connected account carries a commitment or credit — at which point "cost matches the bill" and "savings is honest" can no longer be served by one number.

## Alternatives considered

| Approach | What it gives | Why rejected / chosen |
|---|---|---|
| **Single metric (status quo, NetAmortizedCost)** | Zero code. Savings stays honest. | The displayed cost reads below the bill for any account with credits/commitments. The user's whole validation loop is diff-against-Vantage; this silently breaks it the day a real customer connects. Rejected as the steady state — but kept as the *current* state until a customer actually has commitments. |
| **Store both (chosen)** | Cost = `AmortizedCost` (matches the Bills view / Vantage default), savings = `NetAmortizedCost` (honest). One multi-metric CE call — no extra API cost or latency. One nullable column + one routing change. | Adds a column and a field. That cost is small and bounded; it is the only option that satisfies both consumers without a heuristic. **Chosen.** |
| **Compute savings from commitment-coverage data** | Most "correct" savings model — query CE `GetReservationCoverage` / `GetSavingsPlansCoverage`, subtract covered cost per resource. | Two extra CE API calls per scan (cost + latency + new IAM perms), per-resource coverage join is materially complex, and coverage data is not resource-granular enough to attribute cleanly to a single zombie. Over-engineered for pre-revenue. Re-open only if a customer disputes a specific savings number. Out of scope. |
| **Defer entirely (do nothing, revisit later)** | Zero code; honest about "no customer needs this yet." | This *is* the chosen Status — but a parked design doc is cheap insurance so the fix is a known shape (not a research task) the day it's needed. The doc lands; the MR waits. |

## Decision

Store **two** cost figures per `cost_records` row:

- **`amount`** (unchanged column) — now sourced from **`AmortizedCost`**. This is the "real cost": it matches the AWS Bills view and Vantage's amortized default. Read by `ListCostRecords` → `/v1/costs`, the dashboard CSV, the GDPR export, and any future cost-display surface.
- **`net_amount`** (new column) — sourced from **`NetAmortizedCost`**. This is the savings basis: post-credits, post-commitment. Read only by `Detect()` (→ `MonthlyCost`) and therefore by `Summarize()` and `/v1/summary`.

The two are fetched in a **single Cost Explorer call** by widening the `Metrics` slice — no extra API request, cost, or latency. Cost Explorer's `GetCostAndUsage` and `GetCostAndUsageWithResources` both accept multiple metrics and return each under its own key in `group.Metrics`.

Mapping to FOCUS terminology so multi-cloud (Phase 4) stays consistent:

| AxiaOps field | CE metric | FOCUS column | Meaning |
|---|---|---|---|
| `amount` | `AmortizedCost` | `EffectiveCost` | Amortized commitment value; what the resource effectively costs on the bill. Matches Vantage default. |
| `net_amount` | `NetAmortizedCost` | `BilledCost` (net of credits) | Post-credit, post-discount cash impact; the honest "what disappears if I delete this." |

(FOCUS's own `EffectiveCost`/`BilledCost` split is precisely this cost-vs-cash distinction. Naming the Go field `NetAmount` and documenting the FOCUS mapping means the Azure/GCP providers populate the same two slots from their native cost APIs without re-litigating the model.)

### The three CE fetch-site changes

At all three sites, widen the metric slice and read both keys. Exact slice change (identical at each site):

```go
Metrics: []string{"AmortizedCost", "NetAmortizedCost"},
```

And in each row loop, replace the single-metric read with:

```go
amortized, _ := strconv.ParseFloat(aws.ToString(group.Metrics["AmortizedCost"].Amount), 64)
net, _      := strconv.ParseFloat(aws.ToString(group.Metrics["NetAmortizedCost"].Amount), 64)
```

Currency comes from `group.Metrics["AmortizedCost"].Unit` (both metrics carry the same unit). The records gain `Amount: amortized, NetAmount: net`. See [The `amount <= 0` skip](#5-the-amount--0-skip-rework) for what happens to the skip.

## Side-effects audit

- **Conflict key (migration 020) — unchanged.** `(organization_id, provider, account_id, service, region, resource_id, period_start, period_end)` stays frozen. `net_amount` is a payload column, not part of identity.
- **Upsert (`SaveCostRecords`, `postgres.go:139-159`) — reconcile with `docs/cost-records-upsert-plan.md`.** The `DO UPDATE SET` clause must refresh the new column alongside `amount`. Add one line: `net_amount = EXCLUDED.net_amount`. It is in the same "value pair" as `amount` (and `currency`) — both come from the same CE response for the same conflict key, so they must move together or a re-scan leaves `amount` fresh and `net_amount` stale. No COALESCE: unlike `internal_account_id`, every steady-state fetch populates `net_amount`, and the only NULLs are pre-migration rows that a re-fetch overwrites anyway. (If the upsert MR lands first, this MR adds the column to its `DO UPDATE`; if this MR lands first, the upsert MR must include `net_amount`. Whichever is second owns the reconciliation — flag in that MR's description.)
- **`Detect()` (`detector.go:79`) — the routing flip.** `MonthlyCost` switches from `c.Amount` to the net field with a COALESCE for legacy rows: `MonthlyCost: netOrAmount(c)` where `netOrAmount` returns `c.NetAmount` when the row carries one and falls back to `c.Amount` for pre-migration rows. This keeps `Summarize()` honest (savings on net) while `/costs` keeps showing `amount` (amortized). This is the *only* behavioural routing change.
- **`AnnotateAll()` (`detector.go:179`) — follows `Detect`.** `ResourceRecord.MonthlyCost` is the per-resource figure shown in the resource inventory and is conceptually a savings/"what-it-costs-you" number, so it uses the same `netOrAmount(c)` source as `Detect`. Keeps the inventory consistent with the zombie list.
- **`/v1/summary` (`handler.go:390`) — improves silently.** Reads `Summarize(zombies)`; zombies now carry net `MonthlyCost`. For accounts with no commitments (every account today) the number is unchanged because net == amortized. For commitment-covered accounts it stops overstating savings — the documented intent of the original NetAmortizedCost switch, now preserved without dragging the cost view down with it.
- **`/v1/costs` + CSV + GDPR export — read `amount` (amortized), unchanged code, more accurate output.** `ListCostRecords` (`postgres.go:860`) keeps selecting `amount` as the primary figure; no SELECT change required for the cost view to become bill-accurate (the accuracy comes from `amount` now being sourced from `AmortizedCost`). The export bundle (`export.go:188`) inherits this. The dashboard CSV is client-side over the same records.
- **`zombie_snapshots` / `zombie_snapshot_services` — append-only, derive from `Summarize`.** Snapshots written after this change record net-based savings. Pre-change snapshots stay as-is (point-in-time). Same divergence philosophy as the upsert plan; no recompute.
- **`dismissed_zombies` — unaffected.** Keys on resource-identity fingerprint, not a cost figure. Dismissed-savings recompute from current zombie rows on next scan.
- **Retention (`DeleteCostRecordsOlderThan`, `postgres.go:1219`) — unaffected.** Deletes by `period_end`; column-count-agnostic.
- **RLS — unaffected.** New column inherits the existing per-table policy; no new policy, no `WITH CHECK` change. `SaveCostRecords` already runs inside the org-scoped tx.
- **Validation (`cost.go:65`) — see [Model](#3-model-changes-modelcostrecordgo).** The strict non-negative rule on `Amount` is unchanged. `NetAmount` gets its own (sign-tolerant) rule.
- **`FetchCostExplorerAPICosts` persistence — still not persisted.** Per the upsert plan, this fetcher's output is appended to `allRecords` for the analyzer pass but never `Save`d. Widening its metric slice is harmless (keeps all three sites symmetric) but has no storage effect. Out of scope to change that, same as the upsert plan.

## 1. Schema migration

Next free number is **030** (highest existing is `029_runtime_admin_role`).

**`030_cost_records_net_amount.up.sql`:**

```sql
SET search_path TO axiaops;

-- net_amount holds NetAmortizedCost (post-credits, RI/SP amortized) — the
-- savings basis for Detect/Summarize. amount continues to hold AmortizedCost
-- (the bill-matching "real cost") read by /v1/costs and exports.
--
-- NULLable, no default: existing rows were written under a single metric and
-- have no net figure. Detect()/AnnotateAll() COALESCE net_amount -> amount so
-- legacy rows behave exactly as before. Steady-state scans populate it over the
-- rolling DAYS_BACK window — no backfill step (see "Backfill" below).
ALTER TABLE cost_records ADD COLUMN IF NOT EXISTS net_amount DOUBLE PRECISION;
```

**`030_cost_records_net_amount.down.sql`:**

```sql
SET search_path TO axiaops;
ALTER TABLE cost_records DROP COLUMN IF EXISTS net_amount;
```

Type `DOUBLE PRECISION` matches the existing `amount` column (Go `float64`). No index — `net_amount` is never a query predicate (no `WHERE net_amount …`); it is read alongside the row, not filtered on. No `NOT NULL`/default: NULL is the load-bearing "legacy row, fall back to amount" signal, and adding a default would mask that.

## 2. Storage changes (`postgres.go`)

- **INSERT (`:139`)** — add `net_amount` to the column list and `$14` to VALUES; bind `r.NetAmount`.
- **`DO UPDATE` (`:145`)** — add `net_amount = EXCLUDED.net_amount`.
- **`ListCostRecords` SELECT (`:860`)** — add `net_amount` to the projection and scan into `&r.NetAmount` (a `*float64` to absorb NULL on legacy rows). `/v1/costs` continues to *display* `amount`; `net_amount` rides along in the JSON for completeness and future use but drives no display.
- The `WHERE amount > 0` filter in `ListCostRecords` (`:863`) stays — see the skip rework below for why the cost-side positivity guard remains correct.

## 3. Model changes (`model/cost.go`)

Add one field to `CostRecord` (after `Amount`):

```go
Amount    float64  `json:"amount"`     // AmortizedCost — real cost (bill-matching)
NetAmount *float64 `json:"net_amount"` // NetAmortizedCost — savings basis; nil on legacy rows
```

`*float64` (pointer), not `float64`, so the model distinguishes "legacy row, no net figure" (nil) from "net figure is genuinely 0.0" (pointer to 0). The `netOrAmount` helper used by `Detect`/`AnnotateAll` lives in the analyzer:

```go
func netOrAmount(c model.CostRecord) float64 {
    if c.NetAmount != nil {
        return *c.NetAmount
    }
    return c.Amount
}
```

**Validation posture.** `Validate()` keeps the strict `Amount < 0 → error` rule unchanged (amortized cost is non-negative for line items we store). For `NetAmount`, **do not** apply the non-negative rule — `NetAmortizedCost` is legitimately negative (credits/refunds/true-ups). Instead validate only that, *when present*, it is finite (not NaN/Inf from a bad parse). A negative `net_amount` is valid data and is handled by the skip rework, not rejected at the boundary. Concretely:

```go
if c.NetAmount != nil && (math.IsNaN(*c.NetAmount) || math.IsInf(*c.NetAmount, 0)) {
    return &ValidationError{Field: "net_amount", Message: "must be finite"}
}
```

This preserves the "strict turns silent-drop into a labelled error" property of the validator without rejecting legitimate negative net figures.

## 4. Routing summary (which consumer reads which field)

| Consumer | Field | Source metric | Why |
|---|---|---|---|
| `GET /v1/costs` (`ListCostRecords`) | `amount` | AmortizedCost | Must match the bill / Vantage. |
| Dashboard CSV export | `amount` | AmortizedCost | Same records as `/costs`. |
| GDPR export (`export.go`) | `amount` (+ `net_amount` rides along in JSON) | AmortizedCost | Full record dump; both fields present. |
| `Detect()` → `ZombieResource.MonthlyCost` | `netOrAmount(c)` | NetAmortizedCost | Honest savings (don't overstate covered resources). |
| `Summarize()` → `/v1/summary` | (inherits `MonthlyCost`) | NetAmortizedCost | Savings total. |
| `AnnotateAll()` → `ResourceRecord.MonthlyCost` | `netOrAmount(c)` | NetAmortizedCost | Consistency with zombie list. |

## 5. The `amount <= 0` skip rework

Today each fetcher skips rows where `NetAmortizedCost <= 0` (`aws.go:369`, `:528`, `:611`) so credits don't subtract from savings. With two metrics this single skip no longer expresses the right policy, because the two figures can have different signs (a credit-covered resource has positive `AmortizedCost` but `NetAmortizedCost <= 0`).

New per-row policy at each fetch site:

- **Skip the row only when `AmortizedCost <= 0`.** Amortized cost is the "real cost"; a non-positive amortized line item is the $0.000005-noise / pure-credit-line case the cost view already excludes (and `ListCostRecords` re-guards with `WHERE amount > 0`). Dropping it keeps the cost view clean.
- **Keep the row when `AmortizedCost > 0` but `NetAmortizedCost <= 0`.** This is the fully-covered resource: it shows a real cost on the bill (so it belongs in `/costs`), but its honest savings is **$0, not a dropped row**. Store `net_amount` as `0` in that case (clamp `max(net, 0)`), so:
  - It appears in `/costs` with its true amortized cost.
  - If detected as idle, it surfaces as a zombie with `MonthlyCost = 0` — correctly telling the user "this is idle but killing it saves nothing because it's already paid for," which is *more* informative than silently omitting it.

Clamping net to `0` (rather than storing the negative) keeps `Summarize` from ever subtracting a covered resource's credit from another resource's genuine savings — the original reason the skip existed, now expressed per-field. The clamp lives at the fetch site, before the record is built.

## 6. Dashboard (backend-only initially)

**Recommendation: no UI change in the first cut.** The displayed cost on the Costs screen silently becomes bill-accurate (because `amount` now tracks `AmortizedCost`); the savings figures on the Dashboard/summary stay honest (net). No user-visible toggle, no second column.

The existing caption on the Costs screen reads "Net amortized cost · post-credits, RI/SP amortized" (`docs/feature-raw-cost-view.md`). After this change the displayed figure is **amortized cost (not net)**, so that caption becomes wrong and **must be updated** to e.g. "Amortized cost · RI/SP amortized, matches AWS Bills view." This is the one mandatory dashboard edit — a caption string, not a feature. Reconcile `docs/feature-raw-cost-view.md`'s "Cost Metric" section in the same MR: the cost view is now AmortizedCost; only the savings path uses NetAmortizedCost.

A "real cost vs net savings" side-by-side, a per-resource "covered by commitment" badge, or a `cost_metric` org override are all **deferred** (see Out of scope). The minimal initial surface is: correct numbers, corrected caption, no new controls.

## Backfill

**No special backfill step. The next normal scan repairs the visible window**, consistent with `docs/cost-records-upsert-plan.md`.

1. Migration 030 adds `net_amount` as NULL on every existing row.
2. Next scan (manual or scheduled) re-fetches the rolling `DAYS_BACK` (default 30) window with the widened metric slice.
3. The upsert's `net_amount = EXCLUDED.net_amount` populates the column for every row in that window.
4. Rows older than the window keep `net_amount = NULL`; `netOrAmount` falls them back to `amount`, so they behave exactly as before this change (no savings regression).

No `DAYS_BACK` bump. For accounts with no commitments (every account today) `amount` and `net_amount` are equal anyway, so even the steady-state repair is numerically invisible until a real commitment exists — which is the whole reason this is parked.

## Test strategy

Unit (`services/shared/analyzer/`, `make test`):

1. **`TestDetect_UsesNetAmountForSavings`** — cost record with `Amount=100, NetAmount=ptr(60)`, usage below threshold → zombie `MonthlyCost == 60`.
2. **`TestDetect_LegacyRowFallsBackToAmount`** — `Amount=100, NetAmount=nil` → zombie `MonthlyCost == 100` (COALESCE path).
3. **`TestDetect_FullyCoveredResourceSavesZero`** — `Amount=100, NetAmount=ptr(0)`, idle → zombie present with `MonthlyCost == 0` (row not dropped).
4. **`TestSummarize_NetBasis`** — mixed zombies, assert `PotentialMonthlySave` sums the net figures.
5. **`TestCostRecord_Validate_NetAmountNegativeIsValid`** — `NetAmount=ptr(-5)` passes; `Amount=-5` still fails; `NetAmount=ptr(NaN)` fails on `net_amount`.
6. **Golden harness** — extend the `analyzer/testdata/golden/` fixture schema so `input_costs.json` can carry `net_amount`. Add a `covered_savings_plan` scenario whose `expected_zombies.json` shows `MonthlyCost: 0` for a covered-but-idle resource. Run `UPDATE_GOLDEN=1` to materialize, review the diff.

Provider (`services/ingestion/internal/provider/aws/`, `make test`):

7. **`TestFetchCosts_ReadsBothMetrics`** — `mockCEClient` returns a group with both `AmortizedCost` and `NetAmortizedCost` keys; assert the record carries both, `Amount` from amortized, `NetAmount` from net.
8. **`TestFetchCosts_SkipsNonPositiveAmortized`** — `AmortizedCost <= 0` row dropped.
9. **`TestFetchCosts_KeepsCoveredRowClampsNetToZero`** — `AmortizedCost=100, NetAmortizedCost=-10` → record kept, `Amount=100`, `*NetAmount==0`.

Integration (`services/shared/storage/postgres/postgres_test.go`, `make test-storage`):

10. **`TestSaveCostRecords_PersistsNetAmount`** — save with `NetAmount=ptr(60)`, `ListCostRecords` round-trips it.
11. **`TestSaveCostRecords_UpsertRefreshesNetAmount`** — upsert same conflict key with a changed `net_amount`, assert the new value wins (proves the `DO UPDATE` line is wired).
12. **`TestSaveCostRecords_LegacyNullNetAmount`** — insert a row with `NetAmount=nil`, assert it scans back as nil (NULL preserved, no spurious 0).

Adjust the API handler mock (`services/api/internal/api/test_helpers_test.go`) if the `ListCostRecords` signature/return shape is touched (it is not — only the struct gains a field, which is source-compatible).

## Rollout

No feature flag. Rationale mirrors the upsert plan: pre-revenue, the change strictly increases accuracy, rollback is a one-commit revert plus a `DROP COLUMN` down-migration.

Because the change is **numerically invisible on every current account** (net == amortized with no commitments), there is no before/after Vantage delta to capture today — that is expected and is the trigger condition for un-parking, not a failure. The validation that the wiring works is the unit/integration suite plus a manual scan on dev-1 confirming `net_amount` is populated and equals `amount` on the commitment-free internal account.

Sequence when un-parked: docs MR (this doc) → review → implementation MR (migration 030 + `model` field + the three `aws.go` fetch-site slice changes + `Detect`/`AnnotateAll` `netOrAmount` switch + `postgres.go` INSERT/`DO UPDATE`/SELECT + the dashboard caption fix + tests) → `develop` → `deploy:dev-1`, scan, confirm `net_amount` populated → preview → staging → production. When the first commitment-carrying customer connects, re-confirm `/costs` matches their Bills view and `/summary` no longer overstates savings on covered resources; attach that comparison to the MR or a follow-up note.

## Out of scope

- **CUR (Cost and Usage Reports) integration.** Stays in Cost Explorer land, same as the upsert plan.
- **Commitment-coverage-based savings** (`GetReservationCoverage` / `GetSavingsPlansCoverage`). The clamp-net-to-zero rule is the cheap correct-enough answer; coverage joins are a separate, larger initiative gated on a customer disputing a specific number.
- **Dashboard "real cost vs net savings" side-by-side, per-resource "covered by commitment" badge, and any `cost_metric` org override.** Backend-only first cut; the only UI edit is the caption correction. Revisit if a customer asks to see both figures.
- **Multi-cloud population of the two fields.** Azure/GCP providers map their native cost APIs onto `Amount`/`NetAmount` via the FOCUS `EffectiveCost`/`BilledCost` mapping in Phase 4; not built now.
- **Backfilling `net_amount` on pre-window rows.** Next-scan repair only; legacy rows fall back to `amount`.
- **Changing the conflict-key composition.** Migration 020 is correct and stable; `net_amount` is a payload column only.
- **`FetchCostExplorerAPICosts` persistence.** Still not persisted to `cost_records`; widening its metric slice is cosmetic symmetry, not a storage change — same posture as the upsert plan.
