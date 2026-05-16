# CI/CD Quick Reference — AxiaOps

Fast lookup for common CI/CD tasks, debugging, and operations.

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

## Development Workflow

```bash
# 1. Make local changes
git checkout -b feature/my-feature
# ... code ...

# 2. Test locally before pushing
make test-all         # Run all tests
make lint             # Run linters
make test:vet         # Run go vet

# 3. Push to feature branch (test stage runs automatically)
git push origin feature/my-feature

# 4. Check pipeline in GitLab CI/CD
# → Wait for test stage to complete

# 5. Code review → merge to main
# → Build and deploy stages run automatically
```

---

## Pipeline Stages at a Glance

| Stage | When | Duration | What |
|-------|------|----------|------|
| **test** | All branches | ~2–3 min | Unit tests + linting + vet |
| **build** | main only | ~3–5 min per image | Docker build + ECR push |
| **deploy** | main only | ~5–10 min per service | App Runner update + CloudFront |

---

## Debugging Failed Jobs

### 1. View Job Logs
In GitLab → **CI/CD → Pipelines** → click job name → scroll to logs.

### 2. Common Test Failures

**`test:postgres` fails with connection timeout:**
```
Waiting for postgres:5432...
Connection timed out
```
- PostgreSQL container didn't start in time
- Rerun the job (may be transient)
- Check Docker availability in CI environment

**`test:lint` fails with linting errors:**
```
main.go:42:1: should have a comment, or be unexported (revive)
```
- Run locally to reproduce:
  ```bash
  cd services/api && golangci-lint run ./...
  ```
- Fix the issue and push a new commit

**`test:api` or `test:ingestion` fails with import errors:**
```
cannot find module providing package axiaops.io/shared
```
- Ensure `go.mod` files reference correct paths
- Run `go mod tidy` in the service directory
- Commit and push

### 3. Common Build Failures

**`build:api` fails with "login required":**
```
denied: User is not authorized to perform: ecr:PutImage
```
- AWS credentials missing or invalid
- Check GitLab CI/CD variables: `AWS_ACCESS_KEY_ID`, `AWS_SECRET_ACCESS_KEY`
- Verify IAM policy has `ecr:PutImage` permission (see gitlab_ci_pipeline.md)

**`build:dashboard` fails with "npm error":**
```
npm ERR! code ENOENT
npm ERR! syscall open
```
- Missing `package.json` or `package-lock.json`
- Verify `services/dashboard/` directory exists and has lockfile
- Run `npm install` locally and commit the lock file

### 4. Common Deploy Failures

**`deploy:api` fails with "service not found":**
```
An error occurred (ServiceNotFoundException) when calling the UpdateService operation
```
- App Runner service `axiaops-api` doesn't exist
- Create it via AWS Console or CLI (see gitlab_ci_pipeline.md)
- Verify service name in deploy job (`--service-arn` parameter)

**`deploy:dashboard` fails at CloudFront invalidation:**
```
Warning: CloudFront invalidation failed; cache may be stale
```
- Non-fatal; dashboard deployed successfully but cache wasn't cleared
- Manually invalidate CloudFront in AWS Console:
  - **CloudFront → Distribution → Create Invalidation → Path: `/*`**

---

## Manual Overrides

### Retry a Failed Job
In GitLab → **CI/CD → Pipelines** → click failed job → **Retry job**.

### Skip CI Pipeline
For commits that don't need testing (docs only):
```bash
git push -o ci.skip
# or in commit message:
git commit -m "Docs update

[skip ci]"
```

### Force Rebuild an Image
If an image build fails but the code is correct:
```bash
git commit --allow-empty -m "Rebuild Docker images"
git push origin main
```

---

## Monitoring Deployments

### Check App Runner Status
```bash
aws apprunner describe-service \
  --service-arn arn:aws:apprunner:eu-central-1:123456789012:service/axiaops-api \
  --region eu-central-1 \
  --query 'Service.[Status,UpdateStatus]'

# Output: ["RUNNING", "IN_PROGRESS"] or ["RUNNING", "UPDATE_COMPLETE"]
```

### View App Runner Logs
```bash
aws logs tail /aws/apprunner/axiaops-api/service --follow
```

### Verify Deployment
```bash
# API
curl -s https://api.axiaops.dev/health | jq .

# Ingestion
curl -s https://ingest.axiaops.dev/health | jq .
```

---

## Rollback

### Rollback to Previous Image

If the latest deployment causes issues:

1. **Find previous image SHA** in ECR:
   ```bash
   aws ecr describe-images \
     --repository-name axiaops-api \
     --query 'sort_by(imageDetails, &imagePushedAt)[-5:].[imageTags,imagePushedAt]'
   ```

2. **Manually update App Runner** (temporary):
   ```bash
   aws apprunner update-service \
     --service-arn arn:aws:apprunner:eu-central-1:123456789012:service/axiaops-api \
     --source-configuration ImageRepository={ImageIdentifier=123456789012.dkr.ecr.eu-central-1.amazonaws.com/axiaops-api:PREVIOUS_SHA}
   ```

