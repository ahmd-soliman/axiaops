# SaaS Platform Admin Plane & Entitlement — Design

Status: **draft for refinement — near-term / scheduled** pending acceptance of
[ADR-0002 (SaaS-first for awareness)](decisions/0002-saas-first-for-awareness.md),
which resequences AxiaOps to lead with a hosted SKU. This doc specifies the
staff/admin plane, internal-ops integrations, and the licensing→entitlement
inversion that the SaaS-first direction requires.

> **Author's note (2026-06-04).** Originally written as *preparation* under
> ADR-0001 (self-hosted-first). With ADR-0002 proposing a SaaS-first pivot, this
> work moves from speculative to near-term: the entitlement model (§7) gates the
> ability to charge money, and the admin/support plane (§4–6) is required the
> moment AxiaOps custodies multiple tenants. Four design decisions were resolved
> 2026-06-04 (§11.1 — staff identity, support-access transparency, entitlement
> bootstrap, `past_due` grace); the rest remain open (§11.2, still flagged
> **[DECISION]** inline). **Validate the PLG activation assumption (ADR-0002
> §Risks) before committing the full build.**

## Relationship to other docs

- [ADR-0001 — Deployment Model (self-hosted-first)](decisions/0001-deployment-model.md) — the decision this doc prepares to revisit.
- [`sso-integration-design-saas.md`](sso-integration-design-saas.md) — covers the **tenant** auth boundary under SaaS (Kinde-brokered end-customer login). This doc is its sibling: it covers the **AxiaOps-internal** identity/admin plane and the **entitlement** model, which that doc does not.
- [`license-issuance.md`](license-issuance.md) — the self-hosted license-JWT runbook. §5 here explains why most of it becomes dormant under SaaS.
- [`b1.6-amendment-feature-gating.md`](b1.6-amendment-feature-gating.md) — the scan-gate enforcement model this doc generalises from "license state" to "entitlement state".

---

## 1. Context — what exists today

The self-hosted build is **single-tenant in operation, multi-tenant in mechanism**:

