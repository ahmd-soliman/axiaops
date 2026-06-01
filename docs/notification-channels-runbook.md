# Runbook — Add a Slack or Email notification channel

Audience: an AxiaOps **admin or owner** of an organization who wants scan results
delivered to Slack or email. Background/design: [`notifications-plan.md`](notifications-plan.md).

## What a channel does

After every scan, AxiaOps sends a **digest** (zombie count, potential monthly
savings, top services) to each **enabled** channel whose trigger gate is met.
Channels are **organization-level** — they're shared by the whole org, not
per-user and not per-cloud-account.

- **Email** transport = any SMTP relay (AWS SES, Postmark, your own MTA).
- **Slack** transport = an incoming-webhook URL.

Two follow-up transports (Teams #114, Jira #113) are pre-provisioned in the
schema but not yet shippable — the UI only offers Email and Slack.

## Prerequisites

- Role **admin** or **owner** (the `channels:manage` permission). Viewers can't
  see the Integrations tab.
- The credential for the transport you're adding (below).
- The deployment must have `ENCRYPTION_KEY` set (it always is — it's the same key
  that encrypts cloud-account secrets). Channel config is encrypted at rest with it.
- For dashboard deep-links inside the message, the **ingestion** service needs
  `PUBLIC_HOST` set (e.g. `https://app.example.com`). Optional — if unset, the
  message just omits the link.

---

## Add a Slack channel

### 1. Create a Slack incoming webhook

1. In Slack: **Apps → Incoming Webhooks → Add to Slack** (or create a Slack app
   with the *Incoming Webhooks* feature enabled).
2. Pick the channel to post to (e.g. `#finops-alerts`).
3. Copy the webhook URL — it looks like
   `https://hooks.slack.com/services/T00000000/B00000000/XXXXXXXXXXXXXXXXXXXXXXXX`.
   **Treat it as a secret** — anyone with it can post to your channel.

### 2. Create the channel in AxiaOps

1. Dashboard → avatar menu → **Settings → Integrations**.
2. **Add channel** → Kind = **Slack webhook**.
3. **Label**: a human name, e.g. `FinOps Slack`.
4. **Webhook URL**: paste the URL from step 1.
5. (Optional) adjust the trigger — **Minimum monthly savings** (default `$25`)
   and **Digest size** (default `10` services).
6. **Save**. The channel is created **disabled**.

### 3. Test, then enable

1. Click **Test** on the row. A synthetic digest (5 zombies, $123.45/mo) is sent
   immediately. A green banner = delivered; a red banner shows the failure reason.
2. Confirm the message arrived in Slack.
3. Click the **disabled** status toggle to flip it to **enabled**. Done — the next
   scan will post a real digest if it clears the savings gate.

---

## Add an Email (SMTP) channel

### 1. Get SMTP credentials

Pick a relay. For AWS SES (already in the product's IAM blast radius):

1. SES → **SMTP settings → Create SMTP credentials** (this mints an IAM user with
   an SMTP username/password — note these are *not* your AWS access keys).
2. Verify the **From** address or its domain in SES.
3. Note the SMTP endpoint for your region, e.g. `email-smtp.eu-central-1.amazonaws.com`,
   port `587` (STARTTLS).

Any other relay (Postmark, Mailgun, on-prem Postfix) works the same way — you need
host, port, username, password, and a verified From address.

> SES sandbox accounts can only send to verified recipients and cap at 200/day —
> request production access before pointing real recipients at it.

### 2. Create the channel in AxiaOps

1. Dashboard → **Settings → Integrations → Add channel** → Kind = **Email (SMTP)**.
2. Fill in:
   - **Label** — e.g. `Platform team digest`
   - **SMTP host** — e.g. `email-smtp.eu-central-1.amazonaws.com`
   - **SMTP port** — `587`
   - **SMTP username** — the SES SMTP username (leave blank only for an
     unauthenticated relay)
   - **SMTP password** — the SES SMTP password
   - **From address** — a verified sender, e.g. `finops@example.com`
   - **Recipients** — comma-separated, e.g. `alice@example.com, bob@example.com`
3. (Optional) adjust the trigger gate / digest size.
4. **Save** (created disabled).

### 3. Test, then enable

Same as Slack: **Test** → confirm the email arrives → flip to **enabled**.

---

## Tuning the trigger

Each channel has two independent knobs (see the plan's "First-scan storm" note):

| Knob | Meaning | Default |
|---|---|---|
| **Minimum monthly savings** | Gate — don't notify unless a scan finds at least this much potential savings. | `$25` |
| **Digest size** | Body trim — how many top services to list in the message. | `10` |

Set the gate **low** if you want to hear about the small ($10–$50) zombies the
cloud team forgot — that's the product's value. Set it higher to cut noise on a
big, noisy account.

---

## Editing & secret handling

- On **Edit**, secret fields (SMTP password, webhook URL) show as `***` — that's a
  mask, not the real value. **Leave them as `***` (or clear them) to keep the
  stored secret**; type a new value only to rotate it.
- **Kind is immutable** — to change Slack ↔ Email, delete and recreate.
- Deleting a channel also deletes its delivery history.

## Verifying & troubleshooting deliveries

- **Deliveries** on a channel row opens the recent delivery log (status + time +
  detail). You'll see `sent` and `failed` attempts; rows from the **Test** button
  are labelled `Test send` (the row's `source` is `test`, vs `scan` for real scans).
- **A scan that finds nothing above the gate records no row** — only real send
  attempts are logged, so the drawer stays a useful list of actual deliveries
  rather than a wall of "nothing to report". An empty drawer on a quiet org is
  expected: it means no scan has cleared the channel's savings gate yet (use
  **Test** to confirm the channel itself works).
- Common failures:
  - **Slack** `failed` with a 4xx — webhook URL revoked or wrong; recreate it.
    (AxiaOps scrubs the URL out of the stored error, so the detail won't echo it.)
  - **Email** `failed` `535`/auth — wrong SMTP username/password.
  - **Email** `failed` timeout — host/port unreachable from the ingestion network,
    or a wedged relay (10s per-transport cap).
- Nothing arriving but status is `sent`: check the Slack channel/email spam folder,
  and confirm the From address is verified (SES silently drops unverified senders).

## Operator notes (deployment)

- Notifications dispatch **synchronously inside the scan**, best-effort: a failing
  channel is logged + recorded but **never fails the scan**.
- No retry in v1 — a `failed` delivery is visible in the Deliveries drawer; re-send
  with **Test** or wait for the next scan.
- `notification_dispatches` grows one row per (channel × scan); there is no
  automatic pruning yet — see the plan's "Risks + deferred" for the retention
  follow-up.
