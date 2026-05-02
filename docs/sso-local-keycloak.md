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
   - Valid redirect URIs: `http://localhost:8082/v1/sso/oidc/*/callback`
     - The wildcard is required because the connection ID is in the path and
       Keycloak does exact-match. Replace with the literal connection ID once
       you've created the AxiaOps connection (step 3.1) if you'd rather avoid
       wildcards.
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

2. **(Optional) Update the Keycloak redirect URI** to the exact callback if
   you don't want a wildcard:
   `http://localhost:8082/v1/sso/oidc/<conn-id>/callback`

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
7. Browser lands on `/dashboard` with the AxiaOps session cookie.

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
- `/v1/sso/oidc/<cid>/callback` — full ID-token validation chain (alg-
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
