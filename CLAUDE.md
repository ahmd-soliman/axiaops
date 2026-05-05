# CLAUDE.md — AxiaOps

## What is AxiaOps?

FinOps SaaS that detects idle/zombie cloud resources still incurring costs despite zero usage.
"Know the value of every resource." MVP targets AWS; multi-cloud (Azure, GCP) is Phase 4.

## Current Status

Phase 1 (MVP) complete. Phase 2 in progress — real AWS integration shipped, now working on
observability, scheduled scans, and production deployment (App Runner + RDS).

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
                    # with DEV_MODE=false → Kinde JWT auth on. Mirrors deployed env.
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
- `start-staging` = full docker-compose stack: Postgres + Redis + ingestion + API + dashboard. Native auth enforced (`AUTH_PROVIDER=native`). Dashboard served by nginx on plain HTTP at **`http://localhost:8082`** — TLS termination is the edge proxy's job in every real deployment (App Runner / customer ingress / on-prem reverse proxy in front of dev/staging) and is intentionally absent locally. Use when debugging auth flows, Redis features, or verifying container parity.
- Both modes use real AWS Cost Explorer + CloudWatch data.
- `start-dev` requires AWS credentials in `services/*/.env` or environment.
- `start-staging` additionally requires (under `AUTH_PROVIDER=kinde`/`both` only): `KINDE_*` env vars populated. No local TLS setup is needed.
- In `start-dev` the dashboard proxies `/api/*` through Vite → API on 8080. In `start-staging` nginx serves the built bundle on HTTP and proxies `/api/*` to the containerised API, propagating `X-Forwarded-Proto` from the request — non-Secure cookie under direct-HTTP access (correct), Secure cookie when an edge proxy terminates TLS in front of this stack.
- **Native-auth first-run** (bootstrap → login → dashboard) is documented in [`docs/native-auth-bootstrap.md`](docs/native-auth-bootstrap.md).

## Deployment topology

Non-obvious shape that's easy to misread from `.gitlab-ci.yml`:

- **Each deployed env runs on its own self-hosted host**, NOT a single shared Docker host. The self-hosted stack lives in `self-hosted-infra/stacks/axiaops-dev` (separate repo / Terraform); each host is provisioned with the `deploy` user and a public key from `TF_VAR_deploy_ssh_public_keys`.

  | Env | Hostname | IP |
  |---|---|---|
  | dev-1 | `axiaops-<env>.local` | `192.168.1.121` |
  | dev-2 | `axiaops-<env>.local` | `192.168.1.123` |
  | staging | `axiaops-<env>.local` | `192.168.1.122` |
  | production | App Runner / ECR (separate concern) | — |

- **an edge proxy (an edge proxy)** is the edge proxy in front of every env. Browser → `https://axiaops-<env>.local` → an edge proxy (TLS termination + routing) → host's port 80/8080. The dashboard's `services/dashboard/nginx.conf` listens on plain HTTP and propagates `X-Forwarded-Proto` from an edge proxy, so the API's session cookie correctly toggles `Secure` based on what an edge proxy saw.

- **CI deploys via SSH-as-Docker-context**: `.gitlab-ci.yml` line 357 sets `DOCKER_HOST: ssh://deploy@${DEPLOY_HOST_IP}`. The runner is a distinct host; its `docker login` / `docker pull` / `docker compose up` all execute on the per-env host's daemon over a tunnelled SSH transport. `DEPLOY_SSH_PRIVATE_KEY` MUST be a **File-type** CI variable — Variable-type masking strips PEM newlines and silently corrupts the key.

- **Hostname vs IP**: humans use the `.local` mDNS hostnames everywhere (browser, SSH, scripts). The CI deploy template uses raw IPs because the Alpine-based `docker:24` image's musl libc has no mDNS resolution. Both work; just don't expect the CI container to resolve `axiaops-<env>.local`.

- **`PUBLIC_HOST` per env**: should be the externally-reachable an edge proxy hostname (`https://axiaops-<env>.local`, etc.), not the host IP+port. an edge proxy-terminated TLS makes the API's `X-Forwarded-Proto`-derived cookie `Secure` posture work correctly. Empty → API logs `"sso: ceremony: PUBLIC_HOST is empty"` at startup and SSO ceremonies fail at the IdP redirect. Set as a GitLab CI variable per environment scope (`deploy:preview/staging/production` declare `environment.name`, which keys the lookup); `deploy:dev-1/2` are unscoped by design — set there only if you turn off DEV_MODE for SSO testing.

- **`INTERNAL_DNS` per env (self-hosted IdP only)**: LAN resolver IP injected into the API container via `dns:` in `deploy/{preview,staging,demo}.yml`. Needed when the IdP hostname has split-horizon DNS — public IP for the world, internal LAN IP for on-premises traffic. Without it, the container resolves the IdP via public DNS, hits whatever WAF fronts Keycloak (Cloudflare Bot Fight Mode rejects the Go HTTP client's default UA on `/.well-known/openid-configuration`), and OIDC discovery fails. With it set to e.g. `192.168.1.1` (the router running AdGuard with a `*.example.com` rewrite), traffic stays on the LAN and the discovery fetch succeeds. Scope `*` if all envs share the same router; scope per env otherwise. Not relevant for App Runner / cloud envs (no LAN, no split-horizon).

- **Adding a new env (preview, demo, etc.)** is NOT just a port-pair change. It requires: (a) provisioning a new self-hosted host via the `self-hosted-infra/stacks/axiaops-dev` Terraform stack with the `deploy` user + authorized key, (b) registering the new hostname in an edge proxy with a TLS cert, (c) adding a `deploy:<env>` CI job (and `gate:devmode:<env>` per plan §4.10 layer 1) that points at the new `DEPLOY_HOST_IP`. None of (a) or (b) lives in this repo.

## Database

- **Runtime:** PostgreSQL 16 with Row-Level Security (organization isolation via `SET app.organization_id`)
- **Migrations:** `services/shared/storage/postgres/migrations/` — versioned SQL, run on startup
- Two connection strings: `DATABASE_URL` (app user) and `MIGRATION_DATABASE_URL` (owner/admin)

## Testing Conventions

- Go standard `testing` package — no testify or third-party assertion libs
- Black-box tests: `package foo_test` (not `package foo`)
- Mock interfaces for external services (AWS SDK, HTTP clients)
- Helper functions for fixture building: `costRecord()`, `usageRecord()`
- `httptest.NewRecorder` for handler tests
- RSA key generation for JWT middleware tests (no real Kinde calls)
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

- AES-256-GCM encrypts AWS secrets before DB storage (`ENCRYPTION_KEY` env var, 32-byte hex)
- Kinde OAuth 2.0 PKCE flow — RS256 JWT verified via JWKS endpoint
- RLS enforces organization isolation at the DB level — never query without `app.organization_id` set
- Never commit `.env` files, credentials, or encryption keys
- Production: IAM roles instead of access keys

## Cost Awareness (FinOps for AxiaOps itself)

- Phase 2 target: €24–34/mo (App Runner + RDS db.t4g.micro)
- Avoid NAT Gateways (~€33/mo fixed) — use public subnets with security groups
- CloudWatch log retention: 7 days max
- Clean up old ECR images (€0.10/GB)
- RDS Multi-AZ doubles cost — defer until necessary
- App Runner scales to zero — no idle compute cost

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
