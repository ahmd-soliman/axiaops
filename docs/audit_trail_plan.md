# Audit Trail — Implementation Plan

> Source: roadmap item **3.3 Remediation Actions** (migration `009_add_audit_log.sql`)
> and `docs/development_plan.md` **3.3 Remediation Actions** + **3.10 GDPR / Data Deletion**
> (user deletion must anonymise audit entries, not hard-delete).
>
> Business driver: a remediation workflow with audit trail is a primary differentiator
> against competitors that have no workflow/audit story. No paying customer ships without it.

---

## 1. Scope

What must be in the audit log:

1. **Dismiss / snooze** a zombie resource (`POST /v1/dismissals`)
2. **Revoke** a dismissal or snooze (`DELETE /v1/dismissals/{id}`)
3. **View remediation command** for a zombie (`GET /v1/zombies/{id}/remediation` — new in 3.3)
4. **Scan triggered by a user** (`POST /v1/accounts/{id}/scan`) — proves who kicked off a run
5. **Account connected / updated / deleted** (`POST|PATCH|DELETE /v1/accounts[/id]`) — credentials changes are security-sensitive

Out of scope for this iteration:

- Scheduled/automated scans (no user actor — logged to `scan_runs`, not `audit_log`)
- Read-only list/get endpoints (volume is too high; no value unless an access log is a separate feature)
- Auth events (login/logout — session events are in `sessions` table; Kinde was removed 2026-05)

---

## 2. Current State (what already exists)

- `dismissed_zombies` table — has `dismissed_by` / `revoked_by` columns (`services/shared/storage/postgres/migrations/002_dismiss_snooze.up.sql`). These are populated with **organization_id, not user_id** — see `services/api/internal/api/handler.go:616` (`// swap for user email when available`). The audit trail work fixes this gap.
- `middleware.OrganizationID(ctx)` is the only identity exposed on request context (`services/api/internal/middleware/auth.go:176`). **User identity is not propagated into handlers** today — blocker #1 for the audit trail.
- `users` table exists (`id`, `external_id` (renamed from `kinde_sub` in migration 024), email, `last_seen`) — populated at login; joined to `organizations` via `memberships`.
- Next available migration number is **`013`** — migrations 009–012 are already used.

---

## 3. Data Model

### 3.1 Migration `014_audit_log.up.sql`

> Migration `013` was consumed by the audit-trail prerequisite branch for the
> `kinde_sub` CHECK constraint. The audit_log migration is `014`.

```sql
SET search_path TO axiaops;

CREATE TABLE IF NOT EXISTS audit_log (
    id            BIGSERIAL   PRIMARY KEY,
    organization_id     TEXT        NOT NULL REFERENCES organizations(id),
    user_id       TEXT,                              -- FK → users.id, NULL after GDPR anonymisation
    actor_email   TEXT        NOT NULL DEFAULT '',   -- captured at event time (stable after user delete)
    action        TEXT        NOT NULL,              -- enum: see §3.2
    resource_type TEXT        NOT NULL DEFAULT '',   -- "zombie" | "dismissal" | "account" | "scan"
    resource_id   TEXT        NOT NULL DEFAULT '',   -- zombie fingerprint, dismissal id, account id, scan id
    reason        TEXT        NOT NULL DEFAULT '',   -- dismiss reason code, or free text
    metadata      JSONB       NOT NULL DEFAULT '{}', -- action-specific payload (see §3.3)
    request_id    TEXT        NOT NULL DEFAULT '',   -- from X-Request-ID middleware, for log correlation
    ip_address    INET,                              -- captured from r.RemoteAddr / X-Forwarded-For
    user_agent    TEXT        NOT NULL DEFAULT '',
    created_at    TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE INDEX audit_log_organization_created_idx ON audit_log (organization_id, created_at DESC);
CREATE INDEX audit_log_resource_idx       ON audit_log (organization_id, resource_type, resource_id);
CREATE INDEX audit_log_user_idx           ON audit_log (organization_id, user_id) WHERE user_id IS NOT NULL;

GRANT SELECT, INSERT ON audit_log TO axiaops;          -- UPDATE only for anonymisation (see §7)
GRANT USAGE, SELECT ON SEQUENCE audit_log_id_seq TO axiaops;

ALTER TABLE audit_log ENABLE ROW LEVEL SECURITY;
CREATE POLICY audit_log_organization_isolation ON audit_log
    USING (organization_id = current_setting('app.organization_id', true))
    WITH CHECK (organization_id = current_setting('app.organization_id', true));
```

