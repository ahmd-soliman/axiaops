# Go-To-Market Assessment — AxiaOps

**Audience:** Founder / internal
**Date:** 23 April 2026
**Scope:** Commercial readiness, positioning, and channel strategy — grounded in what is actually shipped in `main`, not what docs or pitches claim.
**Companion docs:** `docs/market-readiness-2026-04.md` (readiness scorecard), `docs/competition.md` (competitor deep-dive), `docs/business_plan.md` (pricing + ICP + financial plan), `docs/pitch.md` (Q&A talking points), `docs/go_live_checklist.md`.

> **Status update (2026-06-11):** several gaps below have since closed — production is live
> on **ECS Express** (not App Runner; pivoted 2026-05), email + Slack scan digests shipped,
> dismiss/snooze + audit trail shipped, GDPR deletion + export shipped, and the self-hosted
> bundle was **deferred** in favour of SaaS-first (draft ADR-0002). Commercial items (UG
> incorporation, Stripe, self-signup, landing page) remain open. Read the rest as an
> April 2026 snapshot.

> **April 2026 ICP refinement — addendum**
>
> Earlier sections of this document treat "MSPs" as a single ICP-1. After a structured review (see §4.5 below — added April 2026), this is revised. The original "MSP" label conflated three distinct buyer personas: (a) general IT MSPs (the ConnectWise/N-able/r/msp population — wrong customer for AxiaOps), (b) specialist AWS Solution Provider partners (real but hard to sell to, with strong incumbents in CloudCheckr/Spot.io and Flexera), and (c) independent FinOps consultants (real, plausible, but a tiny global segment). Revised priority order:
>
> - **ICP-1 (highest priority): independent FinOps consultants and small specialist consultancies (1–5 people).** Real buyer, fast decision cycle, per-account pricing fits. Ceiling: 30–80 paying customers globally.
> - **ICP-2: mid-market EU DevOps/platform teams (€10K–€100K AWS spend).** Larger reachable population, weaker wedge — Vantage and Unusd already serve them.
> - **ICP-3 (deprioritized): specialist AWS Solution Provider partners.** Strong incumbents, channel-mediated buying, long sales cycles. Revisit when the multi-client dashboard ships.
> - **Out of scope: general IT MSPs.** Different buyer; their customers don't have AWS workloads at the scale that justifies €249+/mo tooling.
>
> The honest reachable SAM after this refinement is ~300–1,500 firms globally, an order of magnitude smaller than the earlier "50K MSPs" framing in `business_plan.md` (now corrected). Realistic 24-month SOM: 80–200 customers / €16K–€40K MRR. The €5M exit math now requires the optimistic case to play out (low probability without a multi-client dashboard + channel motion).
>
> **TL;DR — Is AxiaOps market-ready?**
>
> **Two parallel GTM paths, two distinct answers** (see §4):
>
> **Self-hosted license (Model B) — ~4 weeks away.** Package the existing Docker images as a supported Compose distribution with a license-key flow. Sidesteps the Stripe, production-infrastructure, and GDPR-endpoint blockers because the customer runs it in their own environment. Opens ICP-4 (German Mittelstand, regulated industries) that the SaaS path cannot serve. First invoice plausibly late May 2026.
>
> **SaaS paid beta, 5–10 MSPs (Model A) — the *product* is 2 weeks away; the *business* is 5–7 weeks away.** Product blockers are narrow: actor-attribution on dismissals, a demo tenant, a landing page. The real blocker is **the Operating UG is not incorporated** — you cannot legally invoice B2B customers in Germany as a private individual without meaningful commercial friction (liability, trust, VAT, DPA signability). Incorporation can be compressed from the business plan's August target to ~5–7 weeks if the Steuerberater is engaged this week.
>
> **Public SaaS launch — ~8 weeks of product work** (Stripe, production infrastructure, email/Slack alerts, GDPR endpoints), plus the UG gate, whichever finishes last.
>
> **Versus the competition — yes, there is a real wedge.** No competitor combines (a) detection + (b) dismiss/snooze audit workflow + (c) MSP multi-account pricing + (d) EU data residency under €500/mo. Vantage has a, b-lite, and c but is US-centric and MSP-weak. Unusd has a+ but no b or c. Trusted Advisor is free but AWS-only and has no b. The wedge is time-bound — velocity is the only moat.
>
> **The biggest commercial risks — in order:** (1) UG incorporation slipping past July 2026 and blocking all SaaS revenue; (2) pitch and landing-page materials promising features that don't ship (weekly digest, Azure/GCP, self-hosted, delegation) — a trust breach on Day 1; (3) self-hosted support burden swallowing engineering time if Model B scales faster than expected.

---

## 1. Ground Truth — What Actually Ships Today

This is the single-most-important input to GTM. The messaging must match. Verified against code in `services/` on this commit.

### Shipped (demoable today)

| Capability | Evidence | Notes |
|---|---|---|
| AWS detection across 11+ resource types | `services/shared/analyzer/rules.go`, `services/ingestion/internal/provider/aws/discover.go` | EC2, RDS, Lambda, ELB, NAT GW, EIP, EBS, AMI, snapshot, ElastiCache, OpenSearch, Redshift, SageMaker, DynamoDB, EKS, CloudFront, S3, Kinesis, log groups, ECR, Secrets Manager (Tier 1+2) |
| Dismiss-with-reason (5 reasons) + snooze (1/7/30/90d) + revoke | `services/api/internal/api/handler.go` (createDismissal) | Schema + API complete |
| Multi-tenant RLS at DB level | `services/shared/storage/postgres/migrations/011_*.sql` | Tenant isolation enforced in Postgres, not app code |
| AES-256-GCM encryption of customer AWS credentials | `services/shared/crypto/crypto.go` | Key via `ENCRYPTION_KEY` env |
| Kinde OAuth (RS256 JWT, JWKS cached) | `services/api/internal/middleware/auth.go` | Production-grade |
| Dashboard (Vite + React, dark mode) | `services/dashboard/src/screens/*.jsx` | Login, Connect, Dashboard, Trend, Cost Analytics, Detail, Account Settings |
| CSV export | Just landed on `feature/trend-csv-export` | Standardised across trend/ghost/cost screens |
| Trend / savings history (90-day default) | `ghost_snapshots` table + `GET /v1/trend` | Powers sparkline |
| Scheduled auto-scan (default 24h) + on-demand scan | `services/ingestion/cmd/main.go` ticker | Per-account `scan_interval_hours` |
| Prometheus metrics (HTTP + scan + saving) | `/metrics` on api + ingestion | Exposed; no Grafana wired yet |
| GitLab CI — test + build + push to ECR | `.gitlab-ci.yml` | Deploy stage defined, never executed |
| Rate limiting, graceful shutdown, structured logging | `middleware/ratelimit.go`, `signal.NotifyContext` in both `main.go`s | Phase 2 hardening done |

### Half-shipped (do not demo without caveat)

| Capability | What's missing | Effort |
|---|---|---|
| Dismissal **audit trail with actor** | `dismissed_by` / `revoked_by` store tenant ID, not user email. Dashboard does not display them. | 4h |
| Observability in production | Prom metrics exist; no Grafana, no alerts | 3h |
| Redis (caching/queue) | Core shipped; AOF + prod provisioning pending | 1d |

### Not shipped (do not promise)

| Capability | Where docs claim it ships | Reality |
|---|---|---|
| Email / Slack digest | `pitch.md` ("Weekly digest built-in"), `business_plan.md` (core retention driver) | No SMTP/SES/webhook code anywhere |
| Azure provider | `pitch.md` ("Phase 2 Q3 2026"), `business_plan.md` (2028) | No code; internal docs contradict each other |
| GCP provider | Same | Same |
| Self-hosted deployment | `pitch.md` ("Run entirely inside your own VPC") | No packaging, no docs, no license today — but see §4: reframed as a **Q2 2026 parallel GTM path**, ~3–4 engineering weeks to ship. Claims stay untrue for now; update messaging to "Self-hosted pilots Q2 2026 — talk to sales." |
| MSP white-label / reseller dashboard | `business_plan.md` MSP tier spec | Not built |
| Owner resolution from tags (`team`, `owner`, `env`) | `pitch.md` ("Owner resolution shows the responsible team") | Not surfaced in UI |
| Delegation ("FinOps manager assigns to engineer") | `pitch.md` | No delegation model in schema or UI |
| Stripe billing / plan gating / trial | `business_plan.md` pricing tiers | Zero code |
| GDPR data export + right to erasure | `business_plan.md`, `go_live_checklist.md` (Sept 2026) | Not built |
| In-product IAM role wizard | `business_plan.md` onboarding | Still access-key paste |
| Demo mode tenant | Called out as biggest activation blocker | Fake provider exists in tests, not wired to a tenant |
| Scan history log / retry / DLQ | `phase2-plan.md` Track A | Not built |

### GTM consequence of this reality check

**Every sales conversation must use the shipped column as the ceiling.** Any feature from the "not shipped" column that shows up in a demo, landing page, or objection-handling conversation is a commercial lie that will surface on Day 14 of a trial. Three immediate messaging fixes (see §6):

1. Strike "Weekly digest built-in" from all materials. Add it as "Q2 2026 roadmap."
2. Strike the Azure/GCP Phase-2-Q3-2026 claim. Reconcile with `business_plan.md` (2028) and say "Phase 4, 2028."
3. Reframe "Self-hosted." Do not claim it ships today — it doesn't — but do not strike it either. Per §4, self-hosted is a genuine Q2 2026 deliverable and a parallel GTM path. The right language: *"Self-hosted pilots available Q2 2026 for enterprise customers — contact sales."*

---

## 2. Feature vs. Competitor Matrix — Grounded

Four competitors that actually matter for the ICP (MSPs + mid-market DevOps in EU):

| Feature | AxiaOps (today) | Vantage | Unusd | Trusted Advisor | Komiser |
|---|---|---|---|---|---|
| AWS idle detection | ✅ 11+ types | ✅ | ✅ 32+ types (most) | ✅ | ✅ |
| Azure / GCP detection | ❌ | ✅ | ❌ | ❌ | ✅ |
| Dismiss + reason | ✅ 5 reasons | Partial (notes) | ❌ | ❌ | ❌ |
| Snooze | ✅ 1/7/30/90d | ❌ | ❌ | ❌ | ❌ |
| Audit trail (who/when) | ⚠️ schema yes, UI/actor no | ❌ | ❌ | ❌ | ❌ |
| Trend / history | ✅ 90d | ✅ | Limited | ❌ | ❌ |
| MSP multi-client view | ❌ | ❌ | ❌ | ❌ | ❌ |
| White-label reports | ❌ | ❌ | ❌ | ❌ | ❌ |
| CSV export | ✅ | ✅ | ✅ | Partial | ✅ |
| Slack / email alerts | ❌ | ✅ | ✅ | ❌ | ❌ |
| IAM role (not access key) onboarding | ❌ | ✅ | ✅ | n/a | n/a |
| EU data residency (Frankfurt) | ✅ by default | US-primary | US | n/a | self-host |
| GDPR DPA + data export | ❌ | ✅ | Partial | n/a | n/a |
| Self-hosted | 🟡 Q2 2026 (planned) | ❌ | ❌ | ❌ | ✅ |
| Per-account pricing model | ✅ (design) | ❌ spend-% | ✅ flat/N accounts | n/a | free |
| Free tier | (planned) | ✅ | ❌ | 6 free checks | ✅ |
| Public SOC 2 | ❌ (2027 Q4 target) | ✅ | ❌ | AWS-covered | n/a |

