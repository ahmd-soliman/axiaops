# Onboarding Wizard & Organization Rename

Status: shipped, partially superseded by Phase B1 (native auth).
Owner: API service + dashboard. Touches `services/shared` (storage, model, migrations), `services/api` (handler, middleware, kinde package), and `services/dashboard` (new wizard routes + Settings → Organization).

**Post-B1 reality (2026-04-30 update):** the wizard's "name your org" step was dropped because native-auth bootstrap (`POST /v1/auth/bootstrap`) already collects `organization_name` at install time. The wizard is now 2 steps: invite teammates → connect AWS. Renaming after the fact stays at Settings → Organization. The Kinde-clobber bug described in §3 is no longer reachable because `UpsertOrganization` is unused under `AUTH_PROVIDER=native` — kept here for Kinde-mode history until D2.

This document is the implementation contract for the post-signup setup wizard, the organization-rename surface (with Kinde Management API sync), and the dashboard "What's next" panel that replaces the empty-state landing for fresh orgs. It assumes `docs/invitation-flow.md` (Phase 1, email invitations) ships first — the wizard's invite step calls `POST /v1/invitations` directly.

This is Phase 2 of the team-invitations branch. It stays in **Pattern A** (Kinde-coupled organizations). `docs/onboarding-and-app-owned-orgs.md` (Pattern B) is no longer on the roadmap; revisit only if a concrete customer need surfaces (multi-org per user, branded invitation emails customers complain about, IdP swap-out).

---

## 1. Problem

