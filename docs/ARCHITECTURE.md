# AxiaOps — Architecture Reference

This is the navigable hub for engineers working on AxiaOps. Read this first, then drill into the linked CLAUDE.md and per-feature docs only when you need detail.

> **Companion docs**
> - [DEVELOPER_GUIDE.md](DEVELOPER_GUIDE.md) — onboarding, env setup, common workflows
> - [/CLAUDE.md](../CLAUDE.md) — project-level conventions, commands, dev workflow
> - Per-service: [api/CLAUDE.md](../services/api/CLAUDE.md), [ingestion/CLAUDE.md](../services/ingestion/CLAUDE.md), [shared/CLAUDE.md](../services/shared/CLAUDE.md) — dashboard has no per-service CLAUDE.md yet, see [DEVELOPER_GUIDE.md § 3](DEVELOPER_GUIDE.md#3-code-conventions-youll-want-to-absorb) for dashboard conventions
> - Auth: [AUTHENTICATION.md](AUTHENTICATION.md)
> - Operations: [OPERATIONS.md](OPERATIONS.md) — connecting an AWS account, notification channels
> - Testing: [TEST_STRATEGY.md](TEST_STRATEGY.md)

## TL;DR

AxiaOps is a self-hosted, open-source FinOps tool that detects idle/zombie cloud resources still incurring cost. The MVP targets AWS; multi-cloud is on the Phase 4 roadmap. Three Go services communicate via HTTP — `api` (`:8080`) reads from PostgreSQL and exposes a REST surface to a Vite/React dashboard; `ingestion` (`:8081`) fetches AWS data and writes to PostgreSQL; `shared` is a library, not a process. Multi-tenancy is enforced at the database level via Postgres Row-Level Security keyed on `app.organization_id`. DEV_MODE bypasses auth for local development.

---

## 1. System Overview

```mermaid
flowchart TB
    subgraph browser["Browser"]
        dash["Dashboard<br/>(Vite + React, nginx-served)"]
    end

    subgraph runtime["Runtime services"]
        api["API service<br/>:8080<br/>axiaops.io/api"]
        ing["Ingestion service<br/>:8081<br/>axiaops.io/ingestion"]
    end

    subgraph data["Data layer"]
        pg[("PostgreSQL 17<br/>schema 'axiaops'<br/>RLS-enforced")]
        redis[("Redis<br/>(cache + queue,<br/>staging only)")]
    end

    subgraph cloud["AWS (customer account)"]
        ce[/"Cost Explorer"/]
        cw[/"CloudWatch"/]
        describe[/"EC2/RDS/Lambda/<br/>ELB/S3/... Describe"/]
    end

    subgraph idp["External (optional)"]
        oidc[/"OIDC IdP<br/>(Keycloak, Entra,<br/>Auth0, ...)"/]
    end

    dash -->|HTTPS REST<br/>/api/*| api
    api -->|HTTP POST<br/>:8081/scan| ing
    api -->|pgxpool| pg
    ing -->|pgxpool| pg
    api -->|optional<br/>cache + sessions| redis
    ing -->|AWS SDK v2| ce
    ing -->|AWS SDK v2| cw
    ing -->|AWS SDK v2| describe
    api -.->|JWKS<br/>RS256 ID-token| oidc

    classDef svc fill:#FFF7ED,stroke:#FDBA74,color:#9A3412
    classDef store fill:#EFF6FF,stroke:#BFDBFE,color:#1E3A8A
    classDef ext fill:#F5F5F4,stroke:#D6D3D1,color:#44403C
    class api,ing svc
    class pg,redis store
    class dash,ce,cw,describe,oidc ext
```

**Module layout** (Go workspace via `go.work`):

| Module | Path | Type | Imports |
|---|---|---|---|
| `axiaops.io/api` | `services/api/` | Binary | `axiaops.io/shared` |
| `axiaops.io/ingestion` | `services/ingestion/` | Binary | `axiaops.io/shared`, AWS SDK v2 |
| `axiaops.io/shared` | `services/shared/` | Library | (none of the others) |
| `dashboard-v2` | `services/dashboard/` | Vite + React app | — |
| migrate tool | `services/migrate/` | Binary | `axiaops.io/shared` |

The `shared` module is the boundary between "domain logic that runs anywhere" and "service-specific concerns". AWS SDK is *not* in `shared` — cloud-provider code lives in `ingestion/internal/provider/`.

---

## 2. Service Responsibilities

### `api` — read path + control plane

REST server on `:8080`. Exposes the dashboard's full surface (zombies, summary, trends, costs, accounts CRUD, dismissals, audit log, memberships, invitations, native auth, SSO ceremony, version metadata, observability metrics). Reads from PostgreSQL, never calls AWS directly. Triggers scans by HTTP POST to ingestion. See [api/CLAUDE.md](../services/api/CLAUDE.md) for the full endpoint table.

Notable patterns:
- Go 1.22+ `mux.HandleFunc("METHOD /path", fn)` route registration
- **Per-account scan lock is enforced at the DB row level**, not in-memory: `TryMarkAccountScanning` in `services/shared/storage/postgres/postgres.go` runs `UPDATE accounts SET status='scanning' WHERE id=$1 AND status<>'scanning'` and returns whether the row was updated. The atomicity of the UPDATE prevents duplicate concurrent scans even across multiple API replicas.
- 5-minute background ticker (`runStuckScanTicker` wired by `serverbuild.StartTickers` in `services/api/internal/serverbuild/build.go`) resets accounts stuck in `scanning` >15 min via `ResetStuckScans` in `services/shared/storage/postgres/migrate.go`. The 15-min const (`stuckScanTimeout`) lives in `services/api/cmd/main.go`.
- `production` build tag strips DEV_MODE from customer-shipping binaries

### `ingestion` — write path + AWS work

Long-lived HTTP server on `:8081`. Receives `POST /scan` with `{account_id, organization_id}`, decrypts the per-account AWS secret, calls Cost Explorer + CloudWatch + Describe APIs, runs detection, writes results. Mirrored `production` build-tag treatment so DEV_MODE can't be re-enabled at the ingestion side either.

### `shared` — domain library

Domain models, the `Store` interface (single contract for all data access), PostgreSQL implementation with RLS, the analyzer (pure detection functions), AES-256-GCM crypto, structured logging via `log/slog`, Prometheus observability helpers, cache + queue abstractions, audit-log helpers. See [shared/CLAUDE.md](../services/shared/CLAUDE.md).

### `dashboard` — Vite + React (web)

Served as a static bundle. In `start-dev` Vite proxies `/api/*` to the API on 8080; in `start-staging` nginx serves the bundle and proxies through the same path to the dockerised API. **Not** React Native or Expo — the codebase is pure web.

---

## 3. Scan Lifecycle (end-to-end)

The single most important data-flow path in the system.

```mermaid
sequenceDiagram
    autonumber
    actor User
    participant Dash as Dashboard
    participant API as API :8080
    participant ScanLock as scan-lock<br/>(in-mem mutex map)
    participant DB as PostgreSQL
    participant Ing as Ingestion :8081
    participant AWS as AWS APIs

    User->>Dash: click "Scan"
    Dash->>API: POST /v1/accounts/{id}/scan
    API->>DB: TryMarkAccountScanning(accountID)<br/>(atomic UPDATE WHERE status<>'scanning')
    alt already scanning (UPDATE matched 0 rows)
        API-->>Dash: 409 conflict
    else lock acquired
        API-->>Dash: 202 accepted
        API->>Ing: POST :8081/scan {account_id, organization_id}<br/>(goroutine, 15-min timeout)
        Ing->>DB: GetAccount → decrypt AWS secret
        Ing->>AWS: FetchCosts (Cost Explorer, paginated)
        Ing->>AWS: FetchUsage (CloudWatch + Describe APIs)
        Ing->>Ing: analyzer.Detect(costs, usage)
        Note over Ing: API-only Tier-1 discovery:<br/>EBS / snapshots / stopped EC2 /<br/>AMIs / log groups / RDS snapshots /<br/>ECR images / Secrets Manager
        Ing->>DB: SaveZombies + SaveResources
        Ing->>DB: SaveSnapshot + SaveSnapshotServices
        Ing-->>API: 200 OK
        API->>DB: status='connected' + last_scan_at = now()
    end

    Note over API: Background ticker (every 5 min):<br/>any account in 'scanning' > 15 min<br/>→ reset (runStuckScanTicker, services/api/<br/>internal/serverbuild/build.go:374).<br/>Ingestion worker may also set<br/>scan_timeout / circuit_breaker_open<br/>(worker.go:111-113).
```

**Backend account statuses** (canonical values written through the `Store`):

| Status | Set by | Meaning |
|---|---|---|
| `connected` | scan-success path | Healthy; last scan succeeded |
| `scanning` | `TryMarkAccountScanning` | Scan in progress; prevents concurrent re-entry |
| `error` | scan failure path | Last scan returned an error |
| `scan_timeout` | ingestion worker (`services/ingestion/cmd/worker.go:113`) | Scheduled scan exceeded its watchdog window |
| `circuit_breaker_open` | ingestion worker (`services/ingestion/cmd/worker.go:111`) | Repeated failures tripped the per-account breaker — scheduler skips this account until reset |

The dashboard renders the last two with the `theme.warning` (yellow) callout in `services/dashboard/src/components/AccountSelector.jsx` and `screens/AccountSettingsScreen.jsx`.

---

## 4. Deployment Topology

Three supported paths — pick whichever fits your infrastructure:

| Path | What it is | Where |
|---|---|---|
| `docker compose` | Single host, one `docker-compose.yml` | Repo root — `make start-dev` / `make start-staging` |
| Kubernetes (Helm) | One `helm install` per environment, bring your own Postgres | [`deploy/helm/axiaops/`](../deploy/helm/axiaops/) |
| AWS (Terraform) | ECS Express (Fargate) + RDS | [`terraform/`](../terraform/) |

All three sit behind an edge TLS terminator you provide (a reverse proxy, an
ingress controller, or CloudFront in front of ECS Express) — the services
themselves speak plain HTTP internally and rely on `X-Forwarded-Proto` to decide
whether to set the session cookie's `Secure` flag.

**Two env vars matter regardless of which path you pick:**

- **`PUBLIC_HOST`** — the externally-reachable hostname (e.g.
  `https://axiaops.example.com`), not an internal host/IP. Empty → SSO ceremonies
  fail at the IdP redirect.
- **`INTERNAL_DNS`** — needed only if your IdP has split-horizon DNS. Without it the
  API resolves the IdP via public DNS, hits whatever WAF fronts it (Cloudflare Bot
  Fight Mode, for instance, rejects Go's default UA on
  `/.well-known/openid-configuration`), and OIDC discovery fails.

### Graceful shutdown

Both `api` (`:8080`) and `ingestion` (`:8081`) handle `SIGTERM`/`SIGINT` via `signal.NotifyContext`, matters for any orchestrator that sends a term signal before killing the process (ECS, Kubernetes, systemd):

1. Stop accepting new requests
2. Drain in-flight requests / the current scan, up to a 30s timeout (`server.Shutdown(ctx)`)
3. Close the Postgres pool (`pool.Close()`)
4. Exit

A shutdown that hits the timeout logs and exits anyway rather than hanging — existing connections may be cut, but the process won't wedge a rolling deploy. Test locally with `make start-dev` in one terminal and `kill -SIGTERM <pid>` in another; expect `shutdown signal received, draining requests` then `shutdown complete, duration_seconds=…` in the logs.

---

## 5. Database & Multi-Tenancy

PostgreSQL 17 with **schema `axiaops`** (not `public`). Two connection roles: `axiaops_owner` runs migrations and owns DDL; `axiaops` is the app role bound by RLS policies.

### Row-Level Security pattern

Every per-organization table carries `organization_id UUID NOT NULL` and a policy of the form:

```sql
CREATE POLICY tenant_isolation ON {table}
  USING (organization_id = current_setting('app.organization_id', true));
```

The application sets `app.organization_id` **per transaction** (not per pool checkout) via `set_config('app.organization_id', $1, true)` with `local=true` scope. See `services/shared/storage/postgres/postgres.go:74` (`setOrganization`). Every Store method opens a tx, calls `setOrganization` first, then runs queries.

Code surface: `storage.WithOrganizationID(ctx, organizationID)` puts the org on the context; the postgres Store reads it on every query.

**Fail-closed guard for forgotten WithOrganizationID** — `services/shared/storage/postgres/postgres.go:87` explicitly checks the context-org-id before issuing any query and returns `"postgres: organization_id missing from context"` if absent. So a handler that forgets to call `WithOrganizationID()` gets a hard error at the DB boundary rather than a silent empty result set. (RLS would also filter to empty if the GUC were unset — the guard is the *first* line of defence; RLS is the second.)

### Schema overview

```mermaid
erDiagram
    organizations ||--o{ memberships : has
    organizations ||--o{ accounts : owns
    organizations ||--o{ pending_memberships : invites
    organizations ||--o{ audit_log : records
    organizations ||--o{ zombie_snapshots : aggregates
    users ||--o{ memberships : "is member via"
    users ||--o{ sessions : "logged in via"
    accounts ||--o{ cost_records : produces
    accounts ||--o{ resource_records : produces
    accounts ||--o{ zombie_records : produces
    accounts ||--o{ dismissed_zombies : has
    zombie_snapshots ||--o{ zombie_snapshot_services : breaks_down

    organizations {
        uuid id PK
        text name
        timestamp created_at
    }
    users {
        uuid id PK
        text email UK
        text password_hash "argon2id"
        text external_id "OIDC subject"
    }
    memberships {
        uuid id PK
        uuid organization_id FK
        uuid user_id FK
        text role "owner|admin|member|viewer"
    }
    accounts {
        uuid id PK
        uuid organization_id FK
        text provider "aws"
        text label
        text region
        text encrypted_secret "AES-256-GCM"
        text status "idle|scanning|scan_timeout|..."
        timestamp last_scan_at
        int scan_interval_hours
    }
    cost_records {
        uuid id PK
        uuid organization_id FK
        uuid account_id FK
        text service
        text resource_id
        text region
        numeric amount
        date period_start
    }
    zombie_records {
        uuid id PK
        uuid organization_id FK
        uuid account_id FK
        text service
        text resource_id
        numeric monthly_savings_usd
        text verdict
    }
```

(Truncated — see `services/shared/storage/postgres/migrations/` for the canonical, versioned schema.)

### Migration system

Tool: [golang-migrate/migrate v4](https://github.com/golang-migrate/migrate) — versioned SQL files, run on service startup using `MIGRATION_DATABASE_URL` (owner role). Naming: `NNN_description.up.sql` / `NNN_description.down.sql`, embedded via `//go:embed`.

**The five rules that keep upgrades safe** (a customer can skip minor versions —
every release replays whatever migrations are missing since their last upgrade):

1. **Never delete or edit a released migration.** Once a `.up.sql` ships in a tagged
   release it's immutable — fix forward with a new migration.
2. **Never reuse a migration number.** Gaps are fine; reuse breaks golang-migrate's
   version tracking.
3. **`.down.sql` is for local dev only** — there's no promise of a production
   downgrade path via `down`; production downgrade is "restore from backup."
4. **Renames/drops are a two-release dance** — release N adds the new column +
   dual-writes, release N+1 removes the old one. Never drop in one step if there's
   prod data on the column.
5. **Migrations should be fast** — single-digit seconds on a 100k-row table; anything
   potentially long-running needs an operator runbook entry, not just a `.up.sql`.

`postgres.Migrate` is a wrapper around golang-migrate, not a thin passthrough. On every boot it:

1. Pins a `*sql.Conn` and acquires a wrapper-level session advisory lock.
2. Runs **orphan recovery** — finalises any `axiaops.migration_history` row whose post-step UPDATE was lost to a crash mid-run.
3. Runs **backfill** if `migration_history` is empty but `migration_state` is non-empty (first deploy onto an existing DB).
4. Runs **drift detection** — compares the on-disk SHA-256 of every embedded `.up.sql` against the most recent recorded SHA. Mismatch → `slog.Warn` (log-only by default — `Migrate` runs in short-lived migrate/`axiaopsctl` binaries with no `/metrics` endpoint, so a Prometheus counter would be incremented and lost before any scraper reached it; the signal instead flows through the same log pipeline as everything else). `MIGRATION_HISTORY_STRICT=true` flips the posture to refuse-to-start (default-on for CI/staging/dev, default-off for self-hosted installs).
5. Drives `m.Steps(1)` in a loop. Each step gets a pre-INSERT `started` row (committed before the DDL) and a post-step UPDATE to `succeeded`/`failed`.

Two tables: **`migration_state`** (golang-migrate's own bookkeeping — current
`version` + `dirty` flag; `dirty=true` means the last migration failed mid-run and
needs manual intervention) and **`migration_history`** (a forensic audit table the
wrapper writes on every up/down/force event — *when* a version applied, *which
build* ran it, *which file bytes* were applied, whether it ever rolled back and
reapplied). `migration_history` is owned by `axiaops_owner`; the app role has
`SELECT` only. Inspect via `bin/axiaopsctl migrate history`, `bin/axiaopsctl migrate
drift` (prints on-disk-vs-recorded SHA mismatches on demand), or query the table
directly. Truncating `migration_history` is allowed but acts as a re-baseline — the
next boot backfills from the live files as ground truth, and whatever you wanted to
forensically catch is gone.

**Operator escape hatch**: `bin/axiaopsctl migrate force N` writes a `force` history
row (`file_sha256=NULL` — no file is applied, only `migration_state` is rewritten).
Prefer this over the raw `migrate force` binary so the event lands in
`migration_history`. There is no supported bastion-`migrate`-binary workflow — the
legitimate channel is always `axiaopsctl`.

---

## 6. Authentication & Authorization

There are **four** active login paths plus a development bypass:

```mermaid
flowchart TB
    start([User hits dashboard])
    start --> mode{DEV_MODE<br/>= true?}
    mode -->|yes| dev["DevBypass middleware<br/>injects DEV_USER_ID +<br/>DEV_ORGANIZATION_ID"]
    dev --> in([Logged in])

    mode -->|no| bootstrap{First-run?<br/>bootstrap_state row<br/>exists?}
    bootstrap -->|yes| boot["POST /v1/auth/bootstrap<br/>{token, email, password,<br/>organization_name}<br/>Creates first owner +<br/>org. Sealed forever."]
    boot --> in

    bootstrap -->|no| has_session{Has<br/>axiaops_session<br/>cookie?}
    has_session -->|valid| in

    has_session -->|no| flow{Pick flow}
    flow -->|email/password| native["POST /v1/auth/login<br/>argon2id password hash"]
    native --> ms{Multi-org<br/>user?}
    ms -->|no| in
    ms -->|yes| picker["Returns needs_org_selection<br/>+ org list. User picks<br/>→ POST /v1/auth/select-org"]
    picker --> in

    flow -->|SSO| sso["GET /v1/sso/oidc/{cid}/initiate<br/>→ IdP redirect<br/>→ /v1/sso/oidc/callback<br/>(state carries cid)"]
    sso --> in

    flow -->|invite link| invite["POST /v1/auth/invitations/redeem<br/>{token, password, name?}"]
    invite --> in
```

### Middleware composition (outermost → innermost)

`services/api/internal/serverbuild/build.go:285-340` (`ComposeServer`):

```
CORS → request-logging + metrics → request-id → auth (DevBypass OR WrapNative→EnforceSSO) → rate-limiter → mux
```

CORS sits outermost so preflight `OPTIONS` and rejection responses still carry
`Access-Control-Allow-*` headers; the rate-limiter sits closest to the mux because it
buckets by `(organization, user)`, which auth has to resolve first.

1. **CORS** (line 340) — outermost; reads `CORS_ORIGIN` (`*` for legacy/no-credentials,
   or a comma-separated allowlist that reflects `Origin` and emits
   `Access-Control-Allow-Credentials: true` so the session cookie round-trips
   cross-origin). Short-circuits `OPTIONS` preflights with `204`.
2. **Request-ID + structured logging + Prometheus metrics** (line 310) — outermost
   *behind* CORS so every request is counted, including auth failures. Injects
   `X-Request-ID` (uses the incoming header if present, else a fresh UUIDv4;
   retrievable via `middleware.RequestIDFromCtx(ctx)`) and records
   `axiaops_api_requests_total` / `axiaops_api_request_duration_seconds`, labelled
   by the matched **route pattern** (never a raw ID) to avoid cardinality blowup.
3. **Auth** — branches on DEV_MODE:
   - `DevBypass` (line 299, definition `middleware/auth.go:129`) injects `DEV_USER_ID` / `DEV_ORGANIZATION_ID` / `DEV_USER_EMAIL` onto the request context without a DB lookup or cookie.
   - **OR** `WrapNative + EnforceSSO` (lines 304–305) — reads the `axiaops_session`
     HttpOnly cookie, looks it up in the `sessions` table (via Redis cache when
     available), resolves `OrganizationID`/`UserID`/`Email`/`Role`/`AuthMode` onto
     the context. A failed lookup returns `401 unauthenticated` without echoing the
     internal reason. `EnforceSSO` then rejects (`403 sso_required`) a
     `auth_mode="password"` request against an org with SSO enforcement `required`
     — except `/v1/auth/logout`, always allowed. Context accessors:
     `middleware.OrganizationID/UserID/UserEmail/UserName/Role/AuthMode(ctx)`.
   - **Public paths** bypassing auth entirely: `/health`, `/livez`, `/readyz`,
     `/metrics`, every `/v1/auth/*`, `/v1/sso/discover`, the OIDC initiate/callback paths.
4. **Rate limit** (line 292) — Redis-backed token bucket per `(organization, user)`,
   only active when `REDIS_URL` is set (an in-memory fallback would be
   per-replica and meaningless under autoscaling). Default `RATE_LIMIT_MAX`/minute
   (1000); `429` with `Retry-After` + `X-RateLimit-*` headers; disabled in
   `DEV_MODE=true`. Login endpoints share a single budget so an attacker can't
   double the cap by alternating `/auth/login` and `/auth/select-org`.
5. **HTTP mux / handlers** (innermost).

**Public paths** (no auth required) are listed in `middleware/auth.go:45-60` (`publicPath`): `/health`, `/livez`, `/readyz`, `/metrics`, every `/v1/auth/*`, `/v1/sso/discover`, and the OIDC initiate/callback paths.

### Notes

- **Native auth**: argon2id hashes, constant-time compared at `services/api/internal/auth/password.go:104` (`Verify`). Sessions in the `sessions` table; cookie name `axiaops_session`; lookup at `auth/session.go:185` (`Manager.ValidateSession`) which checks the cache first then falls through to the DB.
- **Multi-membership** (B1.5) takes a two-step path: `/v1/auth/login` returns `needs_org_selection`, frontend collects choice, posts to `/v1/auth/select-org` which **re-validates the password** (defence in depth — never trust the frontend to remember step 1). 401 `invalid_credentials` collapses wrong-password / unknown-email / org-not-in-set into one shape so the no-creds membership-probe channel narrows.
- **OIDC SSO**: per-connection JWKS, RS256 ID-token validation. State carries the connection ID; the legacy path-cid callback `/v1/sso/oidc/{cid}/callback` stays wired one release as a deprecation window. Session minting at `services/api/internal/sso/oidc_callback.go:317` with `auth_mode='sso'`.
- **Sessions cap**: `SESSIONS_PER_USER_CAP` (default 10). The (cap+1)th login revokes the oldest.
- **In-app org switcher** (B1.5): `/v1/auth/switch-org` revokes the current session, mints a fresh one bound to the target org, audits the switch as `session.org_switched`.

### Right-to-erasure paths

- `DELETE /v1/users/me` — hard-delete the caller. 409 if sole owner of any organization. Audit log anonymised across all orgs.
- `DELETE /v1/organizations/me` — cascade-delete the entire org including audit log. Owner-only.

---

## 7. Detection Engine

26 detection rules across 18 AWS services, two tiers depending on the data source:

- **Tier 1 (CloudWatch-based)** — joins Cost Explorer billing data with a
  CloudWatch metric; cost with usage at/below threshold ⇒ flagged.
- **Tier 2 (API-only)** — Describe-API state alone determines waste (e.g. an
  unattached EBS volume is always waste, no metric needed).

### Tier 1 — CloudWatch-based

| AWS service | Metric (namespace) | Threshold | Verdict |
|---|---|---|---|
| EC2 | CPUUtilization (AWS/EC2) | ≤ 5% | Idle instance |
| RDS | DatabaseConnections (AWS/RDS) | = 0 | Abandoned database |
| Lambda | Invocations (AWS/Lambda) | = 0 | Unused function |
| ELB | RequestCount (AWS/ApplicationELB) | = 0 | Abandoned load balancer |
| NAT Gateway | BytesOutToDestination (AWS/NATGateway) | = 0 | Unused NAT Gateway |
| ElastiCache | CurrConnections (AWS/ElastiCache) | = 0 | Idle cache cluster |
| OpenSearch | SearchRate (AWS/ES) | = 0 | Unused search cluster |
| Redshift | DatabaseConnections (AWS/Redshift) | = 0 | Abandoned data warehouse |
| SageMaker | Invocations (AWS/SageMaker) | = 0 | Forgotten endpoint |
| DynamoDB | ConsumedReadCapacityUnits (AWS/DynamoDB) | = 0 | Unused table (provisioned mode) |
| EKS | cluster_node_count (ContainerInsights) | = 0 | Empty cluster (requires Container Insights) |
| CloudFront | Requests (AWS/CloudFront) | = 0 | Abandoned distribution |
| Kinesis | IncomingRecords (AWS/Kinesis) | = 0 | Unused data stream |
| S3 | AllRequests (AWS/S3) | = 0 | Abandoned bucket (requires request metrics enabled) |

### Tier 2 — API-only

| Resource | AWS API | Condition | Verdict |
|---|---|---|---|
| Elastic IP | `ec2:DescribeAddresses` | Not attached to any ENI | Unattached EIP |
| EBS Volume | `ec2:DescribeVolumes` | `state = "available"` | Unattached volume |
| EBS Snapshot | `ec2:DescribeSnapshots`+`DescribeImages` | Source volume gone, not backing any AMI | Orphaned snapshot |
| EC2 Instance | `ec2:DescribeInstances` | Stopped > 30 days | Long-stopped instance |
| AMI | `ec2:DescribeImages`+`DescribeInstances` | Age > 90d, unreferenced | Unused AMI |
| CloudWatch Log Group | `logs:DescribeLogGroups` | No retention policy | Wasteful log group |
| RDS Snapshot (manual) | `rds:DescribeDBSnapshots` | Age > 30d, source DB deleted | Orphaned RDS snapshot |
| ECR Repository | `ecr:DescribeRepositories`+`ListImages` | Untagged or age > 90d | Stale container images |
| Secrets Manager | `secretsmanager:ListSecrets` | `LastAccessedDate` > 90d | Unused secret |

The full read-only IAM permission list these calls require is in
[OPERATIONS.md § 1](OPERATIONS.md) — one source of truth shared by the
CloudFormation template, the Terraform snippet, and that doc.

### Detection flow

```
POST /scan → Fetch costs (Cost Explorer)
           → Discover resources (per-service Describe APIs)
           → Fetch usage (CloudWatch GetMetricStatistics)
           → Detect Tier 1 zombies (cost + usage join, apply thresholds)
           → Discover Tier 2 zombies (API-only state checks)
           → Combine, summarize, save to PostgreSQL
```

Detection lives in `services/shared/analyzer/`:

1. `Detect(costs, usage)` in `detector.go:46` — indexes usage by `resource_id` (O(1)), iterates cost records, looks up the service's rule, compares usage against the threshold, and emits a zombie record if the threshold is met.
2. `Summarize(zombies)` aggregates total monthly savings + per-service breakdown.
3. `AnnotateAll(costs, usage, zombies)` marks each cost record as zombie or active.

The `serviceRules` map lives in `analyzer/rules.go:17` — each entry is `rule{metric string, threshold float64, unit string, reason string}`. Adding a new Tier-2 service is one row in this map plus a CloudWatch metric mapping in ingestion.

**Strict validation at the boundary** — `model.CostRecord.Validate()` and `analyzer.UsageRecord.Validate()` reject unknown services, malformed currencies, bad regions, negative amounts. Production scan paths log-and-skip; tests fail-fast. The motivation: a loose validator silently drops "wrong-spelling EC2" rows and `Detect()` returns "0 zombies" with no signal.

**Golden-file detection tests** under `services/shared/analyzer/testdata/golden/<scenario>/` — each folder is a sub-test driven by `analyzer/golden_test.go`. Add a rule case → add a folder; rule change → `UPDATE_GOLDEN=1 go test ./analyzer/...` to rewrite the expected files in place, then review the diff before committing.

### Adding a new detection rule

1. `services/shared/analyzer/rules.go` — add to the `serviceRules` map
2. **Tier 2** (CloudWatch-driven): add the metric fetch in `services/ingestion/internal/provider/aws/cloudwatch.go` so it populates a `UsageRecord` for that resource
3. **Tier 1** (API-only): add a `DiscoverXxxZombies()` function in `services/ingestion/internal/provider/aws/discover_*.go` and call it from `runIngestionCore()` in `services/ingestion/cmd/main.go` (the orchestrator loop, around the existing Discover* calls between lines 544–685)
4. `services/shared/analyzer/testdata/golden/<scenario>/` — add the golden fixtures (input_costs.json, input_usage.json, expected_zombies.json)
5. Unit test for the threshold in `services/shared/analyzer/`
6. Update the IAM read-only policy list in [OPERATIONS.md § 1](OPERATIONS.md)

---

## 8. Observability

Prometheus metrics + structured logging across the stack. See [OBSERVABILITY.md](OBSERVABILITY.md) for the full guide.

```mermaid
flowchart LR
    api["API"] -- HTTP middleware<br/>auto-records --> http_metrics["axiaops_http_*"]
    api -- "DatabaseObserver" --> db_metrics["axiaops_db_*"]
    ing["Ingestion"] -- "AWSObserver" --> aws_metrics["axiaops_aws_*"]
    ing -- "ScanObserver" --> scan_metrics["axiaops_scan_*"]
    ing -- per-account --> savings_gauge["axiaops_potential_<br/>monthly_savings_usd"]

    http_metrics --> handler["/metrics endpoint<br/>(observability.MetricsHandler)"]
    db_metrics --> handler
    aws_metrics --> handler
    scan_metrics --> handler
    savings_gauge --> handler

    handler --> prom["Prometheus scraper"]
```

**Use `observability.MetricsHandler()`** to expose the endpoint, **not** `promhttp.Handler()`. The helper merges the package-private registry that holds `Global.*` with `prometheus.DefaultGatherer`. Wiring `promhttp.Handler()` directly only scrapes the default registry — every metric in `observability/` silently vanishes. (Caught on MR !85; the helper is now the single seam.)

Logs go through Go's standard `log/slog`, configured by `logging.Init(service)` (`services/shared/logging/logging.go`) called before any other init. Auto-attaches `service`, `env`, `version`, `commit_sha`. Use the global `slog` functions directly (`slog.Info("server started", "addr", addr)`) — never log raw secret values.

| Variable | Default | Notes |
|---|---|---|
| `DEV_MODE` | `false` | `true` → text log output |
| `LOG_OUTPUT` | `json` | `text` for human-readable output without full dev mode |
| `LOG_LEVEL` | `info` | `debug` \| `info` \| `warn` \| `error` |

---

## 9. CI/CD

GitHub Actions. Workflow defined in `.github/workflows/ci.yml`.

```mermaid
flowchart LR
    push[push / PR] --> test["test-unit, test-redis,<br/>test-dashboard, test-storage,<br/>lint"]
    test --> integ["test-integration-api,<br/>test-integration-ingestion"]
    integ --> build["build-production-shape<br/>(go build -tags production)"]
    build --> images["build-images<br/>(push to GHCR)"]
    images --> e2e["e2e-regression<br/>(Playwright, non-blocking)"]
```

CI covers test, build, and e2e only — there is no deploy stage in this repo's
workflow. Deploying to your own infrastructure (Kubernetes via
[`deploy/helm/`](../deploy/helm/axiaops/), AWS via [`terraform/`](../terraform/), or
plain `docker compose`) is a separate step you run yourself; see
[OPERATIONS.md](OPERATIONS.md) and each path's own README.

**Production-build smoke test** — `make build-production` runs `go build -tags
production` in CI. If a new feature reads `os.Getenv("DEV_MODE")` directly instead
of going through `cmd/devmode_*.go`, the regression-pin tests in
`cmd/devmode_{dev,production}_test.go` catch it in <30s.

---

## 10. Security Posture

- **Secrets at rest** — AWS account secrets are AES-256-GCM encrypted with `ENCRYPTION_KEY` (32-byte hex) before DB storage. Never commit `.env` or credentials.
- **Multi-tenancy** — RLS-enforced at the DB level. Application bug ≠ tenant data leak (the DB refuses to return out-of-tenant rows).
- **Production IAM** — use roles, not access keys. Customer-onboarding cross-account-role flow documented in [OPERATIONS.md § 1](OPERATIONS.md).
- **Build-tag hardening** — `production` build tag strips DEV_MODE from customer binaries. The lint job `test:lint:no-direct-devmode` rejects any new `os.Getenv("DEV_MODE")` reads outside `cmd/devmode_*.go`.
- **Native auth** — argon2id password hashes; cookie sessions; per-connection OIDC JWKS for SSO; rate limits on login + select-org sharing one budget so an attacker can't double the cap.
- **Audit log** — every privileged mutation writes a row via the `audit` helper in `services/api/internal/audit/`. Right-to-erasure paths anonymise audit_log instead of dropping it.

---

## 11. Where to find things

| Looking for... | Start at |
|---|---|
| API endpoint registration | `services/api/internal/api/handler.go` (Register method) |
| Auth middleware composition | `services/api/internal/middleware/auth.go` + `auth_native.go` |
| Native session minting | `services/api/internal/auth/` (NativeProvider, SessionManager) |
| OIDC ceremony | `services/api/internal/sso/` (initiate + callback) |
| Scan trigger from API | `services/api/internal/api/handler.go` → `scanAccount` handler + scan-lock map |
| AWS Cost Explorer client | `services/ingestion/internal/provider/aws/costs.go` |
| AWS CloudWatch client | `services/ingestion/internal/provider/aws/cloudwatch.go` |
| Per-service Describe calls | `services/ingestion/internal/provider/aws/discover_*.go` |
| Detection rules table | `services/shared/analyzer/detector.go` (`serviceRules`) |
| Domain types | `services/shared/model/` |
| Store interface | `services/shared/storage/storage.go` |
| Postgres impl | `services/shared/storage/postgres/postgres.go` |
| Migrations | `services/shared/storage/postgres/migrations/` |
| Observability helpers | `services/shared/observability/` |
| Theme tokens (dashboard) | `services/dashboard/src/theme/ThemeContext.jsx` |
| Dashboard route shell | `services/dashboard/src/components/AppShell.jsx` |
| Dashboard API client | `services/dashboard/src/api/client.js` |

---

## 12. Glossary

| Term | Meaning |
|---|---|
| **Tier 1 / Tier 2** | Detection method — Tier 1 is API-only; Tier 2 needs CloudWatch metrics. |
| **Zombie / Idle resource** | A cloud resource that's billing but unused per the threshold table in CLAUDE.md. |
| **Scan** | The full pipeline: fetch costs + usage, run detection, persist results, snapshot. |
| **Snapshot** | A dated aggregate row in `zombie_snapshots` written at scan-end; powers the trend chart. |
| **RLS** | Postgres Row-Level Security; the multi-tenancy enforcement mechanism. |
| **DEV_MODE** | Auth-bypass dev env. Used by `make start-dev` and the `deploy:dev-*` jobs. Stripped from `-tags production` binaries. |
| **start-dev** | `make start-dev` — host-mode Go services + Postgres container. |
| **start-staging** | `make start-staging` — full docker-compose stack with native auth on (DEV_MODE=false). |
| **Mummer** | The AWS-API mock used in integration tests; fixtures under `test-infra/mummer/`. |
| **Bootstrap** | The first-run install flow — single-use token mints the first owner + organization. Sealed forever after first success. |
