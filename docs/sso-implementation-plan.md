# SSO Implementation Plan — B1, B2, C

> Implementation roadmap for the native auth + SSO work designed in [`sso-integration-design.md`](sso-integration-design.md) and committed by [ADR-0001](decisions/0001-deployment-model.md). The design doc is the *why*; this is the *what to ship in what order*.
>
> **Status**: draft, 2026-04-30. Lives on the `feat/sso` integration branch.

---

## 1. Scope

| Phase | Deliverable | Effort |
|---|---|---|
| **B1** | Native email/password auth replacing Kinde | 4–6w |
| **B2** | Native OIDC RP — Entra + generic OIDC SSO | 4–6w |
| **C**  | Native SAML SP — Okta, ADFS, Keycloak SSO | 4–6w |

**Total**: 12–18 weeks single-developer. Sequential, not parallel — B2 depends on B1's `services/shared/jwks/` package and native session model; C depends on B2's `sso_*` schema.

**Out of scope** (explicitly):
- Social login (Google personal / GitHub OAuth)
- Magic-link login
- SMTP-driven invitations / forgot-password emails
- SCIM 2.0 provisioning (Phase E in the design doc — post-v1)
- The SaaS variant (`sso-integration-design-saas.md`) — preserved-but-not-scheduled
- 2FA / TOTP — delegated to the IdP under SSO; not added to native auth in v1

---

## 2. Decisions taken

These are settled before implementation starts. Flip explicitly via this doc if circumstances change.

