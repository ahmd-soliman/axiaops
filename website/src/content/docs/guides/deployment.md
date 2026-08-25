---
title: Deployment
description: Deploying AxiaOps via its Helm chart.
---

AxiaOps ships as a Helm chart at `deploy/helm/axiaops` — a generic,
environment-agnostic template. `helm install` = one environment. It
contains no real hostnames, secrets, or environment identity; every
environment-specific value is supplied by whoever installs it.

## What the chart includes

| | |
|---|---|
| **Deploys** | `api`, `dashboard`, `ingestion`, and an in-cluster Valkey. |
| **Does not bundle Postgres** | Every environment runs Postgres as a standalone process outside the chart — you point `postgres.existingSecret` at a pre-created Secret instead. |
| **Does not generate or manage secrets** | `postgres.existingSecret` and `secrets.existingSecret` are both "bring your own Secret" — the chart assumes no sealed-secrets/external-secrets operator. |
| **Ingress is opt-in** | `templates/ingress.yaml` is a plain `networking.k8s.io/v1` Ingress, off by default. A Traefik-based cluster wanting `IngressRoute` CRDs instead should leave `ingress.enabled: false` and supply its own `IngressRoute` alongside whatever installs the chart. |

## Getting it running

```bash
kubectl create secret generic axiaops-postgres \
  --from-literal=database-url="postgres://axiaops:...@host:5432/axiaops?sslmode=disable" \
  --from-literal=migration-database-url="postgres://axiaops_owner:...@host:5432/axiaops?sslmode=disable" \
  --from-literal=runtime-admin-database-url="postgres://axiaops_runtime:...@host:5432/axiaops?sslmode=disable"

kubectl create secret generic axiaops-secrets \
  --from-literal=encryption-key="$(openssl rand -hex 32)"

helm install axiaops . \
  --set postgres.existingSecret=axiaops-postgres \
  --set secrets.existingSecret=axiaops-secrets
```

`helm install`/`upgrade` runs `services/migrate` as a `pre-install,
pre-upgrade` hook Job before any app Pod starts, so migrations always land
before `api`/`ingestion` restart against the new schema.

## Values worth knowing up front

| Key | Default | Notes |
|---|---|---|
| `devMode.enabled` | `true` | Bypasses auth. Only appropriate for a throwaway/personal environment — flip off for anything real. |
| `image.apiSuffix` | `""` | Set to `-production` to pull the DEV_MODE-hardwired-off `api`/`ingestion` image variant. |
| `ingress.enabled` | `false` | Deliberately off by default — see above. |
| `postgres.existingSecret` | `""` | Required for anything to actually start. The chart installs without it (rendering `NOTES.txt` warnings) so `helm template` / CI linting doesn't need a real Secret to succeed. |
| `redis.enabled` | `true` | Deploys the in-cluster Valkey Deployment + Service. Set `redis.auth.existingSecret` to a Secret containing a full `redis-url` DSN to point at an external Redis-compatible service instead — note this currently still deploys the in-cluster pod alongside it; there isn't yet a clean way to disable the embedded pod while keeping an external `REDIS_URL` wired in. |

## Required Secrets

Three Secrets need to exist before the deployment goes fully healthy (Helm
renders fine without them — the chart's own `NOTES.txt` warns about it — but
any Deployment/Job reading them will crash-loop):

- **`axiaops-postgres`** — `database-url`, `migration-database-url`,
  `runtime-admin-database-url`.
- **`axiaops-secrets`** — `encryption-key`.
- **`axiaops-registry`** *(if pulling from a private registry)* —
  `kubernetes.io/dockerconfigjson` image pull credentials.

## Trying it out

For a first install you don't need production-grade backing services: leave
`redis.enabled: true` (the default) so Valkey deploys in-cluster alongside
the app, keep `devMode.enabled: true`, and point `postgres.existingSecret`
at whatever Postgres you have handy — a single node with no HA story is
fine here. Tighten both once you're running it for real — see
[Deploying on AWS](/guides/aws-deployment/) for what that looks like on
EKS.
