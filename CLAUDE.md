# CLAUDE.md — AxiaOps

## What is AxiaOps?

Self-hosted, open-source FinOps tool that detects idle/zombie cloud resources still incurring costs despite zero usage.
"Know the value of every resource." MVP targets AWS; multi-cloud (Azure, GCP) is Phase 4.

## Current Status

Phase 1 (MVP) complete. Phase 2 complete — AWS integration, observability, scheduled
scans, Redis/Valkey, dismiss/snooze, audit trail, GDPR deletion, data export, production
deployment (ECS Express + RDS), and outbound notification channels (email + Slack scan
digests) all shipped.

## Architecture

```
services/
  api/        — HTTP server (:8080), auth middleware, reads from DB
  ingestion/  — Long-lived HTTP server (:8081), fetches AWS data, writes to DB
  shared/     — Domain models, Store interface, PostgreSQL, analyzer, crypto, logging
  dashboard/  — Vite + React web app, served via nginx
```

Go workspace (`go.work`) links all three Go modules. Import paths: `axiaops.io/api`, `axiaops.io/ingestion`, `axiaops.io/shared`.

## Key Commands

```bash
make start-dev      # Host-mode Go services + Postgres container. DEV_MODE=true
                    # (auth bypass). Fast dev loop — the default for day-to-day work.
make start-staging  # Full Docker stack (API, ingestion, dashboard, Redis, Postgres)
                    # with DEV_MODE=false → native cookie auth on. Mirrors deployed env.
make stop           # Kill host-mode services AND `docker compose down` the stack.
make seed           # Populate dummy organization/user/zombie records
make test               # All Go unit tests
make test-storage       # PostgreSQL tests (RLS, migrations) — needs running postgres
make test-all           # Unit + postgres tests
make test-integration   # Spins up an isolated docker-compose stack (postgres, redis,
                        # api, ingestion) and runs end-to-end tests against it.
                        # `test-integration-api` / `test-integration-ingestion` run
                        # only one service's suite.
```

## Dev Workflow

- `start-dev` = host-mode Go (API :8080, ingestion :8081, Vite dashboard :5173) against a local Postgres container. No Redis, no auth. Use this for most coding.
- `start-staging` = full docker-compose stack: Postgres + Redis + ingestion + API + dashboard. Native auth enforced (cookie + sessions table). Dashboard served by nginx on plain HTTP at **`http://localhost:8082`** — TLS termination is the edge proxy's job in every real deployment (CloudFront in front of the prod ECS Express ALB / customer ingress / on-prem reverse proxy in front of dev/staging) and is intentionally absent locally. Use when debugging auth flows, Redis features, or verifying container parity.
- Both modes use real AWS Cost Explorer + CloudWatch data.
- `start-dev` requires AWS credentials in `services/*/.env` or environment.
- `start-staging` needs no extra env beyond what `make start-dev` requires; no local TLS setup is needed.
- In `start-dev` the dashboard proxies `/api/*` through Vite → API on 8080. In `start-staging` nginx serves the built bundle on HTTP and proxies `/api/*` to the containerised API, propagating `X-Forwarded-Proto` from the request — non-Secure cookie under direct-HTTP access (correct), Secure cookie when an edge proxy terminates TLS in front of this stack.
- **Native-auth first-run** (bootstrap → login → dashboard) is documented in [`docs/AUTHENTICATION.md`](docs/AUTHENTICATION.md) § 3.

## Deployment

Two supported paths for running this yourself: the Helm chart
(`deploy/helm/axiaops/`) on any Kubernetes cluster, or ECS Express Mode on
AWS (`terraform/`). Both are covered in the docs site's deployment guides.
Two env vars are worth knowing about regardless of platform:

- **`PUBLIC_HOST`** — the externally-reachable hostname your edge/ingress
  terminates TLS for (e.g. `https://app.example.com`). Drives the
  `X-Forwarded-Proto`-derived cookie `Secure` posture and SSO redirect URIs.
  Empty → API logs `"sso: ceremony: PUBLIC_HOST is empty"` at startup and SSO
  ceremonies fail at the IdP redirect.
- **`INTERNAL_DNS`** (self-hosted IdP only) — a LAN resolver IP for the API
  container, needed only if your IdP has split-horizon DNS (public IP for the
  world, internal IP for on-premises traffic). Not relevant for a
  cloud-hosted IdP or a cloud deployment target.

## Database

- **Runtime:** PostgreSQL 17 with Row-Level Security (organization isolation via `SET app.organization_id`)
- **Migrations:** `services/shared/storage/postgres/migrations/` — versioned SQL, run on startup
- **Three DB roles** (see `docs/AUTHENTICATION.md` § 5):
  - `DATABASE_URL` → `axiaops` app user, RLS-enforced — the request-path pool.
  - `RUNTIME_ADMIN_DATABASE_URL` → `axiaops_runtime`, a least-privilege RLS-bypass role (DML + per-table bypass policies, **no DDL / no ownership**) used for pre-auth / cross-org reads (native login, scheduled-scan enumeration, GDPR purge). Required outside DEV_MODE.
  - `MIGRATION_DATABASE_URL` → `axiaops_owner` schema owner — **migrate task only**; no longer read by the api/ingestion runtime.

## Testing Conventions

- Go standard `testing` package — no testify or third-party assertion libs
- Black-box tests: `package foo_test` (not `package foo`)
- Mock interfaces for external services (AWS SDK, HTTP clients)
- Helper functions for fixture building: `costRecord()`, `usageRecord()`
- `httptest.NewRecorder` for handler tests
- No real network calls in unit tests
- Always run `make test` before committing

## Code Conventions

