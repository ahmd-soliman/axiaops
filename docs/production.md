# Production Setup — AxiaOps

This document covers what is required to deploy AxiaOps beyond local development.
Phase 1 is MVP/dev only. This guide tracks the gap between the current state and
a production-ready deployment.

---

## Current State vs Production Requirements

| Concern | Current (MVP) | Production target |
|---------|--------------|-------------------|
| Database | SQLite (file on disk) | PostgreSQL |
| Usage data | `fixtures/usage.json` (always) | AWS CloudWatch |
| Auth | None — all endpoints open | JWT (Clerk or Supabase Auth) |
| CORS | `Access-Control-Allow-Origin: *` | Locked to your domain |
| TLS | HTTP only | HTTPS via reverse proxy or platform |
| Secrets | Plain env vars | Secret manager (AWS Secrets Manager, Fly.io secrets) |
| Ingestion schedule | Once at startup | Scheduled (cron, cloud scheduler) |
| Logging | `log.Printf` (unstructured) | Structured JSON logs |
| Multi-tenancy | Single account hardcoded | `tenant_id` on all DB rows |

---

## AWS Credentials

The ingestion service uses the AWS SDK v2, which loads credentials in this order:

1. `AWS_ACCESS_KEY_ID` + `AWS_SECRET_ACCESS_KEY` env vars
2. `~/.aws/credentials` file (local dev / EC2 instance profile)
3. IAM Role attached to the compute resource (ECS task role, EC2 instance role)

**For production, use option 3** — attach an IAM role directly to your compute
resource. No credentials to rotate, no secrets to store.

### Minimum IAM policy

```json
{
  "Version": "2012-10-17",
  "Statement": [
    {
      "Effect": "Allow",
      "Action": [
        "ce:GetCostAndUsage"
      ],
      "Resource": "*"
    }
  ]
}
```

When CloudWatch integration is added (Phase 2), add:

```json
{
  "Effect": "Allow",
  "Action": [
    "cloudwatch:GetMetricStatistics",
    "cloudwatch:ListMetrics"
  ],
  "Resource": "*"
}
```

### Multi-tenant (SaaS model — Phase 2)

Customers connect their own AWS accounts. AxiaOps assumes a role in the
customer's account rather than holding their credentials:

1. Customer creates an IAM role in their account with the above policy
2. Customer adds AxiaOps's AWS account ID as a trusted principal
3. AxiaOps calls `sts:AssumeRole` with the customer's role ARN
4. Temporary credentials are used for that request only — nothing stored

AxiaOps's own policy needs `sts:AssumeRole` on `arn:aws:iam::*:role/AxiaOpsReadOnly`.

---

## Database — PostgreSQL (SQLite tests-only)

AxiaOps runs on PostgreSQL. SQLite is retained for unit tests only (throwaway per-test DB files).

**Schema + migrations:**
- Migrations live in `services/shared/storage/postgres/migrations/`
- Both services run migrations automatically on startup (using `MIGRATION_DATABASE_URL`)

---

## RDS vs Aurora PostgreSQL

When moving to AWS-hosted PostgreSQL, the choice is between RDS PostgreSQL and Aurora PostgreSQL.

| | RDS PostgreSQL | Aurora PostgreSQL |
|---|---|---|
| Cost (db.t3.micro) | ~$15/month | ~$45/month minimum |
| Minimum storage | Pay per GB used | 10 GB always billed |
| Storage scaling | Manual resize | Automatic |
| Multi-AZ failover | ~60–120s | ~30s |
| Read replicas | Supported | Supported (lag ~10ms lower) |
| Complexity | Simple, single instance | Cluster with writer + reader endpoints |
| When it pays off | < 1M rows, single region | High read traffic, multiple tenants |

**Decision: use RDS PostgreSQL.**

Aurora costs 3× more with no meaningful benefit at AxiaOps's current scale.
The workload is write-heavy (ingestion) with low read concurrency — exactly
the profile where Aurora's advantages (read replicas, faster failover) don't
apply yet.

Revisit Aurora when:
- Read query latency becomes measurable (many concurrent tenants)
- You need multiple read replicas for reporting queries
- Monthly DB cost is already >$100 (Aurora overhead becomes proportionally smaller)

