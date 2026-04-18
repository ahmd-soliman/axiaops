# AxiaOps — Task Tracker

_Last updated: 2026-04-19_

---

## Phase 2 — Alpha (target August 2026)

### ✅ Done

| Feature | Notes |
|---------|-------|
| AWS Cost Explorer + CloudWatch integration | Real AWS data, dev mock fallback |
| Kinde OAuth 2.0 (PKCE + RS256 JWT) | Auth middleware, tenant/user persistence |
| Multi-tenancy (RLS) | Row-level security on all tables |
| Account management | Connect/delete AWS accounts, encrypted secrets (AES-256-GCM), on-demand scan |
| Resource inventory view | All resources with ghost/active annotation |
| Savings history / trend | `ghost_snapshots` table + `GET /v1/trend` |
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
| Dismiss / snooze workflow | `dismissed_ghosts` table (migration 002), REST endpoints, dashboard UI |
| Snooze expiry worker | Background ticker expires snoozed records via `ExpireSnoozes` |

---

### 🔲 Remaining — Phase 2

| # | Task | Notes |
|---|------|-------|
| 1 | **Wire Redis in API `main.go`** | `REDIS_URL` env var → Redis cache → inject into `NewAuth` + `NewRateLimiter`. Falls back to memory if unset. |
| 2 | **Production deployment** | App Runner (API + ingestion) + RDS + ElastiCache via Terraform. See `docs/production.md`. |
| 3 | **Weekly email digest** | New ghosts after scan → Resend/SendGrid email. References `ghost_snapshots` for delta. |
| 4 | **Slack webhook alert** | Notify channel when new ghosts appear post-scan. |

---

## Phase 3 — Beta / GTM (target December 2026)

| # | Task | Notes |
|---|------|-------|
| 1 | Stripe billing | Starter €49 / Growth €149 / Team €399 |
| 2 | Audit trail for dismissals | Who dismissed what and when — already stored in `dismissed_ghosts`, needs UI |
| 3 | Remediation CLI commands | Per-resource-type shell commands shown in DetailScreen |
| 4 | Scan history log | Per-account scan log with timestamps and ghost delta |
| 5 | Tag / team filtering | Filter ghost list by `owner` tag |
| 6 | CSV export | Export ghost list / scan history |
| 7 | User management + roles | Admin / viewer roles per tenant |
| 8 | GDPR / right to erasure | Data export + account deletion |
| 9 | Expanded detection rules | EBS, S3, CloudFront, Redshift, ElastiCache |
| 10 | Operating entity | Holding GmbH + Operating UG (target August 2026) |

---

## Phase 4+ (2027)

- Multi-cloud (Azure, GCP)
- Mobile app (iOS + Android via Expo)
- Cost forecasting (linear regression over snapshot history)
- IaC plan parser (Terraform / CDK) + CI/CD budget gate
