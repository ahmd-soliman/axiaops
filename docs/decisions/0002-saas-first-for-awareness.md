# ADR-0002: Deployment Model — SaaS-first for awareness; self-hosted resequenced

- **Date**: 2026-06-04
- **Status**: **Accepted** — 2026-06-11
- **Decider**: Ahmed Soliman (founder)
- **Supersedes**: [ADR-0001 (Self-hosted-first)](0001-deployment-model.md)
- **Superseded by**: —

## Context

[ADR-0001](0001-deployment-model.md) (2026-04-29) chose **self-hosted-first**: 3–5 enterprise design partners on annual contracts, running AxiaOps in their own infra, with multi-tenant SaaS deferred until ≥3 self-hosted customers pay. That work shipped: Kinde was removed, native auth (email/password + native OIDC/SAML) was built, the license-JWT entitlement model landed, and the codebase was deliberately structured for dual-SKU reactivation (the `serverbuild.ComposeServer` composition-root factory + the four interface seams that cross its `Deps` boundary — `storage.Store`, `auth.Provider`, `sso.Discoverer`, `sso.Connector` — and the `cmd/api-selfhosted` / `cmd/api-saashosted` split).

Two things have changed since 2026-04-29 that invert the decision:

1. **The self-hosted sales motion is too cold to start from zero brand.** Enterprise self-hosted deals require security reviews, MSAs, and procurement — none of which happen with a vendor the buyer has never heard of. A solo founder with no market awareness cannot run that motion efficiently; the sales effort per deal is intense and the top-of-funnel is empty. ADR-0001 treated the enterprise sales motion as a known cost but underweighted the **cold-start / awareness** problem that precedes it.

2. **The migration tax that justified ADR-0001 has already been paid.** ADR-0001's main reason to reject "SaaS-first now, self-hosted later" (its Alternative C) was that "SaaS → self-hosted is expensive (Kinde rip-out, packaging, sales-motion change)." Kinde is gone, native auth exists, and the dual-`cmd` + composition-seam architecture (`ComposeServer` + the four `Deps` interfaces) means **both SKUs now build from one codebase**. The reverse-migration cost that dominated ADR-0001's calculus is no longer the binding constraint.

This invokes ADR-0001's first review trigger ("the self-hosted thesis was wrong about GTM feasibility; reconsider SaaS") with a new, specific rationale: **lead with SaaS to build market awareness and a self-serve funnel; treat self-hosted as the enterprise/regulated follow-on motion once a brand exists.**

## Decision criteria — re-resolving ADR-0001's four gates

ADR-0001 said flipping any one gate inverts the recommendation. Re-resolved with 2026-06 information:

| Gate | ADR-0001 (2026-04) | ADR-0002 (2026-06) | What changed |
|---|---|---|---|
| **1 — Funding** | No / bootstrapped → n/a | No / bootstrapped → n/a | unchanged |
| **2 — Customer pull** | No hosted-demand partner → self-hosted | **Awareness-first** → SaaS | Reframed: the issue isn't "a customer demanded hosting," it's "there are no customers yet because there's no awareness." SaaS is the awareness/funnel engine. |
| **3 — Workload shape** | Bursty/deep-work → self-hosted | **Continuous accepted as the cost of awareness** → SaaS | Founder accepts the continuous-ops burden as the price of a self-serve funnel. This is the gate that genuinely flips; see Risks. |
| **4 — Scale ambition** | Undecided → self-hosted (reversible) | **Undecided, but reversibility now symmetric** → SaaS | The seam work made SaaS↔self-hosted cheap *both ways*. "Reversible direction" no longer points only at self-hosted. |

Per ADR-0001's outcome table, **Gate 2 reframed + Gate 3 flipped to Continuous each independently invert the recommendation to SaaS-first.**

## Decision

**AxiaOps will pursue SaaS-first as the primary go-to-market, optimised for market awareness and a self-serve funnel. Self-hosted is resequenced to a follow-on enterprise SKU, not abandoned.**

This is the **dual-SKU-from-one-codebase** posture that ADR-0001 deemed fatal for a solo founder — now viable specifically because the auth/composition seam work removed the "two divergent code paths" objection.

Concrete commitments:

1. **Lead with a hosted "AxiaOps Cloud" offering** with a self-serve onboarding aha moment: connect a read-only cross-account role → see wasted spend within the first session. This is the funnel.
2. **Build the SaaS entitlement + billing path** (Stripe → internal `entitlements` projection; see [`saas-platform-admin-design.md`](../saas-platform-admin-design.md) §7). The license JWT goes dormant under SaaS (`SetEnforcementBypass`); entitlement gates instead.
3. **Build the platform admin/support plane** ([`saas-platform-admin-design.md`](../saas-platform-admin-design.md) §4–6): staff identity, audited break-glass cross-tenant access, internal-ops notifications. This is now near-term, not preparation.
4. **Multi-tenant hardening + GDPR-as-controller + abuse/fraud posture** return to the near-term roadmap (the Phase 3 plumbing ADR-0001 deferred).
5. **Keep the self-hosted SKU buildable and warm** — `cmd/api-selfhosted` stays green in CI; the license model stays as-is for it. Self-hosted becomes the upmarket motion for regulated buyers who can't use the cloud, sold *after* awareness exists. The CI cost of keeping a second SKU green is accepted as the price of a friction-free enterprise revival (no bitrot, revival is a flip not a rebuild).
6. **Tenant SSO** follows the SaaS variant ([`sso-integration-design-saas.md`](../sso-integration-design-saas.md)) for the hosted SKU; native OIDC/SAML remains for self-hosted.

