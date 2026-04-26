package middleware

import (
	"context"
	"log/slog"
	"net/http"

	"axiaops.io/shared/authz"
)

// RoleStore is the minimal Store surface that the Require decorator needs.
// Defining it here (rather than importing storage.Store) keeps middleware
// decoupled from the full Store interface — useful for tests.
type RoleStore interface {
	RoleOf(ctx context.Context, tenantID, userID string) (string, error)
}

// Require returns an http.Handler that allows the request only if the
// authenticated user's role grants the given permission. Authentication is
// assumed to have already populated tenant_id and user_id on the request
// context (via Auth.Wrap or DevBypass). Failure modes:
//
//   - tenant_id or user_id missing on context → 403 (caller forgot to wrap
//     in Auth.Wrap)
//   - RoleOf returns an error → 403 (fail-closed)
//   - role does not grant perm → 403
//
// All 403s log a warning with method/path/perm so denial reasons are visible
// in operational logs.
func Require(perm authz.Permission, store RoleStore, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		tid := OrganizationID(r.Context())
		uid := UserID(r.Context())
		if tid == "" || uid == "" {
			slog.Warn("authz: missing identity on context",
				"method", r.Method, "path", r.URL.Path, "perm", perm)
			http.Error(w, "forbidden", http.StatusForbidden)
			return
		}
		role, err := store.RoleOf(r.Context(), tid, uid)
		if err != nil {
			slog.Warn("authz: role lookup failed",
				"method", r.Method, "path", r.URL.Path, "perm", perm, "error", err)
			http.Error(w, "forbidden", http.StatusForbidden)
			return
		}
		if !authz.Allows(authz.Role(role), perm) {
			slog.Warn("authz: forbidden",
				"method", r.Method, "path", r.URL.Path, "perm", perm,
				"tenant_id", tid, "user_id", uid, "role", role)
			http.Error(w, "forbidden", http.StatusForbidden)
			return
		}
		next.ServeHTTP(w, r)
	})
}
