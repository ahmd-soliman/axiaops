# Deployment Files

This directory contains Docker Compose files for different environments.

## Files

### `dev.yml` — Development Environment
- **DEV_MODE**: `true` (auth bypassed)
- **Image tags**: `:dev`
- **Restart policy**: `unless-stopped`
- **Network**: Uses external GitLab runner network
- **Usage**: GitLab CI builds and runs this

```bash
docker-compose -f deploy/dev.yml up -d
```

### `prod.yml` — Production Environment
- **DEV_MODE**: `false` (real Kinde auth required)
- **Image tags**: `:prod`
- **Restart policy**: `always`
- **Network**: Internal network (created by docker-compose)
- **Logging**: JSON format for log aggregation
- **Healthchecks**: Enabled for monitoring
- **Required secrets**: Must be set as env vars

```bash
export DATABASE_URL=...
export ENCRYPTION_KEY=...
export KINDE_ISSUER=...
export KINDE_CLIENT_ID=...
export KINDE_CLIENT_SECRET=...

docker-compose -f deploy/prod.yml up -d
```

## Local Development

For local development (macOS/Linux), use the root `docker-compose.yml` with `make` commands:

```bash
make start-dev    # Uses root docker-compose.yml + .env files
make stop
make test
```

## GitLab CI

In CI, the build stage uses `deploy/dev.yml`:

```bash
docker-compose -f deploy/dev.yml up -d --force-recreate
```

## Environment Variables

### Required in Production
- `DATABASE_URL`
- `MIGRATION_DATABASE_URL`
- `ENCRYPTION_KEY` (32-byte hex)
- `KINDE_ISSUER`
- `KINDE_CLIENT_ID`
- `KINDE_CLIENT_SECRET`

### Optional (have defaults)
- `SCAN_INTERVAL` (default: `60s` for dev, `1h` for prod)
- `DEV_MODE` (default: `true` for dev, `false` for prod)
- `LOG_LEVEL` (default: `info`)
