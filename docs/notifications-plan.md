# Notification channels — implementation plan

Outbound notification channels for zombie findings. v1 ships **email + Slack** in one MR; **Jira (#113)** and **Teams (#114)** are separate follow-ups that reuse the foundation laid here.

## Scope

Today, zombie findings are only visible by logging into the dashboard. This plan adds an org-scoped notification surface: an admin configures one or more channels, each with a transport (email, Slack, …) and a trigger rule; ingestion dispatches a post-scan message to every matching channel.

**Outbound only, no inbound parsing.** Cost Explorer + the existing CloudWatch path are the authoritative cost data sources; parsing AWS billing emails would be a lossy duplicate. v1 = "send a digest to a channel when a scan completes". Per-zombie alerts and inbound email are deferred.

**Email transport for v1 = SES or any SMTP relay.** AWS-only product, SES is already in the IAM blast radius, and a generic SMTP fallback covers self-hosted customers who route via their own MTA. No third-party SDK dependency.

## Data model

Two new tables, both RLS-scoped on `organization_id`, mirroring the `accounts` table shape. Schema is provisioned for all four planned kinds even though only two transports ship in v1 — `kind` is a `CHECK` enum, not a foreign key, so widening it later costs a deploy-time downtime gap. Pre-provisioning it now lets follow-up MRs for #113 / #114 ship without a migration.

`migrations/030_notification_channels.up.sql`:

```sql
CREATE TABLE axiaops.notification_channels (
  id              UUID PRIMARY KEY DEFAULT gen_random_uuid(),
  organization_id UUID NOT NULL REFERENCES axiaops.organizations(id) ON DELETE CASCADE,
  kind            TEXT NOT NULL CHECK (kind IN ('email', 'slack', 'teams', 'jira')),
  label           TEXT NOT NULL,
  enabled         BOOLEAN NOT NULL DEFAULT TRUE,
  trigger_rule    JSONB NOT NULL DEFAULT '{"min_monthly_savings_usd":25,"digest_top_n":10,"on":["new_zombies"]}'::jsonb,
  -- Two decoupled knobs:
  --   min_monthly_savings_usd  — gate, "is this scan worth notifying about?"
  --   digest_top_n             — body trim, "how many findings to list in the message"
  config_ciphertext TEXT NOT NULL,
  -- AES-256-GCM (hex-encoded, nonce-prepended — matches `crypto.Encrypt` output
  -- and the `accounts.secret_encrypted` precedent; do NOT use BYTEA, the codec
  -- returns a hex string and a column-type mismatch would force a wrapper).
  -- Kind-discriminated decoded shape:
  --   email: {"smtp_host","smtp_port","smtp_user","smtp_pass","from","recipients":["…"]}
  --   slack: {"webhook_url"}
  --   teams: {"webhook_url","format":"messagecard|adaptive_card"}   (follow-up)
  --   jira:  {"site_url","project_key","issue_type","account_email","api_token"}  (follow-up)
  created_at         TIMESTAMPTZ NOT NULL DEFAULT now(),
  updated_at         TIMESTAMPTZ NOT NULL DEFAULT now()
  -- NB: no `last_dispatched_at` / `last_error` denormalised columns — read the
  -- newest `notification_dispatches` row per channel instead. The index on
  -- `(channel_id, created_at DESC)` below makes this a cheap join.
);
ALTER TABLE axiaops.notification_channels ENABLE ROW LEVEL SECURITY;
CREATE POLICY tenant_isolation ON axiaops.notification_channels
  USING (organization_id = current_setting('app.organization_id', true)::uuid);

CREATE TABLE axiaops.notification_dispatches (
  id                    UUID PRIMARY KEY DEFAULT gen_random_uuid(),
  organization_id       UUID NOT NULL REFERENCES axiaops.organizations(id) ON DELETE CASCADE,
  channel_id            UUID NOT NULL REFERENCES axiaops.notification_channels(id) ON DELETE CASCADE,
  snapshot_id           UUID REFERENCES axiaops.zombie_snapshots(id) ON DELETE SET NULL,
  account_id            TEXT REFERENCES axiaops.accounts(id) ON DELETE SET NULL,
  status                TEXT NOT NULL CHECK (status IN ('queued','sent','failed','skipped_threshold')),
  zombie_count          INT,
  monthly_savings_cents BIGINT,
  attempts              INT NOT NULL DEFAULT 0,
  external_ticket_id    TEXT,   -- External-system row key (Jira ticket key, Linear ID, GitHub issue #). NULL for fire-and-forget kinds. Partial unique index below enforces "one open row per channel per external resource".
  error                 TEXT,
  dispatched_at         TIMESTAMPTZ,
  created_at            TIMESTAMPTZ NOT NULL DEFAULT now()
);
ALTER TABLE axiaops.notification_dispatches ENABLE ROW LEVEL SECURITY;
CREATE POLICY tenant_isolation ON axiaops.notification_dispatches
  USING (organization_id = current_setting('app.organization_id', true)::uuid);
CREATE INDEX ON axiaops.notification_dispatches (channel_id, created_at DESC);
CREATE UNIQUE INDEX ON axiaops.notification_dispatches (channel_id, external_ticket_id) WHERE external_ticket_id IS NOT NULL;
```

`config_ciphertext` keeps the kind-specific transport blob opaque to SQL — the only thing the DB indexes on is `(organization_id, kind, enabled)`. **Use one ciphertext column, not per-kind columns**: the shape diverges enough that flattening would balloon the schema, and the dispatcher decrypts and unmarshals into a kind-typed Go struct anyway.

**Why `external_ticket_id` ships in v1 even though Jira is deferred:** Jira's dedup story (re-flagging an already-open zombie should *update* the existing ticket, not create a duplicate) needs a stable per-channel external-key with a partial unique index. The column is named generically — Linear, GitHub Issues, ServiceNow, etc. would all reuse it — so it isn't a speculative-abstraction smell despite Jira being the only known consumer today. Pre-provisioning the column + unique index now means #113 ships as transport-only without touching the schema; backfilling it later would require a coordinated `axiaops_owner` migration in prod, which is a human-gated apply.

## Files

- New: `services/shared/storage/postgres/migrations/030_notification_channels.{up,down}.sql`
- Extend: `services/shared/storage/storage.go` — `Store` interface
- Extend: `services/shared/storage/postgres/postgres.go` — impl. The `DeleteOrganizationCascade` table slice (`postgres.go:2206-2215` at time of writing) must list `notification_dispatches` **before** `notification_channels` (FK-safe order).
- New: `services/shared/model/notification.go` — `NotificationChannel`, `NotificationDispatch`, `NotificationPayload`, transport config structs.
- New package: `services/shared/notifications/` — `dispatcher.go`, `transport.go` (interface), `email_smtp.go`, `slack_webhook.go`, `renderer.go`.
- New: `services/api/internal/api/channels.go` + `channels_test.go`. **CRUD precedent lives in `services/api/internal/api/handler.go:695-870`** (`listAccounts`, `createAccount`, `updateAccount` — the redact-on-read / re-encrypt-only-on-non-mask-PATCH UX comes from there). `handler.go` is 1000+ lines today, and this MR is a good moment to extract the account-CRUD block into its own `accounts.go` while `channels.go` is being created — but that extraction is in-scope only if it stays small; defer if it bloats the review.
- Extend: `services/api/internal/api/handler.go` — register the `/v1/channels` routes alongside the existing handlers.
- Extend: **`services/shared/authz/roles.go`** — add `PermChannelsRead`, `PermChannelsManage` next to the existing `PermAccountsRead/Write/Delete/Scan` constants. The api-side seam (`services/api/internal/middleware/authz.go`) is where the route gate uses them; no new authz package.
- Extend: `services/ingestion/cmd/main.go` — dispatch hook after `SaveSnapshotServices` (locate by symbol name; line numbers drift between releases).
- New: `services/dashboard/src/screens/IntegrationsScreen.jsx`.
- Extend: `services/dashboard/src/screens/AccountSettingsScreen.jsx` — Integrations tab link.

## Dispatch seam

`services/ingestion/cmd/main.go` `runIngestionCore` ends at `SaveResources`. Right before that, `SaveSnapshot` then `SaveSnapshotServices` persist the per-scan deltas — this is the natural "scan finished, here's what changed" cut. The snapshot row + `summary.ByService` map are already in scope. Locate by symbol name, not line number — the file drifts between releases.

```go
// After SaveSnapshotServices:
notifications.DispatchForScan(ctx, store, snap, summary, accountID)
```

`DispatchForScan` is **synchronous, non-fatal, per-org via the RLS-bound pool**:
- Loads enabled channels for the org via a new `Store.ListEnabledChannels(ctx)` method. **This is per-org data, so use the RLS-bound app pool**, not `adminPool` — `ctx` already carries `organization_id` by the time it reaches `runIngestionCore` (set on the scan job; see how `SaveSnapshot` reads `OrganizationIDFromCtx` and calls `setOrganization(ctx, tx)`). **Do NOT copy `ListAllAccounts` as a template — that method intentionally uses `adminPool` for cross-org scheduled-scan enumeration, which would silently bypass RLS here.**
- Per channel: evaluates `trigger_rule` against `summary` (e.g. `summary.PotentialMonthlySave >= min_monthly_savings_usd`); if it fires, decrypts `config_ciphertext` and calls `Transport.Send(ctx, channel, payload)` under a **per-transport timeout** (`ctx, cancel := context.WithTimeout(ctx, 10*time.Second); defer cancel()`).
- Writes one `notification_dispatches` row per attempt (`status=sent|failed|skipped_threshold`).
- Errors are logged + recorded on the dispatch row, never abort the scan. Same posture as the existing `SaveSnapshot` error path.

**Why synchronous in v1.** SMTP send is ~200 ms; scans already take minutes; synchronous keeps the dispatch row's lifecycle inside the same `ctx.Done()` shutdown window. If a transport turns flaky and starts blocking scans, v2 = push a `NotificationJob` envelope into the existing `queue.Queue` (the Redis path already handles HMAC-signed envelopes for free — see `docs/c1-hmac-plan.md`).

**Why a per-transport timeout, not just a dispatcher-wide budget.** With 3 channels at no-timeout, a single Slack 5xx tail can add 30s+ latency to every scan. 10 s is generous for SMTP relay + Slack/Teams webhook; longer than 10 s, mark the dispatch `failed` and let the admin re-send via `/v1/channels/:id/test`. No in-process retry in v1 — surface failures in the UI immediately rather than burning the scan loop on backoff.

## Transport interface

```go
// services/shared/notifications/transport.go
type Payload struct {
    OrganizationID  string
    AccountID       string
    ZombieCount     int
    MonthlySavings  float64       // USD
    TopServices     []ServiceRow  // service, count, savings
    DashboardURL    string        // built from PUBLIC_HOST
    SnapshotID      string
}

type Transport interface {
    Send(ctx context.Context, channel model.NotificationChannel, payload Payload) (externalID string, err error)
}
```

`externalID` is empty for email/Slack/Teams (no persistent state); Jira returns the issue key (e.g. `OPS-1234`), which `Dispatcher` writes into `notification_dispatches.external_ticket_id`. On a re-dispatch the dispatcher does a SELECT-by-(`channel_id`, `external_ticket_id`) to decide PUT vs POST.

**Contract for `Send` implementations:**
- Respect `ctx` deadline — `http.Client{Timeout: 10*time.Second}` on the embedded client, **and** propagate `ctx` to the request so the dispatcher's `context.WithTimeout` cuts cleanly.
- Return `err` on transport-level failure (4xx/5xx, network, dial). Do **not** retry inside `Send`; the dispatcher records a `status=failed` row and the admin re-sends via `/v1/channels/:id/test`.
- Scrub bearer-token URLs from returned error strings. Slack's 404 page sometimes echoes the webhook URL; a naive `fmt.Errorf("slack: %s", body)` leaks the secret into `notification_dispatches.error` (and from there into the dashboard's "Recent deliveries" drawer). Use a `redactURL(err.Error(), channel.ConfigURLs())` helper.

