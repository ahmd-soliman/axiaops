# Market Readiness & Pricing Assessment — AxiaOps

**Audience:** Internal / founder
**Date:** 20 April 2026
**Author:** Technical readiness review (candid, not marketing)
**Companion docs:** `docs/business_plan.md`, `docs/competition.md`, `docs/code_review_april_2026.md`, `docs/go_live_checklist.md`

> **TL;DR** — The product is technically ~85% shippable. The detection engine, multi-tenancy, dashboard, and remediation workflow are genuinely good. What's blocking a paid launch is **commercial plumbing** (Stripe, invoicing, trial gating) and **production deployment** (App Runner + RDS + Secrets Manager), not the product itself. Realistic paid-launch window: **8 weeks from today** (mid-June 2026) for a soft launch to 10 design-partner MSPs; **12 weeks (mid-July 2026)** for a public pricing-page launch. The pricing tiers in the business plan are close, but on review are likely **underpriced by ~30–50%** for the MSP and Team tiers relative to Vantage, nOps, and CloudZero benchmarks — detail in §6.

> **Status update (2026-06-11):** production has since shipped on **ECS Express** (the App
> Runner plan below is historical), and email/Slack scan digests, dismiss/snooze, and GDPR
> deletion + export are live. Stripe, self-signup, and the landing page remain open.

---

## 1. Executive Verdict

| Dimension | Score (0–10) | One-line verdict |
|---|---|---|
| Core product (detection + workflow) | **7** | Best-in-class detection + dismiss/snooze; audit-trail UI still missing actor attribution |
| Engineering quality & test coverage | **8** | 44% test LOC ratio, clean code, RLS done right |
| Production deployment readiness | **4** | CI exists, IaC partial, no live staging yet |
| Commercial plumbing (billing/trial/invoice) | **2** | Designed in `tmp/3.1-stripe-billing.md`, zero code shipped |
| Onboarding & cold-start UX | **5** | Account connect works; no demo mode, no empty-state, no in-product walkthrough |
| Compliance & security posture | **6** | AES-256-GCM, RLS, Kinde; missing SOC 2 roadmap visibility, audit log, GDPR endpoints |
| Documentation (customer-facing) | **3** | Internal docs are solid; no public docs, pricing page, or IAM setup guide |
| Sales & marketing readiness | **2** | Business plan exists, but no landing page, no case studies, no waitlist |
| **Overall market readiness** | **5.5** | Engineering is ahead; GTM and billing are the drag |

**Honest one-paragraph summary:** AxiaOps has cleared the hard technical problems (multi-tenant SaaS, encrypted credential storage, real AWS integration across 15+ resource types, a polished remediation workflow). What remains is the *boring but blocking* work: a Stripe integration, a production environment, a landing page with pricing, an IAM setup guide, and a handful of onboarding polish items. None of these are technically risky — they are 6–8 weeks of focused execution away. The biggest risk right now is **not technical debt but commercial inertia**: the product could be shipped to paying users, but the billing/GTM scaffolding to charge them does not exist yet.

---

## 2. Readiness Scorecard — Detailed

### 2.1 Product readiness

**What's shipped and working:**

The detection engine covers 15 AWS resource types across two tiers. Tier 1 (CloudWatch-based) handles EC2 idle instances, RDS abandoned databases, unused Lambda functions, abandoned ELBs, and unused NAT Gateways. Tier 2 (API-only, no CloudWatch cost) catches unattached Elastic IPs, unattached EBS volumes, orphaned snapshots, long-stopped instances, and unused AMIs, plus ElastiCache, OpenSearch, Redshift, SageMaker, DynamoDB, and EKS. This is a genuinely broad surface area for an MVP — broader than Unusd, comparable to the detection subset of Vantage's Autopilot.

The remediation workflow is the strongest differentiator. Dismiss-with-reason (intentional idle, scheduled deletion, false positive, cost accepted, other), snooze (1/7/30/90 days), and revoke are all shipped and tested. This is the feature AWS Trusted Advisor and GCP Recommender don't have, and it's the one that converts "interesting dashboard" into "workflow tool we can't live without."

**Caveat — actor attribution is not yet wired end-to-end.** The `dismissed_ghosts` table has `dismissed_by` and `revoked_by` columns, but the API handler currently writes the organization ID into those fields instead of the authenticated user's email (see `services/api/internal/api/handler.go:565` and `:595`, both flagged with a `// swap for user email when available` comment). The dashboard also does not render actor info anywhere — there's no "dismissed by X on Y" line in the detail view, and no per-resource history modal. Net effect: the schema supports a full audit trail, but today a customer cannot see who on their team approved what. The selling point "MSP shows the client a report proving who approved each dismissal" is not true in the product as it ships right now. Fix is small (see §6.2 and Week 1 of §7): ~4 hours of backend + UI work.

