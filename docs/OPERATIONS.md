# Operations — AxiaOps

Runbooks for the two things an operator/admin does after AxiaOps is running:
connect a cloud account, and wire up notifications.

---

## 1. Connecting an AWS account

AxiaOps scans your AWS account **read-only** — it never creates, modifies, or
deletes anything. Three ways to connect, in order of preference:

1. **Launch Stack (recommended)** — one click; CloudFormation creates a read-only IAM role.
2. **Manual / Terraform** — you create the role yourself from the trust + permissions JSON shown in the UI.
3. **Access keys** — least preferred; long-lived credentials you have to rotate.

All three end the same way: AxiaOps verifies it can assume the role / use the keys,
then starts scanning.

### Option 1 — Launch Stack

1. In AxiaOps: **Connect account → Role tab**. Enter a label and region, click
   **Generate connection**.
2. Click **Launch Stack in AWS ↗** — opens CloudFormation in a new tab. Confirm the
   tab is in the **correct AWS account** (top-right of the console) — the stack
   creates the role there.
3. The **ExternalId** is pre-filled. Leave `RoleName` as `AxiaOpsIntegrationRole`
   unless it collides.
4. Tick the IAM-capability acknowledgement, click **Create stack**. Wait ~30s for
   `CREATE_COMPLETE`, then copy the **RoleArn** from the stack's Outputs tab.
5. Back in AxiaOps, paste the ARN into **Role ARN**, click **Verify and connect**.

The stack creates a single IAM role whose trust policy allows only the AxiaOps
scanner principal to assume it, and only when it presents *your* ExternalId
(blocks confused-deputy access from any other tenant). Its permissions policy is
read-only `Describe*`/`List*` + Cost Explorer + CloudWatch read — no write, modify,
or delete action, ever:

```json
{
  "Version": "2012-10-17",
  "Statement": [{
    "Sid": "AxiaOpsReadOnlyScan",
    "Effect": "Allow",
    "Action": [
      "sts:GetCallerIdentity",
      "ce:GetCostAndUsage", "ce:GetCostAndUsageWithResources",
      "cloudwatch:GetMetricStatistics",
      "ec2:DescribeInstances", "ec2:DescribeVolumes", "ec2:DescribeSnapshots",
      "ec2:DescribeImages", "ec2:DescribeAddresses", "ec2:DescribeNatGateways",
      "rds:DescribeDBInstances", "rds:DescribeDBSnapshots",
      "lambda:ListFunctions",
      "elasticloadbalancing:DescribeLoadBalancers",
      "logs:DescribeLogGroups",
      "ecr:DescribeRepositories", "ecr:DescribeImages",
      "secretsmanager:ListSecrets",
      "elasticache:DescribeCacheClusters",
      "es:ListDomainNames",
      "redshift:DescribeClusters",
      "sagemaker:ListEndpoints",
      "dynamodb:ListTables",
      "kinesis:ListStreams", "kinesis:DescribeStreamSummary",
      "cloudfront:ListDistributions",
      "eks:ListClusters",
      "s3:ListAllMyBuckets", "s3:GetBucketLocation"
    ],
    "Resource": "*"
  }]
}
```

This is the single source of truth mirrored by the CloudFormation template, the
Terraform snippet (Option 2), and the required-permissions list quoted from
`services/ingestion/internal/provider/aws/`.

### Option 2 — Manual / Terraform

In the Role tab's verify step, expand **"Prefer manual setup?"** for the AxiaOps
principal ARN to trust, the trust-policy JSON (ExternalId pre-filled), and the
permissions JSON above. Create `AxiaOpsIntegrationRole` with **both** policies
attached — a role with only the trust policy can be assumed but reads nothing.

### Option 3 — Access keys

**Connect account → Access Keys tab.** Create an IAM user with the read-only policy
shown there, paste the key ID + secret. AxiaOps encrypts the secret at rest
(AES-256-GCM). Prefer a role — access keys are long-lived and must be rotated manually.

