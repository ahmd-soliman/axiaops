# Feature Prioritization — 2026 Q2/Q3

**Status:** Draft for review
**Date:** 2026-04-28
**Context:** Planning the next 8–10 weeks of feature work, given the chosen GTM (VPC-native first, EU/Mittelstand wedge), current product state (MVP + Phase 2 in progress, cross-account role auth WIP), and zero paying customers as of writing.

---

## Decisions captured here

This document ranks three feature areas the team is considering, identifies higher-priority work that should ship first, proposes an 8–10 week sequence, and lists what to defer indefinitely.

The three options under explicit consideration:

1. **BYO SSO auth** — support OIDC and SAML, allow toggling Kinde off.
2. **Multi-cloud** — add Azure and GCP detection alongside AWS.
3. **Enterprise integrations** — Microsoft Teams, Slack, and Jira.

Cross-account IAM role onboarding is treated as already shipped or near-shipped (WIP at time of writing) and is not re-evaluated here.

---

## Ranking of the three options

### 1st — BYO SSO (OIDC / SAML)

**Verdict:** Highest priority of the three. Non-negotiable for VPC-native enterprise sales.

**Why:**

- Mittelstand IT departments and regulated EU buyers will not accept a self-hosted product that requires a third-party SaaS auth provider (Kinde) to function. "Why does your software phone home to a US identity service?" is a deal-killer in the first procurement review.
- Customers expect integration with their existing identity provider — Okta, Azure AD, Keycloak, ADFS. OIDC covers ~90% of these. SAML covers the remaining ~10%, mostly older or larger enterprises with existing SAML federation.
- Without BYO SSO, the entire VPC-native GTM is incoherent: a "self-hosted" product that requires SaaS auth is not actually self-hosted.

**Scope:**

- Ship OIDC first. Most modern IDPs speak it. Lower implementation cost, broader coverage.
- Add SAML when a real customer asks for it. Don't pre-build.
- Keep Kinde as the default for SaaS mode and the public demo. BYO SSO is opt-in via configuration in self-hosted deployments.

**Effort estimate:** ~2 weeks for OIDC, +1 week for SAML when needed.

---

### 2nd — Slack + email subset of "enterprise integrations"

**Verdict:** Medium-high priority. Specifically the digest/notification subset, not the full integration suite.

**Why:**

- Notifications are currently claimed in marketing copy ("weekly digest", "Slack alerts") but not shipped. This is a credibility liability — every prospect who waits a week for the promised email and gets nothing concludes the company over-promises. Either ship or strip.
- Slack incoming webhooks are the simplest possible integration: one URL field per account, HTTP POST on event. Email digest via Resend or Postmark is comparable lift.
- Together these become a real retention lever — without them, customers churn silently when they forget to log in.

**What to ship:**

- Per-account Slack incoming webhook URL configuration.
- Weekly email digest summarizing new zombies, total monthly waste, week-over-week delta.
- Scan-failure notifications (email and Slack) so customers don't silently miss broken scans.

**What to skip:**

- **Microsoft Teams** — Adaptive Cards and the Teams app registration flow are meaningfully more work for marginal additional value at this stage. Defer until a customer specifically asks.
- **Jira** — niche, customer-specific configuration (project mapping, field schemas), and only valuable to customers who already have a remediation-ticket workflow. Defer until asked.

**Effort estimate:** ~1.5 weeks for Slack + email digest combined.

---

### 3rd — Multi-cloud (Azure / GCP)

**Verdict:** Lowest priority of the three. Defer until a paying AWS customer specifically asks.

**Why:**

- Each cloud takes 2–4 months of detection development. Different APIs, different cost models, different waste patterns. Azure has 30+ services worth covering; GCP similar.
- Most EU Mittelstand is AWS-heavy enough that AWS-only is acceptable for the wedge. The customer profile that's actually multi-cloud (large enterprise) has typically already adopted CloudHealth, Cloudability, or Vantage and is not the buyer being targeted.
- Both Azure (Microsoft Cost Management) and GCP (Recommender) ship native, free, built-in cost-optimization tools that handle the obvious cases. The competitive bar in those clouds is higher and the customer pull is lower.

