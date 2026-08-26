package auth

// AuthProviderTier maps the per-session AuthMode to the coarse-grained
// label used by both:
//   - axiaops_auth_provider_active / _last_seen_seconds metrics
//     (middleware/auth_native.go)
//   - the auth_provider field on GET /v1/me (api/me.go)
//
// Today's only tier is "native" (cookie sessions, sourced from
// sessions.auth_mode = password|sso|bootstrap). The unknown/empty
// branches stay as observability anchors — a future provider impl
// would add its own case here.
//
// "unknown" is the deliberate fallback for any unrecognised mode — it
// makes the bug observable both in the API response and in metric
// labels, instead of silently bleeding into "native".
func AuthProviderTier(authMode string) string {
	switch authMode {
	case "password", "sso", "bootstrap":
		return "native"
	case "":
		// No provider ran — DEV_MODE bypass or pre-auth path. Empty
		// signals "unknown / not applicable" to the frontend, which
		// treats it as "assume native for login redirects".
		return ""
	default:
		return "unknown"
	}
}
