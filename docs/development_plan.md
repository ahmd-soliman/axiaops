# Development Plan — AxiaOps FinOps

## Project Overview

**AxiaOps** — A FinOps SaaS tool that identifies idle/zombie cloud resources still incurring costs despite $0.00 usage. Target: AWS (initial), multi-cloud later.

---

## Phase 1 — Incubator / MVP (April – June 2026)

### Goal: Working AxiaOps Detector (local, no auth, fake data)

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
- **Language:** Go 1.24+
- **Framework:** Standard library + `net/http`
- **Data Layer:** SQLite (MVP) → PostgreSQL (production)
- **Flow:** ingest → store → analyze → serve

**Ingestion (`internal/provider`, `internal/storage`):**
- Provider interface — swap AWS Cost Explorer for file fixture with one env var (`DEV_MODE=true`)
- `INSERT OR IGNORE` deduplication — safe to re-run; logs inserted vs skipped counts

**Analysis (`internal/analyzer`):**
- `Detect()` joins cost records with usage records on `resource_id`
- Applies per-service threshold rules (see table below)
- A resource is flagged when `usage.avg <= threshold` for the entire billing period
- `owner` is derived from the `team` tag — every ghost includes an owner so remediation is actionable

**Detection rules:**

| Service | Metric | Threshold | Reason shown |
|---------|--------|-----------|--------------|
| AmazonEC2 | CPUUtilization | ≤ 5% | Instance CPU below 5% — likely idle |
| AmazonRDS | DatabaseConnections | = 0 | Zero connections — likely abandoned |
| AWSLambda | Invocations | = 0 | Zero invocations — likely unused |
| AmazonElasticLoadBalancing | RequestCount | = 0 | Zero requests — likely abandoned |
| AmazonVPC | BytesOutToDestination | = 0 | NAT Gateway zero bytes — likely unused |

Resources with no rule or no usage record are skipped — no determination is made without data.

**API Endpoints (`internal/api`):**
- `GET /ghosts` — list of detected zombie resources with cost, usage metric, reason, and owner
- `GET /summary` — aggregate savings figure and per-service breakdown

#### 1.4 Frontend — React Native
- Single "Dashboard" screen
- Big red number: **"Potential Monthly Savings: $X,XXX"**
- Scrollable list of ghost resources grouped by type
- Tap a resource → detail view with remediation suggestion (e.g., "Delete this EBS volume")
- Stack: Expo + React Native + React Query

#### 1.5 Infrastructure (local)
- Docker Compose: Go ingestion service + SQLite (file on disk)
- `.env`-based config (never committed)

#### 1.6 Dev Environment — File Fixture + SQLite

Local development uses a plain JSON fixture file and SQLite — no external services required.

**Why not LocalStack Cost Explorer:**
LocalStack Pro ($35/month) is required for full Cost Explorer support. The free tier does not include the Cost Explorer (`ce`) service. Rather than paying for Pro or maintaining a LocalStack S3 seeder just for dev, the ingestion service reads cost data directly from a local JSON file.

**Fixture flow:**
```
fixtures/costs.json
       │
    filefixture provider        — DEV_MODE=true
       │
    ingestion (main.go)
       │
    sqlite.Store (axiaops.db)   — INSERT OR IGNORE
       │
    axiaops.db
```

**`DEV_MODE` switch in `main.go`:**
- `DEV_MODE=true` → reads from `fixtures/costs.json` directly (local dev)
- `DEV_MODE=false` → calls real AWS Cost Explorer (production)

No code changes are needed when switching environments — only the environment variable changes.

**Running locally (VS Code):**
Use the `ingestion (dev)` launch configuration in `.vscode/launch.json`. It sets `DEV_MODE=true` and `FIXTURE_PATH=fixtures/costs.json` automatically.

**Running with Docker Compose:**
```bash
cd services/ingestion
docker compose up
```

The container runs the ingestion service with `DEV_MODE=true`. The SQLite database is written inside the container (ephemeral). Mount a volume if persistence across runs is needed.

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

**Duplicate handling:** `INSERT OR IGNORE` skips any row that violates the `UNIQUE` constraint. Running the ingestion service multiple times for the same date range is safe — only new records are written. The log line reports `inserted N, skipped M duplicates`.

**Querying the database (sqlite3 CLI):**
```bash
# open the database
sqlite3 services/ingestion/axiaops.db

# list all records
SELECT provider, service, region, amount, currency, period_start FROM cost_records;

# total cost by service
SELECT service, SUM(amount) AS total FROM cost_records GROUP BY service ORDER BY total DESC;

# records for a specific date range
SELECT * FROM cost_records WHERE period_start >= '2025-09-01' AND period_end <= '2025-09-30';
```