### Reading this matrix

**AxiaOps's two real differentiators today** are (a) the dismiss + snooze workflow and (b) the EU/Frankfurt + per-account-pricing wedge. Everything else is commoditised or is a gap we must close.

**What the matrix reveals about the moat claim** ("remediation workflow + audit trail"): the workflow (dismiss/snooze) is real and unique; the audit trail (who/when) is not yet demoable because actor attribution is not wired. This is a 4-hour fix — and until it ships, the audit-trail claim in every conversation is a lie. **Do not pitch the audit trail to FinOps consultants or MSPs (ICP-2) until this ships. Fix first, pitch second.**

**What the matrix reveals about Vantage as the primary competitor:** Vantage wins on breadth (multi-cloud, SaaS cost, alerts, SOC 2) and loses on depth-of-workflow (no structured dismiss-with-reason, no snooze, no audit trail) and on MSP fit (no reseller tier, US-centric, per-spend pricing penalises MSPs with many small accounts). The AxiaOps wedge against Vantage is specifically **"we are the dismiss/snooze/audit workflow for MSPs that Vantage doesn't build."** That is a narrow but real pitch.

**What the matrix reveals about Unusd:** they are the real detection-depth leader (32+ detection types vs. our 15+). Do not try to win on detection breadth — we will lose. Win on workflow and EU positioning.

**What the matrix reveals about Trusted Advisor:** their free tier is the floor on our pricing. Anyone already paying for AWS Business Support gets basic idle detection free. The question the prospect will ask is "why pay €249/mo when Trusted Advisor is free?" Answer: workflow + multi-account + audit trail. That answer only works if §1 actor-attribution is shipped.

---

## 3. Positioning

### Recommended one-liner

> **"The remediation workflow for cloud waste. Detect idle AWS resources, dismiss with reason, snooze for review, prove savings — without running a 6-month CloudHealth procurement."**

**Why this works:** it names the category (FinOps/cloud waste), names the concrete action (dismiss, snooze, prove), and triangulates against the two relevant comparators (CloudHealth above, scripts below). It does not claim multi-cloud. It does not claim AI. It does not claim savings percentages. Every word is defensible against a customer demand for proof.

### Category choice

Do not call AxiaOps a "FinOps platform" in 2026. The FinOps platform category is owned by Vantage, CloudHealth, and Cloudability, and competing in it forces a head-to-head breadth comparison we lose. Call AxiaOps a **cloud-waste workflow tool** or **idle-resource remediation tool.** The narrower the category, the more ownable the position.

### Messaging by audience

From `business_plan.md` §Messaging, the two-audience split is right. Ground each in a capability we actually ship:

- **Engineering managers → "Cloud Hygiene."** Anchor on the dismiss/snooze workflow and the trend line. "Your cloud looks like an untended garden. Here are 47 resources nobody is using. Here's how you dismiss the 12 that are intentional, snooze the 18 you'll review next quarter, and act on the 17 that should go." This story is true today.
- **CFOs / Finance → "Proven monthly savings."** Anchor on the summary number, the CSV export, and — once shipped — the audit trail. "Last month your team dismissed €3,400 in waste and acted on €2,100, with a full audit log of who approved what." This story becomes true the day actor-attribution ships (Week 1 of the launch plan).

### Positioning pitfalls to avoid in Q2 2026

1. **Don't lead with savings guarantees.** ProsperOps owns outcome-based pricing. We can't verify savings end-to-end yet (no terminate-action, only surface-and-dismiss). Any "guaranteed X% savings" claim is unbackable.
2. **Don't claim multi-cloud in a demo.** It's on the roadmap for 2028 per `business_plan.md`. Any claim otherwise creates a commitment we can't honour, and it attracts an ICP (multi-cloud enterprise) we can't serve.
3. **Don't claim SOC 2 or "enterprise-ready."** Target SOC 2 Type II is Q4 2027. In any RFP before then, disclose this upfront. SMB/MSP doesn't need it; enterprise will ask on day one.
4. **Don't claim "self-serve in 10 minutes."** Today, onboarding requires generating an AWS access key and pasting it. Security-aware prospects will bounce. Either (a) ship the IAM role wizard before making the claim, or (b) adjust the claim to "30 minutes with our setup guide."

---

## 4. Deployment Models & Parallel GTM Paths

The original business plan and the first draft of this assessment assumed one deployment model — multi-tenant SaaS — and one GTM path built around it. That assumption is wrong for two reasons: **(a)** it forces every launch milestone to depend on the Operating UG being incorporated, and **(b)** it excludes the entire enterprise / German-Mittelstand segment that will not connect production AWS credentials to an unknown Berlin SaaS. A second, parallel deployment model (self-hosted / licensed) closes both gaps and is cheaper to ship than the SaaS path.

### 4.1 The three models

| Dimension | **A — SaaS multi-tenant** (current plan) | **B — Self-managed license** (customer runs it) | **C — Managed private** (we run it in their cloud) |
|---|---|---|---|
| Where it runs | Our ECS Express / RDS | Customer's infra (Docker Compose, Helm) | Customer's AWS account, via Terraform module we maintain |
| Who holds data | Us (Frankfurt) | Customer — data never leaves their env | Customer account, we have cross-account IAM |
| Legal posture | We are a GDPR data processor; DPA required | We are a software vendor, not a processor; no DPA needed | We are an operator of their infra; SLA required |
| Engineering cost to ship from today | ~8 weeks (per market-readiness §7) | **~3–4 weeks** (images + Compose + install guide + license key) | 8–10 weeks (Terraform module + cross-account upgrade tooling) |
| Time to first paid invoice (assuming UG ready) | 8 weeks | **~4 weeks** — sidesteps Stripe, GDPR endpoints, prod infra | 10+ weeks |
| Ops burden on us | High, 24/7 | Low (customer runs it) | Medium |
| Support burden | Predictable | **Unpredictable** — every customer environment differs | Medium |
| Upgrade cadence control | Full | None — customer chooses | Full |
| ACV range | €950–€12K/yr | €12K–€30K/yr | €25K–€75K/yr |
| Fit for ICP-1 (MSPs 5–30 accts) | **Best** — they want SaaS | Poor — they don't want another service to run | Fair |
| Fit for ICP-4 (enterprise, Mittelstand) | Poor — SOC 2 / DPA friction | **Best** — data-residency box pre-checked | **Best** — procurement loves "runs in our AWS" |
| AWS-credentials trust barrier | High | None | Low |
| Kinde dependency | Fine | Works as long as internet-reachable; air-gapped needs alt auth | Fine |
| Unlocks UG-incorporation gap | No — UG must exist before first invoice | **Partially** — an annual license invoiced by a friendly GmbH or Einzelunternehmer is less painful than recurring SaaS subscriptions under uncertain legal entity | No |

### 4.2 Why running A and B in parallel beats either alone

**Model B ships faster and un-blocks earlier revenue.**