Down migration: `DROP TABLE audit_log` (acceptable — pre-launch, no customer data).

**Design choices worth calling out:**

- `actor_email` is stored **denormalised** at write time. When a user is hard-deleted (or we anonymise under GDPR), `user_id` is nulled but the event itself is preserved with whatever email was on file at the time. This is exactly what `docs/development_plan.md` §3.10 prescribes — *"replace user_id with a tombstone marker, not a hard delete — preserves audit trail integrity"*.
- `INSERT`-mostly table. We add `UPDATE` grant only for the anonymisation path (see §7). No `DELETE` grant — purging is an `axiaops_owner` operation via GDPR endpoint.
- `metadata JSONB` keeps the schema stable as new action types are added in Phase 3/4 (tagging changes, rule overrides, role changes).
- `created_at DESC` composite index is what all UI queries will hit (organization timeline + per-resource history).

### 3.2 Action enum

Defined as Go constants (`services/shared/model/audit.go`), not a DB enum — keeps migrations cheap when we add new actions.

**Rule:** `audit_log` is mutation-only. Reads (list endpoints, detail views, command lookups) never go here — CloudTrail records real AWS actions, and product telemetry is a better home for UX analytics. Adding a read action to the enum is a red flag.

| Constant | Value | When logged |
|----------|-------|-------------|
| `AuditActionDismissZombie` | `dismiss_zombie` | `POST /v1/dismissals` with `action=dismiss` |
| `AuditActionSnoozeZombie` | `snooze_zombie` | `POST /v1/dismissals` with `action=snooze` |
| `AuditActionRevokeDismissal` | `revoke_dismissal` | `DELETE /v1/dismissals/{id}` |
| `AuditActionScanTriggered` | `scan_triggered` | `POST /v1/accounts/{id}/scan` (user-initiated only) |
| `AuditActionAccountConnected` | `account_connected` | `POST /v1/accounts` |
| `AuditActionAccountUpdated` | `account_updated` | `PATCH /v1/accounts/{id}` |
| `AuditActionAccountDeleted` | `account_deleted` | `DELETE /v1/accounts/{id}` |

### 3.3 `metadata` payload per action

Keep it small and grep-friendly; redact secrets.

```json
// dismiss_zombie / snooze_zombie
{"provider":"aws","service":"AmazonEC2","region":"eu-central-1","monthly_cost":48.00,
 "note":"moving to spot next sprint","snoozed_until":"2026-07-01T00:00:00Z"}

// revoke_dismissal
// metadata is empty — resource_id points at the dismissed_zombies row, which
// carries action/reason/snoozed_until. Denormalising those fields here would
// require an extra Get round-trip for no additional query power (a join
// recovers them). If timeline-grep ergonomics demand it later, add then.
{}

// scan_triggered
{"account_label":"prod-eu","region":"eu-central-1","on_demand":true}

// account_updated
{"fields_changed":["label","region"],"old":{"label":"prod"},"new":{"label":"prod-eu"}}
// NEVER include secret_key / access_key_id in old/new.
```

---

## 4. User Identity Propagation (prerequisite work — shipped)

Handlers today receive organization only. Audit rows need `user_id` + `actor_email`. Done once, used everywhere.

**Status:** implemented as a sibling commit on `feature/audit-trail`. Summary of what landed:

- `middleware.UserID(ctx)` / `middleware.UserEmail(ctx)` helpers alongside `OrganizationID()` (`services/api/internal/middleware/auth.go`).
- `Auth.Wrap` stashes `user.ID` / `user.Email` on the context after `UpsertUser` succeeds.
- `DevBypass(organizationID, userID, userEmail, next)` — 3-arg signature. Local handlers see the same context shape as production.
- **Dev-mode bootstrap (Option 1):** new `Store.EnsureUser(ctx, model.User)` method (struct form — prevents argument-order bugs); `services/api/cmd/main.go` calls it after `EnsureOrganization` so a real users row exists. New env vars `DEV_USER_ID` (default `dev-user-axiaops`) and `DEV_USER_EMAIL` (default `dev@axiaops.local`).
- Synthetic `kinde_sub = "dev:" + id` keeps the `users.kinde_sub UNIQUE` constraint intact alongside real Kinde rows.
- `scripts/seed_test_data.sh` inserts the same dev user row so `make seed` stays idempotent with the startup path.
- `.env.example` documents the two new knobs.
- `handler.go` dismiss / revoke paths now call a `dismissActor(ctx)` helper that prefers email → user id → organization id (was: organization id only — the `// swap for user email when available` TODO is gone).

Remaining test work: add an `auth_test.go` case asserting `UserID` / `UserEmail` round-trip through both `Auth.Wrap` and `DevBypass` — ships with the main audit-trail PR.

---

## 5. Store Contract

Add to `services/shared/storage/storage.go`:

```go
// AuditLogWrite records an audit event. One call per user-facing mutation.
// Returns the new row ID. Errors are logged and swallowed by callers — an audit
// write failure must NOT fail the underlying business operation (§6).
AuditLogWrite(ctx context.Context, e model.AuditEvent) (int64, error)

// AuditLogList returns events for the organization in created_at DESC order.
// Filter is optional; zero values mean "no filter".
AuditLogList(ctx context.Context, f model.AuditFilter) ([]model.AuditEvent, error)

// AuditLogAnonymiseUser sets user_id = NULL and actor_email = 'deleted-user'
// for all rows matching (organization_id, user_id). Called from DELETE /v1/users/{id}
// (Phase 3.9) and organization deletion (3.10).
AuditLogAnonymiseUser(ctx context.Context, userID string) (int64, error)
```

`model.AuditFilter`: `{UserID, ResourceType, ResourceID, Action, Since, Until, Limit, Cursor}`.
Cursor is a `(created_at, id)` pair — keeps pagination stable under concurrent inserts.

Postgres implementation in `services/shared/storage/postgres/postgres.go` — follows the existing transaction / `SET app.organization_id` pattern. `AuditLogWrite` uses a plain `INSERT`; `AuditLogList` uses the `audit_log_organization_created_idx` index with `WHERE` clauses conditional on filter fields.

---

## 6. Recording Events — Handler Wiring

Rules of the road:

- **Audit write failures never fail the user request.** Log at `slog.Error`, bump a Prometheus counter, return success to the caller. Losing an audit row for one dismissal is a smaller harm than telling the user their dismissal failed when it didn't.
- Records are written **inside the same organization-scoped context** as the main operation (`storage.WithOrganizationID` already set).
- Write happens **after the main operation succeeds** — we don't audit phantom actions.
- The record is assembled by a small helper `audit.Record(ctx, store, e)` in `services/shared/audit/` that pulls `user_id` / `actor_email` / `request_id` / `ip` / `user_agent` off the context automatically. Callers only provide the interesting fields.

**Example — dismiss handler (`handler.go:619`):**

```go
id, err := h.store.DismissZombie(ctx, d)
if err != nil {
    // ... existing error handling ...
}

action := model.AuditActionDismissZombie
if d.Action == model.DismissActionSnooze {
    action = model.AuditActionSnoozeZombie
}
audit.Record(ctx, h.store, model.AuditEvent{
    Action:       action,
    ResourceType: "dismissal",
    ResourceID:   fmt.Sprint(id),
    Reason:       d.Reason,
    Metadata: map[string]any{
        "provider":      d.Provider,
        "service":       d.Service,
        "region":        d.Region,
        "resource_id":   d.ResourceID,
        "note":          d.Note,
        "snoozed_until": d.SnoozedUntil,
    },
})
```

