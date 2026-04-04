# Development Plan — AxiaOps FinOps

## Project Overview

**AxiaOps** — A FinOps SaaS tool that identifies idle/zombie cloud resources still incurring costs despite $0.00 usage. Target: AWS (initial), multi-cloud later.

---

## Phase 1 — Incubator / MVP (April – June 2026)

### Goal: Working AxiaOps Detector (local, no auth, fake data)

#### 1.1 Mock Data Generator
- Python script that produces a realistic "Fake Large Enterprise" billing CSV + CloudWatch-style JSON metrics
- Fields: `resource_id`, `resource_type`, `region`, `daily_cost`, `last_usage_date`, `tags`
- Tags must include `Owner` and `Project` on every row — ownership resolution depends on this
- Volume: ~10,000 rows to simulate real enterprise scale
- Seed with realistic patterns (EBS, ELB, RDS snapshots, NAT Gateways)
- Include a `--seed` flag for deterministic test runs
- Include scheduled-batch resource patterns (resources active once/month) to test threshold logic

#### 1.2 Backend — Go Worker
- **Language:** Go 1.22+
- **Framework:** Standard library + `net/http` for API
- **Data Layer:** SQLite (MVP) → PostgreSQL (production)
- **Core Logic:**
  - Use **streaming CSV parsing** — never load entire file into memory (AWS CUR files can be multiple GBs)
  - Join billing data with usage metrics (CPU, IOPS, network) — cost alone is not sufficient to flag a ghost
  - Flag resources with `cost > 0` AND activity below configurable threshold for N days
  - **Zombie threshold is configurable per tenant** (default 7 days; enterprise batch jobs may need 30 days)
  - **Ownership resolution:** every flagged ghost must include an `Owner` derived from resource tags — without this, the remediation step stalls because no one knows if it's safe to delete
  - Categorize by resource type (Idle Load Balancers, unattached EBS, aged snapshots, unused Elastic IPs)
  - Compute `potential_monthly_savings` per resource and in aggregate
- **API Endpoints:**
  - `POST /ingest` — upload billing CSV
  - `GET /ghosts` — returns list of zombie resources with cost breakdown
  - `GET /summary` — returns aggregate savings figure

#### 1.3 Frontend — React Native
- Single "Dashboard" screen
- Big red number: **"Potential Monthly Savings: $X,XXX"**
- Scrollable list of ghost resources grouped by type
- Tap a resource → detail view with remediation suggestion (e.g., "Delete this EBS volume")
- Stack: Expo + React Native + React Query

#### 1.4 Infrastructure (local)
- Docker Compose: Go API + PostgreSQL
- `.env`-based config (never committed)

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

## Clean Room Protocol (Non-Negotiable)

- All development on personal hardware only
- Maintain a work log: date, hours, device used
- Use only mock/generated data — never real employer billing data
- Separate Git identity from work email
- No [redacted] infrastructure, credentials, or architecture referenced

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
