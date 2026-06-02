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
2. **Add channel** → Type = **Slack webhook**.
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

## Choosing a sender: SES vs Workspace relay vs single mailbox

AxiaOps only speaks **SMTP** — SES, a Google Workspace relay, and a single mailbox are all
just different SMTP endpoints you point a channel at (SES exposes an SMTP interface,
`email-smtp.<region>.amazonaws.com:587`). So this is a deployment choice, not a product
one; the setup is identical (host / port 587 / username / password / From).

| | **SES** | **Workspace relay** | **Single mailbox** |
|---|---|---|---|
| Best for | the AxiaOps-hosted product; any AWS-native deployment | Workspace shops wanting a service sender | quick-start / low volume |
| Send as | any verified domain address | any `@axiaops.io` address | one mailbox (+aliases) |
| Daily cap | very high (request prod access; sandbox = 200/day) | 10,000 | ~2,000 |
| Cost | ~$0.10 / 1,000 emails → **cents/month** at digest volume | included in Workspace | included in Workspace |
| Setup | verify domain/identity + DKIM in AWS | admin opens the relay | self-service App Password |

**Recommendation**
- **AxiaOps-hosted / prod → SES.** AWS-native, already in the IAM blast radius, purpose-built
  for transactional mail (bounce/complaint handling, suppression lists), and effectively
  free at notification volume.
- **Self-hosted customer → their choice** — SES if they're on AWS, otherwise their own
  relay or mailbox. AxiaOps treats all three identically.
- **Single personal mailbox → quick-start only**, and even then use a **role** address
  (`notifications@axiaops.io`), never a person's account.

**"Don't I need the emails in a central place?"** No special setup required — **AxiaOps
already records every send** in `notification_dispatches`, surfaced in the per-channel
**deliveries drawer** (status, time, error). *That* is your audit trail. A mailbox/relay
also leaves a copy in its **Sent folder**; **SES does not** (it's fire-and-forget — you'd
archive to S3 or BCC a mailbox if you specifically wanted browsable copies). Since the
dispatches log already answers "what did we send / did it land," SES's lack of a Sent
folder is **not** a reason to prefer a mailbox.

