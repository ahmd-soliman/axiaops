# Multi-owner support — removing the single-owner SPOF

**Status:** design (not yet implemented).
**Drives:** the lockout risk in the RBAC model — an org with exactly one owner has no
in-product recovery if that owner is lost (left the company, lost their password, got
deleted).
**Related:** `docs/rbac-design.md` (the four-role hierarchy), `docs/invitation-flow.md`
(how members get added), `docs/admin-portal-plan.md` (the deferred admin-plane
break-glass that is *not* a substitute for this).

## Problem

AxiaOps enforces **exactly one owner per organization**. That single owner is a
bus-factor single point of failure, because owner is the only tier that can configure
SSO, see licensing/billing, transfer ownership, or delete the org — and the *only* way
to mint a new owner today is the current owner handing off and stepping down.

The constraint is enforced at three layers:

1. **DB — a partial unique index.** Migration
   `services/shared/storage/postgres/migrations/015_memberships.up.sql:27-28` creates
   `memberships_one_owner_per_tenant ON memberships (tenant_id) WHERE role = 'owner'`,
   renamed to `memberships_one_owner_per_organization` in
   `016_rename_tenant_to_organization.up.sql:104`. The index makes a second `owner` row
   for the same org a `23505` unique violation at the storage layer.

2. **Storage — transfer is a demote-then-promote dance.** `TransferOwnership` in
   `services/shared/storage/postgres/postgres.go:1948-1992` is the only path that
   produces an owner after install. It demotes the incumbent first specifically to make
   room under the index — see the comment at
   `services/shared/storage/postgres/postgres.go:1975` ("Demote current owner first to
   free the partial unique index.") before promoting the target. Net owner count is
   invariant at 1.

3. **API — promote-to-owner is hard-blocked.** Both membership write paths reject
   `{role:"owner"}` before any permission check:
   - `services/api/internal/api/memberships.go:99-102` (`POST /v1/memberships`):
     `"owner role can only be assigned via transfer-ownership"`.
   - `services/api/internal/api/memberships.go:177-180` (`PATCH /v1/memberships/{id}/role`):
     same message.

   The dashboard mirrors this: `ASSIGNABLE_ROLES = ['admin', 'member', 'viewer']` at
   `services/dashboard/src/pages/settings/Members.jsx:26` — owner is never an option in
   any role dropdown.

The **last-owner guard** `storage.ErrLastOwner`
(`services/shared/storage/storage.go:27-29`) is the floor that keeps an org from
reaching *zero* owners — `UpdateMembershipRole`
(`services/shared/storage/postgres/postgres.go:1882-1893`) and `DeleteMembership`
(`postgres.go:1930-1940`) both count owners and refuse to demote/remove when the count
is `<= 1`. The GDPR self-delete enforces the same invariant — `DeleteUser`
(`postgres.go:2188-2206`) returns `ErrLastOwner` when the caller is the sole owner of
any org.

### Why the SPOF is severe

Owner is not a cosmetic tier. `RoleOwner` in
`services/shared/authz/roles.go:99-107` is the *only* role granting:

- `sso:manage` / `sso:domain_verify` (`PermSSOManage` / `PermSSODomainVerify`) — SSO is
  owner-only on purpose because misconfiguration can lock the org out of its own data
  (see the comment at `roles.go:51-54`).
- `organization:transfer` (`PermOrganizationTransfer`) — recovery itself.
- `organization:delete` (`PermOrganizationDelete`).
- `members:manage_admin` (`PermMembersManageAdmin`) — inviting/promoting at admin tier.
- `data:export` (`PermDataExport`).

License/billing visibility is owner-gated in the dashboard too (the `LicenseBanner` is
owner-only).

So when the sole owner is lost there is **no in-product recovery**: transfer is
owner-only, and no one else can configure SSO or mint a new owner. The only escape
hatches are out-of-band:

- **Infra break-glass** — a manual `UPDATE memberships SET role='owner' …` against RDS
  (the `aws-prod-sql` runbook, requires a privileged operator + SSO token), or
- the **admin-plane break-glass**, which is explicitly **deferred** (see
  `docs/admin-portal-plan.md` / `docs/saas-platform-admin-design.md` §5–7 — "break-glass
  cross-tenant data reads + impersonation … Deferred").

Both are operator-only and unavailable to a self-hosted customer running without
AxiaOps staff. The product should let an org protect itself.

## Goals and non-goals

**Goals**

- Kill the bus-factor SPOF: an org can have **N owners** (co-owners), so losing one
  leaves the others fully capable — including the ability to mint a replacement.
- Make promote-to-owner a normal, owner-authorised action rather than the side effect of
  a hand-off.
- Keep the no-zero-owners invariant — an org can never be left ownerless.
- Nudge orgs that are still single-owner toward adding a second owner.

**Non-goals**

- **Do not weaken least-privilege.** Owner powers stay owner-tier. We are *not* pushing
  `sso:manage`, `organization:delete`, etc. down to admin. The fix is "allow more than
  one owner," not "make admin more powerful." (Rejected alternative — see below.)
- No new role tier in v1 (a dedicated "Security Admin" is considered and deferred —
  below).
- No change to the SSO break-glass story beyond noting the interaction: an owner must
  always retain a non-SSO login path (below).
- No change to transfer-ownership *semantics* — it stays as the "hand off and step
  down" convenience; it simply stops being the *only* way to create an owner.

## Proposed design — allow multiple owners (co-owners)

### 1. Drop the single-owner unique index (migration)

Replace the partial unique index with a migration that removes it. Latest migration is
`032_staff_identity` (`services/shared/storage/postgres/migrations/`), so the next free
number is **`033`**.

`033_allow_multiple_owners.up.sql`:

```sql
SET search_path TO axiaops;

-- Remove the at-most-one-owner-per-org constraint. Co-owners are now allowed;
-- the no-ZERO-owners invariant moves entirely to the application layer
-- (ErrLastOwner in UpdateMembershipRole / DeleteMembership / DeleteUser).
DROP INDEX IF EXISTS memberships_one_owner_per_organization;
```

`033_allow_multiple_owners.down.sql` recreates it — but a down must fail loudly if the
org has since grown a second owner, because re-imposing uniqueness on data that now
violates it would error mid-transaction with a confusing `23505`. Make the failure
explicit:

```sql
SET search_path TO axiaops;

-- Refuse to roll back if any org now has more than one owner — the unique index
-- could not be rebuilt over multi-owner data, and a silent failure here is worse
-- than a loud one.
DO $$ BEGIN
    IF EXISTS (
        SELECT 1 FROM memberships WHERE role = 'owner'
        GROUP BY organization_id HAVING COUNT(*) > 1
    ) THEN
        RAISE EXCEPTION
          'migration 033 down: org(s) have multiple owners — demote co-owners before rolling back';
    END IF;
END $$;

CREATE UNIQUE INDEX IF NOT EXISTS memberships_one_owner_per_organization
    ON memberships (organization_id) WHERE role = 'owner';
```

`DROP INDEX IF EXISTS` / `CREATE UNIQUE INDEX IF NOT EXISTS` are both idempotent. Note
the index referenced is the **renamed** name `memberships_one_owner_per_organization`
(post-016), not the original `…_per_tenant`.

### 2. Re-enable promote-to-owner (owner-only)

Remove the two hard blocks and route owner promotion through the existing
stricter-permission tier instead.

- **`PATCH /v1/memberships/{id}/role`** (`memberships.go:176-180`): delete the
  `if req.Role == string(authz.RoleOwner)` rejection. The existing
  `needsAdminPerm := isElevated(current.Role) || isElevated(req.Role)` logic
  (`memberships.go:193`) already maps owner→`PermMembersManageAdmin`, which only owners
  hold (`roles.go:100`). So promoting someone to owner *already* requires the caller to
  be an owner once the block is gone — no new permission needed.

- **`POST /v1/memberships`** (`memberships.go:96-102`): delete the
  `if req.Role == string(authz.RoleOwner)` rejection and add an owner branch to the
  permission gate, mirroring the admin branch at `memberships.go:104-110`:

  ```go
  // Promoting to admin OR owner requires owner-tier (members:manage_admin).
  if req.Role == string(authz.RoleAdmin) || req.Role == string(authz.RoleOwner) {
      callerRole, _ := h.store.RoleOf(ctx, tid, uid)
      if !authz.Allows(authz.Role(callerRole), authz.PermMembersManageAdmin) {
          writeError(w, http.StatusForbidden, "forbidden",
              "assigning admin or owner role requires owner permission")
          return
      }
  }
  ```

`validRole` (`memberships.go:330-336`) already accepts `owner`, so no change there.

Transfer-ownership stays as-is (`memberships.go:289-328`, `postgres.go:1948-1992`) — the
"hand off and step down in one atomic move" convenience. It is no longer the *only* way
to mint an owner, just the only way to mint one *while demoting yourself*.

### 3. Last-owner accounting with N owners

The guard is already count-based, so it generalises cleanly to N owners — the existing
`COUNT(*) … WHERE role='owner'` + `<= 1` check is exactly the right predicate:

- **Demote** (`UpdateMembershipRole`, `postgres.go:1882-1893`): when demoting an owner,
  count owners; if `<= 1`, return `ErrLastOwner`. With N owners the count is `N`, the
  demote succeeds, and the org drops to `N-1` (still ≥ 1). No code change required.
- **Remove** (`DeleteMembership`, `postgres.go:1930-1940`): identical pattern. No change.
- **Self-delete** (`DeleteUser`, `postgres.go:2188-2206`): the "sole owner of any org"
  sub-query already checks for *another* owner in the same org — with co-owners present
  the `NOT EXISTS` is false and the delete proceeds. No change.

One latent bug the index used to mask: with the unique index gone, the
`UpdateMembershipRole` promotion path no longer relies on `23505` to detect a racing
owner (`postgres.go:1898-1902`). That branch becomes dead code — promoting to owner can
no longer collide. **Leave the branch in** (it now never fires but is harmless and keeps
the diff minimal), or remove it; either is acceptable. The implementation MR should
decide and note it in the commit. The last-owner guard's correctness does **not** depend
on that branch.

Concurrency note: the demote/remove guards already run `SELECT … FOR UPDATE` on the
target row inside a transaction (`postgres.go:1873`, `:1921`), then `COUNT` owners. Two
operators concurrently demoting two *different* owners of a 2-owner org is the one race
to reason about — but each transaction locks only its own target row, so both could read
`ownerCount = 2` and both succeed, leaving zero owners. To close this, the owner-count
read in the demote/remove paths should take a stronger lock — either `SELECT … FOR
UPDATE` over the owner rows of the org, or an advisory lock keyed on `organization_id` —
so the second transaction observes the first's demotion. This is a real change the
implementation MR must make (the single-owner index made the race impossible before; N
owners reintroduces it). Spell it out in the storage tests.

### 4. UI nudge for single-owner orgs

In `services/dashboard/src/pages/settings/Members.jsx`, when the membership list
contains exactly one `owner`, render an inline callout in the Members pane:

> **Add a second owner to avoid lockout.** This organization has only one owner. If
> they lose access, no one can configure SSO, transfer ownership, or recover the org.

Add `owner` to the role dropdowns **only for owner callers** (gate on
`canManageAdmin`, the same flag already used to gate the admin option at
`Members.jsx:241` / `:518` / `:577`). For non-owners the dropdowns stay
`['admin','member','viewer']` exactly as today. The "Transfer ownership" control
(`Members.jsx:624`) stays — co-promotion and hand-off are distinct actions and both have
their place.

## Affected files

| File | Change |
|------|--------|
| `services/shared/storage/postgres/migrations/033_allow_multiple_owners.up.sql` | **New.** `DROP INDEX IF EXISTS memberships_one_owner_per_organization`. |
| `services/shared/storage/postgres/migrations/033_allow_multiple_owners.down.sql` | **New.** Guarded re-create (raise if any org has >1 owner). |
| `services/api/internal/api/memberships.go:96-110` | Remove the `POST` owner-reject block (`:99-102`); add owner to the elevated-permission gate. |
| `services/api/internal/api/memberships.go:176-180` | Remove the `PATCH` owner-reject block. |
| `services/shared/storage/postgres/postgres.go:1882-1893` | Demote guard: add the org-level owner-row lock to close the concurrent-demote race. |
| `services/shared/storage/postgres/postgres.go:1930-1940` | Remove guard: same org-level lock. |
| `services/shared/storage/postgres/postgres.go:1895-1904` | Optional: drop the now-unreachable `23505`→`ErrLastOwner` promotion branch. |
| `services/shared/storage/postgres/postgres.go:1948-1992` | `TransferOwnership` — no semantic change; update the `:1975` comment ("free the partial unique index" is no longer why we demote first — it's now just transfer semantics). |
| `services/dashboard/src/pages/settings/Members.jsx:26` | Add `owner` to assignable roles, gated to owner callers in the dropdowns (`:241`, `:518`, `:577`). |
| `services/dashboard/src/pages/settings/Members.jsx` | New single-owner callout; update the "Owner is intentionally omitted" comment at `:23-25`. |
| `services/shared/model/audit.go:54-57` | Reuse `AuditActionMemberRoleChanged` for a promote-to-owner (the metadata already carries `old_role`/`new_role`). No new constant needed; the existing `member_role_changed` covers it. |
| `services/api/internal/api/memberships_test.go` | Flip the two "owner rejected" assertions to "owner promotion allowed for owner caller, 403 for admin caller." |
| `services/shared/storage/postgres/memberships_test.go` | New: two owners coexist; demote one succeeds; demote the last → `ErrLastOwner`; concurrent-demote race leaves ≥1 owner. |
| `services/shared/storage/postgres/memberships_migration_test.go` | Update any assertion that pins the `memberships_one_owner_per_organization` index existing (016 still renames it; 033 drops it). |
| `services/{shared,api}/CLAUDE.md` | Note that >1 owner is now allowed; drop "owner only via transfer-ownership" phrasing from the `POST /memberships` and `PATCH …/role` endpoint rows. |

No change to `services/shared/authz/roles.go` — the design's whole point is that owner
powers stay where they are. No change to the `ErrLastOwner` definition.

## Migration and rollback

- **Up** (`033_…up.sql`): `DROP INDEX IF EXISTS memberships_one_owner_per_organization;`
  — idempotent, instant (dropping an index is metadata-only, no table rewrite, no lock
  contention on a table this small).
- **Down** (`033_…down.sql`): guard-then-recreate as shown in §1. The guard makes the
  down deterministic: it either rebuilds the index cleanly (single-owner orgs only) or
  fails with a clear operator message telling them to demote co-owners first.
- **Existing single-owner orgs:** a pure no-op. They keep their one owner; nothing
  forces a second. The index drop changes what is *allowed*, not what *exists*. The
  last-owner guard continues to protect them from reaching zero. The first promote-to-
  owner an org performs is what actually exercises the new capability.
- **Ordering:** migration 033 ships and runs (via the migrate task — owner connection,
  per `docs/runtime-admin-db-role.md`) **before or with** the api change that removes the
  owner-reject blocks. If the api ships first, promote-to-owner still 500s on the unique
  violation, which is ugly but safe. Run the migration first.

## Security / RBAC implications

- **Least-privilege preserved.** No permission moves tier. `roles.go` is untouched. An
  admin still cannot configure SSO, delete the org, or promote anyone to owner — the
  promote-to-owner gate requires `PermMembersManageAdmin`, which only owners hold.
- **Larger high-privilege surface — acceptable, documented.** More owners means more
  principals holding `organization:delete` + `sso:manage`. This is an intentional
  trade: the SPOF it removes (total lockout) is a worse risk than the marginal increase
  in high-privilege accounts, and the org's owners choose how many co-owners to mint.
  The audit trail (`member_role_changed` with `old_role`/`new_role`) records every
  promotion, so a rogue/over-broad promotion is visible. The single-owner UI nudge is
  *advisory*, not mandatory — orgs that prefer one owner can stay there.
- **SSO break-glass interaction.** SSO is owner-only and a bad SSO config can lock an
  org out of its own login. With co-owners, the recommended posture is that **at least
  one owner retains a non-SSO (native password) login path** so an SSO outage doesn't
  take out every owner at once. This is the same advice as the single-owner world, but
  multi-owner makes it actionable: the org can designate one co-owner as the
  native-login break-glass account. Worth a sentence in the SSO setup UI; out of scope
  for this MR's code but noted so the break-glass story stays coherent.
- **No new authn surface.** Promote-to-owner reuses the existing membership write
  endpoints, permission gate, audit hook, and rate posture. Nothing new is exposed.

## Alternatives considered

**(a) Keep single-owner; document the DB break-glass.** Status quo plus a runbook.
Rejected: it leaves a real lockout with no *product* recovery, only an operator SQL
flip — useless to a self-hosted customer with no AxiaOps staff, and a poor look for a
product whose pitch is operational safety. The admin-plane break-glass that might
eventually cover this is explicitly deferred (`docs/admin-portal-plan.md` §5–7).

**(b) Push owner powers down to admin.** Make admin able to configure SSO / transfer /
delete so the loss of the sole owner is survivable. Rejected — directly violates the
least-privilege goal. Every org would then run with multiple principals holding
org-deletion + SSO-config power *by default*, whether or not they want that. Multi-owner
gives the same survivability **opt-in**, owner-authorised, and auditable.

**(c) A dedicated "Security Admin" role** holding `sso:*` + transfer but not delete.
Rejected for v1: it adds a fifth tier to a deliberately coarse four-role model
(`roles.go:3-7`), needs its own permission-set design, dashboard plumbing, and docs, and
still doesn't solve the general "lost the only person who can recover" problem unless you
also allow N of them — at which point you've reinvented multi-owner with extra steps.
Worth revisiting only if customers ask for finer-grained delegation of *specific* owner
powers; tracked as a future enhancement, not a blocker.

**Why multi-owner wins:** it removes the SPOF with the smallest change (one index drop +
two deleted reject-blocks + a permission-gate branch), preserves least-privilege exactly,
reuses the existing audit/permission machinery, and is opt-in per org.

## Rollout

Phased, each phase independently shippable:

1. **Migration** — ship `033_allow_multiple_owners` and run it. Pure capability-enabler;
   nothing in the app yet creates a second owner, so this is inert and safe. Verify
   single-owner orgs are unaffected.
2. **Backend** — remove the two owner-reject blocks, add the `POST` owner permission
   branch, add the org-level owner-count lock to the demote/remove guards. `make test`
   + `make test-storage` (the new multi-owner + race storage tests) + the flipped api
   handler tests. Promote-to-owner now works end-to-end via the API.
3. **UI nudge** — add `owner` to the dropdowns for owner callers and the single-owner
   callout in `Members.jsx`. Frontend-only; no API change.

After all three: a fresh org still installs with one owner (bootstrap is unchanged), and
that owner can now promote a co-owner from Settings → Members instead of being forced to
hand off and step down.
