# SSO Implementation Plan — B1, B2, C

> Implementation roadmap for the native auth + SSO work designed in [`sso-integration-design.md`](sso-integration-design.md) and committed by [ADR-0001](decisions/0001-deployment-model.md). The design doc is the *why*; this is the *what to ship in what order*.
>
> **Status**: draft, 2026-04-30. Lives on the `feat/sso` integration branch.

---

## 1. Scope

| Phase | Deliverable | Effort |
|---|---|---|
| **B1** | Native email/password auth replacing Kinde (single-org-per-session constraint) | 4–6w |
| **B1.5** | Multi-org access — login org-picker + live org switcher (§4.7) | 1w |
| **B2** | Native OIDC RP — Entra + generic OIDC SSO | 4–6w |
| **C**  | Native SAML SP — Okta, ADFS, Keycloak SSO | 4–6w |

**Total**: 13–19 weeks single-developer. Sequential, not parallel — B1.5 depends on B1's session model; B2 depends on B1.5's org-switcher endpoints (so OIDC JIT can mint sessions for the right active org); C depends on B2's `sso_*` schema.

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
       ├─ feat/sso/b1.5-multi-org                (impl branch, blocked on b1 merge — see §4.7)
       ├─ feat/sso/b2-oidc                       (impl branch, blocked on b1.5 merge)
       └─ feat/sso/c-saml                        (impl branch, blocked on b2 merge)
```

- Each implementation branch MRs into `feat/sso`, **not** into `develop`.
- `feat/sso` MRs into `develop` only when **all four phases pass acceptance criteria** (§4.6, §4.7, §5.5, §6.6).
- `feat/sso` is rebased on `develop` at the start of each sub-phase to absorb unrelated `develop` progress.
- This plan doc lives on `feat/sso`; updates land via direct commits or impl-branch MRs as the plan tightens.

---

## 4. Phase B1 — Native auth replacement

### 4.1 Migrations

`services/shared/storage/postgres/migrations/021_native_auth.up.sql`

```sql
SET search_path TO axiaops;

-- ── users: native-auth columns ──────────────────────────────────────────────
-- Self-hosted v1 (per ADR-0001) deploys one stack per customer, so global
-- email uniqueness is the desired property — a user with a given email exists
-- in exactly one AxiaOps installation. Re-evaluate this constraint if
-- multi-tenant SaaS is reactivated per ADR-0001's review triggers.
ALTER TABLE users
    ADD COLUMN password_hash       TEXT        NOT NULL DEFAULT '',
    ADD COLUMN password_set_at     TIMESTAMPTZ,
    ADD COLUMN email_lower         TEXT        GENERATED ALWAYS AS (lower(email)) STORED;

CREATE UNIQUE INDEX users_email_lower_unique ON users (email_lower);

-- ── sessions ────────────────────────────────────────────────────────────────
-- Server-side session store. Cookie carries an opaque session_id;
-- session_token_hash is the SHA-256 of the random token in the cookie.
--
-- IMPORTANT — RLS is intentionally NOT enabled on this table.
--   1. Session lookup happens BEFORE the request has any organization context
--      (it is the lookup that *establishes* the org context). RLS on this
--      table would block every authenticated request because
--      app.organization_id is unset at lookup time.
--   2. The session_token_hash is itself a capability — possession of the
--      plaintext token proves authorisation. RLS adds no security on top.
--   3. Cross-org leakage is impossible because the SELECT clause is
--      `WHERE session_token_hash = $1` — the caller must already know the
--      (cryptographically-random) token to retrieve the row.
-- Session middleware sets app.organization_id from sessions.organization_id
-- AFTER lookup, before any subsequent handler-level query runs.
CREATE TABLE sessions (
    id                  TEXT        PRIMARY KEY,
    user_id             TEXT        NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    organization_id     TEXT        NOT NULL REFERENCES organizations(id) ON DELETE CASCADE,
    auth_mode           TEXT        NOT NULL CHECK (auth_mode IN ('password','sso','bootstrap')),
    session_token_hash  TEXT        NOT NULL UNIQUE,           -- UNIQUE creates the lookup index; no separate index needed
    created_at          TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    expires_at          TIMESTAMPTZ NOT NULL,
    revoked_at          TIMESTAMPTZ,
    last_seen_at        TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    ip                  INET,
    user_agent_hash     TEXT
);
CREATE INDEX sessions_user_idx     ON sessions (user_id);
CREATE INDEX sessions_expires_idx  ON sessions (expires_at) WHERE revoked_at IS NULL;
-- (no sessions_token_idx — the UNIQUE constraint on session_token_hash creates the index)

GRANT SELECT, INSERT, UPDATE, DELETE ON sessions TO axiaops;

-- ── password_resets ─────────────────────────────────────────────────────────
-- Single-use, time-bounded reset tokens. Admin-mediated in v1 (D4).
-- RLS NOT enabled — same capability-based reasoning as `sessions`. The
-- redeem endpoint looks up by token_hash before any org context exists.
CREATE TABLE password_resets (
    id                  TEXT        PRIMARY KEY,
    user_id             TEXT        NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    organization_id     TEXT        NOT NULL REFERENCES organizations(id) ON DELETE CASCADE,
    token_hash          TEXT        NOT NULL UNIQUE,           -- UNIQUE creates the index
    issued_by_user_id   TEXT        REFERENCES users(id) ON DELETE SET NULL,
    expires_at          TIMESTAMPTZ NOT NULL,
    redeemed_at         TIMESTAMPTZ,
    created_at          TIMESTAMPTZ NOT NULL DEFAULT NOW()
);
CREATE INDEX password_resets_user_idx     ON password_resets (user_id);
CREATE INDEX password_resets_expires_idx  ON password_resets (expires_at) WHERE redeemed_at IS NULL;

