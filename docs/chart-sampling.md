# Chart sampling — Dashboard developer notes

How the three chart screens (`OverviewScreen`, `TrendScreen`, `CostAnalyticsScreen`) aggregate, downsample, and label their numbers. Written after the `feat/date-filter-all-charts` MR added a shared `DateRangeChips` picker on every chart screen and surfaced a number of subtle math inconsistencies.

If you're touching any of these screens — or a future fourth screen that consumes time-series financial data — read this first.

## The two data sources

| Endpoint | Backing table | Units | Sampling cadence |
|---|---|---|---|
| `GET /v1/trend` | `zombie_snapshots` | `total_monthly_cost` is a **rate** (USD / month) captured at scan-end | One row per (account, scan) — multi-account orgs get multiple rows per day |
| `GET /v1/costs?days=N` | `cost_records` | `amount` is an **actual** (USD spent in the row's `period_start..period_end` window — typically one day) | One row per (account, service, resource_id, day) — Cost Explorer's natural granularity |

The data-type distinction drives the aggregation rules below. Confuse the two and the numbers will look wrong in subtle, hard-to-explain ways.

## The core rule

> **Sum amounts. Average rates.**

`cost_records.amount` is dollars-spent. Summing across days/services/accounts gives "actual spend over the window" — a meaningful FinOps number. Averaging would give a nonsense per-row mean weighted by however many records the seed / cost-explorer happened to produce.

`zombie_snapshots.total_monthly_cost` is dollars-per-month. Summing rates across days makes no sense (`$/month × N days` has no unit). Averaging gives "average monthly-rate cost over the window" — that's the meaningful headline.

Everything else below follows from this rule.

## Granularity toggle

Both Trend and Cost screens have a `daily | monthly` toggle that appears when `period >= 90` (auto-locked to `daily` for shorter windows because monthly granularity on 30 days of data is silly).

The toggle controls the **bucket size**. It does not change the aggregation rule (sum vs avg) — that's locked by the data type, not the user.

| Toggle | TrendScreen → `chartSnaps` | CostAnalyticsScreen → `costChartData` |
|---|---|---|
| `daily` | `aggregateToDays(filteredSnaps)` — one point per day, **sum** across same-day rows (multi-account collapse to org-wide rate) | `byKey` map keyed by `YYYY-MM-DD`, **sum** of `amount` per day |
| `monthly` | `downsampleByMonth(filteredSnaps)` — one point per calendar month, sum-by-day then **average** the daily sums across the month (`aggregateBucket` helper) | `byKey` map keyed by `YYYY-MM`, **sum** of `amount` per month |

Notice the symmetry on the daily row, and the divergence on the monthly row:

- **Monthly Cost** = sum of every dollar spent in that month → a real total
- **Monthly Trend** = mean monthly-rate across the month → "your zombie footprint cost an average of $X/month during this month"

Both are correct for their data type. Don't unify them by force.

## The headline number

`TrendScreen.jsx` shows a `$X.XX` headline above the chart. Its math:

```js
const headlineCost = selectedSnap
  ? selectedSnap.total_monthly_cost              // user clicked a specific scan — show that exact value
  : avgWindowCost;                               // otherwise, average org-wide rate across the window
```

Where `avgWindowCost` is computed via group-by-day → sum across accounts → average across days. Identical conceptually to `downsampleByMonth`, just for the whole window instead of one calendar month.

`OverviewScreen.jsx`'s **Monthly Waste** stat applies the same idea differently. The source is `/v1/summary.potential_monthly_savings` (already a monthly rate from a single snapshot, not a series), and the period-aware view simply scales it linearly:

```js
const waste = monthlyWaste * (period / 30);
```

with a label flip — `Monthly Waste` becomes `{N}-day Waste` when `period ≠ 30`. The scaling is a frontend approximation; a future MR may add `since`/`until` to `/v1/summary` (issue #120) so this becomes a real server-computed number, but the front-end shape stays the same.

## Why NO auto-weekly bucketing on Trend

Pre-MR, `TrendScreen`'s `downsample()` auto-bucketed weekly when `period > 30 && snaps.length > 30`, even when the user had explicitly picked `daily` on the granularity toggle. This produced two confusing outcomes:

1. **Inconsistent shape vs CostAnalyticsScreen** — at the same `90d` chip, Cost showed 90 daily bars while Trend showed ~13 weekly bars, even though both had a "daily" toggle highlighted.
2. **Silent user override** — picking "daily" did nothing.

The fix in this MR removed `downsample()` entirely and replaced the call site with:

```js
const chartSnaps = effectiveGranularity === 'monthly'
  ? downsampleByMonth(filteredSnaps)
  : aggregateToDays(filteredSnaps);   // honor 'daily' literally
```

Now the toggle does what it says. Use `monthly` if you want a smoothed long-window view; use `daily` if you want every scan point.

If a future product decision wants weekly buckets (e.g., a `weekly` option on the toggle), add a third branch with the appropriate `aggregateBucket`-style helper. Don't reintroduce auto-bucketing behind the toggle's back.

## Period filter end-to-end

The shared `DateRangeChips` component (`services/dashboard/src/components/DateRangeChips.jsx`) emits a day count. Each screen interprets it differently:

| Screen | Wire shape |
|---|---|
| OverviewScreen | `period` drives `fetchCosts(account, null, period)` (server-filtered) + `dailyTotals.slice(-period)` (client-side trim of trend data) + the `waste * (period/30)` scaling. |
| TrendScreen | `period` drives a **date-cutoff** client-side filter: `filteredSnaps = allSnaps.filter(s => new Date(s.snapshot_at) >= Date.now() - period*86400000)`. **Not** `slice(-period)` — that was an entry-count bug fixed in this MR. |
| CostAnalyticsScreen | `period` is forwarded to `fetchCosts(account, null, period)` so the server filters; the client just sums what comes back. |

The `Custom…` chip opens a date-input popover; on apply it calls `onChange(daysBetween(since, until))`. The second argument `{sinceIso, untilIso}` is reserved for future server-side `since`/`until` consumers but not used today.

## Adding a new chart screen

1. Decide what data you're charting:
   - **Rates** (anything from `/v1/trend` or any `*_monthly_cost` field) → average within buckets, never sum.
   - **Amounts** (anything from `/v1/costs` or any `amount` field) → sum within buckets, never average.
2. Mount `<DateRangeChips value={period} onChange={setPeriod} mobile={isMobile} />`. Import `DEFAULT_DAYS` from the same module for the `useState` initial.
3. If you fetch a long-window dataset client-side and trim, **filter by date cutoff** (`getTime() >= cutoffMs`), not by `slice(-period)`. The slice form treats each row as a day, which only works when the upstream already collapsed per-day duplicates.
4. If you have a `daily | monthly` toggle, use `aggregateToDays()` / `downsampleByMonth()` from `TrendScreen.jsx` for rate data, or the byKey-map pattern from `CostAnalyticsScreen.jsx` for amount data. Don't invent a third aggregation shape.
5. If you publish a headline number above the chart, document the math in a comment. Specifically: is it the latest point, the window average, the window total, or a delta? Different choices change the meaning of the same chart.

## Local seed shapes (kept in sync with this doc)

`scripts/seed_test_data.sh` writes a fixed-shape dataset that covers `DAYS` days back from today (currently `DAYS=365`). After re-seeding:

| Table | Rows per day | Total at DAYS=365 |
|---|---|---|
| `zombie_snapshots` | 3 (one per dev account) | **1,095** |
| `zombie_snapshot_services` | varies by zombie population (~15–25/day) | ~6,000 |
| `cost_records` | 14 (3 accounts × ~5 services, see the `VALUES (...)` block) | **5,110** |

The invariant assert at `seed_test_data.sh:1385` computes the expected snapshot count as `DAYS × 3` so changing `DAYS` cascades correctly. The cost-records expected count is `DAYS × 14`.

When you bump `DAYS`, also:
- Update this doc's table.
- Re-run `make seed` to verify the chart screens still render cleanly across the full new window.

## See also

- `services/dashboard/src/screens/TrendScreen.jsx` — the canonical implementation of the rate-data aggregation.
- `services/dashboard/src/screens/CostAnalyticsScreen.jsx` — the canonical implementation of the amount-data aggregation.
- `services/dashboard/src/components/DateRangeChips.jsx` — the shared chip picker.
- Issue #120 (referenced in MR !276) — server-side `since`/`until` on `/v1/trend`, `/v1/costs`, `/v1/summary`. When that lands, the client-side trimming above becomes a fallback for envs that don't yet support the new params.
