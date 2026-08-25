# Enterprise SSO Integration — Design

Status: **design only** — not scheduled, not approved for implementation.
Owner: API service (auth middleware + new `sso` package). Touches `services/shared` (storage, model, crypto), `services/api` (handlers, middleware), `services/dashboard` (admin SSO settings + login domain discovery).

> **2026-04-29 — Recommendation flipped from Option A (Kinde-brokered) to Option B (native SAML + OIDC)** following acceptance of [ADR-0001 (Deployment Model — Self-hosted-first)](decisions/0001-deployment-model.md). Kinde is being removed from the product entirely; auth becomes native (email/password + native SSO) in line with the self-hosted-first GTM.
>
> **Sections updated**: §3.4 (recommendation flip), §3.5 (cutover triggers obsoleted), §3.6.3/§3.6.5 (deployment-tied recommendation), §8.1 (JWKS ownership), §9.1 (Entra redirect URI), §11.3 (alg-confusion mitigation moved to native auth path), §11.9 (Kinde sub-processor moot), §12 (migration & rollout rewritten for native), §14 (phase plan rewritten — Phase F removed), §15 (open questions resolved), §16 (file list reshaped).
>
> **Smaller cleanup pass also touched**: §1.2/§1.4/§1.6 (user stories — JWKS, session TTL, login flow phrasing), §2.1/§2.2 (goals/non-goals — "0 = native password only" and social-login non-goal), §5.4 (login discovery example URLs), §6.2/§6.3 (frontend login UX), §7.7 (SAML callback step 6g), §10.1 (JIT first-login pseudocode).
>
> **Sections preserved**: §3.1/§3.2/§3.3 (alternatives — kept as historical context), §4 data model (`kinde_connection_id` column kept as forward-compat for a hypothetical managed SKU), §5/§6/§7/§8/§9/§10/§11.1–§11.8/§13 (security and protocol details unchanged — the data model was deliberately portable). Some explanatory prose around `kinde_connection_id` in §4.2 / §5 endpoint table is intentionally kept since the column itself remains.

This document is the implementation contract for adding SAML 2.0 / OIDC / Microsoft Entra ID single sign-on to AxiaOps. It is written under the constraint that **JIT provisioning is the v1 user-creation model** alongside the existing `pending_memberships` invitation flow (`docs/invitation-flow.md`). Per ADR-0001, the runtime carrier is native — not Kinde.

A senior engineer should be able to take §3's recommendation and §5/§6 surface and start a branch.

---

## 1. User stories

### 1.1 Org admin configures SAML SSO

**As an organization owner, I want to configure SAML 2.0 SSO so that my company's IdP (e.g. Okta, ADFS) controls who can log in to AxiaOps.**

Acceptance criteria:
- Owner-only — `members:manage_admin` is **not** sufficient; SSO config requires `sso:manage` (owner-only).
- The config screen accepts an IdP metadata URL **or** a pasted IdP metadata XML blob; AxiaOps resolves entityID, SSO endpoint, and signing certificate from either.
- AxiaOps publishes its own SP metadata URL (`/v1/sso/saml/metadata?org=<id>`) for the IdP admin to consume.
- Saving the connection does not immediately enforce it — it lands in `status='draft'`.
- An audit_log row (`sso_connection_created`) is written with `metadata={protocol:"saml", entity_id:"..."}` — never the certificate or any secret.
- Connection state transitions are: `draft` → `active` → `disabled`.

### 1.2 Org admin configures OIDC (Entra ID) SSO

**As an organization owner, I want to configure OIDC for Microsoft Entra ID so that users authenticate via their Azure AD tenant.**

Acceptance criteria:
- The form asks for: Entra **tenant ID**, application (client) ID, client secret. The discovery URL is derived: `https://login.microsoftonline.com/{tenant_id}/v2.0/.well-known/openid-configuration`.
- Generic-OIDC alternative: a single "discovery URL" field plus client ID/secret.
- AxiaOps validates the discovery doc on save (issuer matches, JWKS reachable, supported `response_types_supported` includes `code`).
- Client secret is encrypted at rest via `axiaops.io/shared/crypto.Encrypt` with the same `ENCRYPTION_KEY` used for AWS account secrets.
- Saving fetches the JWKS once and caches it in `cache.Cache` under `sso:jwks:<connection_id>` for 1 hour via the shared `axiaops.io/shared/jwks` package (§8.1).
- Audit log written; the client secret is **never** logged or returned in the API response (write-only field).

### 1.3 Org admin verifies an email domain for JIT

**As an organization owner, I want to verify ownership of `@acme.com` so that any user signing in via SSO with that domain is auto-provisioned into my organization.**

Acceptance criteria:
- Initiating verification returns a unique TXT record value: `axiaops-domain-verification=<random-32-bytes-base64url>`.
- Owner adds the TXT record at `_axiaops.acme.com`; clicks **Verify**; AxiaOps performs DNS lookup and flips `sso_domains.status` from `pending` to `verified`.
- Verification cannot be re-claimed by another organization while a row is `verified` (DB unique index on `lower(domain) WHERE status='verified'`).
- Verifications expire after 90 days unless re-asserted (cron sweep flips to `stale` and emits a Prometheus counter; UI surfaces a warning).
- Public-suffix-list domains (gmail.com, outlook.com, free email providers) are **rejected with 422** — JIT for free-mail domains is forbidden.
- Audit: `sso_domain_verified` written with `metadata={domain:"acme.com"}`.

### 1.4 Org admin enforces SSO-only login

**As an organization owner, I want to enforce SSO-only login so that members cannot bypass our IdP via password or social login.**

Acceptance criteria:
- Setting `sso_connections.enforcement = 'required'` blocks any future authenticated request whose JWT did **not** flow through the org's SSO connection — middleware rejects with 403 + body `{"error":"sso_required"}`.
- Active sessions remain valid until expiry — the configured native session TTL (default ≤1h) bounds the bypass window. *(See §12.3 for the option to forcibly invalidate non-SSO sessions on enforcement change.)*
- Owner cannot lock themselves out: enforcing `required` requires the owner to have completed at least one successful SSO login within the last 24h (proven via `audit_log` lookup). Otherwise return 409 with a "test SSO first" error.
- Three enforcement modes are supported: `optional` (default — both SSO and native password work), `preferred` (login page defaults to SSO but native password still works), `required` (SSO only).
- Audit: `sso_enforcement_changed` with `metadata={from,to}`.

### 1.5 Org admin maps IdP groups to AxiaOps roles

**As an organization owner, I want to map IdP groups to AxiaOps roles so that my "AxiaOps-Admins" AD group becomes admin and "AxiaOps-Viewers" becomes viewer.**

Acceptance criteria:
- Admin enters a group identifier (SAML attribute value or OIDC group claim — typically GUID for Entra, group name for Okta) plus a role from `{admin, member, viewer}` — never `owner`.
- Multiple groups may map to the same role; one user matching multiple mapped groups gets the **highest-privilege** role (deterministic precedence: `admin > member > viewer`).
- Unmapped groups → user falls through to the **email-domain default role** configured on `sso_connections.default_role` (default: `viewer`).
- Group claims are reapplied on every login — if a user is removed from `AxiaOps-Admins` in Entra, on next SSO login their AxiaOps role is downgraded to whatever the remaining mappings yield.
- The org owner role is **never** assigned by group mapping. Promoting to owner is a separate explicit operation (`POST /v1/organizations/transfer-ownership`).

### 1.6 End user (existing member) signs in via SSO

**As an existing AxiaOps member, I want to log in through my company's IdP so that I don't need a separate password.**

Acceptance criteria:
- Login screen shows a "Sign in with SSO" entry point. User enters company email; AxiaOps looks up the verified domain and redirects to the IdP.
- After successful IdP authentication, AxiaOps lands the user on the dashboard with their existing role intact (group mapping may downgrade, never upgrade above their existing pinned role).
- The session cookie / token is set via the same native session path used by password auth — frontend does not need to know whether auth was native-password or SSO-flow.
- Audit: `sso_login_succeeded` with `metadata={connection_id, idp_subject_hash}`.

### 1.7 New user gets JIT-provisioned on first SSO login

**As a new employee whose email is on a verified domain, I want to get an AxiaOps account automatically on my first SSO login so that I don't need to wait for an admin invite.**

Acceptance criteria:
- On first SSO login: middleware looks up `sso_domains` for the email's domain; if `verified`, calls `JITProvisionUser(ctx, organizationID, email, name, externalID, groupClaims)`.
- `JITProvisionUser` is a single transaction: insert/upsert `users` row, insert `memberships` row with the role determined by §1.5 precedence, write `audit_log` row `sso_jit_provisioned`.
- A pending row in `pending_memberships` (from the existing email-invite flow) **takes precedence** over JIT — redeem the invitation first, ignore JIT for that user.
- JIT never assigns `owner` (§11).
- Free-mail domains are rejected at domain-verification time, so JIT cannot fire for `@gmail.com`.

### 1.8 Org admin disables/rotates an SSO connection

**As an organization owner, I want to disable or rotate an SSO connection so that I can respond to a compromised IdP secret or migrate IdPs.**

Acceptance criteria:
- `PATCH /v1/organizations/{id}/sso/connections/{cid}` accepts `{status: "disabled"}` or a new metadata URL / client secret.
- Disabling a connection while `enforcement='required'` is blocked with 409 unless a second connection is already `active` for the org, or `enforcement` is downgraded in the same request.
- Rotating a SAML cert: AxiaOps supports two trusted certs simultaneously (cert + previous_cert) for a 30-day overlap window; old cert is purged on day 30 by a cron sweep.
- Audit: `sso_connection_disabled` / `sso_connection_rotated`.

### 1.9 Auditor reviews SSO login + config-change audit trail

**As an internal auditor (read-only role), I want to see every SSO login and every SSO config change so that I can demonstrate access controls for SOC 2.**

Acceptance criteria:
- All SSO events (successful login, failed login, JIT provision, config CRUD, domain verify, enforcement change) appear in `audit_log` with `resource_type IN ('sso_connection','sso_domain','sso_group_mapping','sso_session')`.
- The existing `GET /v1/audit` endpoint already supports filtering by `?resource_type=...` and `?action=...` — no new API needed.
- SSO failure-mode events include enough context (error category, IdP issuer) but **never** include raw assertions, raw tokens, or secrets.
- Logs are retained per the existing audit retention policy.

### 1.10 AxiaOps support engineer diagnoses a failed SSO login

**As an AxiaOps support engineer, I want a "test connection" button and structured error logs so that I can diagnose why a customer's SSO is failing without asking them to share assertions over email.**

