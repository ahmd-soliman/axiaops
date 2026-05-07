# CLAUDE.md — API Service

## Purpose

REST API server for AxiaOps. Reads zombie/resource data from PostgreSQL, serves it to the
dashboard. Manages cloud account CRUD and triggers ingestion scans via HTTP to the ingestion service.

## Module

`axiaops.io/api` — Go module at `services/api/`. Entry point: `cmd/main.go`.

## Build tags

The api binary supports a `production` build tag (B1.7 layer 3 — plan §4.10.2). Two siblings of `cmd/main.go` define `devModeEnabled() bool`:

- `cmd/devmode_dev.go` (`//go:build !production`, default) — reads `DEV_MODE` env var.
- `cmd/devmode_production.go` (`//go:build production`) — returns false unconditionally.

Every site that previously read `os.Getenv("DEV_MODE")=="true"` routes through `devModeEnabled()` so the build-tag split lives at one seam. **Any new feature gated on dev-vs-prod must consult `devModeEnabled()`, never read the env directly** — bypassing the helper re-introduces the runtime-bypass attack the build tag closes.

Build commands:
- `go build ./cmd/` — default (DEV_MODE honoured). Used for dev-1/dev-2 deploys + local `make start-dev`.
- `go build -tags production ./cmd/` — customer-shipping (DEV_MODE no-op). Wired via `make build-production` and the `BUILD_TAGS` Dockerfile arg.

Test pairs in `cmd/devmode_{dev,production}_test.go` regression-pin both shapes; CI runs `make build-production` on every pipeline so tag regressions surface in <30s.

## Endpoints

