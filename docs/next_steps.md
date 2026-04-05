# Next Steps — AxiaOps

Current status: Phase 1 complete, Phase 2 AWS + CloudWatch integration complete.

---

## Phase 1 — MVP ✅

### Cost + Usage Data
- [x] `fixtures/costs.json` — 13 realistic cost records across EC2, RDS, Lambda, ELB, NAT Gateway
- [x] `fixtures/usage.json` — CloudWatch-style usage metrics per resource
- [x] Some resources deliberately at zero usage to act as zombies in dev/test

### Go Ingestion Service
- [x] `Provider` interface — swap AWS for file fixture via `DEV_MODE=true`
- [x] AWS Cost Explorer integration (`ce:GetCostAndUsage`)
- [x] CloudWatch integration (`cloudwatch:GetMetricStatistics`)
- [x] Resource discovery via Describe APIs (EC2, RDS, Lambda, ELB, NAT Gateway)
- [x] SQLite storage with `INSERT OR IGNORE` deduplication
- [x] Configurable date range — `DAYS_BACK`, `START_DATE`, `END_DATE` env vars

### Zombie Detection
- [x] `Detect()` — joins cost records with usage records on `resource_id`
- [x] Per-service threshold rules (EC2 ≤5% CPU, RDS 0 connections, Lambda 0 invocations, ELB 0 requests, NAT Gateway 0 bytes)
- [x] Owner resolution from `team` resource tag
- [x] `Summarize()` — aggregate savings and per-service breakdown

### REST API
- [x] `GET /ghosts` — list of zombie resources with cost, usage, reason, owner
- [x] `GET /summary` — aggregate savings and per-service breakdown
- [x] CORS middleware

### React Native Dashboard
- [x] Dark navy + orange design
- [x] Savings banner with total ghost spend
- [x] Ghost list with per-service colour coding
- [x] Detail screen with resource info and remediation hint
- [x] Environment-aware API client (dev vs Docker/production)

### Infrastructure
- [x] Docker Compose — one command runs ingestion + dashboard
- [x] nginx reverse proxy — serves dashboard, proxies `/api/*` to ingestion
- [x] `~/.aws` mounted into container — no credentials in `.env`
- [x] GitLab CI pipeline — runs `go test ./...` on every push

### Testing
- [x] 24 unit tests across 5 packages
- [x] Mock interfaces for AWS Cost Explorer and CloudWatch — no real AWS calls in tests

---

## Phase 2 Remaining

### Auth
- [ ] Choose auth provider — Clerk or Supabase Auth
- [ ] Add JWT middleware to Go API — reject unauthenticated requests
- [ ] Add login screen to dashboard
- [ ] Protect all API endpoints (`/ghosts`, `/summary`) behind auth
- [ ] Store user identity with each ingestion run

### PostgreSQL Migration
- [ ] Implement `Store` interface for PostgreSQL (`internal/storage/postgres/`)
- [ ] Add `tenant_id` column to `cost_records` table
- [ ] Enable Row-Level Security — each customer sees only their own data
- [ ] Add `DB_URL` env var, wire PostgreSQL in `main.go` when set
- [ ] Write migration script from SQLite schema to PostgreSQL
- [ ] Test with local PostgreSQL via Docker Compose

### Multi-account Support
- [ ] Add IAM role ARN field per customer account
- [ ] Implement `sts:AssumeRole` in `internal/provider/aws/aws.go`
- [ ] Allow multiple AWS accounts per customer — loop providers in `main.go`
- [ ] Add `cloudwatch:ListMetrics` to IAM policy for auto-discovery

### Scheduled Ingestion
- [x] Add `/ingest` HTTP endpoint — triggers ingestion on demand
- [x] Move ingestion logic out of `main()` startup into a callable function
- [ ] Wire EventBridge cron → POST `/ingest/{customer_id}` nightly

### Weekly Digest
- [ ] Choose email provider — Resend or SendGrid
- [ ] Build digest template — ghost count, top savings, new ghosts since last run
- [ ] Trigger digest after nightly ingestion if new ghosts are detected
- [ ] Add Slack webhook support

---

## Phase 3 Remaining

### Remediation Workflow
- [ ] Add `status` field to ghost records — `open`, `dismissed`, `delegated`
- [ ] `POST /ghosts/{id}/dismiss` — mark as intentional with a reason
- [ ] `POST /ghosts/{id}/delegate` — assign to a team member
- [ ] Audit trail table — log all actions with user, timestamp, reason
- [ ] Pre-generated AWS CLI commands per resource type shown in detail screen

### Multi-cloud
- [ ] Azure Cost Management API — implement `Provider` interface for Azure
- [ ] GCP Billing API — implement `Provider` interface for GCP
- [ ] Add provider selector in dashboard

### FOCUS Specification (FinOps Open Cost and Usage Specification)
- [ ] Implement a `focusfile` provider — reads FOCUS-formatted billing exports from S3/blob storage
- [ ] Map FOCUS columns (`BilledCost`, `ResourceId`, `ServiceName`, `RegionName`, `Tags`) to `model.CostRecord`
- [ ] Use FOCUS as the ingestion path for Azure and GCP — one parser handles all clouds
- [ ] Keep AWS Cost Explorer API for real-time AWS ingestion — FOCUS exports have a 24-hour delay
- [ ] Offer FOCUS as an optional ingestion path for customers who already export billing data to S3
- [ ] See [FinOps Foundation FOCUS spec](https://focus.finops.org) for schema reference

### Reporting
- [ ] Savings trend chart — ghost spend over time
- [ ] PDF export — savings report for FinOps/CFO presentation
- [ ] CSV export — ghost list for spreadsheet analysis

### Mobile App
- [ ] Test Expo build on iOS simulator
- [ ] Test Expo build on Android emulator
- [ ] Submit to TestFlight for internal testing
- [ ] Apple Developer account ($99/year) required

---

## Infrastructure

### App Runner Deployment
- [ ] Create ECR repositories for ingestion and dashboard images
- [ ] Write App Runner service definitions
- [ ] Set up RDS PostgreSQL in `eu-central-1`
- [ ] Configure secrets in AWS Secrets Manager — DB credentials, API keys
- [ ] Set up custom domain + TLS
- [ ] Wire EventBridge cron for nightly ingestion

### CI/CD
- [ ] Add GitLab CI stage to build and push Docker images to ECR on merge to `main`
- [ ] Add GitLab CI stage to deploy to App Runner after image push
- [ ] Add dashboard build + test stage to CI pipeline

---

## Immediate Priority Order

```
1. Scheduled ingestion (/ingest endpoint)   ← unblocks everything else
2. Auth (Clerk or Supabase)                 ← needed before any real customers
3. PostgreSQL + multi-tenancy               ← needed before second customer
4. Weekly digest                            ← first retention mechanism
5. App Runner deployment                    ← move off local dev
6. Remediation workflow                     ← Phase 3 value-add
7. Multi-cloud (Azure, GCP)                 ← Phase 3 expansion
```