- Postgres RLS isolates every data table by `app.organization_id`. The machinery for many orgs in one DB is already exercised (multi-membership users, B1.5).
- **There is no cross-org / staff / super-admin identity anywhere in the codebase.** Every authenticated principal is a member of exactly one org at a time, resolved through `memberships`. The only code that reads across orgs is a short, audited list of maintenance paths running on the `axiaops_runtime` RLS-bypass pool (native login lookup, scheduled-scan enumeration, stuck-scan recovery, GDPR purge — see `docs/runtime-admin-db-role.md`).
- Entitlement is a **per-deployment offline license JWT** (`shared/license`), verified against an embedded RS256 public key, gated at the scan endpoints via `IsScanAllowed()`. The customer can see their own license state at `GET /v1/version`.
- Outbound notifications (`shared/notifications`) are **entirely org-scoped**. There is no system/platform notification path.
- The reactivation seam architecture is **one composition-root factory (`serverbuild.ComposeServer`) plus the four interface-typed dependencies that cross its `Deps` boundary** and would diverge SaaS-vs-self-hosted: `storage.Store`, `auth.Provider`, `sso.Discoverer`, `sso.Connector` (`build.go`'s own comment counts exactly these four). The SaaS variant is a second composition root (`cmd/api-saashosted/main.go`) that fills `Deps` with SaaS-flavoured impls and calls the same `ComposeServer` — no handler/business-logic changes. *(Note: the original plan doc §D11 named a fifth seam `auth.Inviter`; it was never built as a separate interface — invitations stayed token-based via `pending_memberships` — so the shipped seam set is the four above + the factory.)* The license package additionally exposes `SetEnforcementBypass()` / `IsEnforcementBypassed()`, reserved for `cmd/api-saashosted`.

Under SaaS the trust boundary **inverts**: AxiaOps Inc runs the infrastructure and holds every tenant's data. That inversion is what creates the three problems this doc addresses:

1. **Staff need a way in** (support, ops, billing) that does not exist today and must be ruthlessly audited because it crosses tenants.
2. **Staff need to be told things** (signups, payment failures, a support engineer touching a tenant, platform health) — an internal notification path that today has nowhere to live.
3. **Entitlement stops being a file the customer installs** and becomes a billing-driven row AxiaOps controls — which changes what "license" even means.

---

## 2. Goals & non-goals

### 2.1 Goals

- Define an **AxiaOps-staff identity domain** that is structurally separate from tenant users.
- Define a **staff RBAC** (support / ops / billing / super-admin) orthogonal to tenant roles (owner/admin/member/viewer).
- Define **break-glass cross-tenant access** (read + impersonation) that is time-boxed, reason-stamped, fully audited, and (where possible) tenant-visible.
- Define an **internal-ops notification path** for staff (the email/Slack/other integrations requested), distinct from per-tenant channels.
- Settle the **licensing→entitlement model** for SaaS: what replaces the license JWT, what stays dormant, what the customer sees (nothing).

### 2.2 Non-goals

- End-customer SSO under SaaS — owned by [`sso-integration-design-saas.md`](sso-integration-design-saas.md).
- Billing-provider selection / Stripe integration mechanics — a separate doc; here we only define the *entitlement state* billing must drive.
- Migrating an existing self-hosted customer into the hosted SKU (data import) — out of scope; reactivation assumes net-new hosted tenants.
- Re-litigating self-hosted licensing. The license JWT stays exactly as-is for the self-hosted SKU. This doc only describes its SaaS-mode behaviour.

---

## 3. Three planes

The mental model the rest of the doc builds on:

| Plane | Principal | Identity source | Data scope | Surface |
|---|---|---|---|---|
| **Tenant** | end-customer user | tenant IdP (Kinde / native) | one org, RLS-enforced | `app.axiaops.io` (existing dashboard) |
| **Control** | billing system / automation | service credential | cross-org, system tables | webhooks, internal APIs |
| **Admin** | AxiaOps employee | **staff IdP (new)** | cross-org, **audited break-glass** | `admin.axiaops.io` (new) |

The hard rule: **a principal never spans planes.** A staff member is never "also a tenant user"; if an AxiaOps employee wants to be a customer, they sign up as an ordinary tenant. This keeps the audit story clean (every cross-tenant action is unambiguously a staff action) and keeps the blast radius of a compromised tenant credential inside one org.

---

## 4. Staff identity & RBAC

### 4.1 Where staff identities live  — **decided: (A)** (§11.1)

Three options:

- **(A) Separate `staff_users` table + staff-only IdP.** A new identity domain. Staff authenticate via AxiaOps's *own* corporate IdP (Google Workspace / Entra) — never the tenant auth path. Cleanest isolation; staff compromise ≠ tenant compromise and vice-versa.
- **(B) A reserved internal org in the tenant tables**, with staff as members holding a magic role. Reuses all existing machinery (sessions, memberships, RBAC) — cheapest to build. But it muddies RLS (the "staff org" is special-cased everywhere) and a tenant-auth bug now potentially reaches staff.
- **(C) IdP groups only** — no AxiaOps-side staff records; authorize purely on IdP group claims at request time. Least state, but no local audit of *who is staff* independent of the IdP, and offboarding races the IdP.

**Decided: (A).** The whole point of the platform admin plane is that it is a different trust domain. Reusing tenant tables (B) saves a few weeks now and costs a security review later. The `staff_users` table is small and the new `staff.Provider` is a third implementation of the *same* `auth.Provider` seam — the composition root already supports swapping it.

### 4.2 Staff roles

Orthogonal to tenant roles. Proposed tiers (refine in §12):

| Staff role | Capabilities |
|---|---|
| `support` | Read any tenant's data (scoped, audited). **Read entitlement summary** (§7.5). Request impersonation (requires approval or consent — §5). No mutations to billing/entitlement. |
| `ops` | Tenant lifecycle (provision, suspend/resume), trigger/replay scans, read platform health, **read entitlement summary**. No billing PII, no billing writes. |
| `billing` | Entitlement + plan writes, refunds/credits, billing PII read+write. **No tenant FinOps-data reads.** |
| `superadmin` | Manage `staff_users`, grant staff roles, configure internal integrations. The only role that can mint other staff. |

Least-privilege by default: a new staff member is `support` and nothing else. `billing` deliberately cannot read tenant zombie/cost data, and `support` deliberately cannot touch money — the two highest-abuse surfaces are split. The **entitlement *summary*** (plan/status/limits/dates) sits in a shared read tier across all four roles because support cannot help a customer without it; only *writing* entitlement and reading *billing PII* are restricted (§7.5).

### 4.3 Seam reuse

`staff.Provider` implements `auth.Provider`. The admin console is composed by a **fourth composition root**, `cmd/api-admin` (siblings: `cmd/api-selfhosted`, `cmd/api-saashosted`), calling a thin `ComposeAdminServer` — *not* the tenant `ComposeServer`. Separate binary = separate blast radius, separate deploy, separate network exposure (admin plane is not internet-facing the way the tenant API is; put it behind VPN/SSO-only ingress).

---

## 5. Cross-tenant access & impersonation (break-glass)

This is the highest-risk surface in the entire SaaS design. The principles:

1. **No standing cross-tenant data access.** A `support` staff member has the *capability* to read a tenant, but each access is an explicit, scoped, time-boxed *grant* — not an ambient permission. Default state: a staff member can read zero tenants.
2. **Reason required.** Every grant carries a free-text reason + an optional linked support ticket. No reason, no access.
3. **Time-boxed.** Grants auto-expire (default 1h, max configurable). Expiry revokes the cross-tenant read.
4. **Fully audited, in the tenant's own audit log.** A staff read/impersonation writes an `audit_log` row *in the accessed org* with `actor` = the staff principal and a `staff_access` action class — so it survives in the tenant's timeline and is visible to GDPR export. Plus a mirror row in a system `staff_access_log`.
5. **Tenant-visible by default** (decided, §11.1). We surface "an AxiaOps support engineer accessed your account on <date> for <reason>" to the tenant (transparency, à la GitLab/Stripe) — the trust-building default regulated buyers increasingly require. A **silent mode** exists for fraud/abuse investigations but is gated behind `superadmin` + a heightened audit class, so a silent access is itself an audited, privileged act.
6. **Impersonation ≠ read.** Read-only cross-tenant access is one capability; *acting as* a tenant user (impersonation, to reproduce a bug) is a strictly higher one — requires either tenant consent (a support-session token the customer generates) or `ops`+approval, and is always non-silent.

Mechanically this rides the existing `axiaops_runtime` RLS-bypass pool, but **gated** by an active grant rather than open to any code path. A `WithStaffGrant(ctx, grant)` helper sets `app.organization_id` to the target org *and* records the access — the RLS-bypass becomes grant-scoped, not blanket.

---

## 6. Internal-ops integrations (staff notifications)  ← the requested email/Slack/other

Staff need to be *told things*. Today's `shared/notifications` layer cannot do this: every channel is FK'd to an org and read only through RLS-scoped `ListEnabledNotificationChannels`. There is no system-scoped path.

### 6.1 What internal notifications fire on

- **Growth/lifecycle:** new tenant signup, trial started/ending, plan upgrade/downgrade, churn/cancellation.
- **Billing:** payment succeeded/failed, dunning, entitlement about to lapse.
- **Security/trust:** a staff member opened a break-glass grant (§5), impersonation used, repeated auth failures, GDPR deletion executed.
- **Platform health:** scan loop stuck cross-tenant, queue backlog, DB/Redis degraded, deploy completed.

### 6.2 Two ways to build it  **[DECISION]**

- **(A) Reuse the transport layer with a system scope.** Add `scope ∈ {organization, system}` to `notification_channels` (or a parallel `system_notification_channels` table) and a `DispatchToSystem(...)` path that reads system channels on the admin pool. Pro: the `Transport` interface, encryption-at-rest, dispatch-audit, and Email/Slack transports are all reused verbatim — adding Teams/PagerDuty/Opsgenie later is the same ~60-line transport add that's already documented. Con: one table now serves two trust scopes; RLS policy needs a careful carve-out.
- **(B) A separate internal alerter** (its own tiny table + dispatcher) that *imports* the same `Transport` implementations but keeps system channels physically separate from tenant channels. Pro: zero RLS ambiguity; the two scopes can never leak into each other by query mistake. Con: a little duplication in the dispatcher/CRUD.

**Recommendation: (B) for storage isolation, reusing (A)'s `Transport` implementations.** The transports (`email_smtp.go`, `slack_webhook.go`, scrubbing, the encrypted-config pattern) are the valuable, tested part and should be shared. The *channel storage* is where mixing scopes is dangerous, so keep that separate. New transports requested ("or other integration") — PagerDuty/Opsgenie for health pages, Teams, a generic webhook — are each a new `Transport` impl wired into the internal dispatcher; the pre-provisioned `teams`/`jira` enum values show the intended extension path.

### 6.3 Per-staff vs team-wide

v1: team-wide channels only (a Slack `#ops` webhook, an ops mailing list). Per-staff notification preferences are deferred — same call the per-org plan made (`notifications-plan.md` defers per-user prefs).

---

## 7. Entitlement & licensing under SaaS  ← the license question

The user's question, restated: *under SaaS, should the license be shown to the customer (no), kept in the app, or minted long-term — what's best practice?*

### 7.1 The core reframe

A **license JWT is a transport mechanism for entitlement across a trust boundary you do not control.** Self-hosted: the customer runs the binary on their infra, so AxiaOps cannot keep entitlement in a database it owns — it ships a signed, offline-verifiable, expiring token instead. That is the *only* reason the license package exists.

Under SaaS the boundary inverts: **AxiaOps owns the database the tenant lives in.** Entitlement is therefore just a row AxiaOps controls directly, driven by billing state. Shipping a signed token to yourself, to verify against your own embedded key, on your own server, gates nothing a `WHERE` clause can't — it's ceremony with no security value.

So:

- **Show a license to the customer? No — and more than that, there is no customer-facing license at all under SaaS.** The customer sees *plan* and *usage* ("Pro plan, 4/10 accounts connected, renews 2026-07-01"), never a "license". `GET /v1/version`'s `license` sub-object collapses to `state: "managed"` (or is omitted) for hosted tenants — the self-hosted license fields (`customer_id`, `expires_at`, `days_remaining`) are meaningless when entitlement is a live subscription.
- **Keep the license machinery in the app?** Yes — *dormant*. The `shared/license` package stays compiled in; the `cmd/api-saashosted` composition root calls `license.SetEnforcementBypass()` at boot so `IsScanAllowed()` returns true regardless of any license state. This is exactly what the `IsEnforcementBypassed()` seam was reserved for. The scan-gate code does not change — it already consults a single predicate; SaaS just makes that predicate defer to entitlement instead of license.
- **Mint a long-term license instead?** **Not the end state — but it is the deliberate day-one scaffold (decided, §11.1).** A single long-lived platform license (like the 100-year dev fixture, `customer_id="axiaops-saas-platform"`) *works* as a one-line bootstrap — it keeps the existing gate happy with zero new code. But it gates the *platform*, not the *tenant*: it cannot express "tenant A is paid, tenant B's trial lapsed." Per-tenant gating is the entire job under SaaS, so a platform license is the wrong tool for the *end* model. **Sequencing decision:** use the platform-license scaffold for the free validation beta (where every participant is allowed and there is nothing to bill), and build the real per-tenant `entitlements` table + bypass wiring only once the [ADR-0002](decisions/0002-saas-first-for-awareness.md) self-serve activation gate proves out and there is something to charge for. This keeps billing/entitlement plumbing off the critical path until the PLG bet is validated.

> **Status (2026-06-11): SaaS is now the DEFAULT build, shipped to every env.** The model was inverted from the original "opt-in SaaS" design. Previously the SaaS bypass+entitlement wiring was an opt-in `saashosted` build tag and the default build was self-hosted/licensed. Now the **default** build (no tag, `services/{api,ingestion}/cmd/saasmode_saas.go`, `//go:build !selfhosted`) calls `license.SetEnforcementBypass()` at boot and gates scans on per-tenant entitlement, and the **self-hosted licensed** build is the opt-in **`-tags selfhosted`** (`saasmode_selfhosted.go`). So every deployed env (dev-1/2, staging, prod) runs SaaS via the default `build:images` image; the licensed customer image is the manual `build:images-selfhosted` job (`-tags "production selfhosted"`, `-selfhosted`-suffixed tag), shipped pre-built — that pre-built distribution is what makes inverting the *compile* default safe (a bare `go build` never reaches a customer). `build:selfhosted-shape` pins the licensed shape every pipeline; both `main.go` log `license: mode resolved mode=saas|selfhosted` at boot.
>
> Because the fail-closed entitlement gate now governs every env and billing is still unbuilt, **every org is auto-entitled at creation** (a default `active`/`internal` `entitlements` row, idempotent, written on the `axiaops_runtime` pool at the org-create chokepoints; migration 034 also backfills pre-existing orgs). `cmd/entitlement-seed` remains the manual override; a future billing webhook overwrites via `ApplyBillingEvent`. The pre-billing posture is deliberately "entitled by default until billing says otherwise."
>
> *Implementation note: the original plan named separate `cmd/api-saashosted` / `cmd/ingestion-saashosted` composition roots. That doesn't work for ingestion (its wiring lives in `package main`, which a sibling `main` package can't import), so the implementation uses the **`selfhosted` build tag** on the existing roots (SaaS is now the default, licensed is the opt-in) — same compile-time guarantee, no duplicated `main`. The `cmd/api-saashosted` references elsewhere in this doc predate the inversion; the realized mechanism is the build-tag seam.*

