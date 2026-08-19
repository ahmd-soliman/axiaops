# Org-level dashboard — implementation plan (Phase 3 #15)

Promote `/` into a deliberate **organization summary** (7 widgets), backed by **one**
new endpoint `GET /v1/summary/by-account`. Architect-reviewed; grounded in the
actual code, not the spec's stale names.

## Reality reconciliation (the spec is stale)

The original spec said "split `DashboardScreen.jsx` into `OrgSummaryScreen.jsx` +
`AccountDetailScreen.jsx`." None of those files exist. What's actually there:

- `screens/OverviewScreen.jsx` (~1800 lines) is the main screen, rendered by the
  thin wrapper `pages/Overview.jsx` at route `/`. It is **already** an org/account
  hybrid: an `AccountSelector` + URL param `?account=<internalId>` (`useSearchParams`)
  toggles "All Accounts mode" (org aggregates) vs a single account.
- Per-resource detail is `screens/DetailScreen.jsx` at `/detail/:id` — **not** an
  "account detail" screen. The spec's `AccountDetailScreen` doesn't map to it.
- `pages/Overview.jsx` redirects to `/connect?onboarding=1` when the org has zero
  accounts — **this onboarding redirect must be preserved** on the new `/`.

So the spec's instinct (org view vs account view) is right but the *line* is wrong.
The clean seam is **summary vs workbench**, not **org vs account** (see Part 3).

## Part 1 — Backend: `GET /v1/summary/by-account`

**Contract** (auth `PermZombiesRead`, mirroring `getSummary`):
```json
{
  "currency": "USD",
  "accounts": [
    { "internal_account_id": "uuid", "account_id": "123456789012",
      "total_zombies": 12, "potential_monthly_savings": 431.20, "top_service": "AmazonRDS" }
  ]
}
```
- Empty org → `{"accounts":[]}` (never `null` — match the existing `if x == nil` guards).
- Accounts with zero post-dismissal zombies are **omitted** (the frontend overlays the
  full list from `/v1/accounts` for the health strip).
- **No account-metadata join in the backend.** Return `internal_account_id` +
  `account_id` (AWS number, present on `ZombieResource`); the frontend maps
  `internal_account_id → label` from its already-loaded `/v1/accounts` data. Keeps the
  endpoint a pure zombie projection.

**Aggregation happens in the handler layer — no SQL, no new Store method.** Reuse
`getSummary`'s pipeline exactly: `LoadZombies(ctx)` → `enrichWithDismissals(ctx, zombies, "", false)`
(verified: `accountID=""` lists all org dismissals) → group. This guarantees dismissal
exclusion is **identical** to `getSummary` (the one behavior we must not diverge on —
it's fingerprint-based in `enrichWithDismissals`, reimplementing it in SQL is a real
correctness risk for zero benefit at MVP scale). In-memory grouping of a few hundred
zombies is microseconds.

**New code:**
- `analyzer/detector.go` — `SummarizeByAccount(zombies) ByAccountSummary` (pure, mirrors
  `Summarize`: group by `InternalAccountID`, sum `MonthlyCost`, count, derive `top_service`
  by max per-service savings, `round2`, currency from first non-empty).
- `handler.go` — `getSummaryByAccount` (copy `getSummary` steps 1–3, terminal
  `SummarizeByAccount`) + route `GET /v1/summary/by-account`.

**Tests:** `TestSummarizeByAccount` (analyzer — multi-account, top_service, empty→[]);
`TestGetSummaryByAccount` (handler — two-account isolation, **dismissed zombie excluded
from its account total** = the parity guard, empty→`{"accounts":[]}`).

> The spec item "`?account_id=` on `/v1/summary`" is **already implemented** (`getSummary`
> filters in-memory). Mark it done in passing; don't redo it.

## Part 2 — The 7 widgets & their data sources

| # | Widget | Source | New endpoint? | v1 |
|---|--------|--------|--------------|----|
| 1 | Headline tiles (waste, zombies, ratio, spend) | `/v1/summary` + `/v1/costs` | no | must |
| 2 | Org-wide trend | `/v1/trend` (no account), date-rollup (logic exists in `OverviewHero.dailyTotals`) | no | must |
| 3 | Per-account breakdown | **`/v1/summary/by-account`** + `/v1/accounts` labels | **yes** | must |
| 4 | By-service breakdown | `/v1/summary.by_service` (reuse `<ServiceBreakdown>`) | no | must |
| 5 | Top zombies | `/v1/zombies` org-wide, sort by `monthly_cost` desc, top N | no | must |
| 6 | Account health strip | `/v1/accounts` (`status`,`last_scanned_at`,`error_message`) ⊕ `/v1/summary/by-account` | no | must (degraded ok) |
| 7 | Member activity | `/v1/audit?limit=5` | no | nice-to-have |

