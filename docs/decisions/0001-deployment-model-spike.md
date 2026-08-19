# AxiaOps Deployment Strategy: SaaS-first vs Self-hosted-first

Status: **decision spike**, no commitment. Written 2026-04-29 to unblock the §3 recommendation in `docs/sso-integration-design.md`. The SSO doc cannot be finalized until the deployment-direction question is answered.

## The question

Do we ship AxiaOps as **multi-tenant SaaS** (current direction) or as **self-hosted software** that customers run in their own AWS/GCP/Azure/on-prem? Or both?

A "both" answer for a single-developer team is almost always the wrong answer (see below) — pick one for v1.

## TL;DR

For a single-developer pre-revenue product targeting FinOps buyers, **self-hosted-first is faster to first revenue** and avoids the SaaS-plumbing cliff (Stripe billing, multi-tenant SOC 2, GDPR controller paperwork, account-lifecycle UX). SaaS-first is right if the ICP is mid-market dev-tools with low-touch sales and the goal is monthly recurring revenue at scale.

**Recommendation**: self-hosted-first, optimized for 3–5 enterprise design partners. Defer SaaS until self-hosted customers are paying. Reasons in §Recommendation below.

## Side-by-side

| Dimension | SaaS-first (current direction) | Self-hosted-first |
|---|---|---|
| **Time to first paying customer** | 3–6 months — needs Stripe + multi-tenant hardening + GDPR controller + onboarding UX | 1–3 months — package Helm/compose, license key, runbook |
| **Already built** | Multi-tenant RLS, Kinde auth, dashboard, ingestion, AWS detection rules | Same code, **plus** already-containerized via docker-compose + GitLab CI image builds |
| **Still to build** | Stripe (Phase 3 #1), GDPR paperwork (Phase 3 #9p), SOC 2 Type II (Phase 3 #17), production hardening, multi-tenant pen-test, billing edge cases | Helm chart / OCI bundle, license/entitlement check, version-migration tooling, on-host install docs, native auth replacing Kinde |
| **Revenue model** | Monthly subscription, ~€50–500/mo per org *(estimate)* | Annual contract €5–50k + 20% maintenance, OR per-seat subscription with self-hosted entitlement *(estimate)* |
| **Sales motion** | Self-serve signup, content/PLG, low-touch | Enterprise sales — security review, MSA, DPA, procurement; 3–6 month cycles |
| **Update cadence** | Ship daily; observe prod; iterate detection rules continuously | Versioned releases (quarterly); customers run N versions in parallel; backwards-compat burden |
| **Detection-rule iteration** | Full prod telemetry; tune thresholds against real customer scans | Blind to customer environments; iterate via support tickets + opt-in telemetry |
| **Auth path** | Kinde stays; SSO is §3 Option A (Kinde-brokered) | Kinde **comes out entirely**; SSO is Option B (native) day one |
| **SSO effort** | 4–6 weeks (Kinde-brokered) | 8–12 weeks (native SAML + OIDC) — higher upfront, zero third-party auth dependency |
| **Compliance posture** | We hold customer data → full SOC 2 Type II + GDPR controller + DPIA + sub-processors list | Customer holds their own data → SOC 2 scope narrows; GDPR processor-of-our-own-product only |
| **AxiaOps hosting cost** | App Runner + RDS (€24–34/mo at MVP, scales with customers) | Near-zero — customer pays their own infra |
| **Investor optics** | Recurring ARR, classic SaaS metrics | Lower-multiple valuation; "license + maintenance" reads as old-school |
| **Reverse path** | SaaS → self-hosted is hard (Kinde rip-out, packaging, sales motion change) | Self-hosted → SaaS is easier (already containerized; SaaS = same containers, our infra) |
| **Top risk** | Drowning in SaaS plumbing before product-market fit | Long sales cycles, no PLG feedback loop, heterogeneous-environment support burden |

## Why "both" (dual SKU) is a trap for one developer

- **Two release trains** (continuous SaaS vs versioned self-hosted) → 2× release toil.
- **Two auth code paths** (Kinde JWT + native session) → permanent drift risk.
- **Two support models** (incident response on our infra + customer-environment debugging).
- **Two sales motions** competing for one founder's calendar.
- **Two compliance scopes** (full SOC 2 + customer-side audit support).

Pick one. Add the other later only after the first is stable and ≥3 customers are paying.

## Where AxiaOps's actual situation lands

### Tilting toward self-hosted-first

- **Data sensitivity.** Cost-and-usage + IAM cross-account access is among the most sensitive financial telemetry in a company. Many regulated buyers won't trust a pre-Series-A vendor with it.
- **Already containerized.** `docker-compose.yml`, GitLab CI, OCI image builds — most of the packaging substrate exists.
- **Skips the Phase 3 cliff.** Stripe (#1), GDPR controller (#9p), multi-tenant SOC 2 (#17) are months of non-differentiating plumbing. Self-hosted defers most of it.
- **Capacity match.** A solo developer can sustain 3–5 enterprise customers more easily than 50 SaaS subscribers (no fraud, no billing edge cases, no support tooling).
- **Easier first reference logos** in regulated verticals.

### Tilting toward SaaS-first

- **Sunk cost.** Kinde, RLS, App Runner readiness already built; switching now wastes some of that.
- **Faster detection-rule iteration.** Continuous prod telemetry is a real moat — self-hosted starves the loop.
- **Investor language.** ARR/MRR is the only metric that translates cleanly to a Series A pitch.
- **Onboarding friction.** Sign up → paste IAM role → see zombies in 5 minutes is hard to beat.
- **Buyer trend.** Mid-market FinOps buyers are increasingly SaaS-comfortable; the on-prem-only cohort is shrinking.

## Recommendation (opinionated, redirectable)

**Self-hosted-first, with 3–5 enterprise design partners as the v1 GTM.**

Concrete bets:

1. Package as Helm chart + docker-compose bundle; reuse existing GitLab CI for OCI image publishing.
2. Rip out Kinde; ship native auth (email/password + SSO Option B). One auth code path.
3. Pricing: ~€5–10k/yr per organization, no per-seat. Year-1 maintenance included. *(Number needs market validation.)*
4. Defer Stripe (#1), GDPR controller paperwork (#9p), multi-tenant SOC 2 (#17). Revisit once ≥5 self-hosted customers are paying.
5. Telemetry: opt-in `phone-home` for anonymized detection-rule efficacy. Customers can disable. Default off in regulated verticals.
6. SSO: native Option B in v1 — Entra OIDC + SAML + generic OIDC. The §4 data model in the SSO doc is unchanged.

**Switch to SaaS later** when (a) ≥3 self-hosted customers are paying, (b) the product is clearly differentiated, (c) operational rhythm exists for multi-tenant production. Reverse path is cheap because the same containers run as our SaaS.

## What this changes (if accepted)

- **SSO doc** §3.4 recommendation flips to Option B. §3.6.3 becomes the canonical answer. Phase B effort grows from L (4–6w) to XL (8–12w) but no Phase F (cutover) is ever needed.
- **Phase 3 roadmap** rescoped:
  - #1 (Stripe) — defer.
  - #9p (GDPR controller paperwork) — narrow to processor-of-our-own-product.
  - #17 (SOC 2) — narrow to in-house systems only, not customer-data systems.
- **New Phase items needed:**
  - "Helm chart + docker-compose bundle, on-host install runbook"
  - "Native auth replacing Kinde (email/password + SSO)"
  - "License / entitlement model"
  - "Self-hosted release versioning + upgrade policy"
  - "Opt-in telemetry channel"
- **Sales / business** items (out of code scope but real):
  - Standard MSA + DPA templates
  - Security questionnaire pre-fill (SIG Lite, CAIQ)
  - Pricing sheet for first 3 design-partner contracts

## What it does NOT change

- Detection rules (`CLAUDE.md` §FinOps Domain Rules).
- Data model (organizations, RLS, audit_log) — already deployment-agnostic.
- SSO data model in `sso-integration-design.md` §4 — portable to either runtime.
- Phase 4 (multi-cloud Azure/GCP) — product capability, deployment-agnostic.
- Code conventions, observability stack, CI pipelines.

## Open questions for you

These are the ones that determine whether the recommendation above holds:

1. **ICP.** "Regulated financial-services CTOs" → self-hosted. "Mid-market dev-tools VPs of Eng" → SaaS. Which describes who's actually emailing you / who you'd cold-outbound to?
2. **Investor expectations.** If you've raised (or plan to raise) on a SaaS thesis, license + maintenance reads differently to a cap table. Constraint or open?
3. **Design partners in flight.** Any 1–2 conversations already started? What are they asking for — hosted access, or "can we run it ourselves"?
4. **Comfort with enterprise sales motion.** Procurement, security questionnaires, redlines on MSA, 90-day cycles. SaaS skips most of that. Honest self-assessment matters more than capability.
5. **Year-1 revenue target.** €30k from 5 self-hosted contracts vs €30k from 60 SaaS subscribers are very different products to build. What's the number, and over what window?
6. **Kinde sunk-cost tolerance.** Ripping out Kinde is ~1–2 weeks of work but clean. Comfortable with that or prefer to keep it for the Kinde-brokered path?

## Decision protocol

Recommend deciding within **2 weeks**. Both paths can be wrong; staying undecided is the only one that's definitely wrong, because every Phase 2/3 task that lands now picks a side implicitly.

When decided, the next steps are:
- Update `docs/sso-integration-design.md` §3 to reflect the chosen path.
- Add/remove roadmap items per the "What this changes" list.
- Delete this spike doc or move it to `docs/decisions/2026-Q2-deployment-model.md` as an ADR.
