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
- **No ingress-controller-specific resources.** `templates/ingress.yaml` is
  a plain `networking.k8s.io/v1` Ingress. A cluster that wants
  controller-specific resources instead (e.g. Traefik `IngressRoute` CRDs
  for header-based health checks, middlewares, etc.) should leave
  `ingress.enabled: false` here and supply its own alongside whatever
  installs this chart.

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

Either way, `image.tag` is **required** — the chart has no safe default to
fall back to (CI publishes commit-SHA tags like `main-<shortsha>`, not
semver, so there's nothing meaningful to default to). Find a real tag at
the [`api` package page](https://github.com/ahmd-soliman/axiaops/pkgs/container/axiaops%2Fapi).

```bash
kubectl create secret generic axiaops-postgres \
  --from-literal=database-url="postgres://axiaops:...@host:5432/axiaops?sslmode=disable" \
  --from-literal=migration-database-url="postgres://axiaops_owner:...@host:5432/axiaops?sslmode=disable" \
  --from-literal=runtime-admin-database-url="postgres://axiaops_runtime:...@host:5432/axiaops?sslmode=disable"

kubectl create secret generic axiaops-secrets \
  --from-literal=encryption-key="$(openssl rand -hex 32)"

# From the chart repo:
helm install axiaops axiaops/axiaops --version 0.2.0 \
  --set image.tag=main-b636978e \
  --set postgres.existingSecret=axiaops-postgres \
  --set secrets.existingSecret=axiaops-secrets

# From a local clone, same values, just a local path instead of repo/chart:
helm install axiaops . \
  --set image.tag=main-b636978e \
  --set postgres.existingSecret=axiaops-postgres \
  --set secrets.existingSecret=axiaops-secrets
```

`helm install`/`upgrade` runs `services/migrate` as a `pre-install,
pre-upgrade` hook Job before any app Pod starts — migrations always apply
before the api/ingestion/dashboard Pods are (re)created.

## Values

See `values.yaml` — every key is commented in place. The values most worth
knowing about upfront:

| Key | Default | Notes |
|---|---|---|
| `devMode.enabled` | `true` | Auth bypass — only ever appropriate for a throwaway/personal environment. Has no effect against the published image (see `image.apiSuffix`) — CI never publishes a build that honours it. |
| `image.tag` | `""` (**required**) | No safe default — see [Installing](#installing) above. |
| `image.apiSuffix` | `""` | Leave empty. CI only builds and publishes the DEV_MODE-hardwired-off production api/ingestion image — an auth-bypass build is never pushed to a public registry. Want `devMode.enabled: true` to actually do something? Build the DEV_MODE-capable image yourself (`docker build -f services/api/Dockerfile .`, no `BUILD_TAGS`), push it to a registry you control, and point `apiSuffix`/`image.registry` at that. |
| `ingress.enabled` | `false` | Deliberately off by default — see above. |
| `postgres.existingSecret` | `""` | Required for anything to actually start; chart installs without it (renders `NOTES.txt` warnings) so `helm template`/CI linting doesn't need a real Secret to succeed. |
