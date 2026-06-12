# Stripe Billing Integration — Implementation Plan (SaaS SKU)

Status: **draft for refinement.** Implements ADR-0002 commitment #2 (Stripe →
internal `entitlements` projection) and the customer-facing billing surface for
the hosted "AxiaOps Cloud" SKU. Builds directly on the Phase 2A/2B entitlement
scaffold (`services/shared/entitlement/`, migration `033_entitlements` /
`034_entitlement_internal_plan`) — this plan turns that **dormant projection
seam** (`entitlement.ApplyBillingEvent`) into a live, webhook-driven billing
loop. Slice style matches [`self-signup-plan.md`](self-signup-plan.md): numbered,
dependency-ordered, each independently shippable + testable.

> **Goal:** an owner of a SaaS org can subscribe to a paid plan via Stripe
> Checkout (hosted, SAQ-A), Stripe webhooks drive the org's `entitlements` row
> through its lifecycle (trial → active → past_due → canceled/suspended), the
> scan-gate that already reads that row stays the single enforcement point, and
> the customer sees a plan/usage page (never a "license"). **No card data ever
> touches AxiaOps infrastructure.**

> **Supersedes** the stale `Tasks.md` §3.1 task list, which predates the
> entitlement design: it references a `tenants` table, a `plan` column on it, a
> `007_add_billing_fields.sql` migration, and a `middleware/billing.go` tier
> reader — **none of which match the shipped model.** Entitlement is a
> system-scoped `entitlements` table (one row per org, no RLS, `axiaops_runtime`
> pool), and the gate is `entitlement.IsScanAllowedForOrg`, not a middleware
> reading a column. This plan is the canonical billing spec; §3.1 of `Tasks.md`
> is retired by it.

## Relationship to other docs

- [ADR-0002 — SaaS-first for awareness](decisions/0002-saas-first-for-awareness.md) — accepted 2026-06-11; commitment #2 makes this the next major build; open follow-ups (trial shape, pricing, Stripe-vs-Paddle) are resolved as decision rows below.
- [`saas-platform-admin-design.md`](saas-platform-admin-design.md) §7 — the entitlement model (table shape, statuses, grace window, `ApplyBillingEvent` seam). §2.2 explicitly defers "Stripe integration mechanics" to **this** doc.
- [`security-before-billing-plan.md`](security-before-billing-plan.md) — the security pass sequenced **before** this build (§2 prerequisites) and the eight billing-surface security requirements (§3) baked into the slices below. The single external pen-test (§4) lands **after** this build, **before** first invoice.
- [`self-signup-plan.md`](self-signup-plan.md) — the companion funnel plan. Today every new org gets a default `active`/`internal` entitlement at the org-create chokepoints (the `ensureDefaultEntitlement` helper in `postgres.go` for `UpsertOrganization`/`EnsureOrganization`; an equivalent inline INSERT in `ConsumeBootstrapState`). **This plan changes the self-signup default to a `trialing` entitlement** — the seam between the two plans is DS3 + Slice 6 below.
- [`business_plan.md`](business_plan.md) §Pricing — the Starter €49 / Growth €149 / Team €399 tiers. Treated as a **decision row** (DS2), not a hard-coded fact: exact prices live in Stripe + a price-ID→plan map, changeable without a deploy.

---

## 0. Decisions taken (settle before coding)

