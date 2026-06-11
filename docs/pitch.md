# AxiaOps — Pitch Q&A

Common objections and talking points for pitching AxiaOps to engineering teams and decision makers.

> **April 2026 honesty pass:** earlier versions of this document promised features that are not yet in `main` — weekly digest, Azure/GCP, self-hosted, owner resolution, delegation. Each has been reframed to either "shipped today" or "roadmap (date)" so no claim made in a sales conversation surfaces as broken on day 14 of a trial. If you find a claim in this document that contradicts what's in `services/`, fix the document, not the conversation.

---

## "We can just write scripts for this"

You can — and most teams do. The problem is not detection, it is everything after detection.

| | Custom Scripts | AxiaOps (today) |
|---|---|---|
| Setup | Days to weeks per account | Under 30 minutes |
| Maintenance | You own it forever | Maintained for you |
| Multi-cloud | Build each integration | AWS today; Azure/GCP roadmap 2028 |
| Output | Raw data dump | Detection + dismiss/snooze workflow |
| Audit trail | None | Full schema; actor attribution shipping Q2 2026 |
| Alerting | Build it yourself | Email/Slack scan digests (shipped) |
| Onboarding | Tribal knowledge | Self-service dashboard |

Scripts solve the detection problem. AxiaOps solves the **workflow** problem on top of detection — what to do about each ghost, with structured dismiss reasons and snooze windows that survive across team members.

---

## "We are worried about our financial data leaving the company"

This is a valid concern. Here is the honest answer.

**What AxiaOps reads:**
- Cost line items — service name, spend amount, resource ID
- Resource tags — team, environment
- Usage metrics — CPU percentage, connection counts, invocation counts

**What AxiaOps never touches:**
- Actual workload data — no S3 contents, no database rows, no application traffic
- Credentials — access is read-only IAM, no write access to anything in your account

**Mitigations:**

- **EU data residency by default** — data processed and stored in EU (Frankfurt). German legal entity. Built-in GDPR posture.
- **Self-hosted (deferred)** — packaged Docker Compose + license-key gating for customers who need data to stay in their own VPC. Deferred in favour of the SaaS-first launch; enterprise enquiries welcome. *Not generally available today.*
- **Read-only IAM** — the policy only calls describe/list/get APIs. It cannot modify, delete, or provision any resource.
- **Credential storage** — customer AWS credentials are encrypted at rest with AES-256-GCM. The IAM cross-account role onboarding flow (which removes the need to store access keys at all) is shipping Q2 2026; until then, customers paste read-only access keys.
- **SOC 2 compliance** — Type II audit targeted Q4 2027, dependent on paying-customer revenue justifying the €15–€25K audit cost.

**The positioning:**

> If you are comfortable with your cloud provider seeing your spend data — and you have to be, they generate it — then AxiaOps adds no new exposure. EU customers get Frankfurt-resident data and a German DPA. For the most security-conscious teams, self-hosted pilots are available on request from Q2 2026.

---

## "How is this different from AWS Cost Explorer / Azure Cost Management?"

Native cloud tools show you what you spent. They do not tell you whether the resource is still needed, who owns it, or what to do next.

AxiaOps adds the layer above billing:
- Zombie detection — flags resources with zero or near-zero usage across 15+ AWS resource types
- Dismiss-with-reason workflow — five reasons, optional note, snooze for 1/7/30/90 days
- Trend history — track waste reduction over time per account
- Multi-account view — one view across all your AWS accounts (multi-cloud Azure/GCP roadmap 2028)

---

## "We already have a FinOps team"

AxiaOps is a tool for your FinOps team, not a replacement. It removes the manual work of querying billing APIs, correlating usage data, and chasing down resource owners — so the team can focus on decisions rather than data collection.

---

## "What does read-only actually mean? What can it access?"

Two read-only permissions, both scoped to read operations only:

```json
{
  "Version": "2012-10-17",
  "Statement": [{
    "Effect": "Allow",
    "Action": [
      "ce:GetCostAndUsage",
      "cloudwatch:GetMetricStatistics",
      "cloudwatch:ListMetrics",
      "ec2:DescribeInstances",
      "ec2:DescribeNatGateways",
      "rds:DescribeDBInstances",
      "lambda:ListFunctions",
      "elasticloadbalancing:DescribeLoadBalancers"
    ],
    "Resource": "*"
  }]
}
```

