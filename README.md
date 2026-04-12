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
| Database | PostgreSQL 16 (with Row-Level Security) |
| Frontend | React Native (Expo) — web |
| Auth | Kinde OAuth 2.0 (PKCE + RS256 JWT) |
| Hosting | AWS App Runner + RDS |
| Cloud APIs | AWS Cost Explorer, CloudWatch |

---

## Running Locally

Use Docker Compose with Make commands:

### Development Mode

**Dev mode (fixture data, no AWS needed):**

Create `services/ingestion/.env` from the example:
```bash
cp services/ingestion/.env.example services/ingestion/.env
```

The default `.env` has `DEV_MODE=true` — no changes needed for dev mode.

```bash
make start-dev
```

- Dashboard → `http://localhost`
- API → `http://localhost/api/`
- Ingestion → `http://localhost:8081` (internal)

**Real AWS mode:**

Edit `services/ingestion/.env`:
```
DEV_MODE=false
AWS_ACCOUNT_ID=123456789012
AWS_REGION=eu-central-1
```

Your `~/.aws/credentials` are mounted into the container automatically — no need to put keys in the `.env` file.

```bash
make start-staging
```

To stop:
```bash
make stop
```

> `.env` is gitignored — it will never be committed. See `services/ingestion/.env.example` for the full list of available variables.

---

## Running Against Real AWS

### Prerequisites

1. **Enable Cost Explorer** in your AWS account — Billing and Cost Management → Cost Explorer → Enable. Data takes up to 24 hours to appear after first enabling.

2. **Create IAM policies** — do this from your root or admin account in the AWS Console.

   **Policy 1 — `AxiaOpsReadOnly`** (required, always attached):

   IAM → Policies → Create policy → JSON tab:
   ```json
   {
     "Version": "2012-10-17",
     "Statement": [{
       "Effect": "Allow",
       "Action": [
         "ce:GetCostAndUsage",
         "cloudwatch:GetMetricStatistics",
         "cloudwatch:ListMetrics",
         "ec2:DescribeInstances",
         "ec2:DescribeNatGateways",
         "rds:DescribeDBInstances",
         "lambda:ListFunctions",
         "elasticloadbalancing:DescribeLoadBalancers"
       ],
       "Resource": "*"
     }]
   }
   ```
   Description: `Read-only Cost Explorer, CloudWatch and resource discovery access for AxiaOps ingestion service`

   **Policy 2 — `AxiaOpsTestResources`** (optional, attach only when generating test data):

   IAM → Policies → Create policy → JSON tab:
   ```json
   {
     "Version": "2012-10-17",
     "Statement": [{
       "Effect": "Allow",
       "Action": [
         "ec2:AllocateAddress",
         "ec2:ReleaseAddress",
         "ec2:DescribeAddresses"
       ],
       "Resource": "*"
     }]
   }
   ```
   Description: `EC2 test resources - allocate and release Elastic IPs for dev testing only. Detach when not needed.`

3. **Create an IAM user** (`axiaops-dev`):
   - IAM → Users → Create user → name it `axiaops-dev`
   - Attach permissions → **Attach policies directly** → attach `AxiaOpsReadOnly`
   - Generate an access key: Security Credentials → Create access key → "Application running outside AWS"

4. **Find your Account ID** — top-right corner of the AWS Console.

### Running with Real AWS

Configure your AWS credentials via AWS CLI:

```bash
aws configure
# AWS Access Key ID: AKIA...
# AWS Secret Access Key: ...
# Default region: eu-central-1
```

Then use `make start-staging` (see "Running Locally" section above). The Docker container mounts your `~/.aws/credentials` automatically.

**What happens:**

1. Ingestion service fetches cost data from AWS Cost Explorer (last 30 days)
2. Fetches usage metrics from CloudWatch
3. Stores records in PostgreSQL with tenant isolation (RLS)
4. API serves requests at `http://localhost/api/`

### Known limitations (Phase 2)

| Limitation | Phase |
|-----------|-------|
| Single AWS account only | Phase 3 |
| AWS only (no Azure/GCP) | Phase 3 |

### Creating a Test Resource (Elastic IP)

If your AWS account has no recent spend, Cost Explorer returns no data. The cheapest way to generate real cost data is an **Elastic IP** (~$0.12/day when unattached).

**Step 1 — Attach `AxiaOpsTestResources` to `axiaops-dev`**

From your root or admin account (the user cannot modify its own permissions):

IAM → Users → axiaops-dev → Add permissions → Attach policies directly → attach `AxiaOpsTestResources`

> If you haven't created `AxiaOpsTestResources` yet, see the Prerequisites section above.

**Step 2 — Allocate the Elastic IP**

```bash
aws ec2 allocate-address --domain vpc --region eu-central-1
```

The IP starts incurring cost immediately. It will appear in Cost Explorer within 24 hours.

**Step 3 — Release when done**

```bash
# Use the AllocationId from the output above
aws ec2 release-address --allocation-id eipalloc-xxxxxxxxx --region eu-central-1
```

