# ADR-0001: Deployment Model — Self-hosted-first

- **Date**: 2026-04-29
- **Status**: **Superseded by ADR-0002 (SaaS-first for awareness)** — 2026-06-11 (was: Accepted 2026-04-29)
- **Decider**: AxiaOps Maintainers
- **Supersedes**: —
- **Superseded by**: ADR-0002 (SaaS-first for awareness)

## Context

AxiaOps is a solo-developer, pre-revenue FinOps product detecting idle/zombie cloud resources. Phase 1 (MVP) is complete; Phase 2 (real AWS integration, observability, scheduled scans, App Runner + RDS deployment) is in progress. The product currently assumes a multi-tenant SaaS deployment model (App Runner + RDS, ~€24–34/mo at MVP) with Kinde as the auth provider.

Several Phase 3 tasks are SaaS-specific plumbing that does not validate whether the detection rules are valuable:
- Phase 3 #1 — Stripe billing
- Phase 3 #9p — GDPR controller paperwork (privacy policy, DPA, sub-processors, RoPA, DPIA, breach runbook, pen-test)
- Phase 3 #17 — SOC 2 Type II for multi-tenant data processing

Together these represent several months of non-differentiating engineering and compliance work.

The enterprise SSO design (`docs/sso-integration-design.md`) cannot be finalized without choosing a deployment direction: Option A (Kinde-brokered) only works for SaaS, while Option B (native SAML + OIDC) is required for any self-hosted deployment.

A decision spike (`docs/deployment-strategy-spike.md`) compared SaaS-first vs self-hosted-first across time-to-revenue, engineering effort, revenue model, sales motion, telemetry, auth, and compliance.

## Decision criteria — four gates

The Decision below assumes all four gates resolve in favour of self-hosted-first. **Flipping any one gate to "SaaS" inverts the recommendation regardless of the data-sensitivity, packaging-readiness, and Phase-3-cliff arguments** in the spike. Resolve each gate at decision time and record the answer so the reasoning is auditable later.

**Note on framing**: gates 3 and 4 are *not* "which path prevents burnout" or "which path is easier." Both paths are hard for a solo founder; both have burnout vectors. These gates ask which *kind* of difficulty is more compatible with how you operate, and what scale you're actually trying to reach.

### Gate 1 — Funding constraint

Have you raised on a SaaS thesis, or do you plan to raise in the next 12 months?

- [ ] **Yes** → SaaS metrics (ARR, NDR, gross margin) matter for the next round. License + maintenance revenue is harder to pitch and gets a lower multiple from most VCs. Self-hosted creates real fundraising friction. *Lean SaaS-first.*
- [x] **No / bootstrapped** → constraint does not apply. Free to pick the model that fits the customer.

**Resolution**: __________________________________________
**Notes**:

### Gate 2 — Customer pull

Is there a real, in-pipeline design partner who has explicitly said "we want hosted access, not a Helm chart we install ourselves"?

- [ ] **Yes** → follow the customer. A live conversation outweighs the abstract regulated-vertical argument. *Lean SaaS-first* (or single-tenant managed SaaS for that customer specifically).
- [x] **No** → constraint does not apply. Data-sensitivity argument favours self-hosted; the prospects most willing to pay enterprise prices are usually the ones with the strictest stance on third-party data custody.

**Resolution**: __________________________________________
**Notes**:

### Gate 3 — Workload shape

Both paths burn out solo founders; they differ in *what kind* of grind they impose. Between the intense periods that will happen either way, what kind of work do you bounce back into — and which energizes you?

- **Bursty / deep-work** (self-hosted): long stretches of product work + a few high-context relationships with design partners, punctuated by intense closing weeks (cold outbound, MSA redlines, security questionnaires, procurement back-and-forth). Recovery between deals is real and substantial. Cash flow lumpy.
- **Continuous / many-small-things** (SaaS): always-on. Many shallow customer interactions across timezones, marketing/content treadmill, support tickets, billing edge cases, on-call alone for prod incidents. Less rejection per interaction, but no quiet stretches. Cash flow smoother.

For most engineer-founders, **Path A protects more maker time** because the burnout is bursty and recoverable. Path B's burnout is quieter but constant — and harder to recover from because there is no "between."

- [x] **Bursty / deep-work** → self-hosted-first is the better fit.
- [ ] **Continuous / many-small-things** → SaaS-first is the better fit.

**Resolution**: __________________________________________
**Notes**:

> This is **not** a "would you burn out" question. Both paths can burn you out. This gate asks which recovery shape is compatible with how you actually work.

### Gate 4 — Scale ambition

What revenue / customer count do you want to be running 24 months from now?