Acceptance criteria:
- The admin SSO screen has **Test connection** which performs a synthetic-but-real auth flow against the IdP and reports the outcome to the admin (no real session is created).
- Failure reasons are surfaced as enum codes: `metadata_unreachable`, `signature_invalid`, `clock_skew`, `assertion_replayed`, `email_missing`, `domain_unverified`, `unknown_user_no_jit`, `idp_returned_error`.
- Server-side `slog` logs the connection_id, error code, IdP issuer, and a SHA-256 hash of the assertion ID — never the assertion body.
- A Prometheus counter `axiaops_sso_login_total{outcome=success|failure, reason=<enum>, protocol=saml|oidc}` increments on every attempt.

---

## 2. Goals & non-goals

### 2.1 Goals (v1 design surface)

- Per-organization SSO configuration: each AxiaOps organization can have **0..N** SSO connections (0 = native password only, 1 typical, >1 for orgs with both legacy ADFS and a new Entra rollout).
- Protocols: **SAML 2.0**, generic OIDC, Microsoft Entra ID (OIDC flavour with quirks). Okta + Google Workspace covered transitively (Okta exposes both SAML and OIDC; Google Workspace is OIDC-shaped).
- **JIT user provisioning** with email-domain verification + group-to-role mapping.
- **Enforcement modes**: `optional`, `preferred`, `required`.
- **SOC 2-grade audit trail** for every SSO config change and every login attempt.
- **Encryption at rest** for IdP secrets via existing `crypto.Encrypt` (AES-256-GCM).
- **Backwards compatible**: orgs without SSO continue to use native email/password authentication.

### 2.2 Non-goals (v1)

