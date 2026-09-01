# Deployment

Three real deployment paths for AxiaOps:

| Path | Where | Use for |
|---|---|---|
| `docker-compose.yml` (repo root) | Local dev | `make start-dev` / `make start-staging` |
| `deploy/helm/axiaops/` | Kubernetes | Self-hosting on any k8s cluster |
| `terraform/` (repo root) | AWS | ECS Express + RDS |

## `helm/`

A generic, environment-agnostic Helm chart, published as a standard Helm chart
repository (`helm repo add axiaops https://axiaops.io/charts`
— same pattern as `helm repo add traefik https://traefik.github.io/charts`) — see
[`helm/axiaops/README.md`](helm/axiaops/README.md) for install instructions and
the full values reference.

## `certs/`

Vendored TLS trust anchors needed by the services themselves (e.g. the AWS RDS
global CA bundle, required for `sslmode=verify-full` against RDS) — see
[`certs/README.md`](certs/README.md).

## `observability/`

A ready-to-import Grafana dashboard and an example Prometheus scrape config —
see [`docs/OBSERVABILITY.md`](../docs/OBSERVABILITY.md) for what they cover.

## Local development

```bash
make start-dev    # root docker-compose.yml + .env files, DEV_MODE=true
make start-staging  # full stack, native auth
make stop
make test
```

`start-dev` builds the DEV_MODE-capable `api`/`ingestion` images locally
(`docker compose build`, no `BUILD_TAGS`) — that build is never pushed
anywhere. CI (`ci.yml`/`release.yml`) only builds and publishes the
production shape (`BUILD_TAGS=production`, DEV_MODE compiled out); an
auth-bypass image has no business sitting in a public registry, even under
a clearly-labelled tag. If you want a DEV_MODE image outside `docker
compose` (e.g. for the Helm chart's `devMode.enabled`), build it yourself —
`docker build -f services/api/Dockerfile .` with no `BUILD_TAGS` — and push
it to a registry you control.