- **Go 1.25+** — use modern stdlib features (`log/slog`, `http.ServeMux` with `r.PathValue()`)
- **Error wrapping:** `fmt.Errorf("context: operation: %w", err)` — always wrap with `%w`
- **Logging:** `slog.Info/Error/Warn("action", "key", value)` — never `log.Printf`
- **Naming:** Explicit, no abbreviations beyond `ctx`, `err`, `tx`, `mux`
- **Handler pattern:** Constructor `New(store)` → `Register(mux)` → route handlers as methods
- **JSON responses:** Use `writeJSON()` helper, never raw `json.NewEncoder`
- **Context propagation:** `storage.WithOrganizationID(ctx, organizationID)` for all DB calls
- **Transactions:** `defer tx.Rollback(ctx)` immediately after `Begin()`
- **Constants:** Named duration constants (`const stuckScanTimeout = 15 * time.Minute`)

## FinOps Domain Rules

Zombie detection thresholds — do not change without business justification:

| Service | Metric | Threshold | Verdict |
|---------|--------|-----------|---------|
| AmazonEC2 | CPUUtilization | ≤ 5% | Idle instance |
| AmazonRDS | DatabaseConnections | = 0 | Abandoned DB |
| AWSLambda | Invocations | = 0 | Unused function |
| ELB | RequestCount | = 0 | Abandoned LB |
| VPC (NAT) | BytesOutToDestination | = 0 | Unused NAT GW |
| VPC (EIP) | NetworkInterfaceAttachment | = 0 | Unattached EIP |
| CloudFront | Requests | = 0 | Abandoned distribution |
| Kinesis | IncomingRecords | = 0 | Unused data stream |
| S3 | AllRequests | = 0 | Abandoned bucket (requires request metrics) |

API-only rules (no CloudWatch — state derived directly from AWS Describe APIs):

| Service | Detection Method | Threshold | Verdict | Cost |
|---------|-----------------|-----------|---------|------|
| AmazonEC2 (EBS vol) | ec2:DescribeVolumes | state = "available" | Unattached volume | $0.08–0.125/GB-month |
| AmazonEC2 (snapshot) | ec2:DescribeSnapshots + DescribeVolumes | source volume gone, not backing any AMI | Orphaned snapshot | $0.05/GB-month |
| AmazonEC2 (stopped) | ec2:DescribeInstances StateTransitionReason | stopped > 30 days | Long-stopped instance (EBS still bills) | $0.08/GB-month on attached volumes |
| AmazonEC2 (AMI) | ec2:DescribeImages + DescribeInstances | age > 90 days, no instance references it | Unused AMI + backing snapshots | $0.05/GB-month on backing snapshots |
| AmazonCloudWatch (Log Group) | logs:DescribeLogGroups | retentionInDays = null (logs stored forever) | Wasteful log group | $0.03/GB-month |
| AmazonRDS (snapshot) | rds:DescribeDBSnapshots + DescribeDBInstances | manual, age > 30 days, source DB gone | Orphaned RDS snapshot | $0.095/GB-month |
| AmazonECR (images) | ecr:DescribeRepositories + DescribeImages | untagged or age > 90 days (not latest) | Stale container images | $0.10/GB-month |
| AWSSecretsManager | secretsmanager:ListSecrets | LastAccessedDate > 90 days | Unused secret | $0.40/secret-month |

## Security

- **Customer AWS access:** cross-account `sts:AssumeRole` with a per-account `ExternalId` (confused-deputy mitigation). No long-lived customer credentials cross the trust boundary. Onboarding is role ARN + ExternalId, optionally via a one-click CloudFormation Quick-Create URL. Customer-facing flow: `docs/OPERATIONS.md` § 1. Legacy access-key onboarding still works for pre-role customers; new sales should be role-based.
- AES-256-GCM (`ENCRYPTION_KEY` env var, 32-byte hex) encrypts at-rest secrets — currently: legacy AWS access-key secrets (`accounts.secret_encrypted`) and SSO ID tokens (`sessions.id_token_encrypted`). Role-based accounts store `role_arn` + `external_id` unencrypted (they are not secrets).
- Native cookie sessions (argon2id password hashes) + OIDC SSO (per-connection JWKS, RS256 ID-token validation)
- RLS enforces organization isolation at the DB level — never query without `app.organization_id` set
- Never commit `.env` files, credentials, or encryption keys
- **AxiaOps' own AWS runtime:** IAM task roles (ECS), not access keys. (This is about AxiaOps' deployment, not how customer accounts connect — see the first bullet for that.)

## Cost Awareness (FinOps for AxiaOps itself)

- Phase 2 target: €24–34/mo (ECS Express + RDS db.t4g.micro)
- Avoid NAT Gateways (~€33/mo fixed) — use public subnets with security groups
- CloudWatch log retention: 7 days max
- Clean up old ECR images (€0.10/GB)
- RDS Multi-AZ doubles cost — defer until necessary
- ECS Express runs always-on Fargate tasks (no scale-to-zero) — keep task CPU/memory minimal to cap idle compute cost
- Graviton/ARM64 was evaluated and **declined** (June 2026) — ECS Express is x86-only and the ~€46/yr saving doesn't justify the migration

## Source Control

- **Host:** GitHub. Default branch is `main`.
- **CLI:** use `gh` (`gh pr create`, `gh pr view`, etc.).
- **Terminology:** "PR" / "pull request".

## Service-Specific Instructions

@services/api/CLAUDE.md
@services/ingestion/CLAUDE.md
@services/shared/CLAUDE.md
@services/dashboard/CLAUDE.md

## Design & Decision Docs

- **AWS Service Coverage:** See `docs/ARCHITECTURE.md` § 7 — the living list of detection rules per service
