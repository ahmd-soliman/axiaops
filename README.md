# AxiaOps

A FinOps SaaS tool that detects idle and zombie cloud resources still incurring costs despite zero usage — and surfaces an actionable remediation workflow with a full audit trail.

> **Know the value of every resource.**

---

## What It Does

AxiaOps connects to your cloud billing via read-only IAM access and delivers:

- **The Ghost Number** — total monthly spend on idle resources across all connected accounts
- **The Ghost List** — itemized breakdown by resource with cost, usage metric, and remediation suggestion
- **Owner Resolution** — every ghost includes the responsible team derived from resource tags
- **The Weekly Digest** — email/Slack alert when new ghosts appear (Phase 2)
- **Multi-account Dashboard** — single pane for managing multiple cloud accounts (Phase 2)

---

## Tech Stack

| Layer | Technology |
|-------|-----------|
| Backend | Go 1.25+ |
| Database | SQLite (MVP) → PostgreSQL |
| Frontend | React Native (Expo) — web + mobile |
| Auth | Clerk or Supabase Auth (Phase 2) |
| Hosting | Fly.io / Railway |
| CI/CD | GitLab CI |
| Cloud APIs | AWS Cost Explorer, Azure Cost Mgmt, GCP Billing |

---

## Running Locally

Two ways to run: **Docker Compose** (recommended, one command) or **manually** (for development with hot reload).

### Option A — Docker Compose

**Dev mode (fixture data, no AWS needed):**

Create `services/ingestion/.env` from the example:
```bash
cp services/ingestion/.env.example services/ingestion/.env
```

The default `.env` has `DEV_MODE=true` — no changes needed for dev mode.

```bash
docker compose up
```

- Dashboard → `http://localhost:8081`
- API → `http://localhost:8080`

**Real AWS mode:**

Edit `services/ingestion/.env`:
```
DEV_MODE=false
AWS_ACCOUNT_ID=123456789012
AWS_REGION=eu-central-1
```

Your `~/.aws/credentials` are mounted into the container automatically — no need to put keys in the `.env` file.

```bash
docker compose up
```

To stop:
```bash
docker compose down
```

> `.env` is gitignored — it will never be committed. See `services/ingestion/.env.example` for the full list of available variables.

### Option B — Manual (dev mode, no AWS)

**Prerequisites:** Go 1.25+, Node.js 20+

```bash
cd services/ingestion
DEV_MODE=true go run ./cmd/main.go
```

```bash
cd services/dashboard
npm install      # first time only
npm run web
```

Opens at `http://localhost:8081`. Press **F5** in VS Code to start both automatically.

---

## Running Against Real AWS

### Prerequisites

1. **Enable Cost Explorer** in your AWS account — Billing and Cost Management → Cost Explorer → Enable. Data takes up to 24 hours to appear after first enabling.

2. **Create an IAM policy** named `AxiaOpsReadOnly`:

```json
{
  "Version": "2012-10-17",
  "Statement": [
    {
      "Effect": "Allow",
      "Action": ["ce:GetCostAndUsage"],
      "Resource": "*"
    }
  ]
}
```

3. **Create an IAM user** (`axiaops-dev`), attach the policy, and generate an access key under Security Credentials → Create access key → "Application running outside AWS".

4. **Find your Account ID** — top-right corner of the AWS Console.

### Option A — AWS CLI credentials (recommended)

```bash
aws configure
# AWS Access Key ID: AKIA...
# AWS Secret Access Key: ...
# Default region: eu-central-1
# Default output format: json
```

Then run — the SDK picks up credentials automatically:

```bash
cd services/ingestion
AWS_ACCOUNT_ID=123456789012 go run ./cmd/main.go
```

### Option B — Environment variables

```bash
cd services/ingestion
AWS_ACCOUNT_ID=123456789012 \
AWS_ACCESS_KEY_ID=AKIA... \
AWS_SECRET_ACCESS_KEY=... \
AWS_REGION=eu-central-1 \
go run ./cmd/main.go
```

### What happens

1. Fetches the last 30 days of cost data from AWS Cost Explorer
2. Stores records in `axiaops.db` (created automatically)
3. Loads usage metrics from `fixtures/usage.json` — **CloudWatch integration is Phase 2**, so zombie detection still uses fixture usage data even against real cost data
4. Starts the API on `http://localhost:8080`

### Known limitations (Phase 1)

| Limitation | Phase |
|-----------|-------|
| Usage metrics come from fixture, not CloudWatch | Phase 2 |
| Single AWS account only | Phase 2 |
| No auth — API is open | Phase 2 |
| Ingestion runs once at startup, not on a schedule | Phase 2 |

### Troubleshooting

**`DataUnavailableException: Data is not available`**
Cost Explorer needs up to 24 hours to ingest data after being enabled for the first time. Wait and retry.

**`AccessDeniedException`**
The IAM user is missing the `ce:GetCostAndUsage` permission. Check the policy is attached correctly.

