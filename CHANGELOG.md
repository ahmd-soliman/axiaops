# Changelog

All notable changes to AxiaOps are documented in this file.

The format is based on [Keep a Changelog 1.1.0](https://keepachangelog.com/en/1.1.0/),
and this project adheres to [Semantic Versioning 2.0.0](https://semver.org/spec/v2.0.0.html)
under the conventions captured in [`docs/versioning.md`](docs/versioning.md).

## How to update

When opening a `release: X.Y.Z` MR from `develop` into `main`, move entries from
`## [Unreleased]` into a new section headed `## [X.Y.Z] — YYYY-MM-DD` and rotate
the compare links at the bottom of the file. Day-to-day MRs into `develop` add
entries under `## [Unreleased]` only — never edit a published version section
after its tag is cut.

Each version section uses these subheadings, in this order, omitting empty ones:

- **Added** — new features.
- **Changed** — changes in existing behaviour that don't break public contracts.
- **Deprecated** — soon-to-be-removed features, kept working for now.
- **Removed** — features deleted in this release.
- **Fixed** — bug fixes.
- **Security** — vulnerability fixes and hardening.

## [Unreleased]

_Nothing yet — first entries land here in the next development cycle._

## [0.1.0-alpha.5] — 2026-05-25

Patch release on the `0.1.0-alpha` line — unblocks the first AWS production
deploy, which `0.1.0-alpha.4` could not complete. No schema or API changes.

### Fixed

- `deploy:production` now installs `aws-cli` with `apk add --no-cache
  --upgrade aws-cli jq expat`. On the frozen Alpine `docker:24` base the
  bundled `libexpat` was older than the `pyexpat` the current `aws-cli`
  package links against, so every `aws` invocation aborted with
  `XML_SetAllocTrackerActivationThreshold: symbol not found` —
  `aws sts get-caller-identity` and `aws ecr get-login-password` both died,
  and `docker login --password-stdin` then failed with a misleading
  `Cannot perform an interactive login from a non TTY device`. Upgrading
  `expat` in lockstep realigns it with `pyexpat`. (Durable follow-up: a
  pinned prebuilt deploy image with aws-cli v2 + docker-cli, tracked
  separately.)

## [0.1.0-alpha.4] — 2026-05-25

First cut deployed to **AWS production**. The headline is the production
deploy target moving off AWS App Runner onto **ECS Express Mode**, with the
service lifecycle now owned by Terraform in the sibling `axiaops/aws-infra`
repo. Still an internal alpha — no schema or public-API changes against
`0.1.0-alpha.3`.

### Added

- Manual `deploy:preview` button on `develop` pipelines — preview env can be
  brought up on demand from any develop commit without a tag.
- Auto-deploying integration environment on `develop` (its own self-hosted host)
  for end-to-end exercise of the deploy path ahead of staging.

### Changed

- **Production deploy migrated from AWS App Runner to ECS Express Mode**
  (`deploy:production`). The job now wires the OIDC role, the S3 + CloudFront
  dashboard publish, and the ECS one-off migrate task, and is aligned with the
  `axiaops/aws-infra` Terraform contract. Express service lifecycle (create /
  update) is handed to Terraform rather than the CI job.
- AWS production Terraform extracted out of this repo into the sibling
  [`axiaops/aws-infra`](https://gitlab.com/axiaops/aws-infra) stack; this repo
  now carries only a pointer.
- Container images hardened: explicit `ca-certificates`, a `HEALTHCHECK`, and
  a tini init process. (The non-root `USER app` switch was reverted — it broke
  DNS resolution under the CI runner's daemon; tracked for a proper fix.)
- `docs/`: Redis → Valkey migration plan added ahead of the cache-backend
  swap across all envs.

### Fixed

- Dashboard `SERVICE_CONFIG` now styles the six previously-unmapped AWS
  services, so their cards render with the correct icon/label instead of the
  fallback.
- `deploy:production` now fails loudly when an SSM platform parameter is
  missing, instead of proceeding with an empty value.
- CI: the `docs:placeholder` job now compares against the merge request's
  target branch (`$CI_MERGE_REQUEST_TARGET_BRANCH_NAME`) instead of a
  hardcoded `develop`. A CHANGELOG-only `develop → main` release MR
  previously diffed develop against itself, produced no placeholder
  pipeline, and was blocked by the "pipelines must succeed" gate; it now
  gets its pipeline and can merge.

## [0.1.0-alpha.3] — 2026-05-21

Patch release on the `0.1.0-alpha` line — single deploy fix surfaced via
the License page on staging. No schema or API changes.

### Fixed

- Deploy compose files now set `APP_ENV` on both `api` and `ingestion`
  containers per environment (`staging`, `preview`, `demo`, plus
  `${DEPLOY_ENV}` for dev-1 / dev-2). The License page (Settings →
  License) and `GET /v1/version` were reporting `Env: development` on
  every deployed env because the API handler falls back to
  `"development"` when the var is unset
  (`services/api/internal/api/handler.go:534`). Same value also feeds
  the structured-log `env` slog attribute, so log filtering by env now
  works end-to-end.

## [0.1.0-alpha.2] — 2026-05-20

Second alpha cut. Internal dogfooding only (dev-1 / dev-2 / staging). No
breaking schema or API changes against `0.1.0-alpha.1`.

### Added

- Self-service display-name editing via `PATCH /v1/users/me` — closes #78.
  Includes a no-op-rename guard so a PATCH with the current name is a 200
  without writing to the DB or audit log.
- Audit log denormalises the actor's display name onto each row (migration
  `028_audit_actor_name`). Historical audit entries now survive the actor's
  rename or deletion.
- `docs/terraform-prod-design.md` (~2.3k lines) — production Terraform
  shape after three review passes. Terragrunt re-engaged as Alternative B
  with explicit revisit triggers.

### Changed

- **Dashboard navigation refactor.** Top nav consolidated to Vantage's
  shape (Overview / Connect / Audit primary; sub-nav for the rest).
  Settings moved off the top nav into the avatar menu, where the
  admin-affordances belong. Icons trialled and then dropped from nav
  surfaces — labels carry enough signal at this density.
- Avatar menu shows the user's first name instead of email; the trigger
  and dropdown no longer expose the address (cross-org name-disclosure
  posture matches the `M-9` invitation-preview pin).
- Role-onboarding (Connect) UX polish: widened Role ARN input, tightened
  placeholder, restored the concrete-example hint, renamed the external-ID
  prefix. `FEATURE_ROLE_AUTH` feature flag retired — the role tab is the
  default for everyone.
- `AXIAOPS_AWS_ACCOUNT_ID` now validated at boot, with a sensible default
  for on-prem envs.
- `docs/versioning.md` filled in release cadence, support window, and
  migration policy. Added the CHANGELOG link-rotation step.
- `Tasks.md` refreshed: 2.7.2/2.7.4/2.7.23 reconciled with landed work;
  2.7.3 rescoped to docker-compose only (Helm deferred until a real
  customer ask with buying motion).

### Removed

- `FEATURE_ROLE_AUTH` feature flag from the dashboard.

### Fixed

- `nginx` `real_ip` module now recovers the true client IP behind
  multi-hop proxies (App Runner + edge). `docs/httpip.md` and
  `services/dashboard/nginx.conf` reconciled with the actual topology;
  the anti-spoof claim tempered to match what `real_ip` can actually
  guarantee.
- `fix(ci)`: `deploy:demo` was running against the GitLab runner's
  Docker daemon instead of the demo host. `DEPLOY_HOST_IP` now points
  at the demo host as intended.

### Security

- **C-1**: shared-secret HMAC-SHA256 on the api → ingestion hop
  (`POST /scan`, `POST /v1/credentials/verify`, and the Redis queue
  envelope). New `services/shared/httpauth` package with `Sign`,
  `Verify`, `Middleware`, `MultiSecretMiddleware`, and
  `PassthroughWithWarning`. Soft-enforce → hard-enforce rollout knob
  (`INGESTION_HMAC_SOFT_ENFORCE`); multi-slot key rotation
  (`INGESTION_SHARED_SECRET_NEXT`); per-env Redis `requirepass`
  propagation; observability counters. Plan:
  `docs/c1-hmac-plan.md`.
- **M-7**: sessions-cap revocation folded into the mint transaction
  with a per-user advisory lock serialising concurrent mints. Closes
  the race where two near-simultaneous logins could exceed
  `SESSIONS_PER_USER_CAP`. Cap-failure errors are now
  structured-logged.
- **H-3**: `RequireHTTPS` middleware now exempts IPv6 loopback
  (`::1`), matching the IPv4 carve-out. SSO callbacks against local
  IPv6-bound dev servers no longer 400.

## [0.1.0-alpha.1] — 2026-05-16

First tagged release. Captures all work from the Phase 1 MVP baseline
(April 2026, see _[0.0.x] — pre-tagging baseline_ below) through Phase 2
(real AWS integration, observability, native auth + SSO, license verification),
plus the 2026-05-09 security-audit closeouts and the versioning / release
pipeline itself.

### Added

#### Authentication & identity

- Native cookie sessions with argon2id password hashes; first-run install
  flow via `POST /v1/auth/bootstrap` (single-use install token written to
  `/var/run/axiaops/initial_setup_token` mode 0600).
- `GET /v1/auth/bootstrap/state` probe so the dashboard auto-redirects
  `/login → /bootstrap` on fresh installs.
- Email invitations with OOB redemption URLs, cross-org membership for
  existing users, and invitation preview
  (`POST /v1/auth/invitations/preview`, `POST /v1/auth/invitations/redeem`).
- Multi-organization sessions: login picker (`/v1/auth/select-org`) and
  in-app org switcher (`/v1/auth/switch-org`); session-cap eviction policy
  via `SESSIONS_PER_USER_CAP`.
- OIDC SSO with per-connection JWKS, RS256 ID-token validation, `azp`
  enforcement on multi-aud tokens, email-blur discovery on login, JIT
  provisioning, RP-Initiated Logout, and `enforcement=required` blocking
  native-password sessions.
- Self-hosted license JWT verification (RS256) with embedded production
  public key, in-grace handling, and a Settings → License pane in the
  dashboard.
- Build-tag–gated `production` builds that strip `DEV_MODE` from the api
  and ingestion binaries; embedded dev fixture for default builds.
- GDPR right-to-erasure: `DELETE /v1/users/me` (with sole-owner guard) and
  `DELETE /v1/organizations/me`, plus audit-log anonymisation across orgs.
- Admin-issued password-reset tokens (`POST /v1/users/{id}/password-reset`);
  redemption revokes every live session for that user.
- RBAC Phase 1: role tiers (`owner` / `admin` / `member` / `viewer`),
  permission gates on every protected route, and the dashboard memberships
  UI.

#### Detection & analyzer

- Strict validation on cost and usage records at the analyzer boundary;
  golden-file regression harness under
  `services/shared/analyzer/testdata/golden/`.
- API-only detection rules for unattached EBS volumes, orphaned snapshots,
  long-stopped EC2 (>30 days), unused AMIs, never-expiring CloudWatch log
  groups, orphaned RDS snapshots, stale ECR images, and unused Secrets
  Manager secrets.
- Tier 2 CloudWatch-based detections for CloudFront, Kinesis, and S3 with
  request metrics; additional services tracked in
  `docs/tier2_detections_status.md`.
- Scheduled auto-scan: per-account `scan_interval_hours`, background ticker
  that fires due scans, and stuck-scan recovery.

#### Dashboard

- Mobile-responsive layouts across every screen (navbar drawer, overview,
  settings, cloud accounts, audit, trend, detail, onboarding).
- New AxiaOps logo and favicon; theme system migrated to CSS custom
  properties; dark-mode plumbing.
- In-app restore flow with bulk-restore in the Hidden tab; long-tail
  collapse with Pareto 80% marker.
- Friendly error pages (404 / 500 / 503 / maintenance) and a
  `DestructiveConfirm` modal replacing `window.confirm`.
- Vitest + Testing Library scaffolding for frontend unit tests.

#### Audit & observability

- Per-org audit log (`GET /v1/audit`) with cursor pagination, filter UI
  (user, resource, action, time window), and DB-side anonymisation hooks.
- Prometheus metrics for HTTP, database, AWS API, and scan lifecycle;
  `/metrics` exposed via `observability.MetricsHandler()` (merges the
  default gatherer with the package-private registry).
- Structured `log/slog` JSON logging with `service`, `env`, `version`, and
  `commit` auto-attached.

#### Platform

- `cache.Cache` interface with Redis and in-memory implementations;
  backend selected by `REDIS_URL`.
- `queue.Queue` interface with Redis (LPUSH / BRPOP) and synchronous-HTTP
  fallback; backs the scan worker loop in ingestion.
- JWKS caching for OIDC discovery and per-(org, user) request rate
  limiting (advertised via `X-RateLimit-Limit` / `-Remaining` / `-Reset`
  response headers), both built on `cache.Cache`.
- Cache hit / miss / latency metrics exposed via the `observability`
  package.

#### Release pipeline

- Tag-only production deploys gated on semver tags
  (`X.Y.Z[-PRERELEASE.N]`).
- Manual `deploy:demo` job; per-env `PUBLIC_HOST` / `INTERNAL_DNS` plumbing.
- `SET search_path` lint gate on migrations
  (`scripts/check-migration-search-path.sh`).
- Versioning conventions (`docs/versioning.md`) covering tag rules,
  `develop → main` promotion flow, and the tag-only-staging decision
  point.

### Changed

- Migrated from Kinde-hosted auth to self-hosted native auth + OIDC SSO;
  dropped the `kinde_*` columns from `users` and renamed `kinde_sub` →
  `external_id` (migration 024).
- Migrations now record every event into `axiaops.migration_history`.
- Dashboard theme tokens migrated from hardcoded hex values to CSS custom
  properties.

### Security

Closed the 2026-05-09 internal audit findings:

- **C-2** — removed the committed `ENCRYPTION_KEY` fallback from deploy
  manifests.
- **C-3** — single-seam client-IP extraction; reject spoofed
  `X-Forwarded-For`.
- **H-3** — reject non-HTTPS OIDC discovery / metadata URLs.
- **H-4** — global request body cap; disallow unknown JSON fields.
- **H-5** — CSP, HSTS, and `X-Frame-Options` on the dashboard nginx.
- **M-1** — clear `BOOTSTRAP_INSTALL_TOKEN` env post-consume.
- **M-2** — atomic GETDEL on SSO state-token consumption.
- **M-3** — warn on wildcard `CORS_ORIGIN` outside `DEV_MODE`;
  `CORS_ORIGIN` plumbed through deploy manifests.
- **M-4 / M-5** — per-IP rate limiting on public auth probes
  (`/auth/login`, `/auth/select-org`, invitation preview).
- **M-6** — require matching `azp` on multi-aud ID tokens.
- **M-9** — strip `existing_user_name` from invitation preview
  (cross-org name-disclosure pin).

Also landed:

- OIDC RP-Initiated Logout (`logout_resolver`) so dashboard logout
  invalidates the IdP session.
- Open-redirect defence-in-depth on OIDC `return_to`.
- Org-enumeration and domain-confusion pins on `/v1/sso/discover`.
- Strict CSP on the dashboard with externalised inline scripts.
- C-1 ingestion HMAC plan documented (implementation pending; see
  `docs/c1-hmac-plan.md`).

## [0.0.x] — pre-tagging baseline (April 2026)

History before the first tag. Phase 1 MVP delivered:

- Two-service split (API on `:8080`, ingestion on `:8081`) sharing the
  `services/shared/` Go module.
- PostgreSQL 16 with Row-Level Security enforced via
  `app.organization_id`.
- Real AWS Cost Explorer + CloudWatch ingestion with AES-256-GCM–encrypted
  account secrets.
- Initial zombie-detection rules for EC2, RDS, Lambda, ELB, VPC NAT, and
  EIP.
- React + Vite dashboard with the core overview, trend, accounts, and
  detail screens.
- Docker-compose dev / staging stacks; LocalStack fixture seeding for
  offline cost data.

Reconstruct the full Phase 1 history via
`git log 0.1.0-alpha.1 --no-merges` once the tag is fetched.

[Unreleased]: https://gitlab.com/axiaops/axiaops/-/compare/0.1.0-alpha.5...develop
[0.1.0-alpha.5]: https://gitlab.com/axiaops/axiaops/-/tags/0.1.0-alpha.5
[0.1.0-alpha.4]: https://gitlab.com/axiaops/axiaops/-/tags/0.1.0-alpha.4
[0.1.0-alpha.3]: https://gitlab.com/axiaops/axiaops/-/tags/0.1.0-alpha.3
[0.1.0-alpha.2]: https://gitlab.com/axiaops/axiaops/-/tags/0.1.0-alpha.2
[0.1.0-alpha.1]: https://gitlab.com/axiaops/axiaops/-/tags/0.1.0-alpha.1
