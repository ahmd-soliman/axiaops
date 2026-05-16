-- 019_account_role_auth.down.sql
-- Rolls back the cross-account IAM role columns.
--
-- Guard: fail loudly if any role-based accounts exist. Silently dropping
-- role_arn / external_id would leave customers connected via roles with no
-- credentials at all, and they would discover the rollback only when their
-- next scheduled scan failed.

SET search_path TO axiaops;

DO $$
DECLARE
    role_count INTEGER;
BEGIN
    SELECT COUNT(*) INTO role_count FROM accounts WHERE auth_method = 'role';
    IF role_count > 0 THEN
        RAISE EXCEPTION 'cannot roll back 019_account_role_auth: % role-based account(s) still exist; migrate them off auth_method=role first', role_count;
    END IF;
END $$;

DROP INDEX IF EXISTS idx_accounts_auth_method;

ALTER TABLE accounts DROP CONSTRAINT IF EXISTS accounts_role_only_for_aws;
ALTER TABLE accounts DROP CONSTRAINT IF EXISTS accounts_access_key_fields_present;
ALTER TABLE accounts DROP CONSTRAINT IF EXISTS accounts_role_fields_present;
ALTER TABLE accounts DROP CONSTRAINT IF EXISTS accounts_auth_method_check;

ALTER TABLE accounts
    ALTER COLUMN access_key_id    SET NOT NULL,
    ALTER COLUMN secret_encrypted SET NOT NULL;

ALTER TABLE accounts
    DROP COLUMN IF EXISTS error_message,
    DROP COLUMN IF EXISTS external_id,
    DROP COLUMN IF EXISTS role_arn,
    DROP COLUMN IF EXISTS auth_method;
