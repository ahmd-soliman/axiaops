-- 027_session_id_token.down.sql
--
-- Reverses 027_session_id_token.up.sql. Dropping the column erases every
-- stored id_token; SSO sessions in flight at rollback time will lose
-- silent-logout support and fall back to either the IdP's confirm-prompt
-- screen (Keycloak default) or an outright 400 (Okta with strict policy).
-- That posture matches pre-027 behaviour and is acceptable for a rollback.
ALTER TABLE sessions
    DROP COLUMN IF EXISTS id_token_encrypted;