The dashboard has 6 screens (Login, Connect AWS, Dashboard, Trend, Detail, Account Settings), is responsive, has dark-mode support, and has loading/error states. UX polish is respectable but not exceptional — sufficient for paid launch, would benefit from a second design pass before a wider public launch.

**What's missing from the product for paid launch:**

- **Demo mode** — No pre-loaded fake data for prospects to explore before connecting AWS. This is the single biggest activation risk, explicitly called out in the business plan as the "cold start problem." `services/ingestion/` already has a fake provider used for tests; wiring it to a demo organization is ~1 day.
- **Email/Slack alerts** — No digest, no new-ghost notifications. The business plan positions this as a core retention driver. Without it, users have to pull the dashboard manually, which kills weekly-active-user metrics. ~2–3 days (SES + Slack webhook + scheduler).
- **CSV export** — A Team-tier feature in the pricing plan that doesn't exist in code. ~0.5 day.
- **In-product IAM setup wizard** — The "paste your AWS access key" flow works but is not how security-aware customers want to connect. An IAM role with a cross-account AssumeRole + external ID is the industry norm; needs a guided wizard. ~2 days.
- **Empty-state messaging** — New accounts with zero ghosts see a blank list, not "nice work, no waste detected" or "scanning in progress." ~0.5 day.
- **Actor attribution on dismissals** — The schema stores `dismissed_by` and `revoked_by`, but the API writes organization ID instead of user email, and the dashboard doesn't display these fields at all. This breaks the audit-trail pitch. Fix: pull user email from JWT claims in `handler.go:565` and `:595`, surface it in the detail view, optionally add a per-resource history modal. ~4 hours end-to-end.

### 2.2 Engineering quality

The codebase is 14,227 lines of Go across three services, plus React for the dashboard. Test coverage is 6,278 LOC of test code — a 44% test-to-code ratio, which is excellent for a pre-revenue product. Integration tests run in an isolated Docker network with a fake AWS provider capable of generating 10,000+ resources for benchmarking.

Observed strengths: consistent use of `log/slog`, correct `%w` error wrapping, `context.Context` propagation everywhere, Postgres Row-Level Security on every table (no app-level organization filtering), AES-256-GCM for AWS credentials, and a clean handler pattern (`New(store) → Register(mux) → methods`). The go.work multi-module setup is tidy.

Known weaknesses (from `docs/code_review_april_2026.md` and the audit): a P1 memory leak in the in-memory rate limiter (bucket map grows unbounded — ~30 min fix, or moot once Redis limiter ships in Phase 2.14), five missing unit tests (logging init, RequestID middleware, ResetStuckScans, health failure path, Retry-After header — ~1–2 hours), no end-to-end load tests, and no Grafana/alerting wired despite Prometheus metrics being exposed.

None of these are shipping blockers for a design-partner beta. The rate-limiter leak is the only item that should be fixed before any real customer traffic.

### 2.3 Deployment & infrastructure

`docker-compose.yml` orchestrates a working local stack (Postgres, Redis, ingestion, api, dashboard). GitLab CI has four stages (test, integration, build, deploy) and pushes images to ECR. The `deploy:production` job is defined but has never been executed — there is no live staging environment and no App Runner service provisioned. There is no Terraform in the repo for RDS, ElastiCache, Secrets Manager, or App Runner itself. Secrets are currently managed via GitLab CI masked variables, which is acceptable for staging but should move to AWS Secrets Manager for production.

Realistic path to production:
1. Provision RDS db.t4g.micro (Multi-AZ deferred per cost targets) — ~2 hours.
2. Provision ElastiCache t4g.micro for Redis — ~1 hour.
3. Write Terraform for App Runner services (api, ingestion) — ~4 hours.
4. Move secrets to AWS Secrets Manager, update the Go services to fetch at startup — ~4 hours.
5. Configure a domain (axiaops.io likely already registered) with ACM certificate + Route 53 — ~2 hours.
6. Wire Grafana Cloud (free tier) to the Prometheus endpoints and set up 3–5 baseline alerts (error rate, DB saturation, scan failure rate) — ~3 hours.