> Cost stops the moment you release. Detach `AxiaOpsTestResources` from the user afterward — it is not needed for normal ingestion.

---

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
make test           # All Go unit tests
make test-postgres  # PostgreSQL integration tests (RLS, migrations)
make test-all       # Unit + integration
```

---

## API Endpoints

| Method | Path | Description |
|--------|------|-------------|
| `GET` | `/api/ghosts` | List of all detected zombie resources |
| `GET` | `/api/summary` | Aggregate savings and per-service breakdown |

API is available at `http://localhost/api/` when running with Docker Compose.

---

## Detection Rules

| Service | Metric | Threshold | Verdict |
|---------|--------|-----------|---------|
| AmazonEC2 | CPUUtilization | ≤ 5% | Idle instance |
| AmazonRDS | DatabaseConnections | = 0 | Abandoned database |
| AWSLambda | Invocations | = 0 | Unused function |
| ELB | RequestCount | = 0 | Abandoned load balancer |
| VPC (NAT) | BytesOutToDestination | = 0 | Unused NAT Gateway |
| VPC (EIP) | NetworkInterfaceAttachment | = 0 | Unattached EIP |

> Rules do not change without business justification. See `CLAUDE.md` for FinOps domain thresholds.

---

## Environment Variables

| Variable | Default | Description |
|----------|---------|-------------|
| `DEV_MODE` | `false` | `true` → use file fixtures instead of real AWS |
| `DATABASE_URL` | — | PostgreSQL connection string (app user) |
| `MIGRATION_DATABASE_URL` | — | PostgreSQL connection string (owner/admin, migrations only) |
| `ENCRYPTION_KEY` | — | 32-byte hex key for AES-256-GCM (AWS secrets encryption) |
| `AWS_ACCOUNT_ID` | — | Required when `DEV_MODE=false` |
| `AWS_REGION` | `eu-central-1` | AWS region |

---

## Project Structure

```
axiaops/
├── Makefile                        # Build targets (start-dev, test, etc.)
├── docker-compose.yml              # Docker Compose stack (postgres, ingestion, api, dashboard)
├── go.work                         # Go workspace (api, ingestion, shared)
├── services/
│   ├── api/                        # Go API service (:8080)
│   │   ├── cmd/main.go
│   │   └── internal/handlers/      # REST endpoints + auth middleware
│   ├── ingestion/                  # Go ingestion service (:8081)
│   │   ├── cmd/main.go
│   │   ├── fixtures/               # Dev mode: costs.json, usage.json
│   │   └── internal/
│   │       ├── analyzer/           # Zombie detection thresholds
│   │       ├── aws/                # CloudWatch + Cost Explorer clients
│   │       └── scheduler/          # Periodic scans
│   ├── shared/                     # Go shared library
│   │   ├── storage/postgres/       # PostgreSQL + RLS
│   │   ├── crypto/                 # AES-256-GCM encryption
│   │   ├── model/                  # Domain types
│   │   └── logging/                # slog helpers
│   └── dashboard/                  # React Native (Expo) web app (nginx :80)
│       └── src/
│           ├── screens/
│           └── components/
├── docs/                           # Architecture, deployment, business plan
└── scripts/                        # Tooling (migrations, seed data)
```

---

## Roadmap

### Phase 1 — MVP ✅ Complete
- [x] Cost + usage fixture data
- [x] Go ingestion service with PostgreSQL and RLS
- [x] Zombie detection with per-service threshold rules
- [x] REST API (`/api/ghosts`, `/api/summary`)
- [x] React Native web dashboard
- [x] Docker Compose setup
- [x] Comprehensive test coverage

### Phase 2 — Alpha (in progress)
- [x] Real AWS Cost Explorer + CloudWatch integration
- [x] Kinde OAuth 2.0 auth + multi-tenancy (RLS)
- [x] Ingestion scheduled scans
- [ ] Production deployment (App Runner + RDS)
- [ ] Observability (CloudWatch logs + metrics)

### Phase 3 — Beta / Launch
- [ ] Multi-cloud (Azure, GCP)
- [ ] Mobile app (iOS + Android)
- [ ] Remediation workflow (dismiss, delegate, runbooks)
- [ ] Advanced reporting + audit trail

---

## Documentation

| File | Description |
|------|-------------|
| [docs/development_plan.md](docs/development_plan.md) | Architecture decisions, data model, DB schema, phase plans |
| [docs/middleware.md](docs/middleware.md) | API middleware chain — auth, rate limiting, request ID, metrics |
| [docs/production.md](docs/production.md) | Production setup — IAM, PostgreSQL migration, TLS, hosting, scheduling |
| [docs/deployment.md](docs/deployment.md) | Deployment options, Kubernetes vs alternatives, cost estimates by phase |
| [docs/business_plan.md](docs/business_plan.md) | Business model, pricing, GTM strategy |
| [docs/tax_strategy.md](docs/tax_strategy.md) | German tax structure, VAT, exit planning |

---

## Status

**Phase 2 (Alpha) — April 2026.** Phase 1 complete. Real AWS integration shipped; working on production deployment and observability.