| Method | Path | Auth | Purpose |
|--------|------|------|---------|
| GET | /health | No | Deep healthcheck — pings DB; 503 if DB unreachable. Used by Docker `depends_on` and nginx; kept for back-compat |
| GET | /livez | No | Liveness — always 200 unless the process can't reply. Wire orchestrator instance health to this |
| GET | /readyz | No | Readiness — pings DB (503 if down) and reports Redis status (informational; "ok" / "unreachable" / "skipped"). Wire monitoring/synthetic checks to this |
| GET | /metrics | No | Prometheus metrics (internal only) |
| GET | /version | Yes | Build identifier + license summary — `{service, version, commit, env, license}`. `license` is `{state}` only when no license is loaded (DEV_MODE / SaaS / production-with-no-license-installed), otherwise `{state, customer_id, expires_at, days_remaining, max_organizations}`. State values: `valid \| in_grace \| expired \| not_loaded`. Post-B1.6-amendment, `state="expired"` carries the full claim sub-object (the snapshot is retained on past-grace so `/v1/version` stays informative). Source for the LicenseBanner. |
| GET | /zombies | Yes | List zombie resources for organization |
| GET | /summary | Yes | Aggregate savings + per-service breakdown |
| GET | /trend | Yes | Zombie snapshots over time (?account_id, ?service, ?resource_type) |
| GET | /trend/services | Yes | Distinct services available in trend data |
| GET | /trend/resource-types | Yes | Distinct resource types for a service (?service) |
| GET | /resources | Yes | All resource records (zombies + active) |
| GET | /accounts | Yes | List connected cloud accounts |
| POST | /accounts | Yes | Connect new account (encrypts secret) |
| PATCH | /accounts/{id} | Yes | Update label, region, secret_key, scan_interval_hours |
| DELETE | /accounts/{id} | Yes | Remove account |
| POST | /accounts/{id}/scan | Yes | Trigger on-demand ingestion scan |
| POST | /dismissals | Yes | Dismiss or snooze a zombie resource |
| GET | /dismissals | Yes | List active dismissals (?account_id) |
| DELETE | /dismissals/{id} | Yes | Revoke a dismissal |
| GET | /audit | Yes | Organization audit timeline (?user_id, ?resource_type, ?resource_id, ?action, ?since, ?until, ?limit, ?cursor) |
| GET | /me | Yes | Current user's role + permission set; no permission required beyond authn |
| GET | /memberships | Yes | List organization memberships |
| POST | /memberships | Yes | Promote an existing user (by user_id) and assign a role; admin+ only |
| POST | /invitations | Yes | Invite by email. Writes a token-bearing `pending_memberships` row and returns `redemption_url` in the response body for OOB sharing (admin pastes into Slack/email). Admin+ for member/viewer; owner for admin. Optional `enforcement_hint: "sso_required"` (Tasks.md 2.7.20) is set when the org has at least one active OIDC connection with `enforcement="required"` — the URL still works for the redemption hop but every authed request afterward 403s with `sso_required`, so the dashboard renders a yellow callout telling the admin to route the invitee through SSO instead. |
| GET | /invitations | Yes | List pending invitations for the current org (?status=pending\|expired\|revoked) |
| DELETE | /invitations/{id} | Yes | Revoke a pending invitation. The OOB redemption URL becomes invalid the moment the row flips to revoked. |
| PATCH | /memberships/{id}/role | Yes | Promote or demote a member; permission tier depends on target role |
| DELETE | /memberships/{id} | Yes | Remove a member; self-leave bypasses permission check (last-owner guard still applies) |
| POST | /organizations/transfer-ownership | Yes | Atomically transfer owner role to another user; owner only |
| DELETE | /users/me | Yes | Right-to-erasure: hard-delete the caller. 409 if sole owner of any organization — must transfer or delete those organizations first. Anonymises audit_log across all organizations. |
| DELETE | /organizations/me | Yes | Right-to-erasure: cascade-delete the entire organization (every per-organization table including audit_log). Owner-only (`organization:delete`). |
| POST | /users/{id}/password-reset | Yes | Mint an admin-issued password-reset token. Returns `{user_id, redemption_url, expires_at}` for OOB sharing. Admin+ via `members:manage_basic`; owners-only when target is owner (cross-tier escalation guard). TTL via `PASSWORD_RESET_TTL_HOURS` (default 4h). |
| GET | /auth/bootstrap/state | No | First-run probe. Returns `{available: bool}` — true iff a `bootstrap_state` row exists (i.e. a POST to `/auth/bootstrap` would succeed with the right token). Drives the dashboard's mount-time auto-redirect from `/login` → `/bootstrap` on fresh installs (Tasks.md row 2.7.16). Not a new oracle — same posture is observable today by POSTing junk to `/auth/bootstrap` and reading 409 vs 401. |
| POST | /auth/bootstrap | No | First-owner install. Body `{token, email, name, password, organization_name}`. Sealed forever after first success — returns 409 on subsequent attempts. Token comes from `BOOTSTRAP_TOKEN_FILE_PATH` (mode 0600) or `BOOTSTRAP_INSTALL_TOKEN` env. |
| POST | /auth/login | No | Native email + password. Single-membership users: returns 200 with `{user, organization}` and sets the `axiaops_session` cookie. Multi-membership users (B1.5): returns 200 with `{needs_org_selection:true, orgs:[{id,name},...]}` and **no cookie** — the dashboard collects the chosen org and POSTs it to `/auth/select-org`. Rate-limited 10/min/IP + 5/min/email. |
| POST | /auth/select-org | No | B1.5 picker step. Body `{email, password, organization_id}`. Re-validates the password from scratch (defence in depth — never trust the frontend to remember step 1), confirms the chosen org is in the user's membership set, mints a session bound to it. Failure modes collapse to one 401 `invalid_credentials` shape (wrong password, unknown email, org-not-in-set all look the same — narrows the no-creds membership-probe channel). Shares the rate-limit budget with `/auth/login` so an attacker can't double their cap by alternating endpoints. |
| POST | /auth/switch-org | (cookie) | B1.5 in-app org switcher. Body `{organization_id}`. Session-authenticated — caller is already logged in and just re-binds to a different org they're a member of. Same-org POST is a 200 no-op (idempotent for racy clients). Wrong org → 403 `not_a_member` (NOT 401-collapsed: the caller's session is valid; "not a member" is a separate posture from "wrong creds"). On success: revokes the current session (PG + cache + `axiaops_session_revocations_total{reason="org_switch"}`), mints a new one bound to target, sets fresh cookie, audits `session.org_switched` in the FROM org with metadata `{from, to}`. |
| POST | /auth/logout | (cookie) | Revoke the current session and clear the cookie. Tolerant — 204 even when cookie is absent or unknown. |
| POST | /auth/invitations/preview | No | B1.5 peek-before-redeem. Body `{token}`. Returns `{email, organization_name, role, existing_user, existing_user_name}` so the AcceptInviteScreen can pick the right form mode (set new password vs verify existing password). Token is NOT consumed. 410 on unknown / expired / already-redeemed. The user's password hash is NEVER returned. |
| POST | /auth/invitations/redeem | No | Accept an invite token. Two flows the server selects on by looking up the email globally: **new user** — body `{token, password, name}`; server hashes, creates the user, inserts the membership. **Existing user (B1.5 cross-org)** — body `{token, password}`; server verifies the supplied password against the user's stored argon2id hash and only inserts the membership (the user row stays untouched). Both flows mint a session bound to the new org. 410 on invalid token, 401 invalid_credentials on wrong password (existing-user flow), 409 email_taken on race. |
| POST | /auth/password-reset/redeem | No | Set a new password from an admin-issued reset URL. Body `{token, new_password}`. Revokes EVERY live session for that user on success — frontend must redirect to `/login`. 410 on unknown / expired tokens. |

