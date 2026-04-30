package auth

// AuthProviderTier maps the per-session AuthMode to the strangler tier
// label used by both:
//   - axiaops_auth_provider_active / _last_seen_seconds metrics
//     (middleware/auth_native.go)
//   - the auth_provider field on GET /v1/me (api/me.go)
//
// Plan §4.5 reserves the values {native, kinde, both, unknown} for the
// strangler-tier axis. AuthMode is finer-grained ("password", "sso",
// "bootstrap", "kinde") and only useful for per-session debugging via
// audit_log; client-facing surfaces collapse them via this helper.
//
// "unknown" is the deliberate fallback for any unrecognised mode — it
// makes the bug observable both in the API response and in metric
// labels, instead of silently bleeding into "native".
func AuthProviderTier(authMode string) string {
	switch authMode {
	case "password", "sso", "bootstrap":
		return "native"
	case "kinde":
		return "kinde"
	case "":
		// No provider ran — DEV_MODE bypass or pre-auth path. Empty
		// signals "unknown / not applicable" to the frontend, which
		// treats it as "assume native for login redirects".
		return ""
	default:
		return "unknown"
	}
}
