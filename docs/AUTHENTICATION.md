# Authentication & Authorization — AxiaOps

Native auth, roles/permissions, and the supporting security features (password
breach screening, the runtime DB role). See [ARCHITECTURE.md § 6](ARCHITECTURE.md)
for the request-level auth flow diagram and middleware chain; this doc goes deeper
on each piece.

---

## 1. The auth model, end to end

AxiaOps uses **native auth only** — no third-party identity vendor. Four ways in:

| Path | Trigger | Result |
|---|---|---|
| **Bootstrap** | First-ever install, `POST /v1/auth/bootstrap` with a one-time install token | Creates the org + first user as `owner` |
| **Native login** | `POST /v1/auth/login`, email + argon2id password | Session cookie, or `needs_org_selection` for multi-org users |
| **Invitation redeem** | `POST /v1/auth/invitations/redeem {token, password[, name]}` | Creates account + membership + session |
| **SSO (OIDC)** | Email-domain discovery → IdP redirect → callback | JIT-provisions user + membership, mints session with `auth_mode='sso'` |

Roles live in the **`memberships`** table — one row per `(user, organization)`, not
a column on `users`. This is what makes multi-org membership and per-org roles
work: the same person can be `owner` in one org and `viewer` in another.

### Shipped endpoint surface

| Method | Path | Auth | Purpose |
|---|---|---|---|
| `POST` | `/v1/auth/bootstrap` | No | First-owner install |
| `POST` | `/v1/auth/login` | No | Email + password login |
| `POST` | `/v1/auth/logout` | Cookie | Revoke session |
| `POST` | `/v1/auth/invitations/preview` | No | Peek at invite token |
| `POST` | `/v1/auth/invitations/redeem` | No | Accept invite + create account |
| `POST` | `/v1/auth/password-reset/redeem` | No | Set new password from admin-issued token |
| `GET` | `/v1/me` | Yes | Current user, role, permission set |
| `GET` / `POST` | `/v1/memberships` | Yes | List / add (existing user by `user_id`) |
| `PATCH` | `/v1/memberships/{id}/role` | Yes | Promote / demote |
| `DELETE` | `/v1/memberships/{id}` | Yes | Remove (self-leave bypasses the permission check) |
| `POST` / `GET` / `DELETE` | `/v1/invitations` | Yes | Invite by email / list pending / revoke |
| `POST` | `/v1/organizations/transfer-ownership` | Yes | Owner handover |
| `DELETE` | `/v1/organizations/me` | Yes | GDPR right-to-erasure |

### SSO (OIDC)

When an org has an active OIDC connection: user enters email at `/login` → email-blur
calls `GET /v1/sso/discover` → if the domain matches a **verified** `sso_domains` row
for an active connection, redirect to `/v1/sso/oidc/{cid}/initiate` (PKCE + state +
nonce) → IdP → callback validates the ID token (alg-confusion guard rejects
`none`/`HS256`, per-connection JWKS, issuer/audience/nonce), confirms the domain is
verified for *this* connection and *this* org (anti-spoofing), JIT-provisions the
user + membership (group→role mapping if configured, otherwise `default_role`), mints
a session with `auth_mode='sso'`. SAML is not implemented — OIDC only.

