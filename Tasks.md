# AxiaOps — Task Tracker

_Last updated: 2026-04-25_

---

## Phase 2 — Alpha (target August 2026)

### ✅ Done

| Feature | Notes |
|---------|-------|
| AWS Cost Explorer + CloudWatch integration | Real AWS data, dev mock fallback |
| Kinde OAuth 2.0 (PKCE + RS256 JWT) | Auth middleware, tenant/user persistence |
| Multi-tenancy (RLS) | Row-level security on all tables |
| Account management | Connect/delete AWS accounts, encrypted secrets (AES-256-GCM), on-demand scan |
| Resource inventory view | All resources with zombie/active annotation |
| Savings history / trend | `zombie_snapshots` table + `GET /v1/trend` |
| Observability | Structured logging (slog), Prometheus metrics |
| API versioning | `/v1/` prefix on all endpoints |
| In-memory rate limiting | Token bucket per tenant, falls back to Redis when available |
| Graceful shutdown | SIGTERM handling across API + ingestion |
| GitLab CI pipeline | test + build stages, all unit/integration/migration jobs |
| Redis — JWKS cache | `cache.Cache` interface, Redis + memory impls; injected into auth middleware |
| Redis — scan job queue | `queue.Queue` interface, Redis + sync impls; worker in ingestion `cmd/worker.go` |
| Redis — rate limiting | `RateLimiter` backed by `cache.Cache.Incr`; uses Redis when available |
| Scheduled auto-scan | Background ticker in ingestion; `scanScheduledAccounts` every `SCAN_INTERVAL` |
| `cost_records` 90-day retention | Daily cleanup ticker in ingestion; `DeleteOldCostRecords` |
| Dismiss / snooze workflow | `dismissed_zombies` table (migration 002), REST endpoints, dashboard UI |
| Snooze expiry worker | Background ticker expires snoozed records via `ExpireSnoozes` |
| Pricing rates — YAML config | Hardcoded `const` rates in `discover.go` moved to `services/shared/pricing/rates.yml`; loader + per-region override support; fixes the credibility bug from the CE anomaly monitor ($3/mo claim with no source). |

---

### 🔲 Remaining — Phase 2

| # | Task | Notes |
|---|------|-------|
| 1 | **Wire Redis in API `main.go`** ✅ | `cache.New(REDIS_URL)` injected into `NewAuth` + `NewRateLimiter`; falls back to memory if unset |
| 2 | **CSV export — unified across screens** ✅ | TrendScreen added; DashboardScreen + CostAnalyticsScreen migrated to single convention defined in `csv-export` skill (`.claude/skills/csv-export/SKILL.md`). |
| 3 | **Production deployment** | App Runner (API + ingestion) + RDS + ElastiCache via Terraform. See `docs/production.md`. |
| 4 | **Raw cost view** ✅ | `GET /v1/costs` endpoint (`services/api/internal/api/handler.go:47`) + `CostAnalyticsScreen.jsx` shipped. |
| 5 | **Weekly email digest** | New zombies after scan → Resend/SendGrid email. References `zombie_snapshots` for delta. |
| 6 | **Slack webhook alert** | Notify channel when new zombies appear post-scan. |
| 7 | **Rename ghost → zombie across the stack** ✅ | Completed: DB tables, Go types, API routes (`/zombies`), and dashboard field reads are all aligned on "zombie". Single PR covered `ALTER TABLE … RENAME` (metadata-only in Postgres), Go symbol renames (`GhostResource` → `ZombieResource`, `LoadGhosts` → `LoadZombies`, etc.), API routes (`/ghosts` → `/zombies`), and dashboard field reads. Acceptance criterion met: `grep -ri "ghost" services/` returns zero non-historical matches. |
| 8 | **Remove CE anomaly-monitor "ghost" detection** ✅ | `DiscoverIdleCEAnomalyMonitors`, its call site, test, constant, and IAM policy lines (`ce:GetAnomalyMonitors`, `ce:GetAnomalies`) all removed. AWS Cost Anomaly Detection is free — the `$3/mo` pricing claim was fabricated. |
| 9 | **Rename tenant → organization across the stack** | Same shape as #7 (ghost→zombie). Today: dashboard UI prose already migrated (commit `3ff0146`); DB schema, Go code, API URLs, permissions, audit actions, and engineer docs still say "tenant". Two terms for the same concept is a tax on every grep, every onboarding engineer, every customer-support debugging session — fix while pre-launch makes the migration cheap (no production dashboards, no public API contracts, no integrations). JWT format untouched (Kinde sends `org_code` — that's the auth-boundary contract). 12 commits sequenced to keep `make test` green at each: (1–4) Go internal renames `model.Tenant`/`TenantID`/`WithTenantID`/slog labels; (5–6) DB migration 016 + SQL strings in `postgres.go`; (7–9) API URLs `/v1/tenants/*` → `/v1/organizations/*` + permission strings `tenant:delete` → `organization:delete` + audit action `tenant_deleted` → `organization_deleted` (with one-time `UPDATE audit_log` data migration if any prod rows exist); (10–11) Prometheus metric rename + dashboard JS function names; (12) docs sweep. Estimated ~6–8h on a fresh `refactor/tenant-to-organization` branch off `develop` after `feat/gdpr-tenant-deletion` merges. **Blocks Phase 3 #14, #15, #16** — they're all written assuming "organization" terminology lands first. |

