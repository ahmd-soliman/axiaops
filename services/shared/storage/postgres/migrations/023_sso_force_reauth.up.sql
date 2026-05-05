-- 023_sso_force_reauth.up.sql
-- Per-connection override for the OIDC `prompt=login` parameter shipped in
-- commit 9f30ad8.
--
-- The unconditional `prompt=login` closes a silent-identity-substitution bug
-- (logged-out user types different email → IdP cookie silently re-auths
-- previous user). For most IdPs this is the right default. But:
--   - Azure AD with a conditional-access policy that locks session re-auth
--     returns `interaction_required` when `prompt=login` is sent.
--   - Deployments that rely on IdP-side "remember me" or step-up MFA flows
--     get an extra password entry per ceremony.
--
-- The fix is per-connection rather than global: customer admins flip
-- force_reauth=false on connections whose IdP enforces its own session
-- policy, leave it true (default) on everything else. See Tasks.md 2.7.17.
--
-- Default true preserves the current security posture for all existing
-- rows on upgrade.

SET search_path TO axiaops;

ALTER TABLE sso_connections
    ADD COLUMN force_reauth BOOLEAN NOT NULL DEFAULT TRUE;

COMMENT ON COLUMN sso_connections.force_reauth IS
    'When true (default), the OIDC authorize URL carries prompt=login to force '
    'IdP-side re-authentication regardless of existing IdP session. Set false '
    'for IdPs that enforce their own session policy (Azure AD conditional '
    'access, custom step-up MFA flows). See services/api/internal/sso/initiate.go.';
