# AxiaOps Helm chart

A generic, environment-agnostic Helm chart for one AxiaOps environment:
`api`, `ingestion`, `dashboard`, and an in-cluster Valkey. One `helm install` = one environment.

This chart is a **template**, not a deployment. It contains no real
hostnames, secrets, or environment identity — every environment-specific
value (domain, database DSNs, encryption key, ...) is supplied by whoever
installs it, via `-f`/`--set` or (for a GitOps install) an ArgoCD
`Application`'s inline `helm.values`. Real values for any given environment
belong in that deployer's own repo, not here.

## What this chart does NOT do

- **No bundled Postgres.** Bring your own Postgres and point
  `postgres.existingSecret` at a Secret with `database-url` /
  `migration-database-url` / `runtime-admin-database-url` keys already
  created out-of-band.
- **No secret generation or management.** `secrets.existingSecret` and
  `redis.auth.existingSecret` are the same story — bring your own Secret.
  There's no sealed-secrets/external-secrets assumption baked in.
- **Opt-in ingress.** `templates/ingress.yaml` is a plain
  `networking.k8s.io/v1` Ingress, off by default (`ingress.enabled: false`).
  Hostnames are environment identity; set `ingress.enabled: true` and supply
  `ingress.host` when deploying to enable HTTP routing.

## Installing

Two ways to get the chart, same shape as e.g. `helm repo add traefik
https://traefik.github.io/charts`:

**From the chart repo (recommended)** — no clone needed:

```bash
helm repo add axiaops https://ahmd-soliman.github.io/axiaops/charts
helm repo update
helm search repo axiaops --versions   # see what's available
```

**From a local clone** — for chart development, or before the chart repo is
publicly reachable:

```bash
git clone https://github.com/ahmd-soliman/axiaops.git
cd axiaops/deploy/helm/axiaops
```

`image.tag` defaults to this chart's `appVersion` (`Chart.yaml`) — no need
to set it unless you want to override with a different published tag, or a
manually-built one for testing a feature branch that hasn't been released
yet. Browse published tags at the [`api` package
page](https://github.com/ahmd-soliman/axiaops/pkgs/container/axiaops%2Fapi).

> ℹ️ **PostgreSQL User Setup**: `migration-database-url` connects as `axiaops_owner` (must exist in PostgreSQL beforehand to execute DDL migrations). The application users (`axiaops` and `axiaops_runtime`) are automatically created and password-synced by AxiaOps's built-in `Bootstrap()` sequence if missing.

```bash
kubectl create secret generic axiaops-postgres \
  --from-literal=database-url="postgres://axiaops:...@host:5432/axiaops?sslmode=disable" \
  --from-literal=migration-database-url="postgres://axiaops_owner:...@host:5432/axiaops?sslmode=disable" \
  --from-literal=runtime-admin-database-url="postgres://axiaops_runtime:...@host:5432/axiaops?sslmode=disable"

kubectl create secret generic axiaops-secrets \
  --from-literal=encryption-key="$(openssl rand -hex 32)" \
  --from-literal=ingestion-shared-secret="$(openssl rand -hex 32)"

# From the chart repo:
helm install axiaops axiaops/axiaops --version 0.2.4 \
  --set postgres.existingSecret=axiaops-postgres \
  --set secrets.existingSecret=axiaops-secrets

# From a local clone, same values, just a local path instead of repo/chart:
helm install axiaops . \
  --set postgres.existingSecret=axiaops-postgres \
  --set secrets.existingSecret=axiaops-secrets
```

### Secrets management

This chart doesn't care how `axiaops-postgres`/`axiaops-secrets` come to
exist — it only reads them by name (`postgres.existingSecret` /
`secrets.existingSecret`), same as everything else in this "bring your own
Secret" chart (see [What this chart does NOT
do](#what-this-chart-does-not-do) above). Two starting points:

- **Quick start, any cluster** — the `kubectl create secret` commands
  above. No dependencies, good for a first install or a throwaway/personal
  environment.
- **Real deployment, any cloud** — manage them with the [External Secrets
  Operator](https://external-secrets.io/) instead of typing plaintext into
  a terminal. [`examples/external-secret-aws.yaml`](examples/external-secret-aws.yaml)
  is a worked example against AWS Secrets Manager, but the `ExternalSecret`
  shape is identical for GCP Secret Manager, Vault, Azure Key Vault, etc. —
  ESO supports [dozens of providers](https://external-secrets.io/latest/provider/aws-secrets-manager/)
  behind the same `SecretStore`/`ExternalSecret` API, so swap
  `secretStoreRef` and the provider-specific bits of the `SecretStore`
  itself; the two `ExternalSecret` resources in the example don't change.

Purely illustrative — nothing under `examples/` is applied by the chart
itself.

`helm install`/`upgrade` runs `services/migrate` as a `pre-install,
pre-upgrade` hook Job before any app Pod starts — migrations always apply
before the api/ingestion/dashboard Pods are (re)created.

## Values

See `values.yaml` — every key is commented in place. The values most worth
knowing about upfront:

| Key | Default | Notes |
|---|---|---|
| `devMode.enabled` | `true` | Auth bypass — only ever appropriate for a throwaway/personal environment. Has no effect against the published image (see `image.apiSuffix`) — CI never publishes a build that honours it. |
| `image.tag` | `""` | Falls back to `Chart.yaml`'s `appVersion`. Set explicitly to override — see [Installing](#installing) above. |
| `image.apiSuffix` | `""` | Leave empty. CI only builds and publishes the DEV_MODE-hardwired-off production api/ingestion image — an auth-bypass build is never pushed to a public registry. Want `devMode.enabled: true` to actually do something? Build the DEV_MODE-capable image yourself (`docker build -f services/api/Dockerfile .`, no `BUILD_TAGS`), push it to a registry you control, and point `apiSuffix`/`image.registry` at that. |
| `ingress.enabled` | `false` | Deliberately off by default — see above. |
| `postgres.existingSecret` | `""` | Required for anything to actually start; chart installs without it (renders `NOTES.txt` warnings) so `helm template`/CI linting doesn't need a real Secret to succeed. |