### Troubleshooting

| Symptom | Cause | Fix |
|---|---|---|
| `AccessDenied` / trust policy mismatch | Role doesn't trust the AxiaOps principal, or wrong ExternalId | Re-launch/re-copy using the exact ExternalId shown on the Connect screen |
| "ExternalId mismatch" | Stack created with an old ExternalId | Delete the stack, re-launch from a freshly generated connection |
| "role not found" | Wrong account, or stack still creating | Confirm `CREATE_COMPLETE`; confirm the ARN is from the account you intend to scan |
| Stack fails on an IAM capability error | Acknowledgement box wasn't ticked | Re-run, tick the box before Create |

IAM roles are global — region doesn't matter. **Removing access**: delete the
CloudFormation stack (removes the role) and remove the account in AxiaOps; for
access-key connections, delete the IAM user's key.

---

## 2. Notification channels (Slack / Email / Teams)

Requires role **admin** or **owner** (`channels:manage`). After every scan, AxiaOps
sends a digest (zombie count, potential savings, top services) to each **enabled**
channel whose savings-gate trips. Channels are org-level, shared by the whole org.

### Add a Slack channel

1. **api.slack.com/apps → Create New App → From scratch**, pick the workspace.
2. Left nav → **Incoming Webhooks** → toggle on → **Add New Webhook to Workspace** →
   pick a channel → **Allow**. (Private channel: `/invite @AxiaOps` there first.)
3. Copy the webhook URL (`https://hooks.slack.com/services/...`) — treat it as a
   secret; each URL is bound to one channel.
4. AxiaOps → **Settings → Integrations → Add channel** → Type = Slack webhook →
   paste the URL → **Save** (created disabled).
5. Click **Test** — a synthetic digest should arrive. Then flip the channel to
   **enabled**.

Isolate "is the webhook good?" from AxiaOps directly:

```bash
curl -i -X POST -H 'Content-type: application/json' \
  --data '{"text":"test"}' 'https://hooks.slack.com/services/...'
```

`200`/`ok` = webhook valid (if AxiaOps's Test still fails, the stored URL is wrong).
`404 no_service/no_team` = URL revoked, recreate it.

### Add a Microsoft Teams channel

1. Teams channel → `⋯` → **Workflows** → *"Post to a channel when a webhook request is received"*. A legacy Connectors webhook URL will not work — create a Workflow.
2. Copy the generated URL → AxiaOps **Settings → Integrations → Add channel** → Type = Microsoft Teams webhook.
3. Verify with the **Test** button before relying on it.

Isolate "is the webhook good?" from AxiaOps directly:

```bash
curl -i -X POST -H 'Content-type: application/json' \
  -d '{"type":"message","attachments":[{"contentType":"application/vnd.microsoft.card.adaptive","contentUrl":null,"content":{"$schema":"http://adaptivecards.io/schemas/adaptive-card.json","type":"AdaptiveCard","version":"1.4","body":[{"type":"TextBlock","wrap":true,"text":"AxiaOps test"}]}}]}' \
  '<webhook-url>'
```

### Add an Email (SMTP) channel

AxiaOps only speaks SMTP — SES, a Google Workspace relay, or a single mailbox are
just different SMTP endpoints; the setup is the same shape for all three.

**Choosing a sender:**

| | SES | Workspace relay | Single mailbox |
|---|---|---|---|
| Best for | hosted/AWS-native deployments | Workspace shops | quick-start / low volume |
| Daily cap | very high (prod access) / 200 sandbox | 10,000 | ~2,000 |
| Setup | verify domain + DKIM | admin opens the relay | self-service App Password |

Recommendation: SES for an AWS-native deployment (cheap, purpose-built for
transactional mail with bounce/complaint handling); otherwise whatever relay the
self-hosting org already has. Always send from a **role address**
(`notifications@yourdomain.com`), never a person's mailbox — a personal App
Password silently breaks the day that person leaves or resets 2FA.

**⚠️ Use port 587 (STARTTLS), not 465.** AxiaOps's transport doesn't speak implicit
TLS on 465.

**SES**: SMTP settings → Create SMTP credentials (mints an SMTP username/password,
*not* your AWS access keys) → verify the From domain → note the regional endpoint
(`email-smtp.<region>.amazonaws.com:587`).

**Google Workspace** (two options):
- **App Password on one mailbox** — simplest, sends as that one address. Requires
  2-Step Verification on; Security → App passwords → create one (16 chars, remove
  spaces when pasting).
- **Workspace SMTP relay** (`smtp-relay.gmail.com`, any address in your domain,
  10,000/day) — an admin turns on the relay (Admin console → Gmail → Routing → SMTP
  relay service → allowed senders = only my domains, require SMTP auth + TLS), then
  an App Password is minted **on the exact mailbox you'll authenticate as** (must be
  a real licensed user, not an alias/group) — a password minted on a different
  account fails with `535` even though it's valid. **From must be the same domain as
  the SMTP username** — you can't log in as one domain and send From another.