**Total production-infra effort: ~2 engineering days.** The fact that this hasn't been done yet isn't a quality problem, it's a sequencing choice — worth doing now before Stripe, because billing webhooks need a stable public endpoint.

### 2.4 Commercial plumbing — the real blocker

This is the single largest gap.

| Component | State | Effort |
|---|---|---|
| Stripe Go SDK in go.mod | Missing | 5 min |
| DB columns: `organizations.plan`, `trial_ends_at`, `stripe_customer_id`, `stripe_subscription_id` | Missing (design doc only) | 1 hour (migration + RLS update) |
| Checkout session endpoint | Missing | 3 hours |
| Customer portal endpoint | Missing | 2 hours |
| Webhook handler (subscription.created/updated/deleted, invoice.payment_failed) | Missing | 4 hours |
| Plan-based middleware (enforce account count limit, auto-scan gating, CSV export gating) | Missing | 3 hours |
| Trial logic (14-day pro trial on signup, auto-downgrade) | Missing | 2 hours |
| Dashboard: billing page, plan card, upgrade CTA | Missing | 1 day |
| VAT / Umsatzsteuer-ID collection for EU B2B | Missing | 3 hours (Stripe Tax handles most of this) |
| Invoice PDF generation / email | Missing (Stripe handles it, but branding/footer needs config) | 2 hours |

**Total Stripe integration: ~3 focused engineering days.** This is the single highest-leverage item on the roadmap right now — without it, there is no revenue, full stop.

### 2.5 Security & compliance posture

Strengths: AES-256-GCM for customer AWS secrets, Kinde OAuth with RS256 JWT and JWKS verification, Postgres RLS enforcing organization isolation at the database level (not app level), read-only IAM policy for customer AWS access.

Gaps:

- **No SOC 2 roadmap publicly visible.** Business plan targets SOC 2 Type II in Q4 2027 but there is no Vanta/Drata/Secureframe vendor selected, no evidence collection, and no policy documentation. For SMB/MSP sales this is fine in 2026; for any enterprise conversation in 2027 it will be asked on day one.
- **No GDPR data-export or right-to-erasure endpoints.** Business plan and `tmp/3.10-gdpr.md` design it; nothing is shipped. For EU customers this is a real legal exposure if you start taking revenue before it exists. ~1 day to implement a basic export (JSON dump of organization data) and a deletion endpoint that cascades through foreign keys.
- **No audit log for account creation/modification/deletion.** Dismissals have an audit-trail schema (but see §2.1 caveat — actor is not populated correctly yet); account connect/disconnect has no audit log at all. ~0.5 day.
- **No credential rotation process documented.** If a customer's encrypted AWS secret is compromised (unlikely given AES-256-GCM, but still), there's no documented rotation procedure. ~0.5 day to write the runbook.
- **Encryption key in docker-compose.yml** is hardcoded for dev only, which is correct — but make sure the prod deploy reads `ENCRYPTION_KEY` exclusively from Secrets Manager and never from env-var injection that could leak into logs.

### 2.6 Onboarding & cold-start UX

The business plan correctly identifies this as the biggest activation blocker. Current state:

- Signup → Kinde OAuth redirect → organization auto-created → Connect AWS screen. That's good.
- Connect AWS screen asks for access key + secret. This is the weak link. Security-aware prospects will hesitate or bounce. The fix is a cross-account IAM role wizard with a one-click CloudFormation template, which is what Vantage and Datadog both use.
- No demo mode. A first-time visitor cannot explore the product without connecting a real account. The fake provider used in tests is perfect for this; wiring it to a `/demo` organization with realistic pre-scanned data is a ~1-day win.
- No in-product tour. Users who do connect an account land on the dashboard with no guidance on "here's what dismiss does, here's why it matters." Consider Shepherd.js or a manual tooltip-based tour. ~1 day.
- No "first-scan-in-progress" messaging. A new account takes 30–120 seconds to complete its first scan; users see an empty dashboard and assume the product is broken.

### 2.7 Documentation & sales collateral

Internal docs (`docs/*.md`, CLAUDE.md hierarchy) are thorough and well-maintained — this is an asset for onboarding future engineers, not customers. What customers don't have:

- No marketing landing page (axiaops.io likely points to nothing or a placeholder).
- No pricing page.
- No docs site (`docs.axiaops.io` or similar) with IAM setup guide, FAQ, integration guides.
- No public changelog.
- No case study, testimonial, or logo wall.
- No comparison page (AxiaOps vs. Vantage, AxiaOps vs. Trusted Advisor).
- No security/trust page (encryption, IAM scope, data residency, SOC 2 roadmap).

