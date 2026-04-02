# Improvement Notes — AxiaOps

Evaluated suggestions and their integration status across the main docs.

---

## 1. CSV Ingestion — Streaming Required
**Status: Integrated → `development_plan.md` Phase 1.2**

AWS Cost and Usage Report (CUR) files can be several gigabytes. The Go worker must use streaming CSV parsing rather than loading the entire file into memory. Loading a multi-GB file into memory will crash the worker on any reasonably-sized cloud account.

---

## 2. The Orphan Problem — Ownership Resolution
**Status: Integrated → `development_plan.md` Phase 1.2, `mvp-user-stories.md` AXIAOPS-003**

Identifying a zombie resource is 20% of the work. Finding who owns it is 80%.

Every flagged ghost must include an `Owner` attribute derived from resource tags. Without ownership resolution, the remediation step stalls — no one knows if it is safe to delete the resource. The mock data generator must seed realistic `Owner` and `Project` tags on every row so this logic can be built and tested from day one.

---

## 3. The 7-Day Threshold — Configurable Zombie Window
**Status: Integrated → `development_plan.md` Phase 1.2**

A fixed 7-day "last usage" window will generate false positives for enterprise customers with monthly batch jobs (e.g., a monthly reporting server that only runs on the 1st of each month). The zombie threshold must be configurable per tenant — default 7 days, with options for 14, 30, or custom.

---

## 4. Demo Mode — Reduce Cold Start Friction
**Status: Integrated → `business_plan.md` Cold Start section**

New users are reluctant to connect live AWS production accounts to an unknown startup. A "Demo Mode" pre-loaded with mock data lets users experience the full product value before granting any cloud access. This also serves as the onboarding fallback for the web app when users haven't connected an account yet.

---

## 5. "Cloud Hygiene" Marketing Angle
**Status: Integrated → `business_plan.md` Messaging Strategy**

Don't sell only "Cost Savings." Sell "Cloud Hygiene."

- Engineering managers care about a clean, well-governed environment more than the dollar amount saved
- CFOs and finance teams care about the concrete € figure

Use both messages but target them correctly. Lead with hygiene for technical audiences, lead with savings for financial audiences.

---

## 6. Terraform / Pulumi Taint Scripts for Remediation
**Status: Integrated → `development_plan.md` Phase 3.1**

Pre-generating CLI commands alone is not enough. Modern infrastructure teams manage resources via IaC (Terraform, Pulumi, CDK). If a resource is deleted via CLI but still exists in a Terraform state file, the next `terraform apply` or CI/CD pipeline run will recreate it.

Remediation suggestions must include:
- The AWS CLI command (for manual operators)
- A `terraform taint` or resource removal snippet (for IaC shops)

---

## 7. Cold Start Problem — Trust & Activation
**Status: Integrated → `business_plan.md` Cold Start section**

The biggest activation blocker for a new FinOps startup is asking users to connect their production AWS account on day one. Mitigations include:

- Demo Mode (mock data, no credentials needed)
- Published minimal read-only IAM policy
- SOC 2 Type II roadmap commitment
- Optional "scan and forget" mode (no data persistence)
- Open-sourcing the detection engine (users can audit what it does)

---

## Open Question — Not Yet Addressed

**Cold Start trust at the MSP level:** An MSP connecting 20+ client accounts on behalf of their clients requires client consent and potentially DPA (Data Processing Agreement) under GDPR. This needs legal review before the MSP tier launches.
