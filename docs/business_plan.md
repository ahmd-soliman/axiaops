# Business Plan — AxiaOps

## Executive Summary

**Product:** AxiaOps — a web-first FinOps tool that detects idle/zombie cloud resources (ghost spend) and surfaces an actionable remediation workflow with a full audit trail.

**Market:** FinOps / Cloud Cost Optimization — $6B+ market growing 20%+ YoY.

**Model:** SaaS subscription, priced per cloud account monitored.

**Primary Customers:** MSPs, FinOps consultants, and mid-market DevOps teams managing multi-cloud environments.

**Target Exit:** €5M acquisition within 3–5 years (strategic buyer: cloud provider, FinOps platform, or MSP tooling company).

**Legal Entity:** Operating UG (Germany) owned by Holding GmbH.

---

## Problem

Cloud waste is endemic. Gartner estimates 30% of cloud spend is wasted. The biggest culprits are invisible: idle load balancers, unattached EBS volumes, forgotten RDS snapshots, and unused Elastic IPs — resources that cost money 24/7 while delivering zero value.

**Detection alone is not the gap.** AWS Trusted Advisor already detects idle resources for free. The real gap is:

1. **Multi-cloud in one view** — Trusted Advisor is AWS-only; Azure Advisor and GCP Recommender don't talk to each other.
2. **Remediation workflow** — no tool provides a structured approve → act → audit trail loop.
3. **MSP-scale management** — no self-serve tool lets an MSP manage ghost spend across 20+ client accounts from one dashboard.
4. **Tagging hygiene correlation** — ghost resources are almost always untagged; no tool surfaces both problems together.

---

## Solution

AxiaOps connects to cloud billing via read-only IAM access and delivers:

1. **The Ghost Number** — total monthly spend on idle resources across all connected accounts
2. **The Ghost List** — itemized breakdown by resource with age, cost/day, and remediation suggestion
3. **The Remediation Workflow** — approve, delegate, or dismiss each ghost with a full audit trail
4. **The Weekly Digest** — email/Slack alert when new ghosts appear
5. **Multi-account Dashboard** — single pane for MSPs managing multiple client accounts

Core value proposition: **detect waste, action it, prove the savings — across every cloud, for every client.**

---

## Platform Strategy

**Web app first, mobile companion second.**

The core product is a responsive web application. A mobile app (React Native) serves as a companion for:
- Push alerts ("$3,200 in new ghost spend this week")
- Executive glance view (the big savings number)
- Remediation approvals on the go

This mirrors the approach of Datadog, Grafana, and PagerDuty — desktop-native workflows, mobile for awareness and approvals. Building mobile-first would hurt enterprise sales and onboarding (connecting IAM roles via phone is a poor experience).

---

## Target Market

### Primary ICP
- **MSPs** managing cloud for 5–50 clients (biggest monetization opportunity)
- **FinOps consultants** needing client-facing reporting with proof of savings
- **Mid-market DevOps / Platform teams** (Series A–C, €10K–€200K/month cloud spend)

### Secondary
- Startups wanting self-serve cost visibility without enterprise pricing

### Market Size
- ~500,000 companies globally spending $10K+/month on cloud
- MSP market alone: ~50,000 MSPs globally managing cloud
- 0.1% capture at €149/month average = €74,500 MRR

---

## Business Model

### Pricing Tiers

| Plan | Price | Includes |
|------|-------|----------|
| Starter | €49/month | 1 cloud account, email alerts |
| Growth | €149/month | 5 cloud accounts, multi-cloud, Slack alerts |
| Team | €399/month | 20 accounts, PDF reports, remediation tracking + audit trail |
| MSP | €799/month | Unlimited client accounts, white-label reports, reseller dashboard |
| Enterprise | Custom | SSO, dedicated support, SLA |

### Revenue Drivers
- MRR per connected account
- Annual plans at 20% discount
- Future: remediation marketplace (% of savings actioned via AxiaOps)

---

## Competitive Landscape

### Tier 1 — Established Platforms (Don't Compete Directly)

| Tool | Why They Own Enterprise |
|------|------------------------|
| AWS Cost Explorer | Free, built-in, good enough for AWS-only shops |
| CloudHealth (VMware) | Entrenched in large enterprise, high switching cost |
| Apptio Cloudability | CFO-level tooling, strong finance integrations |

**Strategy:** Don't fight here. They're expensive, complex, and ignore the SMB/MSP segment.

### Tier 2 — Direct Overlaps (Monitor Closely)

| Tool | Gap to Exploit |
|------|---------------|
| **Vantage** | No remediation workflow, no MSP tier, US-focused |
| **Unusd** | Detection only, no workflow, no multi-account MSP view |
| **Komiser** | Open-source, no SaaS, no remediation |
| **Infracost** | Dev-time only, no runtime ghost detection |

### Tier 3 — The Real Threat: Free Cloud-Native Tools

| Tool | Limitation |
|------|-----------|
| AWS Trusted Advisor | AWS-only, no workflow, no multi-account aggregation |
| Azure Advisor | Azure-only, siloed |
| GCP Recommender | GCP-only, siloed |

**This is the most important competitive insight:** the core "idle resource detection" feature is free from cloud providers. AxiaOps's moat must be the **workflow layer** (remediation + audit trail) and **multi-cloud aggregation** — not detection alone.

