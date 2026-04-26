package api

import (
	"net/http"

	"axiaops.io/api/internal/middleware"
	"axiaops.io/shared/authz"
	"axiaops.io/shared/storage"
)

// meResponse is the wire shape for GET /v1/me. Permissions are sent as
// strings so the dashboard can drive UI gating without bundling the
// authz package into the JS layer.
type meResponse struct {
	UserID         string   `json:"user_id"`
	OrganizationID string   `json:"organization_id"`
	Email          string   `json:"email"`
	Role           string   `json:"role"`
	Permissions    []string `json:"permissions"`
}

// getMe returns the authenticated user's role and permissions. Used by the
// dashboard to gate UI controls and to refresh after a 403 from another
// endpoint. Authentication is required (the route sits under /v1/) but
// no permission is — every authenticated user can read their own role,
// including users with no membership row, who get role="" and an empty
// permissions array. The dashboard treats that as "removed user, redirect
// to login."
func (h *Handler) getMe(w http.ResponseWriter, r *http.Request) {
	tid := middleware.OrganizationID(r.Context())
	uid := middleware.UserID(r.Context())
	email := middleware.UserEmail(r.Context())

	ctx := storage.WithOrganizationID(r.Context(), tid)
	role, err := h.store.RoleOf(ctx, tid, uid)
	if err != nil {
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}

	perms := authz.PermissionsOf(authz.Role(role))
	permStrs := make([]string, 0, len(perms))
	for _, p := range perms {
		permStrs = append(permStrs, string(p))
	}

	writeJSON(w, meResponse{
		UserID:         uid,
		OrganizationID: tid,
		Email:          email,
		Role:           role,
		Permissions:    permStrs,
	})
}
