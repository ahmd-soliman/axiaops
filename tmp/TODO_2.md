# AxiaOps — Next Steps (Post 2.14 Redis)

> Current state: 2.14 Redis complete (feature/2.14-redis, pending merge to develop).
> This file covers the next three milestones in order of priority.

---

## Milestone Order

| # | Milestone | Target | Blocker? |
|---|-----------|--------|----------|
| 2.15 | Weekly Email Digest + Slack Alerts | July 2026 | No |
| 2.16 | Production Deployment (Terraform + App Runner + RDS) | August 2026 | **Hard blocker** |
| 3.1 | Stripe Billing | September 2026 | **Hard blocker** |
| 3.2 | Dismiss Ghost Workflow | September 2026 | **Hard blocker** |
| 3.10 | GDPR / Data Deletion | September 2026 | **Hard blocker** |

---

## 2.15 — Weekly Email Digest + Slack Alerts

### What needs to be built

**Migration `006_add_slack_webhook.sql`**
```sql
ALTER TABLE accounts ADD COLUMN slack_webhook_url TEXT NOT NULL DEFAULT '';
```

**Migration `006_add_notification_settings.sql`** (or same file)
```sql
CREATE TABLE notification_settings (
    tenant_id          TEXT PRIMARY KEY REFERENCES tenants(id),
    email_digest       BOOLEAN NOT NULL DEFAULT true,
    last_digest_sent_at TIMESTAMPTZ
);
ALTER TABLE notification_settings ENABLE ROW LEVEL SECURITY;
CREATE POLICY notification_settings_tenant_isolation ON notification_settings
    USING (tenant_id = current_setting('app.tenant_id', true));
```

**Email — Resend**
- Add `RESEND_API_KEY` env var to all configs
- `services/shared/notify/email.go` — `SendDigest(ctx, to, digest)` using Resend HTTP API (no SDK needed — one POST)
- Digest payload: ghost count, top 5 by cost, week-over-week delta from `ghost_snapshots`
- Trigger: after each ingestion scan, compare current ghost count to last snapshot — if changed, queue digest
- Frequency guard: check `last_digest_sent_at` — skip if sent within last 6 days

**Slack**
- `services/shared/notify/slack.go` — `SendAlert(ctx, webhookURL, msg)` — simple `POST` with JSON body
- Trigger: after scan completes, if ghost count changed (new or resolved ghosts)
- Message format: `"AxiaOps: 3 new ghost resources detected in {account_label} — $142/mo potential savings"`

**API endpoint**
- `POST /v1/settings/notifications` — body: `{email_digest: bool, slack_webhook_url: string}`
- `GET /v1/settings/notifications` — returns current settings

**Store methods to add** (`storage.go` + `postgres.go`)
```go
GetNotificationSettings(ctx context.Context) (model.NotificationSettings, error)
UpsertNotificationSettings(ctx context.Context, settings model.NotificationSettings) error
UpdateAccountSlackWebhook(ctx context.Context, accountID, webhookURL string) error
```

**Key files**
```
services/shared/notify/
  email.go       — Resend HTTP client
  slack.go       — Slack webhook sender
  digest.go      — digest builder (reads ghost_snapshots, computes delta)
services/api/internal/api/handler.go  — add notification settings endpoints
services/ingestion/cmd/main.go        — trigger notify after scan
```

**Env vars to add**
```
RESEND_API_KEY=re_...
```
Add to: `services/api/.env.example`, `services/ingestion/.env.example`, `deploy/dev.yml`, `deploy/staging.yml`

---

## 2.16 — Production Deployment

> This is the hardest milestone — no code shortcuts. Must be done before first paying customer.

### Terraform structure

```
terraform/
  main.tf           — provider config, backend (S3 + DynamoDB)
  variables.tf      — region, env, image tags
  outputs.tf        — App Runner URLs, RDS endpoint
  modules/
    ecr/            — ECR repos for api, ingestion, dashboard
    rds/            — RDS PostgreSQL db.t4g.micro
    elasticache/    — ElastiCache Serverless (Redis)
    apprunner/      — App Runner services (api + ingestion)
    secrets/        — Secrets Manager entries
    vpc/            — VPC, public subnets (no NAT Gateway)
    cloudfront/     — CloudFront + S3 for dashboard
```

### IAM

**`AxiaOpsCI` user** (GitLab CI only):
```json
{
  "Action": [
    "ecr:GetAuthorizationToken",
    "ecr:BatchCheckLayerAvailability",
    "ecr:PutImage",
    "ecr:InitiateLayerUpload",
    "ecr:UploadLayerPart",
    "ecr:CompleteLayerUpload",
    "apprunner:UpdateService",
    "apprunner:DescribeService"
  ]
}
```