**GUI option:** Open `axiaops.db` with [DB Browser for SQLite](https://sqlitebrowser.org) for a visual table view.

**Production:** The `Store` interface (`internal/storage/storage.go`) is the only contract the rest of the code depends on. Swapping to PostgreSQL requires only a new implementation of `Store` — no changes to providers, main, or tests.

---

## Phase 2 — Alpha (July – September 2026)

### Goal: Real cloud connectivity, single-tenant

#### 2.1 AWS Integration
- Use AWS Cost and Usage Report (CUR) via S3 + Athena **or** AWS Cost Explorer API
- IAM Role with read-only permissions (`ce:GetCostAndUsage`, `ec2:Describe*`, `rds:Describe*`)
- Never store customer credentials — use cross-account IAM role assumption

#### 2.2 Auth & Multi-tenancy
- Auth: Clerk or Supabase Auth (JWT)
- Tenant isolation at database row level (tenant_id on all tables)
- Each tenant connects their own AWS account via IAM role ARN

#### 2.3 Alerting
- Weekly email digest: "You have $X in ghost spend this week"
- In-app notification bell

#### 2.4 Deployment
- Backend: Fly.io or Railway (cheap, no DevOps overhead)
- Frontend: Expo EAS Build → TestFlight / internal track
- Database: Supabase (managed PostgreSQL)

---

## Phase 3 — Beta / GTM (October – December 2026)

### Goal: App Store launch, first paying customers

#### 3.1 Remediation Actions (read-only first)
- "One-click" remediation suggestions with CLI commands pre-generated
- **Terraform/Pulumi taint scripts** — modern shops manage infrastructure via IaC; a CLI command alone won't work because the next pipeline run will recreate the resource. Provide a `terraform taint` or resource removal snippet alongside the CLI command.
- Later: actual API calls with customer approval step

#### 3.2 Multi-cloud
- Azure Cost Management API
- GCP Billing Export → BigQuery

#### 3.3 Reporting
- Exportable PDF/CSV savings report (useful for FinOps teams presenting to CFO)
- Savings trend over time (chart)

#### 3.4 App Store Submission
- Establish Operating UG **before** App Store submission (see Legal section)
- Privacy policy, terms of service

---

## Phase 4 — Proactive Cost Simulation (Q1–Q2 2027)

### Goal: Anticipate costs before deployment — own the full cost lifecycle

This phase transforms AxiaOps from a reactive tool into a proactive one. Instead of only showing what was spent, it predicts what will be spent before infrastructure is deployed.

```
Plan → Deploy → Run
 ↑         ↑       ↑
Simulate  Gate   Optimize   ← AxiaOps owns all three
```

#### 4.1 IaC Plan Parser
- Parse `terraform plan -out=plan.json` output
- Parse AWS CDK `cdk diff` output
- Extract resource types, sizes, regions, and counts
- Map each resource to its pricing model (on-demand, reserved, spot)

#### 4.2 Cost Estimation Engine
- Fetch live pricing from AWS Pricing API, Azure Retail Prices API, GCP Cloud Billing Catalog
- Compute estimated monthly cost per resource
- Aggregate into a total estimated monthly delta (how much will this change my bill?)
- Flag resources with no cost tag — ownership gap surfaced at plan time, not after

#### 4.3 What-if Scenarios
- "What if I use gp3 instead of gp2?" → show savings
- "What if I switch region from us-east-1 to eu-west-1?" → show delta
- "What if I use Spot instead of On-Demand?" → show risk vs savings

#### 4.4 CI/CD Budget Gate
- GitHub Actions / GitLab CI integration — runs on every pull request
- Posts estimated cost delta as a PR comment
- Configurable threshold: warn or block if monthly cost increase exceeds limit
- Example: block merge if a PR adds >€500/month in new infrastructure

#### 4.5 CLI Tool
- `axiaops estimate --plan plan.json` — standalone CLI for local use
- Output: JSON or human-readable table of estimated costs
- Installable via `brew install axiaops` or single binary download

---

## Tech Stack Summary

| Layer | Technology |
|-------|-----------|
| Backend | Go 1.22+ |
| Database | SQLite (MVP) → PostgreSQL |
| Frontend | React Native (Expo) |
| Auth | Clerk or Supabase Auth |
| Hosting | Fly.io / Railway |
| CI/CD | GitHub Actions |
| Cloud APIs | AWS CUR, Cost Explorer, Azure Cost Mgmt, GCP Billing |

---

## Milestones

| Date | Milestone |
|------|-----------|
| April 2026 | Mock data generator + Go ingestion service MVP |
| May 2026 | React Native dashboard — local demo |
| June 2026 | Docker Compose full-stack running locally |
| July 2026 | AWS Cost Explorer integration — real data |
| August 2026 | Auth + multi-tenancy |
| September 2026 | Alpha with 3–5 beta users |
| October 2026 | App Store / Play Store submission |
| November 2026 | First paying customer |
| December 2026 | 10 customers, €5K MRR target |
| Q1 2027 | IaC plan parser + cost estimation engine |
| Q2 2027 | CI/CD budget gate (GitHub Actions / GitLab CI) |
| Q3 2027 | CLI tool + what-if scenarios |
