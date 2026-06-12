# Enterprise SSO Integration — SaaS / Managed-hosted Variant

Status: **reactivated 2026-06-11** — tenant-auth path for the hosted SKU per [ADR-0002](decisions/0002-saas-first-for-awareness.md) commitment #6 (acceptance flipped the deployment posture to SaaS-first). Sibling to [`sso-integration-design.md`](sso-integration-design.md) (the self-hosted path). Note: body below predates Kinde removal (2026-05) — the brokered-IdP role Kinde played now maps to the native OIDC connector layer.

> **2026-04-29.** This document captures the **Option A (Kinde-brokered, multi-tenant SaaS)** path that was rejected as v1 by [ADR-0001 (Deployment Model — Self-hosted-first)](decisions/0001-deployment-model.md). It is preserved here because the work to design Option A had already been done, and the ADR-0001 review trigger (≥3 self-hosted customers paying → re-evaluate adding a managed-hosted SKU) makes this design likely to be reactivated. Future engineers picking this up should not have to re-derive it.
>
> **When to reactivate this doc**: any of the ADR-0001 review triggers — specifically when AxiaOps Inc commits to a "AxiaOps Cloud" managed-hosted SKU alongside the self-hosted SKU.
>
> **Relationship to the self-hosted doc**:
> - User stories (§1), data model (§4), JIT logic (§10), most security properties (§11.1–§11.7), and testing primitives (§13) are **identical** between the two — cross-references throughout point at the sibling doc rather than duplicating.
> - The architectural recommendation (§3), API surface (§5), frontend (§6), protocol-level responsibilities (§7, §8, §9), security boundary (§11.8, §11.9), migration (§12), phase plan (§14), open questions (§15), and file list (§16) **diverge** because Kinde owns the cryptographic surface and the auth boundary.
>
> **Coexistence assumption**: by the time this doc is reactivated, the self-hosted variant is the established v1 product and the SaaS variant is being added alongside. Both are built from one codebase using **two top-level `cmd` binaries** — `cmd/api-selfhosted` (default) and `cmd/api-saashosted` — see §16.4 for the build-separation rationale. The data model is shared; the runtime auth path differs.

This document is the implementation contract for adding SAML 2.0 / OIDC / Microsoft Entra ID single sign-on to the **AxiaOps Cloud (managed-hosted)** SKU. It is written under the constraint that the AxiaOps Cloud build uses **Kinde for the entire authentication boundary** — social login, email/password, and enterprise SSO — and that JIT provisioning is the v1 user-creation model alongside the existing `pending_memberships` invitation flow (`docs/invitation-flow.md`).

---

## 1. User stories