### Defensible Differentiation

1. **Remediation workflow with audit trail** — detect → assign → approve → act → prove savings
2. **Multi-cloud ghost detection in one view** — AWS + Azure + GCP aggregated
3. **MSP-native** — multi-client dashboard, white-label reports, reseller pricing
4. **Tagging hygiene + ghost correlation** — surfaces both problems together

---

## Go-to-Market Strategy

### Messaging Strategy
Sell to two audiences with different languages:
- **Engineering managers** → "Cloud Hygiene" — a clean environment, no zombie resources, no technical debt in your infrastructure
- **CFOs / Finance** → "Cost Savings" — concrete € figures, monthly savings reports, ROI in under 60 seconds

Don't lead with cost savings to engineers. Lead with hygiene. The money argument follows naturally.

### Phase 1 — Community-Led (0 → €5K MRR)
- Post on r/devops, r/aws, Hacker News "Show HN"
- SEO content: "How to find idle AWS resources," "AWS EBS cost optimization guide"
- Product Hunt launch
- Direct outreach to FinOps consultants on LinkedIn

### Phase 2 — Content + SEO (€5K → €25K MRR)
- Publish monthly "State of Cloud Waste" report with anonymized aggregate data
- Target keywords: "cloud cost optimization," "AWS idle resources," "FinOps tool for MSPs"
- Guest posts on DevOps Weekly, Last Week in AWS

### Phase 3 — Partner Channel (€25K+ MRR)
- MSP reseller program (30% margin)
- AWS ISV Partner Program (marketplace listing)
- Integrations: Terraform Cloud, Datadog, Grafana, PagerDuty

---

## Legal & Corporate Structure

### Recommended Structure (Germany)

```
Ahmed (Founder / 100% shareholder)
        |
  Holding GmbH
  (owns IP, holds shares, receives acquisition proceeds)
        |
  Operating UG
  (employs founder, owns code, signs contracts, issues invoices)
```

**Why this matters for a €5M exit:**
- Holding GmbH receives acquisition proceeds → significant tax optimization (Schachtelprivileg)
- Operating UG limits personal liability
- IP cleanly owned by UG — makes due diligence straightforward for acquirers

### Immediate Actions (Before First €1 of Revenue)
1. Found Holding GmbH
2. Found Operating UG (€1 minimum share capital)
3. IP assignment agreement — transfer code ownership from individual to Operating UG
4. Open business bank account (Qonto or Fyrst)
5. Register with Finanzamt for VAT
6. Engage a Steuerberater familiar with SaaS/startup structures

### IP Clean Room (Non-Negotiable)
- All development on personal hardware only
- Maintain dated work log (device, hours, date)
- Use only mock/generated billing data — never employer data
- Separate Git identity from work email

---

## Financial Projections

| Month | Customers | MRR | Notes |
|-------|-----------|-----|-------|
| Month 6 (Oct 2026) | 5 | €500 | Beta users |
| Month 9 (Jan 2027) | 25 | €2,500 | Web app live |
| Month 12 (Apr 2027) | 60 | €6,000 | SEO traction |
| Month 18 (Oct 2027) | 200 | €22,000 | MSP resellers active |
| Month 24 (Apr 2028) | 500 | €55,000 | Multi-cloud live |

**Breakeven:** ~€3K MRR (hosting, tools, part-time contractor)

**Target at exit:** €500K–€1M ARR → 5–10x revenue multiple → €2.5M–€10M exit range

---

## Cold Start Problem

New users are hesitant to connect a live AWS production account to an unknown startup. This is the biggest activation blocker.

**Mitigations:**

| Tactic | Detail |
|--------|--------|
| **Demo Mode** | Pre-loaded mock data so users see full product value before connecting any account — no AWS credentials required to experience the "aha moment" |
| **Read-only IAM** | Publish a minimal IAM policy (read-only, no write permissions) users can inspect before granting access |
| **SOC 2 roadmap** | Commit publicly to SOC 2 Type II certification — reassures enterprise and MSP buyers |
| **No data storage option** | Offer a "scan and forget" mode where no billing data is persisted — results shown in session only |
| **Open-source the scanner** | If the detection engine is open-source, customers can read exactly what it does with their data |

---

## Risks & Mitigations

| Risk | Mitigation |
|------|-----------|
| AWS Trusted Advisor is free and good enough | Compete on workflow + multi-cloud, not detection |
| Vantage or Unusd copies the MSP angle | Move fast, build MSP brand early, lock in resellers |
| Employer IP claim | Clean room log, personal hardware, UG IP assignment |
| Low activation | Show savings number in <5 minutes from signup |
| German tax complexity | Holding + UG structure, Steuerberater from day one |

---

## Success Metrics (KPIs)

- **Activation:** % of signups who connect a cloud account within 24h (target: >60%)
- **Time-to-value:** Minutes from signup to first ghost surfaced (target: <5 min)
- **MRR Growth:** Month-over-month (target: 20%+ early stage)
- **Churn:** Monthly customer churn (target: <3%)
- **NPS:** Net Promoter Score (target: >50)
- **Ghost Savings Surfaced:** Total € identified across all customers (vanity metric but great for marketing)
