# AxiaOps

A self-hosted, open-source FinOps tool that detects idle and zombie cloud resources still incurring costs despite zero usage — and surfaces an actionable remediation workflow with a full audit trail.

> **Know the value of every resource.**

---

## What It Does

AxiaOps connects to your cloud billing via read-only IAM access and delivers:

- **The Zombie Number** — total monthly spend on idle resources across all connected accounts
- **The Zombie List** — itemized breakdown by resource with cost, usage metric, and remediation suggestion
- **Owner Resolution** — every zombie includes the responsible team derived from resource tags
- **The Weekly Digest** — email/Slack alert when new zombies appear (Phase 2)
- **Multi-account Dashboard** — single pane for managing multiple cloud accounts (Phase 2)

---

## Tech Stack

| Layer | Technology |
|-------|-----------|
| Backend | Go 1.25+ |
| Database | PostgreSQL 17 (with Row-Level Security) |
| Frontend | Vite + React — web |
| Auth | Native cookie sessions (argon2id) + OIDC SSO |
| Hosting | AWS ECS Express + RDS |
| Cloud APIs | AWS Cost Explorer, CloudWatch |

---

## Running Locally

Use Docker Compose with Make commands:

### Development Mode

Create `services/ingestion/.env` from the example:
```bash
cp services/ingestion/.env.example services/ingestion/.env
```

Edit it with your AWS account details:
```
AWS_ACCOUNT_ID=123456789012
AWS_REGION=eu-central-1
```

Your `~/.aws/credentials` are mounted into the container automatically — no need to put keys in the `.env` file.

```bash
make start-dev
```

- Dashboard → `http://localhost`
- API → `http://localhost/api/`
- Ingestion → `http://localhost:8081` (internal)

To exercise the full auth chain (native cookie sessions, no DEV_MODE bypass), use:
```bash
make start-staging
```
First-run bootstrap walkthrough lives in `docs/AUTHENTICATION.md` § 3.

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
         "ec2:DescribeVolumes",
         "ec2:DescribeSnapshots",
         "ec2:DescribeAddresses",
         "ec2:DescribeImages",
         "rds:DescribeDBInstances",
         "lambda:ListFunctions",
         "elasticloadbalancing:DescribeLoadBalancers",
         "elasticache:DescribeCacheClusters",
         "es:ListDomainNames",
         "redshift:DescribeClusters",
         "sagemaker:ListEndpoints",
         "dynamodb:ListTables",
         "eks:ListClusters"
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

**Configure your AWS credentials:**

```bash
aws configure
```

You'll be prompted for:
- AWS Access Key ID: `AKIA...`
- AWS Secret Access Key: `...`
- Default region: `eu-central-1`

Then use `make start-staging` (see "Running Locally" section above). The Docker container mounts your `~/.aws/credentials` automatically.

**What happens:**

1. Ingestion service fetches cost data from AWS Cost Explorer (last 30 days)
2. Fetches usage metrics from CloudWatch
3. Stores records in PostgreSQL with organization isolation (RLS)
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

Run this command to create an unattached Elastic IP:

```bash
aws ec2 allocate-address --domain vpc --region eu-central-1
```

The IP starts incurring cost immediately. It will appear in Cost Explorer within 24 hours.

**Step 3 — Release when done**

Use the `AllocationId` from the output above:

```bash
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
make test              # All Go unit tests
make test-storage      # PostgreSQL integration tests (RLS, migrations)
make test-integration  # Full stack integration tests (Docker Compose)
make test-all          # Unit + postgres integration
```

### Integration Tests

Self-contained integration tests run in isolated Docker network:

```bash
make test-integration
```

**What it does:**
- Spins up postgres, redis, ingestion, api, and init-organization containers
- Runs migrations and creates test organization
- Executes 11 integration tests covering API, Redis, and ingestion
- Cleans up automatically

**No port conflicts** - all services communicate via container names on isolated network.

## Seeding Dev Data

Requires `psql`. If not installed:

```bash
brew install libpq
echo 'export PATH="/opt/homebrew/opt/libpq/bin:$PATH"' >> ~/.zshrc && source ~/.zshrc
```

**Basic seed (1000 days, simple random variation):**
```bash
make seed
```