3. **Fix the code**, commit, and re-deploy:
   ```bash
   git revert HEAD  # or fix and commit
   git push origin main  # triggers pipeline
   ```

---

## CI/CD Variables Management

### View All Variables
GitLab → **Settings → CI/CD → Variables**

### Update a Variable
1. Click the variable
2. Edit the value
3. Save

### Rotate AWS Credentials
1. Generate new access keys in AWS IAM Console
2. Update `AWS_ACCESS_KEY_ID` in GitLab
3. Update `AWS_SECRET_ACCESS_KEY` in GitLab
4. Delete old keys from AWS IAM Console

### Rotate ENCRYPTION_KEY
⚠️ **Dangerous operation — requires database migration:**

```bash
# NEVER change this without:
# 1. Backing up the database
# 2. Re-encrypting all account secrets in the DB
# 3. Testing in staging first
# See docs/ops.md for full procedure
```

---

## Performance Tips

### Speed Up Test Stage
- Run tests in parallel locally:
  ```bash
  go test -parallel 4 ./...
  ```

### Speed Up Build Stage
- Use Docker layer caching (automatic in GitLab)
- Minimize Docker image size:
  ```bash
  docker image ls | grep axiaops-api
  # Compare sizes; prune unused layers
  ```

### Speed Up Deploy Stage
- Check App Runner auto-scaling configuration
- Monitor CloudFront invalidation:
  ```bash
  aws cloudfront get-invalidation \
    --distribution-id E123ABC \
    --id I456DEF
  ```

---

## ECR Maintenance

### List All Images
```bash
aws ecr describe-images --repository-name axiaops-api
```

### Delete Old Images (Manual)
```bash
# Find images older than 30 days
aws ecr describe-images \
  --repository-name axiaops-api \
  --query 'imageDetails[?imagePushedAt<`2024-03-11`].[imageDigest,imageTags]'

# Delete by digest
aws ecr batch-delete-image \
  --repository-name axiaops-api \
  --image-ids imageDigest=sha256:abc123
```

### Set Lifecycle Policy (Automatic Cleanup)
```json
{
  "rules": [
    {
      "rulePriority": 1,
      "description": "Delete images older than 30 days (keep latest 5)",
      "selection": {
        "tagStatus": "any",
        "countType": "imageCountMoreThan",
        "countNumber": 5
      },
      "action": {
        "type": "expire"
      }
    }
  ]
}
```

---

## Troubleshooting Checklist

- [ ] **Test fails:** Run `make test-all` locally; fix and commit
- [ ] **Build fails:** Check Docker build locally; verify AWS credentials
- [ ] **Deploy fails:** Verify App Runner service exists; check logs
- [ ] **Performance slow:** Check pipeline duration in GitLab; profile locally
- [ ] **Security issue:** Rotate secrets; check IAM policy
- [ ] **Old images accumulating:** Set ECR lifecycle policy
- [ ] **CloudFront cache stale:** Manually invalidate or wait 24 hours

---

## Emergency Procedures

### Halt All Deployments
1. Go to **Settings → Protected Branches**
2. Require approval before merging to `main`
3. Don't merge until issue is resolved

### Emergency Rollback
```bash
# 1. Revert the bad commit
git revert <commit_sha>
git push origin main

# 2. Pipeline runs automatically, deploys previous version
# 3. Investigate the issue in a feature branch
```

### Disable CI/CD Temporarily
In **Settings → CI/CD → Disable CI/CD** (not recommended — leave enabled for visibility)

---

## Related Commands

```bash
# Local testing (before pushing)
make test-all         # All tests
make test:shared      # Shared module only
make test:api         # API service only
make test:ingestion   # Ingestion service only
make lint             # Linters
make vet              # Go vet

# Development
make start-dev        # Start Docker Compose (fixture data)
make start-staging    # Start with real AWS integration

# Database
make seed             # Populate dummy data
./scripts/check_db.sh # Inspect PostgreSQL

# AWS CLI
aws apprunner list-services --region eu-central-1
aws ecr list-images --repository-name axiaops-api
aws logs tail /aws/apprunner/axiaops-api/service --follow
```

---

## FAQ

**Q: Can I run the pipeline on feature branches?**
A: Test stage runs on all branches. Build and deploy only run on `main` to prevent accidental production changes.

**Q: How often do images get pushed to ECR?**
A: Only when you commit to `main`. Feature branches don't build images.

**Q: Can I manually trigger a build?**
A: In GitLab → **CI/CD → Pipelines** → **Run pipeline** (requires main branch access).

**Q: What if the test environment is flaky?**
A: Retry the job. PostgreSQL startup can be transient. If consistently failing, check Docker disk space.

**Q: How do I test the Docker build locally?**
A: `docker build -f services/api/Dockerfile .`

**Q: Can I deploy without running tests?**
A: No. Tests are required before build. Use `git push -o ci.skip` only for non-code changes (docs, etc.).

---

## Support

- **GitLab CI/CD docs:** https://docs.gitlab.com/ee/ci/
- **AWS App Runner docs:** https://docs.aws.amazon.com/apprunner/
- **Docker docs:** https://docs.docker.com/
- **Go testing:** https://golang.org/pkg/testing/
