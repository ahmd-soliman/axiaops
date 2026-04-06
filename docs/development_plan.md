# Development Plan — AxiaOps FinOps

## Project Overview

**AxiaOps** — A FinOps SaaS tool that identifies idle/zombie cloud resources still incurring costs despite $0.00 usage. Target: AWS (initial), multi-cloud later.

---

## Phase 1 — Incubator / MVP (April – June 2026) ✅

### Goal: Working AxiaOps Detector (local, no auth, fixture data)

#### 1.1 Cost Fixture Data
- `fixtures/costs.json` — 13 realistic `CostRecord` entries with ARN-style resource IDs
- Fields: `provider`, `account_id`, `service`, `region`, `resource_id`, `amount`, `currency`, `period_start`, `period_end`, `tags`
- Tags include `env` and `team` on every record — ownership resolution depends on this
- Covers: EC2, RDS, S3, Lambda, CloudFront, ELB, CloudWatch, NAT Gateway across `eu-central-1` and `eu-west-1`

#### 1.2 Usage Fixture Data
- `fixtures/usage.json` — one CloudWatch-style metric per resource (mirrors what production will fetch from CloudWatch)
- Fields: `resource_id`, `metric`, `unit`, `avg`, `period_days`
- Some resources deliberately have zero usage to act as zombies in development and testing

#### 1.3 Backend — Go Services

**Language:** Go 1.25+ | **Framework:** Standard library + `net/http` | **Data Layer:** SQLite (MVP) → PostgreSQL (production)

**Service architecture (Phase 2+):**

```
services/
  shared/     — model, storage interface + SQLite, analyzer (no AWS SDK)
  api/        — HTTP server, auth middleware, reads ghost_records from DB
  ingestion/  — job only, fetches AWS/fixture data, writes to DB, exits
```

**Flow:**

```
ingestion job (one-shot)
  ├── FetchCosts  → cost_records table
  ├── FetchUsage  → CloudWatch / fixture
  ├── Detect()    → analyzer flags zombies
  └── SaveGhosts  → ghost_records table

API service (always running)
  ├── LoadGhosts  → reads ghost_records from DB
  ├── GET /ghosts   → list of zombie resources
  ├── GET /summary  → aggregate savings
  └── GET /health   → healthcheck (no auth)
```

**Go workspace (`go.work`)** links all three modules locally — no publishing required.

**Ingestion (`services/ingestion/internal/provider`):**
- `Provider` interface — swap AWS Cost Explorer for file fixture with one env var (`DEV_MODE=true`)
- `INSERT OR IGNORE` deduplication — safe to re-run; logs inserted vs skipped counts

**Analysis (`services/shared/analyzer`):**
- `Detect()` joins cost records with usage records on `resource_id`
- Applies per-service threshold rules (see table below)
- A resource is flagged when `usage.avg <= threshold` for the entire billing period
- Resources with no rule or no usage record are skipped
- `owner` is derived from the `team` tag

**Detection rules:**

| Service | Metric | Threshold | Reason shown |
|---------|--------|-----------|--------------|
| AmazonEC2 | CPUUtilization | ≤ 5% | Instance CPU below 5% — likely idle |
| AmazonRDS | DatabaseConnections | = 0 | Zero connections — likely abandoned |
| AWSLambda | Invocations | = 0 | Zero invocations — likely unused |
| AmazonElasticLoadBalancing | RequestCount | = 0 | Zero requests — likely abandoned |
| AmazonVPC | BytesOutToDestination | = 0 | NAT Gateway zero bytes — likely unused |
| AmazonVPC (EIP) | NetworkInterfaceAttachment | = 0 | Elastic IP not attached — $0.005/hour idle charge |

**API (`services/api/internal/api`):**
- `GET /ghosts` — list of detected zombie resources with cost, usage metric, reason, and owner
- `GET /summary` — aggregate savings figure and per-service breakdown
- `GET /health` — healthcheck, bypasses auth
- `GET /accounts` — list connected cloud accounts for the current tenant
- `POST /accounts` — connect a new cloud account (encrypts secret with AES-256-GCM)
- `DELETE /accounts/{id}` — remove a connected account
- `POST /accounts/{id}/scan` — trigger an on-demand ingestion scan for an account
- CORS middleware — permissive in dev, locked to domain in production

#### 1.4 Auth — Kinde (Phase 2, complete)