One call site added per action in §3.2 — all in `services/api/internal/api/`.

### Observability

New Prometheus counter in `services/shared/observability/`:

```
axiaops_audit_writes_total{action, status}    // status = "ok" | "failed"
```

If `failed` counter climbs, ops pages — audit gaps are a compliance risk.

---

## 7. User Deletion / GDPR Interaction

Per `docs/development_plan.md` §3.10:

- `DELETE /v1/users/{id}` (Phase 3.9) calls `AuditLogAnonymiseUser(userID)` — `UPDATE audit_log SET user_id = NULL, actor_email = 'deleted-user' WHERE user_id = $1`. Rows preserved, actor gone.
- `DELETE /v1/organizations/me` (Phase 3.10) cascades a full delete of `audit_log` rows for that organization — *right to erasure* trumps audit retention once the organization itself is gone. Add `audit_log` to the cascade list in the GDPR task.

Both paths require the `UPDATE` grant on `audit_log` for the app user (granted in §3.1 migration).

**Runtime-admin role (migration 029, `docs/runtime-admin-db-role.md`):** the org-cascade purge (`DELETE /v1/organizations/me`) runs on the runtime RLS-bypass pool (`axiaops_runtime`), which therefore holds `DELETE` on `audit_log`. NB: contrary to §3.1's stated "no `DELETE` for the app role," the app role (`axiaops`) *also* holds `DELETE` here in practice — `000_init`'s `ALTER DEFAULT PRIVILEGES … GRANT … DELETE … TO axiaops` fires when `audit_log` is created and nothing revokes it. So `axiaops_runtime`'s `DELETE` is no new exposure; tightening the app role's grant to match §3.1's intent is a separate, out-of-scope cleanup. `axiaops_runtime` still cannot DDL or own objects.

---

## 8. API — Surfacing the Trail

New endpoint (fits 3.3 / Team-tier feature):

| Method | Path | Purpose |
|--------|------|---------|
| GET | `/v1/audit` | Organization audit timeline. Supports `?user_id`, `?resource_type`, `?resource_id`, `?action`, `?since`, `?until`, `?limit` (default 50, max 500), `?cursor`. Admin role only once 3.9 ships; organization-wide in the meantime. |

Follows existing handler pattern (`handler.Register`, `writeJSON`, `OrganizationID`/`UserID` from context).

Response shape mirrors `model.AuditEvent` with the JSON tags already on the struct. Cursor-based pagination returns `{"events":[...],"next_cursor":"..."}`.

No dashboard UI in this iteration — the data is exposed for support / compliance queries and dashboard work can follow once Phase 3.9 user management is in (roles determine who sees the audit page).

---

## 9. Tests

### 9.1 Unit / handler tests (`services/api/internal/api/`)

- `handler_dismiss_test.go` — extend to assert an audit row is recorded with the right `action`, `user_id`, `reason`, `metadata`. Use `MockStore.AuditLogWrite` spy (add to `test_helpers_test.go`).
- `handler_revoke_test.go` — same for `revoke_dismissal`.
- `handler_remediation_test.go` — new file alongside the 3.3 endpoint.
- `handler_scan_test.go` — assert on-demand scan records `scan_triggered`; assert scheduled scan does **not**.
- `handler_audit_test.go` — `GET /v1/audit` happy path + filter params + pagination cursor.
- **Negative path:** make `MockStore.AuditLogWrite` return an error; assert the user operation still returns 2xx and an error counter is observed.

### 9.2 Postgres integration tests (`services/shared/storage/postgres/postgres_test.go`)