**Honest note on the "one endpoint" budget:** it holds, but only because widgets #6 and
#7 reuse *existing* endpoints — they are **not** served by the new endpoint:
- **#6** needs account metadata (`/v1/accounts`, already loaded on this screen) overlaid
  with per-account waste (`/v1/summary/by-account`).
- **#7** needs the audit timeline (`/v1/audit`, viewer-tier — no permission work). But
  `/v1/audit` returns `user_id`, not display names. **v1 cut:** render
  `action + relative time + resource_type`, omit the human name (name resolution is a
  follow-up). This is why #7 is nice-to-have.

Widgets 1, 2, 4, 5 are already fetched by OverviewScreen's All-Accounts mode — but it's
re-composition of existing *endpoints* **plus re-implementing two client-side reductions
that today live inside the don't-touch workbench file**:

- **#1 "total spend":** `/v1/costs` returns a **raw `[]CostRecord`** — no org total. The
  client must `costs.reduce((a,c)=>a+c.amount, 0)` (today only in `OverviewScreen.jsx`
  `totalSpend`). Re-implement the ~3 lines in OrgSummary; don't import from the workbench.
- **#2 org trend:** `/v1/trend` with no account returns **one row per (account, scan)** —
  the client must roll up by date or the delta is meaningless. That rollup lives in
  `OverviewHero.dailyTotals` *inside* the workbench file. **Copy** the ~8 lines into
  OrgSummary (don't import — it's in the file we're not editing).
- **#5 top-zombie deep link:** `/detail/:id` needs `?account=&region=&service=` in the
  query string (DetailScreen reads them) — reconstruct that shape, same as `pages/Overview.jsx`
  does today, or the detail view breaks.

All five widgets are viewer-tier, but the screen now fans out across `PermZombiesRead` +
`PermCostsRead` + `PermSnapshotsRead` + `PermAccountsRead` + `PermAuditRead` — all
viewer-grant, so no permission gate beyond authn (confirmed).

## Part 3 — Frontend approach: compose, don't split

**Recommendation: do NOT do the spec's full split.** Build a new read-only
`screens/OrgSummaryScreen.jsx` at `/` that composes existing presentational pieces, and
keep `OverviewScreen.jsx` as the account **workbench**, moved to `/account`.

Why: `OverviewScreen.jsx` conflates (a) summary rendering and (b) a ~1000-line resource
**workbench** (filter pills, search/sort, bulk dismiss/snooze, Hidden tab with fragile
`resource_id` vs `dismissal_id` juggling, CSV export). The clean seam is summary vs
workbench. The workbench is identical whether scoped to one account or all — splitting it
"by account" would duplicate it. A deep refactor risks regressing shipped, load-bearing
flows for a dashboard re-skin — poor risk/reward.

**Shape:**
- New `screens/OrgSummaryScreen.jsx` at `/` — the 7 widgets, read-only. Preserve the
  zero-accounts → `/connect?onboarding=1` redirect currently in `pages/Overview.jsx`.
- `OverviewScreen.jsx` **unchanged internally**, moved to route `/account` (a route move
  only — do not touch its internals). Per-account cards (#3) + health rows (#6) link to
  `/account?account=<id>`; top zombies (#5) link to `/detail/:id`.
- `components/navItems.jsx` — `/` = "Overview" (org summary); add an entry point to the
  workbench (label TBD in-slice, don't bikeshed here). `isNavActive` already exact-matches
  `/`, so adding `/account` causes no prefix collision.

**Landing-page shift (deliberate) + single-account redirect.** Every authed entry path
lands on `/`: post-login (`pages/Login.jsx`), org-picker (`OrgPickerScreen.jsx`),
post-onboarding (`OnboardingGate.jsx`), the 404/503 "go to overview" buttons, and the
bootstrap probe (`App.jsx`). After this change they all land on the **read-only org
summary** instead of the actionable workbench. None of those redirects need to change
(they still resolve to a valid screen), but it's a real UX shift and must be stated.
**Decision — don't degrade the common case:** when the org has **exactly one account**,
`/` redirects to `/account?account=<internalId>` (the single-account self-hosted operator
keeps landing on the actionable view); orgs with 2+ accounts get the org summary. This
check lives in the new `/` wrapper alongside the existing zero-accounts →
`/connect?onboarding=1` redirect.
- Extract `ServiceBreakdown` / hero tiles into `components/` **only if** the org screen
  needs them and the move is mechanical; otherwise copy the ~80-line component rather than
  block a slice on a risky extraction.

## Part 4 — Slices (each shippable)

> **Gate reality:** `make test` is **Go-only** (no vitest in it), so it's a real gate only
> for slice 0. Slices 1–4 touch only the dashboard — their gate is `npm run lint` +
> `npm run build` **plus manual verification in `make start-dev`** (especially the
> workbench move: confirm bulk dismiss + Hidden-tab restore + CSV export still work at
> `/account`, and that `/`, deep links, and the single-account redirect behave). Don't
> lean on `make test` green for the frontend slices — it proves nothing there.

0. **Backend** (only Go slice): `SummarizeByAccount` + `getSummaryByAccount` + route +
   tests; add `fetchSummaryByAccount()` to `api/client.js`. API serves it; nothing
   consumes it yet.
1. **`OrgSummaryScreen` skeleton + tiles (#1) + by-service (#4)**: new screen at `/`,
   move workbench to `/account`, preserve the zero-accounts → `/connect?onboarding=1`
   redirect **and** add the single-account → `/account?account=<id>` redirect. Decide
   where `WhatsNextPanel` (the onboarding nudge currently in `pages/Overview.jsx`) lives —
   it belongs on the new `/` (first thing a new owner sees), not the workbench. `/` is a
   real (sparse) org page; workbench works at its new route.
2. **Per-account breakdown (#3) + health strip (#6)**: consume the new endpoint, join
   labels + overlay health from `/v1/accounts`; rows link to `/account?account=<id>`.
3. **Org trend (#2) + top zombies (#5)**: `fetchTrend()` no-account + `<AreaChart>`;
   `fetchZombies()` sorted top-N → `/detail/:id`.
4. **(nice-to-have) Member activity (#7)**: `fetchAuditEvents({limit:5})`, action+time+type,
   no name resolution. Ship or defer.
5. **Polish/docs**: `services/api/CLAUDE.md` endpoint table, mark this spec item done,
   nav labels, responsive (`useBreakpoint`), empty/loading/error states.

## Part 5 — Risks / cuts / defer

**Risks**
- **Workbench move must be route-only** — do not refactor `OverviewScreen` internals
  (Hidden-tab id juggling + bulk flows are fragile/commented). Verify bulk dismiss +
  Hidden restore manually in `make start-dev` after the move.
- **Dismissal-exclusion parity** — new endpoint MUST exclude dismissed zombies exactly
  like `getSummary`; reuse `enrichWithDismissals` verbatim; the dismiss-a-zombie handler
  test is the guard. No SQL reimplementation.
- **`account_id` vs `internal_account_id`** (a past bug source) — group on
  `internal_account_id`; `account_id` (AWS number) is display-only.
- **`null` vs `[]`** — empty org returns `{"accounts":[]}`.

**Empty states (design deliberately — the new `/` is the first thing a new owner sees):**
- *Zero accounts* → existing `/connect?onboarding=1` redirect (preserve).
- *Accounts connected, no scan completed yet* (the realistic new-install state): the new
  endpoint returns `{"accounts":[]}`, `/v1/summary` is all-zeros, `/v1/trend` is `[]`, but
  `/v1/accounts` shows `status:"connected"`, `last_scanned_at:null`. The health strip must
  render **"never scanned"** and the headline tiles must read as a clean "scans pending"
  state, not a broken all-zeros page. `WhatsNextPanel` covers the nudge here.
- *Single account* → redirect to the workbench (see Part 3), so the org page is only ever
  shown to multi-account orgs with ≥1 account.

**Scope cuts (v1):** member-activity without name resolution (or defer the tile); no
backend account-metadata join (labels client-side); no deep `OverviewScreen` refactor.

**Defer:** SQL-side by-account aggregation + a `Store` method (only if #16 historical
lineage inflates zombie counts); member-activity name resolution; any caching of the new
endpoint (data is already cheap).

## Files

**Backend:** `analyzer/detector.go` (+`_test.go`), `api/internal/api/handler.go`
(+`handler_test.go`), `services/api/CLAUDE.md`. No migration, no Store method, no
ingestion change.
**Frontend:** `api/client.js`, new `screens/OrgSummaryScreen.jsx`, `pages/Overview.jsx`,
`App.jsx` (route `/account`), `components/navItems.jsx`, maybe extract `ServiceBreakdown`.
**Tracking:** this plan.

## References
- Spec: Phase 3 #15 (+ 3.8 per-account summary).
- Existing patterns: `getSummary` + `enrichWithDismissals` + `analyzer.Summarize`
  (`services/api/internal/api/handler.go`, `services/shared/analyzer/detector.go`).
- Frontend: `screens/OverviewScreen.jsx`, `pages/Overview.jsx`, `components/AreaChart.jsx`,
  `components/navItems.jsx`.