**`AxiaOpsAppRunnerRole`** (App Runner task role):
```json
{
  "Action": [
    "secretsmanager:GetSecretValue",
    "rds-db:connect",
    "ce:GetCostAndUsage",
    "cloudwatch:GetMetricStatistics",
    "cloudwatch:ListMetrics",
    "ec2:DescribeInstances",
    "ec2:DescribeNatGateways",
    "ec2:DescribeAddresses",
    "rds:DescribeDBInstances",
    "lambda:ListFunctions",
    "elasticloadbalancing:DescribeLoadBalancers"
  ]
}
```

### Secrets Manager entries

| Secret name | Value |
|-------------|-------|
| `axiaops/prod/encryption-key` | 32-byte hex |
| `axiaops/prod/redis-url` | ElastiCache endpoint |
| `axiaops/prod/resend-api-key` | Resend key |
| `axiaops/prod/kinde-issuer` | Kinde issuer URL |
| `axiaops/prod/kinde-client-id` | Kinde client ID |
| `axiaops/prod/database-url` | RDS connection string (app user) |
| `axiaops/prod/migration-database-url` | RDS connection string (owner) |

### RDS setup

- Instance: `db.t4g.micro` PostgreSQL 16, `eu-central-1`
- Storage: 20 GB gp3, autoscaling to 100 GB
- Automated backups: 7-day retention
- Run migrations on first deploy: `MIGRATION_DATABASE_URL=... go run ./cmd/migrate`
- Add `migrate` target to Makefile for production use

### App Runner

- `api` service: `:8080`, min 1 / max 3 instances, 512 MB / 0.25 vCPU
- `ingestion` service: `:8081`, min 1 / max 2 instances, 1 GB / 0.5 vCPU (scans are memory-heavy)
- Health check: `GET /health`, 30s timeout, 3 retries
- Auto-deploy: trigger on ECR image push (wired in CI)

### Dashboard

- Expo EAS Build → `npx expo export --platform web`
- Upload to S3: `aws s3 sync dist/ s3://axiaops-dashboard-prod/`
- CloudFront invalidation: `aws cloudfront create-invalidation --paths "/*"`
- Already wired in `.gitlab-ci.yml` deploy stage — just needs real bucket/distribution IDs

### CI/CD variables to add in GitLab (masked + protected)

```
AWS_ACCESS_KEY_ID          — AxiaOpsCI user
AWS_SECRET_ACCESS_KEY      — AxiaOpsCI user
ECR_REGISTRY               — 123456789.dkr.ecr.eu-central-1.amazonaws.com
APPRUNNER_API_ARN          — App Runner service ARN for api
APPRUNNER_INGESTION_ARN    — App Runner service ARN for ingestion
CLOUDFRONT_DISTRIBUTION_ID — dashboard distribution
S3_DASHBOARD_BUCKET        — axiaops-dashboard-prod
```

### docs/ops.md (new file — required before launch)

Document:
- `ENCRYPTION_KEY` rotation procedure (re-encrypt all `secret_encrypted` rows first)
- RDS snapshot restore procedure
- App Runner rollback (redeploy previous image tag)
- ElastiCache flush procedure

---

## 3.1 — Stripe Billing

### Migration `007_add_billing_fields.sql`

```sql
ALTER TABLE tenants
  ADD COLUMN plan                TEXT        NOT NULL DEFAULT 'free',
  ADD COLUMN trial_ends_at       TIMESTAMPTZ,
  ADD COLUMN stripe_customer_id  TEXT,
  ADD COLUMN stripe_subscription_id TEXT;
```

### Tier limits

| Plan | Accounts | Auto-scan | CSV export | Slack alerts | User roles |
|------|----------|-----------|------------|--------------|------------|
| free | 1 | ❌ | ❌ | ❌ | ❌ |
| pro_trial | 5 | ✅ | ✅ | ✅ | ❌ |
| pro | 5 | ✅ | ✅ | ✅ | ❌ |
| team | unlimited | ✅ | ✅ | ✅ | ✅ |

### Key files

```
services/api/internal/middleware/billing.go   — plan enforcement middleware
services/api/internal/api/handler.go          — POST /webhooks/stripe
services/shared/model/tenant.go               — add Plan, TrialEndsAt, StripeCustomerID fields
services/shared/storage/storage.go            — UpdateTenantPlan(ctx, tenantID, plan string)
services/shared/storage/postgres/postgres.go  — implement UpdateTenantPlan
```