GRANT SELECT, INSERT, UPDATE, DELETE ON password_resets TO axiaops;

-- ── bootstrap_state ─────────────────────────────────────────────────────────
-- Multi-replica-safe coordination for the first-owner bootstrap flow (D5).
-- A single row is inserted by exactly one replica (PG advisory lock + ON
-- CONFLICT DO NOTHING). The row holds the install-token hash and is deleted
-- on successful bootstrap. After deletion, organizations table has ≥1 row
-- and the bootstrap endpoint is sealed forever.
--
-- Why a table and not just a sentinel file: replicas don't share filesystems
-- in containerised deployments, and we need exactly-once token generation
-- across the cluster. PG is the only shared coordination point AxiaOps has.
CREATE TABLE bootstrap_state (
    id                  TEXT        PRIMARY KEY DEFAULT 'singleton'
                                    CHECK (id = 'singleton'),  -- enforces ≤1 row
    token_hash          TEXT        NOT NULL,
    created_at          TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    minted_by_pod       TEXT                                    -- HOSTNAME of the replica that won the race; informational
);

GRANT SELECT, INSERT, DELETE ON bootstrap_state TO axiaops;

-- ── pending_memberships: token-based redemption ────────────────────────────
-- Existing table (017_pending_memberships) keys on email for Kinde-redemption.
-- Adapt: add a token column so the invitee can redeem via /v1/auth/invitations/redeem
-- without first authenticating with Kinde.
--
-- Migration is reversible per-environment (S2 architect finding):
--   - New columns are NULLABLE initially.
--   - Existing Kinde-era rows get expires_at backfilled from
--     created_at + INVITATION_TTL_DAYS; invite_token_hash stays NULL,
--     marking them as "Kinde-mode" (only redeemable via Kinde callback).
--   - A CHECK constraint enforces that any row created under native auth
--     MUST have invite_token_hash set; Kinde-mode rows are grandfathered.
--   - The NOT NULL tightening + Kinde-mode row deletion happens in a later
--     migration at the Kinde deprecation date (D2 — 2026-10-30).
-- RLS NOT enabled — capability-based lookup; same reasoning as `sessions`.
ALTER TABLE pending_memberships
    ADD COLUMN invite_token_hash TEXT,
    ADD COLUMN expires_at        TIMESTAMPTZ;

UPDATE pending_memberships
   SET expires_at = created_at + (current_setting('app.invitation_ttl_days', true)::int || ' days')::interval
 WHERE expires_at IS NULL;
-- Fallback if app.invitation_ttl_days is unset during migration:
UPDATE pending_memberships SET expires_at = created_at + INTERVAL '14 days'
 WHERE expires_at IS NULL;

ALTER TABLE pending_memberships
    ALTER COLUMN expires_at SET NOT NULL,
    ADD CONSTRAINT pending_memberships_native_token_required
        CHECK (
            -- Either a Kinde-mode row (no token, redeemed via Kinde callback)
            -- or a native-mode row (token present)
            invite_token_hash IS NULL OR length(invite_token_hash) > 0
        );

CREATE UNIQUE INDEX pending_memberships_token_idx
    ON pending_memberships (invite_token_hash)
    WHERE invite_token_hash IS NOT NULL;