### 7.2 Recommended model: per-tenant Entitlement, billing-driven

Introduce an `entitlements` table (one row per org), the SaaS analogue of the license claim:

```
entitlements
  organization_id   FK, unique
  plan              text     -- free | pro | enterprise
  status            text     -- trialing | active | past_due | canceled | suspended
  max_accounts      int      -- the SaaS analogue of license max_organizations
  features          text[]   -- analogue of license features[]
  trial_ends_at     timestamptz null
  current_period_end timestamptz null
  updated_at        timestamptz
  -- billing_customer_ref, billing_subscription_ref (opaque to this codebase)
```

- **Billing is the source of truth.** Stripe (or chosen provider) webhooks update `status` / `current_period_end`. The app never computes entitlement from payment events directly — it reads this table.
- **One gate predicate, mirroring the license one.** `entitlement.IsScanAllowed(orgID)` returns true for `status ∈ {trialing, active}` (+ a grace window on `past_due`, exactly like the license `in_grace` philosophy in `b1.6-amendment`), false for `canceled/suspended`. Reads/dashboard stay open on lapse (same graceful-degradation posture as self-hosted) — you don't hide a customer's data because a card expired; you stop *doing new paid work* (scans).
- **`past_due` grace window: ~14–21 days (decided, §11.1).** During the dunning window `IsScanAllowed` stays true and scans keep running while the billing provider retries the card and nags. After it, `status` flips to `suspended` and new scans stop — but **dashboard reads are never withheld for non-payment**. Far shorter than the self-hosted license's 90-day grace, because re-billing a fixed card is instant whereas re-issuing a license is a slow, high-touch operator action. The exact day count tracks the billing provider's retry schedule (Stripe Smart Retries default ≈ 3 weeks) — keep the two aligned so the app doesn't suspend before the provider has finished retrying.
- **`max_accounts` enforced at connect-time**, the way `max_organizations` is conceptually bounded today.