**Enforcement**: a connection can require SSO (`enforcement='required'`) — password
logins for that org's domain then get `403 sso_required`. Test with `enforcement:
optional` first; a misconfigured `required` connection locks out password login
except `/v1/auth/logout`.

**Local testing against a real IdP** (Entra or Keycloak) is in
[DEVELOPER_GUIDE.md](DEVELOPER_GUIDE.md) — full click-through runbooks including the
AADSTS/Keycloak error-code decoder tables.

---

## 2. Roles & permissions (RBAC)

Four roles, strictly hierarchical — a higher role has every permission of every lower one:

```
owner > admin > member > viewer
```

| Role | Intent |
|---|---|
| `owner` | Controls the org itself: transfer ownership, delete org. Exactly one at a time (DB-enforced via a partial unique index). |
| `admin` | Full operational control inside the org — manages users, cloud accounts, dismissals. |
| `member` | Daily FinOps work — connect/update cloud accounts, trigger scans, dismiss/snooze zombies. |
| `viewer` | Read-only — zombies, summary, trends, costs. Cannot mutate anything. |

### Permission vocabulary

```
accounts:read / accounts:write / accounts:delete / accounts:scan
zombies:read / zombies:dismiss
snapshots:read / costs:read / resources:read
members:read / members:invite / members:manage_basic / members:manage_admin
organization:transfer / organization:delete
```

Role → permission is a hardcoded map in `services/shared/authz/roles.go` (not
DB-stored) — coarse and simple by design; no custom roles, no per-resource grants.
`members:manage_basic` covers promoting/demoting between member/viewer;
`members:manage_admin` (owner-only) covers promoting to or demoting from admin —
this split stops an admin from minting another admin and escalating permanently.

**Enforcement**: an HTTP-handler-layer decorator (`middleware.Require(perm, ...)`)
looks up the caller's role via `store.RoleOf(ctx, orgID, userID)` (a query that
itself runs inside the org's RLS context) and checks it against the permission map.
RBAC and RLS are orthogonal: RBAC filters *what* a user in org X can do; RLS filters
*which org's data* the query can even see.

### Edge cases worth knowing

- **Last-owner guard.** An org must always have ≥1 owner. Removing or demoting the
  only owner returns `409 Conflict`; to hand off, the owner calls
  `POST /v1/organizations/transfer-ownership` first (one transaction: demotes self to
  admin, promotes target to owner).
- **Self-leave.** Any user can remove themselves (`DELETE /v1/memberships/{id}`
  targeting their own row) without needing `members:manage_*` — still subject to the
  last-owner guard.
- **New-org bootstrap.** The first user to authenticate against a brand-new org (zero
  memberships) is auto-promoted to `owner`, guarded by a DB-level partial unique
  index so a race between two simultaneous first-logins can never produce two owners
  — the loser falls through to a 403 "contact your organization admin."
- **Role-change propagation.** No forced logout or session revocation on a demotion —
  the next request the demoted user makes gets a fresh `RoleOf` lookup and a 403; the
  dashboard intercepts that, refetches `/v1/me`, and re-renders. Server is always the
  source of truth; the client is eventually consistent.
- **`DEV_MODE`.** The dev user has no session — `DevBypass` injects a fixed
  `DEV_ORGANIZATION_ID`/`DEV_USER_ID` onto every request context, seeded as `owner`
  at startup. `Require` runs the exact same lookup path in dev as in prod (no
  dev-mode branch in the permission check itself) so a permission bug can't hide
  behind DEV_MODE.
- **Ingestion is trusted infrastructure, not a principal.** The API→ingestion
  `/scan` call and the scheduled-scan ticker carry no user context — network-level
  trust, not RBAC. If ingestion ever becomes externally reachable this needs revisiting
  (service-to-service auth).
- **Deferred (not v1):** per-cloud-account scoping, custom roles, API keys/service
  accounts as first-class principals, IP allow-listing.

---

## 3. Native-auth bootstrap — local testing runbook

Walking through the first-run flow end-to-end against `make start-staging` (mirrors
production's shape minus edge TLS — production terminates HTTPS at the edge proxy;
this stack runs plain HTTP and relies on `X-Forwarded-Proto` propagation).

### Reset for a clean test

Bootstrap is single-use per install — once an org exists, the endpoint returns 409
forever.

```bash
make stop
# Option A — full wipe (postgres re-initdbs)
sudo rm -rf pg_data
# Option B — truncate via SQL (faster, no sudo)
docker compose up -d postgres
docker exec axiaops-postgres psql -U axiaops_owner -d axiaops -c "
  TRUNCATE TABLE axiaops.audit_log, axiaops.memberships, axiaops.zombie_snapshots,
    axiaops.resource_records, axiaops.zombie_records, axiaops.cost_records,
    axiaops.accounts, axiaops.sessions, axiaops.password_resets,
    axiaops.bootstrap_state, axiaops.users, axiaops.organizations CASCADE;"
