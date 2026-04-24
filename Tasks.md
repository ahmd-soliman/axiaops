# AxiaOps — Task Tracker

_Last updated: 2026-04-24_

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
| 9 | GDPR / right to erasure | Data export + account deletion |
| 10 | Expanded detection rules | EBS, S3, CloudFront, Redshift, ElastiCache |
| 11 | Operating entity | Holding GmbH + Operating UG (target August 2026) |

---

## Phase 4+ (2027)

- Multi-cloud (Azure, GCP)
- Mobile app (iOS + Android)
- Cost forecasting (linear regression over snapshot history)
- IaC plan parser (Terraform / CDK) + CI/CD budget gate

---

## CI — Containerize every job, drop custom runners

Context: current CI uses a shell executor that assumes Go, golangci-lint, and Docker are
pre-installed on the runner host, plus a shared `gitlab-cloud-runner-network` for service
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
