# Runbook — Add a Slack or Email notification channel

Audience: an AxiaOps **admin or owner** of an organization who wants scan results
delivered to Slack or email.

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

The reliable current method is a small Slack app (the legacy "Incoming Webhooks"
directory app is deprecated in many workspaces). You need permission to install apps in
the workspace — if installs are admin-approved, an admin does this or approves the request.

1. Go to **api.slack.com/apps** → **Create New App** → **From scratch**.
2. Name it (e.g. `AxiaOps`), pick the **workspace** → **Create App**.
3. App's left nav → **Incoming Webhooks** → toggle **Activate Incoming Webhooks** to **On**.
4. Scroll down → **Add New Webhook to Workspace**.
5. Pick the **channel** to post to (e.g. `#finops-alerts`) → **Allow**. (For a *private*
   channel, invite the app to it first: in Slack, `/invite @AxiaOps` in that channel.)
6. Back on the Incoming Webhooks page, **Copy** the webhook URL — it looks like
   `https://hooks.slack.com/services/T00000000/B00000000/XXXXXXXXXXXXXXXXXXXXXXXX`.
   **Treat it as a secret** — anyone with it can post to that channel. Each URL is bound to
   the single channel you picked; to post elsewhere, add another webhook (repeat steps 4–6).

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

**Optional — verify the webhook directly** (isolates "is the URL good?" from AxiaOps;
handy if **Test** fails and you want to know whether the webhook or the stored config
is at fault):

```bash
curl -i -X POST -H 'Content-type: application/json' \
  --data '{"text":"AxiaOps webhook test ✅"}' \
  'https://hooks.slack.com/services/T00000000/B00000000/XXXXXXXXXXXXXXXXXXXXXXXX'
```

- **`200` / body `ok`** → webhook valid; the message posts to its channel. If **Test**
  still fails, the URL stored on the channel is wrong — re-paste it.
- **`404` `no_service` / `no_team`** → URL revoked or wrong → recreate the incoming webhook.
- **`400` `invalid_payload`** → malformed JSON (curl-only; not something AxiaOps emits).

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

**Option B — Workspace SMTP relay (`smtp-relay.gmail.com`; send from any address in your
domain, 10,000/day).** Three one-time steps, then add the channel. The examples below use
the domain `axiaops.io` and the mailbox `notifications@axiaops.io` — substitute your own.

**B1 — Turn the relay on** (a Workspace **admin** of the domain, once):
1. **admin.google.com** → **Apps → Google Workspace → Gmail → Routing**.
2. Find **SMTP relay service** → **Configure**.
3. Set: **Name** = `AxiaOps`; **Allowed senders** = *Only addresses in my domains*;
   ☑ **Require SMTP Authentication**; ☑ **Require TLS encryption** → **Save**.
   *(Ignore "Only accept mail from specified IP addresses" — that's the no-password
   alternative, and it only works with a fixed public egress IP, which AxiaOps usually
   doesn't have. Use SMTP Authentication.)*

**B2 — Make an App Password** (on the mailbox you'll send as, e.g. `notifications@axiaops.io` —
use a shared/role address, not a personal one), once:
1. **The SMTP username must be a real, licensed Workspace *user* mailbox** — not an alias, a
   Group, or an address that only exists as a "send-as". You cannot authenticate as an
   alias/group, so create the user first if it doesn't exist (**admin.google.com → Directory →
   Users → Add new user**; first + last name are required — for a service account use
   something like `AxiaOps` / `Notifications`, which also becomes the friendly sender name).
   The `From` can still be an alias of that user.