- **Provider:** Kinde (chosen over Supabase Auth, Clerk, Cognito — see `docs/auth.md`)
- **Flow:** PKCE OAuth via `expo-auth-session` on dashboard → JWT verified by Go middleware
- **Middleware:** `services/api/internal/middleware/auth.go` — RS256 JWT verification via JWKS
- **Tenant persistence:** `org_code` → internal UUID in `tenants` table on first login
- **User persistence:** `kinde_sub` + email in `users` table, `last_seen` updated on each login
- **Migration path:** swap `AUTH_ISSUER` env var — schema is provider-agnostic (see `docs/auth.md`)

#### 1.5 Testing

**Coverage:**

| Package | Tests | What is covered |
|---------|-------|-----------------|
| `shared/analyzer` | 9 | Flags zero-usage resources, skips active, skips missing data, owner fallback, aggregate savings |
| `api/internal/api` | 7 | GET /ghosts, GET /summary — 200, JSON payload, content type, CORS, OPTIONS preflight |
| `api/internal/middleware` | 9 | Valid/invalid/expired/wrong-issuer tokens, missing org_code, OPTIONS passthrough, tenant in context |
| `shared/storage/sqlite` | 11 | Insert, dedup, empty batch, region uniqueness, tags as JSON, UpsertTenant, UpsertUser |
| `ingestion/provider/filefixture` | 5 | Returns all records, multiple records, file not found, invalid JSON, correct Name() |
| `ingestion/provider/aws` | 3 | Single-page response, multi-page pagination, API error propagation |

**Test patterns used:**
- `mockCEClient` — implements `CostExplorerAPI` interface, no real AWS calls
- `os.CreateTemp` — throwaway SQLite file per test, cleaned up via `t.Cleanup`
- `httptest.NewRecorder` — tests HTTP handlers without a real server
- RSA key generation — signs test JWTs for middleware tests without hitting Kinde

#### 1.6 Frontend — React Native (Expo)
- **Stack:** Expo + React Native + React Query — same codebase runs on web, iOS, and Android
- **Web first** — Phase 1 targets web only; mobile comes in Phase 3
- **Dashboard screen** — dark navy header, orange savings number, ghost list with per-service colour coding
  - Accounts bar — shows connected accounts with green/red status dot; Scan button triggers on-demand ingestion
  - Service pill filter — tap a service pill to filter the ghost list; tap again to clear
- **Connect screen** — credential form (label, Access Key ID, Secret Access Key, region) with IAM permissions hint; auto-shown on first login when no accounts are connected
- **Detail screen** — service-coloured header, stats grid, reason, remediation hint per service type
- **Auth:** Kinde PKCE login screen → token stored in `localStorage` (web) / `SecureStore` (native)
- **API client** — sends `Authorization: Bearer <token>` on every request

#### 1.7 Infrastructure — Docker Compose

```
browser
   │
   ▼
nginx (dashboard:80)
   │  serves Expo static build
   │  proxies /api/* → api:8080
   ▼
api service (Go binary)
   │  reads ghost_records from DB, serves REST API
   │  POST /accounts/{id}/scan → triggers ingestion via HTTP
   ▼
axiaops.db (SQLite, persisted in named Docker volume)

ingestion service (long-lived HTTP server on :8081)
   │  POST /scan  — fetches costs + usage, runs analyzer, writes ghost_records
   ▼
axiaops.db (same volume)
```

**Key decisions:**
- nginx proxy eliminates cross-origin requests
- API healthcheck uses `/health` (no auth) — Docker `depends_on: service_healthy`
- SQLite in a named Docker volume — survives container restarts
- Expo web built at Docker image build time — no Node.js runtime in production
- `EXPO_PUBLIC_*` vars passed as Docker build args — baked into static bundle

#### 1.8 Storage Layer — SQLite

**Tables:**

```sql
cost_records   — raw billing data from Cost Explorer / fixture
ghost_records  — detected zombie resources (replaced on each ingestion run)
tenants        — Kinde org_code → internal UUID mapping
users          — Kinde users, linked to tenant, last_seen updated on login
```

**`cost_records` schema:**
```sql
CREATE TABLE IF NOT EXISTS cost_records (
    id           INTEGER  PRIMARY KEY AUTOINCREMENT,
    provider     TEXT     NOT NULL,
    account_id   TEXT     NOT NULL,
    service      TEXT     NOT NULL,
    region       TEXT     NOT NULL,
    resource_id  TEXT,
    amount       REAL     NOT NULL,
    currency     TEXT     NOT NULL,
    period_start DATETIME NOT NULL,
    period_end   DATETIME NOT NULL,
    tags         TEXT,
    fetched_at   DATETIME NOT NULL,
    UNIQUE(provider, account_id, service, region, period_start, period_end)
);
```

**Duplicate handling:** `INSERT OR IGNORE` — safe to re-run ingestion for same date range.