A freshly-signed-up customer today is dropped onto the dashboard with no guidance. They cannot rename the organization (Kinde's `org_name` claim clobbers any local rename on every login — see §3), they cannot tell what to do first, and they cannot invite teammates by email (Phase 1 fixes that part). The result: every new customer goes through a manual handshake with the AxiaOps team.

End state: a fresh signup goes from "I just authenticated with Kinde" to "my org is named, my first teammate is invited, my first AWS account is connected" in one guided pass. Invited users (members/viewers/admins joining an existing org) skip the wizard entirely and land on the dashboard with a contextual "What's next" panel.

---

## 2. Scope

In scope:
- A 3-step wizard at `/onboarding` for fresh organizations: confirm name → invite teammates (optional) → connect first AWS account (optional).
- Organization rename via `PATCH /v1/organizations/me`, with **two-phase commit to Kinde** so invitation emails and Kinde-hosted UI stay in sync with the local name.
- A derived "What's next" panel on the dashboard home (no new endpoints), dismissible per-user.
- Fix the `UpsertOrganization` name-clobber bug at `services/shared/storage/postgres/postgres.go:250-255` — the load-bearing change that makes any rename UX possible.

Out of scope (deferred, with rationale):
- **Pattern B (app-owned organizations).** Dropped from the roadmap. See header.
- **Org-name reconciliation job (24h diff against Kinde).** In Pattern A only the AxiaOps team has Kinde admin access; with basic discipline the only realistic drift source is closed. Add only if drift ever actually shows up in production.
- **Kinde webhook listener for `organization.updated`.** Same reasoning — public endpoint plus signature verification for an event that won't fire is unjustified.
- **Multi-org switching.** Pattern A is single-org per user.
- **Re-enterable wizard / "Restart onboarding" button.** One-liner to add later.
- **Tour library / coachmarks.** The "What's next" panel is enough.

---

## 3. The name-clobber bug (load-bearing fix)

`services/shared/storage/postgres/postgres.go:250-255` runs

```sql
INSERT INTO organizations (id, org_code, name, created_at)
VALUES ($1, $2, $3, $4)
ON CONFLICT (org_code) DO UPDATE SET name = EXCLUDED.name
```

on every authenticated request via `auth.Wrap` → `UpsertOrganization` (`services/api/internal/middleware/auth.go:166`). The `EXCLUDED.name` always carries Kinde's `org_name` JWT claim, so any locally-stored rename is wiped at the next login.

Replace with a no-op `SET` that preserves the local name while still satisfying the `DO UPDATE` requirement:

```sql
ON CONFLICT (org_code) DO UPDATE SET org_code = EXCLUDED.org_code
-- name is owned by AxiaOps after first insert; renames go through
-- PATCH /v1/organizations/me (which also pushes to Kinde via Mgmt API).
```

Add a Postgres integration test that renames the org via `RenameOrganization` (§5.1), invokes `UpsertOrganization` again with a different name argument, and asserts the local name is preserved. This is the regression guard for the bug.

The dashboard also stops sourcing org name from the JWT (`services/dashboard/src/App.jsx:40`) — it consumes `me.organization.name` from the extended `/v1/me` response (§5.3). Without this, the AppShell sidebar shows the JWT-cached value even after a successful rename.

---

## 4. Data model

### Migration `018_organization_onboarding`

```sql
-- up
SET search_path TO axiaops;
ALTER TABLE organizations
    ADD COLUMN IF NOT EXISTS onboarding_completed_at TIMESTAMPTZ NULL;

-- Existing organizations have already completed setup; don't surprise them
-- with a wizard.
UPDATE organizations
   SET onboarding_completed_at = NOW()
 WHERE onboarding_completed_at IS NULL;
```

```sql
-- down
SET search_path TO axiaops;
ALTER TABLE organizations DROP COLUMN IF EXISTS onboarding_completed_at;
```

RLS: the column rides the existing `organizations` policy. No new policy needed.

The flag is a one-way ratchet — once set, the wizard never re-triggers, even if accounts or teammates are later deleted. The "What's next" panel may re-appear because its state is derived; that's acceptable, it's guidance not a blocker.

Slot 017 is reserved for `pending_memberships` (Phase 1). Slot 018 is this migration.

### Server-side, not localStorage

The flag lives on `organizations`, not in browser storage. Cross-device persistence is the requirement: completing the wizard once on a laptop must not show it again on a phone.

### No per-step state machine

The wizard tracks completion of the **flow**, not each step. Skipped steps are captured in audit metadata for analytics; they are not stored on the organization.

---

## 5. API surface

### 5.1 `PATCH /v1/organizations/me`

| Field | Value |
|---|---|
| Permission | `PermOrganizationUpdate` (new — owner-only; admins do not get it) |
| Body | `{"name": "Acme Corp"}` — required, 1..120 chars, no control chars |
| 200 | full organization including `onboarding_completed_at` |
| 400 | invalid body |
| 403 | caller lacks `organization:update` |
| 502 | Kinde Mgmt API failure (see two-phase commit below) |

**Two-phase commit (mirrors `docs/invitation-flow.md` §5):**

1. `BEGIN` txn → `UPDATE organizations SET name = $1 WHERE id = $org` (RLS-scoped). Do not commit yet.
2. Call `kinde.RenameOrganization(ctx, orgCode, name)`.
   - On Kinde 5xx or transport error: `ROLLBACK`, return 502 `{error: "kinde_rename_failed"}`. Local DB unchanged.
   - On Kinde 4xx (validation): `ROLLBACK`, surface the underlying status (typically 400 with Kinde's message).
   - On Kinde 2xx: `COMMIT`.
3. Audit `AuditActionOrganizationRenamed` after commit. Metadata `{old_name, new_name, kinde_synced: true}`.

**Why local-first (not Kinde-first):** Kinde-first risks the local update failing after Kinde already changed → silent drift in the opposite direction. Local-first with rollback gives atomic semantics and matches the existing invitation-create pattern.

**Why push to Kinde at all:** Kinde sends the invitation emails (Phase 1 uses `kinde.InviteUser`); those emails reference the org name. Without the push, an invitee gets "you've been invited to join *old-name*" after the customer renamed.

### 5.2 `POST /v1/organizations/me/onboarding/complete`

| Field | Value |
|---|---|
| Permission | `PermOrganizationUpdate` |
| Body (optional) | `{"steps_skipped": ["invite", "aws-account"]}` — audit metadata only, not stored |
| 200 | `{"onboarding_completed_at": "2026-04-27T..."}` |
| Idempotent | already complete → returns existing timestamp |

Audit `AuditActionOnboardingCompleted` with `metadata.steps_skipped`.

### 5.3 `GET /v1/me` — extended

Additive `organization` block — does not break existing consumers:

```json
{
  "user_id": "...",
  "organization_id": "...",
  "email": "...",
  "role": "owner",
  "permissions": [...],
  "organization": {
    "id": "...",
    "name": "Acme Corp",
    "onboarding_completed_at": null
  }
}
```

The dashboard consumes `me.organization.name` (replacing the JWT read in `App.jsx:40`) and `me.organization.onboarding_completed_at` (the wizard route guard).

### 5.4 New permission

Add `PermOrganizationUpdate` to `services/api/internal/authz/permissions.go`. Owner-only. Renaming the org is identity-level — admins should not silently change the name visible on invitation emails.

---

## 6. Kinde Management API client additions

Phase 1 introduces the M2M client (`docs/invitation-flow.md` §5). This phase adds one method:

- `kinde.Client.RenameOrganization(ctx, orgCode, name) error` — `PATCH {KINDE_MGMT_API_URL}/api/v1/organization?code={orgCode}` with body `{"name": "..."}`.
- Stub implementation in `kinde.NewStub` is a no-op map update keyed by `orgCode`.
- `client_test.go` adds an `httptest.NewServer` case for happy-path, 4xx, and 5xx.

Required Kinde M2M scopes (full list, including Phase 1's): `read:users`, `create:users`, `update:user_properties`, `delete:users`, `read:organizations`, `update:organizations` (new), `update:organization_users`, `delete:organization_users`.

---

## 7. Auth middleware

No new hook. The redemption hook from `docs/invitation-flow.md` §5 still runs after `EnsureFirstMembership`. The only change is the `UpsertOrganization` fix in §3 — the middleware itself is unchanged in shape.

`UpsertOrganization` continues to insert new orgs from Kinde claims on first login (so the auto-provisioning still works). It just stops overwriting `name` on subsequent logins.

---

## 8. Dashboard

### 8.1 Routing — `services/dashboard/src/App.jsx`

```jsx
<Route element={<AuthGuard />}>
  <Route element={<OnboardingGate />}>
    <Route path="/onboarding" element={<OnboardingLayout />}>
      <Route index element={<Navigate to="org-name" replace />} />
      <Route path="org-name"    element={<OnboardingOrgName />} />
      <Route path="invite"      element={<OnboardingInvite />} />
      <Route path="aws-account" element={<OnboardingAwsAccount />} />
    </Route>
    <Route element={<AppShell />}>{/* existing routes unchanged */}</Route>
  </Route>
</Route>
```

Stop reading `org_name` from the JWT (line 40). Source `orgName` from `useMe().organization.name` inside `AppShell`.

### 8.2 `OnboardingGate`

Reads `useMe()`. While `loading`, render nothing (matches `AuthGuard`'s pattern — avoids a flash of wizard for invited users mid-load).

| Condition | Action |
|---|---|
| `role === 'owner' && !onboarding_completed_at && path !== /onboarding/*` | redirect to `/onboarding` |
| `onboarding_completed_at && path startsWith /onboarding` | redirect to `/` |
| else | render `<Outlet />` |

Invited users (role `member` / `viewer` / `admin`) bypass naturally — they're not owners, so the first condition is false. They land on the dashboard with no wizard.

### 8.3 New files

| Path | Responsibility |
|---|---|
| `components/OnboardingGate.jsx` | Route guard described above. |
| `components/OnboardingLayout.jsx` | Wizard chrome — progress dots, no sidebar, "Skip" CTA on optional steps. |
| `pages/onboarding/OrgName.jsx` | Pre-fills `me.organization.name`. `PATCH /v1/organizations/me` → `/onboarding/invite`. **Not skippable** — name is mandatory; the Kinde default auto-fills, so worst case the user clicks Continue. |
| `pages/onboarding/Invite.jsx` | Multi-row email + role form → `POST /v1/invitations` per row. Skip and Continue both → `/onboarding/aws-account`. Inline 409 / 502 handling per `docs/invitation-flow.md`. |
| `pages/onboarding/AwsAccount.jsx` | Embeds shared `<ConnectAccountForm>`. Skip and Connect both call `POST /v1/organizations/me/onboarding/complete` then redirect to `/`. |
| `components/ConnectAccountForm.jsx` | Extracted from existing `pages/Connect.jsx`. Two consumers: existing `/connect` route and the wizard step. |
| `components/onboarding/WhatsNextPanel.jsx` | Four checklist tiles on dashboard home — Connect AWS, Invite teammates, Run a scan, Review zombies. Status derived from existing `GET /accounts`, `GET /memberships`, `GET /invitations`. Per-user dismissal via localStorage (separate from server-side completion flag). |

### 8.4 Modified files

| Path | Change |
|---|---|
| `pages/settings/Organization.jsx` | Add rename section above the existing delete section (the file already has a TODO comment for this). |
| `pages/Dashboard.jsx` | Render `<WhatsNextPanel />` above existing content. |
| `pages/settings/Team.jsx` | Replace the "add user by email (must already exist)" form with the live invite-by-email form. The 409 `user_exists_use_memberships` case auto-converts to a one-click "Add to org" button using `POST /v1/memberships`. Add a "Pending invitations" section reading `GET /v1/invitations?status=pending` with revoke buttons. |
| `api/client.js` | Add `patchOrganization(name)`, `completeOnboarding(payload)`, `listInvitations(status)`, `createInvitation(email, role, name)`, `revokeInvitation(id)`. |
| `App.jsx` | Add onboarding routes + `<OnboardingGate>`. Stop sourcing `orgName` from JWT. |
| `context/AppContext.jsx` | Stop accepting `orgName` from `App.jsx`; consume from `MeContext`. |

---

## 9. Edge cases

| Case | Handling |
|---|---|
| Invited user redeems on first login | Role is member/viewer/admin. `OnboardingGate`'s `role === 'owner'` check fails → straight to dashboard. No wizard. |
| Owner promoted later via `transfer-ownership` | Org's `onboarding_completed_at` was set by the original owner. Wizard does not re-trigger. |
| Concurrent first-logins | `EnsureFirstMembership` partial unique index guarantees one owner. Wizard guard is read-only on `/me`; both tabs see "wizard pending"; one wins on PATCH. Idempotent. |
| Kinde renames upstream after we renamed locally | Invisible to AxiaOps after §3 ships (we don't read `org_name` from JWT). The Kinde-side edit creates drift only for the Kinde-hosted surfaces; documented as "edit org names in AxiaOps, not in Kinde." Customers don't have Kinde admin in Pattern A, so this is an internal-discipline issue only. |
| Kinde rename fails mid-PATCH | Local txn rolls back. User sees a toast with the Kinde error. Retry is safe (idempotent). |
| `update:organizations` scope missing in M2M app | Kinde returns 403; local rolls back; user sees a clear "Kinde permissions misconfigured" message. The production-readiness checklist guards against this. |
| `complete` endpoint fails | Toast, do not redirect. On refresh, the gate re-enters at the appropriate step. Pressing skip again is safe (idempotent). |
| Owner deletes last account + last teammate after onboarding | `onboarding_completed_at` is one-way; wizard does not come back. `WhatsNextPanel` may re-appear (derived). Acceptable. |
| Dev mode | Migration backfills the seeded dev org with `NOW()`. To exercise the wizard locally, set `onboarding_completed_at = NULL` via psql or a `make seed-fresh` target. |

---

## 10. Sequencing

1. **`docs/invitation-flow.md` (Phase 1) ships first.** The wizard's Step 2 calls `POST /v1/invitations` directly; shipping the wizard before invitations would require a placeholder step, which degrades the UX of the very flow this work is supposed to fix.
2. **This phase ships second**, blocked only on Phase 1's invitation handler being live in dev. The `UpsertOrganization` fix in §3 could technically ship with Phase 1 as a defensive change, but bundling it with this phase keeps the regression test (rename → re-login → rename preserved) co-located with the rename UI that exercises it.
3. Within this phase, the order is: migration 018 → store interface (`RenameOrganization`, `MarkOnboardingComplete`) → `UpsertOrganization` fix + regression test → `kinde.Client.RenameOrganization` + stub + tests → `PATCH /v1/organizations/me` + `POST .../onboarding/complete` handlers + tests → `/me` extension → dashboard wizard + Settings rebuild + WhatsNextPanel.

---

## 11. Effort

| Area | Days |
|---|---|
| Migration 018 + `RenameOrganization` / `MarkOnboardingComplete` store + tests | 1.0 |
| `UpsertOrganization` fix + regression test | 0.25 |
| `kinde.Client.RenameOrganization` + stub + httptest mock | 0.25 |
| `PATCH /v1/organizations/me` (with Kinde rename + 502 rollback) + `POST .../complete` handlers + tests | 1.25 |
| `PermOrganizationUpdate`, `/me` extension, audit constants | 0.75 |
| Dashboard: `OnboardingGate`, `OnboardingLayout`, routing | 1.0 |
| Dashboard: 3 step pages | 1.5 |
| Dashboard: `ConnectAccountForm` refactor | 0.5 |
| Dashboard: `WhatsNextPanel` | 0.5 |
| Dashboard: Settings → Organization rename UI | 0.5 |
| Dashboard: Settings → Team rebuild (live invite + pending list) | 1.0 |
| Dashboard: `App.jsx` org-name source migration | 0.25 |
| Manual QA (fresh signup, dev, invited-user, all skip flows) | 0.5 |
| **Total** | **~9.25 d** |

Phase 1 (invitations) is ~7.75 d per `docs/invitation-flow.md` §11. Combined: **~17 d** single-developer.

---

## 12. Verification

```bash
make test           # unit tests across all modules
make test-storage   # postgres integration (RLS, name-preservation regression for §3)
make test-smoke     # full-stack smoke (run with stack up via `make start-dev` in another terminal)
```

Manual end-to-end (dev mode):

1. Reset the dev org's `onboarding_completed_at` to `NULL` via psql (or `make seed-fresh` if added).
2. `make start-dev`. Visit `localhost:5173`. With `DEV_MODE=true` you're auto-logged-in as owner of a fresh org → wizard appears.
3. Step 1: rename the org. Submit. Verify `SELECT name FROM organizations WHERE id = ...;` shows the new name. Verify the Kinde stub received the rename (in dev mode, `kinde.NewStub`'s in-memory map).
4. Step 2: invite `dev-invitee@axiaops.local` as `member`. With the Kinde stub, no real email is sent — the pending row is inserted. Verify `SELECT * FROM pending_memberships;`.
5. Step 3: skip. Land on dashboard. Verify `WhatsNextPanel` shows "Connect AWS" and "Run scan" tiles unchecked.
6. Refresh. Verify the wizard does not re-appear (`onboarding_completed_at` is set).
7. Manually insert a `users` row for `dev-invitee@axiaops.local`, then send a request authenticated as that user → verify `RedeemPendingInvitation` materializes a `memberships` row and removes the pending row. Verify the invited user lands on the dashboard, not the wizard.
8. Settings → Team: verify pending invitations appear with revoke buttons. Click revoke → verify the Kinde stub `RemoveUser` was called and the local row flipped to `revoked`.
9. Settings → Organization: rename the org again. Log out, log back in. Verify the rename persists (the regression guard for §3).

Production-readiness checklist:

- `KINDE_M2M_CLIENT_ID` / `KINDE_M2M_CLIENT_SECRET` set in staging and production.
- `INVITATION_TTL_DAYS` documented (default 14).
- M2M Kinde app has the full scope set listed in §6, including `update:organizations`.
- `docs/auth_flow.md` and `services/api/CLAUDE.md` updated to reflect that the redemption hook and the `UpsertOrganization` semantics are implemented (currently both document them as planned).
- `docs/user_onboarding.md` updated to describe the wizard.
