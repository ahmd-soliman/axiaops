package sso

import (
	"net/http"
)

// InitiateHandler serves GET /v1/sso/oidc/{cid}/initiate — the SP-initiated
// start of an OIDC login. This is a pre-auth route (the browser arrives
// without a session), so it is wired in cmd/main.go alongside
// /v1/sso/discover, NOT through Handler.Register which gates everything
// behind middleware.Require.
//
// SCAFFOLD STATUS — slice 4 follow-up commit will land:
//
//   1. Lookup the connection by {cid} via Connector.Get (RLS allows pre-auth
//      reads of sso_connections rows in v1; design §5.4 — discovery is a
//      controlled side channel anyway).
//   2. Generate a PKCE code_verifier (43–128 chars, RFC 7636) and derive
//      code_challenge=base64url(sha256(verifier)) with method=S256.
//   3. Generate an opaque state (32 bytes random, base64url) and a nonce
//      (32 bytes random, base64url). Persist {cid, verifier, nonce, expires_at,
//      redirect_after_login} keyed on state — ttl 10min. Storage backend is
//      cache.Cache for v1 (single-node-friendly under Redis); a
//      pending_oidc_states table can replace it if multi-node correctness
//      requires durable storage.
//   4. Fetch discovery doc through Validator.discoveryDoc (cached) to obtain
//      authorization_endpoint.
//   5. Build authorization URL with params:
//        client_id=conn.OIDCClientID
//        response_type=code
//        scope="openid email profile groups"     (Entra also needs "offline_access"
//                                                  if refresh tokens are wanted —
//                                                  v1 doesn't use them)
//        redirect_uri=<PUBLIC_HOST>/v1/sso/oidc/{cid}/callback
//        state=<state>
//        nonce=<nonce>
//        code_challenge=<challenge>
//        code_challenge_method=S256
//   6. 302 to the authorization URL.
//
// Audit: the handler emits no audit row on initiate; the callback owns
// success/failure audit per design §10.1.
func NewInitiateHandler() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// TODO(slice 4 follow-up): replace with the ceremony described above.
		http.Error(w, "sso initiate not yet implemented", http.StatusNotImplemented)
	})
}