**Billing middleware** — reads `tenant.plan` from context (set by auth middleware), enforces:
- Account count limit on `POST /v1/accounts`
- Auto-scan block on `POST /v1/accounts/{id}/scan` for free tier
- CSV/PDF export block for free tier

**Stripe webhook handler** (`POST /webhooks/stripe`):
- `customer.subscription.created` → set `plan=pro` or `plan=team`
- `customer.subscription.deleted` → set `plan=free`
- `customer.subscription.updated` → update plan tier
- Verify `Stripe-Signature` header with `STRIPE_WEBHOOK_SECRET`

**Env vars to add**
```
STRIPE_SECRET_KEY=sk_live_...
STRIPE_WEBHOOK_SECRET=whsec_...
```

**14-day trial on signup**: in `UpsertTenant`, set `plan=pro_trial`, `trial_ends_at=NOW()+14 days` for new tenants. Background ticker in API checks `trial_ends_at < NOW()` and downgrades to `free`.

---

## 3.2 — Dismiss Ghost Workflow

### Migration `008_add_dismissed_ghosts.sql`

```sql
CREATE TABLE dismissed_ghosts (
    id           TEXT        PRIMARY KEY,
    tenant_id    TEXT        NOT NULL REFERENCES tenants(id),
    resource_id  TEXT        NOT NULL,
    reason       TEXT        NOT NULL,
    note         TEXT        NOT NULL DEFAULT '',
    dismissed_by TEXT        NOT NULL,  -- user kinde_sub
    dismissed_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    snooze_until TIMESTAMPTZ           -- NULL = permanent dismiss
);
ALTER TABLE dismissed_ghosts ENABLE ROW LEVEL SECURITY;
CREATE POLICY dismissed_ghosts_tenant_isolation ON dismissed_ghosts
    USING (tenant_id = current_setting('app.tenant_id', true));
CREATE UNIQUE INDEX dismissed_ghosts_resource_idx ON dismissed_ghosts(tenant_id, resource_id);
```

### Store methods

```go
DismissGhost(ctx context.Context, d model.DismissedGhost) error
UndismissGhost(ctx context.Context, resourceID string) error
ListDismissedGhosts(ctx context.Context) ([]model.DismissedGhost, error)
ClearExpiredSnoozes(ctx context.Context) (int, error)
```

### API endpoints

| Method | Path | Body / Notes |
|--------|------|--------------|
| `POST` | `/v1/ghosts/{id}/dismiss` | `{reason, note, snooze_until?}` |
| `DELETE` | `/v1/ghosts/{id}/dismiss` | undismiss / cancel snooze |
| `GET` | `/v1/ghosts` | add `?include_dismissed=true` support |

### Background ticker (in API service)

Runs every 15 minutes — calls `ClearExpiredSnoozes()`, logs count.

### Dashboard changes

- `DetailScreen.js` — "Dismiss" button → modal with reason dropdown + optional note + optional snooze date
- Ghost list — dismissed ghosts hidden by default; grey "Intentional" badge when shown
- `DashboardScreen.js` — add `include_dismissed` toggle

---

## 3.10 — GDPR / Data Deletion

### API endpoints

| Method | Path | Notes |
|--------|------|-------|
| `DELETE` | `/v1/tenants/me` | cascade delete all tenant data + cancel Stripe subscription |
| `GET` | `/v1/export` | JSON dump: ghosts, accounts metadata (no secrets), scan history |

### Cascade delete order (FK constraints)

```sql
DELETE FROM dismissed_ghosts  WHERE tenant_id = $1;
DELETE FROM ghost_snapshots   WHERE tenant_id = $1;
DELETE FROM ghost_records     WHERE tenant_id = $1;
DELETE FROM resource_records  WHERE tenant_id = $1;
DELETE FROM cost_records      WHERE tenant_id = $1;
DELETE FROM accounts          WHERE tenant_id = $1;
DELETE FROM users             WHERE tenant_id = $1;
DELETE FROM notification_settings WHERE tenant_id = $1;
DELETE FROM tenants           WHERE id = $1;
```

Run in a single transaction. Cancel Stripe subscription before DB delete (if `stripe_subscription_id` is set).

### Store method

```go
DeleteTenant(ctx context.Context, tenantID string) error
ExportTenantData(ctx context.Context) (model.TenantExport, error)
```

---

## Branch / PR naming convention

```
feature/2.15-alerts          → develop
feature/2.16-terraform        → develop
feature/3.1-stripe-billing    → develop
feature/3.2-dismiss-ghost     → develop
feature/3.10-gdpr             → develop
```