- `TestAuditLog_WriteAndList` — insert N events across two organizations, assert RLS returns only the current organization's rows, assert ordering.
- `TestAuditLog_Filters` — exercise each filter field.
- `TestAuditLog_Pagination` — insert 120 rows, page through with `Limit=50`, assert stable ordering across cursors.
- `TestAuditLog_AnonymiseUser` — write rows for user A and B, anonymise A, assert A's `user_id IS NULL` and `actor_email='deleted-user'`, B untouched.

### 9.3 Middleware tests

- `auth_test.go` — assert `UserID(ctx)` / `UserEmail(ctx)` are populated after `UpsertUser`; assert `DevBypass` populates them with dev defaults.

---

## 10. Rollout

Pre-launch, there are no production rows yet — no backfill needed.

**PR 1 — `feature/audit-trail` (shipped):** user-identity prerequisites. `EnsureUser`, `UserID` / `UserEmail` context helpers, `DevBypass` expansion, `dismissActor`, migration `013` (CHECK constraint on `users.kinde_sub`), dev-user seeding. No audit table yet — just the groundwork handlers need to know *who* acted.

**PR 2 — `feature/audit-log` (this branch):** audit table + handler wiring + endpoint, all in one. Migration `014_audit_log` (schema + RLS), `Store.AuditLogWrite/List/AnonymiseUser`, `services/api/internal/audit` helper, call-site wiring in dismiss/snooze/revoke + account CRUD + on-demand scan, `GET /v1/audit` with cursor pagination, `axiaops_audit_writes_total{action,status}` Prometheus counter, Postgres + handler + endpoint tests. Best-effort semantics: audit-write failure never breaks a user operation.

**PR 3 — Dashboard Audit History screen:** after Phase 3.9 user roles land (only admins should see the audit timeline).

> **Dropped:** an earlier revision of this plan included a `view_remediation` event fired from a `GET /v1/zombies/{id}/remediation` endpoint. Removed — `audit_log` is mutation-only (see §3.2). CloudTrail records the real AWS actions that follow a remediation view; duplicating a weak proxy here added volume without signal.

---

## 11. Risks & Decisions

| Risk / decision | Resolution |
|---|---|
| Audit write failure blocks user operation | **Log and swallow** — user action is the source of truth; audit is best-effort. Alert via `axiaops_audit_writes_total{status="failed"}` counter. |
| PII in `metadata` | No secrets (access keys, JWTs) ever go in. `actor_email` is the only intentional PII — covered by GDPR §3.10. |
| High-volume action types flooding the table | Only user-initiated mutating actions are logged (no reads, no schedules). At 100 dismiss/scan events per organization/month, growth is negligible. Retention policy can be added later if needed (`docs/production.md`). |
| `user_id` FK could break on user delete | No FK declared — `user_id` is a plain `TEXT` column. `AuditLogAnonymiseUser` is the contract, not referential integrity. |
| Replaying the same action twice | No idempotency key in this iteration. Duplicate audit rows are acceptable (timeline view, not billing). Reassess if we ever retry audit writes. |
| Cross-service audit (ingestion writes) | Not needed yet — ingestion has no user actor. `scan_runs` (Phase 3.5) covers automated scan traceability; `audit_log` stays user-initiated. |

---

## 12. Estimate

With AI assistance (single developer, ~6h/day):

| Chunk | Effort | Status |
|---|---|---|
| User identity middleware + DevBypass update + tests | 0.5 day | Shipped in `feature/audit-trail` |
| Migration + model + Store methods + Postgres impl + tests | 0.5 day | Shipped in `feature/audit-log` |
| Audit helper + call-site wiring (dismiss, revoke, scan, account CRUD) + handler tests | 1 day | Shipped in `feature/audit-log` |
| `GET /v1/audit` endpoint + filters + pagination + tests | 0.5 day | Shipped in `feature/audit-log` |
| Docs update (`services/api/CLAUDE.md` endpoint table, `services/shared/CLAUDE.md` package map, README endpoint list) | 0.25 day | Shipped alongside each PR |
| **Total** | **~2.75 days** | |

Fits inside the Phase 3.3 October 2026 milestone without expanding scope. Remediation-view audit event ships with 3.3 itself.
