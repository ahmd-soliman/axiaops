// auth_native.go — middleware that authenticates requests via the
// auth.Provider seam. Today's sole production impl is auth.NativeProvider
// (cookie + sessions table). The seam stays so a different impl (e.g. a
// remote IdP) can swap in without touching the middleware chain.

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
// collapses the per-session AuthMode ("password" | "sso" | "bootstrap")
// to "native"; "unknown" surfaces a Provider that returned an Identity
// without setting AuthMode (architect N1).
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

		// Auth-provider telemetry. Mapping: password/sso/bootstrap →
		// "native"; "" → "unknown".
		tier := providerTier(identity.AuthMode)
		observability.Global.AuthProviderActive.WithLabelValues(tier).Inc()
		observability.Global.AuthProviderLastSeen.WithLabelValues(tier).
			Set(float64(time.Now().Unix()))

		ctx := r.Context()
		ctx = context.WithValue(ctx, organizationIDKey, identity.OrganizationID)
		ctx = context.WithValue(ctx, userIDKey, identity.UserID)
		ctx = context.WithValue(ctx, userEmailKey, identity.Email)
		ctx = context.WithValue(ctx, userNameKey, identity.Name)
		ctx = context.WithValue(ctx, roleKey, identity.Role)
		ctx = context.WithValue(ctx, authModeKey, identity.AuthMode)
		next.ServeHTTP(w, r.WithContext(ctx))
	})
}

// providerTier delegates to auth.AuthProviderTier — the canonical
// AuthMode → strangler-tier mapping. Kept as a thin wrapper so the
// existing call site at WrapNative reads cleanly and so a future
// telemetry-specific override (e.g. a `both`-aware composite) has an
// obvious place to land without touching the auth package.
func providerTier(authMode string) string {
	if authMode == "" {
		// Telemetry deliberately diverges from /v1/me here: an empty
		// AuthMode reaching the metric write means a Provider returned
		// an Identity without setting AuthMode — that's a bug worth
		// observing as `unknown` rather than dropping into the empty
		// label (which could collide with cardinality scrubs).
		return "unknown"
	}
	return auth.AuthProviderTier(authMode)
}
