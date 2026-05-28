# GitLab CI Pipeline — AxiaOps

Complete GitLab CI/CD pipeline configuration for AxiaOps, implementing the Phase 2 deployment strategy.

> ⚠️ **STALE — pre-SSO Phase B (April 2026).** This doc was written before native
> SSO, licensing, per-env self-hosted deploys, and the strangler auth pattern. Source
> of truth has moved:
>
> - **CI variables**: inline comments in `.gitlab-ci.yml` (especially the
>   `.deploy-dev` template's required-variables block) + `CLAUDE.md`
>   "Deployment topology" section
> - **License signing key + ceremony**: `docs/license-issuance.md`
> - **Per-service env vars**: `services/{api,ingestion,shared,dashboard}/CLAUDE.md`
>
> Kept here for historical reference; do not follow as setup steps.

---

## Overview

The GitLab CI pipeline automates:
1. **Test stage** — Go unit tests, linting, and vet checks across all modules
2. **Build stage** — Docker image builds for API, ingestion, and dashboard services
3. **Deploy stage** — Deployment to AWS ECS Express Mode and CloudFront invalidation

**Branch strategy:**
- All branches: Run test stage
- `main` branch only: Run build and deploy stages

---

## Architecture

```
GitLab Repository
  └── Commit pushed to main
        │
        ├─── TEST STAGE (all branches, automatic)
        │     ├── test:shared      — shared module unit tests (analyzer, crypto, models)
        │     ├── test:postgres    — storage PostgreSQL integration tests
        │     ├── test:api         — API service unit tests
        │     ├── test:ingestion   — Ingestion service unit tests
        │     ├── test:lint        — golangci-lint across all modules
        │     └── test:vet         — go vet across all modules
        │
        ├─── BUILD STAGE (main only, automatic after test)
        │     ├── build:api        — Docker build → ECR push
        │     ├── build:ingestion  — Docker build → ECR push
        │     └── build:dashboard  — Docker build → ECR push
        │
        └─── DEPLOY STAGE (tag-gated, manual approval required ⚠️)
              └── deploy:production — Migrate DB (ECS Fargate one-off), update ECS Express services,
                                      sync dashboard to S3 + invalidate CloudFront (click "Play" to trigger)
```

---

## Pipeline Configuration

### Variables

| Variable | Value | Purpose |
|----------|-------|---------|
| `AWS_DEFAULT_REGION` | `eu-central-1` | AWS region for ECR and ECS |
| `AWS_ACCOUNT_ID` | `123456789012` | AWS account ID (AxiaOps) |
| `ECR_REGISTRY` | Derived from account ID | ECR registry URL |
| `IMAGE_*` | Registry + repository name | Docker image URLs |

### Secrets (GitLab CI/CD Variables — Masked)

The `deploy:production` job authenticates via **GitLab OIDC** (`id_tokens` GITLAB_AWS_TOKEN, `sts:AssumeRoleWithWebIdentity`) — **no static AWS access keys are used**. The deploy role `gitlab-ci-axiaops-deploy` is provisioned in the `axiaops/aws-infra` Terraform repo. See `.gitlab-ci.yml` for the full variable list required per job.

| Variable | Value | Scope |
|----------|-------|-------|
| `AWS_CI_ROLE_ARN` | OIDC deploy role ARN | Protected, masked |
| `ENCRYPTION_KEY` | 32-byte hex AES key | Protected, masked |
| `INGESTION_SHARED_SECRET` | 32-byte hex HMAC secret | Protected, masked |
| `CLOUDFRONT_DISTRIBUTION_ID` | CloudFront distribution ID | Protected, masked |

---

## Setup Instructions

> **Note:** The CI deploy role, ECS service definitions, ECR repos, and all AWS infrastructure are provisioned via Terraform in the `axiaops/aws-infra` repo — not by hand. The steps below are for reference only; follow `aws-infra` Terraform to provision a new environment.

### Step 1: AWS IAM / OIDC trust

The `deploy:production` job uses GitLab OIDC — no IAM user or static keys. The deploy role `gitlab-ci-axiaops-deploy` and its permission boundary are defined in `axiaops/aws-infra`. See [`aws-infra/docs/terraform-prod-design.md`](https://gitlab.com/axiaops/aws-infra/-/blob/main/docs/terraform-prod-design.md) for the exact policy shape.

### Step 2: Add GitLab CI/CD Variables

In **GitLab Settings → CI/CD → Variables**:

**Required for build/deploy stage:**
1. `AWS_CI_ROLE_ARN` (masked, protected) — OIDC deploy role ARN (output of `aws-infra` Terraform)
2. `ENCRYPTION_KEY` (masked, protected) — 32-byte hex AES key
3. `INGESTION_SHARED_SECRET` (masked, protected) — 32-byte hex HMAC key
4. `CLOUDFRONT_DISTRIBUTION_ID` (masked, protected) — CloudFront distribution ID (output of `aws-infra` Terraform)

All variables should be:
- **Masked** ✓ (hidden in logs)
- **Protected** ✓ (only on `main` branch / protected tags)

### Step 3: Ensure ECS Express services and ECR repos exist

ECS Express gateway services (`axiaops-api`, `axiaops-ingestion`) and ECR repositories are provisioned by `aws-infra` Terraform. The deploy job uses `update-express-gateway-service` and `describe-express-gateway-service`. See `deploy:production` in `.gitlab-ci.yml` for the exact CLI invocations.

### Step 4: Configure CloudFront Distribution ID

The CloudFront distribution is provisioned by `aws-infra` Terraform. Copy the distribution ID from Terraform outputs and add it as the `CLOUDFRONT_DISTRIBUTION_ID` GitLab variable.

---

## Test Stage Details

### test:shared
Runs unit tests for the shared module (models, storage interface, analyzer, crypto).
- No external dependencies

### test:postgres
Runs PostgreSQL integration tests for the storage layer.
- Spins up a temporary PostgreSQL 16 container
- Tests Row-Level Security policies and migrations
- Must pass before database schema changes merge to main

### test:api
Runs unit tests for the API service.
- Tests HTTP handlers, middleware, auth integration
- Uses mock storage layer

### test:ingestion
Runs unit tests for the ingestion service.
- Tests cost/usage fetchers, provider interface, analyzer integration
- Uses mock AWS SDK clients

### test:lint
Runs golangci-lint across all modules.
- Checks for code quality issues, security problems, and style violations
- Timeout: 5 minutes per module

### test:vet
Runs `go vet` across all modules.
- Detects suspicious constructs and potential bugs

---

## Build Stage Details

### build:api
1. Builds multi-stage Docker image from `services/api/Dockerfile`
2. Tags with commit SHA and `latest`
3. Logs into AWS ECR
4. Pushes both tags to `axiaops-api` repository
5. Only runs on `main` branch

### build:ingestion
Same as `build:api` but for ingestion service.

### build:dashboard
Builds the Vite production bundle with `VITE_*` environment variables baked in at build time. The specific variables are defined in the job in `.gitlab-ci.yml`.

---

## Deploy Stage Details

⚠️ **TAG-GATED MANUAL gate** — `deploy:production` requires a human to click "Play".

### deploy:production
1. **Manual trigger required** — click "Play" in GitLab CI/CD pipeline view (tag-gated)
2. Authenticates to AWS via GitLab OIDC (`sts:AssumeRoleWithWebIdentity`, role `gitlab-ci-axiaops-deploy`) — no static keys
3. Reads `INGESTION_URL` and other platform config from SSM `/axiaops/prod/platform/*`
4. Runs DB migrations as a one-off ECS Fargate task (pinned to the release image) — **before** updating services
5. Updates ingestion ECS Express service first (via `update-express-gateway-service`), then api; polls `describe-express-gateway-service` until steady
6. Syncs the Vite production bundle to S3 + invalidates CloudFront (`/*`); invalidation failure is non-blocking

---

## Monitoring and Troubleshooting

### View Pipeline Status

In GitLab: **CI/CD → Pipelines** — each commit shows pass/fail for each stage and job.

### Common Failures

| Failure | Cause | Solution |
|---------|-------|----------|
| `test` fails | Code issue or test environment setup | Fix code or environment; push new commit |
| `build` fails | Docker build error or ECR authentication | Check `docker build` logs; verify OIDC role trust and ECR permissions |
| `deploy:production` fails | ECS service update rejected or migration failed | Check job logs; see `deploy:production` in `.gitlab-ci.yml` and `aws-infra` for service definitions |
| Linting fails | Code style or quality issues | Run `golangci-lint run ./...` locally and fix |
| PostgreSQL test timeout | Database not ready in time | Increase `services.postgres.timeout` or check PostgreSQL health |

### Check Logs

Click on any job in GitLab to view full logs:
- Build logs show Docker build steps
- Deploy logs show ECS Express service update status
- Test logs show failed assertions

### Rollback on Deploy Failure

If a deployment fails, the previous image SHA is still in ECR (immutable repos). To roll back: re-run `deploy:production` from the previous tag, or use `update-express-gateway-service` with the previous image digest. See `deploy:production` in `.gitlab-ci.yml` for the exact CLI form.

---

## Cost Considerations

| Component | Cost |
|-----------|------|
| ECR storage (images) | ~€1/month (old images accumulate) |
| ECS Express compute | see `aws-infra/docs/terraform-prod-design.md` for current cost model |
| Data transfer (push to ECR) | ~€0.02 per build (negligible) |

**Cost optimization:**
- CI/CD minutes are free if you use a self-hosted runner (see below)
- Shared GitLab runner: 400 minutes/month free
- Old ECR images: Delete monthly to avoid storage charges

---

## Self-Hosted Runner (Optional)

To avoid the 400 minute/month limit on shared runners:

```bash
# Install GitLab Runner (macOS)
brew install gitlab-runner

# Register runner
gitlab-runner register \
  --url https://gitlab.com \
  --token <PROJECT_TOKEN> \
  --executor docker \
  --docker-image docker:24-dind \
  --description "AxiaOps self-hosted"

# Start runner as service (macOS)
gitlab-runner install --user ${USER} --working-directory ~/.gitlab-runner
gitlab-runner start
```

**Cost:** ~€4.51/month for Hetzner CX22 VPS (2 vCPU, 4GB RAM) — more than sufficient.

---

## Best Practices

1. **Always test locally first:**
   ```bash
   make test-all
   make lint
   ```

2. **Test before pushing to main:**
   - Feature branch → test stage only
   - Code review → build and deploy only after approval
   - Push to main → automatic full pipeline

3. **Monitor deployments:**
   - GitLab CI/CD pipeline dashboard
   - AWS ECS service console
   - Application logs in CloudWatch

4. **Rotate secrets:**
   - OIDC role trust is managed in `aws-infra` — no access keys to rotate
   - Rotate `ENCRYPTION_KEY` procedure (see `docs/ops.md`)

5. **Clean up old images:**
   ```bash
   aws ecr describe-images --repository-name axiaops-api --query 'imageDetails[*].[imageTags,imagePushedAt]' | sort -k2 | head -20
   ```

---

## Related Documentation

- [docs/development_plan.md](development_plan.md) — Phase 2.10 GitLab CI Pipeline specification
- [docs/deployment.md](deployment.md) — AWS infrastructure and cost estimates
- [docs/production.md](production.md) — Secrets management and security
- [ci/RUNNER-SETUP.md](../ci/RUNNER-SETUP.md) — Self-hosted runner configuration
