# Changelog

All notable changes to AxiaOps are documented in this file.

The format is based on [Keep a Changelog 1.1.0](https://keepachangelog.com/en/1.1.0/),
and this project adheres to [Semantic Versioning 2.0.0](https://semver.org/spec/v2.0.0.html)
under the conventions captured in [`docs/DEVELOPER_GUIDE.md`](docs/DEVELOPER_GUIDE.md) § 8.

## How to update

When opening a `release: X.Y.Z` PR from `develop` into `main`, move entries from
`## [Unreleased]` into a new section headed `## [X.Y.Z] — YYYY-MM-DD` and rotate
the compare links at the bottom of the file. Day-to-day PRs into `develop` add
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

## [0.1.0-alpha.27] — 2026-08-19

### Fixed

- stop cost-record retention tests from rotting on a hardcoded fixture date

## [0.1.0-alpha.26] — 2026-06-12

### Added

- add license-independent build-version footer
- add Permissions-Policy header (N-5) to SPA nginx configs
- add resource-type sub-filter to cost screen
- remove License page entirely under SaaS (managed state)
- invert license build-tag model — SaaS is default, self-hosted is opt-in
- activate dev-1 as the SaaS (license-removed) deploy target
- gate scans on entitlement in SaaS builds (license removal, Phase 2B)
- scaffold SaaS per-tenant entitlement (dormant Phase 2A)
- expand breach corpus to ~10k via xato-net merge (2.7.11)
- screen passwords against an offline breach corpus (2.7.11)
- show invite link expiry in the result box
- surface invite-email outcome + frame link as fallback

### Changed

- ci(deploy): wire prod SMTP relay password into ECS deploy from CI var
- docs(billing-plan): correct audit.Record signature and harden migration-number examples
- docs(billing-plan): clarify Shared-struct addition (DS9) implementation scope
- docs(billing-plan): third-pass refinements — DS9, migration naming, index guidance
- docs: document cross-plan interactions before billing (H-1 users-RLS, M-8 CSRF)
- docs(billing-plan): second-pass fixes after MR !338 review
- docs: billing plan + signup staleness refresh
- docs(dashboard): performance & state review — 2 high, 9 medium, 17 low findings
- ci(e2e): auto-trigger regression suite on all code-touching MRs
- docs(dashboard): note ordering constraint on resource-id prefix map
- docs(security): pre-billing security pass plan — close #94 remainder before Stripe
- docs: sync stale claims with shipped state; accept ADR-0002 (SaaS-first)
- docs: remove obsolete docs — point-in-time logs, brainstorms, superseded plans
- ci: reuse build:images in e2e instead of rebuilding from source
- ci: gate e2e:regression on unit+lint to skip it on fast failures
- ci: drop e2e:regression from build:images needs (it only added latency)
- ci: make both self-hosted CI jobs manual and independent
- docs(tasks): update Phase 2B row to reflect inverted SaaS-default build model
- docs: revert SaaS dev-1 deploy target (reconsidering design direction)
- docs(index): reference SaaS dev-1 deploy target design doc
- docs(saas): document dev-1 as the first SaaS deploy target (Phase 2B)
- chore: restore stray repo-root cmd binary to develop's version
- docs(tasks): sync 2.7.11 breach-corpus size with shipped ~10k corpus
- docs(auth): design password breach-corpus screening with offline HIBP
- ci(e2e): bake Playwright runner into hermetic image
- ci: rename self-hosted runner tag self-hosted -> app
- test(e2e): single auth-on lane — bootstrap ceremony + real cookie auth
- ci(e2e): add e2e stage + e2e:regression job; broaden plan to full suite
- test(e2e): add docker-compose e2e stack + make test-e2e
- test(dashboard): add Playwright e2e regression suite
- docs(e2e-plan): address !321 review — wording + "all pipelines?"
- docs: plan for a Playwright link-check E2E pipeline stage
- docs(rbac): design multi-owner support to remove single-owner SPOF
- docs(api): document SMTP invite-relay + PUBLIC_HOST knobs in .env.example
- chore(ci): set PUBLIC_HOST on dev-1/dev-2 deploys for invite emails
- chore(deploy): wire global SMTP relay into dev-1/dev-2 for invite emails
- docs(changelog): trim alpha.25 section to its one real entry

### Fixed

- enable Row-Level Security on users table (H-1)
- close dismiss modal before opening restore-confirm to prevent overlay focus fighting
- performance & state-management fixes across tenant + admin UIs
- mitigate invite-email header injection (N-2)
- hide License sidebar link under SaaS (managed state)
- skip load when enforcement bypassed (SaaS default build)
- gate e2e:regression on code_paths changes to unblock docs-only MRs
- gate e2e:regression on code changes; fix SaaS license-redirect test
- send organization_id in the ingestion scan integration test
- seed ci-tenant entitlement for the SaaS-default scan gate
- address code-review findings on the SaaS scan-gate
- clarify breach-corpus licensing and password-check policy sites
- eliminate network dependency from build and runtime
- bake runner deps into cached image (no runtime DNS)
- drop dns override — use runner's working default resolver
- point playwright container at public DNS for apt/npm
- gate postgres health on TCP to fix migrate startup race
- defer apt/npm to container start — build sandbox has no DNS
- make external-link liveness advisory, not fatal
- ESM-safe paths for setup session + storageState
- dashboard healthcheck IPv4 + dump logs on failure
- stop leaking integration test stacks on cancelled jobs
- scope Vitest to src/ so it ignores Playwright e2e specs
- backfill resource_records so every seeded zombie has a resource row
- use real anchors for in-app navigation (#130)


## [0.1.0-alpha.25] — 2026-06-06

### Added

- **Light/dark theme toggle on the native login screen.** The multi-tenant sign-in screen now carries a sun/moon toggle, matching the admin console. The shared pre-auth card styles gained a light palette alongside the existing dark one; the login screen follows the app theme — swapping the logo asset and persisting the choice via the existing `theme` localStorage key. The other four pre-auth screens are unchanged.


## [0.1.0-alpha.24] — 2026-06-06

### Changed

- **Graviton/ARM64 decision corrected back to "Declined — ECS Express is x86-only."** A prior rewrite of [`docs/graviton-arm-decision.md`](docs/graviton-arm-decision.md) had flipped the verdict to "Viable — Express supports ARM" on the strength of a third-party blog. Verified against the live AWS API on the prod account (2026-06-06) and disproven: `create/update-express-gateway-service` expose no `runtimePlatform`/`cpuArchitecture` field, and an Express service pointed at an arm64-only image fails to launch (`CannotPullContainerError … does not contain descriptor matching platform 'linux/amd64'`) — Fargate under Express hard-requires amd64 and doesn't infer arch from the manifest. The doc now carries the empirical evidence, documents the unofficial task-def-override hack (and why it self-reverts under Express's reconcile model, so it's unsafe for prod), and adds a recheck trigger for if/when AWS ships official Express ARM support.

### Added

- **Notification channels — email + Slack scan digests.** Admins can configure org-level outbound channels under **Settings → Integrations** (`/v1/channels`) that receive a savings digest after each scan. Email is SMTP/SES (no third-party SDK); Slack is an incoming webhook. Each channel has a trigger rule (`min_monthly_savings_usd` gate + `digest_top_n` body trim) and a **Test** button. Transport config (SMTP password / webhook URL) is AES-256-GCM encrypted at rest, masked as `***` on read, and scrubbed from delivery-error records. Dispatch is wired into the ingestion scan loop (best-effort, non-fatal) and every attempt is logged to a deliveries drawer. New permissions `channels:read` (viewer+) / `channels:manage` (admin+); migration `031_notification_channels`. Teams/Jira are pre-provisioned in the schema for follow-ups (#113/#114) but not yet shippable. See [`docs/notifications-plan.md`](docs/notifications-plan.md).

## [0.1.0-alpha.23] — 2026-05-30

### Fixed

- **Migrate task now auto-recovers from `migration_state.dirty=true`.** Previously, when a migration crashed mid-step (the alpha.20 prod incident was `ALTER ROLE NOSUPERUSER` rejected on RDS, leaving 029 half-applied with `dirty=true`), the wrapper silently skipped re-applying it on every subsequent boot. `runUpLoop` read `m.Version() → (29, true)`, then `idx.nextPendingUp(29, true)` found no `v > 29` and exited — golang-migrate's own `m.Steps(1)` would have raised `ErrDirty`, but we never reached the call. Net effect across alpha.21 and alpha.22: prod's `axiaops_runtime` role spent two release cycles missing `USAGE` on the schema, every table grant, and all 14 RLS bypass policies, surfacing only when alpha.22 actually wired `RUNTIME_ADMIN_DATABASE_URL` into the runtime path. This release adds a new `reapplyDirty` pass at the top of `runUpLoop` that rewinds via `m.Force(v-1)` and re-advances via `m.Steps(1)`, so the dirty version is re-applied under golang-migrate's normal success/failure machinery. The reapply is recorded as a distinct `migration_history` row with a `(dirty-recovery)` suffix in `name` so operators can tell recovery attempts apart from first-applies at a glance. After this release deploys to prod, the migrate task will idempotently re-run 029 and the role state heals automatically — no manual SQL. (`!273`)
- **Migration `021_native_auth` is now fully idempotent.** Retrofitted `CREATE TABLE sessions / password_resets / bootstrap_state` and `CREATE INDEX sessions_user_idx / sessions_expires_idx / password_resets_user_idx / password_resets_expires_idx` to use `IF NOT EXISTS`. Required by the new `reapplyDirty` recovery path — re-running a bare `CREATE TABLE foo` against an already-created `foo` would error "relation already exists" and infinite-loop the recovery. **Operator note:** because the file's SHA-256 changed, existing deployments (every env that applied 021 at the original SHA) will log `migration_history: file checksum drift detected` on every migrate run. The drift detector is non-strict by default (`MIGRATION_HISTORY_STRICT` is not set in any deploy/*.yml or .gitlab-ci.yml) so this is WARN noise, not a boot failure. Self-hosted operators who set `MIGRATION_HISTORY_STRICT=true` should either disable strict for the alpha.23 deploy or update the recorded SHA: `UPDATE axiaops.migration_history SET file_sha256='<new sha>' WHERE version=21 AND status='succeeded'`. (`!273`)

## [0.1.0-alpha.22] — 2026-05-30

### Fixed

- **`RUNTIME_ADMIN_DATABASE_URL` is now wired into the production ECS task
  definitions for `axiaops-api` and `axiaops-ingestion`.** The non-prod docker-
  compose deploys (`dev-1`, `dev-2`, `preview`, `staging`, `integration`) had
  been wired in `alpha.18`, but the `deploy:production` job in `.gitlab-ci.yml`
  generates ECS task-defs from a hand-written `secrets:` block (only the
  `migrate` task-def is Terraform-managed in aws-infra) — and that block was
  never updated. As a result, the `alpha.20` and `alpha.21` prod deploys
  produced task-defs missing the new env var, and both binaries
  (`api/cmd/main.go:73`, `ingestion/cmd/main.go:718`) `die()` at startup
  outside `DEV_MODE` with `storage: RUNTIME_ADMIN_DATABASE_URL is required`.
  ECS Express's deployment circuit-breaker rolled the api service back to
  `alpha.19` (reads kept working), but the ingestion service stayed on the
  new task-def in an open crashloop — no scheduled scans ran for ~2 hours
  on `2026-05-30`. This release adds the missing entry to both task-def
  `secrets:` blocks, loads `secret_arn_{api,ingestion}_runtime_admin_database_url`
  from the `/axiaops/prod/platform/*` SSM inventory (already published by
  aws-infra `!51`), and adds the standard `:?` "aws-infra not applied yet?"
  guards so a future un-applied state fails the deploy fast instead of
  silently shipping a crashloop again. (`!268`)

## [0.1.0-alpha.21] — 2026-05-30

### Fixed

- **Migration `029_runtime_admin_role` now applies on AWS RDS.** The original
  statement `ALTER ROLE axiaops_runtime NOSUPERUSER NOCREATEDB NOCREATEROLE
  NOINHERIT;` is rejected on RDS with `permission denied to alter role, Only
  roles with the SUPERUSER attribute may change the SUPERUSER attribute`.
  PostgreSQL requires the *caller* to be a true superuser before it can touch
  the SUPERUSER attribute on any role — even setting it to `NOSUPERUSER`-which-
  it-already-is. RDS's `axiaops_owner` is in `rds_superuser`, which is not a
  true superuser (same restriction that previously blocked `BYPASSRLS`). The
  three re-asserted defaults (`NOSUPERUSER` / `NOCREATEDB` / `NOCREATEROLE`)
  are already what `CREATE ROLE` set; only `NOINHERIT` is a real change.
  Migration now reads `ALTER ROLE axiaops_runtime NOINHERIT;`. End state
  identical on non-prod envs where 029 already applied (file SHA changes; the
  hash-drift detector will warn at the next migrate run — non-strict, no
  divergence in DB state). First successful application on prod.

## [0.1.0-alpha.20] — 2026-05-30

### Added

- **`RUNTIME_ADMIN_DATABASE_URL`** — least-privilege RLS-bypass connection
  (`axiaops_runtime`: DML + per-table bypass policies, **no DDL / no ownership**)
  used by api + ingestion for pre-auth / cross-org reads (native login,
  `/v1/me`, scheduled-scan enumeration, GDPR purge, stuck-scan recovery).
  Required outside `DEV_MODE`; the migrate task continues to use
  `MIGRATION_DATABASE_URL` (`axiaops_owner`). Migration `029_runtime_admin_role`
  introduces the role and policies. See `docs/runtime-admin-db-role.md`.

### Changed

- **Postgres image pinned to `postgres:17.5-alpine`** across local
  docker-compose, integration test stacks, the CI `test:storage` service
  container, `make` test targets, and the `aws-prod-sql` skill's throwaway VPC
  container. Matches the deployed envs (the deploy stack runs PG 17 on every self-hosted
  DB) and prod RDS (`engine_version = 17.5`). Local + CI were previously
  drifting on `postgres:16-alpine`.
- **Valkey image pinned to `valkey/valkey:8.1-alpine`** across `docker-compose.yml`
  and `deploy/*.yml`. Was floating on the `8-alpine` major tag.
- **Bootstrap install-token help in the dashboard is now deployment-aware** —
  points operators at the right retrieval surface per deployment shape
  (CloudWatch on ECS, `docker compose logs` for self-hosted, etc.) instead of
  one-size-fits-all copy.
- **CI:** API pipelines label themselves via `workflow:name` for cleaner
  pipeline listings.

### Removed

- Residual Kinde + Expo / React-Native references purged from docs and code.
  The product has shipped on native auth and a Vite + React web dashboard for
  some time; these were stale leftovers.

### Security

- **Runtime services no longer hold schema-owner privileges in non-dev envs.**
  Previously api + ingestion containers connected as `axiaops_owner` (the
  DDL/ownership-holding migrate role) to bypass RLS on cross-org reads — an
  RCE in either always-on container got the keys to reshape the schema.
  Migration `029` introduces the `axiaops_runtime` role with per-table
  permissive RLS-bypass policies (DML-only, no DDL, no ownership) and the
  runtime services now read `RUNTIME_ADMIN_DATABASE_URL`. The
  `MIGRATION_DATABASE_URL` connection is reserved for the one-off migrate
  task. Rolled out to preview / staging / demo / integration via this MR;
  the prod ECS task-def flip lands in the sibling `axiaops/aws-infra` repo.

## [0.1.0-alpha.19] — 2026-05-27

### Changed

- **Cache engine migrated from Redis to Valkey across all envs.** Container
  image: `redis:7-alpine` → `valkey/valkey:8-alpine`. Container name:
  `axiaops-redis-${DEPLOY_ENV}` → `axiaops-valkey-${DEPLOY_ENV}`. CLI
  invocations: `redis-server` / `redis-cli` → `valkey-server` /
  `valkey-cli`. Wire-protocol surfaces (`REDIS_URL`, `REDIS_PASSWORD`, the
  `/readyz` `"redis"` JSON key, the `redis://` URL scheme, the
  `github.com/redis/go-redis/v9` SDK, the `services/shared/{cache,queue}/redis/`
  Go package paths, and Prometheus metric names) intentionally stay —
  Valkey speaks RESP unchanged. Rationale: Redis 7.4+ relicense (RSALv2 /
  SSPL) makes the dependency awkward for self-hosted customers; Valkey 8
  is BSD-3 under Linux Foundation governance, drop-in compatible, and is
  what Debian / Ubuntu / Fedora / AWS ElastiCache now default to. The CI
  cleanup template lists both `axiaops-redis-${DEPLOY_ENV}` and
  `axiaops-valkey-${DEPLOY_ENV}` for a two-release rollback window
  (`docs/redis-to-valkey-migration.md` §3). Drop the redis-named entry
  after two release cycles.

## [0.1.0-alpha.18] — 2026-05-27

### Fixed

- **Account Settings screen now shows the right fields for role-mode accounts.**
  The screen rendered AWS Access Key ID + Secret inputs unconditionally; for
  customers onboarded via Launch Stack (role-based auth) those inputs were
  meaningless and the Save button rejected every submission with "Access Key
  ID is required". Role-mode accounts now show the Role ARN + ExternalId as
  read-only fields with a hint pointing to Connect → Role-based for
  re-verify-required changes; access-key accounts are unchanged.
- **Production dashboard footer no longer reports `dev · local`.** The
  `deploy:production` job's `npm run build` step never exported
  `VITE_APP_VERSION` / `VITE_APP_COMMIT_SHA`, so the bundle baked in the
  config.js fallback values. Every prod build since the S3+CloudFront
  migration shipped with `dashboard dev · local` in the footer. Now the
  exports mirror the existing Docker build args (tag → branch slug → SHA),
  giving operators a real identifier from a customer screenshot.

## [0.1.0-alpha.17] — 2026-05-27

### Fixed

- **Cross-account role onboarding (Verify step) no longer blocked on
  `sts:TagSession`.** The ingestion's cross-account `sts:AssumeRole` call
  no longer passes the `AxiaOpsOrg` session tag, which had required
  `sts:TagSession` on **both** the customer trust policy and the
  AxiaOps-side `AxiaOpsScanner` identity policy. Despite both grants being
  in place (verified via `aws iam get-role-policy` + `simulate-principal-
  policy`), an IAM regional propagation edge case in `eu-central-1` kept
  denying the call. The session tag was originally added "from day one"
  as future-proofing for SCP-based controls keyed on
  `aws:PrincipalTag/AxiaOpsOrg`, but no current feature consumes it, so
  removing it eliminates the entire two-sided permission requirement from
  the onboarding path. Re-introduce when an actual SCP key on
  `AxiaOpsOrg` ships.

### Changed

- **`test:integration:*` CI jobs only dump container logs + teardown on
  test failure.** Previously the `after_script` ran on every job exit,
  producing empty `--- logs <svc> ---` headers and a `cd: ... No such
  file or directory` line on every green pipeline (the Makefile target
  already tore down the stack on success). The block is now gated on
  `$CI_JOB_STATUS = failed` plus a directory-exists guard — silent on
  green, captures real container logs on red.

## [0.1.0-alpha.16] — 2026-05-27

### Added

- **One-click "Launch Stack" AWS onboarding.** Connect-account screen now
  offers a CloudFormation Launch Stack button that pre-fills the trust
  account ID and external ID and drops the operator into the AWS console
  with the template URL ready to apply. Reduces the cross-account role
  onboarding from "follow this 10-step runbook" to a single click for
  customers landing on Connect for the first time. Operator runbook at
  `docs/aws-account-onboarding.md`.
- **Read-only permissions policy surfaced in the Connect screen.** The
  account-onboarding panel now renders the exact AWS-managed read-only
  policy set the role will be granted, so customers can see and audit it
  before clicking Launch Stack instead of inferring it from documentation.
- **`gitlab-await-deploy-gate` skill.** New operator skill that watches a
  GitLab pipeline until its deploy gate is armed (manual stage available
  to click), without ever clicking it. Pairs with `gitlab-release` —
  release the tag, then run this skill to be paged the moment
  `deploy:production` is clickable.

### Fixed

- **BuildKit default attestations disabled on `build:images`.** BuildKit's
  default provenance + SBOM attestations were producing ECR images with
  manifest lists ECS Express Mode treats as multi-platform unknowns,
  causing pull-fail loops on deploy. Disabled at the buildx command level;
  also switched the env flag value to the literal `"true"` for readability.

## [0.1.0-alpha.15] — 2026-05-26

### Fixed

- **Role-based AWS account onboarding now works in production.** The prod
  dashboard is served as a static S3/CloudFront bundle (no nginx), so the
  runtime `window.__ENV__` injection used by the dev/staging Docker path never
  runs — the dashboard resolves `VITE_*` at build time only. The production
  build set no `VITE_AXIAOPS_AWS_ACCOUNT_ID`, so the Connect-account screen
  rendered "Role-based onboarding is not available in this environment" and fell
  back to access keys. The prod dashboard build now bakes it in (sourced from
  the deploy account — the principal customer trust policies allow on
  `sts:AssumeRole`), so the cross-account role-trust flow renders.

## [0.1.0-alpha.14] — 2026-05-26

### Fixed

- **Native login on self-hosted / production rejected every valid credential.**
  The api and ingestion containers were deployed without `MIGRATION_DATABASE_URL`,
  so `storage/postgres.NewWithOwner` silently fell back to the RLS-bound app
  pool. The native-login membership lookup (`LookupUserByEmail`) is
  pre-org-context and reads the RLS-protected `memberships` table with no
  `app.organization_id` set, so it returned zero rows and every login responded
  `401 invalid_credentials` regardless of password (bootstrap was unaffected — it
  sets the org GUC for its own membership insert). Wired the owner DSN into both
  services (aws-infra publishes `/axiaops/<env>/{api,ingestion}/MIGRATION_DATABASE_URL`)
  and added a startup fail-fast guard so a non-dev build refuses to start when
  it is missing — surfacing the misconfiguration as a red deploy instead of a
  green deploy serving broken auth.
- **Demo environment scans now run.** `AXIAOPS_LICENSE` is passed to the demo
  api and ingestion services so the license gate is satisfied.
- **Production deploy poll reads the correct ECS Express status path.** The
  steady-state poll queried the wrong key and logged `status=None`; corrected so
  the deploy reports the real service status.

## [0.1.0-alpha.13] — 2026-05-26

### Fixed

- **Production api → ingestion hop no longer 502s on a guessed hostname.** The
  deploy hardcoded `INGESTION_URL` to a deterministic
  `axiaops-ingestion.ecs.<region>.on.aws` name that never resolves — ECS
  Express assigns a random per-service suffix. The deploy now fetches the real
  endpoint from SSM (`/axiaops/prod/platform/ingestion_url`, published by
  aws-infra) with a sanity gate that fails early on a missing value, fixing the
  api container's credentials-verify and scan requests.
- **Production runtime secrets are now provisioned via SSM at deploy time.**
  `ENCRYPTION_KEY` and `AXIAOPS_LICENSE` are generated/minted and stored in SSM
  Parameter Store instead of Terraform state. The key is generated
  once-if-placeholder (hard-aborting on SSM read error to avoid orphaning
  encrypted account secrets) and synced across the api + ingestion slots (api
  is source-of-truth); the minted license (90-day token for production) is
  written to both SecureString params.
- **`ENCRYPTION_KEY` placeholder detection matches by prefix, not literal.** The
  deploy guard now detects the `PLACEHOLDER_*` prefix instead of an exact
  sentinel string, so a suffix rename on the aws-infra Terraform side can't
  silently break placeholder detection. A real 64-char lowercase-hex key can
  never match the prefix.

## [0.1.0-alpha.12] — 2026-05-26

### Fixed

- **Production api & ingestion containers now actually start.** The deploy's
  `update-express-gateway-service` payload omitted `command`; because the
  Express update *merges* the primary_container, the Terraform bootstrap's
  `sleep infinity` survived every deploy — the real image ran but the process
  never started (nothing on :8080/:8081, no logs, no first-run bootstrap
  banner, and the dashboard unreachable through CloudFront). The payload now
  sets `command` explicitly (`["./api"]` / `["./ingestion"]`), overriding the
  bootstrap sleep so the real binary runs.


## [0.1.0-alpha.11] — 2026-05-25

### Fixed

- **`deploy:production` dashboard publish no longer 403s on `index.html`.**
  The cache-control rewrite used a same-key `aws s3 cp` (s3→s3), whose
  HeadObject needs `s3:GetObject` — the write-only CI deploy role lacks it,
  so it failed `403 ... HeadObject ... Forbidden`. `index.html` is now
  excluded from the `aws s3 sync` and re-uploaded from the local build
  artifact with the no-cache header (a plain PutObject), preserving the
  role's least-privilege write-only posture.


## [0.1.0-alpha.10] — 2026-05-25

### Fixed

- **`deploy:production` completes the post-update steady-state wait on the
  pinned aws-cli.** The wait used `monitor-express-gateway-service
  --monitor-mode TEXT-ONLY`, but the pinned CI aws-cli (alpine 2.32.7)
  rejects that flag and the verb requires a TTY it has no way to provide in
  CI (the TTY-less `--mode TEXT-ONLY` only exists in aws-cli >= 2.34). The
  job now polls `describe-express-gateway-service` for `status=ACTIVE`
  instead — version-stable and TTY-free, with rollout verification via the
  `deploy-status` skill.


## [0.1.0-alpha.9] — 2026-05-25

Patch release on the `0.1.0-alpha` line — makes the first AWS production
deploy actually completable end-to-end. No schema or public-API changes.

### Fixed

- **`deploy:production` now completes on ECS Express.** The Express
  `update-express-gateway-service` call no longer sends `linuxParameters`
  or `healthCheck` in its primary-container payload — the verb rejects
  both, which aborted every tagged production deploy at the service-update
  step (after migrations had already applied). Container liveness stays
  gated at the ALB target group (Terraform-owned), so nothing is lost.
- **First-owner bootstrap is retrievable in production.** On ephemeral
  Fargate the install-token file is unreachable (no SSH / ECS Exec), so
  the api container now prints the single-use token to stdout
  (`BOOTSTRAP_PRINT_BANNER=true`) and disables the on-disk file
  (`BOOTSTRAP_TOKEN_FILE_PATH=""`). The token lands in CloudWatch
  (`/aws/ecs/axiaops-api`), making `POST /v1/auth/bootstrap` completable
  on a fresh production install. Capture it on first boot — a restart
  won't reprint.

## [0.1.0-alpha.7] — 2026-05-25

Patch release on the `0.1.0-alpha` line — fixes the `deploy:production` image so
the first AWS production deploy can complete. No schema or API changes.

### Changed

- `deploy:production` now runs on a **pinned current Alpine** (`image:
  alpine:3.23`) and installs its toolchain inline (`apk add --no-cache aws-cli
  docker-cli jq nodejs npm`), replacing the `0.1.0-alpha.6` custom prebuilt
  `amazonlinux:2023` image. The custom image was removed (Dockerfile +
  `build:ci-deploy-image` job + `CI_DEPLOY_IMAGE_TAG`): it was machinery a
  single, rarely-run, manual deploy didn't need. A current Alpine's apk repo
  carries aws-cli 2.32.7 (musl-native, with the ECS Express verbs), so the
  original "frozen `docker:24` shipped aws-cli 2.15.57" problem is solved by the
  base choice alone. A prebuilt image remains the documented upgrade path
  (issue #102) for if a second consumer or strict reproducibility appears.

### Fixed

- `deploy:production` failed on `0.1.0-alpha.6` at `xargs: command not found` —
  the `amazonlinux:2023` image was minimal and lacked `findutils`, which the
  SSM-chunking step needs. Alpine's busybox provides `xargs`/`find`, so the
  inline-Alpine deploy image resolves it.

## [0.1.0-alpha.6] — 2026-05-25

Patch release on the `0.1.0-alpha` line — clears the next two blockers the
first AWS production deploy surfaced after `0.1.0-alpha.5`. No schema or API
changes.

### Fixed

- `deploy:production` batched all 23 `/axiaops/prod/platform/*` SSM parameter
  names into a single `aws ssm get-parameters --names …` call, but SSM
  `GetParameters` caps at 10 names per call — the deploy aborted with
  `ValidationException ... Member must have length less than or equal to 10`.
  Names are now fetched in chunks of 10 and the per-chunk responses merged
  back into one document, preserving the existing fail-loud check on missing
  parameters.

### Changed

- `deploy:production` now runs on a **pinned prebuilt deploy image**
  (`ci/deploy/Dockerfile`: `amazonlinux:2023` + official aws-cli v2 + docker
  CLI + jq + node) published by a manual `build:ci-deploy-image` job, instead
  of `docker:24` + a runtime `apk add aws-cli`. Alpine's packaged aws-cli
  (2.15.57) lacked the ECS Express Mode verbs
  (`update-/monitor-express-gateway-service`, GA Nov 2025) the deploy needs,
  and the apk install also hit a musl `libexpat`/`pyexpat` symbol skew. The
  pinned glibc image fixes both and makes the toolchain reproducible.
  (Supersedes the interim `--upgrade expat` workaround from `0.1.0-alpha.5`;
  see issue #102 for the durable follow-up.)

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

[Unreleased]: https://github.com/ahmd-soliman/axiaops/compare/0.1.0-alpha.27...develop
[0.1.0-alpha.27]: https://github.com/ahmd-soliman/axiaops/tree/0.1.0-alpha.27
[0.1.0-alpha.26]: https://github.com/ahmd-soliman/axiaops/tree/0.1.0-alpha.26
[0.1.0-alpha.25]: https://github.com/ahmd-soliman/axiaops/tree/0.1.0-alpha.25
[0.1.0-alpha.24]: https://github.com/ahmd-soliman/axiaops/tree/0.1.0-alpha.24
[0.1.0-alpha.23]: https://github.com/ahmd-soliman/axiaops/tree/0.1.0-alpha.23
[0.1.0-alpha.22]: https://github.com/ahmd-soliman/axiaops/tree/0.1.0-alpha.22
[0.1.0-alpha.21]: https://github.com/ahmd-soliman/axiaops/tree/0.1.0-alpha.21
[0.1.0-alpha.20]: https://github.com/ahmd-soliman/axiaops/tree/0.1.0-alpha.20
[0.1.0-alpha.19]: https://github.com/ahmd-soliman/axiaops/tree/0.1.0-alpha.19
[0.1.0-alpha.18]: https://github.com/ahmd-soliman/axiaops/tree/0.1.0-alpha.18
[0.1.0-alpha.17]: https://github.com/ahmd-soliman/axiaops/tree/0.1.0-alpha.17
[0.1.0-alpha.16]: https://github.com/ahmd-soliman/axiaops/tree/0.1.0-alpha.16
[0.1.0-alpha.15]: https://github.com/ahmd-soliman/axiaops/tree/0.1.0-alpha.15
[0.1.0-alpha.14]: https://github.com/ahmd-soliman/axiaops/tree/0.1.0-alpha.14
[0.1.0-alpha.13]: https://github.com/ahmd-soliman/axiaops/tree/0.1.0-alpha.13
[0.1.0-alpha.12]: https://github.com/ahmd-soliman/axiaops/tree/0.1.0-alpha.12
[0.1.0-alpha.11]: https://github.com/ahmd-soliman/axiaops/tree/0.1.0-alpha.11
[0.1.0-alpha.10]: https://github.com/ahmd-soliman/axiaops/tree/0.1.0-alpha.10
[0.1.0-alpha.9]: https://github.com/ahmd-soliman/axiaops/tree/0.1.0-alpha.9
[0.1.0-alpha.7]: https://github.com/ahmd-soliman/axiaops/tree/0.1.0-alpha.7
[0.1.0-alpha.6]: https://github.com/ahmd-soliman/axiaops/tree/0.1.0-alpha.6
[0.1.0-alpha.5]: https://github.com/ahmd-soliman/axiaops/tree/0.1.0-alpha.5
[0.1.0-alpha.4]: https://github.com/ahmd-soliman/axiaops/tree/0.1.0-alpha.4
[0.1.0-alpha.3]: https://github.com/ahmd-soliman/axiaops/tree/0.1.0-alpha.3
[0.1.0-alpha.2]: https://github.com/ahmd-soliman/axiaops/tree/0.1.0-alpha.2
[0.1.0-alpha.1]: https://github.com/ahmd-soliman/axiaops/tree/0.1.0-alpha.1