**See [`sso-integration-design.md` §1](sso-integration-design.md#1-user-stories).** All ten stories (org admin configures SAML/OIDC/Entra; org admin verifies a domain; org admin enforces SSO-only; org admin maps groups; existing user signs in; new user JIT-provisioned; org admin disables/rotates connection; auditor reviews trail; support engineer diagnoses failure) apply verbatim.

The only delta is in story §1.6: under SaaS the session cookie is set by **Kinde's hosted callback** rather than an AxiaOps-internal callback. End-user UX is unchanged; the implementation is different.

---

## 2. Goals & non-goals

### 2.1 Goals (v1 design surface — SaaS)

Same as sibling §2.1, plus:
- **Social login** (Google/Microsoft personal/GitHub OAuth) is IN-scope — provided by Kinde at no incremental cost.
- **Hosted login UI** is provided by Kinde; no native login page in the SaaS build.

> *Original 2026-04 draft also listed "existing Kinde-authed orgs preserved during the reactivation transition" as a goal. That goal is moot under ADR-0001: Kinde is fully removed in self-hosted Phase B1 (sibling §14), so by reactivation time no Kinde-authed orgs exist to preserve. The internal AxiaOps dogfood instance is the only edge case — handled in §12.4 of the sibling doc.*

### 2.2 Non-goals (v1 — SaaS)

Same as sibling §2.2 (SCIM deferred, MFA delegated, WS-Fed out, IdP-initiated SLO defer, B2C IdPs out), plus:
- **Native email/password authentication is NOT IN this build.** It exists in the self-hosted variant; in the SaaS variant Kinde owns the email/password path. Removing this dichotomy is the entire reason for the build separation.
- **Customer-supplied JWT signing keys** — out of scope. Kinde controls JWT issuance for the SaaS SKU.

---

## 3. Architectural decision (SaaS-specific)

### 3.1 Why Option A is right for SaaS specifically

The self-hosted doc §3 weighs Options A (Kinde-brokered), B (native), and C (hybrid). Under self-hosted-first, Option A was rejected because Kinde is unreachable from a customer-self-hosted deployment (sibling §3.6.1).

**For SaaS**, that constraint disappears — AxiaOps Inc runs the infrastructure, and Kinde is reachable. The original Option A reasoning (sibling §3.1) reasserts:

1. **Kinde does the cryptographic heavy lifting** — XML signature verification, OIDC token validation, JWKS rotation, replay caches, IdP metadata refresh. Months of pen-test surface stay outside our codebase.
2. **One auth code path** in the SaaS binary (Kinde JWT validation), as it has been since Phase 1 — no rewrite of `auth.go` for SaaS.
3. **SOC 2 evidence**: Kinde's SOC 2 Type II covers the auth control; AxiaOps Inc only evidences config + audit pipeline. Audit scope narrows.
4. **Time to ship** for the SaaS variant: ~4–6 weeks for Phase B itself, plus 2–3 weeks for Phase A (`cmd/api-saashosted` composition root, Kinde tenant provisioning, ADR-0002) — total **6–9 weeks A+B** to first SaaS login, vs. 8–12 weeks of native cryptography that was paid in the self-hosted v1 path (B2 OIDC + C SAML in the implementation plan). Reusing the self-hosted data model means Phase B is mostly admin-UX + Kinde Mgmt API wiring.
5. **Native SSO partially reused from self-hosted, runtime path differs**. The schema (§4), admin-UX backend (CRUD handlers, domain verification, group mappings), JIT logic (§10), and audit pipeline are **shared** between SKUs — no duplication there. What the SaaS build does **not** reuse is the cryptographic / runtime layer: the native OIDC RP, SAML SP, replay cache, and login screens are bypassed because Kinde provides equivalents. Operational benefit (riding Kinde's SOC 2, no per-customer key management at AxiaOps Inc) is concentrated in this thin runtime layer; everything else stays single-source.

### 3.2 Recommendation

**Option A — Kinde-brokered for the SaaS SKU.** Kinde stays as the auth boundary in the SaaS build. Per-organization enterprise SSO connections are configured via Kinde's Mgmt API. AxiaOps stores domain verifications, group→role mappings, and post-issuance enforcement state in our own DB.

This is **purely additive** to the self-hosted v1 path:
- Self-hosted binary keeps its native auth — no change.
- SaaS binary (`cmd/api-saashosted`) imports Kinde dependencies; self-hosted binary does not.
- Data model unchanged — `kinde_connection_id` column populated in SaaS, empty under self-hosted.

### 3.3 What changes vs. the original (pre-2026-04-29) Option A design

The original Option A design (preserved as historical context in `sso-integration-design.md` §3.1) assumed AxiaOps was Kinde-only. Now Kinde is **only in the SaaS build**:

- The SaaS variant inherits all the §4 data-model improvements built for self-hosted (domain verification, group mapping, replay cache table, audit constants).
- The SaaS variant inherits the admin UX written for self-hosted (Settings → Single Sign-On screen, domain TXT verification flow, group-mapping table) — handlers swap in Kinde Mgmt API calls instead of native CRUD.
- The SaaS variant inherits the §1 user stories and §10 JIT logic verbatim.
- What's **new for SaaS**: Kinde Mgmt API wrapper, Kinde sub-processor disclosure (§11.9), `cmd/api-saashosted/main.go` composition root, dual-SKU release pipeline (§16). Build separation is binary-level (§16.4 Option B), not build-tag-level.

---

## 4. Data model

**See [`sso-integration-design.md` §4](sso-integration-design.md#4-data-model-changes).** All five SSO tables (`sso_connections`, `sso_domains`, `sso_group_mappings`, `sso_assertion_replay`, plus the column additions to `users` and `memberships`) ship identically in the SaaS build. Migration `022_sso_core` is shared (numbered 022 — the implementation plan reserves migration 021 for native auth's `sessions`, `password_resets`, `bootstrap_state` tables; SSO ships after it).

**Native-auth tables under SaaS** (per implementation plan D7 + architect C1):
- `sessions`, `password_resets`, `bootstrap_state` ship in the SaaS schema for parity but are **unused** — Kinde owns session lifecycle and bootstrap. The cache abstraction (`services/shared/cache/`) is reused only for the existing `jwks:<issuer>` Kinde-JWKS keys. The native `axiaops:session:<hash>` keys (B1) are never written under SaaS — Kinde JWTs are stateless on our side.
- The no-RLS posture on these tables (architect finding C1) is preserved in the schema; SaaS handlers never read from them, so it's a cosmetic property.

**SaaS-specific column behaviour:**

- **`sso_connections.kinde_connection_id`** is **load-bearing** under SaaS — every active connection has a non-empty value pointing at the corresponding Kinde Mgmt API resource. Under self-hosted it is empty. Validation: SaaS handlers reject `''` on save; self-hosted handlers reject non-empty values on save (binary-conditional validation — separate `cmd/api-selfhosted` and `cmd/api-saashosted` per §16.4).
- **`sso_assertion_replay`** is **unused** under SaaS — Kinde handles replay protection for SAML assertions itself. Table exists in the schema for self-hosted compatibility; SaaS handlers never write to it.
- **`oidc_client_secret_ciphertext`** and **`saml_signing_cert`** are stored in our DB even under SaaS, because we read them back for the admin UX ("show me the cert I gave Kinde"). Kinde also stores them; the duplication is intentional for admin-UX self-service.

---

## 5. API surface (SaaS deltas)

### 5.1 Endpoints unchanged from sibling

`/v1/organizations/{id}/sso/connections` (CRUD), `/v1/organizations/{id}/sso/domains` (verification), `/v1/organizations/{id}/sso/group-mappings`, `/v1/sso/discover` — same paths, same permissions, same body shapes as sibling §5.2.

### 5.2 Endpoints removed under SaaS

These exist in self-hosted but are **owned by Kinde** in the SaaS build — AxiaOps does not implement them:

| Path | Why owned by Kinde under SaaS |
|---|---|
| `POST /v1/sso/saml/{cid}/acs` | Kinde's hosted ACS handles assertion verification + replay protection. Customer's IdP POSTs to a Kinde-provided URL; Kinde mints a JWT and redirects to AxiaOps with the JWT in the session. |
| `GET /v1/sso/saml/{cid}/metadata` | Kinde generates SP metadata; AxiaOps admin UX surfaces the Kinde-provided URL when the IdP admin needs it. |
| `GET /v1/sso/oidc/{cid}/callback` | Kinde's hosted callback handles the code-for-token exchange. We never see the authorization code. |
| `POST /v1/sso/{protocol}/{cid}/initiate` | Kinde initiates the IdP flow when the user lands on the Kinde Hosted Login page. |
| `/v1/auth/login`, `/v1/auth/register`, `/v1/auth/refresh`, etc. | Kinde owns email/password + session lifecycle. AxiaOps SaaS has no native auth endpoints. |

### 5.3 New endpoints under SaaS

| Method | Path | Permission | Purpose |
|---|---|---|---|
| `POST` | `/v1/sso/kinde/webhook` | none (HMAC-signed by Kinde) | Receive Kinde lifecycle events. Used to keep our `users` / `memberships` / `sso_connections` rows in sync with Kinde-side state. Verifies the `Kinde-Signature` header against `KINDE_WEBHOOK_SECRET`. Dispatch table below. |
| `GET` | `/v1/sso/connections/{cid}/test` | `PermSSOManage` | Synthetic test now calls **Kinde's** test endpoint via Mgmt API. Reason enums map from Kinde's response. |

**Webhook dispatch table** (`/v1/sso/kinde/webhook`) — handler is a single endpoint, but the body's `event_type` selects an internal handler. Document the dispatch up front so the handler doesn't grow into an unstructured switch:

| Kinde event type | AxiaOps action | Tables touched |
|---|---|---|
| `organization.created` | Mirror as `organizations` row via `UpsertOrganization`. | `organizations` |
| `organization.updated` | Sync `name`, `display_name` only. Never overwrite `id`. | `organizations` |
| `organization.deleted` | **No-op for v1** — log + audit only. Org-delete is owner-driven via `DELETE /v1/organizations/me`, not Kinde-driven. Receiving this event probably indicates a Kinde-side admin action that needs human review. |  — |
| `user.organization.added` | Upsert `users` row + `memberships` row (default role: `viewer` unless JIT group mapping resolves higher). | `users`, `memberships`, `audit_log` |
| `user.organization.removed` | Soft-disable `memberships` row (set `status='disabled'`). Don't hard-delete — preserves audit lineage. | `memberships`, `audit_log` |
| `connection.updated` | Refresh `sso_connections` row's status/enforcement; never overwrite `kinde_connection_id`. | `sso_connections`, `audit_log` |
| (any other) | Log at `slog.Warn` with full event_type; do not 500 — Kinde re-delivers on non-2xx. | — |

Replay protection: Kinde signs with timestamp; reject events older than 5 minutes regardless of signature. Tested in §13.3.

### 5.4 Login-page domain discovery (SaaS variant)

The flow under SaaS:

```
1. User lands on /login (served by Kinde Hosted Login).
2. Kinde renders the email field.
3. On email submit, Kinde looks up its own SSO-domain mapping AND calls AxiaOps's
   GET /v1/sso/discover?email=... as a fallback. (Discovery responsibility split.)
4. If Kinde finds the SSO connection → redirects to IdP.
5. If AxiaOps responds with has_sso=true and Kinde has nothing → AxiaOps's domain
   record points at a Kinde connection (the kinde_connection_id we stored at
   connection-create time). AxiaOps API instructs Kinde via Mgmt API to initiate
   that connection, then redirects.
6. If neither → fall through to Kinde's hosted email/password.
```

**Note**: keeping AxiaOps's `sso_domains` table populated even under SaaS is intentional. It allows admin UX to display verified domains, even though Kinde also stores domain↔connection mapping internally. The dual-write keeps the admin experience self-serve.

---

## 6. Frontend (SaaS deltas)

### 6.1 Login & registration

Under SaaS, **Kinde's Hosted Login UI** is used — same as Phase 1–3 today. AxiaOps does not ship a native `LoginScreen.jsx` or `RegisterScreen.jsx` for the SaaS build. The dashboard's `/login` route redirects to Kinde.

This is the major UX divergence from self-hosted: self-hosted ships native login screens because Kinde isn't there. SaaS reuses what Kinde provides.

### 6.2 Admin SSO settings screen

`services/dashboard/src/pages/settings/SSO.jsx` (already built for self-hosted) is reused **with handler-side branching**. Under SaaS:

- "Add connection" wizard saves the metadata to our DB AND calls our backend, which calls Kinde Mgmt API to create the matching Kinde-side connection.
- "Test connection" button calls Kinde's test endpoint via Mgmt API instead of AxiaOps's synthetic auth.
- Domain verification flow is identical (DNS TXT) — our backend writes both to `sso_domains` AND to Kinde's domain mapping.
- Group mapping is AxiaOps-only state (Kinde doesn't store our role mappings).

### 6.3 Members screen, login enforcement banner

Identical to sibling §6.4 / §6.3.

### 6.4 Multi-org access (B1.5 inheritance)

The `OrgSwitcher.jsx` component built in B1.5 (implementation plan §4.7) is **reused under SaaS** for switching the active organisation within an authenticated Kinde session — Kinde supports multi-org membership; the dashboard side just needs to read `/v1/me`'s `memberships` array and POST to `/v1/auth/switch-org` to rotate the active org context.

What is **not** reused:
- `OrgPickerScreen.jsx` (the post-login "select an organisation" page) — Kinde Hosted Login owns IdP-org-selection at JWT issuance time, before the SaaS API ever sees the session. SaaS Kinde JWTs already contain the resolved `org_code`.
- `POST /v1/auth/select-org` (re-validates a password) — there's no password to re-validate under SaaS; Kinde minted the JWT.

What changes shape under SaaS:
- `POST /v1/auth/switch-org` is reused but the implementation under SaaS revalidates the *Kinde JWT* (not a password) and rotates the AxiaOps-side membership view. The Kinde-side org context comes from the JWT; AxiaOps's `app.organization_id` follows.

---

## 7. SAML specifics (SaaS variant)

### 7.1 Library — none in our codebase

Under SaaS we do not link `crewjam/saml` or any SAML library — Kinde is the SP. The library choice in sibling §7.1 is moot for this build.

### 7.2 SP cert / signing key management

Owned by Kinde. The customer's IdP admin uploads our SP metadata (provided by Kinde) into their IdP. We expose the URL in the admin SSO screen verbatim from what Kinde gives us.

### 7.3 Replay protection

Owned by Kinde. The `sso_assertion_replay` table sits unused under SaaS (it exists in the shared schema for self-hosted).

### 7.4 Clock skew, NotBefore/NotOnOrAfter

Owned by Kinde. Kinde rejects assertions outside its tolerance window (typically 60s); AxiaOps sees only valid Kinde-issued JWTs.

### 7.5 IdP-initiated vs SP-initiated

Both supported by Kinde for SaaS — Kinde handles either flow. We document SP-initiated as the recommended path (simpler open-redirect attack surface) but Kinde's handling is what determines the actual surface.

### 7.6 SLO

Owned by Kinde. We implement SP-initiated logout against Kinde (existing `/api/auth/logout` flow); IdP-initiated SLO is Kinde's choice to support or not.

### 7.7 SAML callback flow under SaaS

```
1. User on Kinde Hosted Login enters alice@acme.com.
2. Kinde looks up the SSO connection (or asks AxiaOps via /v1/sso/discover).
3. Kinde initiates SAMLAuthRequest, RelayState=<csrf>, redirects to IdP.
4. IdP authenticates, POSTs SAMLResponse to Kinde's ACS.
5. Kinde verifies signature, NotBefore/NotOnOrAfter, audience, recipient,
   replay protection. Mints a Kinde JWT containing email, sub, groups claim.
6. Kinde redirects to AxiaOps `/callback?code=...`.
7. Dashboard exchanges code for Kinde session cookie.
8. AxiaOps API auth middleware validates the Kinde JWT — Kinde-specific
   middleware is wired into `cmd/api-saashosted/main.go` only.
9. After JWT validation, post-issuance hook runs:
     a. Look up sso_domains by JWT email domain.
     b. If verified domain → JIT provision via §10 logic.
     c. Apply group mapping → membership role.
     d. Audit log row.
10. Dashboard renders.
```

The cryptographic surface is **steps 4–5 inside Kinde**. AxiaOps owns 7–10 (post-issuance authorization, JIT, audit).

---

## 8. OIDC specifics (SaaS variant)

### 8.1 JWKS — Kinde owns rotation

Kinde manages JWKS for its own JWT issuance (already in place since Phase 1, `auth.go:53-98`). Customer IdP JWKS for SSO are managed by Kinde — we never fetch them.

The shared `axiaops.io/shared/jwks` package built for self-hosted is **not used** in the SaaS build.

### 8.2 Discovery doc handling

Owned by Kinde. The customer admin pastes Entra tenant ID + client ID + client secret into our admin SSO screen; we POST it to Kinde Mgmt API; Kinde fetches and validates the discovery doc.

### 8.3 PKCE

Owned by Kinde. The Kinde Hosted Login implements PKCE on the user agent side; AxiaOps backend never sees codes or PKCE verifiers.

### 8.4 Entra quirks

Documented in sibling §8.4 — but the *responsibility* for handling them differs:
- **v2 endpoints**: customer admin specifies tenant; Kinde derives the v2 discovery URL.
- **Tenant ID validation**: Kinde validates `iss` claim; we don't see raw `iss`.
- **Group claims overflow (>200 groups)**: Kinde handles the Graph fallback (assuming Kinde supports it; **NEEDS-CONFIRMATION at reactivation time** — Kinde may have shipped this since 2026-04, or it may still be a gap).
- **`oid` is the stable subject**: Kinde's JWT contains `oid` in a Kinde-defined claim location (e.g. `external_id` or `properties.oid`). Our middleware extracts and stores in `users.sso_external_id`.

### 8.5 OIDC subject persistence

Same as sibling §8.5 — `users.sso_external_id` stores the IdP's stable subject. Under SaaS we extract this from Kinde's JWT (Kinde forwards the IdP subject as a passthrough claim).

---

## 9. Entra ID integration notes (SaaS variant)

### 9.1 App registration steps (for the customer admin)

Same as sibling §9.1, with one delta: **redirect URI** is the Kinde-provided callback URL, not an AxiaOps-internal path. Format typically:

```
https://{your-axiaops-cloud-tenant}.kinde.com/oauth2/callback
```

The admin SSO screen surfaces the correct URL after the customer admin starts the connection-creation wizard — Kinde Mgmt API returns it on connection create, we display it.

### 9.2–§9.4

Same as sibling, except all references to "AxiaOps must..." become "Kinde must..." or "the customer admin gives Kinde..." as appropriate. No substantive differences in customer-admin steps.

---

## 10. JIT provisioning + role mapping

**See [`sso-integration-design.md` §10](sso-integration-design.md#10-jit-provisioning--role-mapping)** — identical logic, identical pseudocode, identical role-precedence rules.

The only delta: the JIT hook fires **after Kinde JWT validation** in the SaaS auth middleware (Kinde-specific middleware composed into `cmd/api-saashosted/main.go`), rather than after a native session is minted.

`pending_memberships` invitation flow takes precedence over JIT — same as sibling §10.4. The invitation flow under SaaS is **the original Kinde Mgmt API path** (sibling Tasks.md #14 pre-2026-04-29 design — Kinde sends the invite email). Under self-hosted it is the **rescoped token-based OOB path** (D3 in the implementation plan): admin generates a one-time invite link, shares it out-of-band — no SMTP. SaaS reactivation switches the underlying delivery mechanism via the `auth.Inviter` interface (implementation plan §4.2 seam S1) — handler code is unchanged; only the constructor in `cmd/api-saashosted/main.go` swaps in a Kinde-Mgmt-API-backed implementation.

---

## 11. Security considerations

### 11.1 Email-verification spoofing

Same as sibling §11.1. We trust the verified domain (DNS-proved); we do not trust `email_verified` from arbitrary IdPs. Kinde forwards the IdP claim; we apply our domain-verification gate regardless.

### 11.2 XML signature wrapping

**Kinde's problem under SaaS.** AxiaOps does not parse SAML XML. Sub-processor risk: if Kinde has a SAML CVE, every SaaS customer is exposed simultaneously. Mitigation: monitor Kinde security advisories; track Kinde's SOC 2 evidence at renewal time.

### 11.3 JWT algorithm confusion

**Kinde's problem under SaaS** for incoming SAML/OIDC IdP tokens. AxiaOps still validates **Kinde-issued** JWTs at the auth boundary; the alg-confusion mitigation in sibling §11.3 applies to *that* validation (`services/shared/jwks/` package, lifted out of `auth.go` in B2 per architect S3). Verify the assertion is in place when re-enabling the SaaS build.

The native `axiaops:session:<hash>` cache from implementation plan D7 is **not** exercised under SaaS — Kinde JWTs are stateless on our side. The SaaS build's only `services/shared/cache/` consumer is the existing `jwks:<issuer>` Kinde-JWKS cache.

### 11.4 Open redirect on `RelayState` / `state`

**Kinde's problem under SaaS** — Kinde mints and validates the CSRF token. AxiaOps still enforces a fixed `/dashboard` post-login destination (existing Phase 1 behaviour).

### 11.5 Audit logging discipline

Same as sibling §11.5. AxiaOps writes audit rows post-Kinde-issuance; we never log Kinde JWTs, raw assertions, or client secrets.

### 11.6 Authn vs authz separation

Same as sibling §11.6. Kinde authenticates; AxiaOps authorizes via `memberships.role`.

### 11.7 Rate limiting

Same as sibling §11.7 — the AxiaOps endpoints rate-limited are `/v1/sso/discover` and the post-issuance JIT hook. Kinde rate-limits its own login surface.

### 11.8 SOC 2 / GDPR alignment

Different shape from sibling §11.8 because we re-process customer billing/usage data:

- **CC6.1 (logical access)**: same audit-log evidence + Kinde's SOC 2 covers the auth control.
- **CC6.6 (boundary defence)**: enforcement=required closes the social-login bypass.
- **GDPR Art. 32**: encryption at rest of IdP secrets satisfies the technical-measures clause; multi-tenant RLS provides organizational isolation.
- **CC7.x (system operations)**: now in scope because we operate customer billing-data infrastructure (ECS Express, RDS).

The SaaS SKU's SOC 2 scope is **wider** than self-hosted — because we hold customer data, we evidence more controls. Reactivate `Tasks.md` Phase 3 #17 (multi-tenant SOC 2 Type II) when this doc is reactivated.

### 11.9 SOC 2 sub-processor disclosure (load-bearing under SaaS)

> **Reverses self-hosted §11.9.** Under self-hosted-first, Kinde is removed from the product entirely and no SSO sub-processor disclosure is needed. **Under SaaS, Kinde is a sub-processor for the entire authentication boundary** (not just SSO) and must appear on the public sub-processors list before any EU customer onboards.

Required disclosures:
- Kinde — authentication, identity broker (sub-processor for ALL SaaS customers).
- AWS — infrastructure (ECS Express, RDS, ElastiCache).
- Resend / Postmark / SendGrid (whichever picked) — transactional email.
- Sentry / similar — error monitoring.

DPA template, sub-processors list, RoPA all need a SaaS-specific revision when this doc is reactivated. The narrowed-scope GDPR work under self-hosted (Tasks.md Phase 3 #9p) is insufficient for SaaS — full controller-with-Kinde-sub-processor scope returns.

---

## 12. Migration & rollout (SaaS variant)

### 12.1 New deployments

A SaaS customer signs up via Kinde Hosted Login — same flow as Phase 1. Owner-by-default (`EnsureFirstMembership` — Kinde-mediated; the JWT carries the `org_code` and the auth middleware promotes the first authenticator).

The implementation-plan D5 install-token bootstrap (`bootstrap_state` table, `BOOTSTRAP_PRINT_BANNER` env var, `/v1/auth/bootstrap` endpoint) is **self-hosted-only**. The SaaS binary's `cmd/api-saashosted/main.go` does **not** call `bootstrap.GenerateInstallTokenIfNeeded` — Kinde Hosted Login owns first-owner creation. The `bootstrap_state` table sits in the schema (architect C5) but is never written under SaaS.

### 12.2 Coexistence with self-hosted SKU

The two SKUs are **separate deployments built from one codebase**:

- Self-hosted: `cmd/api-selfhosted` (default). Native auth. Customer runs the binary.
- SaaS: `cmd/api-saashosted`. Kinde auth. AxiaOps Inc runs the binary on ECS Express.

**Customers do not move between SKUs.** A customer signed up on AxiaOps Cloud cannot drop into self-hosted without re-signing-up on the self-hosted instance (or vice versa). Cross-SKU migration tooling is **out of scope for SaaS reactivation v1**; revisit if customers ask.

### 12.3 Re-enabling Kinde when this doc is reactivated

The three-state strangler (`AUTH_PROVIDER=kinde\|both\|native` per implementation plan D1) is a **self-hosted-only** mechanism — it exists to drain Kinde traffic during the deletion window. SaaS reactivation does not need to revive `both` mode; SaaS deploys are net-new and the `cmd/api-saashosted` binary's auth provider is fixed at composition time.

Steps when the managed-hosted SKU is committed:
1. Recover the `services/api/internal/kinde/` package — at the D2 deprecation date (2026-10-30, telemetry-gated) the self-hosted build deleted it, so retrieve from git history. (If implementation plan §9 Q4 was resolved as "keep the empty placeholder", the directory still exists — just refill it.) The recovered package is imported only by `cmd/api-saashosted`.
2. Add `services/api/internal/middleware/auth_kinde.go` (Kinde JWT validation — a thin wrapper around `services/shared/jwks/`, which was already lifted out of `auth.go` in B2 per architect S3). Wire it from `cmd/api-saashosted/main.go` via the `auth.Provider` interface (implementation plan §4.2 seam S9). The self-hosted `auth_native.go` is untouched.
3. Re-add Kinde environment variables (`KINDE_ISSUER`, `KINDE_M2M_CLIENT_ID`, `KINDE_M2M_CLIENT_SECRET`, `KINDE_WEBHOOK_SECRET`) to the SaaS deployment config.
4. In `cmd/api-saashosted/main.go`'s `ComposeServer` invocation (implementation plan §4.2 seam S6), swap in Kinde-backed implementations of the `auth.Inviter` (S1), `sso.Discoverer` (S4), and `sso.Connector` (S8) interfaces. Self-hosted continues to use the native implementations of all three.
5. Update `Tasks.md` Phase 3 #1 (Stripe), #9p (GDPR — full scope), #14 (invitations — Kinde Mgmt API path), #17 (SOC 2 — multi-tenant) to reactivated.
6. Spin up internal AxiaOps Cloud tenant in Kinde with the SOC 2 + GDPR posture documented.
7. First design partner onboarding via Kinde Hosted Login.

### 12.4 Enforcement modes

Same as sibling §12.3: `optional`, `preferred`, `required`. Under SaaS, "native password blocked" means "Kinde email/password login blocked at Kinde Mgmt API config" — Kinde supports per-org enforcement of which connection types are allowed.

### 12.5 Default configuration

Fresh SaaS install ships with:
- Kinde auth enabled.
- Kinde social login (Google/Microsoft personal/GitHub) enabled by default.
- No SSO connection — first owner can configure post-signup.
- Enforcement = `optional`.
- Email-invitation flow enabled (Kinde Mgmt API path via the `auth.Inviter` interface — Tasks.md Phase 3 #14 pre-2026-04 design). Self-hosted's token-OOB delivery (D3 in implementation plan) is replaced at composition time by the Kinde-backed implementation.

---

## 13. Testing strategy

**See [`sso-integration-design.md` §13](sso-integration-design.md#13-testing-strategy)** for the unit/integration/e2e testing primitives. SaaS-specific deltas:

### 13.1 Kinde sandbox tenant

Spin up a separate Kinde tenant for integration tests; load it with synthetic SSO connections (mock IdPs); CI runs `make test-integration-saas` which exercises the full Kinde-brokered flow against the sandbox.

### 13.2 Test layout under separate-binaries

With Option B (§16.4), test files live next to the package they test — no build tags:
- `services/api/internal/auth/*_test.go` — native auth tests. Run uniformly under `go test ./...`. Self-hosted-only because the package is self-hosted-only.
- `services/api/internal/kinde/*_test.go` — Kinde Mgmt API + webhook signature tests. Run uniformly. SaaS-only because the package is SaaS-only.
- `services/api/internal/sso/*_test.go` — shared SSO logic (CRUD, JIT, audit). Run uniformly under both.

`make test` runs everything once. CI runs `make build-selfhosted` and `make build-saashosted` separately to confirm both binaries link.

### 13.3 Webhook signature verification tests

New for SaaS — verify the Kinde-Signature HMAC validation in `/v1/sso/kinde/webhook`. Reject tampered payloads, replays (timestamp window).

### 13.4 Pen-test surface (SaaS)

Same as sibling §13.4 + Kinde-specific:
- Webhook replay (Kinde signature without timestamp window).
- AxiaOps Cloud tenant isolation in Kinde (org A's connections invisible to org B).
- Kinde Mgmt API token scope minimization — verify our M2M token cannot read other customers' data even if compromised.

---

## 14. Phased delivery plan (SaaS reactivation)

> Effort estimates assume the self-hosted v1 has shipped and the team is one developer (or small team). All effort is *additive* to the self-hosted code already in place.

| Phase | Deliverables | Effort | Blocking deps | Success criteria |
|---|---|---|---|---|
| **A — SaaS reactivation prerequisites** | (1) Kinde Pro/Enterprise tier pricing confirmed and budgeted. (2) Build separation in place per §16.4 (separate `cmd/api-saashosted` binary recommended). (3) Re-introduce `services/api/internal/kinde/` package under the SaaS binary. (4) Internal AxiaOps Cloud Kinde tenant configured. (5) ADR-0002 written committing to dual-SKU. | M (2–3w) | **Either** (a) self-hosted v1 stable + ≥3 self-hosted customers paying (the standard ADR-0001 review trigger), **or** (b) a high-quality SaaS design partner explicitly demanding hosted access who is willing to sign quickly (the alternative trigger called out at the top of this doc). Resolving either gate is sufficient. | Dual-SKU CI passes; SaaS binary builds; Kinde sandbox connected. |
| **B — SaaS auth path + first SSO IdP (Entra OIDC, Kinde-brokered)** | (1) Kinde-backed `auth.Provider` impl wired in `cmd/api-saashosted/main.go` (implementation plan seam S9). (2) Kinde Mgmt API SSO connector (`services/api/internal/kinde/sso.go`) — implements the `sso.Connector` interface (seam S8). (3) Admin UX unchanged; constructor swaps the impl. (4) Kinde-backed `sso.Discoverer` impl wraps the native one (seam S4). (5) Kinde-backed `auth.Inviter` impl (seam S1). (6) Webhook handler `/v1/sso/kinde/webhook`. (7) Sub-processor disclosure on website (§11.9). | L (4–6w) | A. | Internal AxiaOps Cloud tenant logs in via our own Entra tenant via Kinde-brokered SSO with JIT provisioning. Handler/business-logic code is unchanged from self-hosted v1 — only the constructors in the SaaS composition root differ. |
| **C — SAML support (Kinde-brokered)** | Kinde Mgmt API SAML connector wired into admin UX; cert lifecycle surfaced (Kinde stores, we display). | M (2w) | B. | One paying customer's Okta-SAML works via SaaS. |
| **D — Generic OIDC + Entra group overflow** | Generic OIDC admin form (Kinde supports); Microsoft Graph fallback **if Kinde supports** (else document as known limitation). | M (2w) | B. | Test against `mockoidc` + real customer. |
| **E — SCIM 2.0** | Kinde-supported SCIM endpoints (Kinde may host these directly; or we proxy). | XL (6–8w if Kinde provides, else 8–10w) | D + customer demand. | Entra SCIM round-trips. |

**Total SaaS reactivation effort (A+B+C+D)**: ~10–13 weeks. Phase E (SCIM) post-v1 SaaS, conditional on demand.

**Compare to self-hosted v1 (16–20w)**: SaaS reactivation is faster because the data model, admin UX, JIT logic, and audit pipeline are already shipped. Phase B is mostly admin-UX rewiring + Kinde Mgmt API plumbing — no cryptography work.

### 14.1 Out of scope until further notice (SaaS)

- Cross-SKU migration tooling (self-hosted ↔ SaaS).
- Customer-supplied JWT signing keys.
- Per-customer Kinde tenant (we use one shared Kinde tenant with org-per-customer; revisit if a customer demands their own Kinde).
- Native auth in the SaaS build (would defeat the purpose of using Kinde).

---

## 15. Open questions (SaaS reactivation)

These should be revisited at the moment of reactivation, not before:

1. **Kinde Pro/Enterprise tier pricing.** ≤€500/mo was the working assumption pre-2026-04. Re-quote at reactivation; pricing may have changed.
2. **Single Kinde tenant for all SaaS customers vs Kinde-org-per-customer?** Original Phase 1 design used Kinde-org-per-AxiaOps-org (1:1). Confirm this still holds; alternative (one Kinde org, AxiaOps-side org isolation only) reduces Kinde-side complexity but worsens blast radius if a Kinde-side misconfiguration leaks across customers.
3. **Stripe tier gating** (Tasks.md Phase 3 #1 — reactivated under SaaS). What tiers (Starter/Growth/Team/Enterprise) and which features gate which tier? SSO is typically Team-tier+ in B2B SaaS; ratify when reactivating.
4. **Cross-SKU customer migration** — if a self-hosted customer wants to "upgrade" to AxiaOps Cloud (we run it for them), what does that look like? Out of scope for v1 SaaS but worth scoping if 1+ customer asks.
5. **Kinde SCIM support** — Kinde may or may not have shipped SCIM brokering by reactivation date. Determines Phase E scope.
6. **Existing self-hosted customers and SaaS** — does AxiaOps Cloud onboarding require a fresh org, or can a self-hosted customer "import" their existing org? Likely fresh-org-only for v1; document as such.
7. **Pricing model — flat vs per-MAU vs per-AWS-account** — Kinde charges per-MAU. Pass through? Absorb? Charge per-AWS-account-connected? This is a business decision that affects unit economics.
8. **Same questions 5–10 from sibling §15** (first IdP design partner, invitation flow coexistence, owner-role-and-SSO, SCIM timing, single-tenant-vs-multi-tenant Entra app, audit retention) — apply identically.

---

## 16. Appendix — files that change for SaaS

### 16.1 New files (SaaS reactivation)

```
# SaaS-binary-only (imported only from cmd/api-saashosted)
services/api/cmd/api-saashosted/main.go           # SaaS composition root — wires Kinde middleware + handlers
services/api/internal/kinde/client.go             # M2M auth, retry, error mapping
services/api/internal/kinde/sso.go                # Kinde Mgmt API SSO connector wrapper
services/api/internal/kinde/users.go              # Kinde user CRUD via Mgmt API
services/api/internal/kinde/orgs.go               # Kinde org CRUD via Mgmt API
services/api/internal/kinde/webhook.go            # /v1/sso/kinde/webhook handler + HMAC verify + dispatch
services/api/internal/middleware/auth_kinde.go    # Kinde JWT validation — thin wrapper around services/shared/jwks/ (lifted there in B2 per architect S3); supplies Kinde issuer + JWT validation only. Implements the auth.Provider interface (seam S9).

# Test infra
services/api/internal/kinde/testutil/             # Kinde sandbox helpers
services/api/internal/sso/oidc_saas_test.go       # SaaS-specific integration tests

# Docs
docs/decisions/0002-managed-hosted-sku.md         # ADR for the dual-SKU commitment
docs/saas-runbook.md                              # operational runbook for AxiaOps Cloud
```

### 16.2 Files modified (SaaS reactivation)

```
# Composition / shared
services/api/cmd/api-selfhosted/main.go           # renamed from cmd/main.go at SaaS reactivation Phase A; B1 keeps cmd/main.go in place
services/api/internal/sso/handler.go              # Kinde Mgmt API calls behind a small interface that gets a Kinde-backed impl in cmd/api-saashosted and a noop/native impl in cmd/api-selfhosted
services/api/internal/sso/discover.go             # also consults Kinde when the Kinde-backed impl is wired

# Frontend (route-conditional, not build-conditional)
services/dashboard/src/screens/LoginScreen.jsx    # detects whether the API exposes Kinde redirect; behaviour switches at runtime, not build time
services/dashboard/src/screens/RegisterScreen.jsx # hidden when running against the SaaS API

# CI / build
Makefile                                          # add `make build-saashosted`; existing `make build` becomes `make build-selfhosted`
Dockerfile                                        # multi-target: api-selfhosted (default) and api-saashosted
.gitlab-ci.yml                                    # publish two image variants

# Compliance
docs/compliance/gdpr_plan.md                      # un-narrowed (full controller scope)
docs/compliance/soc2_plan.md                      # multi-tenant scope re-enabled
deploy/sub-processors-public.md                   # add Kinde

# Tasks.md
Tasks.md                                          # reactivate Phase 3 #1, #9p, #14, #17 for SaaS
```

### 16.3 Files NOT modified

```
services/shared/storage/postgres/migrations/022_sso_core.up.sql  # shared schema, no change
services/shared/model/sso.go                                     # types unchanged
services/api/internal/sso/jit.go                                 # JIT logic shared
services/api/internal/sso/handler.go (CRUD parts)                # admin UX backend reused
```

### 16.4 Build separation strategy

> **Naming note:** the "Option A" and "Option B" labels in this section are *local to §16.4* and refer to **build-separation strategies**. They are distinct from the architectural Option A/B/C in §3 (which refer to Kinde-brokered vs native vs hybrid auth). When cross-referenced from elsewhere in the doc, always cite as "§16.4 Option B" so the scope is clear.

Two viable options at reactivation:

**Option A — Build tags**
- One repo, one Go module per service.
- Files annotated with `//go:build saashosted` or `//go:build !saashosted`.
- `make build-saashosted` produces the SaaS binary; `make build-selfhosted` (default) produces the self-hosted binary.
- Pro: maximum code reuse at the file level.
- Cons: every test runs under a 2× matrix (`go test` defaults to one tag set; CI must explicitly run both); IDE goto-definition / refactoring tools get confused by tagged files; "where does this symbol come from" becomes a coin flip; subtle bugs from `//go:build !saashosted` files referencing a symbol only declared under `//go:build saashosted` are easy to introduce and hard to catch.

**Option B — Separate cmd binaries (recommended)**
- `services/api/cmd/api-selfhosted/main.go` (default) and `services/api/cmd/api-saashosted/main.go` (additive).
- Each `main.go` wires its own middleware chain (native auth vs Kinde) + composes shared packages.
- Shared internal packages (`internal/sso/handler`, `internal/sso/jit`, `internal/audit`, `services/shared/model`, `services/shared/storage`) imported by both unchanged.
- The Kinde-specific code lives in `services/api/internal/kinde/` and is imported only by `cmd/api-saashosted`. The native auth code in `services/api/internal/auth/` is imported only by `cmd/api-selfhosted`. No build tags on shared code.
- Pro: divergence is concentrated at composition root; tests run uniformly; static analysis works; new contributors can see at a glance what each SKU is. Con: ~20–40 lines of handler-registration boilerplate in each `main.go` (acceptable — drift is detectable in code review).

**Recommend Option B.** Reasoning:
1. **Divergence is small and concentrated**, not pervasive. ~6–8 files differ between SKUs (auth middleware, Kinde client, webhook handler, login screens). Spreading build tags across the codebase to fence off that small surface is heavier than just composing two `main.go`s.
2. **Test ergonomics**. With build tags, `make test` either misses the SaaS-tagged tests or has to run a 2× matrix. With separate binaries, `make test` runs everything once; CI runs both binaries against integration tests separately.
3. **Refactoring safety**. Build tags break IDE features and `go vet ./...`. Separate binaries don't.
4. **Drift is a smaller risk than build-tag traps**. Two `main.go`s with explicit handler registration get caught by code review; a missing build-tag annotation gets caught by a runtime "undefined symbol" panic at the worst possible time.

The original recommendation (Option A — build tags) was reversed on review — build-tag discipline scales worse than the doc originally claimed once a test matrix and IDE tooling are factored in.

### 16.5 Effort delta summary

```
Self-hosted v1 (already shipped, per ADR-0001):  16–20 weeks
SaaS reactivation (Phases A+B+C+D):              10–13 weeks (additive)
Combined dual-SKU at SaaS-reactivation point:    26–33 weeks of cumulative SSO/auth investment
```

The 10–13 weeks of SaaS reactivation is **less than half** of what a hypothetical Kinde-brokered v1 (Option A from the original sibling §3) would have cost from a clean slate, because the schema, admin UX, JIT logic, audit pipeline, and most security primitives are already in place from self-hosted v1.

---

## Cross-reference summary

This document is a **delta document** layered on top of [`sso-integration-design.md`](sso-integration-design.md). Sections that share content:

| Section | Sibling reference | Notes |
|---|---|---|
| §1 User stories | sibling §1 (verbatim) | One delta in §1.6 wording |
| §2 Goals/non-goals | sibling §2 (mostly) | Social login becomes in-scope; native auth becomes non-goal |
| §3 Architecture | NEW | Recommendation flips; alternatives still apply |
| §4 Data model | sibling §4 (verbatim schema) | `kinde_connection_id` becomes load-bearing |
| §5 API surface | sibling §5 (mostly) | ACS/callback/initiate endpoints removed; webhook added |
| §6 Frontend | sibling §6 (mostly) | Login screens → Kinde Hosted; admin UX reused |
| §7 SAML | sibling §7 (Kinde owns) | All cryptography moved to Kinde |
| §8 OIDC | sibling §8 (Kinde owns) | All cryptography moved to Kinde |
| §9 Entra | sibling §9 + redirect-URI delta | Customer admin steps largely identical |
| §10 JIT | sibling §10 (verbatim) | JIT hook fires post-Kinde-issuance |
| §11.1–§11.7 | sibling §11.1–§11.7 | Most concerns now Kinde's |
| §11.8 SOC 2/GDPR | NEW (wider scope) | Multi-tenant data processing back in scope |
| §11.9 Sub-processor | NEW (mandatory) | Kinde must be disclosed |
| §12 Migration | NEW | Coexistence with self-hosted |
| §13 Testing | sibling §13 + Kinde sandbox | Separate-binary CI parity check (`make build-selfhosted` + `make build-saashosted`) |
| §14 Phase plan | NEW | Faster than sibling because schema is reused |
| §15 Open questions | sibling §15 + SaaS-specific | Kinde pricing, dual-SKU questions |
| §16 Files | NEW | Separate-binary strategy (§16.4 Option B — `cmd/api-{selfhosted,saashosted}`) |
