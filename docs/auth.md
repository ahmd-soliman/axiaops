# Auth Provider Evaluation — AxiaOps

> ⚠️ **HISTORICAL.** This document records the original Phase-2 decision to use
> Kinde. That decision was reversed by [ADR-0001](decisions/0001-deployment-model.md)
> and Kinde was removed in MR `chore/remove-kinde-auth` (2026-05-06). Production
> auth is now native cookie sessions (argon2id) + per-org OIDC SSO via the seam
> in `services/api/internal/sso/`. Read this doc as historical context; do not
> act on its recommendations.

## Decision: Kinde (since reversed — see banner above)

Chosen for Phase 2. Reasons summarised at the bottom of this document.

---

## Comparison

| Feature | Kinde | Supabase Auth | Clerk | Cognito |
|---------|-------|---------------|-------|---------|
| **Pricing (free tier)** | Free until \$1M ARR | Free up to 50,000 MAU | Free up to 10,000 MAU | Free up to 50,000 MAU |
| **Paid tier** | \$25/mo (Pro) | \$25/mo (Pro) | \$25/mo (Pro) | \$0.0055/MAU after free tier |
| **SAML SSO** | Included in Pro | Not included — needs WorkOS (\$125/connection/mo) | \$50/org/mo add-on | Included (complex setup) |
| **Organizations** | Built-in, multi-org per user | Not built-in | Built-in | Not built-in (custom attributes) |
| **Org switching** | Built-in | Not built-in | Built-in | Must build yourself |
| **React Native SDK** | Native (no WebView) | Community library | Native (no WebView) | WebView / hosted UI only |
| **Go SDK** | Official | Official (via PostgREST) | No official Go SDK | Official (AWS SDK) |
| **Self-hosted option** | No | Yes (Docker) | No | No (AWS-managed) |
| **GDPR** | Yes | Yes | Yes | Yes |
| **SOC 2 Type II** | Yes | Yes | Yes | Yes |
| **ISO 27001** | In progress | In progress | Yes | Yes |
| **Data residency** | EU region available | EU region available | US only | Any AWS region |
| **Vendor lock-in risk** | Medium | Low (open source) | Medium | High (AWS proprietary) |
| **DX / onboarding** | Excellent | Good | Excellent | Poor |
| **Community size** | Small (growing) | Large | Large | Very large |

---

## Kinde

**Founded:** 2022, Melbourne, Australia. Founders: Dave Faber and Andrew Fogg.
**Backed by:** Blackbird Ventures (top Australian VC — early Canva investor).

### Strengths
- Built-in organizations and org switching — directly maps to AxiaOps multi-tenant model
- SAML SSO included in Pro (\$25/mo) — no WorkOS (\$125/connection/mo) needed
- Native React Native SDK using Expo SecureStore — no WebView pop-up
- Official Go SDK for the backend
- Management API — create orgs and users programmatically on signup
- Developer-first docs with working code examples
- Free until \$1M ARR for most features

### Weaknesses
- Smallest community of the four options
- No self-hosted option — if Kinde shuts down, you must migrate
- Machine-to-machine (M2M) tokens are paid tier
- ISO 27001 not yet certified (in progress)

---

## Supabase Auth

Open-source auth built into the Supabase platform. JWT-based, Postgres-native.

### Strengths
- Open source — lowest vendor lock-in risk; self-hostable
- Large community and ecosystem
- Tight integration with Supabase Postgres (RLS policies can reference `auth.uid()` directly)
- Good documentation

### Weaknesses
- No built-in organizations or org switching — must model this yourself in Postgres
- No SAML SSO without adding WorkOS (\$125/connection/month per enterprise customer)
- React Native support is community-maintained, not official
- Becomes complex fast for B2B multi-tenant with SSO requirements

**Cost with SSO at 10 enterprise customers:**
Supabase Pro \$25/mo + WorkOS \$1,250/mo = **\$1,275/month**

---

## Clerk

Auth provider with excellent DX and a polished hosted UI.

### Strengths
- Excellent developer experience — widely praised
- Built-in organizations and org switching
- Native React Native SDK
- Strong documentation and large community

### Weaknesses
- No official Go SDK — Go backend requires manual JWT verification
- SAML SSO is an add-on: **\$50/org/month** — at 10 enterprise customers that is \$500/month on top of base pricing
- Data residency is US only — potential GDPR issue for EU customers
- More expensive than Kinde at scale

**Cost with SSO at 10 enterprise customers:**
Clerk Pro \$25/mo + SSO add-on \$500/mo = **\$525/month**

---

## Cognito

AWS-managed identity service. Part of the AWS ecosystem.

### Strengths
- SAML SSO included — no separate charge per connection
- Scales to millions of users
- Deep AWS integration (IAM, API Gateway, ALB)
- Large community

