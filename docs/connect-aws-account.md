# Connecting an AWS account to AxiaOps

Customer-facing runbook for the **Connect account** screen. AxiaOps scans your
AWS account read-only to find idle/zombie resources; it never creates, modifies,
or deletes anything.

There are three ways to connect, in order of preference:

1. **Launch Stack (recommended)** — one click; CloudFormation creates a read-only
   IAM role in your account.
2. **Manual / Terraform** — you create the role yourself from the trust +
   permissions JSON we show you.
3. **Access keys** — paste an IAM user's access key + secret (least preferred;
   long-lived credentials you have to rotate).

All three end the same way: AxiaOps verifies it can assume the role / use the
keys, then starts scanning.

---

## Option 1 — Launch Stack (recommended)

**Before you start:** sign in to the AWS account you want AxiaOps to scan, as a
user allowed to create IAM roles and CloudFormation stacks.

1. In AxiaOps: **Connect account → Role tab**. Enter a label (e.g. "Production")
   and the region, then click **Generate connection**.
2. Click **Launch Stack in AWS ↗**. This opens the AWS CloudFormation console in
   a **new tab**. Confirm the tab is in the **correct AWS account** (top-right of
   the console) — the stack creates the role *there*.
3. The **ExternalId** is already filled in for you. Leave **RoleName** as
   `AxiaOpsIntegrationRole` unless it collides with an existing role.
4. Tick **"I acknowledge that AWS CloudFormation might create IAM resources with
   custom names"**, then click **Create stack**.
5. Wait ~30 seconds for the stack status to reach **CREATE_COMPLETE**. Open the
   stack's **Outputs** tab and copy the **RoleArn** value
   (`arn:aws:iam::<your-account>:role/AxiaOpsIntegrationRole`).
6. Back in AxiaOps, paste that ARN into **Role ARN** and click
   **Verify and connect**.

That's it — AxiaOps performs an `sts:AssumeRole` probe to confirm access and the
first scan begins.

### What the stack creates

A single IAM role, `AxiaOpsIntegrationRole`:

- **Trust policy:** allows only the AxiaOps scanner principal shown on the
  Connect screen (`…:role/AxiaOpsScanner`) to assume it, and only when it
  presents *your* ExternalId. This blocks confused-deputy access from any other
  tenant.
- **Permissions policy:** read-only `Describe*`/`List*` + Cost Explorer +
  CloudWatch read. No write, modify, or delete actions — the exact list is
  below, kept identical to the CloudFormation template.

Nothing else is created; the stack is free (IAM-only).

### The permission list (`AxiaOpsReadOnly`)

Enumerated from the actual `Describe`/`List`/`Get` calls in
`services/ingestion/internal/provider/aws/`. This is the single source of
truth the CloudFormation template, the Terraform manual-setup snippet (Option
2), and this doc all mirror — read-only, no write actions, ever.

```json
{
  "Version": "2012-10-17",
  "Statement": [{
    "Sid": "AxiaOpsReadOnlyScan",
    "Effect": "Allow",
    "Action": [
      "sts:GetCallerIdentity",
      "ce:GetCostAndUsage",
      "ce:GetCostAndUsageWithResources",
      "cloudwatch:GetMetricStatistics",
      "ec2:DescribeInstances",
      "ec2:DescribeVolumes",
      "ec2:DescribeSnapshots",
      "ec2:DescribeImages",
      "ec2:DescribeAddresses",
      "ec2:DescribeNatGateways",
      "rds:DescribeDBInstances",
      "rds:DescribeDBSnapshots",
      "lambda:ListFunctions",
      "elasticloadbalancing:DescribeLoadBalancers",
      "logs:DescribeLogGroups",
      "ecr:DescribeRepositories",
      "ecr:DescribeImages",
      "secretsmanager:ListSecrets",
      "elasticache:DescribeCacheClusters",
      "es:ListDomainNames",
      "redshift:DescribeClusters",
      "sagemaker:ListEndpoints",
      "dynamodb:ListTables",
      "kinesis:ListStreams",
      "kinesis:DescribeStreamSummary",
      "cloudfront:ListDistributions",
      "eks:ListClusters",
      "s3:ListAllMyBuckets",
      "s3:GetBucketLocation"
    ],
    "Resource": "*"
  }]
}
```

---

## Option 2 — Manual / Terraform

In the Role tab's verify step, expand **"Prefer manual setup (Terraform /
console)?"**. You'll get:

- the **AxiaOps principal** ARN to trust,
- the **trust policy JSON** (with your ExternalId pre-filled), and
- the **read-only permissions policy JSON**.

Create an IAM role named `AxiaOpsIntegrationRole` with **both** policies attached,
then paste its ARN back and **Verify and connect**. A role with only the trust
policy can be assumed but reads nothing — scans return empty.

---

## Option 3 — Access keys

**Connect account → Access Keys tab.** Create an IAM user with the read-only
policy shown there, generate an access key, and paste the key ID + secret. AxiaOps
encrypts the secret at rest (AES-256-GCM). Prefer a role (Options 1–2): access
keys are long-lived and must be rotated manually.

---

## Troubleshooting

| Symptom (shown on Verify) | Cause | Fix |
|---|---|---|
| `AccessDenied` / "trust policy mismatch" | The role's trust policy doesn't allow the AxiaOps principal, or the ExternalId doesn't match | Re-launch the stack (or re-copy the trust policy) using the **exact ExternalId** shown on the Connect screen — no extra spaces |
| "ExternalId mismatch" | The stack was created with a different/old ExternalId | Delete the stack and re-launch from a freshly generated connection |
| "role not found" | Wrong account, or the stack hasn't finished | Confirm CREATE_COMPLETE and that the ARN is from the account you intend to scan |
| Stack fails with an IAM capability error | The acknowledgement box wasn't ticked | Re-run; tick the IAM-capability checkbox before Create |

**Region note:** IAM roles are global, so it doesn't matter which region the
stack is created in — the role works regardless.

## Removing access

Delete the `AxiaOps-Integration` CloudFormation stack in your AWS account (this
deletes the role), and remove the account from AxiaOps. For access-key
connections, delete the IAM user's key and remove the account in AxiaOps.
