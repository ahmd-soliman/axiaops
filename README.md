# AxiaOps

A FinOps SaaS tool that detects idle and zombie cloud resources still incurring costs despite zero usage — and surfaces an actionable remediation workflow with a full audit trail.

> **Know the value of every resource.**

---

## What It Does

AxiaOps connects to your cloud billing via read-only IAM access and delivers:

- **The Ghost Number** — total monthly spend on idle resources across all connected accounts
- **The Ghost List** — itemized breakdown by resource with cost, usage metric, and remediation suggestion
- **Owner Resolution** — every ghost includes the responsible team derived from resource tags
- **The Weekly Digest** — email/Slack alert when new ghosts appear
- **Multi-account Dashboard** — single pane for managing multiple cloud accounts

---

## Tech Stack

| Layer | Technology |
|-------|-----------|
| Backend | Go 1.24+ |
| Database | SQLite (MVP) → PostgreSQL |
| Frontend | React Native (Expo) — web + mobile |
| Auth | Clerk or Supabase Auth |
| Hosting | Fly.io / Railway |
| CI/CD | GitLab CI |
| Cloud APIs | AWS Cost Explorer, Azure Cost Mgmt, GCP Billing |

---

## Running Locally

There are two services to run: the **ingestion + analysis API** (Go) and the **dashboard** (Expo web).

### Prerequisites

- Go 1.24+
- Node.js 20+

### 1. Start the ingestion service

**Dev mode (file fixture — no AWS account needed):**

```bash
cd services/ingestion
DEV_MODE=true go run ./cmd/main.go
```

**Real AWS mode:**

```bash
cd services/ingestion
AWS_ACCOUNT_ID=123456789012 \
AWS_ACCESS_KEY_ID=AKIA... \
AWS_SECRET_ACCESS_KEY=... \
AWS_REGION=eu-central-1 \
go run ./cmd/main.go
```

> If you have the AWS CLI configured (`aws configure`), you can omit `AWS_ACCESS_KEY_ID` and `AWS_SECRET_ACCESS_KEY` — the SDK picks up credentials from `~/.aws/credentials` automatically.

What it does:
1. Reads cost records from AWS Cost Explorer (or `fixtures/costs.json` in dev mode)
2. Reads usage metrics from `fixtures/usage.json` (CloudWatch integration coming in Phase 2)
3. Stores records in `axiaops.db` (SQLite, created automatically)
4. Runs zombie detection — flags resources with cost but no usage
5. Starts the HTTP API on `http://localhost:8080`

Logs on startup:
```
[filefixture] fetched 13 records — inserted 13, skipped 0 duplicates
analysis: 5 ghost resources detected — potential savings 494.40 USD/month
api: listening on :8080  →  GET /ghosts  GET /summary
```

### 2. Start the dashboard

```bash
cd services/dashboard
npm install      # first time only
npm run web
```

Opens at `http://localhost:8081`.

### 3. Test the API directly

Open `services/ingestion/api.http` in VS Code with the **REST Client** extension and click **Send Request**.

Or use curl:

```bash
curl http://localhost:8080/summary
curl http://localhost:8080/ghosts
```

### Environment variables

| Variable | Default | Description |
|----------|---------|-------------|
| `DEV_MODE` | `false` | `true` → use file fixtures instead of real AWS |
| `FIXTURE_PATH` | `fixtures/costs.json` | Path to cost fixture file |
| `USAGE_PATH` | `fixtures/usage.json` | Path to usage fixture file |
| `DB_PATH` | `axiaops.db` | SQLite database file path |
| `API_ADDR` | `:8080` | HTTP server listen address |
| `AWS_ACCOUNT_ID` | — | Required when `DEV_MODE=false` |

### Running with VS Code

Press **F5** and select `ingestion (dev)` — sets `DEV_MODE=true` automatically and starts the API server with Delve attached for debugging.

---

## API Endpoints

| Method | Path | Description |
|--------|------|-------------|
| `GET` | `/ghosts` | List of all detected zombie resources |
| `GET` | `/summary` | Aggregate savings and per-service breakdown |

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

## Project Structure

```
axiaops/
├── services/
│   ├── ingestion/              # Go — cost ingestion + analysis + API
│   │   ├── cmd/main.go         # Entry point
│   │   ├── fixtures/
│   │   │   ├── costs.json      # Cost records fixture (dev)
│   │   │   └── usage.json      # Usage metrics fixture (dev)
│   │   └── internal/
│   │       ├── analyzer/       # Zombie detection logic
│   │       ├── api/            # HTTP handlers
│   │       ├── model/          # Shared types (CostRecord, GhostResource)
│   │       ├── provider/       # AWS Cost Explorer + file fixture
│   │       └── storage/sqlite/ # SQLite store
│   └── dashboard/              # Expo — React Native web dashboard
│       └── src/
│           ├── api/client.js   # Fetch wrapper for the Go API
│           └── screens/
│               ├── DashboardScreen.js  # Savings banner + ghost list
│               └── DetailScreen.js    # Resource detail + remediation hint
└── docs/                       # Development plan, business plan, etc.
```

---

## Roadmap

### Phase 1 — MVP (April – June 2026)
- [x] Cost fixture data + file-based ingestion
- [x] SQLite storage with deduplication
- [x] Zombie detection with per-service threshold rules
- [x] REST API (`/ghosts`, `/summary`)
- [x] React Native web dashboard
- [ ] Docker Compose — run both services with one command

### Phase 2 — Alpha (July – September 2026)
- AWS Cost Explorer integration (real data)
- Auth + multi-tenancy
- Alerting (email + Slack)

### Phase 3 — Beta / Launch (October – December 2026)
- Mobile app (iOS + Android)
- Azure and GCP support
- PDF savings reports

---

## Documentation

| File | Description |
|------|-------------|
| [docs/development_plan.md](docs/development_plan.md) | Full technical plan, architecture, milestones |
| [docs/business_plan.md](docs/business_plan.md) | Business model, pricing, GTM strategy |
| [docs/tax_strategy.md](docs/tax_strategy.md) | German tax structure, VAT, exit planning |

---

## Status

**Incubator phase — April 2026.** MVP in active development.