```

### Bootstrap flow

```bash
make start-staging   # runs migrate, then docker compose up --build -d
```

Retrieve the one-time install token (mode 0600, deleted on first successful bootstrap):

```bash
docker exec axiaops-api cat /var/run/axiaops/initial_setup_token
```

> The shell appends a trailing `%`/`$` — that's the prompt, not part of the token.
> The token is exactly 64 hex characters.

Open `http://localhost:8082` — a fresh install auto-redirects to `/bootstrap`. Fill
in the token, org name, your name/email, and a password (min 12 chars). On success:
org + owner user + session are created, `bootstrap_state` is deleted (sealing the
endpoint forever), and the install token file is removed.

**On ECS Express/Fargate** the container filesystem isn't reachable at all — set
`BOOTSTRAP_PRINT_BANNER=true` and `BOOTSTRAP_TOKEN_FILE_PATH=""`, then read the token
from CloudWatch (`aws logs tail /aws/ecs/axiaops-api --since 15m ... | grep -iA4
'first-run setup'`). You get exactly one capture at first boot — a restart won't
reprint. Recovery: delete the `bootstrap_state` row to re-mint.

### Changing a user's email

No self-serve `PATCH /users/me/email` endpoint. **SSO users** change it at the IdP —
the next login's JIT upsert picks it up (`sso_external_id` is the stable key, not
email). **Native users** with no SMTP-backed reset flow use a six-call workaround:
invite a new address at `admin`, redeem the invite in a private window, transfer
ownership to the new user, then `DELETE /v1/users/me` as the old (now non-owner)
account — passes the last-owner guard because another owner now exists.

### Troubleshooting

| Symptom | Cause / fix |
|---|---|
| Bootstrap returns 409 immediately | An org already exists — truncate, or use `/login` with the existing user |
| Bootstrap returns 401 `invalid_token` | Wrong token, or you included the shell's trailing `%`/`$` |
| Cookie not sticking (`/v1/me` → 401 right after a 200 bootstrap) | Check the cookie's `Secure` flag in DevTools — should be non-Secure on plain-HTTP localhost. If it's `Secure: true` on plain HTTP, something upstream is sending `X-Forwarded-Proto: https` incorrectly |
| "Address already in use" on :8082/:8080/:5432 | Stale containers from a previous run — `make stop`, then `docker rm -f` any stragglers |

---

## 4. Password breach screening

The native-auth password policy is length-only (≥12 chars) plus a **compromised-password
check** — NIST SP 800-63B §5.1.1.2 requires screening new passwords against a known-breach
corpus.

**Design: embed an offline corpus, never call a live API.** AxiaOps is self-hosted;
an install may be egress-restricted or air-gapped, so a live HaveIBeenPwned API call
is unreliable by design. Follows the GitLab/Django precedent (bundled offline list,
hard-block on a hit) rather than Okta/Entra's live-service model.

**Where it's wired**: `auth.CheckPolicy` (`services/api/internal/auth/password.go`)
is the single seam every password-set path (bootstrap, invite-redeem, password-reset-redeem,
the `hash-password` CLI) funnels through. Login/select-org/switch-org verify an
existing hash and correctly do **not** screen.

**Implementation**: SHA-1 digests of the corpus, sorted, embedded as a raw
20-bytes-per-record binary blob (`services/api/internal/breachlist/breached-passwords.bin`),
looked up via binary search — no map, no bloom filter, zero startup deserialization.
SHA-1 here is a **corpus index only**; password storage is always argon2id.
The package lives under `services/api/internal` (not `shared`), so ingestion's
import graph structurally never links the blob.

