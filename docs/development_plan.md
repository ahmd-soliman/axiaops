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

#### 1.3 Backend — Go Service
- **Language:** Go 1.25+
- **Framework:** Standard library + `net/http`
- **Data Layer:** SQLite (MVP) → PostgreSQL (production)
- **Flow:** ingest → store → analyze → serve

**Ingestion (`internal/provider`, `internal/storage`):**
- `Provider` interface — swap AWS Cost Explorer for file fixture with one env var (`DEV_MODE=true`); no code changes needed when switching environments
- `INSERT OR IGNORE` deduplication — safe to re-run; logs inserted vs skipped counts

**Analysis (`internal/analyzer`):**
- `Detect()` joins cost records with usage records on `resource_id`
- Applies per-service threshold rules (see table below)
- A resource is flagged when `usage.avg <= threshold` for the entire billing period
- Resources with no rule or no usage record are skipped — no determination is made without data
- `owner` is derived from the `team` tag — every ghost includes an owner so remediation is actionable

**Detection rules:**

| Service | Metric | Threshold | Reason shown |
|---------|--------|-----------|--------------|
| AmazonEC2 | CPUUtilization | ≤ 5% | Instance CPU below 5% — likely idle |
| AmazonRDS | DatabaseConnections | = 0 | Zero connections — likely abandoned |
| AWSLambda | Invocations | = 0 | Zero invocations — likely unused |
| AmazonElasticLoadBalancing | RequestCount | = 0 | Zero requests — likely abandoned |
| AmazonVPC | BytesOutToDestination | = 0 | NAT Gateway zero bytes — likely unused |

**API (`internal/api`):**
- `GET /ghosts` — list of detected zombie resources with cost, usage metric, reason, and owner
- `GET /summary` — aggregate savings figure and per-service breakdown
- CORS middleware included — permissive in dev, will be locked to domain in production

#### 1.4 Testing

All backend packages have unit tests (24 tests across 5 packages).

**Coverage:**

| Package | Tests | What is covered |
|---------|-------|-----------------|
| `internal/analyzer` | 9 | Flags zero-usage resources, skips active resources, skips services with no rule, skips missing usage data, owner fallback to "unknown", multiple ghosts, empty summary, aggregate savings |
| `internal/api` | 7 | `GET /ghosts` and `GET /summary` return 200, correct JSON payload, `application/json` content type, CORS header present, OPTIONS preflight returns 204 |
| `internal/storage/sqlite` | 5 | Inserts records, deduplicates on re-run, empty batch, different region is not a duplicate, tags serialised as JSON |
| `internal/provider/filefixture` | 5 | Returns all records, handles multiple records, file not found error, invalid JSON error, correct `Name()` |
| `internal/provider/aws` | 3 | Single-page response, multi-page pagination, API error propagation |

**Test patterns used:**
- `mockCEClient` — implements `CostExplorerAPI` interface to test the AWS provider without real AWS calls
- `os.CreateTemp` — creates a throwaway SQLite file per test; cleaned up via `t.Cleanup`
- `httptest.NewRecorder` — tests HTTP handlers without starting a real server
- Helper functions (`costRecord`, `usageRecord`, `record`) — reduce boilerplate across test cases

#### 1.5 Frontend — React Native (Expo)
- **Stack:** Expo + React Native + React Query — same codebase runs on web, iOS, and Android
- **Web first** — Phase 1 targets web only; mobile comes in Phase 3 once real data and auth are in place
- **Dashboard screen** — dark navy header, orange savings number, scrollable ghost list with per-service colour coding, env and owner chips on each card
- **Detail screen** — service-coloured header, stats grid, reason, resource details table, remediation hint per service type
- **API client** — calls `http://localhost:8080` in dev; calls `/api/*` (nginx proxy) in Docker/production — no CORS issues in production

#### 1.6 Infrastructure — Docker Compose

Both services run with `docker compose up`. See README for run instructions.

**Architecture:**

