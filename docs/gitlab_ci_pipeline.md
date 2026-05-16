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
3. **Deploy stage** — Automatic deployment to AWS App Runner and CloudFront invalidation

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
        └─── DEPLOY STAGE (main only, manual approval required ⚠️)
              ├── deploy:api       — Update App Runner service (click "Play" to trigger)
              ├── deploy:ingestion — Update App Runner service (click "Play" to trigger)
              └── deploy:dashboard — Update App Runner + invalidate CloudFront (click "Play" to trigger)
```

---

## Pipeline Configuration

### Variables

| Variable | Value | Purpose |
|----------|-------|---------|
| `AWS_DEFAULT_REGION` | `eu-central-1` | AWS region for ECR and App Runner |
| `AWS_ACCOUNT_ID` | `123456789012` | AWS account ID (AxiaOps) |
| `ECR_REGISTRY` | Derived from account ID | ECR registry URL |
| `IMAGE_*` | Registry + repository name | Docker image URLs |

### Secrets (GitLab CI/CD Variables — Masked)

Set these in **Settings → CI/CD → Variables** in GitLab:

| Variable | Value | Scope |
|----------|-------|-------|
| `AWS_ACCESS_KEY_ID` | Your AWS access key | Protected, masked |
| `AWS_SECRET_ACCESS_KEY` | Your AWS secret key | Protected, masked |
| `ENCRYPTION_KEY` | 32-byte hex AES key | Protected, masked |
| `VITE_KINDE_ISSUER` | Kinde issuer URL | Protected, masked |
| `VITE_KINDE_CLIENT_ID` | Kinde client ID | Protected, masked |
| `CLOUDFRONT_DISTRIBUTION_ID` | CloudFront distribution ID | Protected, masked |

**Recommendation:** Create an IAM user with minimal permissions (see **IAM Policy** below).

---

## Setup Instructions

### Step 1: Create AWS IAM User and Policy

Create a dedicated IAM user for CI/CD with limited permissions:

```json
{
  "Version": "2012-10-17",
  "Statement": [
    {
      "Effect": "Allow",
      "Action": [
        "ecr:GetAuthorizationToken",
        "ecr:BatchGetImage",
        "ecr:GetDownloadUrlForLayer",
        "ecr:PutImage",
        "ecr:InitiateLayerUpload",
        "ecr:UploadLayerPart",
        "ecr:CompleteLayerUpload",
        "ecr:DescribeRepositories",
        "ecr:ListImages"
      ],
      "Resource": "arn:aws:ecr:eu-central-1:123456789012:repository/axiaops-*"
    },
    {
      "Effect": "Allow",
      "Action": [
        "apprunner:UpdateService",
        "apprunner:DescribeService"
      ],
      "Resource": "arn:aws:apprunner:eu-central-1:123456789012:service/axiaops-*"
    },
    {
      "Effect": "Allow",
      "Action": [
        "cloudfront:CreateInvalidation",
        "cloudfront:GetInvalidation"
      ],
      "Resource": "arn:aws:cloudfront::123456789012:distribution/*"
    }
  ]
}
```

### Step 2: Add GitLab CI/CD Variables

In **GitLab Settings → CI/CD → Variables** (for now, only the build stage needs variables):

**Required for build stage:**
1. Create `VITE_KINDE_ISSUER` (masked, protected) — your Kinde issuer URL
2. Create `VITE_KINDE_CLIENT_ID` (masked, protected)

**Optional (add when AWS account is ready for deployment):**
3. Create `AWS_ACCESS_KEY_ID` (masked, protected)
4. Create `AWS_SECRET_ACCESS_KEY` (masked, protected)
5. Create `CLOUDFRONT_DISTRIBUTION_ID` (masked, protected) — your CloudFront distribution ID

All variables should be:
- **Masked** ✓ (hidden in logs)
- **Protected** ✓ (only on `main` branch)

**Note:** AWS credentials are only needed for deploy stage, which requires manual approval. You can add them later when the AWS account is ready.

### Step 3: Ensure AWS App Runner Services Exist

The pipeline assumes these App Runner services already exist:
- `axiaops-api`
- `axiaops-ingestion`
- `axiaops-dashboard`

Create them via AWS Console or CLI:

```bash
# Create API service
aws apprunner create-service \
  --service-name axiaops-api \
  --source-configuration ImageRepository={ImageIdentifier=123456789012.dkr.ecr.eu-central-1.amazonaws.com/axiaops-api:latest,ImageRepositoryType=ECR,ImageConfiguration={Port=8080}} \
  --auto-scaling-configuration ServiceAutoScalingConfigurationArn=arn:aws:apprunner:eu-central-1:123456789012:autoscalingconfiguration/AxiaOpsScaling \
  --region eu-central-1