**Production path:** `Store` interface (`services/shared/storage/storage.go`) is the only contract. Swapping to PostgreSQL requires a new implementation — no changes to providers, analyzer, or API.

#### 1.9 Dev Environment

**Run locally:**
```bash
./scripts/dev.sh          # fixture data (DEV_MODE=true)
./scripts/dev.sh --aws    # real AWS (DEV_MODE=false)
./scripts/dev.sh stop     # kill all services
```

**Inspect database:**
```bash
./scripts/check_db.sh
```

**`DEV_MODE` switch:**
- `DEV_MODE=true` → reads from `fixtures/costs.json` and `fixtures/usage.json`
- `DEV_MODE=false` → calls real AWS Cost Explorer + Describe APIs + CloudWatch

---

## Phase 2 — Alpha (July – September 2026)

### Goal: Real cloud connectivity, single-tenant

#### 2.1 AWS Integration

**Status: Complete (shipped ahead of schedule)**

**Ingestion flow:**

```
1. Cost Explorer (ce:GetCostAndUsage)
      │  grouped by SERVICE + REGION
      ▼
2. Resource Discovery (Describe APIs)
      │  ec2:DescribeInstances, rds:DescribeDBInstances,
      │  lambda:ListFunctions, elb:DescribeLoadBalancers,
      │  ec2:DescribeNatGateways
      ▼
3. CloudWatch (cloudwatch:GetMetricStatistics)
      │  one call per discovered resource
      ▼
4. Analyzer → Detect() + Summarize()
      ▼
5. SaveGhosts → ghost_records table
      ▼
6. API reads ghost_records → GET /ghosts, GET /summary
```

**IAM policy required (`AxiaOpsReadOnly`):**

```json
{
  "Action": [
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

**Key files:**
- `services/ingestion/internal/provider/aws/aws.go` — Cost Explorer client
- `services/ingestion/internal/provider/aws/discover.go` — resource discovery
- `services/ingestion/internal/provider/aws/cloudwatch.go` — CloudWatch usage fetcher

#### 2.2 Account Management ✅

**Status: Complete (shipped ahead of schedule)**

**Design goals:**
- Provider-agnostic `accounts` table (not `aws_accounts`) — ready for Azure/GCP without schema changes
- Secrets encrypted at rest with AES-256-GCM — `ENCRYPTION_KEY` env var (32-byte hex)
- On-demand scan per account — no fixed schedule needed for MVP

**Schema (`accounts` table):**

```sql
CREATE TABLE IF NOT EXISTS accounts (
    id                TEXT        PRIMARY KEY,
    tenant_id         TEXT        NOT NULL REFERENCES tenants(id),
    provider          TEXT        NOT NULL DEFAULT 'aws',
    label             TEXT        NOT NULL DEFAULT '',
    access_key_id     TEXT        NOT NULL DEFAULT '',
    secret_encrypted  TEXT        NOT NULL DEFAULT '',
    region            TEXT        NOT NULL DEFAULT 'us-east-1',
    status            TEXT        NOT NULL DEFAULT 'connected',
    last_scanned_at   TIMESTAMPTZ,
    created_at        TIMESTAMPTZ NOT NULL
);
ALTER TABLE accounts ENABLE ROW LEVEL SECURITY;
CREATE POLICY accounts_tenant_isolation ON accounts
    USING (tenant_id = current_setting('app.tenant_id', true));
```

**Key files:**
- `services/shared/model/account.go` — Account struct (`SecretEncrypted` omitted from JSON)
- `services/shared/crypto/crypto.go` — AES-256-GCM encrypt/decrypt (shared between api and ingestion)
- `services/shared/storage/storage.go` — Store interface extended with 5 account methods
- `services/shared/storage/postgres/postgres.go` — Full account CRUD implementation
- `services/shared/storage/sqlite/sqlite.go` — Stub implementations (dev-only, accounts not supported in SQLite)
- `services/ingestion/cmd/main.go` — Refactored to long-lived HTTP server; `POST /scan` decrypts credentials and runs ingestion

**Ingestion scan flow:**
1. API receives `POST /accounts/{id}/scan`
2. API sets account status to `scanning`, fires async goroutine
3. Goroutine POSTs `{account_id, tenant_id}` to ingestion service at `http://localhost:8081/scan`
4. Ingestion fetches account from DB, decrypts secret, sets AWS env vars, runs full ingestion
5. API updates account status to `connected` or `error` on completion

#### 2.3 Auth — Kinde ✅

- Chosen over Supabase Auth, Clerk, Cognito — see `docs/auth.md`
- JWT middleware in `services/api/internal/middleware/`
- Tenant + user persisted on first login
- Dashboard login screen with PKCE flow