## API surface

Mirror the account-CRUD block currently inside `services/api/internal/api/handler.go:695-870`:

| Method | Path                            | Permission         |
|--------|----------------------------------|--------------------|
| GET    | `/v1/channels`                   | `channels:read`    |
| POST   | `/v1/channels`                   | `channels:manage`  |
| PATCH  | `/v1/channels/{id}`              | `channels:manage`  |
| DELETE | `/v1/channels/{id}`              | `channels:manage`  |
| POST   | `/v1/channels/{id}/test`         | `channels:manage`  |
| GET    | `/v1/channels/{id}/dispatches`   | `channels:read`    |

- Encrypt-on-write / decrypt-on-read for `config_ciphertext` follows the `accounts.secret_encrypted` pattern in `handler.go`.
- **PATCH redacts secrets on read** (return `"smtp_pass":"***"`, `"webhook_url":"***"`, `"api_token":"***"`); only re-encrypts if the field comes back non-empty/non-mask in the request — same UX as `accounts.secret_encrypted`.
- `POST /test` requires `channels:manage` (it produces side effects: a real outbound HTTP/SMTP call + an audit row). Body is empty — the endpoint synthesizes a **fixed synthetic** `Payload` (5 zombies, $123.45/mo, 3 mock services). Keeping the body fixed keeps the surface boring; if customers later ask "let me test with my own preview content", that's a v2 toggle. Writes a dispatch row with `status='sent'|'failed'`. Required before flipping `enabled=true` on first save (UX gate).
- Audit_log every CRUD via `axiaops.io/api/internal/audit` with new `AuditActionChannelCreated/Updated/Deleted/Tested` constants added to `services/shared/model/audit.go`.