- [ ] **Lifestyle / sustainable**: 5–10 customers, ~€100–300k/yr revenue, deliberately bounded workload, no team. → **Self-hosted-first** is the natural fit. Annual contracts + maintenance at small customer counts is sustainable; SaaS plumbing only pays off at scale.
- [ ] **Growth / scale**: 50+ customers, €1M+ ARR within 24 months, intent to hire and grow, possibly raise capital later even if not now. → **SaaS-first** is the natural fit. License + maintenance scales linearly with sales effort; SaaS scales sub-linearly. Self-hosted at 50+ customers without a team is a support nightmare.
- [x] **Undecided / depends on traction**: → **Self-hosted-first** as a starting position, because it is the reversible direction. Self-hosted → SaaS later is cheap (same containers run on our infra). SaaS → self-hosted later is expensive (Kinde rip-out, packaging, sales-motion change).

**Resolution**: __________________________________________
**Notes**:

> Tension warning: if Gate 3 is "Bursty" but Gate 4 is "Growth at scale," there is a structural conflict — SaaS at scale demands continuous operations, which the bursty workload preference cannot sustain solo. **Resolution**: hire before reaching scale, or rescope Gate 4 to "Lifestyle" until headcount is added.

### Outcome

| Gate 1 | Gate 2 | Gate 3 | Gate 4 | Recommended path |
|---|---|---|---|---|
| No | No | Bursty | Lifestyle / Undecided | **Self-hosted-first** (this ADR's Decision as drafted) |
| Yes | * | * | * | SaaS-first — fundraising trumps |
| * | Yes | * | * | SaaS-first — follow the customer in hand |
| * | * | Continuous | * | SaaS-first — workload shape mismatch with self-hosted |
| * | * | * | Growth at scale | SaaS-first — scale economics; verify Gate 3 is Continuous or plan to hire |

If the resolved path is **self-hosted-first**, accept the Decision section below as drafted.
If the resolved path is **SaaS-first**, the Decision section must be rewritten before this ADR is accepted: flip §3 of `docs/sso-integration-design.md` back to Option A (Kinde-brokered); keep Phase 3 #1 (Stripe), #9p (GDPR controller), #17 (multi-tenant SOC 2) on the roadmap as currently scheduled; drop the Helm-chart and native-auth Phase items from this ADR's "Now committed to" list.

## Decision

**AxiaOps will pursue self-hosted-first as v1.** The v1 GTM is 3–5 enterprise design partners on annual contracts, running AxiaOps in their own AWS/GCP/Azure/on-prem infrastructure. Multi-tenant SaaS is deferred until at least 3 self-hosted customers are paying.

Concrete commitments:

1. Package AxiaOps as a Helm chart and a docker-compose bundle, published as OCI images via the existing GitLab CI pipeline.
2. Replace Kinde with native auth: email/password + SSO via Option B (native SAML + OIDC) of the SSO design doc.
3. Defer Phase 3 #1 (Stripe) indefinitely. Defer Phase 3 #17 (multi-tenant SOC 2) indefinitely. Narrow Phase 3 #9p (GDPR) to processor-of-our-own-product scope.
4. Pricing: ~€5–10k/yr per organization for first design partners; year-1 maintenance included. *(Number requires market validation in the first 3 sales conversations.)*
5. Add an opt-in `phone-home` telemetry channel for anonymized detection-rule efficacy. Default off in regulated verticals; explicit consent in MSA.

## Alternatives considered

### Alternative A — SaaS-first (status quo)
Continue as multi-tenant SaaS on App Runner + RDS, finish Phase 3 SaaS plumbing, ship Kinde-brokered SSO.

**Rejected because:**
- Phase 3 plumbing dominates the next 6 months for non-differentiating work.
- Data sensitivity (read-only IAM access to the customer's full AWS bill) creates trust friction with FinOps buyers in regulated verticals — exactly the cohort most willing to pay enterprise prices.
- The product is not PLG-shaped (FinOps tooling is consultative, not viral).
- A solo developer running a SaaS for 50+ customers requires support, on-call, billing, and fraud-handling tooling that doesn't exist.

### Alternative B — Dual SKU (SaaS + self-hosted simultaneously)
Ship both deployment models in parallel.

**Rejected because:** for a solo developer this means two release trains, two auth code paths, two support models, two compliance scopes, and two sales motions competing for one founder's calendar. Fatal at this team size.

### Alternative C — Hybrid (SaaS-first now, self-hosted later)
Stay SaaS, add self-hosted in Phase 5+.

**Rejected because** the reverse migration (Kinde rip-out, packaging, sales-motion change, customer onboarding rewrite) is more expensive than the forward path. Self-hosted → SaaS is cheap because the same containers run on our infra; SaaS → self-hosted requires unwinding integrated decisions.

## Consequences

### Easier
- **Time to first paying customer** drops from 3–6 months to 1–3 months.
- **SOC 2 scope narrows**: customer holds their own data; we don't process their billing data.
- **AxiaOps hosting cost** approaches zero — customer pays their own infra.
- **Single auth code path** — no Kinde + SSO dichotomy in `auth.go`.
- **Detection-rule iteration** through direct design-partner relationships rather than a support-ticket funnel for 50+ accounts.

### Harder
- **No continuous-prod telemetry** for detection-rule efficacy. Iteration is slower without an opt-in channel; some design partners will refuse telemetry entirely.
- **Enterprise sales motion required**: MSA, DPA, security questionnaires (SIG Lite, CAIQ), procurement cycles 3–6 months. Founder calendar consumed by sales conversations, not product work.
- **Versioned releases** instead of continuous deploy; backwards-compat burden for upgrades; some customers will run versions for months without upgrading.
- **License / entitlement model** eventually needed (annual contracts + good faith are sufficient for the first 5 design partners, but not beyond that).
- **Investor pitch language** shifts from "ARR/MRR" to "annual contract value + maintenance"; lower-multiple valuation in early-stage SaaS markets.
- **Heterogeneous customer environments** — debugging issues in environments we can't see is an order of magnitude harder than debugging our own infra.

### Now committed to
- SSO design doc §3 recommendation flips from Option A to Option B (native). The §4 data model is unchanged — already portable.
- Phase 3 roadmap rescoping (#1, #9p, #17 — all defer or narrow).
- New Phase items needed:
  - Helm chart + docker-compose bundle, on-host install runbook
  - Native auth replacing Kinde (email/password + SSO Option B) — scoped in `docs/sso-implementation-plan.md` Phases B1/B1.5/B2/C
  - License / entitlement model — **scoped in `docs/sso-implementation-plan.md` D12 / Phase B1.6 (advanced from this ADR's "deferred to 6+ customers" follow-up; see follow-ups section below)**
  - Self-hosted release versioning + upgrade policy
  - Opt-in telemetry channel
- Sales/business deliverables (out of code scope but real):
  - Standard MSA + DPA templates
  - Security-questionnaire pre-fill (SIG Lite, CAIQ)
  - Pricing sheet for first 3 design-partner contracts

### Not affected
- Detection rules (`CLAUDE.md` §FinOps Domain Rules).
- Data model (organizations, RLS, audit_log, dismissals, snapshots) — already deployment-agnostic.
- SSO data model in `docs/sso-integration-design.md` §4 — portable to either runtime.
- Phase 4 multi-cloud (Azure/GCP) — product capability, deployment-agnostic.
- Code conventions, observability stack, CI pipelines.

## Review trigger conditions

Reopen this ADR (write a successor) if any of the following become true:

- **6 months elapse with zero paying self-hosted customers.** The thesis was wrong; reconsider SaaS or pivot the product.
- **A high-quality SaaS design partner** explicitly demands hosted access and is willing to sign quickly. Single-customer hosted SKU may be cheaper than full multi-tenant.
- **Investor or fundraise dynamics** require classic SaaS metrics for the next round — the ADR's deferral of Stripe/SaaS plumbing becomes a fundraising blocker.
- **Regulatory shift** that makes self-hosted compliance (narrowed SOC 2 scope) untenable, or makes the SaaS path materially cheaper to certify.
- **3+ self-hosted customers paying and renewing** — at this point, evaluate whether to add SaaS as a second SKU now that the team can sustain it.

## Open follow-ups (track separately, not part of this ADR)

- Pricing validation in the first 3 sales conversations (€5–10k/yr is a hypothesis, not data).
- Telemetry-channel design (what's anonymized, what's opt-in granularity, where it's stored).
- ~~License/entitlement model design (deferred until 6+ self-hosted customers; build then).~~ **Resolved 2026-04-30**: advanced to v1 as `docs/sso-implementation-plan.md` D12 / Phase B1.6 (license-file TTL enforcement, ~1w). Reason for advancement: the original deferral relied on the "annual contracts + good faith" trust model, which breaks for any churned customer who keeps running yesterday's binary indefinitely. The B1.6 scope is deliberately bounded — TTL enforcement only, no license server, no feature-tier gating. Feature-tier gating remains deferred until pricing differentiation is a real product question.
- Decision on whether to keep Kinde for an internal "AxiaOps Cloud" instance for our own dogfooding, or remove entirely.
