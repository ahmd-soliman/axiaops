# AxiaOps Helm chart

A generic, environment-agnostic Helm chart for one AxiaOps environment:
`api`, `ingestion`, `dashboard`, and an in-cluster Valkey. One `helm install` = one environment, same relationship
as one self-hosted host = one environment in `../dev.yml` / `../staging.yml` /
`../preview.yml` / `../demo.yml` / `../integration.yml`.

This chart is a **template**, not a deployment. It contains no real
hostnames, secrets, or environment identity — every environment-specific
value (domain, database DSNs, encryption key, ...) is supplied by whoever
installs it, via `-f`/`--set` or (for a GitOps install) an ArgoCD
`Application`'s inline `helm.values`. Real values for any given environment
belong in that deployer's own repo, not here.

## What this chart does NOT do

- **No bundled Postgres.** Every existing environment runs Postgres as a
  standalone process outside its app deploy unit (see
  `apps/axiaops-<env>-db` in the homelab repo) — this chart keeps that
  separation and expects `postgres.existingSecret` to point at a Secret
  with `database-url` / `migration-database-url` /
  `runtime-admin-database-url` keys already created out-of-band.
- **No secret generation or management.** `secrets.existingSecret` and
  `redis.auth.existingSecret` are the same story — bring your own Secret.
  There's no sealed-secrets/external-secrets assumption baked in.
- **No ingress-controller-specific resources.** `templates/ingress.yaml` is
  a plain `networking.k8s.io/v1` Ingress. A Traefik-based cluster that wants
  `IngressRoute` CRDs instead (for header-based health checks, middlewares,
  etc.) should leave `ingress.enabled: false` here and supply its own
  `IngressRoute` alongside the `Application` that installs this chart —
  see `self-hosted-infra/gitops/apps/` for the established pattern (that's where
  the real, non-generic per-environment config for the `axiaops.*` cluster
  environment lives, not in this repo).

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
pre-upgrade` hook Job before any app Pod starts — same ordering
`.gitlab-ci.yml`'s `deploy:<env>` jobs already enforce (migrate first, then
recreate api/ingestion/etc.).

## Values

See `values.yaml` — every key is commented in place. The values most worth
knowing about upfront:

| Key | Default | Notes |
|---|---|---|
| `devMode.enabled` | `true` | Auth bypass — only ever appropriate for a throwaway/personal environment. |
| `image.apiSuffix` | `""` | Set to `-production` to pull the DEV_MODE-hardwired-off api/ingestion image variant, same knob as `.gitlab-ci.yml`'s `API_IMAGE_TAG_SUFFIX`. |
| `ingress.enabled` | `false` | Deliberately off by default — see above. |
| `postgres.existingSecret` | `""` | Required for anything to actually start; chart installs without it (renders `NOTES.txt` warnings) so `helm template`/CI linting doesn't need a real Secret to succeed. |
