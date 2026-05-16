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

[Unreleased]: https://gitlab.com/axiaops/axiaops/-/compare/0.1.0-alpha.1...develop
[0.1.0-alpha.1]: https://gitlab.com/axiaops/axiaops/-/releases/0.1.0-alpha.1
