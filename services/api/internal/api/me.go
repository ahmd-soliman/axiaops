package api

import (
	"log/slog"
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

	// Memberships is every org this user belongs to with the role they
	// hold there. Drives the org-switcher dropdown in the nav (B1.5
	// §4.7.2) and tells the frontend whether to even render it (no point
	// for single-org users). Empty under DEV_MODE.
	Memberships []membershipSummary `json:"memberships"`

	// AuthProvider is the coarse-grained provider label that
	// authenticated this request: "native" (cookie + sessions table —
	// password / sso / bootstrap) or "" under DEV_MODE where no
	// provider ran. Frontend uses this to render the right login
	// screen on 401 redirects.
	AuthProvider string `json:"auth_provider"`

	// AuthMode is the per-session detail behind AuthProvider —
	// "password", "sso", or "bootstrap". Lets the dashboard distinguish,
	// e.g., a freshly-bootstrapped owner from a normal password login.
	// Empty under DEV_MODE.
	AuthMode string `json:"auth_mode,omitempty"`
}

// membershipSummary is the slim per-membership shape returned in /v1/me's
// memberships array — just enough for the org switcher to render and for
// the frontend to highlight the active row (by matching organization_id
// against meResponse.OrganizationID).
type membershipSummary struct {
	OrganizationID   string `json:"organization_id"`
	OrganizationName string `json:"organization_name"`
	Role             string `json:"role"`
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

	// Organization block — best-effort. A pure read keyed on the
	// organization_id from the request context.
	if tid != "" {
		if org, err := h.store.GetOrganizationByID(ctx, tid); err == nil {
			orgResp := toOrganizationResponse(org)
			resp.Organization = &orgResp
		}
	}

	// Memberships block — best-effort, populates the org-switcher
	// payload (B1.5). Always non-nil so the frontend can serialise as
	// `[]` rather than `null`. Failures degrade to an empty list rather
	// than 500ing /v1/me — the user already has a valid session and can
	// operate inside their current org; the only consequence of an empty
	// list is that the switcher dropdown is hidden until the next
	// refresh succeeds.
	//
	// IMPORTANT for slice 2: the modified /v1/auth/login MUST NOT inherit
	// this swallow pattern. There, an empty membership list materially
	// changes the auth flow (single-org → mint vs multi-org → org
	// picker), and a transient DB error must surface as 500, never
	// silently land the user in the wrong org.
	resp.Memberships = []membershipSummary{}
	if uid != "" {
		rows, err := h.store.ListUserMemberships(ctx, uid)
		if err != nil {
			slog.Warn("me: list user memberships failed; serving empty switcher",
				"user_id", uid, "error", err)
		}
		for _, m := range rows {
			resp.Memberships = append(resp.Memberships, membershipSummary{
				OrganizationID:   m.OrganizationID,
				OrganizationName: m.OrganizationName,
				Role:             m.Role,
			})
		}
	}

	writeJSON(w, resp)
}

