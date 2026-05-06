-- 024_drop_kinde_residue.up.sql
--
-- Drop columns and constraints that the deleted Kinde auth path relied on,
-- and rename `users.kinde_sub` to the more accurate `external_id` (the column
-- now holds SSO `sub` claims, `native:<uuid>` for native-auth users, and
-- `dev:<uuid>` for DEV_MODE users — calling it `kinde_sub` is misleading).
--
-- Companion to the kinde-removal MR (Slice 2). Slice 1 removed the Go code
-- that wrote to `kinde_invitation_id` / `kinde_user_id` / required `kinde_sub`
-- under that name; this migration brings the schema in line with that reality.
--
-- See `docs/kinde-removal-plan.md` §"Migration 024 — single migration does
-- column drops AND rename" for the full rationale.

-- pending_memberships: any rows that reached this DB without invite_token_hash
-- came from the legacy Kinde-mode invitation flow (CreatePendingInvitation +
-- UpdateInvitationKindeIDs). With kinde gone, those rows can't be redeemed —
-- the redemption path requires a hash to look up. Delete them rather than
-- carry stale rows that would 410 every retry. No production install ran the
-- Kinde-mode invitation flow (no paying customers as of removal date), so
-- this is safe in practice; staging/dev will lose any Kinde-era pending rows
-- that were sitting around, which is the right outcome.
DELETE FROM pending_memberships WHERE invite_token_hash IS NULL;

-- Invariant: every pending_memberships row now has a token hash. Promote
-- the column to NOT NULL so the application layer can drop its `if hash==""`
-- branches.
ALTER TABLE pending_memberships ALTER COLUMN invite_token_hash SET NOT NULL;

-- Drop the Kinde Mgmt API ID columns. They were populated only by
-- UpdateInvitationKindeIDs (deleted in Slice 1). The columns can go.
ALTER TABLE pending_memberships DROP COLUMN kinde_invitation_id;
ALTER TABLE pending_memberships DROP COLUMN kinde_user_id;

-- Drop the dev-mode CHECK constraint that pinned `kinde_sub LIKE 'dev:%'`
-- for dev users. It was a defense-in-depth assertion that DEV_MODE rows
-- always carry the synthetic prefix; with the column being renamed and the
-- "dev:" prefix invariant now expressed in Go (postgres.go EnsureUser),
-- the CHECK adds nothing post-rename.
ALTER TABLE users DROP CONSTRAINT IF EXISTS users_dev_kinde_sub_matches_id;

-- Rename. PG keeps the UNIQUE index aligned across the rename automatically
-- (the index references the column by OID, not by name). Existing values
-- (real SSO subs, "native:<uuid>", "dev:<uuid>") are preserved verbatim —
-- this is a column rename only, no data rewrite.
ALTER TABLE users RENAME COLUMN kinde_sub TO external_id;

-- Update the descriptive comment so future readers don't have to dig
-- through git history to learn what this column actually stores.
COMMENT ON COLUMN users.external_id IS
  'Stable identifier from external IdP (SSO `sub` claim) for SSO-authenticated users. '
  '"native:<uuid>" sentinel for native password users (the UUID matches users.id). '
  '"dev:<uuid>" sentinel for DEV_MODE users. Never empty.';