## Dashboard

New `services/dashboard/src/screens/IntegrationsScreen.jsx`, linked from `AccountSettingsScreen.jsx` as a tab (no new top-level route). Shape mirrors `ConnectScreen.jsx`:

- Channel list — kind icon, label, enabled toggle, last-dispatch timestamp + status.
- "Add channel" picker → kind-specific form (`EmailChannelForm`, `SlackChannelForm`) — both submit to `POST /v1/channels` with a `kind` field.
- "Send test" button → `POST /v1/channels/:id/test` → toast on result.
- "Recent deliveries" drawer → `GET /v1/channels/:id/dispatches`.

Reference `AuditScreen.jsx` for the table+filter idiom; invoke the `dashboard-screen` skill at implementation time.

## Why email + Slack ship together

90% of the diff is shared between any two transports: schema, RLS, crypto, store methods, CRUD endpoints, dispatcher, audit, permissions, dashboard list. The transport-specific code per kind is ~50–80 lines (one Go file + one React form component).

Shipping both kinds in one MR is what proves the `Transport` interface is genuinely polymorphic rather than email-shaped pretending to be generic. The `config_ciphertext jsonb` shape is *only obviously right* when two concrete kinds force the abstraction in the same review.

**Caveat:** if HTML email rendering blows up (MIME, plaintext fallback, Outlook CSS), split: land schema + dispatcher + Slack first (JSON is trivially testable), email follow-up. Slack is the simpler transport to validate the seam against.

