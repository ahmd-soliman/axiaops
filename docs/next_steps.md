# Next Steps — AxiaOps

Current status: Phase 1 complete, Phase 2 AWS + CloudWatch integration complete.

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
- [ ] Add `/ingest` HTTP endpoint — triggers ingestion on demand
- [ ] Wire EventBridge cron → POST `/ingest/{customer_id}` nightly
- [ ] Move ingestion logic out of `main()` startup into a callable function

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
