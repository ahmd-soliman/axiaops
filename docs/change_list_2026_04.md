# Change List — April 2026 Honest Review

**Audience:** Founder / internal
**Date:** 26 April 2026
**Source:** Distilled from a candid review covering `business_plan.md`, `competition.md`, `gtm_assessment.md`, `market-readiness-2026-04.md`, `funding.md`, `pitch.md`, `PHASE2_STATUS.md`, and the codebase.
**Purpose:** A single execution-ready checklist for the next 90 days. Companion to the longer assessment docs.

---

## TL;DR

The product is more shippable than the docs imply, but the pitch oversells what's built. The biggest risks right now are (a) ICP confusion (treating "MSPs" as one segment when it's three), (b) underpricing relative to market, (c) selling a roadmap not a product (multi-client dashboard, audit trail with actor, MSP white-label all unbuilt despite being pitched), and (d) UG incorporation timing slipping past August. Below is the change list to fix each, ordered by what unblocks the most for the least work.

---

## 1. Stop the bleeding (this week — ~1 day total)

1. **Fix actor attribution on dismissals.** In `services/api/internal/api/handler.go` ~lines 565 and 595, write the authenticated user's email (from JWT claims) into `dismissed_by` and `revoked_by` instead of tenant ID.
2. **Surface "Dismissed by X on Y" in `DetailScreen.jsx`.** Add a small audit-history list per resource.
3. **Patch the in-memory rate-limiter bucket map** so it gets pruned (P1 from the April code review).
4. **Strike unshipped claims from marketing copy.** Pass over `pitch.md`, `README.md`, any landing-page draft. Strike or reframe "weekly digest built-in," "Azure/GCP," "self-hosted today," "owner resolution," "delegation." Replace with "roadmap (date)" or remove. *Already partially done in `pitch.md` April 2026 honesty pass — finish the rest.*
5. **Reconcile the multi-cloud date contradiction.** Some docs said Q3 2026; `business_plan.md` says 2028. Pick 2028. Update everywhere.

## 2. Validate the ICP before building anything else (next 2 weeks)

6. Build a list of **30 EU AWS Solution Provider partners + 30 self-identified FinOps consultants** from FinOps Foundation Slack and AWS Partner Network directory.
7. Send 60 cold emails offering a 30-minute call and 90-day free pilot. Track replies, calls booked, pilots accepted.
8. **Decision gate after 14 days:**
   - Fewer than 8 calls = MSP/consultant ICP is wrong; redirect to mid-market DevOps; do **not** build the multi-client dashboard.
   - More than 12 calls = build the multi-client dashboard (5–6 weeks).
   - 8–12 = small consultancy beachhead; defer the multi-client dashboard and ship single-tenant polish instead.