- `ce:GetCostAndUsage` — billing line items, same data as the AWS Console Cost Explorer
- `cloudwatch:GetMetricStatistics` — usage metrics (CPU, connections, invocations) to determine if a resource is idle
- `ec2:DescribeInstances`, `rds:DescribeDBInstances`, `lambda:ListFunctions`, `elasticloadbalancing:DescribeLoadBalancers`, `ec2:DescribeNatGateways` — list resources to cross-reference with CloudWatch

All actions are read-only Describe/List/Get. No access to create, modify, or delete any resource.

---

## "What happens if AxiaOps goes down?"

Nothing breaks in your infrastructure. AxiaOps is read-only and sits entirely outside your workloads. The worst case is that the dashboard is unavailable — your cloud resources are unaffected.

---

## "We are a startup, our cloud bill is too small to justify this"

If your monthly cloud bill is under \$1,000 this is probably not for you yet — and we will tell you that honestly.

The sweet spot is teams spending \$5,000/month or more. At that scale even 10% waste is \$500–\$6,000/month. AxiaOps typically pays for itself in the first month.

For early-stage startups: bookmark it for when you scale. Ghost resources compound — the longer they run, the harder they are to find.

---

## "What is the pricing model?"

Usage-based, tied to the number of cloud accounts connected. No per-seat fees — your whole team gets access.

| Tier | Accounts | Price |
|------|----------|-------|
| Free | 1 account | €0 (manual scan only, 7-day retention) |
| Starter | 3 accounts | €79/month |
| Growth | 10 accounts | €249/month |
| Team | 25 accounts | €599/month |
| MSP | 30 included + €12/account over | €999/month (gated on multi-client dashboard shipping Q3 2026) |
| Enterprise | Unlimited + self-hosted | Custom (~€2,500–€8,000/mo) |

You only pay for the accounts you connect. Disconnect an account and the cost drops immediately.

> Pricing revised April 2026. Earlier €49/€149 tiers were below market. See `market-readiness-2026-04.md` §5 for rationale.

---

## "How long does setup take?"

Under 10 minutes for a single AWS account:

1. Enable Cost Explorer in the AWS Console (one click)
2. Create a read-only IAM user and paste the access key into AxiaOps
3. See your first ghost list

No agents to install, no code changes, no infrastructure to manage.

---

## "We tried a tool like this before and it gave us false positives"

False positives are the main reason teams abandon FinOps tools. AxiaOps is conservative by design:

- EC2 instances are only flagged if CPU stays at or below 5% — not a brief dip
- Rules are per-service and based on the metric that actually matters (connections for RDS, not CPU)
- Every detection shows the exact metric, value, and time period so you can verify it yourself
- You can dismiss a ghost permanently — it will not reappear

The goal is a list you trust, not a long list.

---

## "Our engineers will just ignore the alerts"

That is a process problem as much as a tooling problem — and the AxiaOps roadmap is designed around it.

- **Dismiss-with-reason** (shipped today) — every ghost can be dismissed with one of five structured reasons (intentional, scheduled deletion, false positive, cost accepted, other) plus an optional note. Forces the team to articulate *why* a ghost is acceptable rather than just ignore it.
- **Snooze** (shipped today) — re-evaluate any ghost after 1, 7, 30, or 90 days. Avoids permanent dismissal of resources that may become wasteful again.
- **Audit trail with actor attribution** (shipping Q2 2026 — schema exists, UI/actor hookup ~4 hours of work) — dismissed items are recorded with who acted and when, visible to management. *Ask whether this has shipped before pitching it.*
- **Email + Slack scan digests** (shipped) — a summary message after scans rather than per-resource noise.
- **Owner resolution / delegation features** (not on near-term roadmap) — these were aspirational features in earlier pitch material; deferred until customer demand justifies the build. Tag-based filtering by `team` / `owner` is shipping Q3 2026 and partially addresses the same need.

Ignored alerts usually mean the wrong person is getting them, or the list is too long to trust. Both are solvable, on the timeline above.

---

## "We use Terraform — won't unused resources just get destroyed in the next apply?"

