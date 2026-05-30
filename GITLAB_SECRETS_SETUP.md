# GitLab CI/CD Setup Guide

This guide shows how to set up your GitLab CI/CD pipeline for AxiaOps.

## Overview

The CI pipeline (`.gitlab-ci.yml`) runs:
- ✓ Unit tests for all Go services
- ✓ PostgreSQL integration tests  
- ✓ Code linting
- ✓ Docker image builds & push to registry

**No secrets are needed for CI** — `.env` files already have dev defaults.

## Step 1: Commit & Push

The pipeline runs automatically on push:

```bash
git add .gitlab-ci.yml
git commit -m "Add GitLab CI/CD pipeline"
git push origin main
```

## Step 2: Monitor Pipeline

Go to **CI/CD → Pipelines** in your GitLab project to watch the build.

You should see:
- ✓ `test:unit` — Go unit tests
- ✓ `test:postgres` — Database integration tests
- ✓ `test:lint` — Code quality checks
- ✓ `build:api` — Docker image build & push
- ✓ `build:ingestion` — Docker image build & push
- ✓ `build:dashboard` — Docker image build & push

## Step 3: Registry Access (for Docker builds)

The pipeline pushes images to GitLab Container Registry. This works automatically because:
- GitLab provides `$CI_REGISTRY_USER` and `$CI_REGISTRY_PASSWORD`
- They're only valid within CI jobs (secure, no secret needed)

## Optional: Docker Registry Cleanup

After a few builds, you might want to clean up old images:

**Settings → CI/CD → Container Registry** → Delete old images

Or add a cleanup job to `.gitlab-ci.yml`.

## Deployment (Optional - Do This Later)

When you're ready to deploy (to AWS ECS Express, Kubernetes, etc.):

1. Add a `deploy` stage to `.gitlab-ci.yml`
2. Add secrets in **Settings → CI/CD → Variables** (if needed)
3. Configure your deployment target

For now, just focus on passing CI tests!

## Troubleshooting

**Pipeline fails with "postgres connection refused"?**
- The `test:postgres` job starts its own Postgres container
- This is normal in GitLab CI
- Make sure you have `make test-storage` working locally first

**Docker image build fails?**
- Check that Dockerfiles exist: `services/api/Dockerfile`, `services/ingestion/Dockerfile`, etc.
- You may need to add a `.dockerignore` file

**See pipeline logs:**
- Click any job → **Logs** tab
- Scroll down for detailed error messages
