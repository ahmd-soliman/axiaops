package staff

import (
	"log/slog"
	"net/http"
	"strings"

	"axiaops.io/shared/model"
)

// publicAdminPath reports whether path bypasses staff authentication. Login
// must be reachable unauthenticated (it mints the session); logout is tolerant
// (clearing a stale cookie shouldn't require a live session); the infra
// endpoints are unauthenticated like the tenant server's.
func publicAdminPath(path string) bool {
	switch path {
	case "/admin/auth/login", "/admin/auth/logout", "/livez", "/readyz", "/metrics":
		return true
	}
	return false
}

// WrapStaff authenticates each admin-plane request via the staff Provider and
// attaches the resolved Identity to the context. Unauthenticated requests get
// a fixed 401 (no reason leaked). The admin plane has NO DEV_MODE bypass — a
// cross-tenant plane must never be reachable without real staff auth.
func WrapStaff(provider Provider, next http.Handler) http.Handler {
	if provider == nil {
		panic("staff: WrapStaff requires a non-nil Provider")
	}
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// OPTIONS preflights are already terminated by the outer CORS layer
		// (adminCORS) before reaching here, so this gate only needs the
		// public-path bypass.
		if publicAdminPath(r.URL.Path) {
			next.ServeHTTP(w, r)
			return
		}
		id, err := provider.Authenticate(r)
		if err != nil {
			slog.Warn("staff: request unauthenticated", "method", r.Method, "path", r.URL.Path)
			writeError(w, http.StatusUnauthorized, "unauthenticated", "staff authentication required")
			return
		}
		next.ServeHTTP(w, r.WithContext(WithIdentity(r.Context(), id)))
	})
}

// RequireRole returns middleware that 403s unless the staff principal holds at
// least one of the allowed roles. superadmin is always sufficient — it is the
// plane's break-glass tier and must never be locked out of a surface it
// administers. With no roles listed, any authenticated staff passes.
func RequireRole(allowed ...model.StaffRole) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			id, ok := FromContext(r.Context())
			if !ok {
				writeError(w, http.StatusUnauthorized, "unauthenticated", "staff authentication required")
				return
			}
			if len(allowed) == 0 || id.HasRole(model.StaffRoleSuperadmin) {
				next.ServeHTTP(w, r)
				return
			}
			for _, role := range allowed {
				if id.HasRole(role) {
					next.ServeHTTP(w, r)
					return
				}
			}
			labels := make([]string, len(allowed))
			for i, role := range allowed {
				labels[i] = string(role)
			}
			slog.Warn("staff: insufficient role",
				"staff_user_id", id.StaffUserID, "path", r.URL.Path, "need", strings.Join(labels, "|"))
			writeError(w, http.StatusForbidden, "forbidden", "insufficient staff role")
		})
	}
}