```

Down migration drops `bootstrap_state`, `password_resets`, `sessions`, removes the new columns from `users` and `pending_memberships`. Standard pattern.

A **migration 023** at the Kinde deprecation date (D2 — 2026-10-30) tightens `pending_memberships`:

```sql
-- 023_pending_memberships_native_only.up.sql
DELETE FROM pending_memberships WHERE invite_token_hash IS NULL; -- drop Kinde-mode rows
ALTER TABLE pending_memberships ALTER COLUMN invite_token_hash SET NOT NULL;
ALTER TABLE pending_memberships DROP CONSTRAINT pending_memberships_native_token_required;
```

This is intentionally a separate migration so the cutover is reversible per-environment (S2 architect finding).

### 4.2 Backend — services/api

**New files:**

| File | Responsibility |
|---|---|
| `services/api/internal/auth/password.go` | argon2id wrapper. `Hash(plaintext)`, `Verify(plaintext, hash)`. Policy: ≥12 chars; reject top-1000 common passwords list. |
| `services/api/internal/auth/session.go` | `MintSession(ctx, userID, orgID, authMode)` returns `(sessionID, plaintextToken)`. `ValidateSession(ctx, plaintextToken)` returns `(*Session, error)`. `RevokeSession(ctx, sessionID)`. `RevokeUserSessions(ctx, userID)`. TTL default 24h, configurable via `SESSION_TTL_HOURS`. **Cache-aside pattern via `services/shared/cache/`**: `ValidateSession` checks `cache.Get("axiaops:session:"+tokenHash)` first; on miss, SELECTs from PG and populates the cache with TTL = remaining session lifetime. **Cached value MUST include `revoked_at` and `expires_at`** so post-deserialise the read path re-checks `revoked_at IS NULL AND expires_at > NOW()` — never trust mere presence in cache as proof of liveness (architect C4). `RevokeSession` always does the PG write first, then `cache.Delete(...)` — write-through invalidation. `RevokeUserSessions` SELECTs all live token hashes for the user inside the same transaction, commits the revocation, then loops `cache.Delete(...)` per hash (the cache abstraction has no scan/wildcard delete; explicit enumeration is required — architect C4). Cache failures are non-fatal; the read path falls through to PG and logs a `slog.Warn`; counter `axiaops_session_cache_errors_total` increments. **`last_seen_at` updates only on cache miss** (i.e. once per cache TTL ≈ once per few minutes), not per request — defeats write amplification (architect N3). |
| `services/api/internal/auth/session_cache.go` | Thin wrapper that adapts `cache.Cache` for the session read path. Single key shape: `axiaops:session:<sha256(token)>`. Single value shape: serialised `model.Session` including `revoked_at` and `expires_at` (load-bearing — see `session.go`). No business logic — pure caching. Kept separate from `session.go` so the read-through behaviour can be unit-tested in isolation against an in-memory cache implementation. |
| `services/api/internal/auth/handler.go` | Routes below. Uses `Store`, `password`, `session`. |
| `services/api/internal/auth/cookie.go` | Cookie helpers — name `axiaops_session`, `HttpOnly`, `Secure` (in non-DEV_MODE), `SameSite=Lax`, path `/`. |
| `services/api/internal/middleware/auth_native.go` | Reads cookie, calls `auth.ValidateSession` (which goes through the cache layer), attaches user/org/role to request context. Middleware does **not** know whether the hit came from Redis or PG — that's invisible at this layer. |
| `services/api/internal/auth/bootstrap.go` | `POST /v1/auth/bootstrap` handler + the install-token generator that runs on service startup. **Multi-replica-safe** (architect C5): generator opens a tx, takes `pg_advisory_xact_lock(<a-stable-int>)`, checks `CountOrganizations(ctx) == 0`, then `INSERT INTO bootstrap_state (token_hash, minted_by_pod) VALUES (...) ON CONFLICT DO NOTHING RETURNING ...`. The replica whose insert returned a row generated the token; replicas whose insert was a no-op log `bootstrap: token already minted by peer pod=X` and proceed without printing the banner. The plaintext token is written to `BOOTSTRAP_TOKEN_FILE_PATH` (mode `0600`); printing to stdout is **opt-in** via `BOOTSTRAP_PRINT_BANNER=true` (architect S8 — default-secure). Handler validates POSTed token via `subtle.ConstantTimeCompare` against `bootstrap_state.token_hash`, creates org + user + owner membership + session in one tx, then `DELETE FROM bootstrap_state WHERE id='singleton'` and removes the token file. Returns 409 if `bootstrap_state` is empty (already consumed) or any organization already exists. The DB row — not the file — is the source of truth, so pod restarts before bootstrap don't lose state. |

**Modified files:**

| File | Change |
|---|---|
| `services/api/internal/middleware/auth.go` | At startup, branch on `AUTH_PROVIDER` env var. **Three-state strangler machine** (architect S1): `kinde` (Kinde JWT only — legacy), `both` (try cookie first, fall back to Bearer JWT — used during rolling deploys), `native` (cookie only — terminal state). Default `native`. The `both` mode is the *only* safe transitional value during a rolling deploy from `kinde` → `native`; documented in the deploy runbook as the mandatory intermediate step. On every authenticated request, increments `axiaops_auth_provider_active{provider=...}` counter and updates `axiaops_auth_provider_last_seen_seconds{provider=...}` gauge (architect N1 — gauge enables "no traffic in 7 days" SLO query under low-traffic conditions). |
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
| `services/shared/model/session.go` | `Session` struct. |

> **Note (architect S3):** the original plan created `services/shared/jwks/jwks.go` in B1. Removed — the existing `keyfuncFromCache` lives in `services/api/internal/middleware/auth.go` and stays there until B2 needs it. Building an unexercised package in B1 risks bit-rot. B2's first task is to lift it into `services/shared/jwks/` with tests covering both consumers (Kinde JWT validation under `AUTH_PROVIDER=kinde\|both` and the new native OIDC RP).

**Modified files:**

| File | Change |
|---|---|
| `services/shared/storage/storage.go` | Add `CreateSession`, `GetSessionByTokenHash`, `RevokeSession`, `RevokeUserSessions(userID)`, `ListUserSessionTokenHashes(userID)` (used by `RevokeUserSessions` for explicit cache-key enumeration — architect C4), `SweepExpiredSessions(olderThan)`, `CountSessionsForUser(userID)` (used to enforce per-user session cap — architect C2), `CreateInvitation` (token-shaped), `RedeemInvitation`, `CreatePasswordReset`, `RedeemPasswordReset`, `CreateUserWithPassword`, `UpdateUserPassword`, `CreateBootstrapState`, `GetBootstrapState`, `ConsumeBootstrapState`. |
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
| `AUTH_PROVIDER` | `native` | **Three-state strangler machine** (architect S1): `kinde` (legacy — Kinde JWT only), `both` (transitional — accept either cookie or Bearer JWT, used during rolling deploys only), `native` (terminal state — cookie only). The deploy runbook MUST move `kinde` → `both` → `native` in that order; jumping straight from `kinde` to `native` causes auth flapping during the rolling deploy because replicas land on different values mid-rollout. |
| `BOOTSTRAP_INSTALL_TOKEN` | (unset) | **Optional override** for unattended installs (CI, k8s operator). When set, the server uses this value as the install token *instead of* generating a random one. Same single-use semantics, same `bootstrap_state` row machinery; the env var should be cleared from secret stores after first boot. |
| `BOOTSTRAP_TOKEN_FILE_PATH` | `/var/run/axiaops/initial_setup_token` | Where the auto-generated token is written on first startup. Mode `0600`. Deleted on first successful bootstrap. Set to empty string to disable the file. |
| `BOOTSTRAP_PRINT_BANNER` | `false` | **Default-secure** (architect S8): when `false`, the token is only written to the file (mode 0600) and the log line says `bootstrap token written to <path>; cat the file to retrieve it` — no token value in stdout. When `true`, prints the full banner with the token (the original behaviour). Flip to `true` for ephemeral local dev where log-leak risk is zero. |
| `SESSION_TTL_HOURS` | `24` | Native session lifetime. |
| `SESSIONS_PER_USER_CAP` | `10` | Max concurrent active sessions per user (architect C2). On the (cap+1)th login, the oldest active session is revoked. Set to `0` to disable. |
| `INVITATION_TTL_DAYS` | `14` (existing) | Reused for token-based invitations. |
| `PASSWORD_RESET_TTL_HOURS` | `4` | Short — reset tokens are admin-issued and expected to be redeemed quickly. |

**Install-time banner** — only printed when `BOOTSTRAP_PRINT_BANNER=true` AND `bootstrap_state` is empty AND no organizations exist:

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

Default behaviour (with `BOOTSTRAP_PRINT_BANNER=false`) prints **only the path**, never the token:

```
INFO bootstrap: install token written to /var/run/axiaops/initial_setup_token
     (cat the file to retrieve it; pod=<HOSTNAME>)
