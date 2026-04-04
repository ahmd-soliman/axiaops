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

```bash
docker compose up
```

- Dashboard → `http://localhost:8081`
- API → `http://localhost:8080`

To stop:

```bash
docker compose down
```

### Option B — Manual (dev with hot reload)

**Prerequisites:** Go 1.25+, Node.js 20+

**1. Start the ingestion service**

Dev mode (no AWS account needed):
```bash
cd services/ingestion
DEV_MODE=true go run ./cmd/main.go
```

Real AWS mode:
```bash
cd services/ingestion
AWS_ACCOUNT_ID=123456789012 \
AWS_ACCESS_KEY_ID=AKIA... \
AWS_SECRET_ACCESS_KEY=... \
AWS_REGION=eu-central-1 \
go run ./cmd/main.go
```

> If you have the AWS CLI configured (`aws configure`), you can omit `AWS_ACCESS_KEY_ID` and `AWS_SECRET_ACCESS_KEY` — the SDK picks up credentials from `~/.aws/credentials` automatically.

**2. Start the dashboard**

```bash
cd services/dashboard
npm install      # first time only
npm run web
```

Opens at `http://localhost:8081`.

**VS Code shortcut:** Press **F5** → selects `ingestion (dev)` automatically, starts both services.

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
| [docs/business_plan.md](docs/business_plan.md) | Business model, pricing, GTM strategy |
| [docs/tax_strategy.md](docs/tax_strategy.md) | German tax structure, VAT, exit planning |

---

## Status

**Incubator phase — April 2026.** Phase 1 complete, ahead of schedule.