---

## Environment Variables (Production)

| Variable | Dev default | Production value |
|----------|-------------|-----------------|
| `DEV_MODE` | `true` | `false` |
| `FIXTURE_PATH` | `fixtures/costs.json` | — (not used) |
| `USAGE_PATH` | `fixtures/usage.json` | — (CloudWatch in Phase 2) |
| `DATABASE_URL` | — | PostgreSQL connection string (application user) |
| `MIGRATION_DATABASE_URL` | — | PostgreSQL connection string (owner/admin, used for migrations) |
| `API_ADDR` | `:8080` | `:8080` (TLS terminated by platform) |
| `AWS_ACCOUNT_ID` | — | Your AWS account ID |
| `AWS_REGION` | — | `eu-central-1` (or your primary region) |

**Never set `AWS_ACCESS_KEY_ID` / `AWS_SECRET_ACCESS_KEY` in production.**
Use IAM roles instead (see above).

---

## CORS

The current CORS middleware (`internal/api/handler.go`) allows all origins:

```go
w.Header().Set("Access-Control-Allow-Origin", "*")
```

In production this must be locked to your actual domain. When the nginx proxy
is used (Docker Compose or any reverse proxy setup), the browser never reaches
the Go API directly — CORS headers are only needed if the API is exposed
publicly without a proxy.

**With nginx proxy (recommended):** CORS headers on the Go API are irrelevant —
nginx handles the single origin. The `*` header is harmless but can be removed.

**Without proxy:** Change `*` to your domain:
```go
w.Header().Set("Access-Control-Allow-Origin", "https://app.axiaops.io")
```

---

## TLS / HTTPS

Neither the Go service nor nginx currently terminate TLS. In production:

**Option A — Platform-managed TLS (recommended):**
- Fly.io and Railway provision Let's Encrypt certificates automatically
- Deploy behind their load balancer — no nginx changes needed
- The Go service stays on HTTP internally; TLS is terminated at the edge

**Option B — nginx with Let's Encrypt:**
- Add `certbot` to the dashboard container
- Configure nginx with `listen 443 ssl` and certificate paths
- Requires a domain name pointed at the server

---

## Ingestion Schedule

Currently the ingestion service fetches data once at startup and then serves
the cached results until restart. In production, ingestion should run on a schedule.

**Options:**

| Option | How |
|--------|-----|
| Cron job (simple) | Run the ingestion binary on a schedule via `cron` or a cloud scheduler; the HTTP server is a separate long-running process |
| Split into two services | `ingestion-worker` (scheduled job, no HTTP) + `api-server` (long-running, reads from DB) |
| Scheduled task on Fly.io | `fly machine run` on a cron schedule |

The cleanest production architecture splits the worker from the API server.
Both share the same PostgreSQL database. This is Phase 2 work.

---

## Hosting

### Fly.io (recommended for Phase 2)

```bash
# Install flyctl
brew install flyctl

# Authenticate
fly auth login

# Launch ingestion service
cd services/ingestion
fly launch --name axiaops-ingestion

# Set secrets
fly secrets set AWS_ACCOUNT_ID=123456789012 AWS_REGION=eu-central-1

# Launch dashboard
cd services/dashboard
fly launch --name axiaops-dashboard
```

Fly.io provides:
- Automatic TLS
- Private networking between services (ingestion reachable at `axiaops-ingestion.internal:8080`)
- Persistent volumes for SQLite (until PostgreSQL is in place)
- `fly machine run` for scheduled ingestion

### Railway

Similar to Fly.io. Connect your GitLab repository, set environment variables
in the Railway dashboard, and it deploys on every push to `main`.

---

## What Phase 2 Changes

Once Phase 2 is complete, the production setup becomes:

```
User browser
     │ HTTPS
     ▼
Fly.io / Railway edge (TLS termination)
     │
     ▼
dashboard (nginx) → /api/* → ingestion API
     │
     ▼
PostgreSQL (Supabase)
     │
     ▼ scheduled (Fly machine / cron)
ingestion worker → AWS Cost Explorer → CloudWatch
```

Auth is enforced at the API layer — every request carries a JWT verified against
Supabase Auth. Tenant ID is extracted from the JWT and applied to all DB queries.
