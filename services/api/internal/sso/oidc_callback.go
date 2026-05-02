package sso

import (
	"net/http"
)

// CallbackHandler serves GET /v1/sso/oidc/{cid}/callback — the OAuth 2.0
// redirect URI. Pre-auth route wired in cmd/main.go.
//
// SCAFFOLD STATUS — slice 4 follow-up commit will land:
//
//   1. Pull state + code from query string. Reject when either is missing.
//   2. Look up persisted state. Single-use: delete after read. Reject when
//      missing/expired (state TTL 10min — see initiate.go).
//   3. Confirm cid in path matches state.cid (CSRF defense — an attacker
//      can't redirect a state from one connection's callback URL to another
//      since the path is in the state binding).
//   4. Fetch discovery doc (cached) to obtain token_endpoint.
//   5. POST to token_endpoint with grant_type=authorization_code, code=<code>,
//      redirect_uri=<original>, code_verifier=<state.verifier>,
//      client_id=conn.OIDCClientID, client_secret=<decrypt
//      conn.OIDCClientSecretCiphertext via crypto.Decrypt with ENCRYPTION_KEY>.
//   6. Token endpoint returns id_token (+ access_token; we ignore it for v1).
//   7. Validator.ValidateIDToken(ctx, conn, idToken, state.nonce). On
//      ErrIDTokenInvalid → audit AuditActionSSOLoginFailed with metadata
//      {connection_id, reason: "<safe-bucket>"}, render a generic auth-failed
//      page. NEVER surface the wrapped error reason to the browser.
//   8. Extract subject (Entra: oid; generic: sub), email (with Entra fallback
//      to preferred_username/upn per §9.4), name, groups.
//   9. Validate email's domain matches a verified sso_domains row for
//      conn.OrganizationID (design §11.1 — never trust email_verified from
//      arbitrary IdPs; trust the domain because the admin proved DNS control).
//  10. UpsertUser(ctx, conn.OrganizationID, ssoSubject, email, name).
//      For SSO connections the kindeSub parameter receives the IdP subject
//      (oid for Entra, sub elsewhere) — this is what users.sso_external_id
//      will eventually persist (column added by migration 022).
//  11. RedeemPendingInvitation precedence (design §10.4): if a pending
//      membership exists and matches, redeem it and skip JIT. Audit
//      AuditActionSSOLoginSucceeded with {redeemed_invitation: true}.
//  12. Else JIT: role := JITResolveRole(mappings, groups, conn.DefaultRole);
//      JITProvisionMembership(ctx, store, conn.OrganizationID, user.ID, role).
//      Audit AuditActionSSOJITProvisioned with {connection_id, role,
//      group_match}.
//  13. Mint session via auth.Manager.MintSession with AuthMode=AuthModeSSO,
//      OrganizationID=conn.OrganizationID, UserID=user.ID. Set the
//      axiaops_session cookie via auth.SetSession.
//  14. Audit AuditActionSSOLoginSucceeded.
//  15. 302 to the post-login redirect (state.redirect_after_login or
//      "/dashboard"). The redirect target is taken from STATE, not from the
//      query string — open-redirect fuzz acceptance criterion (architect N4).
//
// New audit constants needed (slice 4 follow-up will add to model/audit.go):
//   AuditActionSSOLoginFailed     = "sso_login_failed"
//   AuditActionSSOJITProvisioned  = "sso_jit_provisioned"
//   AuditActionSSOJITRoleUpdated  = "sso_jit_role_updated"
func NewCallbackHandler() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// TODO(slice 4 follow-up): replace with the ceremony described above.
		http.Error(w, "sso callback not yet implemented", http.StatusNotImplemented)
	})
}