**When to revisit:** When at least 2 paying AWS customers explicitly request Azure or GCP support, or when the AWS-native segment is saturated.

---

## Higher-priority features to ship first

These items are sequenced before any of the three options above because each unblocks a real revenue blocker. They are listed in priority order.

### A. VPC-native packaging

**What:** Docker Compose distribution, Helm chart, or Terraform module that takes a fresh customer environment from zero to running AxiaOps in under 30 minutes. Bring-your-own-Postgres or bundled. Bring-your-own-auth or bundled basic-auth fallback. Versioned releases with documented upgrade paths.

**Why first:** Without this, the chosen GTM (VPC-native) cannot be executed. This *is* the strategy in code.

**Effort:** ~2–3 weeks of focused packaging work. Mostly not new product — existing Docker Compose stack adapted for single-tenant deployment.

### B. License-key system

**What:** Startup-time validation against a small Lambda + DynamoDB endpoint hosted on the AxiaOps AWS account. Gates paid features, enforces seat counts, allows entitlement revocation. Optional opt-in telemetry beacon (version + heartbeat).

**Why second:** Tied to (A). Without this you can ship the package but cannot enforce paid tiers. Also the only ongoing infrastructure AxiaOps needs to maintain for the VPC-native business.

**Effort:** ~1 week.

### C. CloudFormation "Launch Stack" template for cross-account role onboarding

**What:** Extends the existing cross-account role WIP. Provides a public "Launch Stack" URL that provisions the IAM role, trust policy, external ID, and required permissions in the customer's AWS account in a single click. Customer copies the role ARN back into AxiaOps and the connection is live.

**Why third:** Today a prospect must create the IAM role manually. With this template, "trying it" to "scanning live data" goes from 10 minutes to 60 seconds. Largest single activation lever available.

**Effort:** ~2–3 days once the role provisioning flow is solid.

### D. Public demo deployment with nightly reseed

**What:** Public URL where a prospect can explore the product without connecting AWS credentials. Pre-seeded with the existing 41 zombies, 1,095 zombie snapshots (365 days × 3 dev accounts), and 5,110 cost records (365 days × 14 account-service rows) produced by `make seed` — exact counts and shape documented in [`docs/chart-sampling.md`](chart-sampling.md). Nightly cron resets state.

**Why:** Removes the single biggest objection in early sales conversations ("can I see what this looks like without giving you AWS credentials?"). Most prospects will not engage seriously without this.

**Effort:** Hours, not days. The seed script already supports remote staging seeding (`make seed-remote-staging`). Required work is the public deployment itself plus the reseed cron entry.

### E. Marketing site / landing page

**What:** One URL with hero copy, three feature blocks, pricing, demo link, "Request access" form. Vercel or Cloudflare Pages free tier hosting.

**Why:** Without this, no inbound. Without inbound, every customer is an outbound cold-email grind. Required for any go-to-market motion to function.

**Effort:** ~2–3 days.

### F. MSP multi-account aggregation view

**What:** Dashboard view that shows all connected AWS accounts at once, sortable by client/account, with consolidated waste totals and per-account drill-down. Currently the dashboard shows one account at a time.

**Why:** The chosen wedge is MSPs and FinOps consultants managing 5–50 client accounts. Today they would have to switch accounts manually one at a time — broken UX for the customer profile being targeted.

**Effort:** ~1 week of dashboard work.

### G. First-run empty-state polish

**What:** When the first scan completes, the dashboard should communicate impact immediately: "Found 12 zombies worth €1,247/month in 3 minutes." Polish loading states, results display, and the "you saved €X" moment. Test the first-five-seconds experience as if a paying prospect.

**Why:** The first scan is the activation moment. If it feels like a generic "scan complete, here's a list" UI, the prospect underweights the value. If it feels like a discovery, they convert. Cheap UX win with disproportionate impact on conversion.

**Effort:** ~3–5 days of UX care.

---

## Suggested 8–10 week sequence

