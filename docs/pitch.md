# AxiaOps — Pitch Q&A

Common objections and talking points for pitching AxiaOps to engineering teams and decision makers.

---

## "We can just write scripts for this"

You can — and most teams do. The problem is not detection, it is everything after detection.

| | Custom Scripts | AxiaOps |
|---|---|---|
| Setup | Days to weeks per account | Minutes |
| Maintenance | You own it forever | Maintained for you |
| Multi-cloud | Build each integration | AWS → Azure → GCP included |
| Output | Raw data dump | Remediation workflow + owner resolution |
| Audit trail | None | Full history of decisions |
| Alerting | Build it yourself | Weekly digest built-in |
| Onboarding | Tribal knowledge | Self-service dashboard |

Scripts solve the detection problem. AxiaOps solves the **workflow** problem — who owns the resource, what to do about it, and proof that it was handled.

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

- **Self-hosted** — run AxiaOps entirely inside your own VPC. Data never leaves your infrastructure. This is the primary answer for enterprise customers with strict data residency requirements.
- **Read-only IAM** — the policy only calls `ce:GetCostAndUsage`. It cannot modify, delete, or provision any resource.
- **No credential storage** — credentials stay in your environment via `~/.aws` or IAM instance roles. They are never sent to AxiaOps servers.
- **SOC 2 compliance** — planned for Phase 3 once paying customers justify the audit cost.

**The positioning:**

> If you are comfortable with your cloud provider seeing your spend data — and you have to be, they generate it — then AxiaOps adds no new exposure. For the most security-conscious teams, self-hosting is available with full data residency guarantees.

---

## "How is this different from AWS Cost Explorer / Azure Cost Management?"

Native cloud tools show you what you spent. They do not tell you whether the resource is still needed, who owns it, or what to do next.

AxiaOps adds the layer above billing:
- Cross-cloud single pane — one view across AWS, Azure, GCP
- Zombie detection — flags resources with zero or near-zero usage
- Owner resolution — maps resource tags to the responsible team
- Remediation workflow — dismiss, delegate, or action with a full audit trail

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
      "cloudwatch:ListMetrics"
    ],
    "Resource": "*"
  }]
}
```

- `ce:GetCostAndUsage` — billing line items, the same data you see in the AWS Console under Cost Explorer
- `cloudwatch:GetMetricStatistics` — usage metrics (CPU, connections, invocations) to determine if a resource is actually idle
- `cloudwatch:ListMetrics` — discover which metrics exist for a given resource

No access to compute, storage, networking, or any write operation on any service.

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
| Starter | 1 account | Free |
| Growth | Up to 5 accounts | €49/month |
| Scale | Up to 20 accounts | €149/month |
| Enterprise | Unlimited + self-hosted | Custom |

You only pay for the accounts you connect. Disconnect an account and the cost drops immediately.

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

That is a process problem as much as a tooling problem — and AxiaOps is designed around it.

- **Owner resolution** — every ghost shows the responsible team derived from resource tags, so alerts go to the right person
- **Delegation** — a FinOps manager can assign a ghost to an engineer directly from the dashboard
- **Audit trail** — dismissed or delegated items are recorded with who acted and when, visible to management
- **Weekly digest** — a summary email/Slack message rather than per-resource noise

Ignored alerts usually mean the wrong person is getting them, or the list is too long to trust. Both are solvable.

---

## "We use Terraform — won't unused resources just get destroyed in the next apply?"

Only if they are managed in Terraform. Ghost resources are often things that were created manually, by a script, or by a service that was later decommissioned — and never added to IaC. They survive every `terraform apply` because Terraform does not know they exist.

AxiaOps finds exactly those resources — the ones outside your IaC state.

---

## "How do you handle multi-account and multi-cloud setups?"

Phase 1 supports a single AWS account. Phase 2 (Q3 2026) adds:
- Multi-account via cross-account IAM role assumption — no need to create users in every account
- Azure Cost Management API
- GCP Billing API

Enterprise customers on the self-hosted plan can connect unlimited accounts from day one.

---

## "Can we get a trial before committing?"

Yes — the Starter tier is permanently free for one account with no time limit. Connect your AWS account, see your ghost list, and decide from there. No credit card required.

---

## "What if we already use Spot instances or auto-scaling — won't that look like idle resources?"

AxiaOps is aware of this. Detection rules look at average usage over a rolling 30-day window, not a single point in time. A Spot instance that was running at 80% CPU last week but terminated today does not get flagged.

Auto-scaling groups are evaluated at the group level, not per-instance — a group that scaled to zero and stayed there is a candidate, not one that scaled down overnight.

---

## "We tag resources inconsistently — will owner resolution work?"

Partially — and we will tell you that upfront. Owner resolution works for resources that have `team`, `owner`, or `env` tags. For untagged resources, AxiaOps shows the account and service but marks the owner as unknown.

This is often the most valuable output: a list of untagged resources is itself a compliance gap. AxiaOps gives you that list for free as a byproduct of ghost detection.

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

For self-hosted deployments, all data stays within your own infrastructure in your chosen region. For the SaaS version, data is stored in EU data centers (Frankfurt) and never transferred outside the EU. A Data Processing Agreement (DPA) is available on request.

---

## "What languages and frameworks does the agent support — do we need to install anything?"

There is no agent. AxiaOps connects via cloud provider APIs using read-only credentials. Nothing is installed on your servers, in your VPC, or in your Kubernetes cluster. It reads billing and usage data from the outside, the same way your cloud provider's own console does.