---

## Phase 3 — Beta / GTM (target December 2026)

| # | Task | Notes |
|---|------|-------|
| 1 | Stripe billing | Starter €49 / Growth €149 / Team €399 |
| 2 | **Copy-paste remediation commands** | Show exact `aws cli` command per resource type (release EIP, stop EC2, delete LB). No write IAM needed. |
| 3 | **Tag / team filtering** | Filter zombie list by `owner` tag — "show me only the payments team's zombies" |
| 4 | **CSV export** | Download zombie list. Finance teams love spreadsheets. |
| 5 | Audit trail UI for dismissals | Who dismissed what and when — already stored in `dismissed_zombies`, needs UI |
| 6 | Scan history log | Per-account scan log with timestamps and zombie delta |
| 7 | Cost forecast | "If nothing changes, you'll waste $X this month" — linear projection over `zombie_snapshots` |
| 8 | User management + roles | Admin / viewer roles per tenant |
| 9 | **GDPR compliance** | Implementation plan: `docs/compliance/gdpr_plan.md`. Covers data inventory, lawful basis, DSR/erasure (`DELETE /v1/tenants/me`, `GET /v1/export`), retention, sub-processors, breach runbook, DPIA, RoPA, public privacy policy / ToS / DPA. Must ship before first paying EU customer (Sep–Oct 2026). |
| 10 | **Expanded detection rules — Custodian backlog** | 13+ new rules ported from `cloud-custodian/cloud-custodian` filters: unused security groups, idle ALB target groups, overprovisioned RDS, idle ElastiCache replication groups, VPC endpoints, TGW attachments, Lambda PCU, IAM access keys, etc. M1 (per-service file split) shipped. Full backlog with priorities, per-rule template, and milestone sequencing in `docs/custodian-rule-backlog.md`. The original entry ("EBS, S3, CloudFront, Redshift, ElastiCache") is superseded — those are all live. |
| 11 | Operating entity | Holding GmbH + Operating UG (target August 2026) |
| 12 | **Pricing rates — live from AWS Pricing API** | Migrate `services/shared/pricing/rates.yml` to a DB-backed `pricing_rates` table refreshed from `pricing:GetProducts`. YAML works fine while rates change ~yearly; this becomes load-bearing once (a) customers complain that numbers don't match their bill, (b) we add Azure/GCP and need multi-provider rate tables, or (c) we backfill historical savings and need point-in-time rates. Until one of those triggers, stay on YAML. |
| 13 | **CUR ingestion — actual-cost mode** | Replace list-price estimates with customer-specific actual costs by ingesting AWS Cost and Usage Reports (CUR). Customer opts in, enables CUR to their S3 bucket, grants us cross-account read. Two deployment shapes: Athena in-place queries (customer pays ~$1–5/mo in query costs, zero egress for us) or ingest to our DB (faster queries, we pay egress). Unlocks exact per-resource cost including Savings Plans / RIs / EDP discounts — the numbers match the customer's actual invoice to the cent. This is the real differentiator vs Terraform cost estimators (Infracost, AWS Pricing Calculator) that can only use list prices. Ahead of task #12 in priority — CUR solves accuracy, #12 only solves "keeping list prices fresh." |
| 14 | **Multi-organization UX** | The data model already supports one user belonging to many orgs (`memberships(user_id, tenant_id, role)`), but the dashboard exposes single-org operationally. Three pieces, ship as one feature: (a) **org switcher** in the navbar, visible only when the current user has ≥2 memberships — pattern: GitHub avatar dropdown / Slack workspace switcher; (b) **invite-by-email** — replace today's "user must have signed in first" constraint with an email-out flow: invitation row created with a token, email sent via Resend with a magic link, recipient clicks → onboarded into the org with the assigned role. Storage requires a new `pending_invitations` table; renames the dashboard's `addMember` back to `inviteMember` once the semantics match the name; (c) **per-org context indicator** so a switched user always knows which org they're viewing — the navbar org badge already exists, just needs to stay accurate after a switch. Unlocks the MSP / FinOps consultant / holding-company segment that CloudHealth and Vantage explicitly tier for. |
| 15 | **Org-level dashboard** | Promote the existing `/` (All Accounts mode) into a proper organization summary page. Lands after the tenant→organization rename so naming is correct from line one. Seven widgets, top to bottom: **headline tiles** (total monthly waste, active zombies, connected accounts, last-scan + stale-account alert); **org-wide trend** (rolls up `zombie_snapshots` client-side by day — `GET /v1/trend` already returns per-account-per-scan rows, sum `total_monthly_cost` grouped by date); **per-account breakdown** (sortable table of waste $ + zombie count per account; rows click through to `/?account=<id>` — the existing account-filtered Dashboard); **by-service breakdown** (existing `GET /v1/summary` already returns this); **top zombies** (existing `GET /v1/zombies` + client sort + slice(10)); **account health strip** (status badges + ⚠ on any account >24h since last scan; derived from `GET /v1/accounts`); **member activity tile** (last 5 audit events; `GET /v1/audit?limit=5` — overlaps with the dedicated Audit page on purpose: dashboard tile is "what happened?" at a glance, Audit page is "let me investigate"). Snapshot drill-down (click a trend-chart point) shows the per-service breakdown only — per-resource lineage requires task #16. **One new backend endpoint:** `GET /v1/summary/by-account` to return per-account aggregates in one round-trip instead of N+1 `/v1/summary?account_id=X` calls (~1h with test). **Frontend:** split `DashboardScreen.jsx` into `OrgSummaryScreen.jsx` + `AccountDetailScreen.jsx` with shared widget components; new HeadlineTile, OrgTrendChart, AccountBreakdown, AccountHealthStrip, MemberActivityTile (~1 day). Mobile responsive out of scope per existing dashboard convention. |
| 16 | **Historical zombie lineage** | Today `zombie_records` is replaced on every scan — only the *current* zombies are queryable. Per-snapshot aggregates are preserved (`zombie_snapshots`, `zombie_snapshot_services`), but the per-resource history is lost. Add an append-only `zombie_history` table — one row per (snapshot, resource_id) with cost + service + reason + the existing zombie metadata. Storage cost ~36k rows/year/account at 100 zombies × daily scans. Unlocks: **per-resource timeline** ("vol-X became a zombie 2026-03-12, dismissed 2026-03-20, re-detected 2026-04-01"); **stale reports** ("47 resources have been zombies for >30 days"); **audit/compliance evidence** ("prove resource X was flagged on date Y"); and **per-resource snapshot drill-down** from the org dashboard's trend chart (the deferred half of task #15's drill — currently per-service only). Backend: schema migration + `Store.SaveZombieHistory` called alongside `SaveZombies` + read methods + tests (~1 day). Frontend: timeline tab on the existing per-resource detail page + a stale-report screen (~1–2 days). Independent of #15 but they pair well — ship #15 first to validate the drill UX, then #16 to deepen it. |
| 17 | **SOC 2 compliance — Type I → Type II** | Implementation plan: `docs/compliance/soc2_plan.md`. Scope: Security + Availability + Confidentiality TSCs (Privacy deferred). Ship Drata + policy library + evidence pipeline by Q4 2026; Type I audit Q2 2027; Type II audit Q4 2027 (6-month observation window May–Oct 2027). Heavy overlap with GDPR Art. 32 controls — pen-test, breach runbook, restore drill, access review are deliverables for both plans. Required to unlock MSP / Team-tier / Enterprise sales (per `docs/business_plan.md`). |

