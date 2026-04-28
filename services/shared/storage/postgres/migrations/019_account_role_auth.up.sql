-- 019_account_role_auth.up.sql
-- Cross-account IAM role onboarding (Phase 2). See docs/cross-account-roles-design.md §5.
--
-- Adds an auth_method discriminator on accounts so a row can authenticate via
-- either long-lived access keys (existing behaviour) or sts:AssumeRole against
-- a customer-side IAM role with a per-account ExternalId.
--
-- Design notes:
--   * auth_method DEFAULT 'access_key' — every existing row backfills
--     correctly without a data migration script.
--   * access_key_id and secret_encrypted become nullable so role-based rows
--     do not have to carry empty-string sentinels. CHECK constraints below
--     keep "valid" rows from sliding through with the wrong fields populated.
--   * external_id is plaintext on purpose (design §2): it is not a secret in
--     the credential sense. Encrypting it would be cargo-cult and would block
--     the dashboard from showing it back to the customer.
--   * error_message gives the dashboard a single source of truth for the
--     "why is this account failing" UX — applies to both auth methods, not
--     just role-based ones.
--   * accounts_role_only_for_aws keeps Azure/GCP rows out of the role auth
--     method; those clouds use differently shaped contracts (managed
--     identity / workload identity, no AssumeRole, no ExternalId).

SET search_path TO axiaops;

ALTER TABLE accounts
    ADD COLUMN IF NOT EXISTS auth_method   TEXT NOT NULL DEFAULT 'access_key',
    ADD COLUMN IF NOT EXISTS role_arn      TEXT,
    ADD COLUMN IF NOT EXISTS external_id   TEXT,
    ADD COLUMN IF NOT EXISTS error_message TEXT;

ALTER TABLE accounts
    ALTER COLUMN access_key_id    DROP NOT NULL,
    ALTER COLUMN secret_encrypted DROP NOT NULL;

ALTER TABLE accounts
    DROP CONSTRAINT IF EXISTS accounts_auth_method_check;
ALTER TABLE accounts
    ADD CONSTRAINT accounts_auth_method_check
    CHECK (auth_method IN ('access_key', 'role'));

ALTER TABLE accounts
    DROP CONSTRAINT IF EXISTS accounts_role_fields_present;
-- Role-based accounts must always carry an external_id (used in the trust
-- policy condition). The role_arn may be NULL on a freshly-created draft
-- (status='pending_role_setup'), but once the customer pastes a role ARN —
-- even a wrong one that fails verification — role_arn is persisted alongside
-- error_message and the row stays in pending_role_setup until the next
-- successful verify. So both states "role_arn IS NULL" and "role_arn IS NOT NULL"
-- are legal in pending_role_setup; outside that status, role_arn is required.
ALTER TABLE accounts
    ADD CONSTRAINT accounts_role_fields_present
    CHECK (
        auth_method = 'access_key'
        OR (auth_method = 'role'
            AND external_id IS NOT NULL
            AND (role_arn IS NOT NULL OR status = 'pending_role_setup'))
    );

ALTER TABLE accounts
    DROP CONSTRAINT IF EXISTS accounts_access_key_fields_present;
ALTER TABLE accounts
    ADD CONSTRAINT accounts_access_key_fields_present
    CHECK (
        auth_method = 'role'
        OR (auth_method = 'access_key'
            AND access_key_id IS NOT NULL
            AND secret_encrypted IS NOT NULL)
    );

ALTER TABLE accounts
    DROP CONSTRAINT IF EXISTS accounts_role_only_for_aws;
ALTER TABLE accounts
    ADD CONSTRAINT accounts_role_only_for_aws
    CHECK (auth_method = 'access_key' OR provider = 'aws');

CREATE INDEX IF NOT EXISTS idx_accounts_auth_method ON accounts(auth_method);