Only if they are managed in Terraform. Ghost resources are often things that were created manually, by a script, or by a service that was later decommissioned — and never added to IaC. They survive every `terraform apply` because Terraform does not know they exist.

AxiaOps finds exactly those resources — the ones outside your IaC state.

---

## "How do you handle multi-account and multi-cloud setups?"

**Today:** AxiaOps supports multiple AWS accounts per organization — connect each one with read-only credentials and see all detected ghosts in one dashboard, with per-account and aggregated views.

**Q2 2026:** cross-account IAM role onboarding wizard replaces the access-key paste flow. One CloudFormation template per account, no long-lived credentials stored on AxiaOps's side.

**Q3 2026 (validation-dependent):** multi-tenant client-switching dashboard for FinOps consultants and AWS Solution Provider partners managing multiple end-customers. Includes per-organization white-label branding and PDF report generation. Currently being scoped — see `gtm_assessment.md` §4.6 for the validation experiment that gates this build.

**2028:** Azure Cost Management and GCP Billing APIs. We don't claim multi-cloud earlier because we haven't built it.

Self-hosted is currently deferred in favour of the SaaS-first launch — enterprise enquiries: talk to sales.

---

## "Can we get a trial before committing?"

Yes — the Starter tier is permanently free for one account with no time limit. Connect your AWS account, see your ghost list, and decide from there. No credit card required.

---

## "What if we already use Spot instances or auto-scaling — won't that look like idle resources?"

AxiaOps is aware of this. Detection rules look at average usage over a rolling 30-day window, not a single point in time. A Spot instance that was running at 80% CPU last week but terminated today does not get flagged.

Auto-scaling groups are evaluated at the group level, not per-instance — a group that scaled to zero and stayed there is a candidate, not one that scaled down overnight.

---

## "We tag resources inconsistently — will tag-based filtering work?"

Tag-based filtering by `team`, `owner`, or `env` is shipping Q3 2026. Until then, every detected ghost shows the resource's tags in the detail view and you can manually inspect them. For untagged resources, AxiaOps shows the account and service.

The list of untagged ghosts is itself a useful output: it's a tagging-hygiene gap that AxiaOps surfaces as a byproduct of detection.

> **Honesty note:** earlier versions of this document promised "owner resolution" as a shipped feature. It is not. The schema captures tags and the UI shows them, but there is no automatic mapping from tag values to a responsible engineer's contact info. That feature is not on the near-term roadmap.

---

## "What is the competitive landscape — why not use Cloudability, Apptio, or CloudHealth?"

Those are enterprise FinOps platforms targeting \$1M+ cloud spend with 6-month procurement cycles and 6-figure price tags.

AxiaOps is for engineering teams that want to act today, not go through a procurement process. It is narrowly focused on zombie resources — the highest-ROI FinOps action — rather than trying to be a full cost allocation and chargeback platform.

If you need chargeback reports for 50 cost centers, use Cloudability. If you want to stop paying for things nobody is using, use AxiaOps.

---

## "What if we delete a resource that turns out to be needed?"

AxiaOps never deletes anything. It surfaces candidates and generates the CLI command to act — you or your engineer runs it deliberately. Every action is opt-in.

The remediation workflow in Phase 3 adds a one-click option, but it still requires explicit confirmation and logs the action with the user who triggered it.

---

## "Is this GDPR compliant?"

The data AxiaOps processes — cost line items, resource IDs, usage metrics — does not contain personal data. It is infrastructure billing data.

**SaaS version:** data is stored in EU data centers (Frankfurt) and never transferred outside the EU. A Data Processing Agreement (DPA) is available on request. Right-to-erasure (full organization deletion with cascade) is shipped today; data export endpoint is shipping Q2 2026.

**Self-hosted:** currently deferred (SaaS-first launch) — talk to sales for enterprise arrangements. Once available, all data stays within your own infrastructure in your chosen region.

---

## "What languages and frameworks does the agent support — do we need to install anything?"

There is no agent. AxiaOps connects via cloud provider APIs using read-only credentials. Nothing is installed on your servers, in your VPC, or in your Kubernetes cluster. It reads billing and usage data from the outside, the same way your cloud provider's own console does.