```
browser
   │
   ▼
nginx (dashboard:80)
   │  serves Expo static build
   │  proxies /api/* → ingestion:8080
   ▼
ingestion (Go binary)
   │  reads fixtures, runs analysis, serves API
   ▼
axiaops.db (SQLite, persisted in named volume db_data)
```

**Key decisions:**
- nginx proxy eliminates cross-origin requests — no CORS headers needed in production
- `depends_on: service_healthy` — dashboard only starts once the ingestion API responds to `/summary`
- SQLite stored in a named Docker volume — survives container restarts
- Expo web is built at Docker image build time (`expo export --platform web`) and served as static files — no Node.js runtime needed in production

#### 1.7 Storage Layer — SQLite

**Schema (`cost_records` table):**

```sql
CREATE TABLE IF NOT EXISTS cost_records (
    id           INTEGER  PRIMARY KEY AUTOINCREMENT,
    provider     TEXT     NOT NULL,         -- e.g. "aws"
    account_id   TEXT     NOT NULL,         -- AWS account ID
    service      TEXT     NOT NULL,         -- e.g. "Amazon EC2"
    region       TEXT     NOT NULL,         -- e.g. "eu-central-1"
    resource_id  TEXT,                      -- optional ARN or resource ID
    amount       REAL     NOT NULL,         -- cost in `currency`
    currency     TEXT     NOT NULL,         -- e.g. "USD"
    period_start DATETIME NOT NULL,         -- billing period start (UTC)
    period_end   DATETIME NOT NULL,         -- billing period end (UTC)
    tags         TEXT,                      -- JSON object: {"env":"prod","team":"platform"}
    fetched_at   DATETIME NOT NULL,         -- when this record was ingested
    UNIQUE(provider, account_id, service, region, period_start, period_end)
);
```

**Duplicate handling:** `INSERT OR IGNORE` skips any row that violates the `UNIQUE` constraint. Running the ingestion service multiple times for the same date range is safe — only new records are written.

**Production path:** The `Store` interface (`internal/storage/storage.go`) is the only contract the rest of the code depends on. Swapping to PostgreSQL requires only a new implementation of `Store` — no changes to providers, main, or tests.

#### 1.8 Dev Environment Decision — File Fixture over LocalStack

LocalStack Pro ($35/month) is required for full Cost Explorer support. The free tier does not include the `ce` service. Rather than paying for Pro or maintaining a seeder, the ingestion service reads cost data directly from a local JSON file. This removes all external dependencies for local development.

**`DEV_MODE` switch:**
- `DEV_MODE=true` → reads from `fixtures/costs.json` and `fixtures/usage.json` directly
- `DEV_MODE=false` → calls real AWS Cost Explorer and (Phase 2) CloudWatch

---

## Phase 2 — Alpha (July – September 2026)

### Goal: Real cloud connectivity, single-tenant

#### 2.1 AWS Integration
- AWS Cost Explorer API already wired — `internal/provider/aws` is complete
- Add CloudWatch provider for usage metrics — same `CostExplorerAPI` mock pattern
- IAM policy required: `ce:GetCostAndUsage`, `cloudwatch:GetMetricStatistics`
- Never store customer credentials — use cross-account IAM role assumption (`sts:AssumeRole`)
- See [docs/production.md](production.md) for IAM policy details and multi-tenant role assumption pattern

#### 2.2 Auth & Multi-tenancy
- Auth: Clerk or Supabase Auth (JWT)
- Tenant isolation at database row level (`tenant_id` on all tables)
- Each tenant connects their own AWS account via IAM role ARN
- PostgreSQL replaces SQLite — implement `Store` interface for PostgreSQL (no changes to providers, analyzer, or API)
- See [docs/production.md](production.md) for the full PostgreSQL migration path and hosting options

#### 2.3 Alerting
- Weekly email digest: "You have $X in ghost spend this week"
- In-app notification bell
- Email provider: Resend or SendGrid

