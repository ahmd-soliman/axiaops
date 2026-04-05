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

The minimum viable IAM policy for Phase 1:

```json
{
  "Version": "2012-10-17",
  "Statement": [{
    "Effect": "Allow",
    "Action": ["ce:GetCostAndUsage"],
    "Resource": "*"
  }]
}
```

That is one API call. It returns billing line items — the same data you see in the AWS Console under Cost Explorer. No access to compute, storage, networking, or any other service.

---

## "What happens if AxiaOps goes down?"

Nothing breaks in your infrastructure. AxiaOps is read-only and sits entirely outside your workloads. The worst case is that the dashboard is unavailable — your cloud resources are unaffected.