## Key Patterns

- **Route registration:** `handler.Register(mux)` — uses Go 1.22+ `mux.HandleFunc("GET /path", fn)`
- **Organization context:** Auth middleware extracts organization from JWT, sets via `middleware.OrganizationID(ctx)`.
  Handlers must call `storage.WithOrganizationID(ctx, organizationID)` before any DB operation.
- **Async scans:** `POST /accounts/{id}/scan` sets status to `scanning`, fires goroutine with
  `context.WithTimeout(context.Background(), 15*time.Minute)`, POSTs to ingestion at `:8081/scan`
- **Scan lock:** In-memory `sync.Mutex` map prevents duplicate concurrent scans per account
- **Stuck scan recovery:** Background ticker (5min) resets accounts stuck in `scanning` >15min

## Adding New Endpoints

1. Add handler method to `Handler` struct in `internal/api/handler.go`
2. Register route in `Register(mux)` with correct HTTP method prefix
3. Extract organization: `tid := middleware.OrganizationID(r.Context())`
4. Set organization on context: `ctx := storage.WithOrganizationID(r.Context(), tid)`
5. Use `writeJSON(w, data)` for responses
6. Add test in `internal/api/handler_test.go` using `httptest.NewRecorder`

## Auth Middleware

- Locations: `internal/middleware/auth.go` (context-key getters/setters + `DevBypass` + `publicPath`); `internal/middleware/auth_native.go` (`WrapNative` — wraps the `auth.Provider` seam, attaches the resolved `Identity` to the request context).
- Production provider is `auth.NativeProvider`: cookie-bound session lookup against the `sessions` table, role + org resolved via `MembershipLookup`. The `auth.Provider` interface is preserved as a single-impl seam so a future SaaS reactivation can swap implementations without touching the middleware chain.
- `DEV_MODE=true` → auth chain replaced by `DevBypass`, uses `DEV_ORGANIZATION_ID` / `DEV_USER_ID` / `DEV_USER_EMAIL`.
- OIDC SSO ceremony is wired in `serverbuild.ComposeServer` when `!cfg.DevMode`: initiate at `/v1/sso/oidc/{cid}/initiate`, callback at the cid-less `/v1/sso/oidc/callback` (state carries connection identity per Tasks.md 2.7.22). The legacy path-cid callback `/v1/sso/oidc/{cid}/callback` stays wired for one release as a deprecation window — hits surface via `axiaops_sso_legacy_callback_total{cid}`. Successful callback mints a native session via the same `SessionManager` with `auth_mode='sso'`.
- See `docs/invitation-flow.md` for how `pending_memberships` rows get redeemed on first login.

## Prometheus Metrics (Phase 2.6)

See `../../OBSERVABILITY.md` for full observability guide.

### HTTP Metrics (Automatic)

The request logging middleware (line 161–182 of main.go) records:

- `axiaops_http_requests_total` — counter per method/route/status
- `axiaops_http_request_duration_seconds` — histogram per method/route
- `axiaops_http_responses_total` — responses by method/route/status
- `axiaops_http_errors_total` — 5xx errors by method/route/status
- `axiaops_http_requests_in_flight` — active requests (gauge)