```

The banner is also suppressed when `BOOTSTRAP_INSTALL_TOKEN` is set (operator already knows the value).

**Telemetry:**
- Counter: `axiaops_auth_provider_active{provider="native\|kinde\|both"}` — incremented on every authenticated request. Used to prove zero Kinde traffic before deletion (D2).
- Gauge: `axiaops_auth_provider_last_seen_seconds{provider}` — Unix timestamp of the most recent request handled by each provider (architect N1). Deletion-readiness query: `time() - axiaops_auth_provider_last_seen_seconds{provider="kinde"} > 7*86400` is robust under low traffic where `rate(... [7d]) == 0` is fragile.
- Counter: `axiaops_auth_login_total{outcome="success\|failure", reason="bad_password\|unknown_user\|rate_limited\|locked"}`.
- Counter: `axiaops_auth_invitations_total{outcome="created\|redeemed\|expired"}`.
- Counter: `axiaops_session_cache_total{outcome="hit\|miss\|error"}`.
- Counter: `axiaops_session_cache_errors_total` — drives the Redis-degradation alert.
- Counter: `axiaops_session_revocations_total{reason="logout\|password_reset\|admin_revoke\|cap_exceeded\|enforcement_change"}`.
- Counter: `axiaops_bootstrap_attempts_total{outcome="success\|sealed\|invalid_token"}`.

**Migration deadlines as deploy gates** (architect S7 — calendar reminders alone don't enforce migration):

| Date | Gate |
|---|---|
| B1 ship +14d | Staging `AUTH_PROVIDER` set to `native` (CI deploy job rejects `kinde` in staging values from this date forward). |
| B1 ship +60d | Internal AxiaOps Cloud dogfood production set to `native`. |
| B1 ship +150d | Tasks.md deletion MR opened (draft) referencing the strangler telemetry dashboard. |
| B1 ship +180d (2026-10-30) | If `axiaops_auth_provider_last_seen_seconds{provider="kinde"} > 30 days` across all environments, deletion MR merges. Otherwise blocked, ADR-0001 review trigger evaluated (the strangler turning permanent is itself a signal). |

These gates are wired into `.gitlab-ci.yml` as date-conditional job rules — not Tasks.md notes alone.

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
- [ ] **Cache liveness re-check verified** (architect C4): after a session is cached, `RevokeSession` is called from a separate connection (without going through the cache helper). Next `ValidateSession` finds the row in the cache, deserialises, sees `revoked_at != NULL`, and rejects. The deserialised value MUST gate liveness — a test that bypasses this re-check fails closed.
- [ ] **`RevokeUserSessions` clears every per-token cache entry** (architect C4): test creates 3 sessions for one user, populates the cache for all 3, calls `RevokeUserSessions`, asserts all 3 cache keys are gone (no scan/glob — explicit enumeration).
- [ ] Redis outage simulation: with `REDIS_URL` set but Redis down, auth still works (degrades to PG SELECT per request) and `axiaops_session_cache_errors_total` counter increments.
- [ ] **Sessions sweep + per-user cap** (architect C2): integration test seeds 11 sessions for one user, the 11th login revokes the oldest; sweep ticker (added to `services/api/cmd/main.go` alongside the stuck-scan ticker) deletes rows where `expires_at < NOW() - 7d` OR `(revoked_at IS NOT NULL AND revoked_at < NOW() - 7d)` and the test asserts the sweep query runs and deletes the seeded expired rows.
- [ ] **No RLS on `sessions` / `password_resets` / `bootstrap_state`** (architect C1): integration test calls `ValidateSession` with no `app.organization_id` set on the connection, asserts the lookup succeeds. A regression that re-enables RLS on these tables must fail this test.
- [ ] **Multi-replica bootstrap race resolved** (architect C5): integration test boots 3 replicas pointing at the same fresh DB simultaneously; asserts exactly one `bootstrap_state` row is created, exactly one banner is printed (or one log line under default-secure), and the other replicas log `token already minted by peer`.
- [ ] **Three-state strangler safe in rolling deploy** (architect S1): integration test simulates a rolling restart with `AUTH_PROVIDER=both` set on half the replicas during the transition; both Kinde JWT cookies and native session cookies are accepted on the same replica with no flapping.
- [ ] **`last_seen_at` write amplification check** (architect N3): test issues 100 successive requests with the same valid session within the cache TTL; asserts `last_seen_at` was updated at most once (only on the first PG miss), not 100 times.
- [ ] **Failure-mode observability** (architect N5): for each labeled failure outcome (`bad_password`, `unknown_user`, `rate_limited`, `locked`, cache `error`), there is a test that triggers the failure and asserts the corresponding counter increments by exactly 1. For each warning slog path (Redis miss, cache deserialise error), there is a test that captures slog output and asserts the warning was emitted.
- [ ] OpenAPI / API docs updated for the new `/v1/auth/*` routes.
- [ ] `Tasks.md` deprecation entry filed; `.gitlab-ci.yml` deploy gates configured (architect S7).
- [ ] **Single-org-per-session constraint enforced explicitly.** A user with >1 active membership cannot log in via `/v1/auth/login`; the endpoint returns 409 `{"error":"multi_org_not_supported","detail":"contact admin","b15_pending":true}`. Acceptance test asserts this rejection. Multi-org support lands in B1.5 (§4.7).

### 4.7 Phase B1.5 — Multi-org access (1w follow-up)

> **Why split from B1**: keeps B1 focused on the auth-provider swap (correctness-critical, schema-heavy). Multi-org is a UX layer on top of the same data model — the `memberships` table already supports it; only the session/login layer needs the addition. Cuts off `feat/sso/b1.5-multi-org` immediately on B1 merge; lands before B2 starts so OIDC JIT can mint sessions bound to the right active org. Effort: ~1 week.

#### 4.7.1 Endpoints

| Method | Path | Auth | Purpose |
|---|---|---|---|
| `POST` | `/v1/auth/login` (modified) | none (rate-limited) | If user has 0 memberships → 401. If exactly 1 → mint session bound to it (existing B1 behaviour). If >1 → return 200 with `{needs_org_selection: true, orgs: [{id, name}, ...]}` and **do not mint a session**. |
| `POST` | `/v1/auth/select-org` | none (rate-limited) | Body: `{email, password, organization_id}`. **Re-validates the password** (defence in depth — never trust the frontend to remember step 1). Confirms membership for the chosen org. Mints session bound to it. |
| `POST` | `/v1/auth/switch-org` | session | Body: `{organization_id}`. Confirms caller has membership in target. Revokes current session (PG UPDATE + cache invalidation), mints new session bound to target, returns new `Set-Cookie`. Audit: `session.org_switched` with `from`/`to`. |

#### 4.7.2 Frontend

| File | Purpose |
|---|---|
| `services/dashboard/src/screens/OrgPickerScreen.jsx` | New. Mounted at `/select-org`. Shows the orgs the user is a member of after a login that returned `needs_org_selection: true`. POSTs `/v1/auth/select-org` with re-supplied creds + chosen org. |
| `services/dashboard/src/components/OrgSwitcher.jsx` | New. Dropdown in the nav bar. Lists user's memberships from `/v1/me`. Clicking an entry POSTs `/v1/auth/switch-org`, on success refreshes the dashboard. |
| `services/dashboard/src/screens/LoginScreen.jsx` (modify) | On successful login response, branch on `needs_org_selection`: if true → navigate to `/select-org` with creds in form state; if false → `/dashboard`. |
| `services/dashboard/src/screens/AcceptInviteScreen.jsx` (modify) | When the email already exists in the DB: form asks for the *existing* password (not "set new"). On success, INSERT membership for the new org without touching other memberships. Optionally offer "switch to this org now" if user is currently logged in. |

#### 4.7.3 Backend

| File | Change |
|---|---|
| `services/api/internal/auth/handler.go` | Modify `Login`. Add `SelectOrg`, `SwitchOrg`. |
| `services/api/internal/auth/session.go` | `RotateSessionForOrg(ctx, currentSessionID, targetOrgID)` — single-tx revoke + mint. Used by `SwitchOrg`. |
| `services/shared/storage/storage.go` | Add `ListUserMemberships(userID)` returning `[]model.Membership` with org metadata. |
| `services/api/internal/api/handler.go` | `/v1/me` returns `memberships: [{org_id, org_name, role}]` — frontend uses this for the switcher. |

#### 4.7.4 Acceptance criteria — B1.5

- [ ] Login with one membership → straight to dashboard (B1 behaviour preserved).
- [ ] Login with two memberships → `needs_org_selection: true`, no session minted.
- [ ] `/v1/auth/select-org` re-validates the password independently (test: pass right password to login, then wrong password to select-org → 401).
- [ ] `/v1/auth/switch-org` revokes the old session (test: old cookie returns 401 after switch) and mints a new one with the target `organization_id`.
- [ ] Audit row `session.org_switched` with `metadata={from, to, user_id}` written on every switch.
- [ ] Switcher dropdown in nav reflects all of the user's active memberships and updates after revocation/addition.
- [ ] Invitation redemption for an existing email creates a new membership without touching the user row or other memberships.
- [ ] Cache invalidated on org switch — old session token's cache key deleted.
- [ ] Per-failure-mode observability: `axiaops_session_revocations_total{reason="org_switch"}` increments on every successful switch; `axiaops_auth_login_total{outcome="org_selection_required"}` increments on every multi-membership login.

---

## 5. Phase B2 — Native OIDC RP

Builds on B1's native session model. **First task of B2** (architect S3): lift `keyfuncFromCache` from `services/api/internal/middleware/auth.go` into a new `services/shared/jwks/jwks.go` package and update both consumers (the existing Kinde JWT validation under `AUTH_PROVIDER=kinde\|both`, and the new native OIDC RP). Tests written against the shared package cover both consumers.

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
| `oidc.go` | OIDC RP core: discovery doc fetch + cache (`sso:oidc-discovery:{cid}`, 24h TTL), JWKS via shared `services/shared/jwks/`, ID-token validation. Algorithm-confusion mitigation in `Keyfunc` (per design doc §11.3). **JWKS auto-refresh on signature failure** (architect S5): on signature verification failure, the RP forces a single re-fetch of JWKS bypassing the cache and retries verification once before returning auth error. Without this, an IdP key rotation = 24h auth outage. |
| `oidc_callback.go` | `GET /v1/sso/oidc/{cid}/callback` — exchanges code, validates token, calls `jit.go`, mints native session via `auth/session.go` with `auth_mode='sso'`. |
| `jit.go` | `JITResolveRole(connID, groups, defaultRole)` (table-driven test); `JITProvisionMembership(orgID, userID, role)` (single tx, idempotent). **Precedence rule** (architect S6): when a user matches multiple group mappings, the **highest role wins** (`admin > member > viewer`). Ties impossible — the order is total. Owner is never assignable via JIT regardless of mapping (sticky owner property; design doc §11.6). Documented as a comment at the top of `jit.go` with the test case enumerating each tie-break. |
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
- [ ] Group mapping precedence (`admin > member > viewer`) verified by table-driven test, including: (a) user in only one mapped group, (b) user in two groups mapping to different roles → highest wins, (c) user in groups mapping to same role → no error, (d) user in zero mapped groups → falls through to `default_role`.
- [ ] Owner cannot be assigned via JIT (asserted by `permission_matrix_test.go`).
- [ ] **JWKS auto-refresh on signature failure** (architect S5): integration test against mockoidc rotates the IdP key mid-test; the next login fails initial verification, the RP re-fetches JWKS bypassing the cache, retries verification, and succeeds — all in one request, no 24h outage.
- [ ] `/v1/sso/discover` returns 200 with `has_sso:false` for unknown domains; constant response shape verified.
- [ ] **Domain-confusion fuzz** (architect N4 — moved from §6.5): `dnstwist`-style domain-confusion test on `/v1/sso/discover` shows no false positives on similar domains (e.g. `acme.com` verified must not match `acme.co` or `aсme.com` punycode).
- [ ] **Open-redirect fuzz on OIDC `state`** (architect N4): fuzz the `state` parameter with `https://evil.com`, `//evil.com`, `javascript:` URLs; all must redirect to the fixed `/dashboard` path regardless of `state` content.
- [ ] `enforcement=required` blocks native-password sessions for the org with 403.
- [ ] `pending_memberships` invitation takes precedence over JIT (per design doc §10.4) — covered by integration test.
- [ ] `ssoSweep` ticker runs and logs sweep count > 0 after seeded expired rows.
- [ ] **JWKS package consumers parity** (architect S3): both `auth.go` (legacy Kinde validation) and `oidc.go` import from `services/shared/jwks/` and the package's tests cover both call shapes (issuer-bound JWKS for Kinde; per-connection JWKS for OIDC).

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

**Compromise / emergency rotation** (architect S4 — original plan said only "deploy with new env var values", which is incomplete):

When the SP private key is suspected leaked, the operator's actions are:
1. Generate a fresh keypair, deploy with new env values (rolling deploy).
2. Trigger the in-app **emergency-rotation** endpoint (`POST /v1/sso/saml/sp-key-rotated`, owner-only): atomically (a) revokes every active session with `auth_mode='sso'` originating from a SAML connection, forcing re-auth via the (now compromised → no-op for attacker) old key; (b) writes an `audit_log` row `sso.sp_key_rotated` with the new cert SHA-256 fingerprint; (c) sets a 30-day banner flag on every org with an active SAML connection.
3. The admin SSO screen shows the banner: *"AxiaOps SP signing certificate was rotated on YYYY-MM-DD. Please re-fetch and re-import the SP metadata at /v1/sso/saml/{cid}/metadata into your IdP within 30 days."* Banner clears automatically after 30 days OR when the operator confirms via `POST /v1/sso/saml/sp-key-rotation-acknowledged`.
4. After the 30-day grace, sessions originating from any connection that hasn't acknowledged are revoked; the connection moves to `status='disabled'` until the customer re-imports SP metadata.

The runbook in `docs/sso-key-rotation.md` enumerates these steps and includes a customer-facing email template.

### 6.5 Pen-test gating

**Hard gate** before any external pilot — see [`sso-integration-design.md` §13.4](sso-integration-design.md#134-pen-test-surface). SAML-specific (XML / replay / signature) lives here; OIDC-specific tests (`dnstwist` domain-confusion, open-redirect on `state`) moved to §5.5 (B2 acceptance) per architect N4 — they apply when B2 ships, not just when SAML lands.

- [ ] `xml-attacker` corpus run against `/v1/sso/saml/{cid}/acs` — all attempts rejected with `signature_invalid` or `assertion_replayed`.
- [ ] Open-redirect fuzzing on SAML `RelayState` — no redirect honoured outside the fixed `/dashboard` path.
- [ ] Multi-assertion SAML responses rejected.
- [ ] XXE attempts rejected (XML parser hardening verified by test).
- [ ] Signature-wrapping attacks (assertions with valid + injected `<saml:Assertion>` elements) rejected — `crewjam/saml`'s default behaviour verified, regression-tested.

### 6.6 Acceptance criteria — C

- [ ] `samltest.id` corpus passes — basic + signed + group-claim assertions.
- [ ] One real customer's Okta-SAML works end-to-end in `start-staging`.
- [ ] SP metadata endpoint produces valid XML that Okta/ADFS/Keycloak can ingest.
- [ ] Cert rotation: new cert added, old retained for 30 days, both verify; sweep purges old after 30 days (verified by test with manipulated `expires_at`).
- [ ] §6.5 pen-test gate cleared.
- [ ] **SP key compromise drill** (architect S4): operator triggers emergency rotation; integration test asserts (a) every SAML session is revoked, (b) audit row `sso.sp_key_rotated` is written, (c) admin SSO screen surfaces the 30-day banner per affected connection, (d) connection that doesn't acknowledge within 30 days is moved to `status='disabled'`.
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
AuthLoginTotal              prometheus.CounterVec   // outcome, reason
AuthSessionsActive          prometheus.GaugeVec     // org_id (cardinality cap: 1000)
AuthInvitationsTotal        prometheus.CounterVec   // outcome
AuthSessionRevocationsTotal prometheus.CounterVec   // reason (logout|password_reset|admin_revoke|cap_exceeded|enforcement_change)
BootstrapAttemptsTotal      prometheus.CounterVec   // outcome (success|sealed|invalid_token)
SessionCacheTotal           prometheus.CounterVec   // outcome (hit|miss|error) — cache-aside health
SessionCacheErrorsTotal     prometheus.Counter      // backend errors (Redis down, etc.) — drives degradation alerts
SSOLoginTotal               prometheus.CounterVec   // outcome, reason, protocol
SSOJITProvisioned           prometheus.CounterVec   // role, source_group_match
AuthProviderActive          prometheus.CounterVec   // provider (native|kinde|both) — strangler telemetry (counter)
AuthProviderLastSeen        prometheus.GaugeVec     // provider — Unix-seconds gauge (architect N1; supports low-traffic SLO queries)
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
| `AUTH_PROVIDER` | B1 | `native` | Three-state strangler toggle: `kinde\|both\|native`. |
| `BOOTSTRAP_INSTALL_TOKEN` | B1 | (unset) | Optional override for unattended installs. |
| `BOOTSTRAP_TOKEN_FILE_PATH` | B1 | `/var/run/axiaops/initial_setup_token` | Where the auto-generated token is written. Set empty to disable file. |
| `BOOTSTRAP_PRINT_BANNER` | B1 | `false` | Default-secure: token only in file, not stdout. Set `true` for ephemeral local dev. |
| `SESSION_TTL_HOURS` | B1 | `24` | |
| `SESSIONS_PER_USER_CAP` | B1 | `10` | Max concurrent active sessions per user. `0` to disable. |
| `REDIS_URL` | B1 | (existing) | When set, session reads use Redis cache via `services/shared/cache/`. When unset, in-memory cache fallback. PG remains the source of truth either way. |
| `INVITATION_TTL_DAYS` | B1 | `14` | Existing var, reused. |
| `PASSWORD_RESET_TTL_HOURS` | B1 | `4` | |
| `SSO_SP_PRIVATE_KEY_PEM` | C | — | Required when any SAML connection is active. |
| `SSO_SP_CERT_PEM` | C | — | ditto. |
| `KINDE_*` | (deprecated) | — | Required only while `AUTH_PROVIDER=kinde\|both`. Removed at deprecation date. |

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

**Resolved:**

| # | Question | Resolution |
|---|---|---|
| ~~Q6~~ | Multi-org access: should B1 ship with multi-org login support or defer? | **Deferred to B1.5** (1-week follow-up between B1 and B2). B1 ships with single-org-per-session; B1.5 adds org-picker login + live switcher. Rationale: B1 is already large; data model already supports multi-org via `memberships`; only the session/login layer needs the addition; ICP risk is bounded by the 1-week B1.5 timeline. See §4.7. |

---

## 10. Risks

| Risk | Mitigation |
|---|---|
| Strangler period extends past deprecation date because dogfood didn't migrate. | CI deploy gates enforce migration: staging on `native` by day 14, dogfood prod by day 60, deletion MR draft by day 150 (architect S7). `.gitlab-ci.yml` rules block `AUTH_PROVIDER=kinde` in staging values from day 14 forward. Without the gate, calendar reminders alone fail. |
| `crewjam/saml` CVE during Phase C development. | Pin to latest tagged release; subscribe to crewjam's security advisories before merge. |
| Native session cookie security regression (e.g. missing `Secure` flag, wrong `SameSite`). | Black-box test asserts cookie attributes. Browser-based smoke test in Phase B1 acceptance. |
| First-owner bootstrap endpoint left enabled accidentally (e.g. `BOOTSTRAP_INSTALL_TOKEN` left set in prod). | Endpoint is sealed by `bootstrap_state` row absence + `Store.CountOrganizations(ctx) > 0` — once any org exists, no token re-grants access. Token file deleted on successful bootstrap; `bootstrap_state` row deleted in the same tx. Endpoint logs WARN every time it's invoked. Metric counter `axiaops_bootstrap_attempts_total{outcome=...}` for monitoring. Runbook says to unset `BOOTSTRAP_INSTALL_TOKEN` from secret stores after first boot and verify both the token file and the `bootstrap_state` row are gone. |
| Operator pastes the token URL into Slack / chat. | Token is in the form body, never in the URL — no copyable URL contains the secret. Default-secure mode prints only the file path to logs (architect S8). |
| OIDC discovery doc TTL too long causes stale JWKS after IdP key rotation. | 24h TTL is a compromise. On signature-verification failure, force a single re-fetch bypassing the cache and retry once before rejecting. Promoted from "risk" to explicit B2 acceptance criterion (architect S5). |
| SAML SP private key compromised. | Key rotation runbook in `docs/sso-key-rotation.md`. Annual rotation cadence; emergency rotation triggers `POST /v1/sso/saml/sp-key-rotated` which (a) revokes every active SAML session, (b) audit-logs the new fingerprint, (c) sets a 30-day banner on every affected connection prompting customer to re-import SP metadata. Connections that don't acknowledge within 30 days move to `status='disabled'`. Detailed in §6.4 (architect S4). |
| Token-based invitation URL leaked (e.g. admin pastes into a public Slack channel). | Tokens are single-use + 14-day TTL. Redemption invalidates. Audit log records redemption + the user-agent IP. Document recommended sharing channels in the admin UI. |
| Multi-replica boot race: two replicas generate different bootstrap tokens, operator pastes one, load balancer routes to the other replica → 401 with no recovery. | `bootstrap_state` table + PG advisory lock makes generation exactly-once cluster-wide. Replicas that lose the race log `token already minted by peer pod=X` and serve from the same DB row. Multi-replica boot test required in B1 acceptance (architect C5). |
| Stale cache after revocation — user keeps access after `RevokeSession` because cached value is still served. | Cached value includes `revoked_at` and `expires_at`; the read path re-checks them after deserialise — never trusts cache presence as proof of liveness (architect C4). Write-through invalidation does PG write *then* `cache.Delete(...)`. `RevokeUserSessions` enumerates token hashes via `ListUserSessionTokenHashes` and deletes each cache key explicitly (no scan/wildcard). Verified by integration test. |
| Sessions table grows unbounded over time. | Per-user cap (`SESSIONS_PER_USER_CAP=10` default) bounds concurrent sessions; oldest-active is revoked on cap exceed. Hourly sweep ticker deletes rows where `expires_at < NOW() - 7d` OR `(revoked_at IS NOT NULL AND revoked_at < NOW() - 7d)`. Acceptance test seeds 11 sessions for one user and verifies cap enforcement + sweep behaviour (architect C2). |
| Rolling deploy from `AUTH_PROVIDER=kinde` to `=native` causes auth flapping (some replicas validate JWTs, others validate cookies; load balancer routes mid-flight). | Three-state machine: deploy must move `kinde` → `both` → `native` in that order. `both` mode accepts either cookie or Bearer JWT — no auth flapping during the rolling restart. Documented in deploy runbook (architect S1). |
| Bootstrap token leaked via stdout / log aggregator (CI logs forwarded to a third-party log service). | Default-secure: `BOOTSTRAP_PRINT_BANNER=false` writes only the file path to stdout, never the token value. Operator must `cat` the token file. Banner with token value is opt-in via `BOOTSTRAP_PRINT_BANNER=true` for local dev (architect S8). |
| Multi-org gap: B1 ships before B1.5; a design partner with users belonging to multiple orgs can't log in until B1.5 lands (~1w later). | B1 returns a clear `409 multi_org_not_supported` with `b15_pending: true` so the frontend can show a helpful message ("contact your admin to consolidate, or wait for the next release"). B1.5 cuts off `feat/sso/b1-native-auth` immediately on merge — calendar reminder + Tasks.md item. Workaround: customer can have the user's other-org memberships temporarily revoked + restored after B1.5. |
| Redis outage degrades performance silently. | Cache errors increment `axiaops_session_cache_errors_total` and log at `slog.Warn`. Alert rule fires when error rate > 1/min for 5 min. Auth still works on PG-only path during the outage. |

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