For a founder-led sale to 5–10 design-partner MSPs, the minimum viable collateral is: landing page with pricing, a 2-page IAM setup PDF, a trust page, and a 90-second demo video. All of this is non-technical work but takes 1–2 weeks of focused effort.

---

## 3. Competitive Positioning Reality Check

The business plan's competitive framing is accurate. Refinements based on April 2026 pricing data:

**Vantage** (the closest direct competitor):

- Starter: free, up to USD 2,500/month cloud spend tracked
- Pro: USD 30/mo, up to USD 7,500 spend
- Business: USD 200/mo, up to USD 20,000 spend
- Enterprise: negotiated, often % of tracked spend
- Autopilot (optimization): +5% of savings generated

**nOps:** flat fixed fee based on cloud spend for visibility; + % of savings for Autonomous Rate Optimization. 14-day free trial. Explicitly markets #1 G2 ranking.

**ProsperOps:** pure savings-share model (typically ~25% of savings generated, no fixed fee). Focused on commitment optimization, not idle detection — not a direct competitor but the pricing model is instructive.

**CloudZero:** enterprise-only, starts around USD 2k–5k/month. Out of AxiaOps's lane.

**AWS Trusted Advisor:** free basic tier, full tier requires AWS Business or Enterprise Support (USD 100/mo minimum, scales with AWS spend).

### What this tells us about AxiaOps pricing

1. The market has two dominant pricing axes: **cloud-spend-scaled** (Vantage, nOps visibility) and **savings-scaled** (ProsperOps, Vantage Autopilot, nOps Autonomous). AxiaOps's per-account pricing is a third model, closer to PagerDuty/Datadog.
2. Per-account pricing is defensible for the MSP niche specifically — MSPs think in terms of "how many client accounts," not "how much total spend." For mid-market DevOps teams, spend-scaled pricing is more natural, and a per-account model can feel arbitrary.
3. The business plan's Starter at €49/mo for 1 account is priced **above** Vantage's free tier and Pro (USD 30 for up to USD 7,500 spend). For a new entrant with no brand, this is aggressive. Recommendation: make Starter free (1 account, manual scans, 7-day retention) and move the €49 price-point up.
4. The business plan's MSP tier at €799/mo for 50 accounts = €16/account — this is almost certainly **too cheap**. An MSP with 50 client accounts managing €2M+ aggregate spend is pulling significant value from the product, and comparable MSP tooling (CloudCheckr MSP, Flexera) runs €3–8/account/month minimum for just reporting, let alone remediation workflow.

Specific pricing proposal in §6.

---

## 4. Ideal Customer Profile (ICP) — Sharpened

The business plan lists MSPs, FinOps consultants, and mid-market DevOps teams. In order of urgency/fit for the next 12 months of revenue:

### ICP-1 (highest priority): Small-to-mid MSPs managing 5–30 client AWS accounts