#### 2.4 Deployment
- Backend: Fly.io or Railway
- Frontend: Expo EAS Build → web deploy; TestFlight for mobile preview
- Database: Supabase (managed PostgreSQL)

---

## Phase 3 — Beta / GTM (October – December 2026)

### Goal: App Store launch, first paying customers

#### 3.1 Remediation Actions
- Pre-generated AWS CLI commands per resource type
- Terraform taint snippets for IaC-managed resources
- Dismiss workflow — mark a ghost as intentional with a reason; hidden from main list
- Audit trail — all dismiss/delegate/delete actions logged with timestamp and user

#### 3.2 Multi-cloud
- Azure Cost Management API
- GCP Billing Export → BigQuery

#### 3.3 Reporting
- Exportable PDF savings report (for FinOps teams presenting to CFO)
- Savings trend over time (chart)

#### 3.4 Mobile App
- Same Expo codebase — `npm run ios` / `npm run android`
- Expo EAS Build handles compilation — no Mac build machine needed
- Apple Developer account ($99/year) required for TestFlight and App Store

#### 3.5 App Store Submission
- Establish Operating UG before submission
- Privacy policy, terms of service

---

## Phase 4 — Proactive Cost Simulation (Q1–Q2 2027)

### Goal: Anticipate costs before deployment — own the full cost lifecycle

```
Plan → Deploy → Run
 ↑         ↑       ↑
Simulate  Gate   Optimize   ← AxiaOps owns all three
```

#### 4.1 IaC Plan Parser
- Parse `terraform plan -out=plan.json` and AWS CDK `cdk diff` output
- Extract resource types, sizes, regions, and counts
- Map each resource to its pricing model (on-demand, reserved, spot)

#### 4.2 Cost Estimation Engine
- Fetch live pricing from AWS Pricing API, Azure Retail Prices API, GCP Cloud Billing Catalog
- Compute estimated monthly cost delta per resource
- Flag resources with no cost tag — ownership gap surfaced at plan time

#### 4.3 What-if Scenarios
- "What if I use gp3 instead of gp2?" → show savings
- "What if I switch region from us-east-1 to eu-west-1?" → show delta
- "What if I use Spot instead of On-Demand?" → show risk vs savings

#### 4.4 CI/CD Budget Gate
- GitLab CI / GitHub Actions integration — runs on every merge request
- Posts estimated cost delta as a MR comment
- Configurable threshold: warn or block if monthly cost increase exceeds limit

#### 4.5 CLI Tool
- `axiaops estimate --plan plan.json` — standalone CLI for local use
- Installable via `brew install axiaops` or single binary download

---

## Tech Stack Summary

| Layer | Technology |
|-------|-----------|
| Backend | Go 1.25+ |
| Database | SQLite (MVP) → PostgreSQL |
| Frontend | React Native (Expo) — web + mobile |
| Auth | Clerk or Supabase Auth |
| Hosting | Fly.io / Railway |
| CI/CD | GitLab CI |
| Cloud APIs | AWS Cost Explorer, CloudWatch, Azure Cost Mgmt, GCP Billing |

---

## Milestones

| Date | Milestone | Status |
|------|-----------|--------|
| April 2026 | Go ingestion service + fixture data + zombie detection | ✅ Done |
| April 2026 | React Native web dashboard | ✅ Done (ahead of schedule) |
| April 2026 | Docker Compose full-stack + unit tests | ✅ Done (ahead of schedule) |
| July 2026 | AWS Cost Explorer + CloudWatch integration | Planned |
| August 2026 | Auth + multi-tenancy + PostgreSQL | Planned |
| September 2026 | Alpha with 3–5 beta users | Planned |
| October 2026 | App Store / Play Store submission | Planned |
| November 2026 | First paying customer | Planned |
| December 2026 | 10 customers, €5K MRR target | Planned |
| Q1 2027 | IaC plan parser + cost estimation engine | Planned |
| Q2 2027 | CI/CD budget gate | Planned |
| Q3 2027 | CLI tool + what-if scenarios | Planned |