---

## Phase 4+ (2027)

- Multi-cloud (Azure, GCP)
  - **Terminology audit before second provider lands.** Today the dashboard mixes generic ("Cloud Accounts" nav, `/cloud-accounts` route, "Connect Account" button) and AWS-specific ("AWS Account ID" column, "AWS account" body copy) labels — fine while we ship AWS only. When Azure / GCP arrive: replace per-place "AWS account" with `${PROVIDER_LABEL[a.provider]}` lookups, add a Provider column with per-row icons, refactor `/connect` into a provider-picker. The umbrella terms ("Cloud Accounts", `/cloud-accounts`) stay as-is — they're already provider-neutral. Mirror in the API: `model.Account.Provider` already exists (`"aws"|"azure"|"gcp"`) but only `"aws"` is ever written today.
- Mobile app (iOS + Android)
- Cost forecasting (linear regression over snapshot history)
- IaC plan parser (Terraform / CDK) + CI/CD budget gate

---

## CI — Containerize every job, drop custom runners

Context: current CI uses a shell executor that assumes Go, golangci-lint, and Docker are
pre-installed on the runner host, plus a shared `gitlab-runner-network` for service
containers. This ties CI to bespoke runner images and broke on the new self-hosted runner (see
commit `dafac6b`, IP-lookup workaround). Instead of owning a custom runner image, make
every job self-contained by specifying `image:` + `services:` — then any generic Docker
runner (GitLab.com shared or a vanilla self-hosted one) can run the pipeline.

