# Deployment Files

This directory contains Docker Compose files for different environments.

## Files

### `dev.yml` — Development Environment
- **DEV_MODE**: `true` (auth bypassed)
- **Image tags**: `:${DEPLOY_ENV}` (e.g. `:dev-1`, `:dev-2`)
- **Restart policy**: `unless-stopped`
- **Network**: Uses external GitLab runner network
- **Usage**: GitLab CI builds and runs this. The CI deploy jobs (`deploy:dev-1`, `deploy:dev-2`) supply `DEPLOY_ENV`, `API_HOST_PORT`, `DASHBOARD_HOST_PORT` so multiple dev slots can run on the same host.

```bash
DEPLOY_ENV=dev-1 API_HOST_PORT=30031 DASHBOARD_HOST_PORT=30032 \
  docker-compose -f deploy/dev.yml up -d
```

### `staging.yml` — Staging Environment
- **DEV_MODE**: `false` (real Kinde auth required)
- **Image tags**: `:staging`
- **Restart policy**: `unless-stopped`
- **Network**: Uses external GitLab runner network
- **Usage**: GitLab CI builds and runs this

```bash
docker-compose -f deploy/staging.yml up -d
```

## Local Development

For local development (macOS/Linux), use the root `docker-compose.yml` with `make` commands:

```bash
make start-dev    # Uses root docker-compose.yml + .env files
make stop
make test
```

## GitLab CI

CI builds and deploys via `.gitlab-ci.yml`. Dev and staging are deployed manually on `main`/`develop` branches. Production is deployed to AWS App Runner via ECR — no compose file is used for production.

## Environment Variables

### Required in Staging/Production
- `DATABASE_URL`
- `MIGRATION_DATABASE_URL`
- `ENCRYPTION_KEY` (32-byte hex)
- `KINDE_ISSUER`
- `KINDE_CLIENT_ID`
- `KINDE_CLIENT_SECRET`

### Optional (have defaults)
- `SCAN_INTERVAL` (default: `60s` for dev, `1h` for staging)
- `DEV_MODE` (default: `true` for dev, `false` for staging)
- `LOG_LEVEL` (default: `info`)
