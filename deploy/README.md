# Deployment

Three real deployment paths for AxiaOps:

| Path | Where | Use for |
|---|---|---|
| `docker-compose.yml` (repo root) | Local dev | `make start-dev` / `make start-staging` |
| `deploy/helm/axiaops/` | Kubernetes | Self-hosting on any k8s cluster |
| `terraform/` (repo root) | AWS | ECS Express + RDS |

## `helm/`

A generic, environment-agnostic Helm chart — see [`helm/axiaops/README.md`](helm/axiaops/README.md)
for the minimum-viable install and the full values reference.

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