### Weaknesses
- Hosted UI does not support org switching — must build custom UI (2–3 weeks minimum)
- No built-in organization concept — must model with custom attributes and custom logic
- Home realm discovery (email domain → SAML provider) must be built from scratch
- React Native flow uses a WebView browser pop-up — not a native experience
- Poor developer experience — documentation is AWS-style (verbose, hard to navigate)
- Significant ongoing maintenance burden for a startup

---

## Login Screen

### Option A — Branded hosted UI (current approach)

- Upload logo and set brand colors in the Kinde dashboard → Settings → Design
- Set a custom domain: `login.axiaops.io` → CNAME pointing to Kinde
- Zero code, looks professional, done in 10 minutes
- **This is what AxiaOps uses now**

#### Setup steps

**Step 1 — Logo**

Kinde dashboard → Settings → Design → Brand assets

Upload: `services/dashboard/assets/icon.png`

**Step 2 — Brand colors**

| Field | Value |
|---|---|
| Primary / Button color | `#F97316` (orange — matches login button) |
| Background | `#0F172A` (dark navy — matches app background) |
| Text on background | `#FFFFFF` |
| Muted text | `#94A3B8` |

**Step 3 — Custom domain**

Kinde dashboard → Settings → Domains → Add domain → enter `login.axiaops.io`

Kinde will provide a CNAME target. Add it in your DNS provider:

```
Type:  CNAME
Name:  login
Value: <CNAME target from Kinde>
TTL:   Auto
```

DNS propagation takes 5–30 minutes. After that the branded login is live.

### Option B — Fully custom UI (Phase 3+)

- Install `@kinde-oss/kinde-auth-pkce-js` SDK in the React Native app
- Build your own form, call `kinde.login()` — Kinde still handles the auth backend
- You own the HTML/CSS; Kinde owns the token issuance and session management
- Worth doing when you need pixel-perfect control or a native mobile login screen

Option B adds 1–2 weeks of frontend work with no auth-security benefit. Defer until
there is a specific UX requirement that Option A cannot satisfy.

---

## Why Kinde Won

| Requirement | Why Kinde |
|-------------|-----------|
| Multi-tenant with org switching | Built-in, no custom code |
| Enterprise SSO without \$125/connection/mo | Included in \$25/mo Pro |
| React Native native flow | Native SDK, no WebView |
| Go backend JWT verification | Official Go SDK |
| Fast to implement | Working auth in under 10 minutes |
| EU data residency | EU region available |

---

## Migration Path Away from Kinde

If Kinde raises prices significantly, is acquired, or shuts down, migration is straightforward because **JWTs are a standard** and the rest of the stack does not depend on Kinde internals.

### What is Kinde-specific

- JWT issuer URL (`https://<your-subdomain>.kinde.com`)
- JWKS endpoint for token verification
- Organization ID claim in the JWT payload (`org_code`)
- Kinde Management API calls (create org, invite user)

### Migration steps

**1. Choose a replacement provider**

| Scenario | Recommended replacement |
|----------|------------------------|
| Stay managed, keep DX | Clerk (add Go SDK shim) or WorkOS |
| Reduce vendor risk | Supabase Auth + custom org model |
| Full control | Keycloak (self-hosted) |

**2. Run both providers in parallel (zero-downtime)**
- Add a `NEW_AUTH_ISSUER` env var to the Go API
- Accept JWTs from both issuers during the migration window
- Migrate users to the new provider (most providers offer bulk import via CSV or API)
- Once all users are migrated, remove the old issuer

**3. What changes in Go code**

Only the JWT middleware changes. The rest of the API is auth-provider agnostic:

```go
// Before (Kinde)
const issuer = "https://axiaops.kinde.com"

// After (any OIDC provider)
const issuer = os.Getenv("AUTH_ISSUER") // e.g. Clerk, Supabase, Keycloak
```

The organization ID (`org_code` in Kinde) maps to whatever claim the new provider uses for the org identifier. That claim name is the only application-level change.

**4. What does NOT change**
- PostgreSQL schema — `organization_id` column is provider-agnostic
- Row-level security policies — they depend on `organization_id`, not on Kinde
- React Native auth screens — swap the Kinde SDK for the new provider's SDK
- All business logic — zero changes

**Migration effort estimate:** 1–2 days of engineering work, assuming user export/import is available from Kinde (it is — Kinde supports user export via Management API).

### Reduce lock-in now

- Store `organization_id` as a UUID you control, not Kinde's `org_code` directly. Map Kinde org_code → your internal organization_id in a `organizations` table. This means a provider swap does not require a data migration.
- Never put Kinde-specific claims in your business logic — only extract `organization_id` in the middleware layer and pass it down as a plain string.

Authorization (role-based access control) is documented separately in `docs/rbac-design.md`.