2. Sign in **as that exact mailbox** → **myaccount.google.com → Security**.
3. **2-Step Verification must be ON** — App Passwords don't exist without it. Turn it on first.
4. Search **App passwords** → **Create** (name it `AxiaOps`) → copy the code. **Generate it
   while signed into the username mailbox** — an App Password minted on a *different* account
   authenticates as that other account and fails with `535 Username and Password not accepted`,
   even though the password itself is valid.
   - Google shows it as four spaced groups, e.g. **`abcd efgh ijkl mnop`**.
   - **Remove the spaces** → use **`abcdefghijklmnop`** (16 chars). That's the password.
   - No "App passwords" option? An admin disabled it, or 2SV is security-key-only — use
     Option A on a different mailbox, or SES.

**B3 — (deliverability)** ensure `axiaops.io` SPF includes `include:_spf.google.com` and DKIM
is on (**Admin console → Apps → Google Workspace → Gmail → Authenticate email**). See the
[deliverability section](#email-deliverability-do-this-once-or-digests-go-to-spam).

**B4 — Add the channel** (Settings → Integrations → Add channel → Type = Email (SMTP)).
**Pick Provider = "Google Workspace relay"** and it fills the host + port for you; you only
type the username, password, From, and recipients. Worked example — exactly what to enter:

| Field | Example value | Notes |
|---|---|---|
| Provider | `Google Workspace relay` | prefills host + port (below) |
| SMTP host | `smtp-relay.gmail.com` | auto-filled by the preset |
| SMTP port | `587` | auto-filled; STARTTLS — **not 465** (unsupported) |
| SMTP username | `notifications@axiaops.io` | **full email**, not just `notifications` |
| SMTP password | `abcdefghijklmnop` | the App Password, **spaces removed** |
| From | `notifications@axiaops.io` | must be **same domain as the username** (see rule below) |
| Recipients | `you@example.com, team@example.com` | **any** domain — recipients are unrestricted |

Then **Save → Test → confirm it arrives → toggle Enabled**.

**The rules that actually matter (this trips people up):**
- **From = same domain as the SMTP username.** Both must belong to the *same* Workspace.
  You authenticate as `notifications@axiaops.io`, so From must be `@axiaops.io`. You can't
  log in as one domain and send "From" another — Google's "only addresses in my domains"
  check rejects it.
- **Where AxiaOps runs does NOT matter.** It logs into Google over the internet with the
  App Password, so a local/dev/staging instance on a different domain works fine — identity
  is the *login*, not the network origin. (Origin only matters with the IP-allowlist option,
  which we're not using.)
- **Recipients can be any domain** — your personal inbox is fine for a test.

### 2. Create the channel in AxiaOps

1. Dashboard → **Settings → Integrations → Add channel** → Type = **Email (SMTP)**.
2. Fill in:
   - **Label** — e.g. `Platform team digest`
   - **Provider** — pick **Amazon SES** or **Google Workspace relay** to auto-fill the host
     + port (edit the SES region in the host if not `eu-central-1`); pick **Custom SMTP**
     to type them yourself (e.g. `smtp.gmail.com` for the single-mailbox App Password path,
     or any other relay).
   - **SMTP host / port** — auto-filled by the preset (`587`, STARTTLS — **not** 465), or
     typed for Custom.
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
    Workspace**: use an **App Password**, not the login password; 2-Step Verification
    must be on; the App Password must be generated **on the same mailbox as the SMTP
    username** (one minted on a different account → `535` even if the password is
    valid); and the username must be a **real user mailbox**, not an alias or Group
    (those can't authenticate). If the message is `Username and Password not accepted`,
    App Passwords may also be disabled org-wide (use SES) or the From isn't an address
    that mailbox may send as.
  - **Email** `failed` `email: send: EOF` — the relay dropped the connection. Against
    the Google Workspace relay this was historically the client greeting with a
    non-domain HELO (the relay answers `421 4.7.0 … closing connection. (EHLO)` and
    never advertises `AUTH`); the transport now EHLOs the sender's domain, so **upgrade
    if an old build is deployed**. It can also mean an **unauthenticated** send the
    relay refused (blank SMTP username while the relay requires auth) — set the
    username + App Password.
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