| # | Decision | Rationale |
|---|---|---|
| **DS1** | **Stripe is the provider; no merchant-of-record (Paddle) for the beta.** EU VAT handling is via **Stripe Tax** (automatic), not a MoR. Revisit if VAT/invoicing compliance proves heavier than Stripe Tax covers (ADR-0002 open follow-up). | Stripe Checkout + Billing Portal give SAQ-A and a hosted dunning UX with the least code. The `entitlement.BillingEvent` seam is provider-agnostic, so a later Paddle swap only re-writes the webhook decoder (Slice 2), not the gate or table. |
| **DS2** | **Pricing is config, not code.** Three launch tiers map to Stripe Prices; a `plan_id → {plan, max_accounts, features}` map lives in one Go file + env-injected price IDs. `plan` column values stay the existing `free`/`pro`/`enterprise`/`internal` CHECK set (migrations 033/034) — the **marketing tier** (Starter/Growth/Team) is a *display* label derived from the price ID, NOT a new `plan` enum value. | The `entitlements.plan` CHECK is `free|pro|enterprise|internal` (migrations 033/034). Adding `starter|growth|team` would be a migration churn every time marketing renames a tier. Map the price ID → an existing `plan` bucket + a `max_accounts` number + a display name; rename tiers in Stripe + the map, never the DB. |
| **DS3** | **Self-signup orgs start `trialing`, card-NOT-required (reverse trial).** The signup default flips from `active`/`internal` to `trialing` with a `trial_ends_at = now + TRIAL_DAYS` and `max_accounts = trial cap`. No Stripe object exists until they hit Checkout. At trial end with no subscription → `status` flips to `canceled` (scans gate; reads stay open). | ADR-0002 is a PLG bet — a card wall before the aha-moment kills activation (mirrors `self-signup-plan.md` DS3's soft-gate philosophy). `trialing` is already scan-allowed by `entitlement.IsScanAllowed`, so the funnel works with zero gate changes. Bootstrap (self-hosted) orgs are unaffected — they keep `internal`/`active` (DS7). |
| **DS4** | **Webhook is the ONLY entitlement write path from billing.** Checkout success does NOT write the entitlement — it redirects to a success page that says "activating…"; the `checkout.session.completed` / `customer.subscription.*` webhook is what projects the row. The success page polls `/v1/billing/entitlement` until status flips. | Security req §3.4: entitlements derive only from webhook-driven DB state, never client-supplied claims. A client that fakes a success redirect changes nothing. Idempotent + order-tolerant by construction (the upsert is keyed on `organization_id`). |
| **DS5** | **`max_accounts` is the only hard plan limit enforced at launch.** Enforced at account-connect time (`POST /v1/accounts`). `features[]` is populated but NOT gated yet (no feature is paywalled in v1). | Keep enforcement surface minimal. `max_accounts` already exists on the row + maps cleanly to the tier table. Feature gating (PDF reports, remediation tracking) is a decision row deferred to post-launch — the `features[]` column is wired so adding a gate later is a predicate, not a migration. |
| **DS6** | **Billing code is INERT in the `-tags selfhosted` build via the existing `saasmode_*.go` build-tag seam — not a runtime flag.** The webhook route, checkout/portal handlers, and the Stripe client are constructed in `saasmode_saas.go`'s wiring and passed through `Deps`; `saasmode_selfhosted.go` supplies nil, so `ComposeServer` registers no billing routes when the resolver-style billing dep is nil. The `stripe-go` dependency lives only in the api module's `billing` package (Slice 2 placement) — it never reaches shared or the ingestion binary; in a `-tags selfhosted` api binary the package still compiles but **no billing handler is ever wired**. | Mirrors exactly how `EntitlementResolver` already flips license-vs-entitlement at the same seam (`entitlementGate(store)` returns `(store, grace)` in saas, `(nil, 0)` in selfhosted). Billing is the SaaS analogue and rides the same compile-time guarantee — a selfhosted/customer binary has no code path that registers `/v1/webhooks/stripe`. Self-hosted keeps the license gate untouched. |
| **DS7** | **Bootstrap/self-hosted orgs keep `internal`/`active`; only the SaaS signup path mints a trial.** The bootstrap chokepoint keeps its current default-entitlement write (`ConsumeBootstrapState` does the INSERT inline in its transaction; `UpsertOrganization`/`EnsureOrganization` call the `ensureDefaultEntitlement` helper in `postgres.go`); the signup chokepoint gets a `trialing` variant. The two are distinguished by which org-create path runs, NOT by build tag (both could in principle run in one binary, but signup is `SIGNUP_ENABLED`-gated per the companion plan). | Self-hosted single-tenant must never enter a trial it can't exit (no Stripe). `internal`/`active` is the deliberate "entitled forever, billing-irrelevant" marker (migration 034). The seam is the org-create chokepoint, consistent with how `self-signup-plan.md` Slice 1 already forks bootstrap vs register. |
| **DS8** | **Restricted Stripe keys, two of them.** The handler that creates Checkout/Portal sessions uses a **restricted key** scoped to Checkout + Billing Portal + Customers (no full secret key in that path). The webhook needs only the **webhook signing secret** (`STRIPE_WEBHOOK_SECRET`) for `ConstructEvent` — it does NOT need a secret key to *verify*; it needs a read-capable key only if it *re-fetches* an object (we avoid that — see Slice 4). Live/test keys are separate per env. | Security req §3.5 (C-2 lesson: no broad secret in a narrow service). Stored in SSM/CI variables, never in compose files. |

---

## 1. Slice ordering & critical path

```
Slice 0  Stripe account/object model + price-ID→plan map   (S)  ── design artifact, no deploy
Slice 1  processed_stripe_events idempotency migration      (S)  ── critical path
         + storage methods (035)
Slice 2  Stripe→BillingEvent decoder (pure, no I/O)         (M)  ── critical path
Slice 3  POST /v1/webhooks/stripe handler + raw-body verify (L)  ── critical path
         + publicPath case + wiring (NO httpjson)
Slice 4  Checkout: POST /v1/billing/checkout-session        (M)  ── critical path
         (owner-only, rate-limited) + success/cancel pages
─────────────────────────────────────────── (minimum: a customer can pay and get entitled)
Slice 5  Customer portal: POST /v1/billing/portal-session   (S)  ── manage/cancel
Slice 6  Self-signup trial entitlement (flip the default)   (M)  ── trial lifecycle
         + trial-expiry sweep ticker
Slice 7  max_accounts enforcement at connect-time           (S)  ── plan limits (DS5)
Slice 8  Dashboard billing/plan page (#131)                 (L)  ── replaces hidden License page
Slice 9  Audit events + internal-ops notification seam      (S)  ── observability/trust
```

**True critical path** (a stranger trials, then pays, and the scan-gate honours
it): Slices **1–4**. Slice 1 (idempotency) MUST precede Slice 3 (the handler that
needs it). Slice 2 (pure decoder) is testable in isolation before the HTTP shell
(Slice 3) exists. Slice 6 (trial) can ship before or after the pay path but the
**signup default must not flip to `trialing` until the Checkout path exists**
(Slice 4) or trials would expire to `canceled` with no way to pay — so Slice 6
depends on Slice 4.

**Cut-line for an internal billing dry-run:** Slices 1–4 behind test-mode keys.
**Minimum responsible public launch:** 1–8 + the pen-test
([`security-before-billing-plan.md`](security-before-billing-plan.md) §4), then
first invoice.

---

## Slice 0 — Stripe account/object model + price-ID→plan map (S)

**Goal.** The non-code design artifact + the one Go file that maps Stripe Prices
to entitlement shape. No deploy.

**Stripe object model (configured in the Stripe dashboard, test + live):**
- **One Product per tier** (`Starter`, `Growth`, `Team`), each with **one recurring monthly Price** (EUR). Pricing is DS2 (Starter €49 / Growth €149 / Team €399 today) — set in Stripe, not code.
- **Stripe Tax** enabled (DS1) for EU VAT.
- **Customer Portal** configured (allowed: update payment method, cancel subscription, view invoices; disallowed: switch to an unmapped price).
- **Smart Retries** enabled on the dunning schedule, aligned to ~21 days (DS / `ENTITLEMENT_GRACE_DAYS`).
- **Restricted API keys** minted (DS8): one for session creation, the webhook secret for verification.

**Files**
- `services/shared/entitlement/pricing.go` — **new**. `PlanForPrice(priceID string) (PlanMapping, bool)` over a map populated from env price IDs. `PlanMapping{ Plan string /* "pro" etc. */, DisplayTier string /* "Growth" */, MaxAccounts int, Features []string }`. Also define the **`PriceResolver` interface here** (alongside `PlanMapping`, mirroring how `Resolver`/`Writer` live in `entitlement.go`) — it is the seam the Slice-2 decoder takes so it stays free of env reads.
- The map is **built at composition time** from env vars (Slice 3/4 wiring), so test/live price IDs differ per env without a code change. `pricing.go` holds the structure + the launch defaults; the IDs are injected.

**additions**
- A canonical mapping table (also the source for the launch-tier doc row):

  | Stripe Price (env) | `plan` (DB CHECK) | Display tier | `max_accounts` | `features[]` (wired, not gated v1) |
  |---|---|---|---|---|
  | `STRIPE_PRICE_STARTER` | `pro` | Starter | 1 | `["email_alerts"]` |
  | `STRIPE_PRICE_GROWTH` | `pro` | Growth | 5 | `["slack_alerts"]` |
  | `STRIPE_PRICE_TEAM` | `enterprise` | Team | 20 | `["pdf_reports","remediation_tracking","audit_trail"]` |
  | (trial, no price) | `pro` | Trial | `TRIAL_MAX_ACCOUNTS` (default 1) | `[]` |

  > Note: `plan` reuses the existing CHECK values (`free|pro|enterprise|internal`) per DS2 — the marketing tier is the display label, not a DB value. `enterprise` is the bucket for Team to keep the door open for custom deals.

**Test plan** (`pricing_test.go`, unit, no I/O): known price ID → correct mapping; unknown price ID → `(_, false)`; the trial default mapping is well-formed.

**Effort: S.**

---

## Slice 1 — `processed_stripe_events` idempotency migration + storage (S)

**Goal.** A dedupe table so replayed/retried webhooks are no-ops (security req §3.3). Migration `035` (highest existing is `034`) — **number-collision warning:** the companion [`self-signup-plan.md`](self-signup-plan.md) Slice 6.1 also claims the next free number for `email_verification`. Whichever plan lands second takes the then-next free number; confirm at implementation time.

**Files**
- `services/shared/storage/postgres/migrations/035_stripe_events.up.sql` / `.down.sql` — **new**.
- `services/shared/storage/storage_entitlement.go` — extend the `EntitlementStore` slice with the dedupe methods (interface-first).
- `services/shared/storage/postgres/entitlement.go` (or the existing entitlement impl file) — impl.

**Migration shape** (system-scoped, no RLS, `axiaops_runtime`-only — identical posture to `entitlements`, migration 033, and for the same reason: written only by AxiaOps, read cross-org/pre-auth by the webhook handler which has no org context at parse time):

```sql
SET search_path TO axiaops;

CREATE TABLE IF NOT EXISTS processed_stripe_events (
    event_id     TEXT        PRIMARY KEY,           -- Stripe event.id (evt_...)
    event_type   TEXT        NOT NULL,              -- e.g. customer.subscription.updated
    received_at  TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

-- Retention sweep target: events older than N days are prunable (the dedupe
-- value decays — Stripe does not retry beyond ~3 days). The ingestion daily
-- retention pass can sweep this like cost_records/notification_dispatches.
CREATE INDEX IF NOT EXISTS processed_stripe_events_received_idx
    ON processed_stripe_events (received_at);

GRANT SELECT, INSERT, DELETE ON processed_stripe_events TO axiaops_runtime;
```

- **No grant to `axiaops` app role** — defence-in-depth, exactly as `entitlements` withholds it (migration 033). The `029_runtime_admin_role` per-table bypass-policy loop only touches RLS-enabled tables and correctly skips this one (same as `entitlements`; `TestRuntimeAdmin_PolicyCoversAllRLSTables` asserts only over RLS tables).
- **Down:** `DROP TABLE processed_stripe_events;`.

**additions** (on `EntitlementStore`):
- `MarkStripeEventProcessed(ctx, eventID, eventType string) (firstTime bool, err error)` — `INSERT … ON CONFLICT (event_id) DO NOTHING`; `firstTime` is true iff a row was inserted (use `RowsAffected()`). This is the atomic claim — the handler skips processing when `firstTime == false`.

**Test plan** (`postgres_test.go`, integration — `make test-storage`):
- First insert → `firstTime=true`; second insert same id → `firstTime=false` (the replay guarantee).
- Concurrent same-id inserts (two goroutines) → exactly one `firstTime=true`.
- RLS-bypass: succeeds with no `app.organization_id` set (admin pool).

**Effort: S.**

---

## Slice 2 — Stripe→`BillingEvent` decoder (pure, no I/O) (M)

**Goal.** Translate the Stripe event types we handle into the existing
provider-agnostic `entitlement.BillingEvent` (`billing.go`) — a pure function,
so it is exhaustively table-testable against real fixture JSON with zero network.

**Files**
- `services/api/internal/billing/stripe_decode.go` — **new** (same `billing` package as the Slice-3 handler). `DecodeStripeEvent(event stripe.Event, pricing entitlement.PriceResolver) (entitlement.BillingEvent, bool, error)`. Returns `(evt, handled, err)` — `handled=false` for event types we deliberately ignore (so the handler 200s them without projecting). `PriceResolver` is the Slice-0 `PlanForPrice` seam (interface so the decoder stays free of env reads). **Placement is deliberate:** the decoder takes `stripe.Event`, so wherever it lives drags in the Stripe SDK. `services/shared/` is imported by the ingestion binary too (and shared's charter is "no provider SDKs" — same reason the AWS SDK lives in ingestion, not shared); putting the decoder in the api's `billing` package keeps `stripe-go` out of shared and out of the ingestion image entirely. `pricing.go` (Slice 0) stays in shared — it is pure Go with no Stripe types.
- `services/api/go.mod` — add `github.com/stripe/stripe-go/v82` (the `stripe.Event` / `stripe.Subscription` types; **pin the current major at implementation time** — stripe-go cuts a new major per API version, so verify the latest `/vNN` then). The webhook signature-verification helper (`webhook.ConstructEvent`) also lives in that SDK but is called in the handler (Slice 3), not the decoder.

**Event → BillingEvent mapping** (the order-tolerant projection; the org is found via `client_reference_id` on checkout, then via `billing_customer_ref` on the org's row for subsequent events):

| Stripe event | Resulting `BillingEvent` |
|---|---|
| `checkout.session.completed` | Look up org by `client_reference_id` (the org ID we set at checkout creation — Slice 4). Set `Status=active` (or `trialing` if the subscription is in trial), `Plan`/`MaxAccounts`/`Features` from the line-item price via `PlanForPrice`, `BillingCustomerRef`/`BillingSubscriptionRef`, `CurrentPeriodEnd`. |
| `customer.subscription.updated` | Org found by stored `BillingCustomerRef`. Map Stripe sub status → entitlement status: `active`→`active`, `trialing`→`trialing`, `past_due`→`past_due`, `canceled`→`canceled`, `unpaid`→`suspended`, `incomplete*`→ ignore (not yet paid). Plan/limits from the current price. `CurrentPeriodEnd` from the sub. |
| `customer.subscription.deleted` | `Status=canceled`. Keep `BillingCustomerRef` so a re-subscribe can be matched. |
| anything else | `handled=false` (ignored, handler 200s). |

- **The grace window is NOT decoded here** — `past_due` stays `past_due` and `IsScanAllowed` derives the grace cutoff at read time from `CurrentPeriodEnd + ENTITLEMENT_GRACE_DAYS` (exactly as the existing predicate does). The decoder never computes grace — billing stays the single source of truth for the period end.
- **Org lookup is a Resolver concern, not the decoder's** — the decoder emits the `BillingCustomerRef`/`client_reference_id` it found; the handler (Slice 3) resolves that to an `organization_id` (a store lookup `GetEntitlementByCustomerRef`, added to `EntitlementStore`). This keeps the decoder pure.

**additions** (on `EntitlementStore`, used by the handler):
- `GetEntitlementByCustomerRef(ctx, customerRef string) (model.Entitlement, error)` — for the non-checkout events that carry only the Stripe customer id. `ErrEntitlementNotFound` when no match.

**Test plan** (`stripe_decode_test.go`, unit, **real fixture payloads** captured from `stripe trigger`):
- One golden fixture per handled event type → asserted `BillingEvent` fields (status, plan, max_accounts, refs, period end).
- Trial subscription → `Status=trialing`, `TrialEndsAt` set.
- `unpaid` → `suspended`; `incomplete` → `handled=false`.
- Unknown price ID in the line item → error (fail loud — we never silently entitle an unmapped price).
- Unhandled event type → `handled=false`, no error.

**Effort: M.**

---

## Slice 3 — `POST /v1/webhooks/stripe` handler + raw-body verify + wiring (L)

**Goal.** The single inbound billing endpoint. Verify the Stripe signature over
the **raw** body, dedupe on `event.id`, decode (Slice 2), resolve org, project
via `ApplyBillingEvent`. Order-tolerant, replay-safe, idempotent.

**Files**
- `services/api/internal/billing/webhook.go` — **new package** `billing`. `Handler` struct (store + webhook secret + pricing map + audit writer); `ServeHTTP` / a `handleWebhook(w, r)` method.
- `services/api/internal/middleware/auth.go` — **add a `publicPath` case for `/v1/webhooks/`** (see grounding note 2 at the end of this doc).
- `services/api/internal/serverbuild/build.go` — register the route inside `ComposeServer` only when a billing dep is non-nil (DS6); add the billing dep to `Deps`.
- `services/api/cmd/saasmode_saas.go` — construct the billing handler (Stripe webhook secret + pricing from env) and pass it into `Deps`. `saasmode_selfhosted.go` passes nil → no route.
- `services/api/cmd/main.go` — read `STRIPE_WEBHOOK_SECRET` + `STRIPE_PRICE_*` into config.

**publicPath — REQUIRES A NEW CASE.** Verified in source: `publicPath` (auth.go
line 46) returns true for the infra paths, the SSO ceremony, and
`strings.HasPrefix(p, "/v1/auth/")`. **`/v1/webhooks/stripe` is NOT under
`/v1/auth/` and matches no existing case**, so without a change the auth
middleware would demand a session cookie and 401 every Stripe delivery. Add:

```go
// Billing webhooks are authenticated by the provider's HMAC signature over the
// raw body (verified in the handler via stripe-go webhook.ConstructEvent), NOT
// by a session cookie — Stripe has no cookie. Prefix-match so future providers
// (/v1/webhooks/<provider>) share the carve-out; each handler MUST verify its
// own signature (the bypass only skips the cookie check, it is not "no auth").
case strings.HasPrefix(p, "/v1/webhooks/"):
    return true
```

Put it as a prefix branch (mirroring the existing `/v1/sso/oidc/` branch), and
document that the signature **is** the auth so a reviewer doesn't read the bypass
as "unauthenticated".

**Handler shape** (security reqs §3.1–§3.4 baked in):
1. **Read the raw body** with a `http.MaxBytesReader(w, r.Body, 64<<10)` cap (Stripe events are small; the 64 KiB cap from H-4 stays). **Do NOT use `httpjson`** — security req §3.2: `DisallowUnknownFields` would 400 on Stripe payloads that gain fields without notice. This is the documented single exception to the H-4 decoder posture; every *other* billing endpoint (Slices 4, 5) uses `httpjson` normally.
2. `event, err := webhook.ConstructEvent(rawBody, r.Header.Get("Stripe-Signature"), h.webhookSecret)` — verifies HMAC + a bounded replay tolerance (stripe-go's default). On error → 400, no processing, increment `axiaops_billing_webhook_total{result="bad_signature"}`.
3. `firstTime, err := h.store.MarkStripeEventProcessed(ctx, event.ID, event.Type)` (Slice 1). `firstTime==false` → 200 immediately (replay/retry no-op). DB error → 500 so Stripe retries.
4. `be, handled, err := DecodeStripeEvent(event, h.pricing)` (Slice 2 — same `billing` package). `handled==false` → 200 (ignored type). Decode error → 500 (retryable) and log.
5. Resolve org id: checkout events carry `client_reference_id`; others use `GetEntitlementByCustomerRef`. No org found → log + 200 (don't wedge Stripe's retry queue on an orphan event; alert via metric).
6. `entitlement.ApplyBillingEvent(ctx, h.store, be)` — the existing idempotent, order-tolerant projection (keyed on `organization_id`). Error → 500 (retryable).
7. **Audit** the projection (Slice 9 fills the actor-less audit seam): `subscription.created/updated/canceled`, `payment.failed`. 200 on success.

**Order-tolerance / replay** come free: the upsert is keyed on org, so a late
`subscription.updated` arriving after `subscription.deleted` simply overwrites to
the latest decoded state; and `MarkStripeEventProcessed` makes any single event
exactly-once. A genuinely out-of-order pair (update older than the row) is the one
residual — acceptable for launch; note it in risks.

**Endpoint-table addition** (`services/api/CLAUDE.md`):

| Method | Path | Auth | Purpose |
|---|---|---|---|
| POST | /webhooks/stripe | Stripe signature (no cookie) | Inbound billing events. Raw-body HMAC verify (`webhook.ConstructEvent`), `event.id` dedupe, decode → `ApplyBillingEvent` into `entitlements`. Idempotent, order-tolerant, replay-safe. **Not registered in `-tags selfhosted`.** Bypasses cookie auth via the `/v1/webhooks/` `publicPath` case; the signature is the auth. |

**Test plan** (`webhook_test.go`, black-box, `httptest`, mock `EntitlementStore`):
- Valid signature + real fixture → `ApplyBillingEvent` invoked with the decoded event; 200.
- **Bad/absent signature → 400, store NOT touched** (construct an event with the wrong secret).
- **Replay:** same `event.id` twice → second call short-circuits (`MarkStripeEventProcessed` returns `firstTime=false`), `ApplyBillingEvent` invoked exactly once; both 200.
- Unhandled event type → 200, no projection.
- Orphan customer ref → 200, projection skipped, metric bumped.
- Signature verification uses a test webhook secret + `webhook.GenerateTestSignedPayload` (stripe-go helper) — **no real network, no live keys**.

**Effort: L** (new package, raw-body + signature path, the httpjson exception, publicPath change, wiring across three composition files).

---

## Slice 4 — Checkout session: `POST /v1/billing/checkout-session` (M)

**Goal.** An authenticated, owner-only, rate-limited POST that creates a Stripe
Checkout session (hosted page → SAQ-A) and returns its URL. Success/cancel return
URLs land on dashboard routes.

**Files**
- `services/api/internal/billing/checkout.go` — `checkoutSession(w, r)` handler method (same `billing.Handler` as Slice 3, or a sibling — share the Stripe client + store).
- `services/api/internal/serverbuild/build.go` — register `POST /v1/billing/checkout-session` (authenticated route, owner-only) when billing dep present.
- `services/api/cmd/saasmode_saas.go` / `main.go` — read the **restricted session-creation key** (`STRIPE_SECRET_KEY`, restricted per DS8) + price IDs.
- `services/dashboard/src/screens/BillingScreen.jsx` (Slice 8) consumes it; success/cancel routes added there.

**Handler shape:**
- **Owner-only.** Reuse the existing authz tier check (the same `authz.Perm*` pattern the account/membership handlers use — billing changes are an owner action, mirroring `organization:delete`). Non-owner → 403.
- **Rate-limit** (security req §3.8): reuse `auth.IPRateLimiter` with a new key-prefix `"billing:checkout"` (the same reuse pattern `self-signup-plan.md` Slice 3 uses for `"auth:register"`, and `"auth:bootstrap_probe"` / `"auth:sso_discover"` use today). **Per-IP budget** — `IPRateLimiter.Allow(ctx, ip)` keys on IP only; there is no per-org limiter today, and owner-only authz already bounds the per-org surface (one owner per org), so per-IP is the launch posture. A dedicated per-org budget would need a new limiter type — deferred. 429 before any Stripe call; checkout-session creation is a Stripe-cost amplifier if abused.
- Decode body `{price_id}` with `httpjson` (normal posture — only the webhook is excepted). Validate `price_id` is one of the configured prices via `PlanForPrice` → 400 on unknown.
- Resolve or create the Stripe **Customer** for the org: if the org's `entitlements.billing_customer_ref` is set, reuse it; else create a Customer (email = owner, metadata `organization_id`) and store the ref. (The ref is also re-affirmed by the webhook — DS4 — so a race just converges.)
- Create the Checkout Session: `mode=subscription`, `line_items=[{price_id, qty 1}]`, `client_reference_id = organization_id` (the webhook's org anchor — DS4; read back by the Slice-2 decoder), `customer = <ref>`, `success_url = PUBLIC_HOST + "/billing?checkout=success"`, `cancel_url = PUBLIC_HOST + "/billing?checkout=cancel"`, Stripe Tax on.
- **No entitlement write here** (DS4) — return `{url}` (200); the dashboard redirects the browser to Stripe's hosted page.
- Audit `billing.checkout_started` (Slice 9).

**Endpoint-table addition:**

| Method | Path | Auth | Purpose |
|---|---|---|---|
| POST | /billing/checkout-session | Yes (owner-only, rate-limited) | Create a Stripe Checkout session for `{price_id}`; returns `{url}` to redirect to the hosted page (SAQ-A). No entitlement write — the webhook projects on completion. **Not registered in `-tags selfhosted`.** |

**Test plan** (`checkout_test.go`, black-box, mock store + **fake Stripe client behind an interface**):
- Owner + valid price → 200 `{url}`, fake Stripe client `CreateSession` called with `client_reference_id == org`, `success_url`/`cancel_url` from `PUBLIC_HOST`.
- Non-owner → 403, Stripe NOT called.
- Unknown price_id → 400, Stripe NOT called.
- Rate-limit cap exceeded → 429 before any Stripe call (assert fake not invoked).
- Existing `billing_customer_ref` → reused (no new Customer create).
- The Stripe client is an interface (`StripeSessions`) so tests inject a fake — **no real network, no live keys.**

**Effort: M.**

---

## Slice 5 — Customer portal session: `POST /v1/billing/portal-session` (S)

**Goal.** Owner-only POST returning a Stripe Billing Portal URL (manage payment
method / cancel / view invoices), so AxiaOps writes no billing-management UI.

**Files**
- `services/api/internal/billing/portal.go` — `portalSession(w, r)` on `billing.Handler`.
- `services/api/internal/serverbuild/build.go` — register `POST /v1/billing/portal-session` (owner-only) when billing present.

**Handler shape:**
- Owner-only (same check as Slice 4). 403 otherwise.
- 409 `no_subscription` if the org has no `billing_customer_ref` (nothing to manage — they must check out first).
- Create a Billing Portal session for the stored customer ref, `return_url = PUBLIC_HOST + "/billing"`. Return `{url}` (200).
- Audit `billing.portal_opened` (Slice 9).
- Cancellation flows entirely through the portal → fires `customer.subscription.deleted` → the webhook (Slice 3) flips `status=canceled`. **No cancel endpoint of our own** (DS — keep the cancel surface in Stripe's hosted UI; CSRF/forged-cancel risk lives where Stripe handles it).

**Endpoint-table addition:**

| Method | Path | Auth | Purpose |
|---|---|---|---|
| POST | /billing/portal-session | Yes (owner-only) | Stripe Billing Portal URL for the org's customer (manage card / cancel / invoices). 409 `no_subscription` when no customer ref. **Not registered in `-tags selfhosted`.** |

**Test plan** (`portal_test.go`): owner + customer ref → 200 `{url}` (fake portal client called with the ref + return_url); no customer ref → 409; non-owner → 403, Stripe NOT called.

**Effort: S.**

---

## Slice 6 — Self-signup trial entitlement + trial-expiry sweep (M)

**Goal.** Flip the self-signup org-create default from `active`/`internal` to a
`trialing` entitlement (DS3), and add a ticker that expires lapsed trials to
`canceled` so the scan-gate stops new scans (reads stay open).

**Files**
- `services/shared/storage/postgres/postgres.go` (where `ensureDefaultEntitlement` lives) + `native_auth.go` (where the signup transaction `RegisterSelfService` lives) — add a `trialing` variant for the **signup** chokepoint only (DS7). The other chokepoints (`UpsertOrganization`/`EnsureOrganization` via the helper; `ConsumeBootstrapState`'s inline INSERT) keep `internal`/`active`.
- The self-signup handler (`self-signup-plan.md` Slice 1's `RegisterSelfService`) calls the trial variant. The two plans meet here: `RegisterSelfService` is the chokepoint; this slice changes the entitlement it writes.
- `services/ingestion/cmd/main.go` (or the api's `StartTickers` — wherever the existing daily retention sweeps run) — add a trial-expiry sweep.
- `services/shared/storage/storage_entitlement.go` + impl — `ExpireLapsedTrials(ctx, now) (int, error)`.

**additions:**
- Trial entitlement: `plan='pro'`, `status='trialing'`, `max_accounts = TRIAL_MAX_ACCOUNTS` (default 1), `trial_ends_at = now + TRIAL_DAYS` (default 14), no billing refs. `IsScanAllowed` already returns true for `trialing` — **zero gate change**.
- `ExpireLapsedTrials`: `UPDATE entitlements SET status='canceled', updated_at=NOW() WHERE status='trialing' AND trial_ends_at < $now AND billing_subscription_ref IS NULL` — only un-converted trials; a trial that became a paid sub (webhook set `active` + a sub ref) is untouched. Cross-org, admin pool. Runs in the existing daily-sweep pass (alongside the cost_records / notification_dispatches retention sweeps in ingestion).
- **Conversion is webhook-driven** (DS4): when a trialing org checks out, `checkout.session.completed` projects `active` with a sub ref, so the sweep skips it.

**Coexistence with the companion plan:** `self-signup-plan.md` ships the signup
*funnel*; this slice changes the *entitlement* that funnel mints. If self-signup
ships first, the signup default is `internal`/`active` (today's behaviour) until
this slice flips it — a safe intermediate state (orgs are over-entitled, not
under). The flip MUST land **after** Slice 4 (Checkout) so an expiring trial has a
pay path. **Test-breakage warning:** `self-signup-plan.md` Slice 1's integration
test pins the default row as `plan='internal'`, `status='active'` — this slice
deliberately breaks that assertion; update it to `trialing` + `trial_ends_at` in
the same MR that flips the default.

**Test plan:**
- Storage (integration): signup chokepoint writes `trialing` + `trial_ends_at`; bootstrap chokepoint still writes `internal`/`active` (DS7 regression).
- `ExpireLapsedTrials`: a past-due trial with no sub ref → `canceled`; a trial that converted (`active` + sub ref) → untouched; an in-window trial → untouched. Count returned.
- Gate behaviour (handler test): `trialing` → scan allowed; post-expiry `canceled` → scan gated, reads still 200 (the existing scangate test matrix already covers `canceled` — extend with the trial-origin case).

**Effort: M.**

---

## Slice 7 — `max_accounts` enforcement at connect-time (S)

**Goal.** Enforce the one hard plan limit at launch (DS5): `POST /v1/accounts`
refuses to connect beyond the org's `max_accounts`.

**Files**
- `services/api/internal/api/handler.go` — in the `POST /accounts` (connect) handler, before the insert: look up the org's entitlement (`GetEntitlement`) + count current accounts (`ListAccounts`); if `count >= entitlement.MaxAccounts` → 402 `plan_limit_reached` with `{limit, current, upgrade_required:true}`.
- Reuse the `entitlement.Resolver` already on the handler (`h.entitlementResolver`) — it's already wired in the SaaS build. **Selfhosted (nil resolver) → no limit** (the license `max_organizations` is conceptually separate; account-count limiting is a SaaS plan concern, so a nil resolver simply skips the check — consistent with DS6).

**additions:**
- Error shape `402 plan_limit_reached` so the dashboard can route to the upgrade CTA (Slice 8) rather than show a generic error.
- A small `accountLimitOK(ctx, resolver, org, currentCount)` helper next to `scangate.go`'s `gateAllowsScan` (same pattern: nil resolver → allow).

**Test plan** (`handler_test.go`): under-limit connect → 200; at-limit connect → 402 `plan_limit_reached` with the limit/current numbers; **nil resolver (selfhosted) → no limit, connect 200** regardless of count.

**Effort: S.**

---

## Slice 8 — Dashboard billing/plan page (#131) (L)

**Goal.** The customer-facing plan/usage page that **replaces the hidden License
page** under SaaS: current plan, status, renewal/trial date, usage vs limit,
upgrade CTA → Checkout, manage → Portal. The word "license" never appears.

**Files**
- `services/api/internal/api/handler.go` — `GET /v1/billing/entitlement` (authenticated, any role): returns `{plan_display, status, trial_ends_at, current_period_end, max_accounts, accounts_used, manage_available}` derived from the org's `entitlements` row + account count. **Never returns billing refs or any token** (design §7.4/§7.5 — customer sees plan + usage, never an internal handle).
- `services/dashboard/src/screens/BillingScreen.jsx` — **new**, route `/billing`. Renders current plan + status + renewal date + usage bar; "Upgrade"/"Choose plan" → POST `/v1/billing/checkout-session` then `window.location = url`; "Manage billing" → POST `/v1/billing/portal-session` then redirect. Handles the `?checkout=success` return by polling `GET /v1/billing/entitlement` until status flips (DS4 — entitlement is webhook-driven, so the success page waits for the projection).
- `services/dashboard/src/pages/settings/License.jsx` + `components/LicenseBanner.jsx` + `utils/license.js` — **already hidden under SaaS** (the `managed` `/v1/version` state collapses them, per Tasks.md 2.7.5a + `services/api/CLAUDE.md` `/version` row). #131 is the *replacement*: wire the Settings nav to `BillingScreen` instead of the hidden `License` page when `/v1/version` reports `state:"managed"`. Leave `License.jsx` intact for the selfhosted build (it shows the real license there).
- `services/dashboard/src/api/client.js` — `getEntitlement()`, `createCheckoutSession(priceID)`, `createPortalSession()`.

**Frontend gating:** the billing nav + screen show only when `/v1/version`
`license.state == "managed"` (the SaaS posture) — selfhosted keeps the License
page. No new build flag; reuse the existing `managed`-state branch the dashboard
already has for hiding the License banner.

**Test plan** (Vitest + RTL, `BillingScreen.test.jsx`, mock fetch):
- Renders plan/status/renewal from a mocked `/v1/billing/entitlement`.
- Upgrade click → posts checkout-session, redirects to returned URL.
- Manage click → posts portal-session, redirects.
- `?checkout=success` → polls entitlement, shows "active" once status flips.
- Trial state → shows "Trial — N days left" + a prominent upgrade CTA.
- License nav shows BillingScreen when `state:"managed"`; License page when not (selfhosted).

**Effort: L.**

---

## Slice 9 — Audit events + internal-ops notification seam (S)

**Goal.** Audit every billing state change with actor attribution, and stub the
internal-ops notification hook (signup / payment-failed) per
[`saas-platform-admin-design.md`](saas-platform-admin-design.md) §6 — **the
dispatch itself may defer; the seam ships now.**

**Files**
- `services/shared/model/audit.go` — new `AuditAction*` constants: `billing.checkout_started`, `billing.portal_opened`, `billing.subscription_activated`, `billing.subscription_updated`, `billing.subscription_canceled`, `billing.payment_failed`.
- `services/api/internal/billing/webhook.go` — emit the subscription/payment audit events. **The webhook has no session actor** — Stripe is the actor. The existing `audit.Record` helper enriches from the *request* (org id from context, user id from cookie), which a webhook lacks. So the webhook uses a lower-level audit write: `store.AuditLogWrite` directly with `actor = "stripe:webhook"` (or `system`), `organization_id` set explicitly from the resolved org — and the caller MUST wrap the context first (`storage.WithOrganizationID(ctx, resolvedOrgID)`): `AuditLogWrite` runs on the RLS-enforced app pool and reads the org from context, so an unwrapped call fails with "organization_id missing from context". This is exactly the "record an actor that is not a member of the org" gap noted in `saas-platform-admin-design.md` §8 — the billing webhook is its first real caller; if `audit_log.actor` is non-nullable, this slice may need a small migration to allow a system actor (verify against the `028_audit_actor_name` shape before coding).
- `services/api/internal/billing/checkout.go` / `portal.go` — these DO have a session actor (owner), so they use the normal `audit.Record(r, store, …)` helper.
- **Internal-ops notification seam:** add a nil-tolerant `notify func(event)` hook on `billing.Handler` (called after a successful `payment_failed` / new-signup projection). v1 wires it to nil (deferred) or a log-only sink; the real `system_notification_channels` dispatcher is `saas-platform-admin-design.md` §6 future work. Leave a `// TODO §6 internal-ops` seam — same posture as `self-signup-plan.md`'s deferred-but-seamed hooks.

**Test plan:** webhook test asserts `AuditLogWrite` called with the right action + `organization_id` + system actor on subscription activation/cancel/payment-failed; checkout/portal tests assert `audit.Record` invoked with the owner actor. The `notify` hook is asserted called once on `payment_failed` (with a fake sink).

**Effort: S** (assuming `audit_log.actor` already tolerates a non-member system actor; +S if a migration `036` is needed for that — split it out then).

---

## Env vars (api — `services/api/CLAUDE.md` style)

| Variable | Required | Default | Notes |
|----------|----------|---------|-------|
| STRIPE_SECRET_KEY | When billing is wired (SaaS, non-DEV) | — | **Restricted** key (DS8) scoped to Checkout + Billing Portal + Customers — used only by the checkout/portal session handlers. Live vs test per env. SSM/CI variable, never compose files (C-2). Unset → billing routes not constructed; the SaaS build runs without a pay path (the trial still works). |
| STRIPE_WEBHOOK_SECRET | When `/v1/webhooks/stripe` is wired | — | Signing secret for `webhook.ConstructEvent` over the raw body (security §3.1). One per env (Stripe issues a distinct secret per webhook endpoint). Unset → webhook route not registered. |
| STRIPE_PRICE_STARTER | When billing is wired | — | Stripe Price ID → `pro` / Starter / `max_accounts=1` (Slice 0 map). Per-env (test vs live IDs differ). |
| STRIPE_PRICE_GROWTH | When billing is wired | — | Price ID → `pro` / Growth / `max_accounts=5`. |
| STRIPE_PRICE_TEAM | When billing is wired | — | Price ID → `enterprise` / Team / `max_accounts=20`. |
| TRIAL_DAYS | No | 14 | Self-signup reverse-trial length (DS3). Trial entitlement gets `trial_ends_at = now + this`. |
| TRIAL_MAX_ACCOUNTS | No | 1 | `max_accounts` granted to a `trialing` org before they subscribe. |
| ENTITLEMENT_GRACE_DAYS | No | 21 | **Already exists** (SaaS build). The `past_due` dunning window past `current_period_end` before scans gate. Keep aligned with Stripe Smart Retries (~21d, DS1) so the app never suspends before Stripe finishes retrying. |
| PUBLIC_HOST | No | — | **Already exists.** Reused to build Checkout `success_url`/`cancel_url` and the Portal `return_url`. Empty → relative URLs the dashboard resolves; for billing, set it (the redirect leaves the SPA, so an absolute origin is needed). |

> No `VITE_*` billing flag is needed: the dashboard shows the billing nav off the
> existing `/v1/version` `state:"managed"` branch (Slice 8), so SaaS-vs-selfhosted
> is already distinguished without a new build var.

---

## Self-hosted SKU non-impact (DS6) — resolved

- **Billing is inert in `-tags selfhosted` via the existing `saasmode_*.go` seam.** The billing handler is constructed in `saasmode_saas.go`'s wiring and threaded through `Deps`; `saasmode_selfhosted.go` supplies nil, and `ComposeServer` registers `/v1/webhooks/stripe`, `/v1/billing/checkout-session`, and `/v1/billing/portal-session` **only when that dep is non-nil** — exactly how `EntitlementResolver` already flips license-vs-entitlement at the same seam (`entitlementGate(store)` returns `(store, grace)` in saas, `(nil, 0)` in selfhosted).
- **The license gate is untouched.** Selfhosted keeps `entitlementResolver == nil` → `gateAllowsScan` takes the license path (`scangate.go`); no entitlement, no billing, no trial. The License page/banner stay live there (the `managed`-state branch is false).
- **`max_accounts` enforcement (Slice 7) is nil-resolver-skipped** — selfhosted connects accounts with no plan cap (its limit is the license `max_organizations`, a separate concern).
- **The `stripe-go` SDK is confined to the api module** (`services/api/internal/billing/`, Slice 2 placement) — it never enters `services/shared/` or the ingestion binary. Within the api, it compiles into both the SaaS and `-tags selfhosted` binaries (Go can't tag-strip an import at package granularity), but no selfhosted code path *calls* it — the handler that imports it is never constructed. This is the accepted cost (a few hundred KB of unused SDK in the selfhosted api image); it carries no runtime behaviour. If binary size ever matters, the billing package can move behind a build-tagged file pair like `saasmode_*.go`, but that is premature now.
- **Recommendation:** stay with the **build-tag seam (DS6)**, not a `SIGNUP_ENABLED`-style runtime flag. Billing is a SKU-axis concern (like the license gate it parallels), and the SKU axis is already the `selfhosted` tag. A runtime flag would let a misconfigured selfhosted env accidentally enable a Stripe loop it has no keys for; the tag makes "selfhosted has no billing" a compile-time guarantee.

---

## Risks & edge cases

- **Webhook ordering.** `MarkStripeEventProcessed` gives exactly-once per event; the org-keyed upsert gives last-writer-wins. The residual is a genuinely out-of-order pair where an *older* `subscription.updated` lands after a newer one — it would briefly regress the row. Acceptable for launch (Stripe rarely reorders within seconds; the next event reconciles). A `updated_at`/event-timestamp guard on the upsert is the hardening, deferred.
- **Checkout success without webhook (network/Stripe lag).** DS4 means the success page shows "activating…" and polls `/v1/billing/entitlement`. If the webhook is delayed minutes, the customer sees a pending state — annoying, not broken. Mitigate with a short Stripe retry + a "refresh" affordance; do NOT write entitlement from the success redirect (security §3.4).
- **Orphan webhook (no matching org).** A `customer.subscription.*` whose customer ref matches no org → 200 + metric + log (don't 500-loop Stripe's retry queue on a permanently-unmatchable event). Happens if a Customer is created out-of-band; alert on the metric.
- **`httpjson` exception is load-bearing and easy to regress.** The webhook MUST NOT use `httpjson` (it would 400 on Stripe's evolving payloads — security §3.2). A reviewer "fixing" the inconsistency would break billing silently. Comment it loudly at the handler + in `services/shared/CLAUDE.md`'s decoder note.
- **Restricted-key blast radius.** The session-creation key (DS8) is restricted; if leaked it can create Checkout/Portal sessions but not move money or read full account data. The webhook secret only verifies. Neither is the full secret key. Still SSM/CI-only (C-2).
- **Trial abuse (sign up repeatedly for fresh trials).** The signup funnel's CAPTCHA + per-IP rate-limit (`self-signup-plan.md` Slices 3/5) is the bot wall; `users_email_lower_unique` stops the same email re-trialing. Multi-email trial farming is a known PLG cost, accepted for the beta; the internal-ops "new signup" notification (Slice 9 seam) gives early visibility.
- **CSRF on checkout/portal.** Both are owner-only authenticated POSTs that move toward money — exactly the M-8 surface `security-before-billing-plan.md` §2 fixes (origin-bound `X-CSRF-Token`) **as a hard prerequisite**. These slices assume that CSRF system is already in place (security pass ships first). Do NOT ship billing endpoints before M-8.
- **Audit actor for webhook events.** The webhook has no member actor; if `audit_log.actor` is non-nullable this needs a small migration (Slice 9 note) — verify `028_audit_actor_name` before coding, split a `036` migration only if required.
- **Pricing drift.** Prices live in Stripe + the env-injected price-ID map; a Stripe price renamed/re-created changes its ID → update the env, not code (DS2). A stale `STRIPE_PRICE_*` → `PlanForPrice` returns `(_, false)` → checkout 400 / webhook decode error (fail loud, never silently entitle).

---

## Effort & cut-line summary

| Slice | Effort | Critical path? | Cuttable for internal dry-run? |
|---|---|---|---|
| 0 — Object model + price map | S | Yes (design) | No |
| 1 — `processed_stripe_events` migration + storage | S | Yes | No (idempotency is load-bearing) |
| 2 — Stripe→BillingEvent decoder (pure) | M | Yes | No |
| 3 — Webhook handler + raw-body verify + publicPath | L | Yes | No |
| 4 — Checkout session (owner-only, rate-limited) | M | Yes | No |
| 5 — Customer portal session | S | No | Defer to post-dry-run |
| 6 — Self-signup trial + expiry sweep | M | No (after Slice 4) | Defer (default stays `internal`/`active` until flipped) |
| 7 — `max_accounts` enforcement | S | No | Defer (no limit until shipped) |
| 8 — Dashboard billing/plan page (#131) | L | No (API works headless) | Defer for dry-run; **required for public launch** |
| 9 — Audit + internal-ops seam | S | No | Audit keep; notify-dispatch defer |

**Minimum to charge a customer (internal dry-run, test-mode keys):** Slices
**1–4**. **Minimum responsible public launch:** 1–9 + the
[`security-before-billing-plan.md`](security-before-billing-plan.md) §2 prereqs
(CSRF/M-8 especially) **before** these slices, and the §4 external pen-test
**after** these slices and **before** the first live invoice.

---

## Sequencing (the full gate)

```
security pass (security-before-billing-plan §2: CSRF/M-8, users-RLS/H-1, metrics-gate/H-2, …)
   │  HARD PREREQUISITE — billing POSTs move money; ship CSRF first
   ▼
Stripe build (Slices 0–9, security §3 reqs baked in: raw-body verify, no-httpjson-on-webhook,
              event.id idempotency, webhook-only entitlement writes, restricted keys, SAQ-A,
              audit, rate-limited checkout)
   │
   ▼
external pen-test (security-before-billing-plan §4 — one spend, covers the paid surface)
   │
   ▼
first live invoice
```

---

## Testing strategy

- **Unit, no network (the default posture):** the decoder (Slice 2) and pricing map (Slice 0) are pure functions tested against **real captured fixture payloads** (`stripe trigger <event> --print` checked into `testdata/`). The webhook handler (Slice 3) signs test payloads with a test secret via `webhook.GenerateTestSignedPayload` — no live keys, no network. Checkout/portal handlers (Slices 4/5) inject a **fake Stripe client behind a `StripeSessions`/`StripePortal` interface** so no SDK call leaves the process. This matches the repo convention (mock external SDKs, `httptest.NewRecorder`, no real network in unit tests).
- **Integration (`make test-storage`):** the `035` migration + `MarkStripeEventProcessed` dedupe (concurrent-winner test), `GetEntitlementByCustomerRef`, the trial-default + `ExpireLapsedTrials` storage paths — all on the admin pool, RLS-bypass asserted.
- **Local e2e (manual, opt-in):** `stripe listen --forward-to localhost:8080/v1/webhooks/stripe` + `stripe trigger checkout.session.completed` against `make start-staging` with test-mode keys — exercises the real signature path + the full project-into-`entitlements` loop end-to-end without a real card (Stripe test cards). Documented in a runbook; **not** in CI (no live secrets in CI).
- **Gate regression:** extend the existing `scangate` handler matrix (`handler_entitlement_gate_test.go` — 7 cases today: the 5 entitlement statuses plus 2 storage-error cases, with `past_due` covered only *inside* grace) with (a) the trial-origin `canceled` case (Slice 6) and (b) the currently-missing **`past_due` past the grace window → blocked** case, and assert reads stay 200 while scans gate.

---

## Explicitly deferred (out of scope for this build — one-line why)

- **Feature gating on `features[]`** (PDF reports, remediation tracking paywalled) — `features[]` is wired but no feature is gated v1 (DS5); adding a gate later is a predicate, not a migration.
- **Annual / multi-currency / per-seat pricing** — launch is monthly EUR per-org (DS1/DS2); Stripe supports the rest without a code change when demand appears.
- **Proration / mid-cycle plan-switch UX in-app** — handled entirely by the Stripe Customer Portal (Slice 5); no in-app plan-switcher.
- **Dunning emails from AxiaOps** — Stripe Smart Retries + Stripe's own dunning emails own this (DS1); the internal-ops "payment_failed" notify (Slice 9) is staff-facing, not customer-facing.
- **Paddle / merchant-of-record migration** — DS1 chose Stripe + Stripe Tax; the `BillingEvent` seam keeps a later swap to the decoder only (ADR-0002 open follow-up).
- **Real internal-ops notification dispatch** (`system_notification_channels`) — `saas-platform-admin-design.md` §6 future work; this build ships the nil-tolerant seam (Slice 9), not the dispatcher.
- **Out-of-order webhook timestamp guard** — last-writer-wins is accepted for launch; an event-timestamp guard on the upsert is the hardening (risks §1).
- **Self-hosted billing** — selfhosted keeps the license gate, no billing, by construction (DS6); there is no self-hosted Stripe path and never will be in one binary.
- **Cancellation-triggered org/data deletion** (stale `Tasks.md` line 482) — canceled orgs keep their data (graceful degradation, design §7.2); GDPR deletion is the separate `/v1/organizations/me` path, not a billing side-effect.

---

## Grounding notes (verified in source)

1. **Highest migration is `034`** (`034_entitlement_internal_plan`), so this plan's idempotency table is `035` and any audit-actor fix the next number after it — **but** `self-signup-plan.md` Slice 6.1 also claims the next free number (`email_verification`); whichever plan's migration lands second renumbers. All migration numbers in both plans are "next free at implementation time", not reservations.
2. **`publicPath` does NOT cover `/v1/webhooks/`** — verified at `services/api/internal/middleware/auth.go` line 46: it matches infra paths, `/v1/sso/oidc/` ceremony, and `strings.HasPrefix(p, "/v1/auth/")`. `/v1/webhooks/stripe` matches none, so Slice 3 MUST add a `/v1/webhooks/` prefix case or every Stripe delivery 401s.
3. **`entitlements` is system-scoped, no RLS, `axiaops_runtime`-only** (migration 033, lines 15–30 + the `GRANT … TO axiaops_runtime` at line 70). `processed_stripe_events` (Slice 1) copies that posture exactly and for the same reason (webhook has no org context at parse time).
4. **The `ApplyBillingEvent` projection already exists and is already idempotent + order-tolerant** (`services/shared/entitlement/billing.go` — keyed on the migration-033 `organization_id` UNIQUE). This plan only adds the Stripe→`BillingEvent` decoder (Slice 2) and the HTTP shell (Slice 3) in front of it; the gate (`entitlement.IsScanAllowed` / `gateAllowsScan` in `services/api/internal/api/scangate.go`) is untouched.
5. **The build-tag seam already flips SaaS-vs-selfhosted at `saasmode_saas.go` / `saasmode_selfhosted.go`** via `entitlementGate(store) → (store, grace)` vs `(nil, 0)` and the `Deps.EntitlementResolver` field. Billing rides the identical seam (DS6) — `saasmode_saas.go` constructs the billing handler, `saasmode_selfhosted.go` passes nil, `ComposeServer` registers billing routes only when non-nil.
6. **`auth.IPRateLimiter` takes a key-prefix and is reused per-surface** (`"auth:bootstrap_probe"` and `"auth:sso_discover"` today — see `serverbuild/build.go`; `"auth:register"` planned) — Slice 4 reuses it with `"billing:checkout"`, no new limiter type. Note it is **per-IP only** (`Allow(ctx context.Context, ip net.IP)` — `ip` is a `net.IP`, not a string; the handler parses the remote address the way the login rate-limit path already does); Slice 4's budget is per-IP, not per-org.
7. **The audit helper `audit.Record(r, w, e)` enriches from the *request*** (`services/api/internal/audit/audit.go` — org id + user id from request context); the webhook has neither, so Slice 9 writes via the lower-level `store.AuditLogWrite` with an explicit org + system actor — the first real caller of the "non-member actor" gap noted in `saas-platform-admin-design.md` §8.