# Create Ingestion service
aws apprunner create-service \
  --service-name axiaops-ingestion \
  --source-configuration ImageRepository={ImageIdentifier=123456789012.dkr.ecr.eu-central-1.amazonaws.com/axiaops-ingestion:latest,ImageRepositoryType=ECR,ImageConfiguration={Port=8081}} \
  --region eu-central-1

# Create Dashboard service
aws apprunner create-service \
  --service-name axiaops-dashboard \
  --source-configuration ImageRepository={ImageIdentifier=123456789012.dkr.ecr.eu-central-1.amazonaws.com/axiaops-dashboard:latest,ImageRepositoryType=ECR,ImageConfiguration={Port=80}} \
  --region eu-central-1
```

### Step 4: Configure CloudFront Distribution ID

If you have a CloudFront distribution fronting the dashboard, note its ID:

```bash
aws cloudfront list-distributions --query 'DistributionList.Items[?DefaultRootObject==`index.html`]'
```

Add the distribution ID as the `CLOUDFRONT_DISTRIBUTION_ID` GitLab variable.

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
Builds dashboard with environment variables:
- `VITE_KINDE_ISSUER` — Kinde OAuth issuer
- `VITE_KINDE_CLIENT_ID` — Kinde client ID
- Bakes these into the static bundle at build time

---

## Deploy Stage Details

⚠️ **Manual Approval Required** — All deploy jobs require manual intervention before execution.

### deploy:api
1. **Manual trigger required** — click "Play" in GitLab CI/CD pipeline view
2. Authenticates to AWS
3. Updates the `axiaops-api` App Runner service with the new image SHA
4. Waits for the deployment to complete (or times out after 5 minutes)
5. Sets environment variable `ENVIRONMENT=production`
6. Expects service ARN: `arn:aws:apprunner:eu-central-1:123456789012:service/axiaops-api`

### deploy:ingestion
1. **Manual trigger required** — click "Play" in GitLab CI/CD pipeline view
2. Same as `deploy:api` but for the ingestion service on port 8081

### deploy:dashboard
1. **Manual trigger required** — click "Play" in GitLab CI/CD pipeline view
2. Updates the `axiaops-dashboard` App Runner service
3. After successful deployment, invalidates CloudFront cache with `/*` pattern
4. CloudFront invalidation is non-blocking — if it fails, the job continues (graceful degradation)

---

## Monitoring and Troubleshooting

### View Pipeline Status

In GitLab: **CI/CD → Pipelines** — each commit shows pass/fail for each stage and job.

### Common Failures

| Failure | Cause | Solution |
|---------|-------|----------|
| `test` fails | Code issue or test environment setup | Fix code or environment; push new commit |
| `build` fails | Docker build error or ECR authentication | Check `docker build` logs; verify AWS credentials |
| `deploy` fails | App Runner service not found or update rejected | Verify service ARN; check App Runner service exists |
| Linting fails | Code style or quality issues | Run `golangci-lint run ./...` locally and fix |
| PostgreSQL test timeout | Database not ready in time | Increase `services.postgres.timeout` or check PostgreSQL health |

### Check Logs

Click on any job in GitLab to view full logs:
- Build logs show Docker build steps
- Deploy logs show App Runner update status
- Test logs show failed assertions

### Rollback on Deploy Failure

If a deployment fails, the previous image (e.g., `latest` tag) is still in ECR. Manually roll back via AWS Console:
1. Go to App Runner service
2. Click "Deploy a new version"
3. Select the previous image SHA from ECR

---

## Cost Considerations

| Component | Cost |
|-----------|------|
| ECR storage (images) | ~€1/month (old images accumulate) |
| App Runner compute | €5–10/month per service (scales to zero) |
| Data transfer (push to ECR) | ~€0.02 per build (negligible) |
| **Total CI/CD cost** | ~€0.05/month |

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
   - AWS App Runner service dashboard
   - Application logs in CloudWatch

4. **Rotate secrets:**
   - Regenerate `AWS_ACCESS_KEY_ID` every 90 days
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