- Docker images already build in CI (`.gitlab-ci.yml`). Self-managed needs: (1) a versioned Compose file the customer runs, (2) a written install guide, (3) a Kinde tenant provisioning flow or license-key mechanism, (4) an update notification channel. Three of the four are close to shipping; item (3) is the real engineering work.
- Model B sidesteps three of the four Milestone B blockers: full Stripe integration (replaced by an annual license invoice), GDPR data-export/erasure endpoints (customer controls their own data), and production AWS infrastructure (customer's problem).
- The first two customers in Model B become reference logos for the SaaS launch — they validate the product without any dependency on the SaaS stack being production-ready.

**Model B opens a segment the SaaS path cannot serve.**

- German Mittelstand CTOs, regulated industries (insurance, healthcare, fintech), and security-first enterprise will not paste access keys into a new SaaS. They will run a container image in their VPC. `pitch.md` already claims this capability exists — that claim needs to become true, not be struck.
- This is the segment `market-readiness-2026-04.md` §4 deferred as ICP-4 "revisit 2027 Q4 post-SOC 2." Model B un-defers them: SOC 2 matters far less when the customer's data never leaves their infrastructure.

**Model A remains the right model for FinOps consultants and AWS Solution Providers (when the multi-client dashboard ships).**

- ICP-1/ICP-3 buyers want one dashboard showing multiple client accounts, not multiple installs. They explicitly do not want to run yet another service. Model B is useless to them.
- The SaaS path stays on the published 8-week plan. Nothing changes there.

> **Note (April 2026 ICP refinement):** the multi-client dashboard required to serve the AWS Solution Provider segment is **not built**. Until it ships (~5–6 weeks of focused engineering), Model A is best-suited for individual FinOps consultants serving one or two clients at a time, and for mid-market DevOps teams managing their own multiple AWS accounts — neither of whom requires per-organization white-label or multi-tenant client switching.

### 4.3 The risk Model B introduces

Support burden. Your first three self-hosted customers will each have a different Postgres version, different Redis config, different ingress policy, and different ideas about where the encryption key should live. Budget 30–40% of founder engineering time for self-hosted support in the first 90 days. If that trade-off is unacceptable, defer Model B.

Three mitigations:
1. **License only to customers who can support their own Postgres + Redis.** Do not sell Model B to companies without a DevOps function — they will need hand-holding you can't afford.
2. **Ship a single-node Compose image only, not Helm.** Keep the supported surface tiny for the first four customers. Kubernetes deployment = 2027.
3. **Charge for setup.** €2K–€5K one-time onboarding fee for self-hosted. This filters out tire-kickers and funds the support time.

### 4.4 Pricing, competitive positioning, and code protection

This subsection combines what would normally be four separate conversations: tier pricing, competitive benchmarking, free-tier strategy, and IP protection. They interact — dropping a tier changes the competitive frame; an open-source decision changes what the license key protects. Keeping them together prevents inconsistent answers.

**Pricing tiers (revised)**

The original draft included three tiers (Starter/Pro/Enterprise). After reviewing competitive positioning and ICP realism, the revised structure is:

| Tier | Annual license | Setup (one-time) | Accounts | Ideal customer profile |
|---|---|---|---|---|
| **Pro** | €24,000 | €5,000 | Up to 25 AWS accounts | Mid-market, €80K+/mo AWS spend, 1 DevOps engineer |
| **Enterprise (entry)** | €48,000 | €10,000 | Unlimited accounts, up to €2M/yr customer AWS spend | €2M/yr AWS spend, regulated industry or security-first |
| **Enterprise (mid)** | €72,000 | €15,000 | Up to €5M/yr customer AWS spend | €2M–€5M/yr AWS spend |
| **Enterprise (scale)** | €96,000+ | €20,000+ | €5M+/yr, custom SLA, named engineer | €5M+/yr AWS spend, Fortune 1000 DACH |

Year-1 Pro invoice: €29K. Year-2+ recurring: €24K. All tiers: 12-month minimum (24 for Enterprise scale), quarterly security updates, signed container images, Compose distribution; Helm chart and air-gapped mode deferred to 2027.

**Dropped: Self-Hosted Starter (€12K/yr for 5 accounts).** The target profile (5 AWS accounts + sufficient DevOps capacity to run self-hosted) barely exists in practice — customers with 5 accounts overwhelmingly prefer SaaS. The per-account rate (€200/month) was also 7x the SaaS-Starter equivalent, a gap outside industry norms (2–4x SaaS→self-hosted premium). Customers who would have bought Starter are steered to SaaS Growth (€249/mo = €2,988/yr).

**Added: Enterprise spend-indexed ladder.** A flat €48K entry leaves 30–50% on the table for the largest customers. CloudHealth, Cloudability, and Flexera all price this segment indexed to tracked AWS spend; matching that pattern captures value at the top without complicating the Pro tier pitch.

**Competitive positioning**

The correct comparison set for a self-hosted prospect is not the SaaS competitors from §2 — it is the much smaller set of tools that actually offer self-hosted / on-prem. A mid-market DACH CTO evaluating AxiaOps Self-Hosted Pro is realistically comparing against:

| Tool | Annual cost (25 accts, mid-market) | Remediation workflow | Audit trail | EU data residency |
|---|---|---|---|---|
| **Komiser (open source)** | €0 license + ~€80K/yr loaded internal engineer time to build workflow | No | No | Self-hosted |
| **Kubecost Enterprise** | €6K–€24K/yr | Kubernetes-only | K8s only | Self-hosted |
| **AxiaOps Self-Hosted Pro** | **€24K/yr + €5K setup** | **Yes — dismiss/snooze/audit** | **Yes (once actor attribution ships)** | **Yes** |
| **Harness FinOps self-hosted** | ~€30K–€50K/yr | Yes | Partial | Yes |
| **CloudHealth dedicated SaaS** | €45K+/yr | Yes (policy-automated) | Partial | US-primary |
| **Flexera One on-prem** | €50K+/yr | Yes | Yes | Yes |
| **DIY (Komiser + scripts + FinOps analyst)** | ~€80K+/yr all-in | Whatever you build | Typically no | Self-hosted |

**AxiaOps Self-Hosted Pro is 20–50% cheaper than the closest workflow-capable competitor (Harness) and ~50% cheaper than Flexera/CloudHealth.** Against Komiser-plus-DIY, AxiaOps is technically more expensive on license alone but ~€45K/yr cheaper once internal engineer time is loaded-cost properly. The pricing is deliberately under the enterprise band — we win mid-market deals the incumbents overpriced themselves out of, not by undercutting the category but by not pricing for Fortune 500 deployment.

**ROI math for the customer:**

| Customer AWS spend/yr | Typical waste (10–15%) | AxiaOps Pro all-in Y1 | Tool cost as % of savings | Deal feel |
|---|---|---|---|---|
| €300K | €30–45K | €34.5K | 77–115% | **Too expensive — steer to SaaS** |
| €600K | €60–90K | €34.5K | 38–58% | Stretched — only viable if security drives the decision |
| €1M | €100–150K | €34.5K | 23–35% | **Sweet spot** |
| €2M | €200–300K | €34.5K | 11–17% | Slam dunk |
| €5M+ | €500K+ | €34.5K | <7% | Upsell to Enterprise |

Rule: only sell Pro to customers with €80K+/mo AWS spend. Qualify others out early into SaaS.

**Free tier and open-source strategy — should AxiaOps follow the Komiser playbook?**

**Short answer: no.** The open-source-core + paid-enterprise playbook (Komiser, Grafana, Sentry, Metabase, GitLab) does not fit AxiaOps's specific situation. Three structural reasons:

1. **The workflow layer is the moat, not the detection.** Komiser open-sourced detection — which is a commodity (AWS Trusted Advisor gives it away free). AxiaOps's defensible position is the dismiss/snooze/audit workflow per §2. Open-sourcing that hands Vantage (which raised $38M) or any well-funded competitor a free starting point they'd take to market inside 60 days with their distribution. The thing that would make AxiaOps defensible stops being defensible.
2. **Open-source requires VC-scale runway.** Every successful open-core company (GitLab, Grafana, Sentry, Elastic, HashiCorp) spent 3–7 years of VC-funded low monetisation building community before enterprise revenue caught up. AxiaOps is bootstrapped, 1 founder, targeting revenue in 8 weeks. The community-maintenance burden — issue triage, PR review, release management, Slack/forum moderation — is 0.3–0.5 FTE minimum at any serious scale. That FTE does not exist.
3. **The conversion math doesn't work at our scale.** Enterprise open-source conversion rates run 1–3%. That produces meaningful revenue at Grafana's 10M+ users; at AxiaOps's realistic 2026 scale (tens to low-hundreds of evaluators), a 2% conversion generates 2–6 paying customers per year while the free community costs more support time than those customers bring in.

**What to do instead — a staged free-and-adjacent strategy:**

- **Keep the SaaS Free tier already planned** (§5): 1 account, 7-day trend history, no alerts, AxiaOps branding on shareable reports. Captures curious evaluators at zero marginal cost.
- **Add a 30-day self-hosted trial license:** time-boxed license key validates for 30 days, unlocks Pro features on up to 3 accounts. Lets enterprise security teams evaluate the binary inside their VPC before committing to an annual contract. Low abuse risk — renewal requires talking to sales.
- **Release free *adjacent* assets to build brand without open-sourcing the product:**
  - **Standalone detection CLI** (new Go binary, MIT-licensed): reads Cost Explorer + CloudWatch, outputs JSON of detected ghost resources. No workflow, no UI, no persistence — just a report generator. Positioned as "Komiser-for-AWS-cost." Community asset, SEO driver, lead generator; upgrade path to AxiaOps for workflow is obvious.
  - **IAM policy templates** (CloudFormation + Terraform): fully open-source on GitHub. These are not defensible anyway — any competitor can read AWS IAM docs.
  - **Detection rule specifications** (thresholds and verdicts): already documented in `docs/aws-coverage.md`. Publish publicly. Specifications are not code; any competitor can read AWS documentation and derive the same thresholds. Publishing transparently is a trust signal, not a give-away.
  - **Content**: "State of Cloud Waste 2026" report, comparison posts, FinOps Foundation Slack contributions. Zero cost, high SEO value.

Net effect: SEO, brand, and funnel benefits of "free and open" without open-sourcing the core product. The CLI + IAM + specs + content combination is what Komiser-style prospects will find when they search for "AWS ghost resource detection" — and the upgrade path they follow is into AxiaOps SaaS or Self-Hosted, not into a forked competitor.

**Code protection posture**

You are not trying to prevent decompilation — with Go binaries, determined reverse-engineering is possible with effort, and perfect protection is not achievable. You are trying to prevent **commercial reuse by a competitor**, which is a legal problem first and a technical problem second. The standard pattern for commercial self-hosted software (GitLab Enterprise, Sentry Business, Redis Enterprise, Harness) looks like:

| Protection layer | AxiaOps posture | Implementation cost |
|---|---|---|
| Go binary with stripped debug symbols (`-ldflags="-s -w"`) | **Yes** — default build flag | 1-line build change |
| License key validation (JWT signed by AxiaOps, validated on binary startup) | **Yes** — this is the real protection | ~2 engineering days |
| Feature flags gated by license (account count, Pro-vs-Enterprise features) | **Yes** | ~1 day |
| Commercial EULA (no redistribution, no reverse-engineering, no commercial reuse) | **Yes** — lawyer-drafted, ~€1,500 one-time | ~1 week elapsed |
| Container image signing (Cosign / sigstore) | **Yes** — low cost, trust signal | ~0.5 day |
| Go obfuscation (`garble`, `gobfuscate`) | **Skip for 2026.** Modest benefit, adds build complexity, none of the listed commercial peers bother. Reconsider at 50+ paying customers if reverse-engineering evidence appears. | 3–5 days if ever added |
| Phone-home / license heartbeat | **Do not implement.** Enterprise-deal killer — security teams and air-gap-conscious buyers reject it. EULA protection is stronger than any phone-home at this price point. | N/A |

**What this looks like in practice.** Customer runs:

```
docker run -e AXIAOPS_LICENSE_KEY=<jwt> axiaops/self-hosted:v1.0.0
```

On startup, the binary parses the JWT, verifies the signature with our embedded public key, checks expiry, and enforces feature flags (account count, feature set). If invalid or expired: startup refuses, prints a message pointing to `support@axiaops.io`. **No network call, no phone-home, no privacy surface.** Air-gapped deployments work identically.

If a customer reverse-engineers the binary to patch out the license check, they've committed an EULA breach. At €24K–€96K/yr license value, that's a lawsuit worth pursuing — and the Steuerberater-referred IP lawyer sends the cease-and-desist. This has never happened in the commercial self-hosted FinOps space at this price band; the customer population is too small and the legal exposure too large.

**The one thing not to overthink:** perfect code protection is neither possible nor the goal. Standard-practice protection (license key + EULA + signed images) combined with quarterly updates — each new release supersedes the last — is sufficient. A pirated copy running on v1.0.0 in 18 months is not a commercial threat.

**Revenue implications of the revised pricing**

With Starter dropped and the Enterprise ladder added, the Self-Hosted revenue model at scale looks like:

| Customers at end of Q3 2026 | Typical mix | Year-1 invoices booked |
|---|---|---|
| 2 paying | 2 × Pro (discounted 30% as first-2 design partners) | ~€40K license + €10K setup = **€50K** |
| 4 paying (end of Q4 2026) | 3 × Pro + 1 × Enterprise entry | ~€96K license + €25K setup = **€121K** |
| 8 paying (end of Q2 2027) | 5 × Pro + 2 × Enterprise entry + 1 × Enterprise mid | ~€288K license + €50K setup = **€338K** |

These are booked-revenue numbers for the Self-Hosted channel alone, independent of the SaaS path. Two-customer ARR (€48K/yr recurring) matches the business plan's Month 12 SaaS MRR target (€4K × 12 = €48K), collected annually rather than monthly and with substantially lower operational complexity for us.

**Handling the "why not just use Komiser?" objection**

Komiser is the most likely free-alternative objection in a DACH self-hosted sales conversation. The right stance: **don't attack Komiser — it is genuinely good software and the prospect may respect it or already use it.** Position AxiaOps as the workflow and accountability layer above detection, not as a "better Komiser."

Three facts to bring into every such conversation:

1. **Komiser's license (Elastic License 2.0) is source-available, not OSI-approved.** Several regulated DACH industries — banks, insurance, automotive, healthcare — have open-source review policies that allow only OSI-approved licenses (Apache 2.0, MIT, BSD, GPL family). The Elastic License 2.0 controversy (AWS forked Elasticsearch as OpenSearch partly because of it) put it on some enterprise legal teams' "avoid" lists. Worth raising if the prospect's legal or procurement function is in the loop.
2. **Komiser's AWS detection coverage is narrower than AxiaOps's.** Komiser handles inventory and basic idle flagging. It does not detect orphaned snapshots whose source AMI was deleted, long-stopped instances still billing for EBS, unused Secrets Manager entries by LastAccessedDate, unused AMIs, stale ECR images, or unattached Elastic IPs missed by billing aggregations. These are the AxiaOps Tier 2 detections documented in `docs/aws-coverage.md`.
3. **The real competitor is not Komiser — it is Komiser plus internal engineering.** German engineering teams often have the Go/Kubernetes skill to extend Komiser themselves. The procurement-spreadsheet alternative is "€0 license + ~0.5 FTE engineer indefinitely." AxiaOps's pitch has to beat that combined alternative, not Komiser in isolation.

**Sales script for the objection:**

> "Komiser is a great detection and inventory tool — we recommend it to teams who aren't ready for a paid tool yet. What Komiser doesn't give you is the workflow above detection: dismiss-with-reason, snooze, audit log of who on your team approved what, and the trend view that shows whether waste is actually dropping or you're re-surfacing the same resources every week.
>
> Most teams using Komiser end up writing 20–40 hours a month of internal tooling to track which ghost resources they've already reviewed, which are intentionally idle, which are scheduled for deletion. That's what AxiaOps replaces — not the detection, the accountability layer above it. If your team has 0.5 FTE to spare on that tooling indefinitely, Komiser is the right call. If not, €24K/yr is less than 0.25 FTE and you get the audit trail for free."

Three things this script does: doesn't disparage Komiser (preserves trust), names the specific missing capability (workflow + audit trail, not generic "enterprise features"), converts to an engineer-time comparison where the math clearly favours us.

**When to steer a prospect TOWARD Komiser and walk away:**

- AWS spend under €300K/yr (Pro ROI doesn't work per the table above)
- Multi-cloud workload where AWS is secondary
- Customer explicitly wants Kubernetes asset inspection
- Customer has genuine 0.5+ FTE of DevOps capacity for internal FinOps tooling indefinitely

These are not deals worth fighting. Refer the prospect to Komiser, preserve the relationship, revisit when their situation changes.

**Where Komiser helps AxiaOps strategically:**

Komiser validates the category (cloud waste detection is a real problem), warms DACH prospects to self-hosted deployments, and handles the segment below our price floor. The free AxiaOps Detection CLI (per the Free Tier section above) is deliberately positioned as "Komiser's AWS subset, narrower, with an upgrade path to workflow." Prospects who find the CLI via SEO self-segment: detection-only users stay on the free CLI at zero cost to us; workflow-needing users convert to SaaS or Self-Hosted. We capture Komiser-style mindshare without the overhead of maintaining a multi-cloud OSS project.

**Qualifier:** rigorous data on Komiser's German-specific adoption is not publicly available. The assessment above is based on qualitative signals (FinOps Foundation DACH community discussions, Elastic License 2.0 review-board friction, absence from German Mittelstand case studies). If you need defensible market-research numbers for a fundraise or board deck, budget €3–5K for a Gartner/G2/specialist-analyst report. For day-to-day sales conversations, the three facts and the script above are sufficient.

**How Komiser monetises — and why AxiaOps doesn't copy the playbook**

Understanding Komiser's revenue model is useful both for "why not just be free like Komiser?" objection handling and for recognising which parts of their approach are worth copying. Short version: **open-core, via Komiser Cloud (hosted SaaS) plus enterprise support, run by Tailwarden as the commercial vehicle**. Same pattern as Sentry, Metabase, and most infra OSS-to-commercial transitions.

What's confirmed:
- Komiser OSS remains free under Elastic License 2.0
- A hosted "Komiser Cloud" SaaS exists (in private beta per last public signals)
- Pricing is not publicly listed — "contact sales" gate

What is less clear (and should be verified via live lookup — `komiser.io`, `tailwarden.com`, Crunchbase — before citing in any deck):
- Current Tailwarden corporate status, funding, and revenue scale as of 2026
- Customer count and conversion rate

Typical revenue mix for open-core FinOps / cloud tooling — whether Komiser fits this exactly or not:

| Revenue source | Typical share | Notes |
|---|---|---|
| Paid SaaS tier (hosted + team/enterprise features) | 60–80% | Per-account or per-user subscription |
| Enterprise support contracts | 10–20% | For customers still self-hosting OSS at scale |
| Cloud Marketplace listings (AWS/Azure/GCP) | 5–10% | Customer burns committed-spend credits; vendor takes cut minus ~3% marketplace fee |
| Professional services / consulting | 5–15% | FinOps onboarding, custom detection rules |

**The structural challenge of this model:** free-tier cannibalisation. The delta between "run Komiser myself" and "pay for Komiser Cloud" is often narrow enough that cost-conscious teams stay on free indefinitely. Sentry and Grafana solved this with features the free tier genuinely lacks (managed uptime, team access, alerts). FinOps tooling has a harder time because FinOps is typically a 1–3 person function, so "team access control" isn't a compelling paywall. Net: open-core FinOps monetisation is slower and lower-conversion than open-core observability.

**Why AxiaOps doesn't follow the same playbook** (elaborating on the Free Tier section above):

1. **Timing mismatch.** Komiser spent ~5+ years building OSS community before meaningful commercial monetisation. AxiaOps is bootstrapped with an 8-week runway to first invoice. The long-ramp OSS-to-SaaS model requires VC runway to absorb the gap.
2. **Community size mismatch.** Open-core converts at 1–3% of the community. That produces real revenue at Grafana's 10M+ users; at AxiaOps's realistic 2026 scale (hundreds of CLI downloads in year 1), 2% is ~4 paying customers. Not enough to justify the community-maintenance overhead.
3. **Moat location mismatch.** Komiser's OSS gave away detection, which was already commodity. AxiaOps's moat is workflow + audit trail, which is *not* commodity. Open-sourcing it hands the differentiator to competitors with more distribution.

**What IS worth copying from Komiser's playbook:**

- **Adjacent free asset for top-of-funnel.** Komiser-the-OSS-tool generates brand and SEO for Komiser Cloud. AxiaOps's free Detection CLI plays the same role without open-sourcing the product. Already in the plan above.
- **AWS Marketplace listing.** Cloud Marketplace distribution lets enterprise customers burn AWS committed-spend credits on our license — makes a €24K/yr invoice feel "pre-paid" to the buyer. Plan to list by end of Q4 2026; this is a real channel for Self-Hosted deals.
- **Separation of "inspector" from "workflow product."** Komiser positioned OSS as an inspector and commercial product as a team tool. AxiaOps mirrors this: free CLI as inspector, SaaS/Self-Hosted as the workflow product.

**What is NOT worth copying:**

- **Elastic License 2.0 route.** OSI-friction in regulated DACH — if AxiaOps ever releases OSS code, use Apache 2.0 or MIT.
- **Community-first sequencing.** Reversed for AxiaOps: commercial first, community-adjacent free assets second.
- **Private-beta SaaS posture.** Signals either low velocity or excessive gating. AxiaOps publishes pricing from day one.

**Sales script for "why don't you just be free like Komiser?":**

> "Komiser monetises through a paid SaaS tier — Komiser Cloud — built on top of the free inspection tool. That works because they started five years ago, built a large OSS community, and raised venture funding to absorb the long ramp between 'big OSS project' and 'profitable SaaS.' AxiaOps is bootstrapped and targeting paying customers in eight weeks, not five years. We get the same 'free inspection tool' benefit through our free detection CLI — Go binary, MIT license, no signup — but the actual workflow product stays commercial because that's what pays the bills from day one. If five years from now we're Grafana-scale, revisit the open-core question then. Today it would be strategic suicide."

Does three things: names Komiser's model accurately (no strawman), explains why timing/stage makes it the wrong fit for AxiaOps, and leaves the door open for future open-sourcing without committing to it.

### 4.5 Managed Private (Model C) — defer to 2027

Model C is strategically attractive but engineering-heavy (cross-account IAM, remote upgrade orchestration, monitoring that survives being in someone else's VPC). Revisit once Models A and B have 3+ customers each. Not a 2026 play.

### 4.6 Recommended sequencing

1. **April–May 2026 — Run the ICP validation experiment first** (see addendum at top of document). 60 cold emails to 30 EU AWS Solution Provider partners + 30 self-identified FinOps consultants. Two-week response window. Decision gate: <8 calls = redirect to mid-market DevOps and skip the multi-client dashboard build; >12 calls = build the dashboard; 8–12 = beachhead-only, no multi-client build yet. **Do not build the multi-client dashboard before this experiment runs.** It is 5–6 weeks of work whose ROI depends entirely on whether the segment engages.
2. **May 2026 — Ship Model B packaging.** Compose file, install guide, license-key gating, pricing page for self-hosted tier. Target: 2 paid self-hosted customers by end of May. Pre-requires UG only if you want to invoice under the company; otherwise a friendly-entity arrangement or a "letter of intent + invoice once UG ready" works.
3. **June–July 2026 — Ship Model A SaaS launch.** Per the existing 8-week plan in `market-readiness-2026-04.md` §7. Model B customers provide the first testimonials. UG incorporation target moved from August to **mid-June** (see `business_plan.md` and `funding.md`).
4. **Q3 2026 — Both channels in market.** Sequencing depends on validation outcome: if AWS Solution Provider validation succeeded, ship multi-client dashboard for the SaaS path; if not, polish single-tenant SaaS and lean on consultants + mid-market.
5. **2027 Q1 — Revisit Model C.** If 2+ enterprise self-hosted customers are asking for managed-private, build it then.

This sequencing changes the answer to "when is AxiaOps market-ready":

- **Self-hosted paid beta (Model B): ~4 weeks away.** 3 engineering weeks for packaging + 1 week for a friendly legal entity or accelerated UG.
- **SaaS paid beta (Model A): ~2 weeks of engineering + UG (which is the critical path — 5–7 weeks if rushed, not 12–14).**
- **Public SaaS launch: ~8 weeks** (unchanged).

The UG is now explicitly the limiting factor for SaaS, not the product. Model B is the product-limited path and ships first.

---

## 5. Pricing — GTM-Specific Observations

The pricing analysis in `market-readiness-2026-04.md` §5 is sound. Two additions specific to GTM execution:

### 4.1 The real Vantage comparison is the Pro tier

Vantage Pro is USD 30/mo up to USD 7,500/mo tracked spend. Many prospects will anchor our Starter against Vantage Pro. Response: "Vantage Pro tracks spend and shows reports. AxiaOps Starter (€79/mo, 3 accounts) **acts on** ghost resources — dismiss, snooze, audit. Different product, different outcome." Only works if the actor-attribution dismiss audit trail is shipped and demoable.

### 4.2 The MSP tier is the one to defend hard

Per `market-readiness-2026-04.md`, €999/mo for 30 accounts + €12 overage is the recommended MSP price. An MSP buyer will push for €15/account flat. Don't negotiate on per-account price — negotiate on included accounts (start at 30, go up to 50 for a larger commitment). Reason: base + overage prices a *growing* MSP correctly; flat per-account caps our upside as they scale.

### 4.3 Discount posture for design partners

Stick with the `business_plan.md` commitment: first 10 customers get 50% lifetime on Growth or Team. Add two rules not in the existing doc:

- **Never discount the MSP tier.** If an MSP asks for 50% off the €999 base, offer 30% off for year 1 only. MSPs who self-select into a discounted tier are not pilots — they are margin buyers and they will churn if their clients churn.
- **Never discount below 50%.** A 75% discount signals desperation and anchors future customers at the discounted price. If a prospect wants 75% off, they don't want the product at all.

---

## 6. Immediate Messaging Fixes (Blocks First Demo)

Before booking any demo, update these artifacts. Each is a 30-minute copy edit, not engineering work.

| File | Current claim | Fix |
|---|---|---|
| `docs/pitch.md` L18 | "Weekly digest built-in" in the custom-scripts comparison table | "Weekly digest — coming Q2 2026 (in development)." |
| `docs/pitch.md` L71–91 | IAM policy snippet lists 8 permissions | Replace with the actual permissions the provider uses — `services/ingestion/internal/provider/aws/` touches ElastiCache, OpenSearch, Redshift, SageMaker, DynamoDB, EKS, CloudFront, S3, Kinesis, Logs, ECR, SecretsManager beyond the 8 listed. Under-promising the IAM scope then requesting more permissions at connect-time burns trust. |
| `docs/pitch.md` L180 | "Phase 2 (Q3 2026) adds multi-account + Azure + GCP" | "Multi-account cross-account IAM: Q3 2026. Azure + GCP: Phase 4, 2028 (per `business_plan.md`)." |
| `docs/pitch.md` L40 | "Self-hosted — run entirely inside your own VPC" | Mark as **"Q2 2026 — packaging in progress. Pilots available for enterprise customers now."** Do not strike — per §4 this is a real Q2 2026 parallel GTM path. Until the Compose distribution is packaged and licensed, be explicit that it is a pilot arrangement, not a self-service option. |
| `docs/pitch.md` L121–126 | Pricing table shows Starter Free / Growth €49 / Scale €149 | Align with the recommended pricing in `market-readiness-2026-04.md` §5.2 (Free / €79 / €249 / €599 / €999). The current pitch underprices. |
| `docs/pitch.md` L161–163 | "Owner resolution — every ghost shows the responsible team" | Keep only if the `team` tag surfaces in the dashboard detail view. If not, change to: "Owner resolution is on the roadmap." Check `services/dashboard/src/screens/Detail*.jsx` before a demo. |
| `docs/pitch.md` L164 | "Delegation — assign a ghost to an engineer" | Not shipped. Strike or mark "roadmap." |
| `README.md` | (spot-check for same claims) | Same edits if present |
| Landing page | (when built) | Apply all of the above from the start |

**Why this matters more than it looks:** a prospect who reads `pitch.md` before a demo arrives expecting weekly digests, self-hosting, multi-cloud, and delegation. They see none of those in the product. Even if they liked the rest, the trust damage on Day 1 is real and compounding. These fixes are free and must land before the first external demo.

---

## 7. ICP + Channel Strategy

Refining `business_plan.md` §Target Market with specific channel plays.

### ICP-1 — Small-to-mid MSPs (5–30 AWS accounts) — Priority 1

**Why them first:** per-account pricing natively fits, dismiss+snooze solves a real client-reporting pain ("prove to your client you reviewed their environment"), MSP buyers move fast (CTO/Founder is usually the buyer, no procurement).

**Channels (ordered by expected CAC, lowest first):**

1. **Founder-led direct outreach (LinkedIn + email).** 10 target MSPs/week, 4 weeks. Target: 6 demos booked, 2 pilots started. Use the 2-page "AxiaOps for MSPs" PDF mentioned in the go-live checklist (needs to be written — ~4h).
2. **FinOps Foundation Slack — #msp channel.** Post one thoughtful case study per month (anonymised: "How we found €X in waste across N client accounts in 30 days"). Do not cold-drop link-only posts; the community punishes that.
3. **r/msp subreddit.** One post on "ghost resources in client AWS accounts" — not a product pitch, a technical walkthrough with a soft CTA at the end.
4. **German IT-Systemhaus directory.** ~500 qualified targets in Germany/Austria. Outbound via personalised email (not LinkedIn — Systemhäuser Founder don't live on LinkedIn).
5. **AWS Partner Network directory (deferred).** Only after we have 3 MSP references. APN listing without logos is noise.

**Deal motion:** 30-min demo → 14-day free trial on 2–3 client accounts → paid Growth or Team tier. Offer 50% lifetime on first 10 only.

**Expected conversion:** 6 demos → 2 trials → 1 paid. Target: 5 MSP customers by end of Q3 2026.

### ICP-2 — FinOps consultants — Priority 2

**Why them:** audit trail is their core value prop to clients ("I did the work and here's who approved what"). Gated on actor-attribution shipping.

**Channels:**

1. **FinOps Foundation Slack — #consulting channel.**
2. **LinkedIn thought-leader outreach** — 20 named consultants, personal message, offer free Team-tier pilot.
3. **FinOps X / FinOps Foundation events** in EU (cheapest Q3 2026 event: FinOps Summit Europe, Amsterdam). Don't sponsor; attend and have coffee with consultants.

**Deal motion:** consultant becomes a reseller-lite: they use AxiaOps under their engagement, pass cost through to client. Sell on Team (€599) tier.

**Deferred until:** actor attribution ships (Week 1 of launch plan).

### ICP-3 — Series A–C startups with €10K–€100K/mo AWS spend — Priority 3

**Why them:** fastest close cycle (CTO approves <€500/mo without finance), but biggest activation risk (no demo mode = they bounce).

**Channels:**

1. **Product Hunt launch** (Week 8 of the launch plan).
2. **Show HN** — one post, one technical writeup ("How we detect 15 types of idle AWS resources in Go").
3. **SEO content** — "idle EBS volumes," "AWS ghost resources," "stopped EC2 instances still billing." 1 post/week.
4. **YC Work at a Startup / YC Slack groups** — if founder has Yc connections.

**Gated on:** demo mode + IAM role wizard + landing page. Do not launch ICP-3 channels before Week 8.

**Deal motion:** self-serve signup → 14-day Pro trial → Starter or Growth. This is where free→paid conversion % is the KPI that matters most.

### ICP-4 — German Mittelstand & security-conscious enterprise — via Self-Hosted (Model B)

**Previously deferred. Now reopened via the self-hosted deployment path (§4).**

**Why they work when the deployment model is self-hosted:** the SOC 2 / DPA / procurement friction that kills SaaS enterprise deals largely evaporates when the customer runs the software in their own VPC. Data never leaves their infrastructure, so the GDPR processor-relationship conversation disappears, and the security-review checklist shrinks from "where does our data go" to "can we audit your container image." The latter is a conversation a small vendor can win.

**Profile:** Mittelstand and mid-market companies with €20K–€200K/month AWS spend, a DevOps function capable of running Postgres + Redis + a Go service, and a procurement process that prefers CAPEX-style annual contracts over monthly SaaS subscriptions. Typical regions: DACH (Germany / Austria / Switzerland), Benelux, Nordics.

**Why they self-select into Model B:** regulated industry (insurance, healthcare, fintech), existing internal policy against pasting cloud credentials into third-party SaaS, CISO requirements around data residency beyond "Frankfurt SaaS," or simply organizational preference for "we run our own infrastructure."

**Channels:**

1. **Founder network + warm intros from the first 2 MSPs.** Every MSP who uses AxiaOps has 1–2 clients who would self-host; ask for the intro after they've been happy for 60 days.
2. **Bitkom + Eco Association** (German digital industry associations) — targeted outreach to members with >250 employees. Listed in `business_plan.md` §Legal network, now a GTM channel.
3. **LinkedIn outreach to German/Austrian CTOs** with FinOps or cloud-cost pain. ~30 qualified targets/week via LinkedIn Sales Navigator.
4. **Defer AWS Partner Network / Marketplace** until Model A has 3 references — APN is not a fit for self-hosted.

**Deal motion:** 30-min intro call → technical review of Compose package + IAM policy → 2-week pilot on one AWS account in their infra → 12-month Self-Hosted Pro license at €24K/yr + €5K setup. Close cycle: 6–10 weeks.

**Expected conversion:** 20 outbound → 5 intro calls → 2 pilots → 1 paid. Target: 2 self-hosted customers by end of Q2 2026 (€48K ARR + €10K setup = **~€58K booked revenue by July 2026, potentially before the SaaS launch closes its first customer**).

**Why this is material to the overall plan:** two self-hosted customers at €24K/yr each = €48K ARR, which is more than the business plan's Week 12 SaaS target (€2.5K × 12 = €30K), booked earlier, with a stronger customer profile for later reference. This does not replace the SaaS path — it runs beside it and funds the runway while SaaS grows.

---

## 8. Competitive Response Playbook

What to do if a competitor moves during the next 90 days.

| Scenario | Likelihood | AxiaOps response |
|---|---|---|
| **Vantage ships structured dismiss-with-reason** | Medium | Ship actor-attribution + per-resource history modal within 30 days. Emphasise MSP multi-account (their weak spot) in every demo. |
| **Vantage ships MSP reseller tier** | Low | Accelerate white-label roadmap (logo + custom domain on PDF reports). Lean harder on EU/GDPR positioning. |
| **Unusd ships Azure/GCP** | Low | No change to our plan. Azure/GCP is not a Q2 2026 fight we can win. |
| **AWS bakes dismiss/snooze into Trusted Advisor** | Low (2026), Medium (2027) | Existential for the AWS-only pitch. Mitigation: accelerate Azure/GCP roadmap into 2027, not 2028. |
| **AWS discounts Business Support aggressively** | Low | Minor impact; our ICP pays for support anyway. Emphasise the multi-account view. |
| **CloudHealth launches an SMB-priced tier** | Low (brand dilution risk for them) | Likely a full-feature tier at €500+/mo. We stay cheaper and faster. |
| **A YC or funded startup launches a near-clone** | Medium (this space is hot) | Ship faster. Lock in MSP customers with annual contracts + lifetime discount before they can. |
| **Stripe/AWS Marketplace listing competitor shows up** | Medium | Get AxiaOps on AWS Marketplace by end of Q4 2026. Deferred per business plan; now moved up. |

**The single biggest competitive risk is not any of the above — it is AxiaOps losing 6 weeks to Stripe integration and shipping in August instead of June.** Velocity is the only defensible moat at this stage.

---

## 9. Go / No-Go Gates by Launch Milestone

Four distinct launches, four distinct readiness bars. Do not conflate them. With the parallel-paths strategy from §4, Milestone A now splits into two channels — A-Self-Hosted and A-SaaS — that can ship independently.

### Legal & corporate gate — applies to all milestones

These are the *non-engineering* blockers. They must be scheduled as a project this week, not deferred to August 2026.

- [ ] **Steuerberater engaged** (target: this week). Candidates: FASTDOCS, Klooger & Partner, or Mittelstandsberatung Berlin. Fee: €800–€2,500 setup + €150–€300/mo ongoing.
- [ ] **Operating UG incorporated** (target: 5–7 weeks from kick-off, not the business plan's August 2026). Notary + €1 minimum share capital + Gesellschaftsvertrag + Handelsregister entry. Use an "UG Gründung Express" service (€300–€800) if doing solo.
- [ ] **Business bank account** (target: parallel with UG, finish within 1 week of Handelsregister). Qonto (fastest, ~3–5 days), Fyrst, or Commerzbank.
- [ ] **USt-ID (VAT) registered** (target: 2–4 weeks after UG, Finanzamt-paced — not compressible).
- [ ] **ToS, Privacy, DPA templates** drafted (iubenda or similar, ~€200/yr).
- [ ] **Holding GmbH** — not a launch gate; can follow 3–6 months after the UG. Do not let Holding setup delay the UG.

**If UG slips past end of June 2026:** fall back to one of three bridges — (a) run design-partner pilots free and back-bill on UG completion, (b) invoice short-term via a friendly existing GmbH with a pass-through agreement, (c) operate as Einzelunternehmer for 2–3 early customers with an explicit "we're incorporating in parallel" disclosure. Option (c) is viable for self-hosted (Model B) annual licenses because the legal exposure is lower than recurring SaaS subscriptions.

### Milestone A-Self-Hosted — Enterprise self-hosted pilot (Model B)

**Target:** 2 paying enterprise/Mittelstand customers by end of Q2 2026, €48K ARR + €10K setup fees.

**Gates (all must be true):**

- [ ] Versioned Docker Compose distribution (`docker-compose.self-hosted.yml` + pinned image tags). (*~3d*)
- [ ] Install guide: requirements, one-command bring-up, smoke-test steps. (*~1d*)
- [ ] License-key mechanism (JWT signed by AxiaOps, validated on startup, expires annually). (*~2d*)
- [ ] Kinde tenant provisioning flow for self-hosted customers (manual via customer-facing form + internal runbook). (*~1d*)
- [ ] Self-hosted pricing page section (can be a section of main landing page). (*~0.5d*)
- [ ] License agreement template (~4 pages, lawyer-reviewed). (*External, ~1–2 weeks*)
- [ ] Legal/corporate gate resolved OR a working bridge per the fallback above.
- [ ] IAM policy doc published.
- [ ] Support email working.

**Realistic target date: end of May 2026** — this ships faster than Milestone A-SaaS because it sidesteps Stripe, production infra, and GDPR endpoints. Primary risk: legal entity. Primary upside: books ~€58K in first invoices before SaaS signs customer one.

### Milestone A-SaaS — Design-partner beta (SaaS, Model A)

**Target:** 5–10 MSPs, €0–€999/mo revenue per account.

**Gates (all must be true):**

- [ ] Dismissal actor-attribution: API writes user email to `dismissed_by` / `revoked_by`; dashboard displays it. (*~4h*)
- [ ] One working demo tenant with realistic fake data. (*~1d*)
- [ ] Landing page with pricing (Notion or Framer-hosted is fine for the beta). (*~1d*)
- [ ] Ability to manually bill via Stripe Invoice (not full integration — just manual invoicing via Stripe dashboard). (*~1h*)
- [ ] **Legal/corporate gate resolved** (UG + bank + USt-ID). This is the critical-path blocker — product is ~2 weeks, UG is 5–7 weeks.
- [ ] All three messaging fixes in §6 applied.
- [ ] IAM policy doc published (PDF or web page).
- [ ] Support email working (`support@axiaops.io` → founder inbox).

**Realistic target date: mid-to-late June 2026**, gated entirely on the UG timeline. Product-only readiness: mid-May 2026.

### Milestone B — Public paid launch (pricing page + signup)

**Target:** 20–50 paying customers, ~€5K MRR by Q3 2026.

**Gates (everything in A-SaaS, plus):**

- [ ] Full Stripe integration: checkout, portal, webhook, plan gating, trial logic. (*~3 engineering days per market-readiness §2.4*)
- [x] Production environment live (June 2026): Terraform, RDS, ECS Express, Secrets Manager, Grafana, alerts.
- [ ] Email + Slack digest alerts (core retention driver). (*~2–3d*)
- [ ] IAM cross-account role wizard (replace access-key paste). (*~2d*)
- [ ] GDPR data export + deletion endpoints. (*~1d*)
- [ ] Trust / security page published.
- [ ] Public changelog.
- [ ] Status page live.
- [ ] Docs site with IAM setup + FAQ.
- [ ] 90-second product demo video.

**Realistic target date: mid-July to mid-August 2026.**

### Milestone C — Broad public marketing (Product Hunt / HN / content push)

**Target:** 500+ landing-page visitors/week, 50+ free signups/week.

**Gates (everything in B, plus):**

- [ ] 3 real customer testimonials + logos on landing page.
- [ ] 2 published case studies ("How MSP X found €Y in waste").
- [ ] Comparison page (AxiaOps vs. Vantage, AxiaOps vs. Trusted Advisor).
- [ ] Free→paid conversion measured for at least 2 weeks on Milestone B (baseline).
- [ ] NPS survey wired (Delighted or similar).
- [ ] Onboarding email sequence written (5 emails).

**Realistic target date: Q4 2026.** The business plan's October 2026 invoice target is a Milestone A event (either A-Self-Hosted or A-SaaS), not a Milestone C event. Don't confuse them.

---

## 10. GTM Risk Register (Distinct from Engineering Risks)

The engineering risk register in `market-readiness-2026-04.md` §8 is comprehensive. These are the *market-facing* risks that are ours to own, not the engineers'.

| Risk | Likelihood | Impact | Owner | Mitigation |
|---|---|---|---|---|
| **UG incorporation slips past end of June 2026** | **High (if Steuerberater not engaged this week)** | **Very High** — blocks first SaaS invoice | Founder | Engage Steuerberater within 7 days; use UG-Express service to compress to 5–7 weeks; parallelise bank + USt-ID applications. Fallback: Einzelunternehmer for self-hosted annual contracts only (§9 fallback). |
| **Self-hosted support burden swallows engineering time** | **Medium (if Model B scales past 5 customers)** | **High** | Founder | Hard-cap Model B at 5 customers in 2026. Charge setup fees (€3K–€10K) that fund support. Only sell to customers with internal DevOps capability. Single-node Compose only; no Helm support until 2027. |
| **Self-hosted customer asks for features that fork the codebase** (air-gapped auth, custom detection rules) | **Medium** | **High** — could create a branch we can't maintain | Founder | Explicit license terms: no source access, no custom builds. Escalate custom asks to Enterprise tier (€48K+/yr). Never fork the codebase for one customer. |
| Pitch/landing page claims features that don't ship (digest, Azure, delegation, owner resolution) | **High (today)** | **High** | Founder | §6 messaging fixes before first demo. Audit `pitch.md` + `README.md` this week. Self-hosted can now be claimed honestly as "Q2 2026 pilots" per §4. |
| First demo includes audit-trail pitch before actor attribution ships | **High (today)** | **High** | Founder | Don't demo the audit trail until Week 1 fix lands. Script the demo around dismiss+snooze+trend only. |
| First 10 design-partner MSPs churn at month 3 because no alerts | **High** | **High** | Founder | Ship digest alerts by Week 3 of the launch plan. Commit to first-10 customers in writing that alerts land by day 30 of their subscription. |
| MSPs want white-label before it's built | **Medium** | **Medium** | Founder | Pre-sell it as a Team+ tier feature, ETA Q3 2026. Ship a minimum version (logo + custom domain on CSV/PDF) in Week 4 if it blocks a deal. |
| Free-tier users never convert | **Medium** | **Low** | Founder | KPI at Week 12: free→paid <1% means Free tier value is too high or paid tier value is too low. Trim Free tier features if needed (reduce trend retention from 7 to 3 days). |
| Pricing tested too low, customers happy but margin insufficient | **Medium** | **Medium** | Founder | Raise Starter €79 → €99 at customer 20. Grandfather first 20. |
| Pricing tested too high, conversion is 0% | **Medium** | **High** | Founder | If free→paid <1% at Week 8, test a 20% price cut for 30 days on one tier (Growth). Do not discount MSP or Self-Hosted. |
| Competitor outspends on SEO/content | **Low (now), Medium (2027)** | **Medium** | Founder | Double down on niche content (MSP-specific + EU-GDPR-specific + self-hosted-specific) — broad SEO is a losing fight. |
| EU GDPR complaint from a free-tier user before GDPR endpoints ship | **Low** | **High** | Founder | Delay EU free-tier signup collection until GDPR endpoints are live. Alternatively, free tier available to US/non-EU only at first. (Self-hosted customers sidestep this entirely.) |
| Founder burnout during 8-week sprint kills GTM velocity | **Medium** | **High** | Founder | Hard-cap work week; pre-schedule 1 full day off/week; use a contractor for landing-page design (not founder time). Pick one path (SaaS *or* self-hosted) to drive each week — don't context-switch both simultaneously. |
| First serious customer wants self-hosted — **previously a loss, now a win** | **Medium** | **High (positive)** | Founder | Route to Model B (§4). Self-Hosted Pro at €24K/yr + €5K setup. Target close cycle: 6–10 weeks. |
| Legal entity bridge (Einzelunternehmer → UG) creates a weird contract transition | **Medium** | **Medium** | Founder | Pre-draft the "assignment of contract" letter template. Every early contract includes a clause: "Seller may assign this agreement to a successor entity without buyer consent during formation period." Steuerberater reviews wording. |
| Product Hunt launch flops (ICP-3 path fails) | **Medium** | **Low** | Founder | Minor impact — ICP-1 (MSPs) + ICP-4 (self-hosted enterprise) are the revenue paths. Treat PH as a brand/SEO asset, not a revenue channel. |
| Pricing page discourages Starter conversions | **Medium** | **Medium** | Founder | Offer Free→Starter upgrade incentive (first month 50% off if upgraded within 14 days of signup). |

---

## 11. KPIs — What to Measure From Day 1

Two dashboards: one **founder-facing** (weekly review), one **customer-facing** (on the landing page, updated monthly).

### Founder-facing (weekly, Notion or Airtable)

**Activation funnel:**
- Landing page → signup conversion — target 3%
- Signup → AWS account connected — target 60%
- Account connected → first ghost surfaced within 5 min — target 95%
- Free → trial → paid — target 8–12% (below 5% means positioning/pricing is off)

**Engagement:**
- WAU/MAU — target 0.5+
- Dismiss actions per week per active account — target 3+ (proves workflow fit, not just dashboard fit)
- Alert open rate (once alerts ship) — target 25%

**Revenue:**
- MRR (target Week 12: €2.5K; Week 24: €10K)
- Paying customers (target Week 12: 8; Week 24: 30)
- Net dollar retention — track starting month 3
- Gross churn — target <3%/mo; if >5% investigate immediately

**Quality:**
- P1 incidents — target 0
- Scan success rate — target >99%
- p99 API latency — target <500ms
- Support response time — <8h business hours

### Customer-facing (landing page, updated monthly)

Pick 2–3 of these depending on momentum:
- "€X in waste surfaced across all customer accounts last month"
- "N customer accounts monitored"
- "N dismissed resources logged and audit-trailed"

Do **not** publish MRR or customer count publicly in year 1. Too many competitor-weaponisable datapoints.

---

## 12. Kill-Criteria (When to Pivot)

Knowing when to stop is as important as knowing when to push. Pre-commit to these now, review monthly.

**After Week 12 post-launch (Q3 2026):**

| If this is true | Then consider |
|---|---|
| MRR < €1,500 | Pricing is wrong, or ICP-1 is the wrong ICP. Test ICP-2 (consultants) as primary for 4 weeks. |
| Free → paid < 1% | Free tier is too generous or paid tier value is unclear. Cut Free tier retention to 3 days; add "Upgrade to unlock dismiss workflow" gate. |
| Gross churn > 10% in first 90 days | Retention problem (almost always: no alerts). Ship alerts immediately regardless of other priorities. |
| 0 MSP customers signed | The MSP pitch is wrong. Re-run customer interviews (5 MSPs, 30-min calls) to recalibrate. |
| 3+ lost deals to Vantage on the same reason | That reason is a must-fix. Prioritise the fix over new feature work. |

**After Week 24 post-launch (Q4 2026):**

| If this is true | Then consider |
|---|---|
| MRR < €5K | Strategic rethink: is the SaaS model right? Could MSP-reseller be a better model (license AxiaOps to MSPs who rebrand)? |
| Total AWS-spend-influenced by AxiaOps customers < €200K/mo | The ICP is not spending enough on AWS to justify our price. Move upmarket — drop the Starter tier and lead with Growth. |

---

## 13. Recommended Next Actions (This Week)

In priority order, things a founder can do today without waiting for engineering:

**Legal (critical path — every day of delay pushes first SaaS invoice):**

1. **Contact 2–3 Steuerberater today; book the first meeting for this week.** Target criteria: experience with SaaS / UG Gründungen / international founders. Fee range: €800–€2,500 setup. Without this, every GTM action below is downstream of an unresolved 5–7 week blocker.
2. **Pick a UG-Gründung service or notary.** Fastest-path options: firma.de (€249–€599), Ecovis, or a local Berlin/Frankfurt notary. Target notary appointment by end of Week 2.
3. **Draft a one-paragraph "formation-period clause"** for every early customer contract: seller may assign the agreement to a successor legal entity during the UG formation period. 30 min with the Steuerberater.

**Messaging & collateral (unblocks demos):**

4. **Audit `pitch.md` and apply §6 messaging fixes** — 1 hour. Key changes: Azure/GCP → 2028; weekly digest → roadmap; self-hosted → "Q2 2026 pilots available."
5. **Apply the same messaging fixes to `README.md`** — 30 min.
6. **Draft the "AxiaOps for MSPs" 2-page PDF** — 4 hours. Use the feature matrix in §2 and the positioning one-liner in §3.
7. **Draft the "AxiaOps Self-Hosted" 2-page PDF** (new) — 3 hours. Target ICP-4 per §7. Emphasise data-never-leaves-your-VPC, annual license, one-command deploy.
8. **Book a Notion or Framer landing page build** — 2 hours skeleton; pay a freelance designer €500–€1,500 for polish by Week 4. Show both SaaS tiers and a "Self-Hosted — contact sales" block.

**Outreach prep:**

9. **Build the 10-target MSP outreach list (ICP-1)** — 2 hours. Criteria: EU MSPs managing 5–30 client AWS accounts, founder-led, < 50 employees.
10. **Build the 10-target self-hosted list (ICP-4)** — 2 hours. Criteria: DACH Mittelstand with 200–2,000 employees, public cloud-heavy workload, known security sensitivity. Sources: Bitkom member directory, Handelsblatt "Best in Cloud" winner list, XING InsiderKnowledge.
11. **Draft two outreach email templates** — 2 hours total. One MSP-angle ("find ghost spend across your client portfolio"), one enterprise-angle ("cloud-waste detection that runs in your VPC").
12. **Write the first FinOps Foundation Slack post** — 2 hours. Don't post yet; draft and let it sit 48h before publishing.

**Things NOT to do this week:**

- Do not start the Stripe integration (engineering, not GTM, and it's a 3-day block that pulls the founder away from sales prep *and* from legal).
- Do not start Azure or GCP provider code (2028 per business plan).
- Do not start the self-hosted Compose packaging yet (Week 2–3 work, blocked on license-terms decisions made with Steuerberater this week).
- Do not sign up for Product Hunt launch slot (premature — ~Week 8).
- Do not write the comparison page (AxiaOps vs. Vantage) before 3 customer conversations have validated what customers actually compare.

---

## 14. Closing Assessment

AxiaOps has the hard part done: a real detection engine against real AWS accounts, a workflow layer that no direct competitor has built, clean code, multi-tenant security done right, and — when the actor-attribution gap closes — an audit-trail pitch that lands. The product can absolutely be sold.

The critical re-framing from the first draft of this assessment: **two deployment models, two ICPs, two sales motions running in parallel.**

- **SaaS (Model A) for MSPs.** The plan in `market-readiness-2026-04.md` §7 is right. The product is 2 weeks away. The blocker is the UG, not the code — start the Steuerberater conversation this week.
- **Self-hosted license (Model B) for German Mittelstand and security-conscious enterprise.** 3–4 weeks of engineering to ship a Compose distribution with a license key. Sidesteps Stripe, production infrastructure, and GDPR endpoints. Can plausibly book €58K in first-invoice revenue before SaaS closes customer one.

**The three GTM claims to ruthlessly protect:**

1. *"Detect + dismiss + snooze + audit — as a workflow, not a report."* This is the one thing no one else ships.
2. *"EU-first, Frankfurt, German-incorporated, GDPR-native — or self-hosted in your own VPC."* A real moat against US-primary Vantage. The "or self-hosted" half was missing from the first draft and is the ICP-4 unlock.
3. *"Per-account pricing for MSPs, annual license for enterprise."* Two pricing models matched to two buyer mental models.

**The three GTM claims to drop immediately:**

1. *"Weekly digest built-in."* Not shipped. Roadmap language only.
2. *"Multi-cloud today."* Not shipped, not 2026, not clearly 2027. Phase 4, 2028 per `business_plan.md`.
3. *"Delegation / owner resolution in the dashboard."* Not shipped. Strike or mark roadmap.

**The three things that must happen in the next 14 days or the whole plan slips:**

1. **Engage a Steuerberater and begin incorporation.** Every week of delay is a week pushed on the first SaaS invoice. The business plan's August target has ~2 months of slack that can be recovered. See §15 for the UG-vs-GmbH structure decision — it materially changes the total tax + fee cost over the company's life and should not be deferred to "after incorporation."
2. **Book 5 conversations with target customers** — 3 MSPs (ICP-1) and 2 Mittelstand CTOs (ICP-4). The fastest way to invalidate pricing theory is to try to sell.
3. **Decide now: parallel paths (recommended) or single path.** The parallel-paths strategy has higher engineering cost but much shorter time-to-first-revenue and better customer diversity. The single-path (SaaS-only) strategy is simpler to execute but waits longer for cash and excludes ICP-4 entirely. This is a founder decision that can't be deferred.

If 3 of the 5 target conversations say "yes, we'd pay for this if it shipped," the pricing and positioning are validated. If none do, the §5 pricing tiers and §7 channel strategy in this doc are theory. Validate before shipping the landing page.

**Velocity is the moat. Conversations are the input. The incorporation is the critical path for SaaS; the Compose package is the critical path for self-hosted. Start both this week.**

---

## 15. Legal Structure Decision — UG, GmbH, and the Holding Question

The business plan's original structure is **Holding GmbH + Operating UG**. That is a reasonable default but it is not automatic, and two specific questions — raised in founder discussion after the initial draft — deserve a dedicated answer because they have **€10K–€400K in downstream financial consequences** depending on which way you go.

**Question 1:** Should the Operating entity be a UG (€1 stammkapital) or a GmbH (€25K stammkapital)?

**Question 2:** Should both entities be formed at the same time, or should the Operating entity be created first and the Holding added later once revenue proves out?

### 15.1 The "Operating now, Holding later" tax trap

This is the most expensive mistake a bootstrapped German founder can make. It looks like a prudent capital-efficiency move. It isn't.

If you set up an Operating GmbH (or UG) owned by you personally, and later transfer those shares to a newly-formed Holding GmbH, German tax law treats the transfer as a **sale at market value**. Market value at the time of transfer, not at formation.

Indicative tax owed on the transfer, assuming ~26.4% Abgeltungsteuer on 60% of the gain under Teileinkünfteverfahren (exact rate depends on structure and personal situation — confirm with Steuerberater):

| When you transfer | Operating entity value | Taxable gain | Tax bill |
|---|---|---|---|
| Month 0 (at formation) | €25K book | €0 | **€0** |
| Month 6 (~€1K MRR) | ~€50–100K | ~€25–75K | ~€6K–€20K |
| Month 12 (~€5K MRR) | ~€300–500K | ~€275–475K | **~€70K–€125K** |
| Month 24 (~€25K MRR) | ~€1.5–2.5M | ~€1.5–2.5M | **~€400K–€650K** |

You pay this **on paper gains** — the company hasn't sold, you haven't received cash. Founders call this the *kalte Progression* (cold taxation) trap.

### 15.2 The §20 UmwStG workaround and why it's not free

There is a legal mechanism (§20 Umwandlungssteuergesetz) to transfer shares into a Holding at **book value** rather than market value, avoiding the tax bill. It works, but introduces three real costs:

1. **Seven-year retention period.** If the Holding sells the contributed shares within 7 years, the transfer is retroactively re-taxed at the original market value. AxiaOps's target exit horizon is 3–5 years per `business_plan.md`, which means any exit triggers the retroactive tax. The workaround defeats itself at exit.
2. **Restructure fees.** €2K–€5K in Steuerberater fees to execute correctly. One documentation error and Finanzamt treats it as a market-value sale.
3. **Diligence complexity.** Every acquirer's tax team digs into a §20 structure at exit. More red flags, more negotiation leverage to the buyer.

**So §20 is viable but worse than doing it right the first time.**

### 15.3 The recommended structure for AxiaOps: Holding UG + Operating GmbH

**Default answer: Holding UG + Operating GmbH (Option B below).** The three-way comparison is worth walking through once, but for AxiaOps's specific situation — bootstrapped, exit-in-3-5-years, two deployment models with enterprise customers doing diligence on Operating, no planned VC round — Option B is materially stronger than the alternatives and should be the starting point of the Steuerberater conversation.

| Option | Holding | Operating | Upfront cash | Fit for AxiaOps |
|---|---|---|---|---|
| **A** | GmbH (€25K) | GmbH (€25K) | ~€25K* | Overspend — only right if fundraising within 12 months |
| **B — RECOMMENDED** | **UG (€1)** | **GmbH (€25K)** | ~€26K | **Captures 100% of the tax benefit; minimises upfront capital; Operating GmbH satisfies Model B enterprise diligence** |
| **C** | GmbH (€25K) | UG (€1) | ~€26K | Wrong-way round for AxiaOps — puts the UG form on the entity customers actually see |

*Option A upfront cash can be ~€25K total if the Holding's paid-in capital is routed into Operating as a capital contribution at formation (single pool of €25K funds both entities). Your liquid requirement is €25K, not €50K. But see below on why the extra administrative complexity usually isn't worth it for AxiaOps.

**Why the tax benefit is identical across all three options.** §8b KStG (the ~95% exemption on dividends and capital gains flowing to a corporate shareholder, yielding ~1.5% effective tax) applies to *any* corporate shareholder. Holding UG and Holding GmbH qualify identically. **The Holding form is not a tax decision.** It is a capital-efficiency and perception decision.

**Why Option B is the right default for AxiaOps:**

1. **Tax outcome identical to Option A.** You lose nothing on the single biggest financial lever in the company (§8b KStG on exit proceeds — ~€700K–€800K saved on a €3M exit). Both UG and GmbH Holdings capture it.
2. **Capital efficiency — €25K stays in your pocket.** Locking €25K in a Holding GmbH that exists only to own shares is real money that could instead be personal runway or Operating working capital. For a bootstrapped founder with 12–18 months of runway as the critical constraint, this is the decision variable.
3. **Customers never see the Holding.** The UG vs. GmbH perception argument matters at the Operating layer (customers sign contracts with Operating). It does not apply at the Holding layer. No customer email, invoice, DPA, or contract mentions the Holding — see §15.7 for the disclosure rules.
4. **Auto-conversion funds itself.** Operating GmbH's profits flow as dividends to Holding UG. The UG's mandatory 25% retention rule accumulates capital in the Holding; by the time Operating is throwing off meaningful dividends (year 2–3), the Holding UG has typically auto-accumulated most or all of the €25K needed to convert to GmbH. The conversion is paid for by the business, not by you.
5. **Exit diligence doesn't care.** When an acquirer buys Operating's shares from the Holding at year 3–5, they run diligence on the ownership chain, not on the Holding's stammkapital. A €1 Holding UG selling shares of a solidly-capitalised Operating GmbH raises no red flags because the acquirer is buying Operating, not Holding.

**Why Option A (Holding GmbH) is only right in specific cases.** Three scenarios tilt the answer to A:

- **Fundraising is realistic within 12 months.** VCs want to own shares in a GmbH. Restructuring a Holding UG → GmbH mid-fundraise costs time, dilution, and deal-terms leverage.
- **Multi-founder situation with ≥€50K aggregate capital.** The "save €25K" argument weakens when two founders each contribute €25K.
- **Very fast exit timeline (<24 months).** Not enough time for the UG to auto-accumulate capital; you'd convert manually near exit anyway.

None of these apply to AxiaOps today. If any become true later, Option B converts to Option A cleanly at the cost of ~€1,500–€3,000 in fees and 4–6 weeks — paid out of revenue by that point, not from personal capital.

**Why Option C (Holding GmbH + Operating UG) is wrong for AxiaOps.** This was the business plan's original default, defensible when AxiaOps was SaaS-only with pure-MSP ICP. It is no longer the right call now that Model B enterprise self-hosted is in play: the Operating UG is the entity signing €24K+ annual licenses with German Mittelstand customers who run Handelsregister lookups as standard procurement. A €1 stammkapital showing up in that lookup damages trust at exactly the deal size where trust matters most. Option C puts the UG form on the one entity where UG form costs you deals.

### 15.4 Capital-position sensitivity and when to delay incorporation

Option B's ~€26K upfront requirement (€25K for Operating GmbH, ~€1K for Holding UG setup + notary fees for both) is still meaningful. If liquid capital is below that threshold:

- **€20K–€26K liquid:** Do Option B anyway if personal runway is otherwise secure. The €5K gap to €26K is meaningful but manageable; the Holding structure's tax benefit (€700K+ on exit) dwarfs the short-term cash pressure.
- **€10K–€20K liquid:** Delay incorporation by 2–3 months. Run 1–2 self-hosted pilots as Einzelunternehmer (annual license contracts, assignment-clause per §13 action #3), using first invoice revenue (€15K+ per self-hosted pilot) to fund the structure. This sidesteps the capital constraint entirely and buys market validation before committing incorporation capital.
- **Under €10K liquid:** Do not incorporate yet. Either explore FFF (Friends, Family, Founders) or small-ticket angel funding to reach €25K, or pause the project until runway is in place. Running a GmbH with inadequate working capital is a worse problem than the short delay.

**Do not:** set up the Operating entity alone now, and plan to add the Holding later. The §20 UmwStG workaround and its 7-year lock-in is worse than the upfront commitment — see §15.1–15.2 above.

### 15.5 The right question to ask a Steuerberater

Do not ask "should I do UG or GmbH" or "should I form both now." Those questions get you a conservative default answer, not a quantified recommendation. Ask instead:

> *"I expect to exit this company in 3–5 years at a target valuation of €3–5M. I have €[X] in liquid capital available for entity formation. I want the Holding structure for §8b KStG treatment at exit, and I want to serve enterprise customers who will do due diligence on the Operating entity. My working assumption is Holding UG + Operating GmbH — capturing the full §8b treatment while minimising upfront capital, with a planned Holding UG → GmbH conversion once Operating dividends auto-accumulate the €25K. Is there any reason this is wrong for my specific tax residency, revenue outlook, or exit plan? If yes, what's the alternative and why does the trade-off favour it?"*

This framing forces a quantified answer with a 5-year horizon rather than a one-line rule of thumb. Expect a €800–€1,500 initial consultation; this is the single most financially consequential conversation you'll have about the company.

### 15.6 One more thing that's often overlooked

The **Holding GmbH/UG is formed in your name, not the company's name.** Founder-level tax residency matters. If you become a tax resident outside Germany before exit, the Holding structure's tax benefits can be partially or fully forfeited (Wegzugsbesteuerung — exit tax on unrealised capital gains when leaving Germany). If there's any possibility of relocating in the next 5 years, raise this with the Steuerberater explicitly. It's a solvable problem but only if planned in advance.

### 15.7 Which entity appears in customer-facing communications

**Rule: Operating entity only. Never the Holding.**

Under §35a GmbHG and §5 TMG, every business email, letter, invoice, contract, and website page must disclose the Operating entity's legal name (including "(haftungsbeschränkt)" for UGs), registered seat, Handelsregister court + number, managing director(s), and — once issued — the USt-IdNr. This disclosure applies to the entity that transacts with the customer. The Holding is a shareholder, not a party to the transaction, and has no disclosure obligation in customer communication. It also should not appear:

- **Email signature** — Operating only. One signature template, used everywhere.
- **Invoices** — Operating only (Stripe dashboard configured with Operating's details).
- **Website Impressum** — Operating only. Separate Impressum for the Holding is not needed unless the Holding runs its own public-facing presence (rare; usually it doesn't).
- **ToS, Privacy Policy, DPA** — Operating is the contracting party and data controller.
- **Support responses, marketing emails, LinkedIn posts, press releases** — Operating (or just "AxiaOps" as brand).

The Holding appears only in: shareholder resolutions, cap-table documents, investor agreements (if fundraising), share purchase agreements at exit, and internal tax filings. None of these are customer-facing.

**Einzelunternehmer bridge caveat:** if pilot contracts are signed before the Operating GmbH is incorporated (per §15.4 and §13 action #3), the signature and contract must reflect the founder as Einzelunternehmer — not GmbH or UG. Using "GmbH" before the Handelsregister entry is legally problematic under §4 GmbHG. Assign those contracts to the Operating GmbH at formation using the formation-period clause.

---

*Prepared as an internal GTM self-assessment. This document complements the readiness scorecard in `market-readiness-2026-04.md` by focusing on commercial execution: positioning, channels, messaging integrity, and launch gating. Where it differs from other docs, treat this as the current working view — pending validation from the first 10 customer conversations.*