**`AWS_ACCOUNT_ID is required`**
You ran without `DEV_MODE=true` but forgot to set `AWS_ACCOUNT_ID`.

---

## Running Tests

```bash
cd services/ingestion
go test ./...           # uses cache if nothing changed
go test ./... -count=1  # force full re-run
```

24 tests across 5 packages — all pass.

---

## API Endpoints

| Method | Path | Description |
|--------|------|-------------|
| `GET` | `/ghosts` | List of all detected zombie resources |
| `GET` | `/summary` | Aggregate savings and per-service breakdown |

Test with the REST Client extension: open `services/ingestion/api.http` in VS Code and click **Send Request**.

**Example `/summary` response:**
```json
{
  "total_ghosts": 5,
  "potential_monthly_savings": 494.40,
  "currency": "USD",
  "by_service": {
    "AmazonRDS": { "ghosts": 1, "savings": 210.00 },
    "AmazonEC2": { "ghosts": 1, "savings": 189.60 }
  }
}
```

---

## Detection Rules (MVP)

| Service | Metric | Threshold | Verdict |
|---------|--------|-----------|---------|
| AmazonEC2 | CPUUtilization | ≤ 5% | Idle instance |
| AmazonRDS | DatabaseConnections | = 0 | Abandoned database |
| AWSLambda | Invocations | = 0 | Unused function |
| AmazonElasticLoadBalancing | RequestCount | = 0 | Abandoned load balancer |
| AmazonVPC | BytesOutToDestination | = 0 | Unused NAT Gateway |

---

## Environment Variables

| Variable | Default | Description |
|----------|---------|-------------|
| `DEV_MODE` | `false` | `true` → use file fixtures instead of real AWS |
| `FIXTURE_PATH` | `fixtures/costs.json` | Path to cost fixture file |
| `USAGE_PATH` | `fixtures/usage.json` | Path to usage fixture file |
| `DB_PATH` | `axiaops.db` | SQLite database file path |
| `API_ADDR` | `:8080` | HTTP server listen address |
| `AWS_ACCOUNT_ID` | — | Required when `DEV_MODE=false` |

---

## Project Structure

```
axiaops/
├── docker-compose.yml              # One-command local stack
├── dev.sh                          # Alternative: shell script start/stop
├── services/
│   ├── ingestion/                  # Go — cost ingestion + analysis + API
│   │   ├── cmd/main.go             # Entry point
│   │   ├── fixtures/
│   │   │   ├── costs.json          # Cost records fixture (dev)
│   │   │   └── usage.json          # Usage metrics fixture (dev)
│   │   └── internal/
│   │       ├── analyzer/           # Zombie detection logic + tests
│   │       ├── api/                # HTTP handlers + tests
│   │       ├── model/              # Shared types (CostRecord, GhostResource)
│   │       ├── provider/           # AWS Cost Explorer + file fixture + tests
│   │       └── storage/sqlite/     # SQLite store + tests
│   └── dashboard/                  # Expo — React Native web dashboard
│       └── src/
│           ├── api/client.js       # Fetch wrapper for the Go API
│           └── screens/
│               ├── DashboardScreen.js  # Savings banner + ghost list
│               └── DetailScreen.js    # Resource detail + remediation hint
├── scripts/
│   └── create_user_stories.py      # Creates GitLab issues via API
└── docs/                           # Architecture, business plan, tax strategy
```

---

## Roadmap

### Phase 1 — MVP (April – June 2026)
- [x] Cost + usage fixture data
- [x] Go ingestion service with SQLite storage and deduplication
- [x] Zombie detection with per-service threshold rules
- [x] REST API (`/ghosts`, `/summary`) with CORS
- [x] React Native web dashboard (dark navy + orange design)
- [x] Unit tests — 24 tests across 5 packages
- [x] Docker Compose — single command runs everything

### Phase 2 — Alpha (July – September 2026)
- [ ] Real AWS Cost Explorer + CloudWatch integration
- [ ] Auth + multi-tenancy (Clerk or Supabase)
- [ ] Multi-account support (cross-account IAM role assumption)
- [ ] Weekly email digest

### Phase 3 — Beta / Launch (October – December 2026)
- [ ] Mobile app (iOS + Android)
- [ ] Azure and GCP support
- [ ] Remediation workflow (dismiss, delegate, one-click CLI commands)
- [ ] PDF savings reports

---

## Documentation

| File | Description |
|------|-------------|
| [docs/development_plan.md](docs/development_plan.md) | Architecture decisions, data model, DB schema, phase plans |
| [docs/production.md](docs/production.md) | Production setup — IAM, PostgreSQL migration, TLS, hosting, scheduling |
| [docs/deployment.md](docs/deployment.md) | Deployment options, Kubernetes vs alternatives, cost estimates by phase |
| [docs/business_plan.md](docs/business_plan.md) | Business model, pricing, GTM strategy |
| [docs/tax_strategy.md](docs/tax_strategy.md) | German tax structure, VAT, exit planning |

---

## Status

**Incubator phase — April 2026.** Phase 1 complete, ahead of schedule.