9. **Rewrite `competition.md`** to include the MSP-specific incumbents (Spot.io's CloudCheckr MSP, Flexera, ConnectWise/N-able) and the adjacent tools (Datadog Cloud Cost, Cast.ai, Finout, AWS Compute Optimizer, OpenCost/Kubecost) that today's doc misses.

## 3. Legal and corporate (start this week, completes ~6 weeks)

10. **Engage a Steuerberater this week.** Don't wait for August.
11. **Decide (legal entity) vs Holding UG** based on available founder capital (see `business_plan.md` § (legal entity) vs Holding UG and `funding.md`).
12. **File the Holding** (GmbH or UG). Holding founds the (operating entity) as sole shareholder.
13. **Sign IP assignment** from individual to UG before any customer code or contract, dated to UG incorporation.
14. **Open business bank account** ((a business bank)).
15. **Register Umsatzsteuer-ID.**
16. **Get DPA, ToS, Privacy Policy** templates from iubenda or a Datenschutz-Anwalt (~€200/yr).
17. **Target completion: mid-June 2026**, not August.

## 4. Commercial plumbing (3 focused engineering days)

18. Add Stripe Go SDK to `go.mod`.
19. Migration: add `plan`, `trial_ends_at`, `stripe_customer_id`, `stripe_subscription_id` to `tenants` (or `organizations` if going multi-client).
20. Endpoints: `POST /v1/billing/checkout`, `POST /v1/billing/portal`, `POST /v1/webhooks/stripe`.
21. Plan-gating middleware: enforce account count, auto-scan, CSV export, alerts based on plan.
22. 14-day Pro trial on signup, auto-downgrade. No credit card required.
23. Dashboard billing page with plan card and upgrade CTA.
24. Stripe Tax for EU VAT, SEPA Direct Debit enabled.

## 5. Production deployment (~2 engineering days)

25. **Terraform modules:** App Runner (api + ingestion), RDS `db.t4g.micro` Single-AZ, ElastiCache Serverless or Upstash, Secrets Manager, ECR, VPC with public subnets only (no NAT GW).
26. **Secrets Manager:** move `ENCRYPTION_KEY`, `REDIS_URL`, `KINDE_*`, `RESEND_API_KEY`. Remove env-var injection from production deploy.
27. **Domain:** wire `axiaops.io` → ACM cert → Route 53 → App Runner.
28. **Grafana Cloud free tier** scraping `/metrics`, with 5 alerts: error rate, DB saturation, scan failure rate, rate-limit rejections, auth failures.
29. **Status page** (Better Stack or Instatus, ~€20/mo).
30. **RDS automated daily snapshots**, 7-day retention. CloudWatch log retention 7 days.

## 6. Onboarding fixes (~5 engineering days)

31. **Demo mode:** wire the existing fake provider to a `/demo` tenant with pre-loaded fake data. Public, no signup required.
32. **IAM cross-account role wizard:** replace the access-key paste form. Generate a one-click CloudFormation template per tenant with a unique `external_id`. Store role ARN instead of access key in `accounts` table.
33. **First-scan progress indicator:** poll account status, show "Scanning… typically 30–120 seconds" instead of empty dashboard.
34. **Empty-state copy** on the dashboard for "no ghosts found" and "scan in progress."
35. **In-product tour** or 5 tooltips on first login covering: connect, scan, dismiss, snooze, trend.

## 7. Alerts and retention (~3 engineering days)

36. **Resend integration** for email digests. Add `RESEND_API_KEY` env var.
37. **HTML weekly digest template:** ghost count, top 5 by cost, week-over-week delta from `ghost_snapshots`.
38. **Slack webhook per account:** migration to add `slack_webhook_url` column. Post on scan completion when ghost count changes.
39. **`POST /v1/settings/notifications`** for per-tenant toggle.

## 8. GDPR endpoints (~1 engineering day)

40. **`GET /v1/export`** — JSON dump of all tenant data: ghosts, accounts (no secrets), snapshots, dismissals, audit log.
41. **Verify `DELETE /v1/tenants/me` cascades cleanly** through every table. Test it.
42. **Audit log on account create/modify/delete** (the current audit log only covers dismissals).
43. **Document data retention periods** on the privacy policy page.

## 9. ICP-conditional build (only after step 8 decision gate)

**If validation says yes to AWS Solution Providers / specialist consultancies:**

44. New table: `organizations(id, kind, name, billing_plan, branding_json, created_at)`.
45. Add `organization_id` to `tenants`, backfill, make NOT NULL.
46. New `memberships` table joining users to org or tenant with role.
47. Update RLS policies to allow scoping by either `app.tenant_id` or `app.organization_id`. Expand `permission_matrix_test.go`.
48. New `ClientsScreen.jsx` with sortable client list. Tenant switcher in header.
49. PDF report generator (`gofpdf` or HTML-to-PDF via chromedp). Branded per organization. Cover page, savings number, top resources, dismiss audit trail.
50. Per-organization branding fields: logo URL, primary color, footer text.
51. Stripe subscription on organization, not tenant. Per-client overage billing.

**If validation says mid-market DevOps instead:**

44b. Skip the dashboard rebuild. Polish the single-tenant experience: multi-user invites with `admin`/`viewer` roles, Slack/email alerts, tag-based filtering, PDF report (single-tenant version).
45b. Build content for "DevOps team's first FinOps tool" rather than MSP-specific content.

## 10. Pricing changes (immediate, no engineering)

52. Replace tiers in `business_plan.md` and any pricing page with the recommendations from `market-readiness-2026-04.md` §5.2: Free €0 / Starter €79 / Growth €249 / Team €599 / MSP €999 + €12/account over 30. The current €49 Starter and €799 MSP are underpriced for the value.
53. Annual billing at 2 months free (16.7% off). Don't go deeper.
54. Lock in 50% lifetime discount for first 10 customers in exchange for written testimonial + logo + reference call. Don't advertise this publicly.

## 11. Sales collateral (~2 weeks of non-engineering work)

55. **Landing page at `axiaops.io`:** hero, "the ghost number" demo screenshot, pricing, FAQ, security/trust signals, CTA. Framer template is fine.
56. **Pricing page** (can be a section of landing initially).
57. **Trust/security page:** encryption details, IAM scope, EU data residency, SOC 2 roadmap honestly stated as "Q4 2027 target."
58. **Docs site at `docs.axiaops.io`:** IAM setup guide, FAQ, API reference, changelog.
59. **90-second product demo video** (Loom or Arcade).
60. **Comparison pages:** AxiaOps vs. Vantage, AxiaOps vs. AWS Trusted Advisor + Cost Explorer.
61. **2-page PDF** for the validation outreach in step 7.
62. **Public changelog** at `/changelog`.

## 12. Support and ops (~3 days)

63. `support@axiaops.io` wired to Helpscout or Front (~€20/mo).
64. Onboarding email sequence: welcome, day-1, day-3, day-7, day-14.
65. Runbooks: incident response, customer data deletion, credential rotation, on-call.
66. SLA document, even informal: "best effort, 24h response in business hours."
67. NPS survey at day 30 via a shared Notion or Tally form.

---

## Things to explicitly NOT do (resist these temptations)

68. **Do not build Azure or GCP detection in 2026.** Defer to 2028 as the business plan says.
69. **Do not pursue SOC 2 before €15K MRR.** The audit costs €15–€25K and won't move SMB/MSP deals.
70. **Do not build the mobile app.** Web-responsive is sufficient.
71. **Do not list on AppSumo or run lifetime deals.** Wrong buyer persona, damages pricing.
72. **Do not raise VC.** The funding doc has the right answer — bootstrap, founder loan, possibly Gründungszuschuss.
73. **Do not build Phase 5 cost-simulation / IaC parser features.** Infracost owns that lane and the runtime product needs revenue first.
74. **Do not write more strategy docs.** There are already 70+ in the repo. The next document should be a customer-facing changelog or pricing page, not another internal strategy doc.

---

## The five things that matter most

If everything above is too much to track, compress it to these five:

1. **Run the 60-prospect validation experiment** (steps 6–8) before any big build.
2. **Incorporate the Holding + (operating entity) immediately** (step 12), targeting mid-June not August.
3. **Ship Stripe + production deployment** (sections 4 + 5) in 3+2 days.
4. **Fix actor attribution and strike unshipped claims** from marketing this week (sections 1).
5. **Decide MSP vs. mid-market based on validation data** (step 9 conditional) and build accordingly.

Everything else is detail.

---

## Conversation context

This list was distilled from a candid evaluation conversation that covered: whether AxiaOps is sellable in a saturated FinOps market; whether MSPs are even the right ICP (they aren't, as drawn — segment was conflated with general IT MSPs, AWS Solution Provider partners, and FinOps consultants); the actual competitor landscape (which omitted Spot.io's CloudCheckr MSP, Flexera, Datadog Cloud Cost, Cast.ai, Finout, and others); legal entity structure ((legal entity) + (operating entity) vs. cheaper Holding UG + (operating entity) path); and the specific product gaps that block the pitch from being deliverable today (multi-client dashboard, audit trail actor attribution, white-label reports, email/Slack alerts, Stripe billing, GDPR data export, IAM role wizard, demo mode, production deployment).

The honest framing of the outcome distribution: ~60% lifestyle SaaS at €1K–€8K MRR, ~25% under 5 customers within 18 months, ~12% sustainable solo business at €15K–€40K MRR, ~3% category breakout. The €5M exit target is a planning aspiration, not a base case. See `business_plan.md` § Financial Projections (revised April 2026) for the two-scenario forecast.
