-- 037_drop_staff_identity.down.sql — reverse of 037 up.
-- Recreates staff_users and staff_role_grants in their pre-037 shape (032's
-- original definition, unchanged since). Does NOT restore data — only
-- schema. A real rollback that needs the data back must restore from a
-- pre-037 backup.

SET search_path TO axiaops;

CREATE TABLE IF NOT EXISTS staff_users (
    id            TEXT        PRIMARY KEY DEFAULT gen_random_uuid()::text,
    email         TEXT        NOT NULL,
    name          TEXT        NOT NULL DEFAULT '',
    password_hash TEXT,
    status        TEXT        NOT NULL DEFAULT 'active'
                              CHECK (status IN ('active', 'suspended')),
    created_at    TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at    TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    last_seen     TIMESTAMPTZ
);

CREATE UNIQUE INDEX IF NOT EXISTS staff_users_email_lower_unique
    ON staff_users (lower(email));

GRANT SELECT, INSERT, UPDATE, DELETE ON staff_users TO axiaops_runtime;

CREATE TABLE IF NOT EXISTS staff_role_grants (
    id            TEXT        PRIMARY KEY DEFAULT gen_random_uuid()::text,
    staff_user_id TEXT        NOT NULL REFERENCES staff_users(id) ON DELETE CASCADE,
    role          TEXT        NOT NULL
                              CHECK (role IN ('support', 'ops', 'billing', 'superadmin')),
    granted_by    TEXT        REFERENCES staff_users(id) ON DELETE SET NULL,
    created_at    TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    UNIQUE (staff_user_id, role)
);

CREATE INDEX IF NOT EXISTS staff_role_grants_staff_user_idx
    ON staff_role_grants (staff_user_id);

GRANT SELECT, INSERT, UPDATE, DELETE ON staff_role_grants TO axiaops_runtime;