#### 2.3 Alerting
- Weekly email digest: "You have $X in ghost spend this week"
- Email provider: Resend (preferred) or SendGrid

#### 2.4 Deployment
- API service: AWS App Runner — see `docs/deployment.md`
- Ingestion job: EventBridge cron → Lambda or App Runner task
- Frontend: Expo EAS Build → web deploy
- Database: RDS PostgreSQL (`db.t4g.micro`)

---

## Phase 3 — Beta / GTM (October – December 2026)

### Goal: App Store launch, first paying customers

#### 3.1 Remediation Actions
- Pre-generated AWS CLI commands per resource type
- Dismiss workflow — mark a ghost as intentional with a reason
- Audit trail — all actions logged with timestamp and user

#### 3.2 Resource Inventory View ✅

**Status: Complete (shipped ahead of schedule)**

- `model.ResourceRecord` — all resources with cost + usage + `is_ghost bool`
- `resource_records` table populated by ingestion job (replace-on-run, like `ghost_records`)
- `analyzer.AnnotateAll(costs, usage, ghosts)` — marks each cost record as ghost or active using the pre-computed ghost slice (EIP ghosts included automatically)
- `GET /resources` — full inventory; frontend filters client-side
- Dashboard "Ghost Resources / All Resources" toggle — ghost-only by default
- Resource cards show usage metric for active resources; ghost badge + reason for zombies
- DetailScreen adapts: hides "Why flagged" and "Suggested Action" for non-ghost resources

#### 3.3 Multi-cloud
- Azure Cost Management API
- GCP Billing Export → BigQuery

#### 3.4 Reporting
- Exportable PDF savings report
- Savings trend over time (chart)
- CSV export

#### 3.5 Mobile App
- Same Expo codebase — `npm run ios` / `npm run android`
- Apple Developer account ($99/year) required for TestFlight

#### 3.6 App Store Submission
- Establish Operating UG before submission
- Privacy policy, terms of service

---

## Phase 4 — Proactive Cost Simulation (Q1–Q2 2027)

### Goal: Anticipate costs before deployment

```
Plan → Deploy → Run
 ↑         ↑       ↑
Simulate  Gate   Optimize   ← AxiaOps owns all three
```

#### 4.1 IaC Plan Parser
- Parse `terraform plan -out=plan.json` and AWS CDK `cdk diff` output
- Extract resource types, sizes, regions, counts

#### 4.2 Cost Estimation Engine
- Fetch live pricing from AWS Pricing API, Azure Retail Prices API, GCP Cloud Billing Catalog
- Compute estimated monthly cost delta per resource

#### 4.3 What-if Scenarios
- "What if I use gp3 instead of gp2?" → show savings
- "What if I switch region?" → show delta
- "What if I use Spot?" → show risk vs savings

#### 4.4 CI/CD Budget Gate
- GitLab CI / GitHub Actions integration
- Posts cost delta as MR comment
- Configurable threshold: warn or block

#### 4.5 CLI Tool
- `axiaops estimate --plan plan.json`
- `brew install axiaops`

---

## Tech Stack Summary

| Layer | Technology |
|-------|-----------|
| Backend | Go 1.25+ |
| Database | SQLite (MVP) → PostgreSQL |
| Frontend | React Native (Expo) — web + mobile |
| Auth | Kinde (see docs/auth.md) |
| Hosting | AWS App Runner |
| CI/CD | GitLab CI |
| Cloud APIs | AWS Cost Explorer, CloudWatch, Azure Cost Mgmt, GCP Billing |

---

## Milestones

| Date | Milestone | Status |
|------|-----------|--------|
| April 2026 | Go ingestion service + fixture data + zombie detection | ✅ Done |
| April 2026 | React Native web dashboard | ✅ Done |
| April 2026 | Docker Compose full-stack + unit tests | ✅ Done |
| April 2026 | AWS Cost Explorer + CloudWatch integration | ✅ Done |
| April 2026 | Kinde auth + tenant/user persistence | ✅ Done |
| April 2026 | API/ingestion service split + ghost_records DB | ✅ Done |
| April 2026 | Account management — connect AWS, encrypted secrets, on-demand scan | ✅ Done |
| August 2026 | PostgreSQL + multi-tenancy | Planned |
| September 2026 | Alpha with 3–5 beta users | Planned |
| October 2026 | App Store / Play Store submission | Planned |
| November 2026 | First paying customer | Planned |
| December 2026 | 10 customers, €5K MRR target | Planned |
| Q1 2027 | IaC plan parser + cost estimation engine | Planned |
| Q2 2027 | CI/CD budget gate | Planned |
| Q3 2027 | CLI tool + what-if scenarios | Planned |