Seed data includes 90 days of realistic trend snapshots (gradual growth, weekly patterns, random noise) for chart development.

---

## API Endpoints

| Method | Path | Description |
|--------|------|-------------|
| `GET` | `/api/zombies` | List of all detected zombie resources |
| `GET` | `/api/summary` | Aggregate savings and per-service breakdown |

API is available at `http://localhost/api/` when running with Docker Compose.

---

## Detection Rules

### CloudWatch-Based Detection (Tier 0)

| Service | Metric | Threshold | Verdict |
|---------|--------|-----------|---------|
| AmazonEC2 | CPUUtilization | ≤ 5% | Idle instance |
| AmazonRDS | DatabaseConnections | = 0 | Abandoned database |
| AWSLambda | Invocations | = 0 | Unused function |
| ELB | RequestCount | = 0 | Abandoned load balancer |
| VPC (NAT) | BytesOutToDestination | = 0 | Unused NAT Gateway |

### API-Only Detection (Tier 1) ✅

| Resource Type | Detection Method | Cost Formula | Threshold |
|---------------|------------------|--------------|-----------|
| **Unattached EIP** | `ec2:DescribeAddresses` | $3.60/month | No network interface attached |
| **Unattached EBS Volume** | `ec2:DescribeVolumes` | `sizeGB × $0.08/month` | State = "available" |
| **Orphaned EBS Snapshot** | `ec2:DescribeSnapshots` | `sizeGB × $0.05/month` | Source volume deleted + not backing AMI |
| **Stopped EC2 Instance** | `ec2:DescribeInstances` | `ebsGB × $0.08/month` | Stopped > 30 days |
| **Old AMI** | `ec2:DescribeImages` | `snapshotGB × $0.05/month` | Age > 90 days + not in use |

> **Tier 1 detections** are API-only (no CloudWatch needed) and typically surface the largest savings in real-world FinOps audits.

### CloudWatch-Based Detection (Tier 2) ✅

| Service | Metric | Threshold | Typical Cost | Verdict |
|---------|--------|-----------|--------------|---------|
| ElastiCache | CurrConnections (`AWS/ElastiCache`) | = 0 | $25-100/mo | Idle cluster |
| OpenSearch/ES | SearchRate (`AWS/ES`) | = 0 | $25+/mo | Unused cluster |
| Redshift | DatabaseConnections (`AWS/Redshift`) | = 0 | $180+/mo | Abandoned cluster |
| SageMaker | Invocations (`AWS/SageMaker`) | = 0 | $100+/mo | Forgotten endpoint |
| DynamoDB | ConsumedReadCapacityUnits (`AWS/DynamoDB`) | = 0 | Varies | Unused provisioned table |
| EKS | cluster_node_count (`ContainerInsights`) | = 0 | $73/mo | Control plane with no nodes |

