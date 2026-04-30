package api

import (
	"net/http"

	"axiaops.io/api/internal/auth"
	"axiaops.io/api/internal/middleware"
	"axiaops.io/shared/authz"
	"axiaops.io/shared/storage"
)

// meResponse is the wire shape for GET /v1/me. Permissions are sent as
// strings so the dashboard can drive UI gating without bundling the
// authz package into the JS layer. Organization is additive — older
// clients ignoring the field stay working.
type meResponse struct {
	UserID         string                `json:"user_id"`
	OrganizationID string                `json:"organization_id"`
	Email          string                `json:"email"`
	Role           string                `json:"role"`
	Permissions    []string              `json:"permissions"`
	Organization   *organizationResponse `json:"organization,omitempty"`

	// AuthProvider is the strangler tier that authenticated this
	// request: "native" (cookie + sessions table — password / sso /
	// bootstrap), "kinde" (legacy Bearer JWT), or "" under DEV_MODE
	// where no provider ran. Frontend uses this to render the right
	// login screen on 401 redirects and to show an SSO badge in the
	// account menu. See plan §4.5.
	AuthProvider string `json:"auth_provider"`

	// AuthMode is the per-session detail behind AuthProvider —
	// "password", "sso", "bootstrap" (under native), or "kinde". Lets
	// the dashboard distinguish, e.g., a freshly-bootstrapped owner
	// from a normal password login. Empty under DEV_MODE.
	AuthMode string `json:"auth_mode,omitempty"`
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
	orgCode := middleware.OrganizationCode(r.Context())
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

	authMode := middleware.AuthMode(r.Context())
	resp := meResponse{
		UserID:         uid,
		OrganizationID: tid,
		Email:          email,
		Role:           role,
		Permissions:    permStrs,
		AuthProvider:   auth.AuthProviderTier(authMode),
		AuthMode:       authMode,
	}

	// Organization block — best-effort. A user with no membership might still
	// have a valid organization (e.g. invited user pre-redemption); fall back
	// to whatever UpsertOrganization returns for the org_code in the JWT.
	if orgCode != "" {
		if org, err := h.store.UpsertOrganization(ctx, orgCode, ""); err == nil {
			orgResp := toOrganizationResponse(org)
			resp.Organization = &orgResp
		}
	}

	writeJSON(w, resp)
}