## Alternatives considered

### Alternative A — Stay self-hosted-first (ADR-0001 as-is)
**Rejected because:** the cold-start awareness problem makes the enterprise self-hosted motion impractically slow from zero brand, and the migration-cost argument that justified it has been overtaken by the seam work.

### Alternative B — Dual SKU, equal priority
Ship both with equal investment. **Rejected because:** a solo founder still cannot run two equal sales motions + two support models simultaneously. The resolution is *sequencing* (SaaS-first, self-hosted follow-on), not parallel equality. One motion leads.

### Alternative C — Pure SaaS, kill self-hosted
**Rejected because:** the regulated/air-gapped enterprise segment (the highest-ACV buyers) genuinely cannot use hosted, and the self-hosted SKU is already built. Killing it forfeits the upmarket motion for near-zero saving. Keep it warm (commitment #5: green in CI, revival is a flip not a rebuild).

## Consequences

### Easier
- **Top-of-funnel exists.** Self-serve trial → awareness → inbound, instead of cold enterprise outbound.
- **Continuous product telemetry** returns — detection-rule efficacy is observable across tenants (the thing ADR-0001 lamented losing).
- **Faster feedback loops**; smoother cash flow (subscriptions vs lumpy annual contracts).
- **Reversibility is now symmetric** — the seam work means pivoting again is cheap either way.

### Harder
- **Front-loads the plumbing ADR-0001 deferred**: Stripe billing, multi-tenant hardening, admin/support plane, GDPR-as-controller, abuse/fraud handling — months of non-detection work before this pays off.
- **Continuous solo ops / on-call** — Gate 3's "no quiet stretches." This is the dominant personal-sustainability risk.
- **Data-custody objection returns** for regulated buyers — mitigated by keeping the self-hosted SKU as their path, but it means the SaaS funnel skews toward smaller/less-regulated orgs first (lower initial ACV).
- **Bigger breach blast radius** — AxiaOps now custodies many tenants' read-only AWS access in one place.
- **Two SKUs green in CI** — keeping `cmd/api-selfhosted` warm alongside the SaaS build is ongoing CI + maintenance cost, accepted in exchange for a friction-free enterprise revival.

### Now committed to
- Re-status [`saas-platform-admin-design.md`](../saas-platform-admin-design.md) from "preparation / not scheduled" to near-term/scheduled.
- Reactivate [`sso-integration-design-saas.md`](../sso-integration-design-saas.md) as the tenant-auth path for the hosted SKU.
- Roadmap rescope in `Tasks.md`: un-defer Phase 3 #1 (Stripe), un-defer/right-scope #9p (GDPR controller) and #17 (multi-tenant SOC 2).
- Self-hosted Helm/bundle work drops from "near-term blocker" to "warm, enterprise-follow-on" — stays green in CI, revived on demand for a concrete enterprise deal (see commitment #5).

### Not affected
- Detection rules, data model, RLS, observability, CI — all deployment-agnostic (as ADR-0001 already noted).
- The self-hosted SKU's license model — unchanged for that build.
- Phase 4 multi-cloud — still a product capability, still demand-gated, still below "make the SaaS funnel convert" in priority.

## Risks & the load-bearing assumption

**The bet rests on AxiaOps being PLG-shaped — which ADR-0001 explicitly disputed** ("FinOps tooling is consultative, not viral"). This ADR is correct *only if* the self-serve aha moment lands: a user can connect a read-only role and see credible, valuable waste findings within one session, with no consultative hand-holding. **Validate this before committing the full plumbing spend.**

Cheap validation gate before heavy investment: a hosted trial that delivers the connect→findings aha to N self-serve users with a measured activation rate. If activation is weak, the product is consultative after all and ADR-0001's instinct was right — stop and reconsider before building Stripe/multi-tenant/admin-plane.

## Review trigger conditions

Reopen (successor ADR) if:
- **Self-serve activation rate stays low** after the validation gate — the PLG assumption was wrong; the consultative/self-hosted thesis was right.
- **A large regulated design partner** wants to sign for self-hosted at high ACV — consider leading with self-hosted for that deal even while the SaaS funnel runs.
- **Solo continuous-ops burden proves unsustainable** before there's revenue to hire — rescope to a narrower hosted offering or revert.
- **Unit economics underwater** — hosting + support cost per self-serve tenant exceeds what the funnel monetises.

## Open follow-ups
- Define the self-serve activation metric + the validation-gate threshold (the load-bearing experiment).
- Billing provider selection (Stripe vs Paddle-as-merchant-of-record for EU VAT) — separate doc.
- Free-tier / trial shape (card-required? reverse trial? account limit?).
- Pricing for the hosted tiers (distinct from the self-hosted €5–10k/yr annual-contract number).
