# Refactor: tenant → organization

Tracking ticket: axiaops#25 (Phase 2 #9).

## Why now

The dashboard prose already says "organization" (commit `3ff0146`). Everything
behind the API still says "tenant" — DB schema, Go types, API URLs, permission
strings, audit actions, Prometheus labels, engineer docs. Two terms for one
concept is a tax on every grep, every onboarding engineer, every customer-support
debug session. Pre-launch is the cheap window: no production dashboards, no
public API contracts, no third-party integrations.

This change blocks Phase 3 #14 (multi-organization UX), #15 (org-level
dashboard), and #16 (zombie lineage) — all written assuming "organization"
landed first.

## Sequencing — 12 commits

`make test` (and `make test-storage` at commit 5) must be green at every commit
boundary. Order is chosen so each commit is independently buildable.

| # | Commit | Why this slot |
|---|--------|---------------|
| 1 | `docs(refactor): plan tenant→organization rename` | This document. Zero code. |
| 2 | `refactor(observability): rename tenant_id label to organization_id` | Self-contained — Prometheus metric labels and metric name. No callers depend on the *spelling* of the label. |
| 3 | `refactor(model): rename Tenant type to Organization` | Go-only type rename. SQL strings unchanged — column is still `tenant_id` in the DB until commit 5, but the Go-side struct field becomes `OrganizationID`. |
| 4 | `refactor(storage): rename WithTenantID to WithOrganizationID` | Context helper + slog labels. No DB impact. |
| 5 | `refactor(storage): migration 016 rename tenants→organizations + tenant_id→organization_id` | The DB rename. Includes RLS policy + GUC rename (`app.tenant_id` → `app.organization_id`) and **must** ship in the same commit as the SQL string updates in `postgres.go`, otherwise tests are broken between the migration apply and the Go SQL update. `make test-storage` is the gate. |
| 6 | `refactor(authz): rename tenant permissions to organization` | `PermTenant*` → `PermOrganization*` in Go and the JS mirror is renamed in commit 8 — that's intentional (mirror lives in dashboard, dashboard URL change is its own commit). |
| 7 | `refactor(api): rename audit action tenant_deleted to organization_deleted` | One constant, one call site, one validation map. Also covered by the data migration in 016. |
| 8 | `refactor(api,dashboard): move /v1/tenants/* routes to /v1/organizations/*` | The breaking-URL commit. API and dashboard URL must move together — otherwise the running stack 404s. |
| 9 | `refactor(api): rename remaining handler internals (variable + slog)` | Local-variable sweep that wasn't pulled in by earlier commits. |
| 10 | `refactor(dashboard): rename remaining tenant references in JS` | Default-state field names and audit filter values. |
| 11 | `docs: sweep tenant→organization across all CLAUDE.md and docs/*.md` | Engineer docs. Excludes historical changelogs and external vendor docs. |
| 12 | `chore: env var DEV_TENANT_ID → DEV_ORGANIZATION_ID + final grep gate` | Env var rename + a `scripts/` grep guard that fails CI if `tenant` reappears outside the allow-list. |

## Do NOT rename — the allow-list

These references must survive the rename. The grep gate in commit 12 must permit
them.

1. **Kinde JWT claims**: `claims["org_code"]` and `claims["org_name"]` in
   `services/api/internal/middleware/auth.go` and any test that fixtures Kinde
   JWTs. This is an external auth-boundary contract — not ours to rename. The
   variable names `orgCode`, `orgName` and surrounding comments stay.
2. **Historical changelog and decision docs**: `docs/change_list_*.md`,
   `docs/PHASE2_*.md`, `docs/USER_STORIES_STATUS.md`, `docs/code_review_*.md`,
   any `docs/IMPLEMENTATION_*.md`. These are immutable record — past commit
   summaries that say "tenant" are accurate descriptions of what shipped under
   that name. They do not get retconned.
3. **Historical migrations 001–015**: the SQL is shipped, signed-off, and the
   `down.sql` files refer to `tenant_id` to be reversible. Do not edit
   anything below `migrations/016_*`.
4. **External vendor terminology** quoted in docs: AWS docs in
   `docs/cloudtrail-analysis.md`, `docs/aws-coverage.md` referencing AWS's own
   "multi-tenant" or "tenancy" wording stay verbatim.

## Danger zones

- **RLS atomicity in migration 016.** Use `ALTER POLICY ... RENAME TO` and
  `ALTER POLICY ... USING (...)` rather than DROP+CREATE. The whole migration
  runs in a single transaction. The 012 ghost→zombie migration is the playbook
  for the `RENAME TO` half; 016 additionally has to update the policy
  `USING`/`WITH CHECK` predicates.
- **GUC name change (`app.tenant_id` → `app.organization_id`)**. Migrations run
  on app pool startup, before traffic — fine at deploy. But running 016 against
  a live DB while the app is serving traffic will silently filter every query
  on every active session to zero rows until those sessions reset and re-set
  the new GUC. Mitigation: deploy with a brief restart of API+ingestion right
  after migration.
- **In-flight scan goroutines.** `scanAccount` in the API uses
  `context.WithTimeout(context.Background(), ...)`, deriving tenant from the
  account row rather than ctx values. The context-key rename in commit 4 is
  safe for them.
- **Grafana dashboards.** Metric label `tenant_id` → `organization_id` and
  metric name `axiaops_tenant_deletions_total` → `axiaops_organization_deletions_total`
  are breaking for any Grafana dashboard JSON. None are checked in today.
- **`DEV_TENANT_ID` env var.** Sticky shells / `.env` files held by
  developers. Pure rename in commit 12 — no shim — because pre-launch.

## What stays "tenant" forever

- The literal strings `org_code`, `org_name` in the Kinde JWT mapping.
- The phrase "multi-tenant" when describing the AxiaOps deployment model in
  external-facing docs (it's the standard SaaS term and means something
  different from "organization" — it's about isolation, not entity naming).

## Verification at the end

```bash
grep -ri "tenant" services/ docs/ \
  | grep -v "org_code" \
  | grep -v "org_name" \
  | grep -v "docs/change_list" \
  | grep -v "docs/PHASE2_" \
  | grep -v "docs/code_review" \
  | grep -v "docs/IMPLEMENTATION_" \
  | grep -v "docs/USER_STORIES_STATUS" \
  | grep -v "migrations/0\(0\|1[0-5]\)" \
  | grep -v "multi-tenant"
```

Should be empty. The commit-12 grep guard codifies this.
