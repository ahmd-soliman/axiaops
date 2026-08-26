---
title: Operations
description: Connecting an AWS account and setting up notification channels.
---

The two things you'll actually do after AxiaOps is running: connect a cloud
account, and (optionally) wire up notifications.

## Connecting an AWS account

AxiaOps scans your AWS account **read-only** — it never creates, modifies, or
deletes anything. Three ways to connect, in order of preference:

1. **Launch Stack (recommended)** — one click; CloudFormation creates a read-only IAM role for you.
2. **Manual / Terraform** — you create the role yourself from the trust + permissions JSON AxiaOps shows you.
3. **Access keys** — least preferred; long-lived credentials you have to rotate yourself.

### Launch Stack

1. **Connect account → Role tab** — enter a label and region, click **Generate connection**.
2. **Launch Stack in AWS ↗** — opens CloudFormation in a new tab. Double-check the
   tab is in the **correct AWS account** before continuing.
3. The **ExternalId** is pre-filled — leave `RoleName` as `AxiaOpsIntegrationRole`
   unless it collides with something existing.
4. Acknowledge the IAM-capability checkbox, **Create stack**, wait ~30s for
   `CREATE_COMPLETE`, then copy the **RoleArn** from the stack's Outputs.
5. Paste the ARN into AxiaOps, **Verify and connect**.

The role's trust policy only allows the AxiaOps scanner to assume it, and only
with *your* ExternalId — this blocks confused-deputy access from any other
tenant. Its permissions are strictly `Describe*`/`List*` + Cost Explorer +
CloudWatch read — no write, modify, or delete action, ever.

### Manual / Terraform

Expand **"Prefer manual setup?"** in the verify step for the trust-policy JSON
(ExternalId pre-filled) and the read-only permissions JSON. Create the role
with both attached — a role with only the trust policy can be assumed but reads
nothing.

### Access keys

**Connect account → Access Keys tab.** Create an IAM user with the read-only
policy shown there and paste the key ID + secret. AxiaOps encrypts it at rest.
Prefer a role if you can — access keys need manual rotation.

### Troubleshooting

| Symptom | Fix |
|---|---|
| `AccessDenied` / trust policy mismatch | Re-launch/re-copy using the *exact* ExternalId shown on the Connect screen |
| "ExternalId mismatch" | Delete the stack, re-launch from a freshly generated connection |
| "role not found" | Confirm the stack reached `CREATE_COMPLETE` and the ARN is from the right account |

**Removing access**: delete the CloudFormation stack (removes the role) and remove
the account in AxiaOps.

## Notification channels

Requires `admin` or `owner`. After every scan, AxiaOps sends a digest (zombie
count, potential savings, top services) to each enabled channel whose savings
threshold is met.

### Slack

1. **api.slack.com/apps → Create New App → From scratch**, pick your workspace.
2. **Incoming Webhooks** → toggle on → **Add New Webhook to Workspace** → pick a channel.
3. Copy the webhook URL — treat it as a secret.
4. AxiaOps → **Settings → Integrations → Add channel** → Slack webhook → paste the
   URL → **Save** (starts disabled).
5. **Test**, confirm the digest arrives, then flip to **enabled**.

### Email (SMTP)

AxiaOps speaks plain SMTP — Amazon SES, a Google Workspace relay, or a single
mailbox's App Password all work the same way. SES is the natural fit if you're
already on AWS (cheap, purpose-built for transactional mail). Whichever you pick:

- **Use port 587 (STARTTLS)** — not 465, AxiaOps doesn't speak implicit TLS.
- **Send from a role address** (`notifications@yourdomain.com`), never a person's
  mailbox — a personal App Password silently breaks the day that person leaves.
- **Set up SPF + DKIM + DMARC** on your sending domain, or digests land in spam
  regardless of whether the send technically succeeded.

Add the channel: **Settings → Integrations → Add channel → Email (SMTP)** → pick a
provider preset (auto-fills host/port) or Custom → fill in credentials → **Save →
Test → Enable**.

### Tuning

Each channel has two knobs: **minimum monthly savings** (don't notify below this,
default $25) and **digest size** (how many top services to list, default 10). The
**Deliveries** drawer on each channel shows recent sends and failures — a scan that
finds nothing above the threshold records no row, so an empty drawer on a quiet
account is expected, not broken.

## Learn more

Full deliverability guidance, SMTP error-code tables, and the security model
behind channel credentials live in the repo's
[`docs/OPERATIONS.md`](https://github.com/ahmd-soliman/axiaops/blob/main/docs/OPERATIONS.md).