- **SCIM 2.0** user/group sync — designed-for in the data model (§4 includes `external_id`, `external_directory_id`) but not implemented in v1. Phased delivery moves SCIM to **Phase E** (§14).
- **Social login** (Google/Microsoft personal/GitHub OAuth) — out of scope for self-hosted v1. Customers who want "Sign in with Google" point their Google Workspace at AxiaOps via OIDC instead. Revisit if a managed-hosted SKU is introduced.
- **MFA enforcement** — delegated entirely to the IdP. AxiaOps does not store MFA state.
- **Legacy WS-Federation** — explicitly out of scope. Customers on WS-Fed-only ADFS are asked to either upgrade to SAML 2.0 (ADFS supports both) or use the Entra OIDC path.
- **Just-in-time deprovisioning** — when a user is removed from the IdP, their AxiaOps membership persists until next login (where group mapping may downgrade them) or until SCIM is delivered. v1 documents this gap; admins can revoke manually.
- **IdP-initiated SLO (Single Logout)** — defer. v1 supports SP-initiated logout only (clears local AxiaOps session; IdP session lifetime is the IdP's problem).
- **RP-initiated logout** (`end_session_endpoint`, OIDC RP-Initiated Logout 1.0) — defer. AxiaOps logout revokes the AxiaOps session but does NOT chase the IdP's `end_session_endpoint` to also kill the realm cookie. The would-be exploit — *"a logged-out user types a different email at /login and silently re-auths as the previous user via the lingering IdP cookie"* — is closed by the `prompt=login` parameter on the authorize URL (`services/api/internal/sso/initiate.go`, pinned by `initiate_test.go`), which forces the IdP to render its login form regardless of cookie state. So the residual gap is purely UX: a user who clicks logout on AxiaOps is still logged in at Keycloak / Entra / Okta, and a sibling RP on the same realm would honour that. AxiaOps in v1 self-hosted is the only RP per realm (single-tenant-per-install per ADR-0001), so no sibling app exists to orphan into. Revisit when (a) a second RP joins the realm, (b) a customer security review flags the orphaned IdP session, or (c) we add a "reauth before destructive action" gate. Cost when revisited: store `id_token` on `sessions` (schema migration), conditional logout flow (SSO vs native), `post_logout_redirect_uri` registration in the IdP client.
- **B2C / consumer IdPs** — Sign in with Apple, Google personal accounts, etc. AxiaOps is B2B.

---

## 3. Architectural options — the core decision

The biggest decision in this doc: **do we extend Kinde to broker enterprise IdPs, build native SAML/OIDC in AxiaOps, or run a hybrid?**

### 3.1 Option A — Kinde-brokered (recommended for v1)

Kinde already supports per-organization enterprise SSO connections (SAML, OIDC, Entra, Okta, Google Workspace) on its paid tiers. AxiaOps would:
- Continue using Kinde as the only authentication boundary (`auth.go:142` JWT verification stays unchanged).
- Add an admin UX in AxiaOps that **calls Kinde Mgmt API** to create/update/delete SSO connections per Kinde organization.
- Add `sso_domains` and `sso_group_mappings` tables in AxiaOps (Kinde does not store these in a way that maps cleanly to our role model).
- Translate Kinde's `org_code` → `organizations.id` per existing pattern (`auth.go:167`); no change to JWT shape.
- Group claims arrive as Kinde-passthrough `groups` claim from the upstream IdP — Kinde forwards what the IdP sent.

**What AxiaOps writes itself:** domain verification, group→role mapping, enforcement state, JIT provisioning (post-token), audit log entries. The cryptographic surface (XML signature verification, OIDC token verification) is **not** in our codebase.

**Pros:**
- **Time to ship: weeks, not months.** No `crewjam/saml` integration, no SP cert lifecycle, no XML canonicalisation bugs to debug at 2am.
- **No new attack surface in our code.** SAML signature wrapping (CVE-rich category), OIDC algorithm confusion, replay caches — all Kinde's problem.
- Kinde already does cert rotation, JWKS rotation, IdP metadata refresh, and clock-skew handling.
- One auth code path in `auth.go` to maintain. JWKS caching infrastructure (`cache/`) is already wired (`auth.go:44-98`).
- SOC 2 evidence: Kinde's own SOC 2 Type II covers the auth control; we only need to evidence our config + audit pipeline.

**Cons:**
- **Vendor lock-in deepens.** Migrating away from Kinde becomes more painful — every customer's SSO setup is in Kinde.
- **Pricing pressure.** Kinde charges per-MAU on enterprise tiers, and SSO connections may be a paid feature gate. **NEEDS-CONFIRMATION:** verify Kinde Pro/Enterprise tier pricing before committing — this is a cost surface that could change the math.
- **Per-org Kinde config dependency.** Every customer SSO setup must be replicated in our Kinde tenant via Mgmt API; an outage of Kinde's Mgmt API blocks new SSO setups (not existing logins).
- **Limited control of IdP-side metadata.** If Kinde does not expose, e.g., `RequestedAuthnContext` or per-attribute mapping for a given protocol, we have no path forward without escalating to Kinde.
- **Logs live in two places.** Kinde keeps the assertion/token; we keep the post-translation audit. Forensics need both.

### 3.2 Option B — Native (in-AxiaOps) SAML + OIDC

AxiaOps implements SP-side SAML 2.0 (via `crewjam/saml`) and an OIDC RP, bypassing Kinde for SSO-enabled organizations. Kinde stays for non-SSO orgs.

**Pros:**
- **No vendor lock-in.** Migration path off Kinde is owned by us, not gated on a third party.
- **Full control** over assertion mapping, attribute schemas, error reporting, replay caches.
- **No per-MAU cost increase** for adding SSO; cost lives in our codebase complexity.
- **Federated logs** in one place (our audit_log).

**Cons:**
- **Months, not weeks.** SAML alone is a 4–6 week investment for a single engineer; OIDC RP is another 1–2 weeks; ongoing cert/key lifecycle is permanent overhead.
- **Cryptographic surface** added to AxiaOps codebase: XML signature verification, canonicalisation, OIDC token validation, replay caches, clock-skew handling.
- **Two auth code paths** in `auth.go` — one for Kinde JWTs, one for native SSO sessions. Risk: drift, missed-perm checks on the second path.
- **Higher SOC 2 evidence burden.** We carry the auth control, so every CVE in `crewjam/saml` becomes our incident.
- **Pen-test surface** explodes: assertion replay, signature wrapping, open redirects on `RelayState`, XML external entity attacks if we misconfigure the parser.

### 3.3 Option C — Hybrid (Kinde for OIDC + Entra; native for SAML)

Reasoning: Kinde does OIDC well; SAML is where Kinde's abstraction may leak (per-attribute mapping, cert rotation cadence). Implement SAML natively, keep OIDC on Kinde.

**Pros:**
- Most enterprise prospects in our ICP (mid-market FinOps buyers) are on Entra — OIDC covers ~70% of demand cheaply via Kinde.
- Native SAML scratches the long-tail (Okta-on-SAML, ADFS, niche IdPs).

**Cons:**
- **Worst-of-both-worlds operationally**: two code paths *and* a Kinde dependency *and* a `crewjam/saml` dependency.
- **Migration story is fractured**: an org that started on Kinde-OIDC and then wants to consolidate on SAML would see provisioning split across systems.
- **Higher cognitive load** for support — failure modes differ per protocol.

### 3.4 Recommendation: **Option B — Native (in-AxiaOps) SAML + OIDC.**

> **Changed 2026-04-29** following [ADR-0001 acceptance](decisions/0001-deployment-model.md). The original recommendation was Option A (Kinde-brokered) conditional on AxiaOps remaining SaaS-only. ADR-0001 selected self-hosted-first as v1, which forces Option B per §3.6 — Kinde cannot be reached from a customer-self-hosted deployment. The original Option A reasoning (single-dev team, weeks-not-months) is preserved in §3.1 as historical context for the alternatives weighed.

Reasoning:
1. **Deployment compatibility (load-bearing).** §3.6 establishes that Option A fails for customer-self-hosted: Kinde is unreachable from many customer networks; the AxiaOps Kinde tenant cannot be administered by self-hosted customers; introducing a third-party auth dependency negates the "you own the data" value proposition. Option B is the only path consistent with ADR-0001.
2. **Single auth code path.** With Kinde removed entirely, `auth.go` carries one path (native session validation) rather than two (Kinde JWT + native). Eliminates the drift risk called out as a Con of Option B in §3.2.
3. **No Phase F cutover ever needed.** §14 originally listed Phase F as future risk insurance; under self-hosted-first it is removed. Capacity that would have gone into Phase F flows into Phases B/C/D.
4. **Cost.** No Kinde subscription — recurring infra cost drops to zero on the auth axis. The cryptographic-surface engineering investment (XML signature verification, OIDC RP) is paid once and amortized across all customers and across both deployment models if a managed SKU is added later.
5. **Compliance.** Self-hosted narrows SOC 2 scope (we don't process customer billing data); §11.9 Kinde-as-sub-processor disclosure becomes moot. Customers' IdPs are *their* identity providers, not our sub-processors.

Trade-offs accepted:
- **8–10 weeks of Phase B native-OIDC plumbing** (§14) instead of 4–6 weeks under Option A. One-time cost.
- **Pen-test surface added to AxiaOps codebase**: XML signature wrapping, OIDC algorithm confusion, replay caches. Mitigated in §11.2/§11.3.
- **We carry the auth control for SOC 2.** No Kinde SOC 2 Type II to ride; we evidence the control ourselves. Self-hosted scope reduction (§11.8) compensates.

### 3.5 ~~Trigger conditions for native cutover~~ — Obsolete

> **Obsolete as of 2026-04-29.** This section originally listed conditions under which AxiaOps would migrate from Option A (Kinde-brokered) to Option B (native). With ADR-0001's self-hosted-first decision, **Option B is the v1 path** — there is no cutover from A to B because A was never built. Section retained for historical context.

The original triggers — pricing, capability gap, lock-in pain, compliance, outage tolerance, **and self-hosted SKU on roadmap** — are listed below. The last one is what triggered the decision. The data model in §4 was deliberately portable between options A and B; that portability is now forward-compat insurance for a hypothetical "managed AxiaOps Cloud" SKU rather than reverse-compat for an in-flight Option A build.

Original triggers (all moot after 2026-04-29):
- Pricing: Kinde enterprise tier exceeds 30% of gross margin per customer.
- Capability gap: paying customer demands a feature Kinde won't ship within 90 days.
- Lock-in pain: customer asks for portable SSO config export Kinde can't provide.
- Compliance: customer requires AxiaOps-controlled key material for signing.
- Outage tolerance: Kinde outage breaks login SLAs.
- **Self-hosted SKU on roadmap** — *this is what triggered ADR-0001.*

The data model in §4 remains portable: if a future managed-hosted SKU re-introduces Kinde for hosted customers only, the existing `sso_connections` schema accommodates both runtimes via `kinde_connection_id` (currently unused under Option B).

### 3.6 Deployment-model fit (SaaS vs self-hosted)

The §3 choice is partly a function of how AxiaOps itself is deployed. Today AxiaOps is **multi-tenant SaaS only** (Phase 1–3); a self-hosted enterprise SKU is not on the current roadmap. But buyers in regulated verticals (banking, healthcare, public sector) routinely demand on-prem or VPC-isolated software. The auth boundary should not foreclose that path even if we don't build it now.

#### 3.6.1 SSO option × deployment model

| Deployment model | Description | Option A (Kinde) | Option B (native) | Option C (hybrid) |
|---|---|:-:|:-:|:-:|
| **Multi-tenant SaaS** (current) | One stack, all customers share infra (RLS-isolated). | ✅ **best fit** | ✅ works, more code | ⚠️ no benefit |
| **Single-tenant managed SaaS** | AxiaOps runs a dedicated stack per customer (e.g. a dedicated ECS Express stack per tenant). | ✅ works | ✅ works | ⚠️ |
| **Customer-self-hosted** | Customer runs AxiaOps containers in their own AWS/GCP/Azure/on-prem. | ❌ Kinde dependency untenable | ✅ **required** | ❌ |
| **Air-gapped self-hosted** | No outbound internet (regulated/classified networks). | ❌ Kinde unreachable | ✅ **required** + local IdP | ❌ |

**Why Option A fails for self-hosted:**
- Kinde is a hosted authentication service. A self-hosted AxiaOps deployment would need outbound HTTPS to Kinde on every login (auth code exchange, JWKS fetch). Many target customers won't allow that egress.
- The AxiaOps Kinde tenant is owned by AxiaOps Inc — a self-hosted customer can't administer their own SSO connections without us granting per-customer Mgmt-API access, which is a non-starter both contractually and operationally.
- Self-hosted customers are buying *control*. Introducing a third-party auth dependency negates the value proposition and turns "you own the data" into "you own the data, but Kinde gates access to it."

#### 3.6.2 IdP compatibility by deployment model

| IdP | Multi-tenant SaaS | Customer-self-hosted | Air-gapped |
|---|---|---|---|
| **Microsoft Entra ID** (cloud) | ✅ via Kinde or native OIDC | ✅ native OIDC (customer's tenant) | ❌ requires reaching `login.microsoftonline.com` |
| **Okta** (cloud) | ✅ via Kinde or native OIDC/SAML | ✅ native OIDC/SAML | ❌ requires reaching Okta cloud |
| **Google Workspace** (cloud) | ✅ via Kinde or native OIDC | ✅ native OIDC | ❌ |
| **ADFS** (on-prem) | ✅ native SAML | ✅ native SAML — natural fit | ✅ **natural fit** if ADFS is on the local network |
| **Keycloak** (self-hostable) | ⚠️ rare in SaaS-buyer ICP | ✅ native SAML/OIDC | ✅ runs locally; recommended air-gap broker |
| **Authentik / ZITADEL / Dex** | ⚠️ rare | ✅ native | ✅ runs locally |
| **Generic LDAP / AD** (no SAML/OIDC layer) | — | ⚠️ needs intermediary (Keycloak/ADFS) | ⚠️ same |

#### 3.6.3 Recommendation tied to deployment

> **Updated 2026-04-29** to reflect ADR-0001 acceptance. The original three-bullet recommendation conditional on the deployment direction is replaced with the resolved direction.

- **v1 (self-hosted-first per ADR-0001):** **Option B (native)** is the path. Customer runs AxiaOps in their own infrastructure; no Kinde dependency in the binary. `auth.go` is rewritten for native session validation in Phase B. The work is §7 (SAML library), §8 (native OIDC RP), and the §4 data model (already portable).
- **If/when a managed AxiaOps Cloud SKU is introduced** (≥3 self-hosted customers paying — the ADR-0001 review trigger): re-evaluate whether to add Kinde *only for the hosted variant*. The `sso_connections.kinde_connection_id` column is the forward-compat hook. Build that as a SaaS-only path behind a build tag (`//go:build saashosted`) without disturbing the self-hosted binary.
- **Single-tenant managed SaaS as an interim** (one regulated customer who insists on hosted): native auth still works — operate AxiaOps as the customer's vendor on a dedicated stack. No Kinde introduction needed.

#### 3.6.4 Air-gapped specifics (forward-looking, not v1)

- **SAML > OIDC** in air-gapped: SP↔IdP exchange is metadata-XML based and can be fully pre-staged offline. OIDC requires `.well-known/openid-configuration` reachability — fine if the local IdP exposes it, fails if the customer expects to point at a public cloud IdP.
- **JWKS rotation** must work over the local network only — no `login.microsoftonline.com` round-trips. Cache JWKS aggressively; allow long stale-while-revalidate windows.
- **Recommended stack for an air-gapped pilot**: Option B with SAML primary, OIDC against a customer-local Keycloak as secondary, **zero Kinde dependency in the binary** for that build.

#### 3.6.5 Practical implication

> **Resolved 2026-04-29.** §15 Q11 (self-hosted SKU) was answered via ADR-0001 — self-hosted-first as v1. Option A is therefore not built; Option B is the path. The §4 data-model portability remains useful as insurance for a future managed-hosted SKU.

---

## 4. Data model changes

Five new tables across the v1 surface. All are **identical for Kinde-brokered and native** — only the runtime caller differs.

### 4.1 Migration `021_sso_core.up.sql`

```sql
-- 021_sso_core.up.sql
-- Enterprise SSO: per-organization IdP connections, verified email domains,
-- and group→role mappings. Anticipates SCIM (external_id columns) without
-- shipping it. See docs/sso-integration-design.md.

SET search_path TO axiaops;

-- ── sso_connections ─────────────────────────────────────────────────────────
-- One row per (organization, IdP). Multiple connections per org allowed (e.g.
-- staged Entra rollout while ADFS still serves a subset). Routing decision at
-- login is by email domain via sso_domains.
--
-- secrets stored in client_secret_ciphertext are AES-256-GCM via crypto.Encrypt
-- with the same ENCRYPTION_KEY used for accounts.aws_secret_key_ciphertext.
-- saml_signing_cert and saml_previous_cert are PEM-encoded — public material,
-- not encrypted. The IdP's signing certs.

CREATE TABLE IF NOT EXISTS sso_connections (
    id                          TEXT        PRIMARY KEY,
    organization_id             TEXT        NOT NULL REFERENCES organizations(id) ON DELETE CASCADE,

    protocol                    TEXT        NOT NULL CHECK (protocol IN ('saml','oidc')),
    label                       TEXT        NOT NULL DEFAULT '',  -- "Acme Okta", "Entra prod"
    status                      TEXT        NOT NULL DEFAULT 'draft'
                                            CHECK (status IN ('draft','active','disabled')),
    enforcement                 TEXT        NOT NULL DEFAULT 'optional'
                                            CHECK (enforcement IN ('optional','preferred','required')),
    default_role                TEXT        NOT NULL DEFAULT 'viewer'
                                            CHECK (default_role IN ('admin','member','viewer')),

    -- IdP metadata (both protocols)
    idp_issuer                  TEXT        NOT NULL DEFAULT '',
    idp_metadata_url            TEXT        NOT NULL DEFAULT '',
    idp_metadata_xml            TEXT        NOT NULL DEFAULT '',  -- pasted alternative to URL

    -- OIDC fields
    oidc_client_id              TEXT        NOT NULL DEFAULT '',
    oidc_client_secret_ciphertext BYTEA     NOT NULL DEFAULT '\x'::bytea,
    oidc_discovery_url          TEXT        NOT NULL DEFAULT '',
    oidc_tenant_id              TEXT        NOT NULL DEFAULT '',  -- Entra-specific

    -- SAML fields
    saml_sso_url                TEXT        NOT NULL DEFAULT '',
    saml_signing_cert           TEXT        NOT NULL DEFAULT '',  -- PEM, current
    saml_previous_cert          TEXT        NOT NULL DEFAULT '',  -- PEM, rotation overlap
    saml_previous_cert_expires_at TIMESTAMPTZ,

    -- Pointer back to Kinde when Option A is the runtime (NEEDS-CONFIRMATION on field name)
    kinde_connection_id         TEXT        NOT NULL DEFAULT '',

    -- SCIM forward-compat (filled later, never read in v1)
    scim_token_ciphertext       BYTEA       NOT NULL DEFAULT '\x'::bytea,
    scim_endpoint               TEXT        NOT NULL DEFAULT '',

    created_by_user_id          TEXT        REFERENCES users(id) ON DELETE SET NULL,
    created_at                  TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at                  TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE INDEX IF NOT EXISTS sso_connections_org_status_idx
    ON sso_connections (organization_id, status);

GRANT SELECT, INSERT, UPDATE, DELETE ON sso_connections TO axiaops;
ALTER TABLE sso_connections ENABLE ROW LEVEL SECURITY;
DO $$ BEGIN
    CREATE POLICY sso_connections_org_isolation ON sso_connections
        USING (organization_id = current_setting('app.organization_id', true))
        WITH CHECK (organization_id = current_setting('app.organization_id', true));
EXCEPTION WHEN duplicate_object THEN NULL;
END $$;

-- ── sso_domains ─────────────────────────────────────────────────────────────
-- Verified email domains. Login-page lookup: email → domain → (org, connection).
-- A domain can be claimed by exactly one organization at a time (partial unique
-- index on status='verified').
--
-- Public-suffix-list domains (gmail.com, outlook.com, ...) are rejected at
-- handler-level — no DB constraint (the PSL changes; keep it in code).

CREATE TABLE IF NOT EXISTS sso_domains (
    id                  TEXT        PRIMARY KEY,
    organization_id     TEXT        NOT NULL REFERENCES organizations(id) ON DELETE CASCADE,
    sso_connection_id   TEXT        NOT NULL REFERENCES sso_connections(id) ON DELETE CASCADE,

    domain              TEXT        NOT NULL,
    status              TEXT        NOT NULL DEFAULT 'pending'
                                    CHECK (status IN ('pending','verified','stale','revoked')),
    verification_token  TEXT        NOT NULL,  -- "axiaops-domain-verification=..."

    verified_at         TIMESTAMPTZ,
    last_asserted_at    TIMESTAMPTZ,           -- updated on re-verify (90-day cadence)
    expires_at          TIMESTAMPTZ,           -- verified_at + 90d, swept by cron

    created_at          TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at          TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

-- A domain can be verified by exactly one org globally.
CREATE UNIQUE INDEX IF NOT EXISTS sso_domains_one_verified_per_domain
    ON sso_domains (lower(domain)) WHERE status = 'verified';

-- Login-page hot path: by domain.
CREATE INDEX IF NOT EXISTS sso_domains_lookup_idx
    ON sso_domains (lower(domain), status);

-- Cron sweep: stale domains.
CREATE INDEX IF NOT EXISTS sso_domains_expiry_idx
    ON sso_domains (expires_at) WHERE status = 'verified';

GRANT SELECT, INSERT, UPDATE, DELETE ON sso_domains TO axiaops;
ALTER TABLE sso_domains ENABLE ROW LEVEL SECURITY;
DO $$ BEGIN
    CREATE POLICY sso_domains_org_isolation ON sso_domains
        USING (organization_id = current_setting('app.organization_id', true))
        WITH CHECK (organization_id = current_setting('app.organization_id', true));
EXCEPTION WHEN duplicate_object THEN NULL;
END $$;

-- ── sso_group_mappings ──────────────────────────────────────────────────────
-- IdP group identifier → AxiaOps role. group_external_id is whatever the IdP
-- sends — Entra GUID, Okta group name, generic SAML attribute value. We
-- compare with strings; case-sensitivity preserved (Entra is case-sensitive on
-- GUIDs, Okta is case-insensitive on names — admins stage their inputs).
-- Owner is deliberately excluded from the role check.

CREATE TABLE IF NOT EXISTS sso_group_mappings (
    id                      TEXT        PRIMARY KEY,
    organization_id         TEXT        NOT NULL REFERENCES organizations(id) ON DELETE CASCADE,
    sso_connection_id       TEXT        NOT NULL REFERENCES sso_connections(id) ON DELETE CASCADE,

    group_external_id       TEXT        NOT NULL,
    group_display_name      TEXT        NOT NULL DEFAULT '',
    role                    TEXT        NOT NULL CHECK (role IN ('admin','member','viewer')),

    created_at              TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    UNIQUE (sso_connection_id, group_external_id)
);

GRANT SELECT, INSERT, UPDATE, DELETE ON sso_group_mappings TO axiaops;
ALTER TABLE sso_group_mappings ENABLE ROW LEVEL SECURITY;
DO $$ BEGIN
    CREATE POLICY sso_group_mappings_org_isolation ON sso_group_mappings
        USING (organization_id = current_setting('app.organization_id', true))
        WITH CHECK (organization_id = current_setting('app.organization_id', true));
EXCEPTION WHEN duplicate_object THEN NULL;
END $$;

-- ── sso_assertion_replay (Option B native SAML only) ────────────────────────
-- Replay-protection cache for SAML assertion IDs. TTL = NotOnOrAfter + skew.
-- Created here as an empty placeholder so the migration doesn't fork by
-- option; the table is unused under Option A (Kinde handles replay protection).

CREATE TABLE IF NOT EXISTS sso_assertion_replay (
    assertion_id        TEXT        PRIMARY KEY,
    sso_connection_id   TEXT        NOT NULL REFERENCES sso_connections(id) ON DELETE CASCADE,
    seen_at             TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    expires_at          TIMESTAMPTZ NOT NULL
);
CREATE INDEX IF NOT EXISTS sso_assertion_replay_expires_idx
    ON sso_assertion_replay (expires_at);
GRANT SELECT, INSERT, DELETE ON sso_assertion_replay TO axiaops;

-- ── users + memberships additions ──────────────────────────────────────────

ALTER TABLE users
    ADD COLUMN IF NOT EXISTS sso_external_id TEXT NOT NULL DEFAULT '',
    ADD COLUMN IF NOT EXISTS sso_connection_id TEXT REFERENCES sso_connections(id) ON DELETE SET NULL;

CREATE INDEX IF NOT EXISTS users_sso_external_idx
    ON users (sso_connection_id, sso_external_id)
    WHERE sso_connection_id IS NOT NULL;

ALTER TABLE memberships
    ADD COLUMN IF NOT EXISTS provisioned_via TEXT NOT NULL DEFAULT 'manual'
        CHECK (provisioned_via IN ('manual','invitation','jit','scim'));
```

Down migration drops the new tables in reverse FK order and removes the columns. Standard pattern.

### 4.2 Why these shapes

- **`sso_connections.idp_metadata_url` and `idp_metadata_xml` both present**: customers split roughly 50/50 on which they can paste. Pick one; keep both columns nullable; handler validates exactly-one-set.
- **`oidc_client_secret_ciphertext` is `BYTEA`**: same pattern as `accounts.aws_secret_key_ciphertext` — encrypted blob, never returned in API responses.
- **`saml_previous_cert` + `saml_previous_cert_expires_at`**: enables zero-downtime cert rotation. UX is "paste new cert, old cert auto-expires in 30 days." Cron sweep clears expired previous-certs.
- **`kinde_connection_id`**: only populated under Option A; lets us call back into Kinde Mgmt API by ID without round-tripping our org_code → IdP type → connection lookup. Empty string under Option B. **Validation under self-hosted (Option B)**: handlers in `services/api/internal/sso/handler.go` reject any non-empty value on `POST` / `PATCH` with a 422. (The mirror rule under a future SaaS reactivation — handlers reject the empty string — is captured in [`sso-integration-design-saas.md` §4](sso-integration-design-saas.md#4-data-model). Keeping both rules in their respective docs avoids drift.)
- **`scim_endpoint` + `scim_token_ciphertext`**: forward-compat. Phase E populates these.
- **`users.sso_external_id`**: the IdP's stable subject identifier (`sub` for OIDC, NameID for SAML). Indexed with `sso_connection_id` so re-login finds the same user even if email changes (hard requirement for SCIM later).
- **`memberships.provisioned_via`**: lets the UI distinguish "JIT-provisioned, role from group claim" vs "manually invited" — important UX cue for admins reviewing the team list.

### 4.3 RLS

Every new table has an `organization_id` column and an RLS policy mirroring `memberships` and `pending_memberships`. Cross-org reads are blocked at the DB level — same guarantee as the rest of the codebase.

### 4.4 Encryption

OIDC client secrets and SCIM tokens use `crypto.Encrypt(key, plaintext)` with `ENCRYPTION_KEY`. No new key material. Decrypt only happens in the SSO callback handler (where the secret is needed to swap code for token); never returned over the API.

### 4.5 Audit-log additions

New action constants in `services/shared/model/audit.go`:

```go
AuditActionSSOConnectionCreated     = "sso_connection_created"
AuditActionSSOConnectionUpdated     = "sso_connection_updated"
AuditActionSSOConnectionDeleted     = "sso_connection_deleted"
AuditActionSSOConnectionDisabled    = "sso_connection_disabled"
AuditActionSSOEnforcementChanged    = "sso_enforcement_changed"
AuditActionSSODomainVerificationStarted = "sso_domain_verification_started"
AuditActionSSODomainVerified        = "sso_domain_verified"
AuditActionSSODomainRevoked         = "sso_domain_revoked"
AuditActionSSOGroupMappingChanged   = "sso_group_mapping_changed"
AuditActionSSOLoginSucceeded        = "sso_login_succeeded"
AuditActionSSOLoginFailed           = "sso_login_failed"
AuditActionSSOJITProvisioned        = "sso_jit_provisioned"
```

All added to `ValidAuditActions` in the same file. No schema change needed for `audit_log` — it already has `metadata JSONB` for connection_id/protocol/reason.

---

## 5. API surface

All paths under `/v1/`. Permission column refers to existing `authz.Permission` constants where reusable; new constants flagged.

### 5.1 New permission constants

In `services/shared/authz/roles.go`:

```go
PermSSORead           Permission = "sso:read"           // viewer+
PermSSOManage         Permission = "sso:manage"         // owner only
PermSSODomainVerify   Permission = "sso:domain_verify"  // owner only
```

`sso:manage` is owner-only because misconfiguring SSO can lock out the entire org. Admin can read state but cannot mutate.

### 5.2 Endpoints

| Method | Path | Permission | Purpose |
|---|---|---|---|
| `GET` | `/v1/organizations/{id}/sso/connections` | `PermSSORead` | List connections for org. Returns metadata; **never** returns secrets, certs, or `kinde_connection_id`. |
| `POST` | `/v1/organizations/{id}/sso/connections` | `PermSSOManage` | Create a draft connection. Body validated by protocol (§7, §8). Returns `{id, status:'draft', sp_metadata_url, ...}`. |
| `GET` | `/v1/organizations/{id}/sso/connections/{cid}` | `PermSSORead` | Fetch one connection. Secrets redacted. |
| `PATCH` | `/v1/organizations/{id}/sso/connections/{cid}` | `PermSSOManage` | Update label, status, enforcement, default_role, IdP metadata, secrets. Each field optional. |
| `DELETE` | `/v1/organizations/{id}/sso/connections/{cid}` | `PermSSOManage` | Hard-delete. Blocks if `enforcement='required'` and no other active connection. |
| `POST` | `/v1/organizations/{id}/sso/connections/{cid}/test` | `PermSSOManage` | Synthetic auth — validates metadata reachability, JWKS fetch, returns enum reason on failure. No session created. |
| `POST` | `/v1/organizations/{id}/sso/connections/{cid}/refresh-metadata` | `PermSSOManage` | Force re-fetch of `idp_metadata_url`. Updates `idp_metadata_xml` cache. |
| `POST` | `/v1/organizations/{id}/sso/domains` | `PermSSODomainVerify` | Start domain verification. Returns `{domain, verification_token, txt_record_name}`. |
| `GET` | `/v1/organizations/{id}/sso/domains` | `PermSSORead` | List domains + statuses. |
| `POST` | `/v1/organizations/{id}/sso/domains/{did}/verify` | `PermSSODomainVerify` | Trigger DNS lookup for `_axiaops.{domain}` TXT. Flips status. |
| `DELETE` | `/v1/organizations/{id}/sso/domains/{did}` | `PermSSODomainVerify` | Revoke. |
| `GET` | `/v1/organizations/{id}/sso/group-mappings` | `PermSSORead` | List. |
| `PUT` | `/v1/organizations/{id}/sso/group-mappings` | `PermSSOManage` | Replace whole set (transactional). |
| `GET` | `/v1/sso/discover?email={email}` | none (rate-limited) | Login-page domain discovery. Returns `{has_sso: bool, redirect_url?: string}`. **Never** returns 404 vs 200 in a way that lets attackers enumerate domains — always 200 with `has_sso:false` for unknown. |
| `POST` | `/v1/sso/saml/{cid}/acs` | none (signed assertion) | SAML Assertion Consumer Service — IdP POSTs SAMLResponse here. Native (Option B) only. Under Option A, Kinde owns the ACS. |
| `GET` | `/v1/sso/saml/{cid}/metadata` | none | Publish SP metadata XML for IdP admin. Native only. |
| `GET` | `/v1/sso/oidc/{cid}/callback` | none (state-bound) | OIDC redirect URI. Native only. |
| `GET` | `/v1/sso/oidc/{cid}/initiate` | none (rate-limited) | SP-initiated OIDC start: builds the IdP authorisation URL with PKCE + opaque `state`, 302s the browser. Email passed as a query param so the connection can be selected. Native only. |
| `GET` | `/v1/sso/saml/{cid}/initiate` | none (rate-limited) | SP-initiated SAML start: builds `SAMLAuthRequest`, mints CSRF-bound `RelayState`, 302s the browser. Native only. |

### 5.3 Permission model

- **`sso:read`**: granted to viewer/member/admin/owner — anyone can see *that* SSO is configured (informational on the team list). Secrets, certs, and `kinde_connection_id` never leave the server.
- **`sso:manage`**: owner only. Configuring SSO has lockout potential — admins must escalate.
- **`sso:domain_verify`**: owner only. Same blast radius as connection management.
- **All mutations write `audit_log`** via the existing `axiaops.io/api/internal/audit` helper (`audit.go`).

### 5.4 Login-page discovery flow

```
1. User enters email at /login.
2. Dashboard calls GET /v1/sso/discover?email=alice@acme.com.
3. Server: parse domain, lookup sso_domains WHERE lower(domain)='acme.com' AND status='verified'.
4. If found → response: `{has_sso:true, redirect_url:"/v1/sso/oidc/<cid>/initiate?email=...", protocol:"oidc"}`.
5. If not found → response: `{has_sso:false}`. Dashboard falls through to the native email/password login form.
6. Rate limit: 30 req/min/IP via existing ratelimit middleware (same as auth endpoints).
```

The `redirect_url` is always an AxiaOps-internal path — `/v1/sso/{protocol}/{cid}/initiate` — under self-hosted (no third-party broker). The initiate handler builds the IdP redirect with the appropriate protocol-specific parameters and 302s the browser to the IdP.

**Domain enumeration / timing oracle**: the discovery endpoint exposes a `cid` in the `redirect_url` for `has_sso:true` responses. `cid` is an opaque random ID (not enumerable), so leakage is bounded — but the success path still differs from the not-found path in body size and processing time. To keep the response shape constant:
- Both branches return HTTP 200 with the same JSON shape (`has_sso` toggles, `redirect_url` is `""` when false).
- Add ~5ms of constant work to the `has_sso:false` branch to mask the DB lookup latency on the success path. Document the constant in code; revisit if it shows up as a measurable side channel during pen-test (§13.4).
- Rate limit (30 req/min/IP) bounds enumeration regardless.

---

## 6. Frontend changes

### 6.1 Admin SSO settings screen

New page: `services/dashboard/src/pages/settings/SSO.jsx` reachable from Settings → Single Sign-On (visible only to owners — gated by `me.permissions` containing `sso:read`).

Sections:
- **Connections** — list + add/edit/delete buttons. Add flow is a wizard: protocol → (SAML metadata XML upload OR OIDC discovery URL) → label → save as draft. Edit form same shape.
- **Domains** — verified-domain list + "Verify a domain" CTA. Verify flow shows the TXT record value with copy-to-clipboard, then **Verify** button that polls.
- **Group → role mapping** — table (group ID, display name, role); add/edit/remove rows; save replaces the set.
- **Enforcement** — radio (`Optional`, `Preferred`, `Required`). Selecting `Required` opens a confirm dialog that blocks unless owner has logged in via SSO in the last 24h (server-side check, surfaced as 409).
- **Test connection** button beside each connection. Shows green/red badge + reason enum.

### 6.2 Login screen domain discovery

Update `services/dashboard/src/screens/LoginScreen.jsx`:

```
[Email input]
        ↓ onBlur or onSubmit
GET /v1/sso/discover?email=...
        ↓
  has_sso=true  → window.location = redirect_url   (no password prompt shown)
  has_sso=false → reveal native email/password form
```

### 6.3 SSO-only enforcement UX

When `sso_connections.enforcement='required'` for an org, the login screen shows the email input and SSO redirect; native password form is hidden. If a user reaches the dashboard with a native (non-SSO) session and the org has enforcement on, the auth middleware returns 403 + `{error:"sso_required"}` — frontend redirects to `/login?reason=sso_required` showing a banner.

### 6.4 Members screen

Update `services/dashboard/src/pages/settings/Team.jsx` to show a "Provisioned via" column (`Manual`, `Invitation`, `JIT (SSO)`) so admins can tell at a glance which members came in through SSO.

---

## 7. SAML specifics

### 7.1 Library choice

| Library | Pros | Cons |
|---|---|---|
| **`crewjam/saml`** (recommended) | Most maintained Go SAML SP. Used by Tailscale, BoxyHQ. Active commits. Real cert-rotation primitives. | API is mid-level; some quirks around URL building. |
| `russellhaering/gosaml2` | Stable. Simpler API. | Less feature-complete; SLO support patchy. |
| `crewjam/go-saml` (older) | — | **Deprecated** in favour of `crewjam/saml`. Don't use. |

**Pick `crewjam/saml`** if Option B (native) ships. Add to `services/api/go.mod`.

### 7.2 SP cert / signing key management

- AxiaOps generates one SP signing keypair per **deployment environment** (staging, production), not per org. Stored in env var `SSO_SP_PRIVATE_KEY_PEM` + `SSO_SP_CERT_PEM`. Generated at deploy time; rotated annually.
- SP metadata is published per-connection at `/v1/sso/saml/{cid}/metadata` and includes the SP cert. IdP admin imports it.
- IdP cert lives in `sso_connections.saml_signing_cert`. Rotation: admin pastes new cert; old one moves to `saml_previous_cert` with `saml_previous_cert_expires_at = NOW() + 30 days`. During the overlap, signature verification accepts either.

### 7.3 Replay protection

Every assertion has an `ID` attribute. Check:

1. Look up `sso_assertion_replay.assertion_id` — if exists and not expired, **reject** with `assertion_replayed`.
2. Insert with `expires_at = assertion.NotOnOrAfter + clock_skew`.
3. Cron sweep deletes expired rows nightly (`DELETE FROM sso_assertion_replay WHERE expires_at < NOW()`).

**Backend choice**: PostgreSQL is fine at MVP scale (1000s of assertions/day, single index lookup). At 100x that scale, move to Redis (`cache.Cache`) — interface stays identical. Don't premature-optimise.

**Sweep integration**: the nightly purge of expired replay rows runs on the existing API-service background ticker pattern (the same shape as the stuck-scan recovery ticker registered in `services/api/cmd/main.go`). Add a `ssoSweep` ticker registered alongside it: 24h interval, reads all `expires_at < NOW()`, deletes in batches of 1000. The 90-day domain-verification expiry sweep (§1.3) and the 30-day SAML `previous_cert` sweep (§7.2 / §1.8) live on the same ticker — one cron, three queries.

### 7.4 Clock skew, NotBefore/NotOnOrAfter

Standard `crewjam/saml` configuration: 60-second clock skew tolerance. Reject assertions where `NotBefore > NOW + 60s` or `NotOnOrAfter < NOW - 60s`.

### 7.5 IdP-initiated vs SP-initiated

**SP-initiated only in v1.** Reasoning:
- IdP-initiated has a permanent open-redirect-via-RelayState attack surface — every IdP-initiated SAML deployment has had a CVE in this area at some point.
- SP-initiated is what every IdP supports cleanly.
- Customers asking for IdP-initiated tend to be "dashboard tile in Okta" UX — solvable by Okta bookmark to our `/login` URL with no IdP-initiated dependency.

Document as a non-goal (§2.2 update); revisit if 3+ customers ask.

### 7.6 SLO (Single Logout)

**Defer.** Reasoning: SLO in SAML is famously broken (race conditions across SP/IdP, bookkeeping for which logout went through). SP-initiated logout (clear local session, redirect to `/login`) is what 90% of customers expect. Document the gap; revisit if a customer asks specifically for `LogoutRequest` support.

### 7.7 Native SAML callback flow (Option B)

```
1. User on /login enters alice@acme.com.
2. GET /v1/sso/discover → redirect to /v1/sso/saml/{cid}/initiate?email=...
3. Initiate handler: build SAMLAuthRequest with crewjam/saml, RelayState=<csrf_token>, redirect to IdP.
4. IdP authenticates user.
5. IdP POSTs SAMLResponse to /v1/sso/saml/{cid}/acs.
6. ACS handler:
     a. Verify signature with sso_connections.saml_signing_cert (or previous_cert).
     b. Validate NotBefore/NotOnOrAfter, audience, recipient.
     c. Check assertion_replay; insert.
     d. Extract email, NameID, group attribute.
     e. Look up sso_domains by email domain.
     f. JIT-provision or load existing user.
     g. Mint a native AxiaOps session (JWT + `sessions` row) and redirect to `/callback`.
7. Callback handler issues session cookie; dashboard renders.
```

Step 6g is the bridge between SSO and the rest of the product: SSO authenticates the user, then we mint a native session that the rest of the app (handlers, RBAC, audit) consumes uniformly — same shape as a session minted by native email/password login. This is what makes auth code single-pathed (§3.4 reasoning #2).

**Dependency on native auth design**: the `sessions` row, the JWT shape, and the cookie format are owned by **Phase B1 native auth** (§14) — see the forthcoming `docs/native-auth-design.md` (must exist before B1 starts). This SSO doc consumes them as primitives. If `native-auth-design.md` lands later than expected, B2 blocks on it — record that dependency when the tracking item is created.

---

## 8. OIDC specifics

### 8.1 JWKS package

Build a shared `axiaops.io/shared/jwks` package at the start of Phase B1. API: `keyfuncFromCache(ctx, issuer, jwksURL, cache)` returning a `jwt.Keyfunc`. Cache key: `sso:jwks:{connection_id}`. TTL: 1h. Resilience: a transient cache-layer error falls back to a live fetch (the security primitive — signature verification with the IdP's published key — is unchanged; only the latency path differs). A live-fetch failure surfaces to the caller as an auth error; do **not** silently accept tokens.

> **Note 2026-04-29**: this section originally described extracting `keyfuncFromCache` from the Kinde path in `auth.go`. Under ADR-0001 + Option B, the Kinde path is being removed entirely; the JWKS package is built fresh as part of Phase B1 (native auth) and consumed by the OIDC RP (Phase B2) from day one.

### 8.2 Discovery doc handling

On `POST /v1/organizations/{id}/sso/connections` with `protocol=oidc`:

1. Fetch `oidc_discovery_url` (or derive from `oidc_tenant_id` for Entra).
2. Validate: `issuer` matches discovery URL host, `jwks_uri` reachable, `response_types_supported` contains `code`, `id_token_signing_alg_values_supported` contains `RS256`.
3. Cache discovery doc at `sso:oidc-discovery:{cid}` for 24h. Refresh on metadata-refresh button or on first failure of next login.

### 8.3 PKCE — confidential vs public

AxiaOps API is a **confidential client** (it has a server-side secret). PKCE is still recommended (defence in depth). Use `code` flow with PKCE on the dashboard side; client_secret on the token exchange — both belt and braces.

### 8.4 Entra quirks

- **v1 vs v2 endpoints**: always use v2 (`/v2.0/.well-known/openid-configuration`). v1 has different claim shapes (`oid` vs `sub`) and is on a deprecation path.
- **Tenant ID handling**: Entra's `iss` claim is `https://login.microsoftonline.com/{tenant_id}/v2.0`. Validate the tenant_id matches `sso_connections.oidc_tenant_id` — otherwise a different Entra tenant could mint tokens that pass signature checks (Microsoft signs across tenants with the same key, sometimes).
- **Group claims overflow**: when a user is in >200 groups, Entra omits the `groups` claim and instead returns a `_claim_names` / `_claim_sources` block pointing at Microsoft Graph. v1 documents this as a known limitation: users in >200 groups fall through to `default_role`. Phase D adds Graph fallback (`GET https://graph.microsoft.com/v1.0/me/getMemberObjects`) using a delegated token from the same login.
- **`oid` is the stable subject**, not `sub`. Persist `oid` to `users.sso_external_id` for Entra connections (not `sub` — `sub` is per-app and changes if the customer changes their Entra app registration).

### 8.5 OIDC subject persistence

`users.sso_external_id` stores:
- For Entra: `oid` claim (Azure AD object ID, GUID).
- For generic OIDC: `sub` claim.
- For SAML: `NameID` (preserve format — `urn:oasis:names:tc:SAML:1.1:nameid-format:emailAddress` typical).

Re-login lookup: `WHERE sso_connection_id = $1 AND sso_external_id = $2`. Email is **not** the lookup key — emails change.

---

## 9. Entra ID integration notes

### 9.1 App registration steps (for the customer admin)

The admin SSO screen surfaces a step-by-step:

1. Azure Portal → Entra ID → App registrations → New registration.
2. Name: `AxiaOps Production` (any).
3. Supported account types: **Single tenant** (recommended) or Multi-tenant (advanced; v1 says Single).
4. Redirect URI: `Web` → `https://{customer-axiaops-host}/v1/sso/oidc/{cid}/callback`. Under self-hosted-first (ADR-0001), the host is the customer's own deployment (e.g. `axiaops.acme.internal`) — *not* a centralized AxiaOps Inc domain. The admin SSO screen surfaces the correct callback URL for the running instance.
5. After creation: Certificates & secrets → New client secret → copy the value (one-time visibility).
6. API permissions: add `User.Read` (delegated, default). For >200-groups Graph fallback, add `GroupMember.Read.All` (Phase D).
7. Token configuration → Add groups claim → Security groups, **emit as Group ID** (GUID — not name).
8. Paste tenant ID, client ID, client secret into AxiaOps SSO config.

### 9.2 Required Microsoft Graph permissions (v1 / Phase D)

- v1: `User.Read` (delegated) — read /me. Already granted by default.
- Phase D (groups overflow): `GroupMember.Read.All` (delegated, admin consent required).

### 9.3 Multi-tenant vs single-tenant

**v1 supports single-tenant only.** Multi-tenant Entra apps are more complex (issuer is `/common/`, tenant ID validation differs) and the customer-config story is more confusing. Single-tenant matches our deployment model (each customer is in their own Entra tenant).

### 9.4 Microsoft-specific gotchas

- **Email might be missing**: Entra B2B guest users sometimes have `email` empty in the token; fall back to `preferred_username` or `upn`. Document the fallback chain.
- **Group claim emit-as**: if admin selects "sAMAccountName" instead of "Group ID", group mappings work with names but become brittle (renames break mappings). UX should warn.
- **Conditional Access**: customer's CA policies (e.g. require MFA) apply to the SSO flow transparently — AxiaOps does nothing.

---

## 10. JIT provisioning + role mapping

### 10.1 First-login flow

In auth middleware, after native session validation (the Phase B1 replacement for Kinde JWT verification at `auth.go:142`), before `UpsertUser`:

> The fields `session.auth_mode` and `session.email` referenced below are part of the **native session model** owned by Phase B1 / `docs/native-auth-design.md` (forthcoming). The session row is set with `auth_mode='sso'` in step 6 of §7.7 (SAML) and the equivalent OIDC callback. Email/password sessions land with `auth_mode='password'` and skip this block.

```
if session.auth_mode == 'sso':
    domain = parse_email(session.email).domain
    sso_domain_row = lookup sso_domains WHERE lower(domain)=$1 AND status='verified'

    if sso_domain_row not found:
        if sso_connections.enforcement='required':
            403 {"error":"sso_domain_unverified"}
        else:
            fall through to non-SSO path (native session, normal upsert)

    org = sso_domain_row.organization_id
    user = UpsertUser(org, sso_external_id, email, name)

    # Pending invitation takes precedence over JIT
    if RedeemPendingInvitation(org, user.id, email) returned true:
        # User now has a membership from the invite; skip JIT.
        emit audit sso_login_succeeded {redeemed_invitation:true}
        continue

    # JIT path
    role = JITResolveRole(connection_id, jwt.groups, default_role)
    JITProvisionMembership(org, user.id, role)  # idempotent
    emit audit sso_jit_provisioned {connection_id, role, group_match}
```

### 10.2 Role precedence

```go
func JITResolveRole(connID string, groups []string, defaultRole string) string {
    matches := storeListGroupMappings(connID)
    var best string
    for _, g := range groups {
        if r, ok := matches[g]; ok {
            best = highestRole(best, r) // admin > member > viewer
        }
    }
    if best == "" {
        return defaultRole
    }
    return best
}
```

Deterministic. Owner is **never** assignable.

### 10.3 Re-login: stale group handling

On every SSO login (not just first), `JITResolveRole` runs and the resulting role is **applied to the existing membership** if it differs:

- Removed from `AxiaOps-Admins` group → next login downgrades from `admin` to whatever remains (or `default_role`).
- Added to `AxiaOps-Admins` → next login upgrades to `admin`.
- **Never downgrades the org's last `owner`** — owner role is sticky and ignored by JIT logic. Owner promotion is explicit only.
- Audit row written for every role change via JIT (`sso_jit_role_updated` — new constant).

### 10.4 Interplay with `pending_memberships`

The existing invitation flow (`docs/invitation-flow.md`) and JIT coexist:

| Scenario | Behaviour |
|---|---|
| User has pending invite + email matches SSO domain | Invitation **wins**. `RedeemPendingInvitation` runs first; JIT skipped. |
| User has pending invite + email does NOT match SSO domain | Invitation works. SSO login does not flow through this org. |
| User has no invite + email matches verified SSO domain | JIT provisions membership at `default_role` (or group-mapped role). |
| User has no invite + email does NOT match verified SSO domain | If `enforcement='required'` → 403; otherwise standard "no membership → 403 from `Require`". |

---

## 11. Security considerations

### 11.1 Email-verification spoofing

**Attack**: a malicious IdP sends `email_verified: true` for `victim@othercompany.com`, hoping AxiaOps trusts it.

**Mitigation**:
- We **do not** trust `email_verified` from arbitrary IdPs. We trust the **domain** because the customer admin proved DNS control.
- The connection is bound to a verified domain set; emails outside those domains route to the non-SSO path (or 403 under `required` enforcement).
- Never JIT-provision a user whose email domain isn't on `sso_domains` for the same org as the connection.

### 11.2 XML signature wrapping (SAML, Option B only)

**Attack**: attacker injects a malicious assertion before/after a valid one, leveraging XML parser quirks.

**Mitigation**:
- `crewjam/saml` does the right thing by default — verifies the **specific element** signed, not "any signature in the document."
- Disable XML external entity (XXE) parsing on our XML parser.
- Reject SAMLResponse documents with multiple `<saml:Assertion>` elements.
- Pen-test the ACS endpoint with `xml-attacker` corpus before going live.

### 11.3 JWT algorithm confusion (OIDC)

**Attack**: switch `alg` from `RS256` to `HS256` and use the public key as HMAC secret.

**Mitigation**:
- `jwt.ParseWithClaims` must be called with a `Keyfunc` that explicitly inspects `token.Method` and rejects anything that isn't `*jwt.SigningMethodRSA`. **Phase B1 (native auth replacement)** is the implementation point — write the assertion into the new `services/shared/jwks/` package's `Keyfunc` from day one. The same package backs both native session validation and SSO OIDC verification (§8.1), so the protection applies uniformly.
- Reject `alg=none` and any HMAC algorithm at the `Keyfunc` level, before signature verification runs.
- Unit-test in `services/api/internal/sso/oidc_test.go` (§13.1) — synthetic `id_token` with `alg=HS256` using the public key as secret must be rejected.

### 11.4 Open redirect on `RelayState` (SAML) / `state` (OIDC)

**Attack**: `RelayState=https://evil.com` — after successful login, redirect victim to attacker's site.

**Mitigation**:
- `RelayState` / `state` is a **CSRF-bound opaque token**, not a URL. We mint and validate it; never honour user input as a redirect target.
- Always redirect to a fixed `/dashboard` after successful SSO login.

### 11.5 Audit logging discipline

Every SSO event writes `audit_log` (§4.5). Failure-mode events include enough context to diagnose without leaking secrets:

| Field | Logged? |
|---|---|
| connection_id | Yes |
| protocol | Yes |
| outcome (success/failure) | Yes |
| failure reason (enum) | Yes |
| IdP issuer | Yes |
| email | Yes |
| user_id (if existing) | Yes |
| **assertion_id** | Hash only (SHA-256) |
| **assertion body** | **No** |
| **id_token** | **No** |
| **client_secret** | **No** |
| **SAML signing cert** | **No** (it's PEM-public, but no need to log) |

### 11.6 Authn vs authz separation

SSO authenticates ("who are you"). RBAC (`memberships.role`) authorises ("what can you do"). A successful SSO login **never** grants `owner`. The only paths to `owner` are `EnsureFirstMembership` (first user in a brand-new org) and `TransferOwnership` (explicit owner-to-owner handoff). Documented as a security property; tested in `services/api/internal/sso/permission_matrix_test.go` (forward-looking — written in Phase B2 alongside the JIT handler).

### 11.7 Rate limiting

- `/v1/sso/discover` — 30 req/min/IP (existing rate-limit middleware).
- `/v1/sso/saml/{cid}/acs` and `/v1/sso/oidc/{cid}/callback` — 60 req/min/IP. Real users hit these once per session; abuse pattern is replay attempts.
- `POST /v1/organizations/{id}/sso/domains/{did}/verify` — 10 req/min/org (DNS lookups can be slow; throttle).

### 11.8 SOC 2 / GDPR alignment

- **CC6.1 (logical access)**: SSO config lives in audit_log; retention matches existing audit retention.
- **CC6.6 (boundary defence)**: enforcement=required closes the social-login bypass.
- **GDPR Art. 32**: encryption at rest of IdP secrets satisfies "appropriate technical measures."

### 11.9 ~~SOC 2 sub-processor disclosure~~ — Moot

> **Updated 2026-04-29.** Originally this section flagged Kinde as a sub-processor for the SSO authentication path that would need to appear on the public sub-processors list. With ADR-0001 + §3.4 flip to Option B (native), **Kinde is removed from the product entirely** — no SSO sub-processor disclosure is required.

Customers' own IdPs (Entra, Okta, Google Workspace, customer-hosted Keycloak/ADFS, etc.) are **not** AxiaOps sub-processors. They are the customer's own identity providers; AxiaOps federates with them on the customer's behalf. The customer is the data controller for their identity data; AxiaOps does not process any identity data outside the customer's own infrastructure (under self-hosted-first per ADR-0001).

If a future managed-hosted SKU re-introduces Kinde for hosted customers only, the sub-processor disclosure becomes scoped to that SKU's customers. Track as a deliverable then, not now.

---

## 12. Migration & rollout

> **Rewritten 2026-04-29** for native auth under ADR-0001. Originally this section covered "existing Kinde-authed orgs" and "Kinde social + SSO coexistence." Under self-hosted-first, there are no existing Kinde customers — every deployment ships with native auth (email/password) baseline and optionally an SSO connection. Sections renumbered.

### 12.1 New deployments

Every self-hosted v1 customer starts on **native auth** (email/password baseline) per Phase 2.7.1. SSO is added per organization via the admin UX once the IdP side is configured.

Bootstrapping order at install time:
1. Operator installs the AxiaOps Helm chart / docker-compose bundle.
2. First admin signs up via native email/password — `EnsureFirstMembership` assigns the `owner` role.
3. Owner optionally configures SSO via Settings → Single Sign-On (§6.1).
4. Owner verifies an email domain (§1.3) and turns on JIT.
5. Owner sets enforcement mode (§12.3) when ready.

### 12.2 Coexistence — native password + SSO

Under enforcement `optional` or `preferred`, both login methods work. Risk: a user signs up via password first and later logs in via SSO with the same email — without dedupe, this produces **two `users` rows** for the same human.

**Mitigation**: dedupe by `users.email` within an organization on first SSO login.
- Native password signup writes `users` keyed on `(organization_id, email)`.
- Native SSO login writes `users` keyed on `(sso_connection_id, sso_external_id)` — *different key*.
- On first SSO login, before inserting a new `users` row, check `SELECT id FROM users WHERE organization_id=$1 AND lower(email)=lower($2)`. If found, **merge**: update the existing row to set `sso_connection_id` + `sso_external_id`, do not create a duplicate.
- Implementation: `MergeOrUpsertSSOUser` in `services/shared/storage/postgres/postgres.go`, single-transaction.
- Tested in §13: integration test with password-signup-then-SSO flow, asserts one `users` row + one `memberships` row at the end.

### 12.3 Enforcement modes

| Mode | Login behaviour | Use case |
|---|---|---|
| `optional` (default) | Both native password and SSO accepted. SSO is one option among many on the login screen. | Initial rollout; teams with non-employee admins (contractors). |
| `preferred` | Login screen routes to SSO by default; "use password instead" link still visible. Native password fallback works. | Phased migration toward SSO-only. |
| `required` | Only SSO works for the org's verified domains. Native password rejected at middleware (403 `sso_required`). | Hard cutover; SOC 2 / compliance posture. |

**Lockout prevention** (§1.4): the owner must have a recent successful SSO login (within 24h) before flipping to `required`. Server-side check via `audit_log` lookup. Frontend cannot bypass.

**Session invalidation on enforcement change**: when an org flips from `optional`/`preferred` to `required`, native-password sessions issued before the flip should be invalidated immediately. Native auth controls session TTL directly (unlike Kinde) — implementation: `sessions.revoked_after` column (defined by the native session model in **Phase B1** / `docs/native-auth-design.md`) is updated to `NOW()` for the affected org's non-SSO sessions on enforcement change. The SSO doc consumes this column; the column itself is owned by the native auth design. *(See §15 Q7 — confirm whether to do this automatically or only on explicit owner action.)*

### 12.4 Backfill

No backfill needed for fresh self-hosted installs — every install starts with the migration-021 schema in place.

For the **internal AxiaOps Cloud dogfood instance** (if we run one) currently on Kinde:
- One-time data migration: `users.kinde_sub` rows mapped to `users.password_hash=NULL` (signal that password setup is pending) + welcome email sent inviting them to set a password.
- Or: convert the dogfood org to SSO immediately via Entra/Google Workspace, skip native password entirely.
- Either way the migration is internal-only — no customer-facing migration tooling is needed for v1 launch.

### 12.5 Default configuration

A fresh install ships with:
- Native auth enabled.
- No SSO connection.
- Enforcement = `optional` (it's the only sensible default — no SSO connection exists yet).
- Email-invitation flow enabled (§Phase 3 #14 native rescope).

---

## 13. Testing strategy

### 13.1 Unit tests

- **OIDC token validation** (`services/api/internal/sso/oidc_test.go`): synthetic `id_token` signed with a test RSA key; assert acceptance of valid + rejection of expired, wrong issuer, wrong audience, `alg=none`, `alg=HS256` with public key as secret.
- **SAML assertion validation** (`services/api/internal/sso/saml_test.go`): pre-signed assertions captured from `samltest.id` corpus; assert acceptance + rejection of expired, replay (second submission), signature wrapping, multi-assertion.
- **JIT role resolver** (`sso/role_test.go`): table-driven — empty groups, multi-group with conflicting roles, group not in mapping, default fallback.
- **Domain verifier** (`sso/domain_test.go`): mock DNS resolver; assert correct TXT name (`_axiaops.{domain}`), correct token comparison, public-suffix-list rejection.

### 13.2 Integration tests

Run under `make test-integration`:

- **Mock IdP container** — add a `mockoidc` or `mock-oauth2-server` service to `docker-compose.test.yml`. Point an SSO connection at it; run a full login flow; assert membership row created.
- **Mock SAML IdP** — `keycloak` in test-only mode, or `samltest.id` for human verification.
- **Postgres integration** (`services/shared/storage/postgres/sso_test.go`): RLS isolation (org A's connection invisible from org B), partial unique index on verified domains, replay cache TTL.

### 13.3 End-to-end (manual + scripted)

Add to `make test-integration-api`:
1. Create draft connection.
2. Verify domain (mock DNS).
3. Set group mapping.
4. Synthetic login (mock IdP) → assert JIT membership.
5. Re-login with different group → assert role change.
6. Set enforcement=required → assert non-SSO login blocked.
7. Disable connection → assert subsequent logins fail.

### 13.4 Pen-test surface

Before any external pilot:
- `xml-attacker` against the SAML ACS endpoint.
- `mitmproxy`-based replay of OIDC callbacks.
- `dnstwist`-style domain-confusion testing on `sso_discover`.
- Open-redirect fuzzing on `RelayState` and `state`.

This is the right time to engage an external pen-test (Phase 3 #9p backlog already includes one).

### 13.5 Test data

- Synthetic IdP responses live in `services/api/internal/sso/testdata/` — checked-in fixtures.
- Real-IdP captures (Entra, Okta) require redaction; do **not** check these in. Document the local-test setup in `docs/sso-local-dev.md` as a follow-up.

---

## 14. Phased delivery plan

> **Rewritten 2026-04-29** to reflect the Option-B native path under ADR-0001. Phase B effort grows (no Kinde to do the heavy lifting); Phase F (native cutover) is removed entirely (there is no Kinde to cut over from).
>
> **Updated 2026-04-30**: Phase B split into **B1 (native auth replacement)** and **B2 (SSO OIDC + first IdP)**. The original combined Phase B was load-bearing for non-SSO auth without saying so; the split surfaces the dependency and lets B1 ship independently if SSO is descoped from v1. B1+B2 combined effort is unchanged from the original Phase B (8–10w).

| Phase | Deliverables | Effort | Blocking deps | Success criteria |
|---|---|---|---|---|
| **A — Design alignment + ADR ratification** | ✅ This doc reviewed; ADR-0001 accepted (2026-04-29); §3 recommendation flipped from A to B; §11.9 sub-processor disclosure marked moot. ✅ Phase B split into B1/B2 (2026-04-30). | S (≤1w) | — | Done. |
| **B1 — Native auth replacement for Kinde** | (1) Native session model (`sessions` table + migration), email/password baseline, native JWT issuance, password hashing (argon2id). (2) Replace Kinde JWT validation in `auth.go` with native session validation. (3) Native login + signup screens (`LoginScreen.jsx`, `RegisterScreen.jsx`). (4) Delete `services/api/internal/kinde/` package and Kinde env vars. (5) Migration adds `sessions` + `users.password_hash`. (6) Shared `services/shared/jwks/` package created (consumed by B2 OIDC RP). (7) `docs/native-auth-design.md` written **before** B1 starts — owns session shape, cookie format, password policy, and the `auth_mode` field consumed in §10.1. | L (4–6w) | A + `docs/native-auth-design.md`. | Self-hosted instance authenticates via email/password with **zero Kinde calls**; `services/api/internal/kinde/` directory deleted; existing handler tests pass against the new auth path. |
| **B2 — Native OIDC RP + first IdP (Entra)** | (1) Migration 021 (`sso_connections` + `sso_domains` + `sso_group_mappings` + `sso_assertion_replay`). (2) Native OIDC RP at `services/api/internal/sso/oidc.go` — discovery doc handling, JWKS fetch via `services/shared/jwks/`, PKCE + code flow, ID-token validation. (3) Initiate handler `/v1/sso/oidc/{cid}/initiate` and callback handler `/v1/sso/oidc/{cid}/callback`. (4) Admin SSO UX (`services/dashboard/src/pages/settings/SSO.jsx`). (5) Domain discovery `/v1/sso/discover` (with the timing-oracle mitigation in §5.4). (6) JIT provisioning + permission matrix test (§11.6). (7) Audit-log wiring. (8) `ssoSweep` ticker for replay/expiry/cert-rotation cleanup (§7.3). | L (4–6w) | B1. | Internal AxiaOps team logs into self-hosted instance via Entra OIDC with JIT provisioning. |
| **C — Native SAML support** | `crewjam/saml` integration; SP signing keypair + lifecycle (env-var-managed per deployment); SP metadata endpoint `/v1/sso/saml/{cid}/metadata`; ACS handler `/v1/sso/saml/{cid}/acs`; SAML initiate handler `/v1/sso/saml/{cid}/initiate`; assertion replay cache (PG-backed initially, Redis later); cert rotation overlap; clock-skew handling; pen-test against `xml-attacker` corpus. | L (4–6w) | B2. | (1) Test against `samltest.id` corpus + one real customer's Okta-SAML in staging. (2) **`xml-attacker` and open-redirect fuzzing pass clean** — explicit go/no-go gate before any external pilot (§13.4). |
| **D — Generic OIDC + Entra group overflow** | "Generic OIDC" admin form (single discovery URL + client ID/secret); Microsoft Graph fallback for >200 groups; cert/key rotation runbook (`docs/sso-key-rotation.md`); validation against Keycloak + Authentik in test stack. | M (2–3w) | B2. | Mock-OIDC + real Keycloak + real Entra all pass integration tests. |
| **E — SCIM 2.0** | SCIM endpoints (`/scim/v2/Users`, `/scim/v2/Groups`); SCIM token issuance + rotation; deprovisioning logic; mapping back to AxiaOps roles. | XL (8–10w) | D + paying customer asking for it. | Entra SCIM provisioning round-trips correctly (create, update, deactivate). |
| ~~**F — Native cutover**~~ | ~~Migrate from Kinde-brokered to native.~~ **Removed 2026-04-29** — Option B is the v1 path; there is no Kinde to cut over from. | — | — | — |

Effort labels: S=≤1w, M=2–3w, L=4–6w, XL=≥8w. Single-developer assumption.

**Total v1 effort (A+B1+B2+C+D)**: ~16–20 weeks. Phase E (SCIM) is post-v1, conditional on customer demand.

**Critical path note**: B1 is load-bearing for **non-SSO authentication** (replacing Kinde with email/password is in B1, not bundled with SSO). If SSO is descoped from v1 for time-to-market reasons, B1 still ships and the product runs on email/password — B2 becomes a fast-follow. This is the ADR-0001 commitment expressed in the phase plan.

### 14.1 Out of scope until further notice

- IdP-initiated SLO.
- WS-Federation.
- B2C / consumer IdPs.
- Per-attribute mapping UI (SCIM solves this differently).

---

## 15. Open questions

These block downstream decisions; user input needed before Phase B starts.

**Resolved by ADR-0001 (2026-04-29):**

1. ~~**Kinde-brokered or native?**~~ → **Native (Option B)** per ADR-0001 + §3.4.
2. ~~**Kinde Pro/Enterprise pricing.**~~ → Moot; Kinde removed from product.
11. ~~**Self-hosted / on-prem SKU within 12–24 months?**~~ → **Self-hosted-first as v1** per ADR-0001.

**Must resolve before Phase B2 starts (gating):**

3. **Admin self-serve or gated onboarding?** Should owners configure SSO themselves, or is it a "contact AxiaOps support" gated process initially? Self-serve is cheaper to operate but exposes more failure modes; gated lets us learn from the first 10 deployments before automating. **Why gating**: §6.1 (admin SSO settings screen) is a Phase B2 deliverable, and its surface differs materially between self-serve (full wizard, domain TXT verification UI, group mapping editor) and gated (single read-only "SSO is configured" panel + a support-contact CTA). Pick before B2 scope is locked, otherwise the admin UX gets rebuilt mid-flight.

**Still open — input needed before Phase B2 starts:**

4. ~~**Pricing — paid-tier feature?**~~ — Stripe (Phase 3 #1) deferred per ADR-0001. SSO pricing is per design-partner contract, not tier-gated, until SaaS is reintroduced. Reopen if/when a managed-hosted SKU lands.
5. **Which IdP gets first design partner?** Entra is most common in our ICP, but if the first paying customer is Okta-on-SAML, Phase C jumps ahead of Phase D. Need a customer signal.
6. **Keep `pending_memberships` invitation flow alongside SSO+JIT, or require pre-invite for SSO orgs?** §10.4 keeps both. Alternative: once an org has `enforcement=required`, disable the invitation flow.
7. **Owner role and SSO**: should the owner ever be required to log in via SSO when `enforcement=required`? §1.4 has lockout-prevention logic but the original spec said "active sessions valid until expiry" — under native auth we control session TTL directly, so we can also forcibly invalidate non-SSO sessions on enforcement change. Confirm desired behaviour.
8. **SCIM timing.** §14 puts SCIM at Phase E. Some enterprise procurement asks for SCIM before they'll buy. Track as a flag in customer-development conversations.
9. **Single-tenant Entra app vs multi-tenant.** §9.3 says single-tenant for v1. Multi-tenant would let us ship one Entra app for all customers (simpler customer config) but complicates issuer validation. Confirm v1 single-tenant.
10. **Audit retention for SSO events.** Existing audit retention applies — but enterprise customers may demand longer retention for SSO events specifically (SOC 2 evidence). Confirm retention matches or exceeds the SOC 2 plan (Phase 3 #17 — narrowed scope under self-hosted per ADR-0001).

---

## 16. Appendix — files that will change

### 16.1 New files (Phase B1/B2 baseline, Option B native)

> **Updated 2026-04-29.** File list reshaped for the native runtime: removed Kinde Mgmt-API wrapper, added native OIDC RP + SAML SP + native session/auth modules.
> **Updated 2026-04-30.** File list re-tagged by sub-phase (B1 = native auth; B2 = SSO OIDC; C = SAML).

```
# Phase B1 — native auth (owns sessions, password, JWKS shared package)
services/shared/jwks/jwks.go                    # JWKS fetch + cache (consumed by B2)
services/api/internal/auth/native.go            # email/password baseline + native JWT issuance
services/api/internal/auth/session.go           # native session model + TTL handling
services/api/internal/auth/password.go          # argon2id password hashing
services/shared/storage/postgres/migrations/0NN_native_sessions.up.sql   # owned by docs/native-auth-design.md
services/shared/storage/postgres/migrations/0NN_native_sessions.down.sql # ↑ migration number assigned when that doc lands

# Phase B2 — SSO core schema + native OIDC RP
services/shared/storage/postgres/migrations/021_sso_core.up.sql
services/shared/storage/postgres/migrations/021_sso_core.down.sql
services/shared/model/sso.go                    # SSOConnection, SSODomain, SSOGroupMapping types
services/api/internal/sso/handler.go            # connections, domains, group-mappings CRUD
services/api/internal/sso/discover.go           # GET /v1/sso/discover
services/api/internal/sso/jit.go                # JITResolveRole, JITProvisionMembership
services/api/internal/sso/test.go               # POST /sso/connections/{cid}/test
services/api/internal/sso/oidc.go               # native OIDC RP (Entra/generic)
services/api/internal/sso/oidc_callback.go      # /v1/sso/oidc/{cid}/callback
services/api/internal/sso/initiate.go           # SP-initiated flow start (OIDC + SAML)
services/api/internal/sso/sweep.go              # ssoSweep ticker — replay/expiry/cert (§7.3)
services/api/internal/sso/permission_matrix_test.go # owner-never-via-SSO test (§11.6)

# Phase C — native SAML SP
services/api/internal/sso/saml.go               # crewjam/saml integration
services/api/internal/sso/saml_acs.go           # /v1/sso/saml/{cid}/acs
services/api/internal/sso/saml_metadata.go      # /v1/sso/saml/{cid}/metadata
services/api/internal/sso/replay.go             # assertion replay cache

# Frontend
services/dashboard/src/pages/settings/SSO.jsx
services/dashboard/src/pages/settings/sso/Connections.jsx
services/dashboard/src/pages/settings/sso/Domains.jsx
services/dashboard/src/pages/settings/sso/GroupMappings.jsx
services/dashboard/src/screens/LoginScreen.jsx        # rewritten — native login + SSO discovery
services/dashboard/src/screens/RegisterScreen.jsx     # new — native signup (Kinde was hosted)

docs/sso-local-dev.md                           # follow-up
docs/sso-key-rotation.md                        # Phase D runbook
```

### 16.2 Files modified

```
services/shared/storage/storage.go              # Store interface methods (§4 SSO CRUD + native users)
services/shared/storage/postgres/postgres.go    # implementations
services/shared/model/audit.go                  # 12 new AuditAction* constants (§4.5)
services/shared/model/user.go                   # add password_hash column reference
services/shared/authz/roles.go                  # 3 new permission constants (§5.1)
services/api/cmd/main.go                        # wire native auth handler + ssoHandler
services/api/internal/middleware/auth.go        # REWRITTEN — native session validation, no Kinde
services/api/internal/api/handler.go            # /v1/me returns has_sso + auth_provider
services/dashboard/src/pages/settings/Team.jsx  # provisioned_via column
services/api/CLAUDE.md                          # endpoint table + auth section rewrite
services/shared/CLAUDE.md                       # tables list
docs/decisions/0001-deployment-model.md         # ADR (already accepted)
```

### 16.3 Files to be deleted (in Phase B1)

These exist in the current codebase as of 2026-04-30 and will be removed when Phase B1 lands; not yet deleted.

```
# Kinde integration — to be removed under ADR-0001 (Phase B1)
services/api/internal/kinde/                    # whole package — currently has client.go, client_test.go, invitations.go, stub.go
services/api/internal/middleware/kinde_*.go     # any Kinde-specific middleware (none today; placeholder for cleanup)
```

### 16.4 Effort total (Option B native, Phases A+B1+B2+C+D)

~16–20 weeks single-developer for v1. Breakdown: A=done; B1=4–6w (native auth replacement); B2=4–6w (SSO OIDC + first IdP); C=4–6w (SAML); D=2–3w (generic OIDC + group overflow). Phase E (SCIM) is post-v1.

The +6–7 weeks vs the original Option A estimate (~10–13 weeks) is the cost of:
- (a) rewriting auth middleware for native sessions (Kinde was doing this work for us),
- (b) writing the OIDC RP and SAML SP ourselves,
- (c) building the native login/signup UI (Kinde provided hosted login screens).

Offset by the elimination of Phase F (native cutover, originally XL = 10–14w) which is no longer needed — net effort delta is **negative** vs the original A→F arc.

