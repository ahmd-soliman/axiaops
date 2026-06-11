# CLAUDE.md — AxiaOps

## What is AxiaOps?

FinOps SaaS that detects idle/zombie cloud resources still incurring costs despite zero usage.
"Know the value of every resource." MVP targets AWS; multi-cloud (Azure, GCP) is Phase 4.

## Current Status

Phase 1 (MVP) complete. Phase 2 complete — AWS integration, observability, scheduled
scans, Redis/Valkey, dismiss/snooze, audit trail, GDPR deletion, data export, production
deployment (ECS Express + RDS), and outbound notification channels (email + Slack scan
digests — see `docs/notifications-plan.md`) all shipped.

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
- `start-staging` = full docker-compose stack: Postgres + Redis + ingestion + API + dashboard. Native auth enforced (cookie + sessions table). Dashboard served by nginx on plain HTTP at **`http://localhost:8082`** — TLS termination is the edge proxy's job in every real deployment (CloudFront in front of the prod ECS Express ALB / customer ingress / on-prem reverse proxy in front of dev/staging) and is intentionally absent locally. License posture: the Makefile target injects the embedded dev fixture as `AXIAOPS_LICENSE` so scans run locally — `customer_id="axiaops-dev-fixture"` distinguishes this from deployed staging (which gets a CI-minted production-key-signed JWT, `customer_id="axiaops-internal-staging"`). Throwaway plumbing per issue #76. Use when debugging auth flows, Redis features, or verifying container parity.
- Both modes use real AWS Cost Explorer + CloudWatch data.
- `start-dev` requires AWS credentials in `services/*/.env` or environment.
- `start-staging` needs no extra env beyond what `make start-dev` requires; no local TLS setup is needed.
- In `start-dev` the dashboard proxies `/api/*` through Vite → API on 8080. In `start-staging` nginx serves the built bundle on HTTP and proxies `/api/*` to the containerised API, propagating `X-Forwarded-Proto` from the request — non-Secure cookie under direct-HTTP access (correct), Secure cookie when an edge proxy terminates TLS in front of this stack.
- **Native-auth first-run** (bootstrap → login → dashboard) is documented in [`docs/native-auth-bootstrap.md`](docs/native-auth-bootstrap.md).

## Deployment topology

Non-obvious shape that's easy to misread from `.gitlab-ci.yml`:

- **Infrastructure-as-code lives in two sibling repos**, not in this one:
  - [`(internal infra repo)`](https://gitlab.com/(internal infra repo)) — the self-hosted stack (`stacks/axiaops-dev`) for dev-1 / dev-2 / staging / preview / demo hosts. Each host is provisioned with the `deploy` user and a public key from `TF_VAR_deploy_ssh_public_keys`.
  - [`axiaops/aws-infra`](https://gitlab.com/axiaops/aws-infra) — the AWS production stack (VPC, RDS, ECS Express, ECR, S3+CloudFront, IAM OIDC role for CI). Design: [`aws-infra/docs/terraform-prod-design.md`](https://gitlab.com/axiaops/aws-infra/-/blob/main/docs/terraform-prod-design.md).

- **Each deployed env runs on its own self-hosted host**, NOT a single shared Docker host.

  | Env | Hostname | IP |
  |---|---|---|
  | dev-1 | `axiaops-<env>.local` | `192.168.1.121` |
  | dev-2 | `axiaops-<env>.local` | `192.168.1.123` |
  | staging | `axiaops-<env>.local` | `192.168.1.122` |
  | production | ECS Express / ECR (separate concern) | — |

- **an edge proxy (an edge proxy)** is the edge proxy in front of every env. Browser → `https://axiaops-<env>.local` → an edge proxy (TLS termination + routing) → host's port 80/8080. The dashboard's `services/dashboard/nginx.conf` listens on plain HTTP and propagates `X-Forwarded-Proto` from an edge proxy, so the API's session cookie correctly toggles `Secure` based on what an edge proxy saw.

- **CI deploys via SSH-as-Docker-context**: `.gitlab-ci.yml` line 357 sets `DOCKER_HOST: ssh://deploy@${DEPLOY_HOST_IP}`. The runner is a distinct host; its `docker login` / `docker pull` / `docker compose up` all execute on the per-env host's daemon over a tunnelled SSH transport. `DEPLOY_SSH_PRIVATE_KEY` MUST be a **File-type** CI variable — Variable-type masking strips PEM newlines and silently corrupts the key.

- **Hostname vs IP**: humans use the `.local` mDNS hostnames everywhere (browser, SSH, scripts). The CI deploy template uses raw IPs because the Alpine-based `docker:24` image's musl libc has no mDNS resolution. Both work; just don't expect the CI container to resolve `axiaops-<env>.local`.

- **`PUBLIC_HOST` per env**: should be the externally-reachable an edge proxy hostname (`https://axiaops-<env>.local`, etc.), not the host IP+port. an edge proxy-terminated TLS makes the API's `X-Forwarded-Proto`-derived cookie `Secure` posture work correctly. Empty → API logs `"sso: ceremony: PUBLIC_HOST is empty"` at startup and SSO ceremonies fail at the IdP redirect. Set as a GitLab CI variable per environment scope (`deploy:preview/staging/production` declare `environment.name`, which keys the lookup); `deploy:dev-1/2` are unscoped by design — set there only if you turn off DEV_MODE for SSO testing.

- **`INTERNAL_DNS` per env (self-hosted IdP only)**: LAN resolver IP injected into the API container via `dns:` in `deploy/{preview,staging,demo}.yml`. Needed when the IdP hostname has split-horizon DNS — public IP for the world, internal LAN IP for on-premises traffic. Without it, the container resolves the IdP via public DNS, hits whatever WAF fronts Keycloak (Cloudflare Bot Fight Mode rejects the Go HTTP client's default UA on `/.well-known/openid-configuration`), and OIDC discovery fails. With it set to e.g. `192.168.1.1` (the router running AdGuard with a `*.example.com` rewrite), traffic stays on the LAN and the discovery fetch succeeds. Scope `*` if all envs share the same router; scope per env otherwise. Not relevant for ECS Express / cloud envs (no LAN, no split-horizon).

- **Adding a new env (preview, demo, etc.)** is NOT just a port-pair change. It requires: (a) provisioning a new self-hosted host via the `self-hosted-infra/stacks/axiaops-dev` Terraform stack with the `deploy` user + authorized key, (b) registering the new hostname in an edge proxy with a TLS cert, (c) adding a `deploy:<env>` CI job (and `gate:devmode:<env>` per plan §4.10 layer 1) that points at the new `DEPLOY_HOST_IP`. None of (a) or (b) lives in this repo.

## Database

- **Runtime:** PostgreSQL 17 with Row-Level Security (organization isolation via `SET app.organization_id`)
- **Migrations:** `services/shared/storage/postgres/migrations/` — versioned SQL, run on startup
- **Three DB roles** (see `docs/runtime-admin-db-role.md`):
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

- **Customer AWS access:** cross-account `sts:AssumeRole` with a per-account `ExternalId` (confused-deputy mitigation). No long-lived customer credentials cross the trust boundary. Onboarding is role ARN + ExternalId, optionally via a one-click CloudFormation Quick-Create URL. Design: `docs/cross-account-roles-design.md`. Customer-facing flow: `docs/connect-aws-account.md`. Legacy access-key onboarding still works for pre-role customers (Phase 2 coexistence — see §7 of the design doc); new sales should be role-based.
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
- Graviton/ARM64 was evaluated and **declined** (June 2026) — ECS Express is x86-only and the ~€46/yr saving doesn't justify the migration. See `docs/graviton-arm-decision.md` for the math + revisit trigger

## Source Control

- **Host:** GitLab (`git@gitlab.com:axiaops/axiaops.git`). There is no GitHub remote.
- **CLI:** use `glab`, not `gh`. `gh pr ...` will fail — the equivalent is `glab mr ...`.
- **Terminology:** "MR" / "merge request", not "PR" / "pull request". The default branch is `main`; MRs historically also targeted `develop` in older history.
- Any instruction in a global skill that says "use `gh`" or "open a PR" applies via the GitLab equivalent in this repo.

## Agent Delegation

- **Committing** — always delegate to the `commit` agent
- **Code review before committing** — delegate to the `code-reviewer` agent
- **Planning non-trivial features** that affect multiple services or the data model — delegate to the `architect` agent

## Service-Specific Instructions

@services/api/CLAUDE.md
@services/ingestion/CLAUDE.md
@services/shared/CLAUDE.md
@services/dashboard/CLAUDE.md

## Design & Decision Docs

- **CloudTrail Integration:** See `docs/cloudtrail-analysis.md` — Why CloudTrail detection was deferred to Phase 4+, ROI analysis, when to reconsider
- **AWS Service Coverage:** See `tmp/aws-coverage-and-cost-explorer-notes.md` — Why certain services are prioritized, detection patterns
- **Tier 2 Detections:** See `docs/tier2_detections_status.md` — ElastiCache, OpenSearch, Redshift, SageMaker, DynamoDB, EKS detection status
- **Graviton/ARM64 (declined):** See `docs/graviton-arm-decision.md` — why prod stays on ECS Express + x86, the ~€46/yr savings math, and the revisit trigger
- **SaaS dev-1 deploy target:** See `docs/saas-dev1-deploy-target.md` — dev-1 as the first SaaS (license-removed) deploy target, built with `-tags saashosted` alone so DEV_MODE/no-auth is preserved; the `build:images-saashosted` job + `deploy:dev-1` wiring + entitlement seeding
