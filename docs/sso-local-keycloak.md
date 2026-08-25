# SSO local end-to-end test against Keycloak

A runbook for exercising the AxiaOps OIDC RP (Phase B2) end-to-end against a real
external IdP — Keycloak — without standing up a staging deployment or wiring a
cloud IdP. Faster than the `make test-integration-sso` automated path (which
uses an in-process minimal RS256 issuer) for click-through validation of the
admin UX, the email-blur discovery flow, the OIDC ceremony, and JIT membership
provisioning.

> SAML is **Phase C** — not implemented. This runbook is OIDC-only.

## Prerequisites

- A reachable Keycloak instance (any version ≥ 20). The AxiaOps API container
  must be able to fetch `<keycloak>/realms/<realm>/.well-known/openid-configuration`.
- Local AxiaOps stack via `make start-staging` (full Docker stack with native
  auth on plain HTTP at `http://localhost:8082`).
- Direct access to the AxiaOps Postgres container — the runbook bypasses DNS
  TXT verification with a direct `UPDATE` on `sso_domains`.

## 1. Keycloak setup

1. **Realm**: pick or create one (e.g. `axiaops-test`). Note the name.

2. **Client**:
   - Client ID: `axiaops-rp` (you'll paste this into AxiaOps).
   - Client authentication: **on** — confidential client; Keycloak mints a secret.
   - Authentication flow: **Standard flow on**, others off.
   - Valid redirect URIs: `http://localhost:8082/v1/sso/oidc/callback`
     - Single literal URI — connection identity flows through the `state`
       parameter, not the URL path. The legacy `/v1/sso/oidc/<cid>/callback`
       form still resolves for one release (a deprecation
       window); add it as an additional redirect URI only if you're
       upgrading an existing AxiaOps install whose initiate side has not
       yet been redeployed.
   - Web origins: `http://localhost:8082`.
   - **Save**, then in the **Credentials** tab copy the secret. You'll paste it
     into AxiaOps.

3. **Test user**:
   - Users → Add user → set email (e.g. `alice@example.com`) → Credentials → Set
     password (turn off "Temporary").

4. **(Optional) Groups for JIT role mapping**:
   - Groups → create `g-engineering` (will map to admin) and `g-support` (will
     map to member). Add the test user to one.
   - Client → Client scopes → `axiaops-rp-dedicated` → Add mapper →
     **Group Membership**:
     - Token claim name: `groups`
     - Full group path: **off** (you want bare names like `g-engineering`, not
       `/g-engineering` — `JITResolveRole` does an exact-match lookup).
     - Add to ID token: **on**. Add to userinfo: optional.

## 2. Reachability sanity check

The AxiaOps API container must reach Keycloak's discovery URL. From your host:

```bash
curl -fsSL https://your-keycloak-host/realms/axiaops-test/.well-known/openid-configuration | head
```

If Keycloak resolves to `localhost` from your host but a different IP from
inside Docker, the API container won't reach it. Use the LAN/host IP or
`host.docker.internal` (Docker for Mac/Windows) when filling in the discovery
URL on the AxiaOps side. Get the exact URL right once and use it for both the
admin form and any troubleshooting.

## 3. AxiaOps setup

```bash
make start-staging      # http://localhost:8082, AUTH_PROVIDER=native
```

Bootstrap the first owner — token from the API container logs or
`BOOTSTRAP_TOKEN_FILE_PATH` (default `/var/run/axiaops/initial_setup_token`).
See [`docs/native-auth-bootstrap.md`](native-auth-bootstrap.md) for the full
bootstrap walkthrough.

In the dashboard, **Settings → SSO**:

1. **Connections → Add OIDC**:
   - Label: `Keycloak (test)`
   - OIDC Discovery URL: the URL from §2 (must be reachable from inside the
     API container)
   - Client ID: `axiaops-rp`
   - Client Secret: the one Keycloak minted
   - Default role: `viewer`
   - Save as **draft** first → copy the connection ID from the URL → flip to
     **active**.

2. **(Optional) Update the Keycloak redirect URI**: the cid-less
   `/v1/sso/oidc/callback` form set in §1 covers every connection, so
   nothing needs to change here. Skip unless you specifically want to
   enumerate per-connection callback URIs (rare).

3. **Domains → Add `example.com`** (or whatever domain matches your test
   user's email).
   - DNS TXT verification can't succeed for `example.com` from a local stack.
     Bypass it directly in Postgres:

     ```bash
     docker exec -i axiaops-postgres-1 psql -U axiaops_owner axiaops <<'SQL'
     UPDATE axiaops.sso_domains
     SET status='verified', verified_at=NOW(), expires_at=NOW() + INTERVAL '365 days'
     WHERE domain='example.com';
     SQL
     ```

   - The bypass is **dev-only**. In real deployments, the domain row must be
     verified by the DNS TXT flow — that's the anti-spoofing boundary
     (design doc §11.1).

4. **Group Mappings** (only if you set up groups in §1.4):
   - `g-engineering` → `admin`
   - `g-support` → `member`

5. **Enforcement** — leave at `optional` for the first test. Set to `required`
   only after you've confirmed SSO works end-to-end; otherwise you can lock
   yourself out of the dashboard if SSO breaks (the only escape is
   `/v1/auth/logout`, which is in the enforce-skip-set).

## 4. Test the round-trip

Log out of the dashboard. Go to `/login`, enter `alice@example.com`:

1. Email-blur fires `GET /v1/sso/discover?email=alice@example.com` → response
   `{has_sso:true, redirect_url: ".../v1/sso/oidc/<conn-id>/initiate"}`.
2. Dashboard redirects to the initiate URL.
3. Initiate handler 302s to Keycloak's authorize endpoint with PKCE
   `code_challenge_method=S256`, opaque `state`, fresh `nonce`.
4. Authenticate as alice in Keycloak.
5. Keycloak redirects back to AxiaOps callback with `code` + `state`.
6. Callback validates state (single-use), exchanges code for tokens,
   validates the ID token (alg-confusion guard rejects `none`/`HS256`,
   JWKS fetched via the shared package, issuer/audience/nonce checked),
   confirms the email's domain is verified for THIS connection AND THIS
   org (anti-spoofing), runs JIT (group→role precedence; owner-never-via-JIT
   pin), mints a session with `auth_mode='sso'`.
7. Browser lands on `/` (the SPA's post-login landing) with the AxiaOps session cookie.

## 5. What to watch in API logs

Successful login:

```
sso: callback: load connection cid=<conn-id>
sso: callback: code exchange ok
sso: callback: domain verified
audit: SSO_LOGIN_SUCCEEDED user_id=<uuid> connection_id=<conn-id> redeemed_invitation=false protocol=oidc
audit: SSO_JIT_PROVISIONED  (first login) or SSO_JIT_ROLE_UPDATED (re-login, role changed)
```

Failure modes write `audit: SSO_LOGIN_FAILED` with a coarse reason. The codes
are deliberately coarse — detailed reasons go to `slog` only, not the audit
trail (architect §11 logging discipline).

| Reason code | Cause |
|---|---|
| `state_invalid` | Unknown / expired / already-consumed state token |
| `cid_mismatch` | State was minted for connection A, callback hit on B |
| `code_exchange_failed` | Token endpoint rejected the auth code or the redirect URI didn't match |
| `id_token_invalid` | Signature, alg, issuer, audience, nonce, or `iat` failed validation |
| `domain_unverified` | Email's domain isn't in `sso_domains` for THIS connection AND THIS org |
| `cross_connection_domain` | Domain is verified, but for a different connection |
| `invitation_redeem_failed` | Hard-fail on `pending_memberships` redemption — admin role choice would have been silently bypassed |
| `jit_failed` | `JITProvisionMembership` returned an error (`ErrJITOwnerForbidden`, store error, …) |
| `mint_session_failed` | Session-table insert failed |

## 6. Common gotchas

- **`redirect_uri` mismatch**: Keycloak does exact-match. Either wildcard it
  in the client config (`*/callback`) or update the redirect URIs after you
  know the connection ID.
- **`PUBLIC_HOST` empty**: API logs warn `PUBLIC_HOST is empty —
  IdP-registered redirect_uri will not match the URL the callback receives`.
  Set `PUBLIC_HOST=http://localhost:8082` in `services/api/.env` for staging.
- **Discovery URL not reachable from API container**: symptom is a 503 on
  `/v1/sso/oidc/<cid>/initiate` with log `discovery unavailable`. Use a
  host-reachable URL (LAN IP or `host.docker.internal`), not `localhost`.
- **`alg=HS256` ID token**: the alg-confusion guard hard-rejects HS256 and
  `none` regardless of what the IdP advertises. Keycloak's default is RS256;
  if you've changed the realm signing algorithm, change it back.
- **`groups` claim missing from ID token**: JIT falls back to
  `connection.default_role` (viewer in the runbook setup). Verify the claim by
  decoding the ID token (`jwt.io`) or hitting Keycloak's userinfo endpoint
  with the access token. Common fix: the Group Membership mapper in §1.4 has
  "Add to ID token" off by default.
- **Cyrillic / homoglyph email domain**: AxiaOps lookup is byte-exact on
  `lower(domain)`. A Unicode lookalike like `aсme.com` (Cyrillic с) won't
  match `acme.com`. This is the org-takeover defence pinned by
  `services/api/internal/sso/discover_test.go`'s domain-confusion fuzz —
  not a bug.
- **Domain row stale after 365d**: harmless for a test; if `status` flips to
  `stale`, re-run the SQL update from §3.3. Production handles this via the
  24h `ssoSweep` ticker.

## 7. What you'll have proven

A successful round-trip exercises:

- `/v1/sso/discover` — pre-auth, constant-shape, latency-floored (architect N4 fuzz)
- `/v1/sso/oidc/<cid>/initiate` — PKCE + state + nonce ceremony
- `/v1/sso/oidc/callback` — full ID-token validation chain (alg-
  confusion, issuer, audience, nonce, anti-spoofing domain check), JIT
  membership, session minting with `auth_mode='sso'`, audit row
- `/v1/me` — returns the SSO-provisioned user with the JIT-resolved role
- (If group mappings configured) `JITResolveRole` precedence: admin > member > viewer
- (If two browsers / two users tested) The OIDC state is single-use — replay
  fails at consume time with `state_invalid`

## 8. After the test

To return the dashboard to a clean state (e.g. before re-running the test
with different group mappings or a different default role):

```bash
docker exec -i axiaops-postgres-1 psql -U axiaops_owner axiaops <<'SQL'
-- Wipe alice's user + memberships + sessions so the next login goes through
-- the JIT-create branch again rather than JIT-update.
DELETE FROM axiaops.users WHERE email = 'alice@example.com';
SQL
```

Connection, domain, and group mappings persist — keep them around for the
next iteration.

## 9. Test report template

Copy the block below into a fresh document or a scratch comment on the
relevant MR / issue, fill it in as you go, and save it as evidence the
real-IdP round-trip held. Probes are ordered cheapest-first — if probe 1
(Discovery) fails, the rest can't run, so triage there before continuing.

```markdown
# SSO local end-to-end test — <YYYY-MM-DD>

- **Tester**: <your name>
- **AxiaOps SHA**: $(git rev-parse --short HEAD)  on  <branch>
- **Keycloak**: <version>  reachable at <URL>
- **AxiaOps stack**: make start-staging  (PUBLIC_HOST=<value from .env>)
- **Test domain**: example.com    Test user: alice@example.com
- **Test groups**: g-engineering→admin, g-support→member  (or "none")

## Probes

### Probe 1 — Discovery endpoint reachable
- [ ] `curl <discovery URL>` returns 200 with valid JSON from the host
- [ ] Same URL is reachable from inside the API container
      (`docker exec axiaops-api-1 curl -fsSL <url>`)
- Notes:

### Probe 2 — Connection + domain config persists
- [ ] Connection saved as draft, then activated; reflected in
      `Settings → SSO → Connections` list
- [ ] Domain row inserted; `UPDATE sso_domains` to `verified`
      succeeds
- [ ] (Optional) Group mappings saved; PUT replaced full set as expected
- Notes:

### Probe 3 — /v1/sso/discover identifies SSO domain
- [ ] `curl 'http://localhost:8082/v1/sso/discover?email=alice@example.com'`
      returns `{has_sso: true, redirect_url: "..."}`
- [ ] `curl 'http://localhost:8082/v1/sso/discover?email=bob@unknown.example'`
      returns `{has_sso: false}` (no `redirect_url` key)
- [ ] Wall-clock latency is ≥ ~5ms in both cases (timing-channel pin)
- Notes:

### Probe 4 — First-time SSO login (JIT create, default-role path)
- [ ] Email-blur on `/login` redirects to Keycloak authorize URL
- [ ] Keycloak shows AxiaOps client; auth as alice succeeds
- [ ] Browser lands on `/` with the AxiaOps session cookie set
- [ ] Audit log shows `SSO_LOGIN_SUCCEEDED` + `SSO_JIT_PROVISIONED`
- [ ] `memberships.provisioned_via='jit'`, role matches expectation
      (default_role if no groups; group-mapped role otherwise)
- Notes:

### Probe 5 — Re-login (idempotent JIT, role unchanged → no audit noise)
- [ ] Log out, log in again as alice
- [ ] `SSO_LOGIN_SUCCEEDED` audited again
- [ ] `SSO_JIT_PROVISIONED` and `SSO_JIT_ROLE_UPDATED` are NOT
      audited (no role change → no JIT event)
- Notes:

### Probe 6 — Group change updates role on next login
- [ ] In Keycloak, move alice from `g-support` to `g-engineering`
      (or vice versa)
- [ ] Log out, log in as alice
- [ ] Audit log shows `SSO_JIT_ROLE_UPDATED`
- [ ] `memberships.role` reflects the new group's mapping
- [ ] `provisioned_via` is still `'jit'` (not flipped to manual)
- Notes:

### Probe 7 — Invitation precedence over JIT
- [ ] As an admin, invite `bob@example.com` at role `viewer` via
      Settings → Members → Invite
- [ ] In Keycloak, create user `bob@example.com` and put bob in
      `g-engineering` (would JIT-resolve to admin)
- [ ] Log in as bob via SSO
- [ ] `memberships.role='viewer'` (invite wins over JIT admin)
- [ ] `provisioned_via='invitation'`
- [ ] `pending_memberships` row consumed (DELETED)
- Notes:

### Probe 8 — Anti-spoofing: unverified-domain email rejected
- [ ] In Keycloak, change alice's email to `alice@unverified.example`
- [ ] Log in via SSO
- [ ] Browser lands on `/login?error=auth_failed` (not `/`)
- [ ] Audit log shows `SSO_LOGIN_FAILED` reason `domain_unverified`
- [ ] No session cookie set
- Notes:

### Probe 9 — enforcement=required blocks password sessions
- [ ] Settings → SSO → Enforcement → set to `required`
- [ ] Log out, log in as a NATIVE-PASSWORD user (e.g. the
      bootstrapped owner if their email is on the verified domain)
- [ ] Any authenticated request returns `403 {"error":"sso_required"}`
- [ ] `/v1/auth/logout` still works (skip-path)
- [ ] Reset enforcement to `optional` before continuing
- Notes:

### Probe 10 — Open-redirect defence
- [ ] `curl '...initiate?return_to=https://evil.com'` — login completes,
      lands on `/` (not evil.com)
- [ ] `curl '...initiate?return_to=//evil.com'` — same
- [ ] `curl '...initiate?return_to=/dashboard/zombies'` — login completes
      and lands on `/dashboard/zombies` (legitimate path preserved). Note:
      this URL still resolves through the SPA's `/*` catch-all → blank
      page until/unless we wire a /dashboard route. The assertion is that
      the open-redirect guard PRESERVES the path, not that the page renders.
- Notes:

### Probe 11 — IdP-session bleed defence (`prompt=login` pin)
Pins the fix for the silent-identity-substitution bug: without `prompt=login`
on the authorize URL, a logged-out user typing a different email at /login
silently re-auths as whoever still owns the Keycloak realm cookie.
- [ ] Log in as alice via SSO. Confirm /v1/me shows alice.
- [ ] Click logout in AxiaOps. Confirm cookie is cleared in DevTools.
- [ ] At /login, type `bob@example.com` (a different Keycloak user on the
      same verified domain).
- [ ] Keycloak **MUST** render its login form — not silently 302 back to
      AxiaOps. The username field is pre-filled with bob's email.
- [ ] Enter bob's password → land in dashboard as bob (not alice).
      `/v1/me` returns bob's user_id and email.
- [ ] Repeat with the form back-buttoned / cancelled → no AxiaOps session
      minted, stay at /login.
- Negative regression check: `curl -s '<initiate_url>'` and decode the
      Location header — the authorize URL **must** carry `prompt=login`.
      Pinned by `services/api/internal/sso/initiate_test.go`.
- Notes:

### Probe 12 — SSO redirect button-flash regression
Pins commit `b267c20`. After email-blur SSO discovery returns
`has_sso=true`, the dashboard calls `window.location.assign(redirect_url)`.
React paints first, then the browser navigates — without the timeout-gated
reveal, a manual "Continue to SSO" button flashed for that paint window.
The fix gates the button behind a 1500ms reveal so a fast redirect never
shows it.
- [ ] At /login, type a Keycloak-domain email (e.g. `alice@example.com`).
      The email-blur discover should fire on Tab / click-out.
- [ ] During the redirect, the form should show a **spinner + "Redirecting
      to your single sign-on provider…"** hint and the **"Sign in with
      password instead"** escape hatch.
- [ ] **The "Continue to SSO" button MUST NOT be visible** during a
      normal-speed redirect (< ~1.5s). If it appears within the first
      second, the reveal timer regressed.
- [ ] To force the slow-network path: in DevTools → Network → enable
      "Slow 3G" throttling, then repeat. After ~1.5s the "Continue to
      SSO" button reveals as a manual fallback. This is the desired
      fallback behaviour.
- [ ] With throttling off, the "Sign in with password instead" link is
      always present (escape hatch must never be timer-gated).
- Notes:

### Probe 13 — Profile page shows org name (regression)
Pins commit `5fe461a`. Under native auth there's no JWT, so the legacy
`parseJwt(getToken()).org_name` source is empty. Without the fix, Profile
fell through to `me.organization_id` and rendered the raw UUID.
- [ ] After SSO login, navigate to **Profile** (avatar menu → My Profile).
- [ ] The **Organization** field shows the friendly name (e.g.
      "AxiaOps") — NOT the UUID.
- [ ] Source: `/v1/me` response body. In DevTools → Network → /v1/me, the
      response carries `organization.name` populated. The Profile page
      reads `me?.organization?.name` first, then falls back to the JWT-
      derived `orgName`, then `me?.organization_id`, then `'—'`.
- [ ] If `/v1/me` legitimately fails to populate `organization` (e.g.
      best-effort `GetOrganizationByID` errored), fallback chain still
      renders something — never blanks the field.
- Notes:

## Bugs found

| # | Severity | Probe | Description | Fix commit |
|---|---|---|---|---|
|   |   |   |   |   |

## Decision

- [ ] All probes green → safe to merge `feat/sso → develop`
- [ ] Bugs found, fixed in <commits>, re-tested → safe to merge
- [ ] Bugs found, deferred → describe risk and gate the merge

Sign-off: <name> on <date>
```

The 10 probes map to the §5.5 acceptance items already pinned by automated
tests — running them against a real IdP is the parity check between mock and
real. If a probe fails, the corresponding automated test is the place to
look first: the bug is almost always a mock gap rather than a real-IdP
incompatibility.
