-- 024_drop_kinde_residue.down.sql
--
-- Reverse of 024_drop_kinde_residue.up.sql. Restores the kinde_sub column
-- name + the dropped pending_memberships columns nullable. Data for the
-- dropped columns is gone (they were intentionally cleared); existing
-- external_id values flow back into kinde_sub unchanged.
--
-- Useful for rolling back a bad deploy of the kinde-removal MR; not
-- expected to run otherwise. The CHECK constraint and NOT NULL on
-- invite_token_hash are NOT restored — adding them back would require
-- backfilling the deleted rows and recomputing token hashes (impossible),
-- so the down migration produces a slightly looser schema than what 023
-- left behind. That's acceptable for a rollback.

ALTER TABLE users RENAME COLUMN external_id TO kinde_sub;
ALTER TABLE pending_memberships ADD COLUMN kinde_invitation_id TEXT NOT NULL DEFAULT '';
ALTER TABLE pending_memberships ADD COLUMN kinde_user_id       TEXT NOT NULL DEFAULT '';
ALTER TABLE pending_memberships ALTER COLUMN invite_token_hash DROP NOT NULL;