**Current corpus** (shipped as a *bootstrap seed*, not the full HIBP top-1M — the
full ordered file can't be fetched from the build environment):

| Field | Value |
|---|---|
| Source | Curated head (~338 classics) + xato-net top-10,000 (prevalence-ordered, breach-derived) |
| N (digests) | 10,044 |
| `.bin` size | ~196 KB |
| Generated by | `scripts/gen-breachlist.sh` (seed mode) |

The full HIBP top-1M (~20 MB embedded) is a documented, turnkey, no-code-change
upgrade — not a pending obligation. To swap it in:

```bash
# 1. Get the prevalence-ordered hash file (PwnedPasswordsDownloader, ordered mode,
#    or the published torrent)
# 2. Generate:
scripts/gen-breachlist.sh hibp /path/to/pwned-passwords-ordered.txt 1000000
# 3. Update the manifest below with the new N / size / SHA-256, and NOTICE
# 4. make build-production && check the image-size delta
```

Regenerating from the current seed wordlist (no source swap) is just
`scripts/gen-breachlist.sh` (default seed mode) — update the manifest table and
commit the regenerated `.bin` alongside the wordlist diff.

**Licensing**: HIBP's Pwned Passwords data is CC BY 4.0 — attributed in the root
`NOTICE`. The downloader tool itself is not embedded, only the resulting digests, so
its license doesn't propagate.

**Invariants that must not break**: raw 20-byte records, sorted ascending, no
delimiters (binary search depends on it); no normalization anywhere in the
hash-and-store chain (the corpus generator, `IsCompromised`, and `auth.Hash` must all
hash the exact same bytes — no `TrimSpace`/Unicode-NFC on one path and not the
others); the embed is unconditional — no `-tags production` split, since making the
check optional would let an operator silently ship a NIST-non-compliant binary.

Full provenance/regeneration detail (SHA-256 of the current asset, verification
commands) lives in the corpus's own `NOTICE` file and the generator script's
`--help`.

---

## 5. Runtime database role (`axiaops_runtime`)

**Problem**: the runtime services (api, ingestion) need to bypass RLS for a handful
of legitimate cross-org reads — native login (`LookupUserByEmail`, pre-auth so no org
context exists yet), `/v1/me`, the scheduled-scan account enumerator, the GDPR
org-cascade purge. Historically this ran through the same `axiaops_owner` connection
used for migrations — the schema owner, which also carries DDL/DROP/ownership. An RCE
in the always-on api/ingestion container got the keys to reshape the schema, not just
read across orgs.

**Why not `BYPASSRLS`** (the obvious fix): setting the `BYPASSRLS` role attribute
requires a true Postgres superuser. On the production target (AWS RDS) the
master/owner role is `rds_superuser`, which is *not* a real superuser and cannot
grant `BYPASSRLS`. A migration that worked against local Docker Postgres would fail
silently different on prod RDS.

**Chosen mechanism**: a dedicated `axiaops_runtime` role — `NOLOGIN`-by-default
until synced, no superuser, no ownership, no `BYPASSRLS` — combined with a
**permissive RLS policy per RLS-enabled table**, scoped to that role only:

```sql
CREATE POLICY <table>_runtime_bypass ON axiaops.<table>
    TO axiaops_runtime USING (true) WITH CHECK (true);
```

Postgres combines permissive policies with OR, so for `axiaops_runtime` the effective
predicate becomes `<org-isolation policy> OR true` — always visible — while the
ordinary app role's policy is untouched (it isn't a member of `axiaops_runtime`).
Creating a policy only needs table ownership, not superuser, so it works on RDS.
A migration enumerates every RLS-enabled table dynamically (via `pg_class`/`relrowsecurity`)
rather than hand-maintaining a list, and a CI invariant test fails if any RLS table
lacks its `_runtime_bypass` policy — the guard against a future table silently
returning zero rows to the runtime.

**Net effect**: `axiaops_owner` (schema owner — DDL, ownership) is now reserved for
the one-off migrate task only; `axiaops_runtime` (DML-only, cross-org via policy, no
DDL) is what api/ingestion actually connect through at runtime for the bypass cases
above. `postgres.NewWithRuntimeAdmin` is the constructor; `NewWithOwner` remains only
as the empty-fallback path some tests rely on.

Status: shipped in production (ECS Express) and on all non-dev environments.

---

## 6. See also

- [ARCHITECTURE.md § 5](ARCHITECTURE.md) — Row-Level Security pattern, schema overview.
- [ARCHITECTURE.md § 6](ARCHITECTURE.md) — request-level middleware chain and auth flow diagram.
- [DEVELOPER_GUIDE.md](DEVELOPER_GUIDE.md) — local SSO testing against Entra/Keycloak.
