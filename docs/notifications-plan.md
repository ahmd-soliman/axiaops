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
  trigger_rule    JSONB NOT NULL DEFAULT '{}'::jsonb,
  -- e.g. {"min_monthly_savings_usd": 100, "on": ["new_zombies"]}
  config_ciphertext BYTEA NOT NULL,
  -- AES-256-GCM. Kind-discriminated decoded shape:
  --   email: {"smtp_host","smtp_port","smtp_user","smtp_pass","from","recipients":["…"]}
  --   slack: {"webhook_url"}
  --   teams: {"webhook_url","format":"messagecard|adaptive_card"}   (follow-up)
  --   jira:  {"site_url","project_key","issue_type","account_email","api_token"}  (follow-up)
  last_dispatched_at TIMESTAMPTZ,
  last_error         TEXT,
  created_at         TIMESTAMPTZ NOT NULL DEFAULT now(),
  updated_at         TIMESTAMPTZ NOT NULL DEFAULT now()
);
ALTER TABLE axiaops.notification_channels ENABLE ROW LEVEL SECURITY;
CREATE POLICY tenant_isolation ON axiaops.notification_channels
  USING (organization_id = current_setting('app.organization_id', true)::uuid);

CREATE TABLE axiaops.notification_dispatches (
  id                    UUID PRIMARY KEY DEFAULT gen_random_uuid(),
  organization_id       UUID NOT NULL REFERENCES axiaops.organizations(id) ON DELETE CASCADE,
  channel_id            UUID NOT NULL REFERENCES axiaops.notification_channels(id) ON DELETE CASCADE,
  snapshot_id           UUID REFERENCES axiaops.zombie_snapshots(id) ON DELETE SET NULL,
  account_id            TEXT,
  status                TEXT NOT NULL CHECK (status IN ('queued','sent','failed','skipped_threshold')),
  zombie_count          INT,
  monthly_savings_cents BIGINT,
  attempts              INT NOT NULL DEFAULT 0,
  external_ticket_id    TEXT,   -- Jira ticket key for dedup/update semantics. NULL for fire-and-forget kinds.
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

**Why `external_ticket_id` ships in v1 even though Jira is deferred:** Jira's dedup story (re-flagging an already-open zombie should *update* the existing ticket, not create a duplicate) needs a stable per-channel external-id key with a partial unique index. Pre-provisioning the column + unique index now means #113 ships as transport-only without touching the schema; backfilling it later would require a coordinated `axiaops_owner` migration in prod.

## Files

- New: `services/shared/storage/postgres/migrations/030_notification_channels.{up,down}.sql`
- Extend: `services/shared/storage/storage.go` — `Store` interface
- Extend: `services/shared/storage/postgres/postgres.go` — impl
- New: `services/shared/model/notification.go` — `NotificationChannel`, `NotificationDispatch`, `NotificationPayload`, transport config structs
- New package: `services/shared/notifications/` — `dispatcher.go`, `transport.go` (interface), `email_smtp.go`, `slack_webhook.go`, `renderer.go`
- New: `services/api/internal/api/channels.go` + `channels_test.go`
- Extend: `services/api/internal/api/handler.go` — register routes
- Extend: `services/api/internal/authz/` — `channels:read`, `channels:manage`
- Extend: `services/ingestion/cmd/main.go` — dispatch hook after `SaveSnapshotServices` (~line 695)
- New: `services/dashboard/src/screens/IntegrationsScreen.jsx`
- Extend: `services/dashboard/src/screens/AccountSettingsScreen.jsx` — Integrations tab link

## Dispatch seam

`services/ingestion/cmd/main.go` `runIngestionCore` ends at `SaveResources` (line ~707). Right before that, `SaveSnapshot` (~660) and `SaveSnapshotServices` (~690) persist the per-scan deltas — this is the natural "scan finished, here's what changed" cut. The snapshot row + `summary.ByService` map are already in scope.

```go
// After SaveSnapshotServices:
notifications.DispatchForScan(ctx, store, snap, summary, accountID)
```

`DispatchForScan` is **synchronous, non-fatal**:
- Loads enabled channels for the org via a new `Store.ListEnabledChannels(ctx)` method.
- Per channel: evaluates `trigger_rule` against `summary` (e.g. `summary.PotentialMonthlySave >= min_monthly_savings_usd`); if it fires, decrypts `config_ciphertext` and calls `Transport.Send(ctx, payload)`.
- Writes one `notification_dispatches` row per attempt (`status=sent|failed|skipped_threshold`).
- Errors are logged + recorded on the dispatch row, never abort the scan. Same posture as the existing `SaveSnapshot` error path.

**Why synchronous in v1.** SMTP send is ~200 ms; scans already take minutes; synchronous keeps the dispatch row's lifecycle inside the same `ctx.Done()` shutdown window. If a transport turns flaky and starts blocking scans, v2 = push a `NotificationJob` envelope into the existing `queue.Queue` (the Redis path already handles HMAC-signed envelopes for free — see `docs/c1-hmac-plan.md`).

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

## API surface

Mirror `services/api/internal/api/accounts.go`:

| Method | Path                            | Permission         |
|--------|----------------------------------|--------------------|
| GET    | `/v1/channels`                   | `channels:read`    |
| POST   | `/v1/channels`                   | `channels:manage`  |
| PATCH  | `/v1/channels/{id}`              | `channels:manage`  |
| DELETE | `/v1/channels/{id}`              | `channels:manage`  |
| POST   | `/v1/channels/{id}/test`         | `channels:manage`  |
| GET    | `/v1/channels/{id}/dispatches`   | `channels:read`    |

- Encrypt-on-write / decrypt-on-read for `config_ciphertext` follows the `accounts.secret_key` pattern.
- **PATCH redacts secrets on read** (return `"smtp_pass":"***"`); only re-encrypts if the field comes back non-empty/non-mask in the request — same UX as `accounts.secret_key`.
- `POST /test` synthesizes a fake `Payload` with realistic content and runs `Transport.Send` end-to-end. Writes a dispatch row with `status='sent'|'failed'`. Required before flipping `enabled=true` on first save (UX gate).
- Audit_log every CRUD via `axiaops.io/api/internal/audit` with new `AuditActionChannelCreated/Updated/Deleted/Tested` constants.

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

- **GDPR cascade — easy to miss.** Recipient emails live in `config_ciphertext`. The cascade-delete chain (`organizations → notification_channels → notification_dispatches`) makes erasure automatic, but the org-erasure step list in `services/api/internal/api/organizations.go` (right-to-erasure) must enumerate the two new tables. Reviewer should grep for `DeleteOrganizationCascade` and confirm.
- **First-scan storm.** A fresh org's first scan surfaces dozens of zombies. Default `trigger_rule` = `{"min_monthly_savings_usd": 100}` to bound noise; admin can widen later. Avoid `enabled=false` default — that's a sharper UX cliff.
- **Slack webhook URLs are bearer tokens.** Encrypt the whole URL, never log it, redact in API responses (`"webhook_url":"***"`). Same posture as SMTP creds.
- **SMTP credential rotation UX.** PATCH with empty `smtp_pass` = "keep existing" (same convention as `accounts.secret_key`). Document on the form.
- **Per-user opt-out / preferences.** Out of scope. Channels are org-level. v2 might add `trigger_rule.owner_team = "platform"` as a filter.
- **Retry / DLQ.** v1 = single attempt; `attempts` column is reserved for v2. Failed dispatch is visible in the UI; admin re-sends via `/test`.
- **Rate limits.** SES sandbox = 200/day; one email per channel per scan stays well below. Add a per-org daily counter only if customers report bumping into it.
- **Email digest vs per-zombie.** v1 = one digest per scan summarizing top N zombies. Per-zombie alerts are a v2 `trigger_rule.on = ["new_zombie"]` mode joining against the previous snapshot.

## References

- Architect's analysis that produced this plan: in-conversation, 2026-05-30 alpha.22 release session.
- Issue #113 — Jira channel (this plan is its foundation).
- Issue #114 — Microsoft Teams channel (this plan is its foundation).
- Crypto pattern: `services/shared/crypto/`.
- Audit pattern: `services/api/internal/audit/` + `services/shared/model/audit.go`.
- Dispatch precedent (synchronous, scan-anchored): `services/ingestion/cmd/main.go` `runIngestionCore`.
