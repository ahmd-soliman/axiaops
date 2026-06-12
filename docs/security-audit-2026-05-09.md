# Security Audit — 2026-05-09

**Audit metadata**

| Field | Value |
|---|---|
| Audit date | 2026-05-09 |
| Branch | `security/audit-2026-05-09` (no code changes — this doc only) |
| Base commit | `3659b4f` (origin/develop at audit time) |
| Scope | api + ingestion + shared + dashboard + deploy manifests + migrations |
| Method | Architect-agent deep dive across the source tree, every finding verified by reading the cited file:line |
| Threat model | Self-hosted FinOps SaaS, multi-tenant orgs sharing one Postgres (RLS-isolated), behind an edge proxy TLS edge, native cookie sessions + per-org OIDC SSO |
| Output | Read-only analysis — no source files modified |

This audit re-evaluates the codebase against its own stated security posture (CLAUDE.md, per-service CLAUDE.md, `docs/native-auth-bootstrap.md`, `docs/invitation-flow.md`, plan §4.10). Severity ratings reflect impact under the threat model above.

---

## Resolution status — 2026-05-12

Branch `security/hardening-2026-05` (13 commits on top of `origin/develop` at `dba1b68`) closes 12 of the 27 findings. The rest are tracked in [issue #94](https://gitlab.com/axiaops/axiaops/-/work_items/94).

| Finding | Severity | Status | Commit |
|---|---|---|---|
| **C-1** | Critical | ✅ Resolved on branch `security/c1-ingestion-hmac-impl` (HMAC + Redis envelope + `requirepass`). Pending merge of the linked MR. | — |
| **C-2** | Critical | ✅ Resolved | `541d7c1` |
| **C-3** | Critical | ✅ Resolved | `c5d6467` |
| **H-1** | High | ✅ Resolved — migration 035 enables RLS on `users` (org-isolation + `users_runtime_bypass`); app-pool `users` callsites moved to the runtime pool / org-scoped tx (`EnsureUser`, `UpsertUser`, `GetUserByID`, `GetUserByEmail`, `SetUserSSOConnection`, `GetUserSSOConnectionID`, `ListMemberships`). | branch `docs/billing-plan-and-signup-refresh` |
| H-2 | High | Open — pending decision on bearer-token vs separate listener | — |
| **H-3** | High | ✅ Resolved | `e7337d8` (+ `01ad35d` test refactor) |
| **H-4** | High | ✅ Resolved | `579f103` |
| **H-5** | High | ✅ Resolved | `884c711` |
| **M-1** | Medium | ✅ Resolved | `8148019` |
| **M-2** | Medium | ✅ Resolved | `0c088d7` (+ `01ad35d` failure-mode docstring) |
| **M-3** | Medium | ✅ Resolved | `0bef472` + `d7492b6` (deploy plumbing) |
| **M-4** | Medium | ✅ Resolved | `3b907e0` |
| **M-5** | Medium | ✅ Resolved | `3b907e0` |
| **M-6** | Medium | ✅ Resolved | `badea60` |
| M-7 | Medium | Open | — |
| M-8 | Medium | Open | — |
| **M-9** | Medium | ✅ Resolved | `3ae83e4` (+ `01ad35d` regression test) |
| M-10 | Medium | Deferred per audit (SOC2/ISO trigger) | — |
| L-1..L-9 | Low | Open — most pair with H-2 or are informational | — |

Findings carried over without code changes:
- L-2 (IPv6 zone-id stripping) was implicitly addressed by C-3's `httpip` extraction — verify before closing.
- I-1 (CLAUDE.md drift about ingestion `/scan` auth) — ✅ fixed alongside C-1: `services/ingestion/CLAUDE.md` Endpoints table now lists `/scan` and `/v1/credentials/verify` as `Auth: HMAC`.
- §12 Q7 (Redis `requirepass`) — ✅ closed alongside C-1: deploy/*.yml + docker-compose.yml gate Redis on `REDIS_PASSWORD`, and `REDIS_URL` carries the password via userinfo.

Operator follow-ups still owed:
- **C-2 rotation runbook** (rotate dev-1/dev-2 keys, re-encrypt or wipe `accounts.secret_encrypted`, date the exposure window via `git log -S '5f67e3ef'`).
- **CORS_ORIGIN for production** — set via GitLab CI variable scoped to `production` (preview/staging already set on 2026-05-12).

Additional concerns surfaced during the hardening work (not in the original audit) are listed in issue #94: H-3 IPv6 loopback gap, missing `Permissions-Policy` header, 413-vs-400 detection in body-size errors, SSRF surface on `oidc_discovery_url` private IPs, dashboard wire-compat audit for `DisallowUnknownFields`.

---

## Critical

### C-1. Ingestion `/scan` and `/v1/credentials/verify` accept unauthenticated requests on the LAN

- **Location**: `services/ingestion/cmd/main.go:144-206` (`POST /scan`), `:210` (`POST /v1/credentials/verify`).
- **Description**: The ingestion mux registers these endpoints with no shared-secret check, no mTLS, no `Authorization` header validation, no IP allowlist. The only gate on `/scan` is `license.IsScanAllowedForState`. The handler unmarshals `{account_id, organization_id}` from the body and immediately runs the scan with `storage.WithOrganizationID(...)` — RLS is set to whatever the caller put in the body. `services/ingestion/CLAUDE.md` advertises "Auth: Yes" for `POST /scan`, which is incorrect — the doc and the code disagree.
- **Impact**: Any process that can reach `axiaops-ingestion-${env}:8081` over the docker/self-hosted network can:
  1. Trigger arbitrary scans for any `organization_id` — burns AWS API quota, skews dashboards, and runs the ingestion path under a forged tenant context.
  2. Pass a role ARN + external ID to `/v1/credentials/verify` and harvest the resolved AWS account number from the response — reconnaissance against any role with a misconfigured trust policy.
  3. Drive `runScan` to load a connected `accounts` row (any org's), have the service decrypt `secret_encrypted` with the shared `ENCRYPTION_KEY`, and fire AWS calls in that customer's account.
- **Threat model**: Requires LAN access (the per-env `axiaops-${env}-network`). The deployment posture is "mutually-trusted internal services," but defence in depth here is ~30 lines of code, and a single compromised sidecar / future service on the same compose network / pivot from one container collapses the isolation entirely.
- **Remediation**: Add a shared-secret HMAC header (`X-AxiaOps-Ingestion-Token`) generated at deploy time, injected into both `api` and `ingestion` env, and verified at the start of `POST /scan` and `POST /v1/credentials/verify`. Update `services/shared/queue/sync/sync.go:46-50` and the redis queue worker to attach the header. Reject with 401 on missing/wrong token before any DB lookup.

### C-2. Hardcoded `ENCRYPTION_KEY` committed as a fallback in compose + dev manifests

- **Location**: `docker-compose.yml:42`, `:94`; `deploy/dev.yml:24`, `:46`. Pattern: `ENCRYPTION_KEY: ${ENCRYPTION_KEY:-<32-byte hex literal>}` — same literal at all four call sites. (Value intentionally not reproduced here; grep `ENCRYPTION_KEY` in those files for the exact bytes.)
- **Description**: A 32-byte AES-256-GCM key is committed in two manifests as a `${ENCRYPTION_KEY:-…}` default. `services/api/.env` and `services/ingestion/.env` (gitignored on disk locally but present in many developers' checkouts) carry the same value. Per `.gitlab-ci.yml`, `deploy:dev-1` and `deploy:dev-2` are unscoped CI jobs, so a pipeline that runs deploy:dev without the operator setting the CI variable falls back to this committed value.
- **Impact**: Anyone with `git log` access to develop (every dev with repo read, plus log mirrors) can decrypt any AWS secret stored in the dev-1/dev-2 `accounts.secret_encrypted` column. dev-1 and dev-2 are real self-hosted hosts behind an edge proxy with TLS — they accept real AWS access keys for connected accounts. If an internal tester or operator connected a real AWS account thinking the dev host was throwaway, that key is decryptable by every developer with repo access.
- **Remediation**:
  1. Remove the literal default from `docker-compose.yml:42`, `:94` and `deploy/dev.yml:24`, `:46`. Make `ENCRYPTION_KEY` required (no `:-fallback`), so `docker compose up` fails fast on a missing value.
  2. Rotate the dev-1/dev-2 keys (`openssl rand -hex 32`), set as **File-type** CI vars scoped to `dev-1` / `dev-2`.
  3. Re-encrypt `accounts.secret_encrypted` on those hosts under the new key, or delete the rows (dev hosts are documented throwaway).
  4. `git log -p -S '<first 8 chars of the literal>'` to date the exposure window. Treat all data ever encrypted under this key as compromised.

### C-3. Audit log + SSO callback record attacker-spoofable client IP (leftmost X-Forwarded-For)

- **Location**: `services/api/internal/audit/audit.go:107-124` (`clientIP`); `services/api/internal/sso/oidc_callback.go:547-563` (`callbackClientIP`).
- **Description**: Both helpers take the leftmost comma-separated value of `X-Forwarded-For`. nginx and App Runner *append* the connecting peer's IP to whatever XFF header the client sent — so a request from `attacker-ip` carrying `X-Forwarded-For: 1.2.3.4` becomes `X-Forwarded-For: 1.2.3.4, attacker-ip` after the proxy. Taking leftmost returns `1.2.3.4` — fully attacker-controlled. The same auth package's own `requestIP` (`auth/handler.go:1184-1223`) does the right thing (rightmost token + X-Real-IP), with a long comment explaining exactly why; the audit and SSO-callback sites were missed when that fix landed.
- **Impact**:
  1. Audit-log `ip_address` values are attacker-chosen — pollutes forensic IP correlation, lets an attacker plant other-org IPs in your audit history.
  2. SSO callback's session row gets a forged `ip` value — an analyst chasing a session-takeover incident sees the attacker's chosen IP rather than the real source.
  3. Login + invitation rate limiting still uses the correct `requestIP` (X-Real-IP / rightmost XFF), so the rate limiter is **not** bypassable here — the leak is forensic, not authn.
- **Remediation**: Export `requestIP` from the auth package (or move it to a shared `httpip` helper) and have both `clientIP` and `callbackClientIP` call it. Single seam, single fix.

---

## High

### H-1. `users` table has no Row-Level Security policy

- **Location**: `services/shared/storage/postgres/migrations/001_initial.up.sql:16-24` (table create); no `ENABLE ROW LEVEL SECURITY` on `users` ever applied across migrations.
- **Description**: Migration 001 enables RLS on `cost_records`, `zombie_records`, `resource_records`, `accounts`, `zombie_snapshots`. Later migrations add RLS to `memberships` (015), `pending_memberships` (017), `audit_log` (014), and SSO tables (022). `users` is conspicuously absent. Code mostly uses `adminPool` for users access (which bypasses RLS anyway), but the app pool has full SELECT/INSERT/UPDATE/DELETE on users. Any future handler that opens an app-pool tx and runs `SELECT * FROM users WHERE …` without an explicit org filter sees every user across every org.
- **Impact**: Defence-in-depth gap. There is no current code path that exploits this (`native_auth.go:39-41` documents users as "scoped via FK only"), but a future regression to `s.pool` would silently return cross-tenant rows. Users carry sensitive columns (`password_hash`, `email`, `sso_external_id`) — high-risk-of-future-regression rather than current exploit.
- **Remediation**:
  ```sql
  ALTER TABLE users ENABLE ROW LEVEL SECURITY;
  CREATE POLICY users_organization_isolation ON users
    USING (organization_id = current_setting('app.organization_id', true))
    WITH CHECK (organization_id = current_setting('app.organization_id', true));
  ```
  Audit existing app-pool users-table consumers (cross-org B1.5 invite-existing-user uses `adminPool` already — unaffected). Future regression to `s.pool` will fail closed.

### H-2. `/metrics` exposed unauthenticated with per-org labels

- **Location**: `services/api/internal/serverbuild/build.go:283`; `services/api/internal/middleware/auth.go:47` (`/metrics` in `publicPath`); `services/dashboard/nginx.conf:55-57` (only blocks `/api/metrics` at the dashboard nginx, not at the API container itself); `services/ingestion/cmd/main.go:142` (same pattern).
- **Description**: `/metrics` carries `organization_id`, `customer_id`, license expiry timing, per-route request rate. The dashboard nginx blocks `/api/metrics` at the public edge — but the API container exposes `8080:8080` directly in `deploy/{staging,dev}.yml`, so any LAN host on `axiaops-${env}-network` can reach `http://api:8080/metrics`. an edge proxy is supposed to only proxy to `dashboard:80` publicly; that posture holds today but is one-typo-away from a public leak.
- **Impact**: LAN-side enumeration of every org, the license customer_id, and granular timing data. Customer enumeration on a multi-tenant install. If an edge proxy is misconfigured to proxy `https://axiaops-<env>.local/metrics`, the data leaks to the public internet.
- **Remediation**:
  1. Drop `/metrics` from `auth.go:47` `publicPath`. Behind `WrapNative`, give `/metrics` a dedicated gate that accepts a `METRICS_BEARER_TOKEN` header. *Or* add a `:9090` listener bound to the docker bridge that serves `/metrics`, and remove `/metrics` from the public 8080 listener.
  2. Same treatment for ingestion `/metrics`.
  3. Confirm an edge proxy config does not proxy `/metrics` for any env.

### H-3. SSO connector accepts non-https `oidc_discovery_url` / `idp_metadata_url`

- **Location**: `services/api/internal/sso/handler.go:122` (`OIDCDiscoveryURL` request field); `services/api/internal/sso/connector.go:68-86` (`NativeConnector.Save` — no scheme validation); `services/api/internal/sso/oidc.go:280` (discovery doc fetch uses URL verbatim).
- **Description**: An admin can POST `/v1/sso/connections` with `oidc_discovery_url=http://idp.example.com/.well-known/openid-configuration`. The validator's discovery fetch and the subsequent token-endpoint POST run over plain HTTP. The token POST sends `client_secret` in the body — over plain HTTP that's wire-readable. The startup-time `slog.Warn` at `initiate.go:79-83` only checks `publicHost`, never per-connection `oidc_discovery_url`.
- **Impact**: A deceptive or compromised admin (in scope for the per-org SSO threat model — admins drive the IdP relationship, not blanket-trusted with internal handshakes) can route the OIDC ceremony over plain HTTP, exposing `client_secret` to any LAN-side observer or transparent proxy.
- **Remediation**: In `NativeConnector.Save` (`connector.go:68`), reject any connection where `OIDCDiscoveryURL` is non-empty AND does not start with `https://`. Same check on `IdPMetadataURL`. Loosen for tests via an env-var override only, not production.

### H-4. No body-size cap or `DisallowUnknownFields` outside the auth package

- **Location**: `services/api/internal/api/handler.go:711` (createAccount), `:793` (updateAccount), `:1086` (createDismissal); `services/api/internal/sso/handler.go:143`, `:217`, `:327`, `:464`. Reference (correct pattern): `services/api/internal/auth/handler.go:1229` (`decodeJSON` wraps `r.Body` in `http.MaxBytesReader(w, r.Body, 64<<10)` and uses `dec.DisallowUnknownFields()`).
- **Description**: Only the auth package's `decodeJSON` helper applies a body-size limit. Every other handler does plain `json.NewDecoder(r.Body).Decode(&req)`. There is no global body-size cap in the middleware chain (`serverbuild/build.go:285+`).
- **Impact**: An authenticated attacker can POST a multi-GB body to `/v1/accounts`, `/v1/dismissals`, `/v1/sso/connections`, or any other handler — the JSON decoder consumes RAM proportional to the inbound body until OOM. Mid-tier DoS. `DisallowUnknownFields` is also missing, so any future struct field rename creates a silent data-loss surface.
- **Remediation**: Promote `decodeJSON` to a shared package (e.g. `services/api/internal/httpjson`). Replace every `json.NewDecoder(r.Body).Decode(&req)` site with `httpjson.Decode(w, r, &req)`. The 64 KiB cap is a sane default for every request struct in the codebase.

### H-5. No security headers from API or dashboard nginx

- **Location**: `services/dashboard/nginx.conf` (no `add_header`); `services/api/internal/api/handler.go:179-211` (cors middleware sets only CORS headers).
- **Description**: No CSP, no `X-Content-Type-Options: nosniff`, no `X-Frame-Options: DENY` / CSP `frame-ancestors`, no HSTS, no Referrer-Policy. The dashboard is a SPA served from an HTTPS origin behind an edge proxy. Its responses carry none of these.
- **Impact**:
  1. Clickjacking — the dashboard can be framed by any site. SameSite=Lax blocks cross-site POSTs, but framed top-level navigation still permits UI overlay tricks.
  2. No HSTS at the app — relies wholly on an edge proxy. A first-time HTTP visit + an edge proxy misconfiguration = downgrade.
  3. MIME sniffing — a future feature that serves user-uploadable content could be coerced into HTML/JS interpretation.
- **Remediation**: Add to `services/dashboard/nginx.conf`:
  ```
  add_header X-Content-Type-Options "nosniff" always;
  add_header X-Frame-Options "DENY" always;
  add_header Referrer-Policy "strict-origin-when-cross-origin" always;
  add_header Strict-Transport-Security "max-age=31536000; includeSubDomains" always;
  add_header Content-Security-Policy "default-src 'self'; script-src 'self'; style-src 'self' 'unsafe-inline'; img-src 'self' data:; connect-src 'self'; frame-ancestors 'none'" always;
  ```
  Validate against the actual Vite bundle (may need a hash for inline styles). Test the OIDC redirect — the IdP authorize URL is a top-level navigation, not a frame, so `frame-ancestors 'none'` does not break it.

---

## Medium

### M-1. Bootstrap install token: env-var path leaks to in-container processes

- **Location**: `services/api/internal/auth/install_token.go:131-147`, `:209-227` (banner gated on `BOOTSTRAP_PRINT_BANNER=true`); `services/api/internal/auth/handler.go:288` (`removeInstallTokenFile`).
- **Description**: Default-secure when `BOOTSTRAP_PRINT_BANNER=false`. But when an operator overrides via `BOOTSTRAP_INSTALL_TOKEN`, the env var lives in `/proc/1/environ` and any process inside the container can read it during the bootstrap window. CLAUDE.md tells operators to "clear from secret stores after first boot" — operator discipline.
- **Remediation**:
  1. After `ConsumeBootstrapState` succeeds, `os.Unsetenv("BOOTSTRAP_INSTALL_TOKEN")` to shrink the in-container exposure window.
  2. Emit a startup log line when the env var is still set so the operator runbook surfaces in prod logs.

### M-2. SSO state Consume is not atomic (Get-then-Del race)

- **Location**: `services/api/internal/sso/state.go:118-161` (the author flags the issue at `:121-132`).
- **Description**: `cache.Cache` has no atomic Get+Del primitive — two concurrent `Consume` calls on the same state token both succeed at Get before either Del completes. For honest IdPs the single-use authorization-code constraint catches the second call; for a buggy or compromised IdP that issues reusable codes, the state non-atomicity becomes a real replay window.
- **Remediation**: Implement `cache.Cache.GetDel(ctx, key) ([]byte, error)` backed by Redis 6.2+ `GETDEL` (atomic), or wrap `Consume` with `golang.org/x/sync/singleflight` keyed on the state token. GETDEL is preferable — singleflight is per-process; multi-replica deployments still race.

### M-3. `CORS_ORIGIN=*` default is silent — no startup warning

- **Location**: `services/api/internal/api/handler.go:179-211` (default at line 181-183).
- **Description**: When `CORS_ORIGIN` is empty, the default is `*`. The wildcard is intentionally non-credentialed (so the cookie won't round-trip), but it widens read-surface for any future endpoint that returns sensitive data without auth. There's no startup warning that `CORS_ORIGIN=*` is set in production.
- **Remediation**: Make `CORS_ORIGIN` required when `!DEV_MODE` (composition root `die()`s if empty). At minimum, `slog.Warn` at startup when `CORS_ORIGIN=*` AND `DEV_MODE=false`.

### M-4. Bootstrap state probe is a free reconnaissance signal

- **Location**: `services/api/internal/auth/handler.go:149-168` (`GET /v1/auth/bootstrap/state`).
- **Description**: Returns `{available: true}` iff a `bootstrap_state` row exists. The CLAUDE.md note ("not a new oracle") is correct for already-bootstrapped, but `available: true` is a positive signal: an attacker scanning installs (Shodan / IP list) identifies which ones are mid-bootstrap and racing for the token. The endpoint is **not** rate-limited (login limiter only covers `/login`, `/select-org`, `/invitations/redeem`).
- **Remediation**: Wire bootstrap-state probe through a per-IP rate limiter (no email key). Optional: add a small latency-pad similar to `sso/discoverer.go` so timing reveals nothing.

### M-5. `/v1/sso/discover` and `/v1/auth/bootstrap/state` not rate-limited

- **Location**: `services/api/internal/middleware/ratelimit.go:14-15`, `:46-48` (org-keyed limiter — no public endpoints); `services/api/internal/auth/ratelimit.go:65-71` (login limiter — only covers login/select-org/invitations-redeem).
- **Description**: `/v1/sso/discover` has the latency-pad mitigation (constant-shape response), but no rate limit. An unauthenticated attacker can hammer it at unlimited rate to enumerate verified domains. `/v1/auth/bootstrap/state` is similarly uncovered.
- **Remediation**: Add per-IP rate limit (`LoginRateLimiter`-shaped, IP-only key) on both endpoints. 30/min/IP is a reasonable starting cap.

### M-6. SSO callback does not check `azp` when `aud` is multi-valued

- **Location**: `services/api/internal/sso/oidc.go:215-217`, `:320-337`.
- **Description**: When `aud` is a JSON array, `audienceMatches` returns true if any element equals the configured `OIDCClientID`. OIDC §3.1.3.7 requires that for multi-aud tokens, the client `SHOULD` verify `azp == client_id`. AxiaOps does not.
- **Impact**: An IdP that issues a token with `aud: ["client-A","client-B"]` and `azp: "client-A"` to client-A — that token is intended only for client-A's verification context, but AxiaOps as client-B accepts it. Real-world impact low (few IdPs issue multi-aud ID tokens), but worth closing.
- **Remediation**: After `audienceMatches`, if the `aud` claim is a slice of len > 1, additionally read `azp` and require `claims["azp"] == conn.OIDCClientID`.

### M-7. Sessions cap is best-effort — "11th login" can briefly exceed

- **Location**: `services/api/internal/auth/session.go:160-175`, `:340-381`.
- **Description**: `MintSession` inserts the new row first, then runs `enforceCap` in the same call. If the cap-revoke step fails (PG transient or cache inconsistency), a leftover 11th+ session persists until the 5-minute sweep. The author flags this at `:167`.
- **Impact**: Window where a user has more concurrent sessions than the documented contract allows. Short window; modest impact.
- **Remediation**: Either fold the cap-enforcement into the same transaction as the insert (matches the documented guarantee), or update user-facing docs to reflect "best-effort" semantics.

### M-8. CSRF defence relies entirely on SameSite=Lax + same-origin

- **Location**: `services/api/internal/auth/cookie.go:77`. No CSRF token system.
- **Description**: SameSite=Lax blocks cross-site POST/PATCH/DELETE — that's the primary mitigation. None of the GET endpoints are state-changing, so SameSite-Lax-permitted top-level GET navigation is fine. POST/PATCH/DELETE on `/v1/accounts`, `/v1/dismissals`, `/v1/users/me`, `/v1/organizations/me` rely entirely on SameSite=Lax. Modern browsers default Lax, but older/embedded webviews don't, and the cookie is non-Strict by design (OOB-redemption flow).
- **Impact**: A user on a non-Lax-defaulting browser on a malicious site could be CSRFed into POSTing to `/v1/accounts/{id}/scan` or `DELETE /v1/users/me`. Not an immediate exploit but a brittle reliance.
- **Remediation**: Add an origin-bound CSRF token for state-changing methods. Simplest pattern: `X-CSRF-Token` header on every mutating request, value pinned to a `csrf_token` second cookie issued at session-mint time. Same-origin XHR can read the cookie; cross-origin cannot.

### M-9. `/v1/auth/invitations/preview` discloses `existing_user_name`

- **Location**: `services/api/internal/auth/handler.go:820-887`.
- **Description**: A token holder learns whether the invited email is an existing AxiaOps user globally and that user's display name. Per-IP rate-limited. Token is the capability, but once a token email is forwarded, the response discloses cross-org info. The `existing_user_name` field is mostly cosmetic UX.
- **Remediation**: Acceptable trade — design requires the existing-user boolean to pick the right form mode. Consider stripping `existing_user_name` if the dashboard can render "Welcome back" without a name. Document the disclosure in `docs/invitation-flow.md`.

### M-10. Audit log is RLS-isolated but not tamper-evident

- **Location**: `services/api/internal/audit/audit.go`; `services/shared/storage/postgres/postgres.go:1448-1457`.
- **Description**: Plain INSERT into `audit_log`. The app user has full write access; RLS prevents cross-org reads/writes. No hash chain, no signing, no `prev_hash`. An attacker who lands a Postgres session in the API container can rewrite history within their org.
- **Impact**: Forensics-grade integrity gap. The threat model assumes "AxiaOps operator with shell on the API container is trusted," so this is bounded — but only as long as that trust holds.
- **Remediation**: Out of scope for MVP. A per-org audit hash chain (`prev_hash`, `row_hash` keyed at row insert time) closes this. Defer until SOC2/ISO becomes a customer ask.

---

## Low

### L-1. `axiaops_bootstrap_attempts_total` metric leaks bootstrap activity

`services/api/internal/auth/handler.go:258-272`. With H-2, an unauth `/metrics` scraper learns "this install has been bootstrapped" / "is being attacked." Pairs with M-4. Mostly forensic noise.

### L-2. `clientIP` does not strip IPv6 zone identifiers

`services/api/internal/audit/audit.go:107-124`. `net.ParseIP` returns nil for `fe80::1%eth0`. Edge case; benign (rate limit treats as "unknown").

### L-3. `/v1/version` license sub-object exposes `customer_id` to any authenticated user

`services/api/internal/api/handler.go:527-539`. In a multi-org install, every user across every org sees the install-wide license claim. Mostly fine — `customer_id` is a billing-side identifier — but worth restricting to admin+ roles in a future hardening pass.

### L-4. `LicenseDaysRemaining` gauge exposes license countdown via `/metrics`

`services/shared/license/ticker.go`. Pairs with H-2. With unauth `/metrics`, an attacker learns days-until-expiry — useful for timing pressure on a renewal-blocking exploit.

### L-5. AssumeRole session names embed `organization_id`

`services/ingestion/internal/provider/aws/aws.go:152-155`. Documented intent — the customer's CloudTrail surfaces their own org_id, which is a UUID (non-PII). Not a finding; recorded for posterity.

### L-6. `users.email_lower` partial-unique index allows multiple empty-email rows

`services/shared/storage/postgres/migrations/021_native_auth.up.sql:36-38`. Back-compat for Kinde-era sentinel rows. Native-auth users have non-empty emails — no live exposure.

### L-7. JWT library version split — v4 and v5 both present transitively

`services/api/go.sum` shows `golang-jwt/jwt/v4 v4.5.2` and `v5 v5.3.1`. v4 is transitive (likely from `aws-sdk-go-v2` or `keyfunc/v3`). Direct code uses v5 only. v4.5.2 is current and patched.

### L-8. Dashboard SPA `auth/storage.js` legacy localStorage path

`services/dashboard/src/auth/storage.js`, `App.jsx:46`, `:128`. `axiaops_token` localStorage key is DEV_MODE-only. Real auth uses HttpOnly cookie. Dev-only path; stripped from production via `VITE_DEV_MODE=false`.

### L-9. JWKS cache thundering-herd on signature failure

`services/api/internal/sso/oidc.go:140-156` (TODO at `:145`); `services/shared/jwks/jwks.go:55-60`. Concurrent JWKS-eviction-and-refetch calls don't coalesce. Documented; singleflight is the right fix. AxiaOps scale today: not a problem.

---

## Informational

### I-1. CLAUDE.md drift — ingestion `/scan` documented as "Auth: Yes"

See C-1. `services/ingestion/CLAUDE.md` ingestion-flow section also describes "Set AWS credentials as env vars" — actual code uses SDK `StaticCredentialsProvider` (`aws.go:52`), which is more secure. Update the doc.

### I-2. Argon2id parameters are reasonable

`m=64 MiB, t=3, p=2` per `services/api/internal/auth/password.go:29-32`. OWASP ASVS L2 conformant.

### I-3. AES-256-GCM nonce handling is correct

`services/shared/crypto/crypto.go:32-37` — fresh CSPRNG nonce per encrypt, prepended to ciphertext, decrypted by extracting the same prefix. No nonce reuse risk under any caller pattern.

### I-4. Redis-backed cache failures fail open by design

Session cache, login rate limiter, middleware rate limiter all fail open on cache errors. Documented at every site. Trade-off favours uptime over absolute rate-limiting — appropriate for the threat model. Operators must alert on `axiaops_session_cache_errors_total`.

### I-5. Cookie posture is correct

`services/api/internal/auth/cookie.go:36-38`. Host-only (`Domain=""`), `Path=/`, `HttpOnly`, `SameSite=Lax`. `Secure` derived per-request from `X-Forwarded-Proto`. Local docker-compose without TLS produces non-Secure cookies that round-trip; an edge proxy-terminated TLS produces Secure cookies — correct.

### I-6. RLS GUC is transaction-scoped

`services/shared/storage/postgres/postgres.go:76-83`. `set_config('app.organization_id', $1, true)` — `true` makes it tx-scoped. Connection pool reuse will not leak the GUC between transactions on the same connection.

### I-7. `BOOTSTRAP_TOKEN_FILE_PATH` default needs writable `/var/run/axiaops/`

Operators using read-only-root images must set `BOOTSTRAP_TOKEN_FILE_PATH=""`. Documented in CLAUDE.md; surface in deploy/README runbooks.

---

## Executive summary

AxiaOps's authentication and tenant-isolation foundations are well-designed: argon2id with proper PHC encoding; AES-256-GCM with per-call CSPRNG nonces; RLS on every multi-tenant data table (the one omission, `users` — H-1 — closed by migration 035); organization-scoped sessions with capacity limits and write-through cache invalidation; OIDC with PKCE + per-connection JWKS + alg-confusion guards; and a thoughtful B1.7 build-tag posture that strips DEV_MODE from customer binaries. The codebase shows strong security-engineering culture (timing-equalised login, public-suffix-list domain rejection, idempotent state-store consume, provenance-aware JIT).

Three issues need urgent fixes:

1. The ingestion service exposes unauthenticated `/scan` and `/v1/credentials/verify` on the LAN — a single shared-network compromise yields cross-tenant scan execution.
2. A hardcoded `ENCRYPTION_KEY` is committed to `docker-compose.yml` and `deploy/dev.yml` as a `:-` fallback. Anyone with repo-read can decrypt dev-1/dev-2 AWS account secrets.
3. The audit log + SSO callback record attacker-spoofable client IPs (leftmost X-Forwarded-For — the auth package itself fixed this exact bug, but the audit/SSO sites missed it).

A handful of high-severity hardening gaps follow: missing RLS on `users`, public `/metrics`, no HTTPS enforcement on `OIDCDiscoveryURL`, no body-size cap outside auth handlers, and no security headers from the dashboard nginx. None are exploitable by an unauthenticated public attacker today, but together they're the difference between MVP and compliance-audit-ready.

---

## What's solid

- **Password hashing** — argon2id PHC strings; constant-time compare via `crypto/subtle`. (`auth/password.go`)
- **Session model** — cookie carries plaintext, DB stores SHA-256 hash, cache is opaque-key-keyed by the same hash, write-through invalidation on revoke. (`auth/session.go`, `session_cache.go`)
- **OIDC alg-confusion mitigation** — dual-layer: package-level whitelist of asymmetric algs intersected with discovery-doc-published algs, plus `WithValidMethods` on the parser. `none`/`HS256` structurally rejected. (`sso/oidc.go:51-61`)
- **PKCE + nonce + state binding** — state carries `cid` so an attacker can't redirect a state minted for connection A to connection B's callback; nonce mandatory with skip-mode emitting `slog.Warn`. (`sso/state.go`, `sso/oidc_callback.go`)
- **PSL-based domain claim rejection** — `golang.org/x/net/publicsuffix` blocks claiming `gmail.com`, `github.io`, etc. (`sso/domain.go`)
- **Email-domain SSO post-validation** — callback verifies the IdP-asserted email domain matches a verified `sso_domains` row bound to *this* connection. (`sso/oidc_callback.go:228-248`)
- **JIT owner-never-via-claim** — explicit role priority excludes owner; defence-in-depth at `jit.go:126` and DB CHECK at `sso_group_mappings`. (`sso/jit.go:36-91`)
- **Provenance-aware membership reconcile** — SSO callback won't overwrite invitation/manual/scim memberships even on group-claim change. (`sso/jit.go:188-197`)
- **Login rate limiter** — per-IP + per-email + shared budget across `/login` and `/select-org` (attacker can't double cap by alternating). (`auth/ratelimit.go`)
- **`requestIP` correctly handles XFF append behaviour** in the auth path (rightmost token + X-Real-IP fallback). The audit/SSO sites need the same fix — see C-3. (`auth/handler.go:1184-1223`)
- **Build-tag bypass closure** — `devModeEnabled()` is the single seam; production builds zero out both the dev license fixture and the DEV_MODE env-read. (`{api,ingestion}/cmd/devmode_*.go`, `shared/license/embed_*.go`)
- **License RS256-only** — `WithValidMethods(RS256)` plus a keyfunc that re-asserts `*jwt.SigningMethodRSA`. 90-day max grace. (`shared/license/license.go:344-356`)
- **RLS GUC scoped per-tx** — no leak across pool reuse. (`shared/storage/postgres/postgres.go:76-83`)
- **`AuditLogAnonymiseUser`** — defence-in-depth: explicit `WHERE organization_id = $1` AND RLS. (`shared/storage/postgres/postgres.go:1530-1535`)
- **Pre-flight rate limit on `/v1/auth/invitations/preview`** — closes the global-user enumeration channel. (`auth/handler.go:852-864`)

---

## Prioritised remediation roadmap

### Do first (this sprint — critical exposure)

1. **C-1** — Authenticate `/scan` and `/v1/credentials/verify` on ingestion. Shared HMAC token in env, verified at handler entry.
2. **C-2** — Remove the hardcoded `ENCRYPTION_KEY` fallback. Rotate dev-1/dev-2 keys. `git log -p -S` to date the exposure window.
3. **C-3** — Replace `clientIP` (`audit.go:107`) and `callbackClientIP` (`oidc_callback.go:547`) with the auth-package-correct version (export from auth or move to a shared `httpip` helper).

### Do soon (next sprint — defence-in-depth gaps)

4. ~~**H-1** — Add RLS to `users` (mirrors memberships).~~ ✅ Done — migration 035.
5. **H-2** — Auth-gate `/metrics` (bearer token or separate listener).
6. **H-3** — Reject non-HTTPS `oidc_discovery_url` / `idp_metadata_url`.
7. **H-4** — Promote `decodeJSON` to a shared helper; apply 64 KiB cap + DisallowUnknownFields globally.
8. **H-5** — Add security headers (CSP / HSTS / X-Frame-Options / X-Content-Type-Options / Referrer-Policy) at `services/dashboard/nginx.conf`.

### Can defer (next quarter — quality bars)

9. **M-1 .. M-9** — Bootstrap env-clear post-consume; atomic GETDEL on SSO state; CORS_ORIGIN startup warning; rate-limit `/v1/sso/discover` and `/v1/auth/bootstrap/state`; SSO `azp` check; sessions-cap exactness; CSRF token system; tighten invitation preview disclosure.
10. **M-10** — Audit-log hash chain, only when SOC2/ISO compliance becomes a customer ask.

---

## Files referenced

Primary citation paths (verified by reading at audit time):

- `services/ingestion/cmd/main.go` — unauthenticated `/scan`, `/v1/credentials/verify` registrations (C-1)
- `docker-compose.yml`, `deploy/dev.yml` — hardcoded ENCRYPTION_KEY (C-2)
- `services/api/internal/audit/audit.go`, `services/api/internal/sso/oidc_callback.go` — leftmost-XFF bug (C-3)
- `services/api/internal/auth/handler.go` — auth handlers + correct `requestIP` reference for C-3
- `services/api/internal/auth/cookie.go`, `session.go`, `password.go`, `ratelimit.go` — auth/session core
- `services/api/internal/middleware/auth.go`, `auth_native.go`, `sso_enforcement.go`, `ratelimit.go`, `authz.go`
- `services/api/internal/sso/oidc.go`, `oidc_callback.go`, `state.go`, `initiate.go`, `discover.go`, `discoverer.go`, `domain.go`, `connector.go`, `handler.go`, `jit.go`
- `services/api/internal/api/handler.go`, `services/api/internal/serverbuild/build.go`
- `services/shared/crypto/crypto.go`
- `services/shared/license/license.go`, `embed.go`, `embed_dev.go`, `embed_production.go`
- `services/shared/jwks/jwks.go`
- `services/shared/storage/postgres/postgres.go`, `native_auth.go`, `invitations.go`, `sso.go`
- `services/shared/storage/postgres/migrations/001_initial.up.sql`, `014_audit_log.up.sql`, `015_memberships.up.sql`, `017_pending_memberships.up.sql`, `021_native_auth.up.sql`, `022_sso_core.up.sql` — RLS coverage (H-1)
- `services/shared/queue/sync/sync.go` — sync queue ingestion call (C-1)
- `services/dashboard/nginx.conf` — public-edge nginx, headers gap (H-5)
- `services/dashboard/src/api/client.js`, `src/auth/storage.js`, `src/App.jsx`, `src/screens/NativeLoginScreen.jsx`
- `services/api/cmd/main.go`, `services/api/cmd/devmode_dev.go`, `services/api/cmd/devmode_production.go`
- `services/ingestion/cmd/devmode_dev.go`, `services/ingestion/cmd/devmode_production.go`, `services/ingestion/cmd/verify.go`
- `.gitlab-ci.yml`, `deploy/staging.yml`, `deploy/preview.yml`, `deploy/demo.yml`