| Week  | Focus                                                  | Deliverable                                                              |
|-------|--------------------------------------------------------|--------------------------------------------------------------------------|
| 1–2   | Demo deployment + landing page + empty-state polish    | Public demo URL, marketing site live, first-run UX polished              |
| 3–5   | VPC-native packaging + license-key system              | Docker Compose / Helm distribution + license validation endpoint        |
| 5–6   | CloudFormation onboarding template                     | One-click Launch Stack URL for cross-account role provisioning           |
| 6–8   | BYO SSO via OIDC                                       | Self-hosted deployments can integrate with customer Okta/Azure AD/Keycloak |
| 8–9   | Slack + email digest                                   | Notifications shipped; marketing claims become honest                    |
| 9–10  | MSP multi-account aggregation view                     | Dashboard rollup across all connected AWS accounts                       |

**Parallelism note:** Items in weeks 1–2 are mostly different work streams (deployment ops, frontend marketing, dashboard UX) and can run concurrently if more than one person is working.

---

## Deferred indefinitely

These items should not be touched until a paying customer or active enterprise pitch explicitly requires them. Each represents weeks-to-months of work that does not unblock revenue at this stage.

- **Microsoft Teams integration** — Adaptive Cards complexity not justified by retention impact at current scale.
- **Jira integration** — niche customer-specific workflow, ship when asked.
- **SAML auth** — OIDC covers ~90% of identity providers; ship SAML when a customer requires it.
- **Azure detection** — months of work, target customer (multi-cloud enterprise) is not the chosen wedge.
- **GCP detection** — same reasoning as Azure.
- **Automated remediation** (terminate, resize, snapshot+delete) — competitor differentiation but not required for detection-and-dismiss positioning. Defer until customers ask.
- **Cost allocation by team / cost-centre / chargeback** — would expand the buyer profile from FinOps consultant to enterprise finance, which is not the wedge. Defer.
- **SOC 2 audit** — start when MRR > €5K/month and an enterprise customer requests it.

---

## Risk assessment if any priority item is skipped

| Skipped item                          | Failure mode                                                                                                 |
|---------------------------------------|--------------------------------------------------------------------------------------------------------------|
| BYO SSO                               | First enterprise/Mittelstand prospect rejects in procurement review. VPC-native GTM unviable.                |
| Slack + email digest                  | Customers churn silently when they forget to log in. Marketing claim becomes a lie that surfaces in trial.   |
| VPC-native packaging                  | Cannot sell self-hosted at all. Forced back into SaaS-only path with longer ramp.                           |
| License-key system                    | Cannot enforce paid tiers. Customers run for free or piracy occurs.                                          |
| CloudFormation template               | Activation drops 10x. Every prospect requires hand-holding through IAM setup.                                |
| Demo deployment                       | Cannot answer "can I see it without connecting AWS credentials?" — first-call friction kills 50%+ of leads. |
| Marketing site                        | Zero inbound. Every customer is a cold-outreach grind.                                                       |
| MSP multi-account view                | MSP wedge customers reject the dashboard as unfit for their workflow.                                        |
| First-run empty-state polish          | Conversion drops post-trial. Prospects interpret "scan complete, 12 results" as utilitarian rather than valuable. |
| Multi-cloud (Azure/GCP)               | Lose multi-cloud enterprise prospects. Acceptable given they were not the chosen wedge.                      |
| Teams / Jira                          | Lose customers requiring those specific workflows. Acceptable; rare in target segment.                      |

---

## Reference

- AxiaOps GTM and customer profile: `docs/gtm_assessment.md`
- Existing seed data and demo capability: `scripts/seed_test_data.sh`, `Makefile` targets `seed`, `seed-remote-staging`
- Cross-account role design (WIP): `docs/cross-account-roles-design.md`
- Phase 2 status: `docs/PHASE2_STATUS.md`

---

**Next action:** Review and approve sequencing. Once approved, create GitLab issues for each item A–G with effort estimates and owner assignments. Items in weeks 1–2 can start immediately as parallel workstreams.