> Whatever you pick, the [deliverability](#email-deliverability-do-this-once-or-digests-go-to-spam)
> items (SPF/DKIM/DMARC + role From) matter far more than the sender choice.

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

> ⚠️ **Use port 587 (STARTTLS), not 465.** AxiaOps's email transport opens a plain
> connection and upgrades with STARTTLS — it does **not** speak implicit TLS
> (SMTPS) on port 465. A channel pointed at 465 will fail to connect. Every relay
> below supports 587.

#### Sending from a Google Workspace `@axiaops.io` address

Google Workspace gives you two ways to send. Pick one:

**Option A — App Password (simplest; sends as one mailbox).** Good when a single
mailbox (e.g. `finops@axiaops.io`) is the sender.

1. The sending Workspace account must have **2-Step Verification** enabled.
2. In that account: **Google Account → Security → 2-Step Verification → App
   passwords** → create one (a 16-character string). This is the SMTP password —
   *not* the account's login password.
   - If you don't see "App passwords", a Workspace admin has disabled them
     org-wide → use Option B.
3. SMTP settings:
   - **host** `smtp.gmail.com` · **port** `587` (STARTTLS)
   - **username** the full address, e.g. `finops@axiaops.io`
   - **password** the 16-char App Password (paste without spaces)
   - **from** the same address (or a "Send as" alias that mailbox is authorised
     to use)
   - Limit: ~2,000 messages/day on Workspace (external recipients count). One
     digest per scan stays far below this.

**Option B — Workspace SMTP relay (`smtp-relay.gmail.com`; send from any
`@axiaops.io` address, higher limits).** Better for a service sender. Step-by-step for an
`axiaops.io` Google Workspace account:

**B1. Enable the relay (Workspace super-admin, one-time).**
1. Sign in at **admin.google.com** as a **super administrator**.
2. **Apps → Google Workspace → Gmail → Routing**.
3. Find **SMTP relay service** → **Configure** (or *Add another rule* if one exists).
4. Name it, e.g. `AxiaOps notifications`.
5. **Allowed senders** → select **"Only addresses in my domains"** (prevents an open relay).
6. **Authentication** → tick **"Require SMTP Authentication"** (recommended for AxiaOps —
   its sender has no fixed egress IP). *Only* if you have a static egress IP and prefer
   network-level trust, instead tick **"Only accept mail from the specified IP addresses"**
   and add that IP (then you can skip the App Password in B2).
7. **Encryption** → tick **"Require TLS encryption"**.
8. **Save.** Propagation is usually minutes but can take up to ~24h.

**B2. Create an App Password (skip if you chose the IP-allowlist path).** Do this on a
**role** mailbox you'll send through, e.g. `notifications@axiaops.io` — not a personal one.
1. Sign in as that mailbox → **myaccount.google.com → Security**.
2. Ensure **2-Step Verification** is **ON** (App Passwords require it).
3. Open **App passwords** → create one named `AxiaOps` → copy the **16-char** value.
   - No "App passwords" option? A super-admin must allow it (**Admin console → Security →
     Authentication → "Allow users to manage their app passwords"**), and 2SV must not be
     security-key-only (which disables app passwords).

**B3. Authenticate the domain for deliverability** (see the
[Email deliverability](#email-deliverability-do-this-once-or-digests-go-to-spam) section):
ensure `axiaops.io` SPF includes `include:_spf.google.com`, and turn on DKIM at
**Admin console → Apps → Google Workspace → Gmail → Authenticate email**.

**B4. Create the channel in AxiaOps** (Settings → Integrations → Add channel → Type =
Email (SMTP)):
   - **host** `smtp-relay.gmail.com` · **port** `587` (STARTTLS)
   - **username / password** the role mailbox (`notifications@axiaops.io`) + its App Password
     — leave both blank only if you chose the IP-allowlist path in B1.6
   - **from** any `@axiaops.io` address (e.g. `notifications@axiaops.io`)
   - **recipients** comma-separated
   - **Save → Test → confirm arrival → enable.** Limit: 10,000 messages/day.

### 2. Create the channel in AxiaOps

1. Dashboard → **Settings → Integrations → Add channel** → Type = **Email (SMTP)**.
2. Fill in:
   - **Label** — e.g. `Platform team digest`
   - **SMTP host** — `email-smtp.eu-central-1.amazonaws.com` (SES) or
     `smtp.gmail.com` (Workspace App Password) / `smtp-relay.gmail.com` (Workspace relay)
   - **SMTP port** — `587` (STARTTLS — **not** 465)
   - **SMTP username** — the SES SMTP username, or the Workspace mailbox
     (`finops@axiaops.io`); leave blank only for an IP-allowlisted relay
   - **SMTP password** — the SES SMTP password, or the Workspace **App Password**
   - **From address** — a verified/authorised sender, e.g. `finops@axiaops.io`
   - **Recipients** — comma-separated, e.g. `alice@axiaops.io, bob@axiaops.io`
3. (Optional) adjust the trigger gate / digest size.
4. **Save** (created disabled).

### 3. Test, then enable

Same as Slack: **Test** → confirm the email arrives → flip to **enabled**.

## Email deliverability (do this once, or digests go to spam)

The relay choice (SES vs Workspace, Option A vs B) matters far less than these. A **Test
that "sent" but never arrived** is almost always one of these missing — the message left
AxiaOps fine and the receiver dropped or spam-binned it.

1. **Authenticate the domain — SPF + DKIM + DMARC** (highest leverage):
   - **SPF** — a DNS TXT record on `axiaops.io` authorizing your sender. SES:
     `v=spf1 include:amazonses.com ~all`; Google Workspace: `v=spf1 include:_spf.google.com ~all`.
     (One SPF record per domain — merge includes if you use both.)
   - **DKIM** — cryptographic signing. SES: enable "Easy DKIM" on the verified identity and
     add the 3 CNAMEs it gives you. Workspace: Admin console → Gmail → *Authenticate email*
     → generate the key → publish the DKIM TXT record.
   - **DMARC** — a `_dmarc.axiaops.io` TXT record (start permissive:
     `v=DMARC1; p=none; rua=mailto:dmarc@axiaops.io`) so receivers trust aligned mail and
     you get visibility before tightening to `quarantine`/`reject`.

2. **Send from a dedicated *role* address, never a person's mailbox** — e.g.
   `notifications@axiaops.io`, not `alice@axiaops.io`. A personal mailbox's App Password
   silently breaks the day that person leaves, resets 2-Step Verification, or is suspended.
   A role mailbox (or the domain relay) survives staffing changes.

3. **Verify the From address/domain** at the relay — SES silently drops mail from an
   unverified sender; Workspace requires the From to be an address the authenticated
   mailbox may send as.

> **Bigger-picture best practice:** Google Workspace SMTP is built for *human* mail, not a
> product's transactional stream. For a real deployment prefer a dedicated ESP — **Amazon
> SES** is the natural fit (AWS-native, already in the IAM blast radius, purpose-built for
> transactional mail with bounce/complaint handling, scales past Workspace's 10k/day cap).
> Workspace SMTP is the convenient path for a self-hosted operator who already has it and
> sends low volume. Either way, items 1–3 above are non-negotiable.

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
  - **Email** `failed` `535`/auth — wrong SMTP username/password. For **Google
    Workspace**: you must use an **App Password**, not the account login password;
    2-Step Verification must be on; and if the message is `Username and Password
    not accepted`, App Passwords may be disabled org-wide (use the SMTP relay
    option) or the From isn't an address that mailbox may send as.
  - **Email** `failed` timeout / connection refused — host/port unreachable from
    the ingestion network, a wedged relay (10s per-transport cap), **or the channel
    is pointed at port 465** (implicit TLS — unsupported; switch to 587).
- Nothing arriving but status is `sent`: check the Slack channel/email spam folder,
  and confirm the From address is verified (SES silently drops unverified senders).

## Security model

How AxiaOps protects the credentials you enter, and the choices behind the setup
guidance above. The through-line is **limit the blast radius if any one thing leaks** —
no layer is assumed perfect.

**What AxiaOps does with a channel's secrets (built in):**
- **Encrypted at rest** — the SMTP password / webhook URL is AES-256-GCM encrypted
  (`ENCRYPTION_KEY`) before it touches the database, so a stolen DB dump or backup yields
  ciphertext, not credentials.
- **Never returned on read** — the API redacts secret fields to `***`; the real value never
  leaves the server, so it can't be read back via the API, the browser, or shoulder-surfed
  in the form. Editing other fields and re-saving `***` keeps the stored secret.
- **Scrubbed from errors** — a transport strips its own bearer secret (webhook URL / SMTP
  password) from any error before it's stored on the dispatch row or logged (Slack's 404
  body sometimes echoes the webhook URL — this stops it leaking into the deliveries drawer).
- **Tenant-isolated** — Row-Level Security on the channel + dispatch tables means one
  organization physically cannot read another's channels or delivery history.
- **Least privilege** — configuring a channel requires `channels:manage` (admin+), the same
  tier as deleting a cloud account, because a channel holds credentials and sends outbound;
  viewers can see channels exist but not the secrets.

**Choices behind the sending setup (your side):**
- **TLS in transit (STARTTLS / 587)** — credentials + message are never sent in cleartext.
- **App Password + 2-Step Verification, not the login password** — a scoped, revocable
  send-only credential; if it leaks it can send mail but can't log into or change the
  Google account.
- **Role address, not a person's mailbox** — org-owned, auditable, survives staff changes;
  avoids a service credential orphaned in someone's personal account.
- **SPF + DKIM + DMARC** — stop others spoofing `@axiaops.io` (and get your own mail
  trusted); DMARC reports surface attempted abuse.
- **Relay hardening** — *Allowed senders = only my domains* prevents an open relay;
  *Require SMTP Authentication + Require TLS* reject anonymous/cleartext callers. IP-allowlist
  (if you have a static egress IP) trades a stealable shared secret for network-level trust.

## Operator notes (deployment)

- Notifications dispatch **synchronously inside the scan**, best-effort: a failing
  channel is logged + recorded but **never fails the scan**.
- No retry in v1 — a `failed` delivery is visible in the Deliveries drawer; re-send
  with **Test** or wait for the next scan.
- `notification_dispatches` grows one row per (channel × scan); there is no
  automatic pruning yet — see the plan's "Risks + deferred" for the retention
  follow-up.
