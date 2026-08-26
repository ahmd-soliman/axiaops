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

## Minimum viable install

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
pre-upgrade` hook Job before any app Pod starts — migrations always apply
before the api/ingestion/dashboard Pods are (re)created.

## Values

See `values.yaml` — every key is commented in place. The values most worth
knowing about upfront:

| Key | Default | Notes |
|---|---|---|
| `devMode.enabled` | `true` | Auth bypass — only ever appropriate for a throwaway/personal environment. |
| `image.apiSuffix` | `""` | Empty (default) pulls the DEV_MODE-hardwired-off production api/ingestion image. Set to `-devmode` to instead pull the sibling build that honours a `DEV_MODE` env var at runtime — an explicit, deliberate opt-in, not the default. |
| `ingress.enabled` | `false` | Deliberately off by default — see above. |
| `postgres.existingSecret` | `""` | Required for anything to actually start; chart installs without it (renders `NOTES.txt` warnings) so `helm template`/CI linting doesn't need a real Secret to succeed. |
