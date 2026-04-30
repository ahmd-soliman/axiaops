// auth_native.go — middleware that authenticates requests via the
// auth.Provider seam (D11/S1). Under AUTH_PROVIDER=native the provider
// is auth.NativeProvider; under `both` (strangler transition) it is a
// composite that tries cookie first, falls back to Bearer JWT; under
// `kinde` the legacy Auth.Wrap path is still used directly. The
// composition root in cmd/main.go picks one.

package middleware

import (
	"context"
	"log/slog"
	"net/http"
	"time"

	"axiaops.io/api/internal/auth"
	"axiaops.io/shared/observability"
)

// WrapNative returns an http.Handler that authenticates each request via
// the supplied auth.Provider, attaches the resolved Identity to the
// request context, and rejects unauthenticated requests with HTTP 401.
//
// Public infra paths (/health, /livez, /readyz, /metrics) and OPTIONS
// preflights bypass authentication — same policy as Auth.Wrap.
//
// Telemetry: every authenticated request increments
// axiaops_auth_provider_active{provider} and updates
// axiaops_auth_provider_last_seen_seconds{provider}. The provider label
// is the AuthMode of the resolved Identity ("password" | "sso" |
// "bootstrap" | "kinde"). Strangler deletion-readiness queries against
// the gauge under low-traffic conditions (architect N1).
func WrapNative(provider auth.Provider, next http.Handler) http.Handler {
	if provider == nil {
		// Surface the misconfiguration loudly. A nil provider means
		// the composition root forgot to wire one — every request
		// would silently 401, which is much harder to diagnose.
		panic("middleware: WrapNative requires a non-nil auth.Provider")
	}
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodOptions || publicPath(r.URL.Path) {
			next.ServeHTTP(w, r)
			return
		}

		identity, err := provider.Authenticate(r)
		if err != nil {
			// 401 with a fixed body — never echo the internal reason
			// (architect §11 / plan §7.1: don't leak whether the
			// cookie was missing, expired, or revoked).
			slog.Warn("auth: request unauthenticated",
				"method", r.Method, "path", r.URL.Path)
			http.Error(w, "unauthenticated", http.StatusUnauthorized)
			return
		}

		// Strangler telemetry — provider label is the env-var tier
		// ("native"|"kinde"|"both") per plan §4.5, NOT the per-session
		// AuthMode. Mapping: password/sso/bootstrap → "native"; kinde
		// → "kinde". The "both" label is emitted by the composite
		// provider when it lands in the next slice.
		tier := providerTier(identity.AuthMode)
		observability.Global.AuthProviderActive.WithLabelValues(tier).Inc()
		observability.Global.AuthProviderLastSeen.WithLabelValues(tier).
			Set(float64(time.Now().Unix()))

		ctx := r.Context()
		ctx = context.WithValue(ctx, organizationIDKey, identity.OrganizationID)
		// organizationCodeKey is the Kinde org_code under the legacy
		// path; under native we use organization_id as the value (same
		// convention DevBypass uses) so handlers that already read
		// OrganizationCode keep working.
		ctx = context.WithValue(ctx, organizationCodeKey, identity.OrganizationID)
		ctx = context.WithValue(ctx, userIDKey, identity.UserID)
		ctx = context.WithValue(ctx, userEmailKey, identity.Email)
		ctx = context.WithValue(ctx, roleKey, identity.Role)
		ctx = context.WithValue(ctx, authModeKey, identity.AuthMode)
		next.ServeHTTP(w, r.WithContext(ctx))
	})
}

// providerTier maps a per-session AuthMode to the strangler tier label
// used by axiaops_auth_provider_active / _last_seen_seconds metrics.
// Plan §4.5 reserves the labels {native, kinde, both} for the deletion-
// readiness queries; AuthMode is finer-grained and only useful for
// per-session debugging via the audit_log.
func providerTier(authMode string) string {
	switch authMode {
	case "password", "sso", "bootstrap":
		return "native"
	case "kinde":
		return "kinde"
	default:
		// A provider that returns an Identity with an AuthMode the
		// strangler doesn't recognise is a bug; bucket separately so
		// the alert fires instead of silently bleeding into "native".
		return "unknown"
	}
}
