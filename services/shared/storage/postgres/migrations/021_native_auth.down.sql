-- 021_native_auth.down.sql
-- Reverse 021_native_auth.up.sql.

SET search_path TO axiaops;

-- pending_memberships token additions
DROP INDEX IF EXISTS pending_memberships_token_idx;
ALTER TABLE pending_memberships DROP CONSTRAINT IF EXISTS pending_memberships_native_token_shape;
ALTER TABLE pending_memberships DROP COLUMN IF EXISTS invite_token_hash;

-- bootstrap singleton
DROP TABLE IF EXISTS bootstrap_state;

-- password reset tokens
DROP TABLE IF EXISTS password_resets;

-- session store
DROP TABLE IF EXISTS sessions;

-- users native-auth columns
DROP INDEX IF EXISTS users_email_lower_unique;
ALTER TABLE users DROP COLUMN IF EXISTS email_lower;
ALTER TABLE users DROP COLUMN IF EXISTS password_set_at;
ALTER TABLE users DROP COLUMN IF EXISTS password_hash;