| # | Decision | Rationale |
|---|---|---|
| D1 | **Strangler pattern** for Kinde removal. Both auth paths ship in B1; `AUTH_PROVIDER=native\|kinde` env var selects at startup. Native is default. | Reversible during the high-risk transition window; rollback is a config flip, not a redeploy. |
| D2 | **Hard deprecation date**: 2026-10-30 (B1 ship + 6 months). After that, the Kinde code path is deleted in a single PR. Tasks.md item dated for it. | Without a deletion date, the strangler turns into permanent dual-path. |
| D3 | **Token-based invitations, OOB delivery** — no SMTP. Admin POSTs an invite, gets a one-time URL containing the token, shares it via Slack/password manager/whatever. Invitee clicks → sets password → membership created. | No SMTP infrastructure required for self-hosted; matches GitLab/Mattermost/Outline self-hosted patterns. |
| D4 | **Admin-mediated password reset** for v1. No self-service "forgot password" page. Admin POSTs `/v1/users/{id}/password-reset`, gets a one-time URL, shares OOB. | No SMTP means no link to email; self-service requires SMTP or in-app secondary channel. Revisit when SMTP is added. |
| D5 | **First-owner bootstrap — GitLab-shaped install-token flow.** On first startup, if no organizations exist, the server generates a 32-byte hex install token, prints it to stdout, and writes it to `/var/run/axiaops/initial_setup_token`. Operator visits `/bootstrap`, supplies the token plus their own email/name/password via POST form (token in body, never in URL). Single-use; consumed and wiped from memory + disk after redemption. Endpoint returns 409 forever after. Optional `BOOTSTRAP_INSTALL_TOKEN` env var for unattended installs (suppresses log banner; same flow). | Server generates the entropy (operator doesn't have to know to run `openssl rand`). Token-in-POST-body avoids the URL leak channels (browser history, access logs, Referer headers). Matches the install-time UX of GitLab, Vault, Forgejo. The token gates the bootstrap *action* — it is **not** the user's password. The user picks their own password during the same form submission. |
| D6 | **Password hashing**: argon2id, defaults `time=3, memory=64MiB, parallelism=2, saltLen=16, keyLen=32`. | OWASP ASVS recommendation. bcrypt acceptable but argon2id is the modern default. |
| D7 | **Session storage**: PostgreSQL `sessions` table is the source of truth. Read path wrapped by the existing `services/shared/cache/` abstraction — Redis when `REDIS_URL` is set (production / staging), in-memory cache otherwise. Write path always writes PG and invalidates the cache. | Production best practice (Architecture 3 — PG durable + Redis cache). Sub-ms reads when Redis is present; auth still works without Redis (degrades to PG SELECT per request). Self-hosted customers don't have to run Redis to use AxiaOps. Reuses existing cache machinery — no new infrastructure abstraction. |
| D8 | **DBs wiped on B1 cutover.** No migration of existing Kinde-authed users in dev/staging. | Confirmed by user; no production deployments exist yet. |
| D9 | **DEV_MODE preserved.** `DEV_MODE=true` continues to bypass auth and auto-login as `DEV_USER_ID` with owner role. | Dev ergonomics; no behavior change. |
| D10 | **Same RBAC model** — owner / admin / member / viewer roles unchanged. SSO/JIT never grants `owner` (per design doc §11.6). | Roles are a separate axis from auth provider. |

---

## 3. Branch strategy

```
develop                                          (current)
  └─ feat/sso                                    (this branch — long-lived integration)
       ├─ feat/sso/b1-native-auth                (impl branch, MRs into feat/sso)
       ├─ feat/sso/b2-oidc                       (impl branch, blocked on b1 merge)
       └─ feat/sso/c-saml                        (impl branch, blocked on b2 merge)
```

- Each implementation branch MRs into `feat/sso`, **not** into `develop`.
- `feat/sso` MRs into `develop` only when **all three phases pass acceptance criteria** (§4.5, §5.5, §6.5).
- `feat/sso` is rebased on `develop` at the start of each sub-phase to absorb unrelated `develop` progress.
- This plan doc lives on `feat/sso`; updates land via direct commits or impl-branch MRs as the plan tightens.

---

## 4. Phase B1 — Native auth replacement

### 4.1 Migrations

`services/shared/storage/postgres/migrations/021_native_auth.up.sql`

```sql
SET search_path TO axiaops;

-- ── users: native-auth columns ──────────────────────────────────────────────
ALTER TABLE users
    ADD COLUMN password_hash       TEXT        NOT NULL DEFAULT '',
    ADD COLUMN password_set_at     TIMESTAMPTZ,
    ADD COLUMN email_lower         TEXT        GENERATED ALWAYS AS (lower(email)) STORED;

CREATE UNIQUE INDEX users_email_lower_unique ON users (email_lower);

-- ── sessions ────────────────────────────────────────────────────────────────
-- Server-side session store. Cookie carries an opaque session_id;
-- session_token_hash is the SHA-256 of the random token in the cookie.
CREATE TABLE sessions (
    id                  TEXT        PRIMARY KEY,
    user_id             TEXT        NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    organization_id     TEXT        NOT NULL REFERENCES organizations(id) ON DELETE CASCADE,
    auth_mode           TEXT        NOT NULL CHECK (auth_mode IN ('password','sso','bootstrap')),
    session_token_hash  TEXT        NOT NULL UNIQUE,
    created_at          TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    expires_at          TIMESTAMPTZ NOT NULL,
    revoked_at          TIMESTAMPTZ,
    last_seen_at        TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    ip                  INET,
    user_agent_hash     TEXT
);
CREATE INDEX sessions_user_idx     ON sessions (user_id);
CREATE INDEX sessions_expires_idx  ON sessions (expires_at) WHERE revoked_at IS NULL;
CREATE INDEX sessions_token_idx    ON sessions (session_token_hash);

ALTER TABLE sessions ENABLE ROW LEVEL SECURITY;
CREATE POLICY sessions_org_isolation ON sessions
    USING (organization_id = current_setting('app.organization_id', true))
    WITH CHECK (organization_id = current_setting('app.organization_id', true));

GRANT SELECT, INSERT, UPDATE, DELETE ON sessions TO axiaops;

-- ── password_resets ─────────────────────────────────────────────────────────
-- Single-use, time-bounded reset tokens. Admin-mediated in v1 (D4).
CREATE TABLE password_resets (
    id                  TEXT        PRIMARY KEY,
    user_id             TEXT        NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    organization_id     TEXT        NOT NULL REFERENCES organizations(id) ON DELETE CASCADE,
    token_hash          TEXT        NOT NULL UNIQUE,
    issued_by_user_id   TEXT        REFERENCES users(id) ON DELETE SET NULL,
    expires_at          TIMESTAMPTZ NOT NULL,
    redeemed_at         TIMESTAMPTZ,
    created_at          TIMESTAMPTZ NOT NULL DEFAULT NOW()
);
CREATE INDEX password_resets_user_idx ON password_resets (user_id);
CREATE INDEX password_resets_expires_idx ON password_resets (expires_at) WHERE redeemed_at IS NULL;

ALTER TABLE password_resets ENABLE ROW LEVEL SECURITY;
CREATE POLICY password_resets_org_isolation ON password_resets
    USING (organization_id = current_setting('app.organization_id', true))
    WITH CHECK (organization_id = current_setting('app.organization_id', true));

GRANT SELECT, INSERT, UPDATE, DELETE ON password_resets TO axiaops;

-- ── pending_memberships: token-based redemption ────────────────────────────
-- Existing table (017_pending_memberships) keys on email for Kinde-redemption.
-- Adapt: add a token column so the invitee can redeem via /v1/auth/invitations/redeem
-- without first authenticating with Kinde.
ALTER TABLE pending_memberships
    ADD COLUMN invite_token_hash TEXT,
    ADD COLUMN expires_at        TIMESTAMPTZ;

-- Existing rows from Kinde-era are dropped (D8 — DBs wiped).
DELETE FROM pending_memberships;

ALTER TABLE pending_memberships
    ALTER COLUMN invite_token_hash SET NOT NULL,
    ALTER COLUMN expires_at        SET NOT NULL;

CREATE UNIQUE INDEX pending_memberships_token_idx
    ON pending_memberships (invite_token_hash);
```

Down migration drops `sessions`, `password_resets`, removes the new columns. Standard pattern.

### 4.2 Backend — services/api

**New files:**

| File | Responsibility |
|---|---|
| `services/api/internal/auth/password.go` | argon2id wrapper. `Hash(plaintext)`, `Verify(plaintext, hash)`. Policy: ≥12 chars; reject top-1000 common passwords list. |
| `services/api/internal/auth/session.go` | `MintSession(ctx, userID, orgID, authMode)` returns `(sessionID, plaintextToken)`. `ValidateSession(ctx, plaintextToken)` returns `(*Session, error)`. `RevokeSession(ctx, sessionID)`. `RevokeUserSessions(ctx, userID)`. TTL default 24h, configurable via `SESSION_TTL_HOURS`. **Cache-aside pattern via `services/shared/cache/`**: `ValidateSession` checks `cache.Get("session:"+tokenHash)` first; on miss, SELECTs from PG and populates the cache with TTL = remaining session lifetime. `RevokeSession` and `RevokeUserSessions` always do the PG write first then `cache.Delete(...)` — write-through invalidation. Cache failures are non-fatal; the read path falls through to PG and logs a `slog.Warn`. |
| `services/api/internal/auth/session_cache.go` | Thin wrapper that adapts `cache.Cache` for the session read path. Single key shape: `axiaops:session:<sha256(token)>`. Single value shape: serialized `model.Session`. No business logic — pure caching. Kept separate from `session.go` so the read-through behaviour can be unit-tested in isolation against an in-memory cache implementation. |
| `services/api/internal/auth/handler.go` | Routes below. Uses `Store`, `password`, `session`. |
| `services/api/internal/auth/cookie.go` | Cookie helpers — name `axiaops_session`, `HttpOnly`, `Secure` (in non-DEV_MODE), `SameSite=Lax`, path `/`. |
| `services/api/internal/middleware/auth_native.go` | Reads cookie, calls `auth.ValidateSession` (which goes through the cache layer), attaches user/org/role to request context. Middleware does **not** know whether the hit came from Redis or PG — that's invisible at this layer. |
| `services/api/internal/auth/bootstrap.go` | `POST /v1/auth/bootstrap` handler + the install-token generator that runs on service startup. Generator: on `cmd/main.go` boot, if `Store.CountOrganizations(ctx)==0` and no token is already present, mint 32 random bytes (hex-encoded), keep in process memory, print a banner to stdout (see §4.5), and write to `BOOTSTRAP_TOKEN_FILE_PATH` with mode `0600`. Handler: validates the POSTed token via `subtle.ConstantTimeCompare`, then creates org + user + owner membership + session in one tx, then wipes the token from memory + deletes the file. Returns 409 if any org already exists or no token was generated (e.g. service restarted after bootstrap). |

**Modified files:**

| File | Change |
|---|---|
| `services/api/internal/middleware/auth.go` | At startup, branch on `AUTH_PROVIDER` env var. If `native`, install `auth_native` middleware. If `kinde`, keep current Kinde JWT validation. Increment `axiaops_auth_provider_active{provider=...}` counter. |
| `services/api/internal/api/handler.go` | `/v1/me` returns `{user, org, role, auth_provider}` so frontend knows which auth path is active. |
| `services/api/cmd/main.go` | Wire `auth.Handler.Register(mux)`. On startup, call `bootstrap.GenerateInstallTokenIfNeeded(ctx, store)` — generates + prints + writes the token iff no orgs exist and `BOOTSTRAP_INSTALL_TOKEN` is unset. |
| `services/api/internal/kinde/invitations.go` | **Adapt**, do not delete (still used under `AUTH_PROVIDER=kinde`). The new invitation creation path (under `AUTH_PROVIDER=native`) lives in `auth/handler.go` — generates a token, writes `pending_memberships`, returns redemption URL in the API response. |

**New endpoints (all under `/v1/auth/`):**

| Method | Path | Auth | Purpose |
|---|---|---|---|
| `POST` | `/v1/auth/bootstrap` | none (gated by D5) | Create the very first org + owner. Body: `{email, password, name, token}` — token from server-generated banner / token file / `BOOTSTRAP_INSTALL_TOKEN` env. Server constant-time-compares the token, then creates org + user + owner + session in one tx, then wipes the token. Returns session cookie. 409 if no token was generated or an org already exists. |
| `POST` | `/v1/auth/login` | none (rate-limited) | Native email/password login. Body: `{email, password}`. Returns session cookie. |
| `POST` | `/v1/auth/logout` | session | Revoke the current session. |
| `POST` | `/v1/auth/invitations/redeem` | none (token-bound) | Body: `{token, password, name}`. Hashes token, looks up `pending_memberships`, creates user + membership, deletes pending row, mints session. |
| `POST` | `/v1/auth/password-reset/redeem` | none (token-bound) | Body: `{token, new_password}`. Hashes token, looks up `password_resets`, updates `users.password_hash`, marks token redeemed, optionally revokes other sessions for that user. |
| `POST` | `/v1/users/{id}/password-reset` | `users:manage` (admin/owner) | Generate a reset token. Returns `{redemption_url, expires_at}`. Token never stored plaintext. |

**Modified existing endpoints:**

- `POST /v1/invitations` — under `AUTH_PROVIDER=native`, generates a token instead of calling Kinde Mgmt API. Returns `{invitation_id, redemption_url, expires_at}`. The `redemption_url` is `https://{host}/accept-invite?token=<plaintext>`.

**Rate limiting** (existing ratelimit middleware):
- `/v1/auth/login` — 10/min/IP, 5/min per email.
- `/v1/auth/bootstrap` — 5/min/IP (effectively unused after first run).
- `/v1/auth/invitations/redeem` — 30/min/IP.
- `/v1/auth/password-reset/redeem` — 30/min/IP.

### 4.3 Backend — services/shared

**New files:**

| File | Responsibility |
|---|---|
| `services/shared/jwks/jwks.go` | JWKS fetch + cache, `keyfuncFromCache(ctx, issuer, jwksURL, cache)`. Built in B1 (consumed in B2). Even though B1 doesn't validate external JWTs, the package is created here so the alg-confusion mitigation lives in one place from day one (per design doc §11.3). |
| `services/shared/model/session.go` | `Session` struct. |

**Modified files:**

| File | Change |
|---|---|
| `services/shared/storage/storage.go` | Add `CreateSession`, `GetSessionByTokenHash`, `RevokeSession`, `RevokeUserSessions(userID)`, `CreateInvitation` (token-shaped), `RedeemInvitation`, `CreatePasswordReset`, `RedeemPasswordReset`, `CreateUserWithPassword`, `UpdateUserPassword`. |
| `services/shared/storage/postgres/postgres.go` | Implementations. All inside transactions; `defer tx.Rollback`. |
| `services/shared/model/audit.go` | Add `AuditActionUserPasswordChanged`, `AuditActionUserPasswordResetIssued`, `AuditActionUserPasswordResetRedeemed`, `AuditActionInvitationRedeemed` (already exists for Kinde flow — reuse). |
| `services/shared/model/user.go` | Add `PasswordHash`, `PasswordSetAt` fields. |

### 4.4 Frontend — services/dashboard

**New screens:**

| File | Purpose |
|---|---|
| `services/dashboard/src/screens/LoginScreen.jsx` | Replaces Kinde redirect. Email + password form. POSTs `/v1/auth/login`. On success, navigates to `/dashboard`. |
| `services/dashboard/src/screens/AcceptInviteScreen.jsx` | Mounted at `/accept-invite`. Reads `?token=...` from URL, prompts for password + name, POSTs `/v1/auth/invitations/redeem`. |
| `services/dashboard/src/screens/PasswordResetScreen.jsx` | Mounted at `/password-reset`. Reads token, prompts for new password, POSTs redeem. |
| `services/dashboard/src/screens/BootstrapScreen.jsx` | Mounted at `/bootstrap`. Renders form: email, name, password, token. Token is its own field with `type="text"` (not `password` — operators paste from logs and need to see what they pasted), monospace, with a "show/hide" toggle. POSTs `/v1/auth/bootstrap` with all four fields in the body. **Token is never in the URL** — landing on `/bootstrap` shows the form; the token lives only in form state and the POST body. On 409 (already bootstrapped) → redirect to `/login`. |

**Modified:**

| File | Change |
|---|---|
| `services/dashboard/src/api/auth.js` | Replace Kinde SDK calls with `fetch('/v1/auth/...')`. Cookies handle the session — no token storage in JS. |
| `services/dashboard/src/pages/settings/Team.jsx` | "Invite member" form: on success, show modal with copyable redemption URL + "share this link with the invitee" text. |
| `services/dashboard/src/pages/settings/Members.jsx` (or wherever member CRUD lives) | "Reset password" button per member → calls `/v1/users/{id}/password-reset`, shows modal with copyable reset URL. |
| `package.json` | Remove `@kinde-oss/kinde-auth-react` (or equivalent Kinde SDK). |

### 4.5 Strangler-pattern wiring

**New env vars:**

| Var | Default | Notes |
|---|---|---|
| `AUTH_PROVIDER` | `native` | `native\|kinde`. Selects the auth middleware at startup. |
| `BOOTSTRAP_INSTALL_TOKEN` | (unset) | **Optional override** for unattended installs (CI, k8s operator). When set, the server uses this value as the install token *instead of* generating a random one and skips the stdout banner. Same single-use semantics; the env var should be cleared from secret stores after first boot. |
| `BOOTSTRAP_TOKEN_FILE_PATH` | `/var/run/axiaops/initial_setup_token` | Where the auto-generated token is written on first startup. Mode `0600`. Deleted on first successful bootstrap. Set to empty string to disable the file (rely on stdout banner only). |
| `SESSION_TTL_HOURS` | `24` | Native session lifetime. |
| `INVITATION_TTL_DAYS` | `14` (existing) | Reused for token-based invitations. |
| `PASSWORD_RESET_TTL_HOURS` | `4` | Short — reset tokens are admin-issued and expected to be redeemed quickly. |

**Install-time banner** — printed to stdout on first startup if `Store.CountOrganizations(ctx)==0`:

```
╔══════════════════════════════════════════════════════════════════╗
║  AxiaOps first-run setup                                         ║
║                                                                  ║
║  Visit:   https://<your-host>/bootstrap                          ║
║  Token:   4f8a9b2c1d6e3f7a8b9c0d1e2f3a4b5c6d7e8f9a0b1c2d3e4f5a6  ║
║                                                                  ║
║  This token is single-use and grants creation of the first       ║
║  organization owner. It will not be shown again.                 ║
║                                                                  ║
║  Token also written to: /var/run/axiaops/initial_setup_token     ║
║  Delete it from logs and disk after first use.                   ║
╚══════════════════════════════════════════════════════════════════╝
```

The banner is suppressed when `BOOTSTRAP_INSTALL_TOKEN` is set (unattended install).

**Telemetry:**
- New Prometheus counter: `axiaops_auth_provider_active{provider="native\|kinde"}` — incremented on every authenticated request. Used to prove zero Kinde traffic before deletion (D2).
- New counter: `axiaops_auth_login_total{outcome="success\|failure", reason="bad_password\|unknown_user\|rate_limited\|locked"}`.
- New counter: `axiaops_auth_invitations_total{outcome="created\|redeemed\|expired"}`.

**Deprecation tracking:**
- New entry in `Tasks.md`: "**[DEPRECATION 2026-10-30]** Delete Kinde auth path." Stub MR drafted on B1 ship day; merges on the deprecation date if telemetry shows zero Kinde traffic.

### 4.6 Acceptance criteria — B1

Tick each before opening `feat/sso/b1-native-auth` MR:

- [ ] `make start-dev` boots with `AUTH_PROVIDER=native` and a fresh DB; the server prints the install-token banner to stdout; visiting `/bootstrap`, pasting the token + email/password, lands the operator in the dashboard as owner.
- [ ] Token is also present at `/var/run/axiaops/initial_setup_token` with mode `0600`; deleted after successful bootstrap.
- [ ] `BOOTSTRAP_INSTALL_TOKEN` env var override works for unattended install: banner suppressed, env value accepted by `/bootstrap`.
- [ ] `make start-staging` boots with `AUTH_PROVIDER=kinde` and existing Kinde tenant works unchanged.
- [ ] After first successful bootstrap, `/v1/auth/bootstrap` returns 409 across restarts (no token regeneration once an org exists).
- [ ] Token comparison uses `subtle.ConstantTimeCompare` (verified by code inspection + a unit test that submits a near-miss).
- [ ] Token never appears in the bootstrap URL — verified by an integration test that asserts `Location` and `Referer` headers do not contain the token.
- [ ] `POST /v1/invitations` returns a redemption URL; visiting it lets the invitee set a password and lands them in the org with the assigned role.
- [ ] `POST /v1/users/{id}/password-reset` returns a reset URL; visiting it updates the password and revokes other sessions for that user.
- [ ] `axiaops_auth_provider_active` counter increments correctly under both providers in test.
- [ ] `DEV_MODE=true` still bypasses auth and auto-logs in as `DEV_USER_ID`.
- [ ] All existing handler tests pass against `AUTH_PROVIDER=native` (no Kinde JWT in the test setup).
- [ ] Native auth path covered by black-box tests in `services/api/internal/auth/*_test.go`: signup, login, logout, invitation redemption, password reset, expired token rejection, single-use token enforcement, rate limiting.
- [ ] argon2id parameters verified via test (`time=3, memory=64MiB`).
- [ ] No plaintext passwords or tokens in logs (grep `slog` outputs in test).
- [ ] Session cache-aside path verified: integration test asserts (a) cold request hits PG, (b) warm request within TTL is served from cache without a PG roundtrip, (c) `RevokeSession` invalidates the cache so the next request misses, (d) `start-dev` works without `REDIS_URL` set (in-memory cache fallback).
- [ ] Redis outage simulation: with `REDIS_URL` set but Redis down, auth still works (degrades to PG SELECT per request) and `axiaops_session_cache_errors_total` counter increments.
- [ ] OpenAPI / API docs updated for the new `/v1/auth/*` routes.
- [ ] `Tasks.md` deprecation entry filed.

---

## 5. Phase B2 — Native OIDC RP

Builds on B1's `services/shared/jwks/` and native session model.

### 5.1 Migrations

`services/shared/storage/postgres/migrations/022_sso_core.up.sql`

Schema as specified in [`sso-integration-design.md` §4.1](sso-integration-design.md#41-migration-021_sso_coreupsql), bumped from 021 → 022 because B1 takes 021. Tables:

- `sso_connections`
- `sso_domains` (with partial unique index on `lower(domain) WHERE status='verified'`)
- `sso_group_mappings`
- `sso_assertion_replay` (created here even though it's only used in Phase C)
- Column adds: `users.sso_external_id`, `users.sso_connection_id`, `memberships.provisioned_via`

All RLS policies as in design doc §4.3.

### 5.2 Backend — services/api

**New files** (all in `services/api/internal/sso/`):

| File | Routes / responsibility |
|---|---|
| `handler.go` | CRUD: connections, domains, group mappings. Permission-checked per [design doc §5](sso-integration-design.md#5-api-surface). |
| `discover.go` | `GET /v1/sso/discover` with constant-shape response + ~5ms padding to mask DB lookup (per design doc §5.4). |
| `initiate.go` | `GET /v1/sso/oidc/{cid}/initiate` — builds IdP authorization URL with PKCE + opaque `state`. |
| `oidc.go` | OIDC RP core: discovery doc fetch + cache (`sso:oidc-discovery:{cid}`, 24h TTL), JWKS via shared `services/shared/jwks/`, ID-token validation. Algorithm-confusion mitigation in `Keyfunc` (per design doc §11.3). |
| `oidc_callback.go` | `GET /v1/sso/oidc/{cid}/callback` — exchanges code, validates token, calls `jit.go`, mints native session via `auth/session.go` with `auth_mode='sso'`. |
| `jit.go` | `JITResolveRole(connID, groups, defaultRole)` (table-driven test); `JITProvisionMembership(orgID, userID, role)` (single tx, idempotent). |
| `domain.go` | DNS TXT verification. Public-suffix-list rejection (`golang.org/x/net/publicsuffix`). |
| `test.go` | `POST /v1/sso/connections/{cid}/test` — synthetic auth probe, returns enum reason. |
| `sweep.go` | Ticker registered in `cmd/main.go` alongside the existing stuck-scan ticker. 24h interval. Three queries: replay expiry, domain expiry, SAML cert previous-cert sunset (last query is a no-op until Phase C). |
| `permission_matrix_test.go` | Asserts owner role can never be set via JIT. |

**New permissions** in `services/shared/authz/roles.go`:

```go
PermSSORead         Permission = "sso:read"          // viewer+
PermSSOManage       Permission = "sso:manage"        // owner only
PermSSODomainVerify Permission = "sso:domain_verify" // owner only
```

**Audit constants** in `services/shared/model/audit.go` — 12 new actions per design doc §4.5.

**Modified files:**

| File | Change |
|---|---|
| `services/api/cmd/main.go` | Register `sso.Handler.Register(mux)`; start `ssoSweep` ticker. |
| `services/api/internal/middleware/auth_native.go` | Enforcement check: if user's org has `enforcement='required'` and `session.auth_mode != 'sso'`, return 403 `{"error":"sso_required"}`. |
| `services/api/internal/api/handler.go` | `/v1/me` returns `has_sso` and `enforcement_mode`. |

### 5.3 Frontend

| File | Purpose |
|---|---|
| `services/dashboard/src/pages/settings/SSO.jsx` | Container: tabs for Connections / Domains / Group Mappings / Enforcement. Visible only to owners. |
| `services/dashboard/src/pages/settings/sso/Connections.jsx` | List + add/edit/delete. Wizard: protocol → metadata → label → save as draft. |
| `services/dashboard/src/pages/settings/sso/Domains.jsx` | Verify-domain flow: shows TXT record + copy button + Verify button. |
| `services/dashboard/src/pages/settings/sso/GroupMappings.jsx` | Table editor; PUT replaces full set. |
| `services/dashboard/src/pages/settings/sso/Enforcement.jsx` | Radio: optional/preferred/required. Required confirms with "have you tested SSO recently?" guard surfaced from server 409. |
| `services/dashboard/src/screens/LoginScreen.jsx` (modify) | Email-blur calls `/v1/sso/discover`; if `has_sso=true`, redirects to `redirect_url`. Otherwise reveals password field. |

### 5.4 Test infrastructure

- Add `mockoidc` (or `oauth2-proxy/mockoidc`) container to `docker-compose.test.yml`.
- Integration test in `services/api/internal/sso/oidc_integration_test.go`: full code-flow round-trip against mockoidc.
- Unit tests in `oidc_test.go`: synthetic ID tokens with valid + expired + wrong-issuer + wrong-audience + `alg=none` + `alg=HS256` (must reject all four invalid cases).
- `domain_test.go`: mock DNS resolver, public-suffix-list rejection.
- `jit_test.go`: table-driven role precedence, owner-never-via-JIT.

### 5.5 Acceptance criteria — B2

- [ ] Migration 022 applies cleanly on a wiped DB and rolls back cleanly.
- [ ] Owner can create an OIDC connection, verify a domain, configure group mappings, and set enforcement = `optional`.
- [ ] Mock-OIDC integration test passes end-to-end (login → JIT → membership row).
- [ ] Internal Entra OIDC test (against AxiaOps Inc's own Entra tenant) passes from a `start-staging` deployment.
- [ ] Group mapping precedence (`admin > member > viewer`) verified by table-driven test.
- [ ] Owner cannot be assigned via JIT (asserted by `permission_matrix_test.go`).
- [ ] `/v1/sso/discover` returns 200 with `has_sso:false` for unknown domains; constant response shape verified.
- [ ] `enforcement=required` blocks native-password sessions for the org with 403.
- [ ] `pending_memberships` invitation takes precedence over JIT (per design doc §10.4) — covered by integration test.
- [ ] `ssoSweep` ticker runs and logs sweep count > 0 after seeded expired rows.

---

## 6. Phase C — Native SAML SP

Adds SAML support on top of B2's schema. No new tables — `sso_assertion_replay` already exists.

### 6.1 Dependencies

```
go get github.com/crewjam/saml@latest
```

Pin to a specific version; review release notes for any open advisories before merge.

### 6.2 Backend — services/api

**New files** (all in `services/api/internal/sso/`):

| File | Routes / responsibility |
|---|---|
| `saml.go` | SP setup. Loads `SSO_SP_PRIVATE_KEY_PEM` + `SSO_SP_CERT_PEM` env vars at startup. Generates `crewjam/saml` `ServiceProvider` per connection. |
| `saml_metadata.go` | `GET /v1/sso/saml/{cid}/metadata` — publishes SP metadata XML for the IdP admin to import. |
| `saml_initiate.go` | `GET /v1/sso/saml/{cid}/initiate` — builds `SAMLAuthRequest`, mints CSRF-bound `RelayState`, redirects to IdP. |
| `saml_acs.go` | `POST /v1/sso/saml/{cid}/acs` — verifies signature against `saml_signing_cert` (or `saml_previous_cert` during rotation), validates `NotBefore`/`NotOnOrAfter` (60s skew), checks audience and recipient, calls `replay.go`, extracts NameID + email + group attribute, calls `jit.go`, mints native session. |
| `replay.go` | `WasReplayed(ctx, assertionID, expiresAt)`. PG-backed initially (uses `sso_assertion_replay` from B2 migration). |

**Modified:**

| File | Change |
|---|---|
| `services/api/internal/sso/handler.go` | SAML connection CRUD adds `saml_sso_url`, `saml_signing_cert`, `saml_previous_cert` fields. Cert validation on save (PEM parse + key type check). |
| `services/api/internal/sso/sweep.go` | Adds query: `UPDATE sso_connections SET saml_previous_cert = '' WHERE saml_previous_cert_expires_at < NOW()`. |
| `services/api/internal/sso/handler.go` | "Test connection" (`POST /v1/sso/connections/{cid}/test`) for SAML synthesizes an unsigned probe and reports back what fails (signature, metadata-unreachable, etc.) — no real session created. |
| `services/api/cmd/main.go` | Wire SAML routes. |

### 6.3 Frontend

- `services/dashboard/src/pages/settings/sso/Connections.jsx` (modify) — wizard adds SAML branch: paste IdP metadata XML or URL, label, save as draft.
- "Download SP metadata" button per SAML connection that downloads the `/v1/sso/saml/{cid}/metadata` payload.
- "Rotate signing cert" UX: paste new cert; old moves to `previous_cert` with 30-day overlap.

### 6.4 SP keypair lifecycle

- One SP keypair per deployment environment, **not** per org. Stored in env vars `SSO_SP_PRIVATE_KEY_PEM` + `SSO_SP_CERT_PEM`.
- Generated at deploy time: `openssl req -x509 -newkey rsa:2048 -nodes -days 365 ...` — runbook in `docs/sso-key-rotation.md`.
- Annual rotation cadence; rotation is a deployment event (env var update + restart), not a per-org action.

### 6.5 Pen-test gating

**Hard gate** before any external pilot — see [`sso-integration-design.md` §13.4](sso-integration-design.md#134-pen-test-surface):
- [ ] `xml-attacker` corpus run against `/v1/sso/saml/{cid}/acs` — all attempts rejected with `signature_invalid` or `assertion_replayed`.
- [ ] `dnstwist`-style domain-confusion test on `/v1/sso/discover` — no false positives on similar domains.
- [ ] Open-redirect fuzzing on `RelayState` and OIDC `state` — no redirect honoured outside the fixed `/dashboard` path.
- [ ] Multi-assertion SAML responses rejected.
- [ ] XXE attempts rejected (XML parser hardening verified by test).

### 6.6 Acceptance criteria — C

- [ ] `samltest.id` corpus passes — basic + signed + group-claim assertions.
- [ ] One real customer's Okta-SAML works end-to-end in `start-staging`.
- [ ] SP metadata endpoint produces valid XML that Okta/ADFS/Keycloak can ingest.
- [ ] Cert rotation: new cert added, old retained for 30 days, both verify; sweep purges old after 30 days (verified by test with manipulated `expires_at`).
- [ ] §6.5 pen-test gate cleared.
- [ ] Replay protection verified: identical assertion submitted twice → second rejected with `assertion_replayed`.
- [ ] Clock skew: assertions with `NotBefore = NOW + 65s` rejected; with `NotBefore = NOW + 30s` accepted.
- [ ] All SAML failure modes log a SHA-256 of the assertion ID, not the assertion body.

---

## 7. Cross-cutting concerns

### 7.1 Logging discipline

Across all three phases:
- **Never** log: passwords (plain or hashed), session tokens (plain or hashed), invitation tokens, password-reset tokens, SAML assertion bodies, OIDC `id_token` bodies, `client_secret`, IdP signing cert private keys.
- **Always** log: action type, outcome, user ID (if known), org ID, IdP issuer (for SSO), error category enum.
- **Hash before logging**: assertion IDs (SHA-256), session token prefixes for correlation (first 4 chars only).
- Test: a unit test runs every auth/SSO path with a `slog` capture and grep-asserts no banned tokens appear.

### 7.2 Observability

Add to `services/shared/observability/`:

```go
AuthLoginTotal          prometheus.CounterVec   // outcome, reason
AuthSessionsActive      prometheus.GaugeVec     // org_id (cardinality cap: 1000)
AuthInvitationsTotal    prometheus.CounterVec   // outcome
SessionCacheTotal       prometheus.CounterVec   // outcome (hit|miss|error) — cache-aside health
SessionCacheErrorsTotal prometheus.Counter      // backend errors (Redis down, etc.) — drives degradation alerts
SSOLoginTotal           prometheus.CounterVec   // outcome, reason, protocol
SSOJITProvisioned       prometheus.CounterVec   // role, source_group_match
AuthProviderActive      prometheus.CounterVec   // provider (native|kinde) — strangler telemetry
```

### 7.3 Docs to update

| Doc | Change |
|---|---|
| `services/api/CLAUDE.md` | Endpoint table — replace Kinde-specific notes with native auth + SSO. Env var section rewrite. |
| `services/shared/CLAUDE.md` | Tables list adds `sessions`, `password_resets`, `sso_*`. |
| `CLAUDE.md` (root) | "Security" section: AES-256-GCM line stays; replace Kinde JWT line with "argon2id password hashing + native session cookies." |
| `Tasks.md` | Add Phase B1/B2/C entries with acceptance criteria pointers; add deprecation entry. |
| `docs/auth.md` | Mark as historical (Kinde evaluation predates ADR-0001). |
| `docs/auth_flow.md` | Rewrite for native + SSO flows. |
| `docs/invitation-flow.md` | Add the token-based redemption path; keep the Kinde path for the strangler window. |
| `docs/sso-integration-design.md` | Cross-reference this plan as the implementation roadmap. |

### 7.4 Env vars summary

| Var | Phase | Default | Notes |
|---|---|---|---|
| `AUTH_PROVIDER` | B1 | `native` | Strangler toggle. |
| `BOOTSTRAP_INSTALL_TOKEN` | B1 | (unset) | Optional override for unattended installs. |
| `BOOTSTRAP_TOKEN_FILE_PATH` | B1 | `/var/run/axiaops/initial_setup_token` | Where the auto-generated token is written. Set empty to disable file. |
| `SESSION_TTL_HOURS` | B1 | `24` | |
| `REDIS_URL` | B1 | (existing) | When set, session reads use Redis cache via `services/shared/cache/`. When unset, in-memory cache fallback. PG remains the source of truth either way. |
| `INVITATION_TTL_DAYS` | B1 | `14` | Existing var, reused. |
| `PASSWORD_RESET_TTL_HOURS` | B1 | `4` | |
| `SSO_SP_PRIVATE_KEY_PEM` | C | — | Required when any SAML connection is active. |
| `SSO_SP_CERT_PEM` | C | — | ditto. |
| `KINDE_*` | (deprecated) | — | Required only while `AUTH_PROVIDER=kinde`. Removed at deprecation date. |

---

## 8. Implementation branch playbook

For each impl branch:

1. `git checkout feat/sso && git pull` — start from latest integration branch.
2. `git checkout -b feat/sso/<phase>-<scope>` — branch off.
3. Run `make test` before first commit to confirm baseline.
4. Land incremental commits; rebase on `feat/sso` periodically to absorb peer impl branches.
5. Open MR targeting `feat/sso` (not `develop`).
6. Self-review against the acceptance criteria checklist for that phase.
7. Run `make test-all` + `make test-integration` before requesting review.
8. After merge into `feat/sso`: rebase the next phase's impl branch.

When all three phases land in `feat/sso`:
- Final integration test pass on `feat/sso`.
- Update Tasks.md with the deprecation date entry.
- Single MR `feat/sso` → `develop` with all three phases.

---

## 9. Open questions

These are **not blockers for B1**. Resolve at the boundaries indicated.

| # | Question | Decide by |
|---|---|---|
| Q1 | Self-service forgot-password page — should we add an in-app secondary channel (e.g. "show the reset URL on the admin's screen on next login")? Or stay admin-mediated? | B1 ship. Default per D4: admin-mediated. |
| Q2 | Admin self-serve vs gated SSO onboarding (design doc §15 Q3). Affects `services/dashboard/src/pages/settings/SSO.jsx` scope. | Before B2 starts. |
| Q3 | First IdP design partner — Entra (B2 first) or Okta SAML (C jumps ahead)? | Before B2 starts. |
| Q4 | At deprecation date (2026-10-30), keep the empty `services/api/internal/kinde/` directory as a placeholder for future SaaS reactivation, or delete entirely (relying on git history)? | Deprecation date. |
| Q5 | Magic-link login as a Phase D fast-follow if customer demand surfaces? Out of scope for v1 SSO; tracked here for visibility. | Post-v1. |

---

## 10. Risks

| Risk | Mitigation |
|---|---|
| Strangler period extends past deprecation date because dogfood didn't migrate. | Calendar reminder on day 150; require dogfood on `AUTH_PROVIDER=native` by day 30. |
| `crewjam/saml` CVE during Phase C development. | Pin to latest tagged release; subscribe to crewjam's security advisories before merge. |
| Native session cookie security regression (e.g. missing `Secure` flag, wrong `SameSite`). | Black-box test asserts cookie attributes. Browser-based smoke test in Phase B1 acceptance. |
| Stale cache after revocation — user keeps access after `RevokeSession`. | Write-through invalidation: revocation paths always do PG write *then* `cache.Delete`. Integration test in B1 acceptance verifies the next request after revocation misses the cache and sees `revoked_at != NULL`. Cache TTL is also bounded by remaining session lifetime as a backstop. |
| Redis outage degrades performance silently. | Cache errors increment `axiaops_session_cache_errors_total` and log at `slog.Warn`. Alert rule fires when error rate > 1/min for 5 min. Auth still works on PG-only path during the outage. |
| First-owner bootstrap endpoint left enabled accidentally (e.g. token file not deleted, `BOOTSTRAP_INSTALL_TOKEN` left set in prod). | Endpoint is sealed by `Store.CountOrganizations(ctx) > 0` — once any org exists, no token re-grants access. Token file is deleted on successful bootstrap. Endpoint logs WARN every time it's invoked. Metric counter `axiaops_bootstrap_attempts_total{outcome=...}` for monitoring. Runbook says to unset `BOOTSTRAP_INSTALL_TOKEN` from secret stores after first boot and verify the token file is gone. |
| Install token leaked via stdout / log aggregator (CI logs forwarded to a third-party log service). | Token is single-use and the bootstrap endpoint is sealed after first claim. Operators with strict log-handling requirements can set `BOOTSTRAP_INSTALL_TOKEN` themselves to suppress the stdout banner. Banner explicitly says "delete from logs after first use." |
| Operator pastes the token URL or token into Slack / chat. | Token is in the form body, never in the URL — no copyable URL contains the secret. The banner shows the token as a separate line item, easier to redact from screenshots. |
| OIDC discovery doc TTL too long causes stale JWKS after IdP key rotation. | 24h TTL is a compromise. On signature-verification failure, force a single re-fetch before rejecting. Documented in B2 acceptance. |
| SAML SP private key compromised. | Key rotation runbook in `docs/sso-key-rotation.md`. Annual rotation cadence; emergency rotation = deploy with new env var values. |
| Token-based invitation URL leaked (e.g. admin pastes into a public Slack channel). | Tokens are single-use + 14-day TTL. Redemption invalidates. Audit log records redemption + the user-agent IP. Document recommended sharing channels in the admin UI. |

---

## 11. Out of scope (re-stated for clarity)

These were considered and explicitly excluded from B1+B2+C. Tracked in this list so future contributors don't reopen them without an ADR:

- **SMTP-driven invitations / forgot-password** — explicitly deferred (D3, D4).
- **Social login** — Kinde was providing this as a side benefit; we drop it in the native rebuild.
- **Magic-link login** — would require SMTP.
- **2FA / TOTP for native auth** — delegated to the IdP under SSO. Native auth has no second factor in v1; revisit if a customer demands it.
- **SCIM 2.0 provisioning** — Phase E in design doc.
- **WS-Federation** — design doc §2.2 non-goal.
- **IdP-initiated SAML SLO** — design doc §7.6 defer.
- **B2C / consumer IdPs** (Sign in with Apple, Google personal) — AxiaOps is B2B.
- **SaaS variant** — preserved as design-only in `sso-integration-design-saas.md`; reactivate per ADR-0001 review triggers.
