# Pre-Billing Security Pass — Plan

- **Date:** 2026-06-11
- **Status:** proposed
- **Owner:** Ahmed
- **Context:** [ADR-0002](decisions/0002-saas-first-for-awareness.md) (accepted 2026-06-11) makes
  Stripe billing the next major build. This doc sequences the residual security work from the
  2026-05-09 audit *before* that build, and pins the security requirements the billing surface
  must ship with.
- **Trackers:** issue [#94](https://gitlab.com/axiaops/axiaops/-/work_items/94) (audit remainder),
  issue [#95](https://gitlab.com/axiaops/axiaops/-/issues/95) (Kinde residue), `Tasks.md` §2.7.23
- **Companion docs:** [`security-audit-2026-05-09.md`](security-audit-2026-05-09.md),
  [`saas-platform-admin-design.md`](saas-platform-admin-design.md) §7 (entitlement projection)

---

## 1. Why a pass now (and why scoped)

The May audit is mostly closed: all 3 Criticals, H-3/H-4/H-5, M-1..M-7, M-9, and Redis
`requirepass` are shipped (see the resolution table in the audit doc and §2.7.23). What remains
open in #94 is small — but billing changes the threat profile in three ways:

1. **State-changing requests start moving money.** Checkout-session and portal-session creation,
   plan changes, and cancellation are authenticated POSTs where forged cross-site requests have
   financial consequences. CSRF defence today is SameSite=Lax only (M-8).
2. **Cross-org leakage gets payment context.** Billing links organizations to Stripe customer
   IDs and subscription state. The `users` table still has no RLS policy (H-1).
3. **Paying customers become an enumeration target.** Per-org labels on an unauthenticated
   `/metrics` endpoint (H-2) matter more when the org list is a customer list.

A **full re-audit is not warranted** — the audit already enumerated the work; this is execution
of the remainder whose risk billing amplifies, plus billing-specific requirements designed in
from day one. The external validation step is the pen-test in §4, scheduled once so it covers
the billing surface.

## 2. Scope — close before Stripe work starts

| Item | Finding | Work | Estimate |
|---|---|---|---|
| CSRF token system | M-8 | Origin-bound `X-CSRF-Token` + second non-HttpOnly cookie issued at session-mint; enforced on state-changing routes. **Hard prerequisite for billing endpoints.** | ~1–2d |
| `users` table RLS | H-1 | Prerequisite app-pool refactor (`GetUserByID` / `UpsertUser` / `EnsureUser` in `services/shared/storage/postgres/postgres.go`), then policy migration mirroring `memberships`. | ~1d |
| `/metrics` auth-gate | H-2 | External exposure already closed at nginx (2.7.18 `return 404`); decide bearer-token (`METRICS_BEARER_TOKEN`) vs separate `:9090` listener and implement. | ~½d |
| SSRF guard on `oidc_discovery_url` | #94 addendum | H-3 enforces scheme but not destination IP range; reject private/link-local ranges (with test override). | ~½d |
| Kinde residue (code/env) | #95 | `.env.example` `KINDE_*` vars, `scripts/start.sh` `VITE_KINDE_*` exports, `deploy/README.md` required-vars list, remaining unstamped docs. Docs-side residue in compliance docs already cleaned 2026-06-11. | ~½d |

**Total: ~3–4 days.**

Explicitly **out of scope**: M-10 audit-log hash chain (deferred by the audit itself until
SOC 2/ISO becomes a customer ask), L-1..L-9 (cosmetic or paired with H-2), and any new
audit sweep.

## 3. Billing-surface security requirements (design-in, not retrofit)

1. **Webhook signature verification** via the official `stripe-go` SDK
   (`webhook.ConstructEvent` over the **raw** request body) with a bounded replay tolerance.
2. **Decoder exception — do not use `httpjson` on the webhook route.** H-4's
   `DisallowUnknownFields` posture will 400 on Stripe payloads, which gain fields without
   notice. The webhook handler parses with the SDK; the 64 KiB body cap stays (Stripe events
   are small). Every *other* billing endpoint uses `httpjson` as normal.
3. **Idempotent event processing.** Stripe retries webhooks; dedupe on `event.id` (processed-
   events table or unique constraint) so replays and retries are no-ops.
4. **Entitlements derive only from webhook-driven DB state** (the `entitlements` projection per
   `saas-platform-admin-design.md` §7). Never from client-supplied plan claims; the license JWT
   stays dormant under SaaS per ADR-0002 commitment #2.
5. **Restricted API keys.** Separate live/test keys, least-privilege restricted keys (no raw
   secret key in services that only create checkout sessions), stored in Secrets Manager /
   GitLab CI variables — never in compose files (C-2 lesson).
6. **No card data on our side.** Stripe Checkout hosted pages keep AxiaOps at SAQ-A; we store
   Stripe customer/subscription IDs only.
7. **Audit-log billing events** (subscription created/changed/cancelled, payment failed) through
   the existing audit trail, with actor attribution.
8. **Rate-limit checkout-session creation** per org/IP (reuse `auth.IPRateLimiter`) — it is an
   unauthenticated-adjacent cost amplifier if abused.

## 4. Verification gate — one external pen-test, after billing, before first invoice

The launch plan already budgets an external pen-test (€2–5K, boutique). Sequence it **after**
the Stripe integration lands and **before** the first invoice, so the paid surface (billing
endpoints, webhook handler, entitlement gating, auth/session/CSRF) is in scope and the spend
happens once. Findings triage follows the audit doc's severity convention.

## 5. Sequencing

```
security pass (§2, ~3–4d) ──→ Stripe build (with §3 baked in) ──→ pen-test (§4) ──→ first invoice
```

Reopen this doc if the billing provider decision changes (Stripe vs Paddle merchant-of-record,
ADR-0002 open follow-up) — §3 items 1–3 are provider-specific in mechanics but not in intent.