> **Tier 2 detections** use CloudWatch metrics and fit the existing rule framework. EKS detection requires [Container Insights](https://docs.aws.amazon.com/AmazonCloudWatch/latest/monitoring/Container-Insights-setup-EKS.html) to be enabled on the cluster — clusters without it are skipped rather than producing false positives.

> Rules do not change without business justification. See `CLAUDE.md` for FinOps domain thresholds.

---

## Environment Variables

| Variable | Default | Description |
|----------|---------|-------------|
| `DEV_MODE` | `false` | `true` → use mock data instead of real AWS |
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
│   │   └── internal/
│   │       ├── analyzer/           # Zombie detection thresholds
│   │       ├── aws/                # CloudWatch + Cost Explorer clients
│   │       └── scheduler/          # Periodic scans
│   ├── shared/                     # Go shared library
│   │   ├── storage/postgres/       # PostgreSQL + RLS
│   │   ├── crypto/                 # AES-256-GCM encryption
│   │   ├── model/                  # Domain types
│   │   └── logging/                # slog helpers
│   └── dashboard/                  # Vite + React web app (nginx :80)
│       └── src/
│           ├── screens/
│           └── components/
├── docs/                           # Architecture, deployment, business plan
└── scripts/                        # Tooling (migrations, seed data)
```

---

## Roadmap

### Phase 1 — MVP ✅ Complete
- [x] Go ingestion service with PostgreSQL and RLS
- [x] Zombie detection with per-service threshold rules
- [x] REST API (`/api/v1/zombies`, `/api/v1/summary`, `/api/v1/resources`)
- [x] React web dashboard
- [x] Docker Compose setup
- [x] Comprehensive test coverage (44+ tests across 6 packages)
- [x] AWS Cost Explorer + CloudWatch integration

### Phase 2 — Alpha (in progress, target August 2026)
- [x] AWS Cost Explorer + CloudWatch + resource discovery integration
- [x] Native cookie auth + OIDC SSO + multi-tenancy (RLS)
- [x] Account management — connect AWS accounts, encrypted secrets, on-demand scan
- [x] Resource inventory view — all resources with zombie/active annotation
- [x] Savings history / trend (`zombie_snapshots` + `GET /v1/trend`)
- [x] Observability — structured logging (slog), Prometheus metrics
- [x] API versioning — `/v1/` prefix on all endpoints
- [x] In-memory rate limiting + graceful shutdown
- [x] CI pipeline — test + build stages
- [x] Scheduled auto-scan (24h default per account)
- [x] `cost_records` 90-day retention cleanup
- [x] Redis (now Valkey since 2026-05-27 migration) — JWKS cache, scan job queue, rate limiting
- [x] Dismiss zombie workflow + snooze + audit trail
- [x] Wire Redis in API `main.go` (inject into auth + rate limiter)
- [ ] Weekly email digest + Slack alerts
- [x] Production deployment (ECS Express + RDS + Valkey via Terraform)

### Phase 3 — Beta / Launch (target December 2026)
- [x] GDPR / right to erasure + data export
- [ ] Remediation CLI commands per resource type
- [ ] Scan history log + tag/team filtering + CSV export
- [x] Expanded detection rules (EBS, S3, CloudFront, Redshift, ElastiCache)
- [x] User management + roles (admin/viewer)

### Phase 4 — Scale (2027)
- [ ] Multi-cloud (Azure, GCP)
- [ ] Mobile app (iOS + Android)
- [ ] Cost forecasting (linear regression over snapshot history)

### Phase 5 — Pre-deployment Simulation (2027+)
- [ ] IaC plan parser (Terraform / CDK)
- [ ] CI/CD budget gate
- [ ] What-if cost scenarios

---

## Documentation

**Start here:**

| File | Description |
|------|-------------|
| **[docs/ARCHITECTURE.md](docs/ARCHITECTURE.md)** | **System architecture hub — diagrams of system, deployment, scan lifecycle, auth flows, data model. Read this first.** |
| **[docs/DEVELOPER_GUIDE.md](docs/DEVELOPER_GUIDE.md)** | **Developer onboarding — setup, conventions, common workflows, gotchas.** |

**Reference docs:**

| File | Description |
|------|-------------|
| [docs/AUTHENTICATION.md](docs/AUTHENTICATION.md) | Native auth, roles/permissions, password breach screening, the runtime DB role |
| [docs/OPERATIONS.md](docs/OPERATIONS.md) | Connecting an AWS account, Slack/Email notification channels |
| [docs/OBSERVABILITY.md](docs/OBSERVABILITY.md) | Prometheus metrics, structured logging, AWS-scan error handling/resilience |
| [docs/TEST_STRATEGY.md](docs/TEST_STRATEGY.md) | Unit/integration/e2e/AWS testing architecture |

---

## Status

**Phase 2 (Alpha) — April 2026.** Most of Phase 2 is complete: auth, account management, resource inventory, savings trend, observability, CI pipeline, scheduled auto-scan, Redis (cache/queue/rate limiting), dismiss/snooze workflow, email/Slack notification channels. Target first paying customer: October 2026.

---

## License

AxiaOps is licensed under the [Apache License, Version 2.0](LICENSE). See [`NOTICE`](NOTICE) for third-party attributions.

"AxiaOps" and the AxiaOps logo are trademarks of the AxiaOps project. The license grants no trademark rights (Apache-2.0 §6) — you're free to use, modify, and redistribute the code, including for a competing product, but forks and derivative works should use their own name and branding rather than "AxiaOps."

## Contributing

Contributions are welcome. By submitting a pull/merge request, you agree that your contribution is licensed under the same Apache-2.0 terms as the rest of the project.
