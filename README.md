<p align="center">
  <picture>
    <source media="(prefers-color-scheme: dark)" srcset="logos/axiaops-logo-dark.svg">
    <img alt="AxiaOps" src="logos/axiaops-logo.svg" width="360">
  </picture>
</p>

<h2 align="center">AxiaOps — FinOps</h2>

<p align="center">A self-hosted, open-source FinOps tool for tracking AWS cloud costs and catching the idle and zombie resources quietly driving them up — with trend history, per-account breakdowns, and an actionable remediation workflow with a full audit trail.</p>

<p align="center"><strong>Know the value of every resource.</strong></p>

<p align="center">
  <a href="https://ahmd-soliman.github.io/axiaops"><strong>Documentation</strong></a>
</p>

---

## Deploying with Helm

Install AxiaOps into your Kubernetes cluster via the official Helm chart:

```bash
helm repo add axiaops https://ahmd-soliman.github.io/axiaops/charts
helm repo update
helm install axiaops axiaops/axiaops --version 0.3.0
```

See [deploy/helm/axiaops](deploy/helm/axiaops/README.md) for full configuration parameters.

---

## Running Locally

AxiaOps connects to your cloud billing via read-only IAM access and delivers:

- **The Zombie Number** — total monthly spend on idle resources across all connected accounts
- **The Zombie List** — itemized breakdown by resource with cost, usage metric, and remediation suggestion
- **Owner Resolution** — every zombie includes the responsible team derived from resource tags
- **Notification channels** — Slack or email alert when a scan finds new zombies above a savings threshold
- **Multi-account Dashboard** — single pane for managing multiple cloud accounts

---

## Tech Stack

| Layer | Technology |
|-------|-----------|
| Backend | Go 1.25+ |
| Database | PostgreSQL 17 (with Row-Level Security) |
| Frontend | Vite + React (Node.js 24+) — web |
| Auth | Native cookie sessions (argon2id) + OIDC SSO |
| Hosting | AWS ECS Express + RDS |
| Cloud APIs | AWS Cost Explorer, CloudWatch |

---

## Supported AWS Services

AxiaOps monitors 20+ top AWS services for idle and zombie resources:

| Service Category | AWS Services Covered | Detection Mechanism |
|---|---|---|
| **AI & Generative AI** | **Amazon Bedrock** (Provisioned Throughput), **Amazon Kendra** (AI Search Indexes), **Amazon SageMaker** (ML Endpoints) | CloudWatch Invocations & Search Queries |
| **Compute & Containers** | **Amazon EC2** (Instances, AMIs), **Amazon ECS** (Services & Tasks), **Amazon EKS** (Clusters) | CPU & Memory Utilization, Instance Status |
| **Storage & Backups** | **Amazon S3** (Idle Buckets & Incomplete Multipart Uploads), **Amazon EBS** (Unattached Volumes, Orphaned Snapshots) | Metric Activity & Multipart Upload Age |
| **Databases & Cache** | **Amazon RDS** (Instances & Snapshots), **Amazon DocumentDB** (Clusters), **Amazon DynamoDB** (Tables), **Amazon ElastiCache** (Redis/Memcached), **Amazon Redshift** (Clusters) | Active DB Connections & Read/Write Capacity |
| **Networking & CDN** | **Amazon VPC** (Unattached Elastic IPs, Idle NAT Gateways), **Amazon Route53** (Unused Hosted Zones), **Amazon CloudFront** (CDN Distributions) | Active Traffic & DNS Record Counts |
| **Messaging & Analytics** | **Amazon MSK** (Managed Kafka), **Amazon Kinesis** (Data Streams), **Amazon CloudWatch** (Wasteful Log Groups) | Ingestion Rates & Log Retention Policies |
| **Security** | **AWS Secrets Manager** (Unaccessed Secrets) | Access Timestamps |

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

To exercise the full auth chain (native cookie sessions, no DEV_MODE bypass), `api`/`ingestion`
run as real containers here (not host-mode like `start-dev`), so two more `.env` files need to
exist first:
```bash
cp services/api/.env.example services/api/.env
cp .env.example .env
```
At minimum, set real values for `ENCRYPTION_KEY` (in `services/api/.env`) and
`INGESTION_SHARED_SECRET` (in the root `.env` — `docker-compose.yml` reads it from there, not
from either service's `.env`):
```
ENCRYPTION_KEY=<run: openssl rand -hex 32>
INGESTION_SHARED_SECRET=<run: openssl rand -hex 32>
```
Then:
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
         "eks:ListClusters",
         "ecs:ListClusters",
         "ecs:ListServices",
         "docdb:DescribeDBClusters",
         "kafka:ListClustersV2",
         "route53:ListHostedZones",
         "bedrock:ListProvisionedModelThroughputs",
         "kendra:ListIndices"
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

### Known limitations

- AWS only — no Azure/GCP support yet

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

26 detection rules across 18 AWS services, split into two tiers — **Tier 1** joins Cost Explorer billing data with a CloudWatch metric (cost + usage at/below threshold ⇒ flagged); **Tier 2** is API-only, where Describe-API state alone determines waste (e.g. an unattached EBS volume is always waste, no metric needed). Full rule tables, thresholds, and the required IAM permissions live in [`docs/ARCHITECTURE.md` § 7](docs/ARCHITECTURE.md#7-detection-engine).

Rules do not change without business justification — see `CLAUDE.md` for FinOps domain thresholds.

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

**Docs site:** the [`website/`](website/) directory is the public docs site source (Astro + Starlight) — run `cd website && npm install && npm run dev` for a local preview. See [website/README.md](website/README.md).

---

## Status

**Alpha.** Core functionality is working end to end: auth, account management, resource inventory, savings trend, observability, CI pipeline, scheduled auto-scan, Redis (cache/queue/rate limiting), dismiss/snooze workflow, email/Slack notification channels.

---

## License

AxiaOps is licensed under the [Apache License, Version 2.0](LICENSE). See [`NOTICE`](NOTICE) for third-party attributions.

"AxiaOps" and the AxiaOps logo are trademarks of the AxiaOps project. The license grants no trademark rights (Apache-2.0 §6) — you're free to use, modify, and redistribute the code, including for a competing product, but forks and derivative works should use their own name and branding rather than "AxiaOps."

## Contributing

Contributions are welcome. By submitting a pull/merge request, you agree that your contribution is licensed under the same Apache-2.0 terms as the rest of the project.
