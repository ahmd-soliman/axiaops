# SSO local end-to-end test against Microsoft Entra ID

A runbook for exercising the AxiaOps OIDC RP (Phase B2) end-to-end against
Microsoft Entra ID — the IdP most likely to land on a self-hosted customer's
desk first. Click-through validation of the admin UX, the email-blur discovery
flow, the OIDC ceremony, and JIT membership provisioning.

Companion to [`sso-local-keycloak.md`](sso-local-keycloak.md). Same shape,
Entra-specific quirks called out where they bite.

> SAML is **Phase C** — not implemented. This runbook is OIDC-only.

## Prerequisites

- A reachable Entra tenant. **Free `*.onmicrosoft.com` is fine** — same iss
  shape, same JWKS, same group-claim behaviour as a corporate tenant. Get one
  at <https://aka.ms/CreateTenant> (cookie-blocking gotcha below).
- Local AxiaOps stack via `make start-staging` (full Docker stack with native
  auth on plain HTTP at `http://localhost:8082`). `make start-staging` sets
  `DEV_MODE=false` on the api container — required for the SSO ceremony
  routes to register at all (`serverbuild.ComposeServer` only wires them
  when native auth is active). If you've manually `docker compose up`'d
  the api without that override, the ceremony 404s.
- `PUBLIC_HOST=http://localhost:8082` (the docker-compose default — only
  override if you're running on a different host/port). Entra accepts
  `http://localhost` as a redirect URI by exception, so no HTTPS or tunnel
  is needed for local testing.
- Direct access to the AxiaOps Postgres container — the runbook bypasses DNS
  TXT verification with a direct `UPDATE` on `sso_domains`.

## 1. Get a free Entra tenant

If you already have a tenant you're allowed to register apps in, skip to §2.

1. Open <https://aka.ms/CreateTenant>. Sign in with any Microsoft account.
2. **Create a tenant** → **Microsoft Entra ID** → fill in:
   - Organization name: `<yourname> Test`
   - Initial domain: `<yourname>test` → produces `<yourname>test.onmicrosoft.com`
   - Region: closest one (irrelevant for testing)
3. You're now Global Admin of `<yourname>test.onmicrosoft.com` with your own
   tenant_id.

### Common blockers on the create-tenant flow

The portal uses MSAL silent token renewal in iframes; modern browser defaults
break it. Symptoms (verbatim error strings to grep on):

- `no_tokens_found` (MSAL JS error) — third-party storage blocked. Allow
  third-party cookies for `[*.]microsoftonline.com`, `[*.]microsoft.com`,
  `[*.]office.com`, `[*.]live.com`, then retry.
- `AADSTS50058: login_required ... silent sign-in request was sent but no
  user is signed in` — same root cause; the iframe couldn't carry your
  cookies. Same fix.
- "You don't qualify for a sandbox subscription" on the M365 Developer
  Program signup — Microsoft tightened eligibility in 2024. Use
  `aka.ms/CreateTenant` directly instead; the plain create-tenant flow has
  no eligibility check.

Chrome / Edge in a fresh profile is the most reliable. Safari / Brave / Firefox
strict mode all trip the silent-renewal flow.

## 2. Entra app registration (in your test tenant)

1. **Microsoft Entra ID** → **App registrations** → **+ New registration**.
2. Name: `AxiaOps SSO Test`.
3. Supported account types: **Single tenant** (your test tenant only).
4. Redirect URI: leave blank — you need the AxiaOps connection ID first
   (filled in §4).
5. Register → on the **Overview** page, capture two GUIDs:
   - **Application (client) ID** — goes in AxiaOps "Client ID".
   - **Directory (tenant) ID** — goes in AxiaOps "Tenant ID" AND inside the
     Discovery URL.

   The third GUID on this page (**Object ID**) is internal Entra database
   bookkeeping. **Ignore it.** Microsoft labels four different things "ID";
   easy to mix them up.

6. **Certificates & secrets** → **+ New client secret** → 6 months → copy
   the **Value** column **immediately**.

   ⚠️ The secret has TWO columns: **Value** (the actual cryptographic secret,
   ~40 chars with `~`/`.`/letters/digits) and **Secret ID** (a GUID that's
   Microsoft's internal handle for the secret record — **never used by any
   OIDC client**). If you paste the Secret ID into AxiaOps's "Client secret"
   field by mistake, you'll see AADSTS700016 because the actual secret never
   reached AxiaOps. The Value column is hidden after first display; if you
   missed it, generate a new secret.

7. **Token configuration** → **+ Add groups claim** → tick **Security
   groups** → ID type **Group ID** → Save. Required only if you want to
   exercise group→role JIT mapping; skip for a default-role-only test.

   ⚠️ Leave **Emit groups as role claims** **unchecked**. Checked routes the
   group IDs into the `roles` claim instead of `groups`. AxiaOps reads
   `groups` (`services/api/internal/sso/oidc_callback.go:464`) — with this
   toggle on, every JIT login silently falls through to `default_role=viewer`
   because `token_groups=[]` from AxiaOps's perspective, even though the IDs
   are in fact in the token under a different key.

   ⚠️ The per-token-type panel (click into the claim row after saving)
   exposes ID / Access / SAML toggles. AxiaOps validates the **ID** token
   only — confirm ID is checked. If only Access is ticked, same symptom as
   above.

8. (Optional) Create a test user + group:
   - **Users** → **+ New user** → **Create new user**:
     - User principal name: `alice@<yourtenant>.onmicrosoft.com`
     - Untick auto-generate password, set one manually.
   - **Groups** → **+ New group** (Security) → "Engineering" → add Alice →
     copy the group's **Object ID**.
   - Sign Alice in once at <https://myaccount.microsoft.com> to clear the
     "must change password" prompt.

## 3. AxiaOps setup

```bash
make start-staging      # http://localhost:8082, native auth on
```

Bootstrap the first owner — token from the API container logs or
`BOOTSTRAP_TOKEN_FILE_PATH` (default `/var/run/axiaops/initial_setup_token`).
See [`native-auth-bootstrap.md`](native-auth-bootstrap.md) for the full flow.

In the dashboard, **Settings → SSO**:

1. **Connections → + New** (OIDC):

   | AxiaOps field | Value | Notes |
   |---|---|---|
   | Protocol | OIDC | only option today |
   | Label | `Entra Test` | admin display + audit |
   | Status | active | |
   | Enforcement | **optional** | DO NOT pick `required` until the round-trip works — would lock out password login on a misconfig |
   | Default role | viewer | JIT fallback when no group mapping hits |
   | Force re-authentication | leave on | emits `prompt=login` to Entra; default true |
   | **Discovery URL** | `https://login.microsoftonline.com/<tenant_id>/v2.0/.well-known/openid-configuration` | replace `<tenant_id>` with the Directory (tenant) ID from §2.5 |
   | **Client ID** | from §2.5 (Application (client) ID) | |
   | **Client secret** | from §2.6 (the **Value**, NOT the Secret ID) | |
   | **Tenant ID** | the same `<tenant_id>` GUID | populates `oidc_tenant_id`; the per-connection anti-cross-tenant validator (design doc §724) needs this to reject tokens from other Entra tenants signed by Microsoft's shared keys |

   The tenant_id appears in **two** places — once inside the Discovery URL
   string (so the OIDC RP fetches the right `*/v2.0/.well-known/...` doc) and
   once in the standalone Tenant ID field (so the validator binds tokens to
   this tenant). Same GUID, two consumers.

   Save → capture the **connection ID** (`cid`). The dashboard doesn't
   render it as a column today, so dig it out of devtools: open the Network
   tab, find the `POST /v1/sso/connections` (create) or `GET /v1/sso/connections`
   (list) response, the `id` field in the JSON body is the cid (a UUID).
   Exposing it in the UI is tracked as a follow-up.

2. **Domains → + Add** `<yourtenant>.onmicrosoft.com`.

   DNS TXT verification on a `.onmicrosoft.com` you don't own is impossible.
   Bypass with a direct UPDATE — **dev only**:

   ```bash
   docker exec -i axiaops-postgres psql -U axiaops_owner axiaops <<'SQL'
   UPDATE axiaops.sso_domains
     SET status = 'verified', verified_at = NOW(),
         expires_at = NOW() + INTERVAL '365 days'
     WHERE domain = '<yourtenant>.onmicrosoft.com';
   SQL
   ```

   ⚠️ The `WHERE` clause in `GetVerifiedSSODomainByName`
   (`services/shared/storage/postgres/sso.go:410`) only checks
   `status = 'verified'` — `verified_at` is metadata that's not part of the
   filter. The UPDATE above sets both for completeness, but the load-bearing
   field is `status`. If you set only `verified_at`, `/v1/sso/discover` will
   keep returning `has_sso:false`.

3. (Optional) **Group Mappings** — only if you set up Engineering in §2.8.
   Map the group's **Object ID** (not display name) → AxiaOps `admin`.

## 4. Register the redirect URI back in Entra

Now that you have the AxiaOps cid:

1. Entra app → **Authentication** → **+ Add a platform** → **Web** (or
   **+ Add URI** if a Web platform already exists).
2. Redirect URI:
   ```
   http://localhost:8082/v1/sso/oidc/callback
   ```
   - `http` (not https) — Entra's localhost exception.
   - **No cid in the path.** Connection identity flows through the
     `state` parameter — one redirect URI per AxiaOps host, regardless of
     how many SSO connections you create. The legacy
     `/v1/sso/oidc/<cid>/callback` form still resolves for one release as
     a deprecation window, but new app registrations
     should use the cid-less form.
3. Do **not** tick the implicit-flow checkboxes (Access tokens / ID tokens) —
   we use authorization code + PKCE only.
4. **Save**.

## 5. Test the round-trip

Open an **incognito** window (so no AxiaOps session bleeds in). Go to
`http://localhost:8082/login`, type `alice@<yourtenant>.onmicrosoft.com`,
blur the field:

1. Email-blur fires `GET /v1/sso/discover?email=alice@...` → response
   `{has_sso:true, redirect_url:".../v1/sso/oidc/<cid>/initiate?email=..."}`.
2. Dashboard redirects to the initiate URL.
3. Initiate handler 302s to `https://login.microsoftonline.com/<tenant_id>/oauth2/v2.0/authorize`
   with PKCE `code_challenge_method=S256`, opaque `state`, fresh `nonce`,
   `prompt=login` (forced by `force_reauth=true`), and `login_hint=<email>`.
4. Sign in as Alice in Entra, complete first-run password change if it
   triggers, accept consent on first run.
5. Entra redirects back to AxiaOps callback with `code` + `state`.
6. Callback validates state (single-use), exchanges code for tokens,
   validates the ID token (alg-confusion guard rejects `none`/`HS256`,
   per-connection JWKS via the shared package, issuer/audience/nonce + the
   `oidc_tenant_id` binding from §3.1 checked), confirms the email's domain
   is verified for THIS connection AND THIS org (anti-spoofing), runs JIT
   (group→role precedence; owner-never-via-JIT pin; Entra `oid` captured as
   `users.external_id` per design doc §726), mints a session with
   `auth_mode='sso'`.
7. Browser lands on `/dashboard` with the AxiaOps session cookie.

## 6. AADSTS error decoder

You'll hit at least one of these on the first try. Translation table:

| Error | Meaning | Fix |
|---|---|---|
| **AADSTS700016** "Application … was not found in the directory" | The client_id in your Discovery URL's tenant doesn't exist there. Either the client_id is wrong (often a Secret ID pasted as Client ID — see §2.6) OR the tenant in the Discovery URL is a different tenant than where the app was registered. | Re-open the app's **Overview** page, copy Application (client) ID and Directory (tenant) ID **from the same page session**. Don't trust values from older tabs. |
| **AADSTS50011** "redirect URI specified in the request does not match" | The redirect URI AxiaOps sent (`http://localhost:8082/v1/sso/oidc/callback`) isn't registered in the Entra app. | Add it under Authentication → Web → Redirect URIs (§4). Exact-match — `http` vs `https`, port, and trailing path all matter. The `state` parameter carries the cid; the URL itself is cid-less. |
| **AADSTS50105** "your administrator has configured the application … to block users unless they are specifically granted access" | The app has **Assignment required = Yes** and Alice isn't assigned. | Either: **Enterprise applications** → app → Properties → flip to **No**; or **Users and groups** → **+ Add** → Alice. |
| **AADSTS50058** "silent sign-in request was sent but no user is signed in" | Browser blocked iframe cookies during silent token renewal. Almost always a Safari / Brave / Firefox-strict / locked-down Chrome session. | Allow third-party cookies for `[*.]microsoftonline.com` (and `microsoft.com`, `office.com`, `live.com`). Or use a fresh Chrome / Edge profile. |
| **MSAL JS `no_tokens_found`** | Same root cause as AADSTS50058 — third-party storage blocked. | Same fix. |
| **`{has_sso: false}` from /v1/sso/discover** | Email's domain isn't matching a verified `sso_domains` row. | Check `SELECT status, verified_at FROM axiaops.sso_domains` — needs status = 'verified', not just verified_at populated. |
| **JIT keeps resolving to `default_role` even after group mapping is added** | Token's `groups` claim is empty (`token_groups=[]`). | Most common cause: **Emit groups as role claims** is checked in Token configuration — group IDs land in `roles` not `groups`. Untick it. Second cause: claim isn't enabled for the **ID** token type (only Access / SAML). Third cause: Alice was removed from the group. |

## 7. Verify (psql)

```bash
docker exec -it axiaops-postgres psql -U axiaops_owner axiaops
```

```sql
-- oid was captured (Entra's stable subject), not sub
SELECT id, email, external_id
  FROM axiaops.users
  WHERE email = 'alice@<yourtenant>.onmicrosoft.com';

-- JIT placed the membership (provisioned_via='jit')
SELECT m.user_id, m.role, m.provisioned_via, u.email
  FROM axiaops.memberships m
  JOIN axiaops.users u ON u.id = m.user_id
  WHERE u.email = 'alice@<yourtenant>.onmicrosoft.com';

-- Audit trail: sso_jit_provisioned (first time) + sso_login_succeeded
SELECT action, created_at FROM axiaops.audit_log
  WHERE action LIKE 'sso%' ORDER BY created_at DESC LIMIT 8;

-- Session minted with auth_mode='sso'
SELECT id, user_id, auth_mode, created_at FROM axiaops.sessions
  WHERE revoked_at IS NULL ORDER BY created_at DESC LIMIT 3;
```

## 8. Re-login + enforcement test

1. Logout → log back in as Alice → audit should show one new
   `sso_login_succeeded`, no second `sso_jit_provisioned`, no duplicate user
   row. (`sso_jit_role_updated` instead if you changed group mappings between
   logins.)
2. In AxiaOps: change connection enforcement → `required`, save.
3. Try to log in via native password as a different user → expect 403
   `{"error":"sso_required"}`.
4. Log in as Alice via SSO → still works (SSO sessions bypass enforcement).
5. Roll enforcement back to `optional` when done.

## 9. Reset between iterations

```bash
docker exec -i axiaops-postgres psql -U axiaops_owner axiaops <<'SQL'
DELETE FROM axiaops.users WHERE email = 'alice@<yourtenant>.onmicrosoft.com';
SQL
```

Connection, domain, and group mappings persist — keep them around for the
next iteration.

## 10. What you'll have proven

- `/v1/sso/discover` — domain-aware constant-shape lookup against a real
  Entra-managed domain.
- `/v1/sso/oidc/<cid>/initiate` — PKCE + state + nonce + login_hint +
  prompt=login ceremony against `login.microsoftonline.com`.
- `/v1/sso/oidc/callback` — full ID-token validation chain against
  Microsoft's JWKS (cross-tenant signing-key sharing closed by the
  `oidc_tenant_id` binding), JIT with `oid`-as-`external_id`, session
  minting with `auth_mode='sso'`, audit row.
- `/v1/me` — returns the SSO-provisioned user with the JIT-resolved role.

Closes the B2 design-doc acceptance criterion (`docs/sso-integration-design.md`
line 1042 — *"Internal AxiaOps team logs into self-hosted instance via Entra
OIDC with JIT provisioning"*) and the matching plan acceptance criterion
(`docs/sso-implementation-plan.md` §5.5 line 1008). Unblocks customer
onboarding for Entra-backed orgs.