### 7.3 Decision matrix

| Approach | Per-tenant gating | Customer sees a "license" | New code | Verdict |
|---|---|---|---|---|
| Per-tenant license JWT minted per org | weak (static, can't track live billing) | yes (bad) | medium | ✗ wrong tool |
| One long-lived platform license | none | no | ~1 line | scaffolding only |
| **Bypass license + `entitlements` table** | **strong, live** | **no** | medium | ✓ **recommended** |

### 7.4 What the customer sees

Never a license. A **billing/plan page**: current plan, usage vs limits, renewal date, invoices, "upgrade." All sourced from `entitlements` + the billing provider's portal. The word "license" does not appear in the hosted product.

### 7.5 What staff see — entitlement visibility

The question "is the license visible to admin/support?" only arises under SaaS — under self-hosted, AxiaOps has no access to the customer's deployment, so staff cannot see a customer's license JWT at all (the customer sees their own at `/v1/version`; AxiaOps does not). Under SaaS the per-tenant license is replaced by entitlement (§7.2), and the answer is **yes, role-scoped — but only the derived state, never a raw token**:

| Surface | support | ops | billing | superadmin |
|---|---|---|---|---|
| **Entitlement summary** — plan, status, limits + current usage, trial/renewal dates, lapse reason | read | read | read | read |
| **Write entitlement** — plan change, grace override, suspend/resume | — | suspend/resume only | full | full |
| **Billing PII** — cards, invoices, refunds, dunning detail | — | — | read+write | read |
| **Tenant FinOps data** — zombies, costs (the break-glass surface, §5) | grant-gated | grant-gated | — | — |

Two invariants regardless of role:

- **No raw license/token or signing material in any UI** — only the derived entitlement state. Per-tenant there is no JWT to show; the optional dormant *platform*-license scaffold (§7.1) is a deployment-level fact visible to `ops`/`superadmin` only, never per-tenant support.
- **Reading the entitlement summary is NOT a tenant-data read.** "Pro, active, 4/10 accounts, renews 2026-07-01" is not the customer's cost data and does not trip the audited break-glass grant (§5). Only zombie/cost reads do. This is why support can answer "why did my scans stop?" (entitlement lapse) without opening a grant, but cannot see *what* the scans found without one.

---

## 8. Data-model deltas (summary)

New tables (all system-scoped, on the admin pool — *not* org-RLS):

- `staff_users` — AxiaOps employee identities (§4).
- `staff_role_grants` — staff principal → staff role.
- `staff_access_grants` — break-glass cross-tenant grants (target org, reason, expiry, granted_by) (§5).
- `staff_access_log` — system-side mirror of every cross-tenant action (the tenant-side copy lives in that org's `audit_log`).
- `system_notification_channels` + `system_notification_dispatches` — internal-ops integrations (§6, option B).
- `entitlements` — per-org plan/status/limits (§7).

Existing `audit_log` gains a `staff_access` action class and a way to record an actor that is *not* a member of the org (today `actor` is always an org member).

---

## 9. Security properties

- **Plane separation** (§3): no principal spans tenant/admin. Staff IdP ≠ tenant IdP.
- **No standing cross-tenant access** (§5): RLS-bypass is grant-scoped, reason-stamped, time-boxed, double-audited.
- **Separate admin binary/ingress** (§4.3): `cmd/api-admin` is not internet-facing like the tenant API; VPN/SSO-only.
- **Entitlement lapse degrades gracefully** (§7.2): stops new scans, never hides existing data — mirrors `b1.6-amendment`.
- **Internal channel secrets** reuse the existing encrypted-at-rest + error-scrubbing transport pattern.
- **Least-privilege staff roles** (§4.2): support can't touch money; billing can't read tenant data.

---

## 10. Phased rollout (sketch — refine in §12)

1. **Entitlement first** (§7) — the `entitlements` table + bypass wiring + gate predicate. Unblocks "charge money" and is independent of the admin plane.
2. **Staff identity + read-only admin console** (§4, read paths of §5) — see tenants, no impersonation yet.
3. **Break-glass + impersonation** (§5) — the audited write/act paths.
4. **Internal-ops notifications** (§6) — wire the lifecycle/billing/security/health events.

Each phase is shippable alone; (1) has standalone value even before any admin UI exists.

---

## 11. Decisions

### 11.1 Resolved (2026-06-04)

1. **Staff identity home (§4.1)** — **separate `staff_users` table + corporate IdP.** Staff and customer auth are different trust domains; the clean blast-radius separation is worth the extra auth path over the "internal org" shortcut that entangles them.
2. **Tenant-visible support access (§5.5)** — **transparent by default, with a gated silent mode** for fraud/abuse investigations (silent access requires `superadmin` + a heightened audit class). Trust posture regulated buyers increasingly require.
3. **Entitlement bootstrap (§7.1)** — **scaffold now, real `entitlements` before charging.** Use the one-line platform-license scaffold for the free validation beta; build the real per-tenant `entitlements` table + bypass wiring only once self-serve activation proves out (the [ADR-0002](decisions/0002-saas-first-for-awareness.md) validation gate). No billing/entitlement plumbing before the PLG bet is validated.
4. **`past_due` grace (§7.2)** — **~14–21 days of dunning, scans keep running; then suspend new scans, never cut dashboard reads.** SaaS-standard, far shorter than the self-hosted license's 90 days (re-billing is instant; re-issuing a license is not). Reads are never withheld for non-payment — a customer's own cost data is not held hostage over an expired card.

### 11.2 Still open

5. **Staff role taxonomy (§4.2)** — is support/ops/billing/superadmin the right cut, or do we need a separate "read-only auditor" / "engineering" tier?
6. **Impersonation consent model (§5.6)** — customer-generated support token vs ops-approval — which is v1?
7. **Internal notifications storage (§6.2)** — separate table (recommended B) vs scoped column on existing channels (A)?
8. **Admin plane hosting** — same ECS cluster behind separate ingress, or fully separate account/VPC for blast-radius isolation?

---

## Appendix A — Prior art & best practices (entitlement vs license)

Industry grounding for the §7 recommendation, so a future reviewer doesn't re-derive it.

### A.1 The core split: nobody puts a license on a SaaS app

A *license* is a signed artifact shipped across a trust boundary the vendor does **not** control (the customer's own infra). SaaS controls the boundary, so entitlement is just data the vendor owns. Every mature dual-SKU vendor lands on **one feature set, two entitlement mechanisms**:

| Vendor | SaaS / cloud entitlement | Self-hosted entitlement |
|---|---|---|
| GitLab | subscription + seats (GitLab.com) — **no license file** | EE activation code / offline license file |
| HashiCorp | HCP metered/subscription billing — **no license file** | Enterprise license file |
| Atlassian | Cloud subscription + seats — **no license file** | Data Center license key |
| Sourcegraph / Metabase / Mattermost / PostHog | cloud billing | enterprise license key |

**No vendor uses a license artifact on the SaaS side.** This is the direct justification for keeping `shared/license` dormant under SaaS (`SetEnforcementBypass`) and gating on `entitlements` instead — and for *not* minting per-tenant license JWTs (§7.1).

> Trend note: GitLab moved self-managed onto "cloud licensing" — an activation code that syncs entitlement from their billing system rather than a static offline file (offline still offered for air-gapped). Even the self-hosted license is drifting toward "thin pointer to billing-as-truth" where connectivity allows. AxiaOps keeps the pure-offline JWT because its ICP is regulated/air-gapped — the conservative end of that spectrum, deliberately chosen.

### A.2 SaaS entitlement best practices (the patterns to copy)

1. **Billing is source of truth, *projected* into an internal entitlements store — never gated inline.** Anti-pattern: `if stripe.status == "active"` scattered through handlers. Best practice (Stripe's own Entitlements API, 2024; GitLab; Chargebee/Recurly playbooks): billing webhooks update an internal table, the app reads only that table. Provider-agnostic, survives billing-provider outages, one auditable seam. → matches the §7.2 single-predicate design.
2. **Model plan / features / quotas as three distinct things**, not tiers-with-if-statements: a label (`plan`), booleans (`features[]`), numerics (`limits{}`). Enables custom enterprise deals + grandfathered plans without code changes.
3. **Graceful degradation on non-payment — warn → dunning → downgrade, never hard-cut.** Stripe Smart Retries (~weeks), GitLab/Atlassian degrade rather than brick. A payment failure stops *new paid work* and nags; it never destroys or hides existing data. → identical to the `b1.6-amendment` posture, now generalised to entitlement (§7.2).
4. **Customer sees plan + usage, never an internal token** (§7.4).
5. **Idempotent, order-tolerant webhook handling** — billing events arrive out of order and get replayed; the projection must be idempotent.

### A.3 Self-hosted licensing best practices (what AxiaOps already does)

- **Signed, offline-verifiable license file/JWT, public-key-verified in the binary, with a grace period before degradation** — GitLab EE, HashiCorp Enterprise, Elastic, JetBrains, Sourcegraph. This is `shared/license`. Correct for air-gapped/regulated buyers (no connectivity required).
- **Phone-home / license-server** (Keygen, Cryptolens, Replicated) — better revocation + usage telemetry, but requires connectivity → customer-hostile for AxiaOps's ICP. Correctly avoided.
- **Buy-vs-build tooling** that exists if ever reconsidered: Keygen, LicenseSpring, Cryptolens (licensing); Replicated (self-hosted distribution + air-gap). The current dependency-free offline-JWT approach is sound.

## Appendix B — seams this design reuses (already in the codebase)

| Seam | Location | This doc's use |
|---|---|---|
| `auth.Provider` | `services/api/internal/auth/provider.go` | `staff.Provider` is a 3rd impl |
| `serverbuild.ComposeServer` | `services/api/internal/serverbuild/build.go` | `ComposeAdminServer` sibling for `cmd/api-admin` |
| `license.SetEnforcementBypass` / `IsEnforcementBypassed` | `services/shared/license/state.go` | `cmd/api-saashosted` bypasses; entitlement gates instead |
| `notifications.Transport` | `services/shared/notifications/transport.go` | internal-ops dispatcher reuses the transports |
| `axiaops_runtime` RLS-bypass pool | `docs/runtime-admin-db-role.md` | grant-scoped cross-tenant access (§5) |
| scan-gate predicate pattern | `b1.6-amendment-feature-gating.md` | generalised license-state → entitlement-state |