Add the channel: **Settings → Integrations → Add channel → Email (SMTP)** → pick a
Provider preset (SES / Google Workspace relay, auto-fills host+port) or Custom → fill
host/port/username/password/From/recipients → **Save → Test → Enable**.

### Deliverability (do this once, or digests go to spam)

1. **SPF + DKIM + DMARC** on your sending domain — highest leverage. SES: `v=spf1
   include:amazonses.com ~all` + Easy DKIM. Workspace: `v=spf1
   include:_spf.google.com ~all` + Authenticate-email DKIM. DMARC: start permissive
   (`v=DMARC1; p=none; rua=mailto:dmarc@yourdomain.com`).
2. Send from a **role address**, never a person's mailbox.
3. **Verify the From address/domain** at the relay — SES silently drops mail from an
   unverified sender.

### Tuning & troubleshooting

Each channel has two knobs: **minimum monthly savings** (gate, default $25) and
**digest size** (top-N services shown, default 10). On **Edit**, secret fields show
as `***` — leave them to keep the stored secret, type a new value to rotate.
Deleting a channel deletes its delivery history. The **Deliveries** drawer on each
channel row shows recent sends (`sent`/`failed`, `scan` vs `test` source) — a scan
that finds nothing above the gate records no row, so an empty drawer on a quiet org
is expected.

| Failure | Likely cause |
|---|---|
| Slack `failed`, 4xx | Webhook revoked — recreate it |
| Teams `failed`, 4xx | Webhook revoked or the Workflow was deleted — regenerate it. Also check it is a **Workflows** URL, not a legacy Connectors one |
| Teams `sent` but the card looks truncated | A `TextBlock` is missing `wrap: true` — a rendering bug on our side, not a config problem |
| Email `failed`, `535` | Wrong SMTP credentials. Workspace: must be an App Password (not login password) minted on the same mailbox as the SMTP username, which must be a real user (not alias/group) |
| Email `failed`, `EOF` | Relay dropped the connection — often an unauthenticated send the relay refused, or (historically) a non-domain HELO |
| Email `failed`, timeout | Host/port unreachable, or the channel is pointed at port 465 |
| Status `sent` but nothing arrives | Check spam folder; confirm the From address is verified at the relay |

### Security model

Channel secrets (SMTP password, webhook URL) are AES-256-GCM encrypted at rest,
never returned on read (API redacts to `***`), scrubbed from error bodies before
they're logged or stored on the dispatch row, and RLS-isolated per org. Configuring
a channel requires `channels:manage` (admin+) — the same tier as deleting a cloud
account, since a channel holds outbound-send credentials.

Notifications dispatch synchronously inside the scan, best-effort — a failing
channel is logged but never fails the scan. No retry in v1; re-send with **Test** or
wait for the next scan.