End state: the runner needs only Docker. CI YAML pins every tool version. Tests run
identically locally and in CI.

**Phase 1 — Containerize test jobs**

- [ ] `test:unit` → add `image: golang:1.25`, drop `.go_setup` reliance.
- [ ] `test:lint` → `image: golangci/golangci-lint:v2.1.0` (or matching version).
- [ ] `test:storage` → `image: golang:1.25` + `services: [{ name: postgres:16-alpine, alias: postgres, variables: … }]`. Drop manual `docker run`, readiness probe, `after_script`, IP lookup.
- [ ] `test:redis` → `image: golang:1.25` + `services: [{ name: redis:7-alpine, alias: redis }]`.
- [ ] Remove `.go_setup` block once nothing references it.
- [ ] Revert commit `dafac6b` (IP-lookup hack) — no longer needed.
- [ ] Drop unused variables (`RUNNER_NETWORK`, `PG_CONTAINER`, `REDIS_CONTAINER`, `POSTGRES_PASSWORD`, `POSTGRES_OWNER_PASSWORD`) if nothing else uses them.

**Phase 2 — Containerize infrastructure jobs**

- [ ] `test:integration:*` → `image: docker:24` + `services: [docker:24-dind]`. Makefile target unchanged.
- [ ] `build:images` → `image: docker:24` + DinD. Mostly already this.
- [ ] `deploy:*` → `image: docker:24` + `apt-get install awscli` in `before_script`, or `image: amazon/aws-cli` with nested docker.

**Phase 3 — Swap the runners**

- [ ] Try GitLab.com shared runners on a test branch. Confirm all jobs green.
- [ ] Decide: move everything to shared runners, or keep a minimal self-hosted runner for deploys that need VPC access (App Runner, RDS).
- [ ] If mixed model: tag deploy jobs with `tags: [self-hosted]`, leave test/build on shared.
- [ ] Decommission old socket-mount runner.
- [ ] Decommission self-hosted runner (or repurpose with stock `gitlab-runner` image and docker executor, no custom image).

**Phase 4 — Cleanup**

- [ ] Pin service image tags (`postgres:16.4-alpine`, not `16-alpine`).
- [ ] Pin Go image tag (`golang:1.25.3`, not `golang:1.25`).
- [ ] Pin DinD tag.
- [ ] Document the CI model in `docs/` — one page, "runners are disposable, images are pinned."
- [ ] Add a `make test-ci` target that runs the exact same image invocations locally so engineers can reproduce CI failures.

**Risks to watch**

- First-job-on-runner image pull latency. Use GitLab's image caching or accept ~5-10s on cold runs.
- Host-behavior-dependent tests (timezone, DNS, filesystem case sensitivity) may pass locally and fail in CI. Expect 1-2 surprises across a year.
- Deploy jobs that need VPC/VPN access can't run on shared runners — requires a small self-hosted runner or ECS/Fargate one-off tasks for migrations (already the pattern for production).