These are exposed via `/metrics` endpoint for Prometheus scraping.

### Database Metrics (To Add)

Wrap database calls with `observability.DatabaseObserver`:

```go
import "axiaops.io/shared/observability"

observer := observability.NewDatabaseObserver("LOAD_ZOMBIES")
defer observer.Observe()
zombies, err := h.store.LoadZombies(ctx)
if err != nil {
    observer.ObserveError()
    // ... handle error
}
```

### Error Handling (Structured Logging)

Use `observability.LogError()` for API errors:

```go
import "axiaops.io/shared/observability"

if err != nil {
    observability.LogError(r.Context(), err,
        "operation", "list_zombies",
        "endpoint", "GET /v1/zombies",
    )
}
```

Errors are logged to stdout with structured context (JSON format in production).

## Environment Variables

| Variable | Required | Default | Notes |
|----------|----------|---------|-------|
| DATABASE_URL | Yes | — | PostgreSQL app-user connection |
| MIGRATION_DATABASE_URL | Yes | — | PostgreSQL owner connection (migrations) |
| API_ADDR | No | :8080 | Listen address |
| SESSION_TTL_HOURS | No | 24 | Native session lifetime. |
| SESSIONS_PER_USER_CAP | No | 10 | Max concurrent active sessions per user. The (cap+1)th login revokes the oldest. `0` disables the cap. |
| BOOTSTRAP_INSTALL_TOKEN | No | — | Optional override for the auto-generated install token (unattended k8s installs). When set the banner is suppressed; same single-use semantics. Clear from secret stores after first boot. |
| BOOTSTRAP_TOKEN_FILE_PATH | No | /var/run/axiaops/initial_setup_token | Where the install token is written on first startup (mode 0600). Deleted on first successful bootstrap. Set to empty string to disable file writing. |
| BOOTSTRAP_PRINT_BANNER | No | false | Default-secure: when false, the install token is only written to the file (never stdout). Set `true` for ephemeral local dev where stdout-leak risk is zero. |
| PASSWORD_RESET_TTL_HOURS | No | 4 | Admin-issued password-reset token lifetime. Short by design — admins are expected to share the URL OOB and the user redeems promptly. |
| PUBLIC_HOST | No | — | Externally-reachable origin (`https://app.example.com`) used to build OOB redemption URLs for invitations and password resets. Empty produces relative URLs the frontend resolves against `window.location.origin`. |
| INVITATION_TTL_DAYS | No | 14 | How long a `pending_memberships` row stays redeemable. |
| DEV_MODE | No | false | Skip auth, use fixed organization |
| DEV_ORGANIZATION_ID | When `DEV_MODE=true` | — | Organization ID for dev bypass. No default — startup `die()`s if unset while `DEV_MODE=true`. |
| DEV_USER_ID | No | dev-user-axiaops | User ID seeded in dev mode; `EnsureDevMembership` assigns it `owner` |
| DEV_USER_EMAIL | No | dev@axiaops.local | Email for the dev user row |
| CORS_ORIGIN | No | * | Allowed CORS origin. Two shapes: `*` (legacy posture, no credentials) or comma-separated allowlist (e.g. `http://localhost:5173`) which reflects the request Origin and emits `Access-Control-Allow-Credentials: true` so the native-auth session cookie round-trips. Required as a non-wildcard value when the dashboard is on a different origin from the API (e.g. local Vite dev). |
| ENCRYPTION_KEY | Yes | — | 32-byte hex for AES-256-GCM |
| APP_ENV | No | — | Environment (production, staging, development) |
| APP_VERSION | No | — | Release version (e.g., 2.6.0); surfaced via `GET /v1/version` |
| APP_COMMIT_SHA | No | — | Short git SHA of the build; surfaced via `GET /v1/version` |
| LOG_LEVEL | No | info | Log level (debug, info, warn, error) |
| LOG_OUTPUT | No | json | Log format (json or text) |

## Testing

```bash
cd services/api && go test ./...
```

Tests use `httptest.NewRecorder`, mock Store implementation, and RSA-generated JWTs.
No real DB or network calls in unit tests.