## Why Jira (#113) and Teams (#114) are separate

Each is genuinely smaller scope post-foundation — schema is already provisioned, dispatcher already calls `Transport.Send` polymorphically — but each has a domain-specific concern that's worth its own review:

- **Jira (#113)** is a different mental model: it creates *issues* with persistent state, dedup keyed by `external_ticket_id`, and a likely v2 "close issue on dismiss" flow. Mixing this into the foundation muddies the polymorphism signal. Foundation MR pre-provisions `external_ticket_id` + its partial unique index; the Jira MR adds only `jira_rest.go` + form component.
- **Teams (#114)** is mid-migration on the wire format: Microsoft is deprecating Office 365 Connectors (MessageCard) in favor of Power Automate Workflows (Adaptive Card). Locking in a format today risks rework in 6 months. Worth letting the upstream guidance settle.

## Implementation order

1. Migration 030 + `Store` methods + Postgres impl + integration tests (`db-migration` skill).
2. `services/shared/notifications/` package — `Transport` interface, `Payload`, `Renderer`, `Dispatcher`. Unit tests with a fake transport.
3. `email_smtp.go` + `slack_webhook.go` — table-driven tests against stub servers (`httptest.NewServer` for Slack, `net.Listen` minimal SMTP capture for email).
4. API CRUD endpoints (`api-endpoint` skill) + audit + permissions + handler tests.
5. Wire `notifications.DispatchForScan` into `runIngestionCore` after `SaveSnapshotServices`.
6. Dashboard screen (`dashboard-screen` skill) — list, two add-forms, test button, dispatches drawer.
7. Env-var / permission docs update.

## Risks + deferred

- **GDPR cascade — easy to miss.** Recipient emails live in `config_ciphertext`. The cascade-delete chain (`organizations → notification_channels → notification_dispatches`) makes erasure automatic at the DB level, but the org-erasure helper `DeleteOrganizationCascade` in **`services/shared/storage/postgres/postgres.go:2181`** uses a hard-coded table slice (lines 2206-2215 at time of writing) to drive the per-tenant delete loop, and that slice must list `notification_dispatches` **before** `notification_channels`. Reviewer should grep for `DeleteOrganizationCascade` and confirm the order.
- **First-scan storm — two decoupled knobs.** `trigger_rule.min_monthly_savings_usd` is the **gate** ("is this scan worth notifying about?") and `trigger_rule.digest_top_n` is the **body trim** ("how many findings to list in the message"). The original single-knob design conflated them — a $100 gate would silence small-org demos where the first scan surfaces $40 of real findings. Defaults: gate=$25 (covers one mid-size EBS / a stopped instance / a handful of orphaned snapshots, while suppressing single-Lambda noise around $13/mo), digest_top_n=10. The product's whole value prop is finding the $10–$50 zombies the cloud team forgot, so the gate must not silence them. Avoid `enabled=false` default — sharper UX cliff than a permissive gate.
- **Slack/Teams webhook URLs are bearer tokens — redact + scrub.** Encrypt the whole URL (in `config_ciphertext`), redact in API responses (`"webhook_url":"***"`), **and** scrub from `notification_dispatches.error` strings before write — Slack's 404 page sometimes echoes the URL into its response body and a naive error wrap leaks it into the dispatches drawer in the dashboard. `redactURL(err.Error(), channel.ConfigURLs())` helper in the dispatcher.
- **SMTP credential rotation UX.** PATCH with empty `smtp_pass` = "keep existing" (same convention as `accounts.secret_encrypted` in `handler.go`). Document on the form.
- **Per-user opt-out / preferences.** Out of scope. Channels are org-level. v2 might add `trigger_rule.owner_team = "platform"` as a filter.
- **Retry / DLQ.** v1 = single attempt + 10 s per-transport timeout; `attempts` column is reserved for v2. Failed dispatch is visible in the UI; admin re-sends via `/test`.
- **Rate limits.** SES sandbox = 200/day; one email per channel per scan stays well below. Slack incoming-webhooks cap at ~1 req/sec per webhook, Teams at ~4 req/sec — neither bites at one-per-scan. Add a per-org daily counter only if customers report bumping into it.
- **Email digest vs per-zombie.** v1 = one digest per scan, body-trimmed to `digest_top_n` by per-resource savings descending. Per-zombie alerts are a v2 `trigger_rule.on = ["new_zombie"]` mode joining against the previous snapshot.
- **License gating is a non-issue today.** `services/shared/license/license.go` doesn't currently track a notification quota; `license.IsScanAllowed` already gates upstream so the dispatch loop never runs on an expired license. Flag for v2 only if customers ship abuse.

## References

- Architect's analysis that produced this plan: in-conversation, 2026-05-30 alpha.22 release session.
- Issue #113 — Jira channel (this plan is its foundation).
- Issue #114 — Microsoft Teams channel (this plan is its foundation).
- Crypto pattern: `services/shared/crypto/`.
- Audit pattern: `services/api/internal/audit/` + `services/shared/model/audit.go`.
- Dispatch precedent (synchronous, scan-anchored): `services/ingestion/cmd/main.go` `runIngestionCore`.