- **Why they're the best first customer:** They feel the pain on every client invoice ("why are we paying for this?"), the remediation workflow is directly sellable as a service to their clients, and they pay per-account pricing without flinching.
- **Where to find them:** FinOps Foundation Slack (specifically the #msp and #consulting channels), r/msp subreddit, AWS Partner Network directory (filter for "Solutions Provider" + small/mid-tier), German IT-Systemhaus association directory.
- **Expected deal size:** €399–€799/mo (Team or MSP tier).
- **Sales motion:** Founder-led, 30-minute demo, paid 30-day pilot on 2–3 client accounts, expand on proof.

### ICP-2: Independent FinOps consultants

- **Why they work:** They need a reporting tool that makes their hourly work visible and provable. The audit-trail pitch — "your client sees exactly who on your team dismissed, snoozed, and remediated each ghost" — is the hook, but it depends on fixing the actor-attribution gap called out in §2.1 before any demo to this ICP. Until then, lean on dismiss-with-reason + trend chart instead.
- **Deal size:** €149–€399/mo (Growth or Team).
- **Sales motion:** Community-led (FinOps Foundation, LinkedIn thought-leader outreach).

### ICP-3: Series A–C startups with €10K–€100K/mo AWS spend

- **Why they work:** Platform/DevOps team of 2–5 people, no dedicated FinOps function, CTO is accountable for cloud cost to the CFO. Buying decision is fast (CTO approves < €500/mo without finance sign-off).
- **Deal size:** €149–€399/mo (Growth or Team).
- **Sales motion:** SEO + content + Product Hunt launch + YC/Angel-investor network referrals.

### ICP-4 (deprioritize): Enterprises (>€200K/mo spend)

- **Why to wait:** SOC 2 Type II is table stakes, procurement cycles are 3–6 months, RFPs are brutal, and CloudHealth/Cloudability are entrenched. Revisit 2027 Q4 post-SOC 2.

---

## 5. Pricing Strategy — Recommended

### 5.1 Guiding principles

1. **Free tier exists for activation, not revenue.** A free tier removes friction for design partners and drives SEO-led signups. The goal is never to convert them — the goal is to let them generate pipeline for the paid tiers.
2. **Per-account pricing is the right model for MSPs.** Keep it.
3. **Mid-market teams prefer bundled tiers over spend-scaled pricing** for < €500/mo products. (Spend-scaled pricing is only natural when the buyer already thinks in spend terms, which is usually the case at €2k+/mo deal sizes.)
4. **Offer a savings-share add-on later**, not as the core model. ProsperOps-style savings pricing requires a trust relationship and audit-grade verification that AxiaOps does not yet have.
5. **Anchor prices in euros, accept USD at parity + ~5%.** The target market is EU-first; German/EU B2B buyers prefer EUR invoices with VAT handled by Stripe Tax.
6. **Annual contracts: 2 months free (16.7% off).** Industry standard. Do not offer steeper discounts — it signals desperation.

### 5.2 Recommended tier structure

| Plan | Monthly price | Annual price (2 mo free) | Accounts | Key gates |
|---|---|---|---|---|
| **Free** | €0 | — | 1 account | Manual scan only, 7-day trend retention, no alerts, community support, AxiaOps branding on shareable reports |
| **Starter** | €79 / mo | €790 / yr | 3 accounts | Daily auto-scan, 90-day trend, email alerts, CSV export, email support (48h) |
| **Growth** | €249 / mo | €2,490 / yr | 10 accounts | Hourly auto-scan, 365-day trend, Slack integration, dismiss audit trail export, priority email support (24h) |
| **Team** | €599 / mo | €5,990 / yr | 25 accounts | SSO (Kinde organizations), team management, tag-based filtering, PDF reports, dedicated Slack channel |
| **MSP** | €999 / mo + €12/account over 30 | €9,990 / yr base | 30 included, overage metered | White-label branding, reseller dashboard, client-facing reports, 20% partner margin on downstream |
| **Enterprise** | Custom (typically €2,500–€8,000/mo) | Annual only | Unlimited | SOC 2, custom DPA, SLA (99.9%), dedicated CSM, SAML SSO, custom detection rules |

#### Why this differs from the business plan's current tiers

- **Free tier added.** The business plan's €49 Starter is replaced by a Free tier (1 account, limited features) + a new €79 Starter (3 accounts, full features). This addresses the cold-start problem: prospects can connect one real account risk-free, see value, then upgrade. The €49 price-point was a dead zone — too expensive for curious evaluators, too cheap to signal value.
- **Prices raised across the board** (except Starter which is functionally new). The old pricing was ~30–50% below market for the Team and MSP tiers. Raising prices in early stage is very hard to do later; start higher and offer lifetime discounts to the first 10 customers (per the existing plan) rather than underpricing the public tier.
- **MSP tier restructured** with a base + overage model. €999 for 30 accounts = €33/account base; overage at €12/account. This more accurately captures value for MSPs who add clients over time.
- **Enterprise price disclosed in comments** (not on pricing page). "Contact sales" with a disclosed rough range avoids the two common failure modes: prospects assuming it's unaffordable (bounce) or assuming it's cheap (wasted calls).

### 5.3 Trial strategy

- **14-day Pro trial on signup**, no credit card required. Auto-downgrade to Free on expiry. This is the industry default (nOps, Vantage, Datadog all do this).
- **After downgrade, send a one-email sequence:** day 0 ("your trial ended, here's what you saw"), day 3 ("your ghost count is up by X since you stopped looking — re-upgrade here").
- **Do not gate the Free tier behind a trial.** Let people stay on Free forever; they become advocates and content for "X customers using AxiaOps" on the landing page.

### 5.4 Early-customer pricing (first 10 design partners)

The business plan commits to 50% lifetime discount for the first 10 customers in exchange for testimonials. Keep this. Concretely:

- First 10 paying customers lock in 50% off **Growth or Team** tier for the life of their subscription.
- No discount on Free, Starter, or MSP tiers.
- In exchange, contractually commit them to (a) a written testimonial within 60 days, (b) a logo on the landing page, (c) a reference call for 1 prospect in their first year.
- **Do not** publicly advertise the 50% discount. It's a closed-door offer to warm contacts. Public discounting damages the pricing anchor.

### 5.5 Savings-share add-on (Phase 4, post-revenue)

Once AxiaOps has ~€10K MRR and a track record of proved savings in customer audit trails, introduce an optional add-on: **"Guaranteed Savings" — 15% of verified monthly savings, billed on top of the base plan.** This is a premium option for CFO-led buyers who prefer outcome-based pricing. Gate it behind the Team tier. Audit trail makes verification straightforward. Don't lead with this — it's a 2027 offering.

### 5.6 Payment operations

- **Stripe Billing** for subscriptions, Stripe Tax for EU VAT.
- **Invoice branding**: AxiaOps logo, Operating UG legal entity, German VAT ID, €EUR default currency. Must be in place before the first invoice per the business plan's legal checklist.
- **Payment methods**: card (default), SEPA Direct Debit (EU B2B), bank transfer for Enterprise tier only.
- **Dunning**: Stripe's smart retries + email sequence; 15-day grace period before subscription cancellation on failed payment.
- **Refund policy**: 30-day money-back guarantee for first-time subscribers only; no refund on annual contracts after 30 days (prorated credit on downgrade).

---

## 6. Go-to-Market Readiness Checklist

Items required **before** the first invoice is issued. This is the "done means done" list.

### Legal & corporate (blocks revenue)

- Operating UG incorporated, IP assignment signed, bank account open (per business plan: target August 2026)
- Holding GmbH structure in place
- Umsatzsteuer-ID registered
- Steuerberater engaged for SaaS accounting
- Terms of Service, Privacy Policy, DPA drafted (template from iubenda or similar, ~€200/yr)
- Cookie banner / GDPR consent on marketing site (Cookiebot, ~€15/mo)

### Technical (blocks customer activation)

- Rate limiter memory leak fixed (P1 from code review)
- Dismissal actor attribution wired end-to-end (API writes user email to `dismissed_by`/`revoked_by`; dashboard displays it; optional per-resource history modal)
- App Runner production services deployed with Terraform
- RDS Postgres production instance with automated backups enabled
- ElastiCache Redis production cluster
- AWS Secrets Manager integration (replace GitLab CI env injection for production secrets)
- Domain + ACM certificate + Route 53 wired
- Grafana Cloud dashboards with 3–5 baseline alerts (error rate, DB saturation, scan failure rate, rate-limit rejections, auth failures)
- Stripe integration complete (see §2.4 breakdown)
- GDPR data export + deletion endpoints shipped
- Audit log for account create/modify/delete
- IAM cross-account role wizard (replace access-key paste form)
- Demo mode organization (fake provider data pre-loaded)
- Email/Slack digest alerts shipped
- In-product first-scan progress indicator
- Status page (Better Stack or Instatus, ~€20/mo)

### Sales & marketing collateral (blocks pipeline)

- Landing page: hero, the ghost number, detection screenshot, pricing, FAQ, trust signals, CTA
- Pricing page (can be a section of landing page initially)
- Trust/security page: encryption, IAM scope, data residency, SOC 2 roadmap
- Docs site: IAM setup guide, integration guides, FAQ, changelog
- 90-second product demo video (Loom or Arcade)
- 2-page PDF "AxiaOps for MSPs" (for partner outreach)
- Public changelog (`/changelog` route or Canny)
- Comparison page: AxiaOps vs. Vantage, AxiaOps vs. AWS Trusted Advisor
- Cold outreach templates (email + LinkedIn + Slack DM variants)
- 10-target MSP list for founder-led outreach
- FinOps Foundation Slack introduction post drafted and queued

### Support & operations (blocks retention)

- Support email (`support@axiaops.io`) wired to a shared inbox (Helpscout or Front, ~€20/mo)
- Onboarding email sequence (welcome, day-1, day-3, day-7, day-14)
- Runbooks: incident response, customer data deletion, credential rotation, on-call rotation
- SLA document (even for non-Enterprise — "best effort, 24h response during business hours")
- Known-issues document publicly visible
- Customer success NPS survey at day 30

---

## 7. 8-Week Launch Plan

This is the concrete sequence for a paid-launch by mid-June 2026.

**Week 1 (Apr 21–27): Unblock production**
- Fix rate limiter memory leak (0.5d)
- Fix dismissal actor attribution: write user email (not organization ID) to `dismissed_by`/`revoked_by`, surface "Dismissed by X on Y" in detail view, optional per-resource history modal (0.5d)
- Write Terraform for RDS, ElastiCache, App Runner (1d)
- Provision production infra, move secrets to Secrets Manager (1d)
- Wire axiaops.io domain + ACM + Route 53 (0.5d)
- Grafana Cloud dashboards + 5 baseline alerts (0.5d)
- Status page live (0.5d)

**Week 2 (Apr 28 – May 4): Stripe end-to-end**
- DB migration for plan/trial/stripe fields (0.5d)
- Stripe checkout + customer portal (1d)
- Webhook handler + plan-based middleware (1d)
- Trial logic + auto-downgrade (0.5d)
- Dashboard billing page (1d)
- Test with Stripe test mode end-to-end (0.5d)

**Week 3 (May 5–11): Onboarding polish**
- IAM cross-account role wizard (2d)
- Demo mode organization with fake provider data (1d)
- In-product first-scan progress indicator (0.5d)
- Empty-state copy across dashboard (0.5d)
- Email/Slack digest alerts (1d)

**Week 4 (May 12–18): Compliance + collateral**
- GDPR data export + deletion endpoints (1d)
- Audit log for account ops (0.5d)
- ToS, Privacy, DPA drafted (0.5d)
- Landing page live with pricing (3d — likely needs a designer or Framer template)

**Week 5 (May 19–25): Sales collateral**
- Docs site live with IAM setup guide, FAQ (2d)
- 90-second demo video (1d)
- Trust/security page (0.5d)
- Comparison page (0.5d)
- 10-target MSP outreach list built (1d)

**Week 6 (May 26 – Jun 1): Legal + infrastructure hardening**
- Operating UG incorporation finalized (depends on Steuerberater; started earlier)
- Support email + Helpscout wired (0.5d)
- Runbooks written (1d)
- Load test production infra (1d)
- Final bug bash + security review (2d)

**Week 7 (Jun 2–8): Soft launch to 10 design partners**
- Send cold outreach to 10 target MSPs (week-long drip)
- Book 5+ demos
- Offer 50% lifetime discount for first 10 customers
- Onboard first 2–3 customers to paid Growth/Team tier

**Week 8 (Jun 9–15): Public launch**
- Product Hunt launch
- Hacker News "Show HN"
- FinOps Foundation Slack announcement
- LinkedIn founder post + 3-part content series starts

**Target at end of Week 8:** 3 paying customers (~€1K MRR), 50 free-tier signups, 500 landing-page visitors/week, first public testimonial.

---

## 8. Risk Register

| Risk | Likelihood | Impact | Mitigation | Owner |
|---|---|---|---|---|
| Cold start: prospects won't connect AWS to unknown startup | **High** | **High** | Demo mode + IAM role wizard + published IAM policy + SOC 2 roadmap visible | Founder |
| AWS Trusted Advisor or Vantage ships equivalent remediation workflow | Medium | High | Move fast on MSP-native features (white-label, multi-client) — these are Trusted Advisor's weak point | Founder |
| Stripe integration delays push launch by 2+ weeks | Medium | Medium | Time-box to 3 engineering days, use Stripe Checkout (not custom card form), skip Stripe Tax complexity until post-launch | Engineering |
| Production costs spiral above €34/mo target | Low | Low | Alert on billing >€50, RDS db.t4g.micro only, no Multi-AZ until €5K MRR, App Runner scales to zero | Engineering |
| First customer discovers a P1 security bug | Low | **Very High** | External pen test (€2–5k) before first invoice; bug bounty via Intigriti after 3 paying customers | Founder |
| GDPR complaint from EU customer | Low | High | Data export + deletion endpoints live at launch; Frankfurt data residency; DPA template signed with every customer | Founder |
| Pricing set too low, leaves money on table | Medium | Medium | This doc's recommended pricing (§6) is ~30% above business plan; raise further after 20 customers if conversion stays strong | Founder |
| Pricing set too high, conversion collapses | Medium | Medium | Track free→paid conversion weekly; if < 2% at week 8, test 20% price cut for 30 days | Founder |
| Churn from first cohort due to missing alerts feature | Medium | Medium | Email/Slack digest is in Week 3 of the 8-week plan; do not skip | Engineering |
| Demoing audit trail before actor attribution is wired undermines the workflow-tool pitch | Medium | High | Actor-attribution fix is in Week 1; do not show the dismiss flow to an MSP prospect before it lands | Engineering |
| MSP partner asks for white-label before it's built | Medium | Low | White-label is Team+ tier spec; can ship basic version (logo + custom domain on reports) in 2 days when needed | Engineering |
| Kinde OAuth provider has an outage | Low | High | Add Kinde status page to incident response runbook; evaluate Auth0/WorkOS as backup in 2027 | Engineering |
| Founder burns out on 8-week sprint | Medium | High | Hard-cap work at 50 hours/week; pre-commit to a 4-day week in Week 7 after soft launch | Founder |

---

## 9. Post-Launch KPIs (First 90 days)

Track these weekly. Publish them to a private founder dashboard (Notion or Airtable).

**Activation funnel:**
- Landing page → signup: target 3%+ (industry benchmark 2–5% for B2B SaaS)
- Signup → AWS account connected: target 60%+ (business plan target)
- Account connected → first ghost surfaced: target <5 min, 95% of accounts
- Trial → paid: target 8–12% (below 5% means pricing or value prop problem)

**Engagement:**
- Weekly active users (WAU) / Monthly active users (MAU): target 0.5+
- Dismiss actions per week per active account: target 3+ (proves workflow use)
- Alerts opened (email click-through): target 25%+

**Business health:**
- MRR (target week 12: €2.5K per business plan)
- Paying customers (target week 12: 8–15)
- Net revenue retention: track starting month 3
- Gross churn: target <3% monthly
- CAC payback: target <4 months

**Quality signals:**
- P1 incidents: target 0
- p99 API latency: target <500ms
- Scan success rate: target >99%
- Support response time: target <8h business hours, <48h weekends

---

## 10. Open Decisions (Before Launch)

A founder-only list of things to decide, with a recommended default:

1. **Is the Free tier public or invite-only for first 3 months?** *Recommend public.* Invite-only creates false scarcity with no benefit; SEO depends on indexable signup.
2. **Do we ship a mobile app in v1?** *Recommend no.* Web-responsive is sufficient per the business plan; mobile is Phase 3.
3. **Do we accept crypto or only cards/SEPA?** *Recommend cards + SEPA only.* Crypto adds compliance headache for zero incremental demand in target ICP.
4. **Do we offer lifetime deals on AppSumo or similar?** *Recommend no.* AppSumo attracts the wrong buyer persona (lifetime-deal hunters, not MSPs) and damages pricing power.
5. **Open-source the detection engine?** *Recommend no for 2026.* Revisit when we have 100 paying customers; open-core could be a 2027 moat but is distracting now.
6. **Accept the Operating UG delay?** *Recommend rushing to July incorporation* — the business plan's August target pushes first invoice to October, which may slip further. Every week earlier that the UG is ready is a week earlier we can take revenue.
7. **Price in EUR only, or dual EUR/USD on the pricing page?** *Recommend EUR primary, USD toggle.* EU-first positioning, but Product Hunt and HN audiences are USD-first and will bounce on EUR-only.

---

## 11. Closing Assessment

AxiaOps is closer to market than the Phase 2 status tracker suggests — the product, detection engine, workflow, and engineering quality are all at the level of a credible paid-beta release. What's missing is the **commercial scaffolding**: a way to charge money, a public pricing page, a live production environment, and the onboarding polish that removes friction for the first 10 customers.

This is a tractable gap. It's eight focused weeks of work, priced in the order-of-magnitude of €0 in external spend (besides Stripe fees, Grafana Cloud free tier, ~€50/mo tooling total) and 1 founder + 0–1 contractors. Starting immediately puts a paid launch in mid-June 2026, design-partner revenue by end of Q2 2026, and the first serious MRR milestone (€5K) by end of Q3 2026 — consistent with the business plan's trajectory but ahead of the October 2026 revenue target by ~3 months.

The riskiest mistake right now is not technical; it's under-pricing. The business plan's €49 Starter and €799 MSP tiers are both priced as if AxiaOps were a commodity. The detection + remediation workflow is not a commodity — it's the best-in-class solution for a specific ICP (MSPs and FinOps consultants). Price accordingly from day one.

**Recommended next action:** Start Week 1 of the 8-week plan today. Specifically: fix the rate limiter leak this week, begin Terraform for production infra this week, and start drafting the landing page copy this weekend. Every day of delay is a day of competitor risk and compounding opportunity cost.

---

*This assessment was prepared as an internal founder self-evaluation. It is candid by design. Where the tone seems blunt, it's in the service of the outcome — a successful launch by mid-2026.*
