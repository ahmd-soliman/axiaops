package api

import (
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"strings"
	"unicode"
	"unicode/utf8"

	"axiaops.io/api/internal/audit"
	"axiaops.io/api/internal/auth"
	"axiaops.io/api/internal/middleware"
	"axiaops.io/shared/authz"
	"axiaops.io/shared/model"
	"axiaops.io/shared/storage"
)

// displayNameMaxLen caps users.name at the same boundary as bootstrap and
// invitation flows. Empty is allowed and means "unset" — the dashboard falls
// back to the email local-part for rendering.
const displayNameMaxLen = 120

// meResponse is the wire shape for GET /v1/me. Permissions are sent as
// strings so the dashboard can drive UI gating without bundling the
// authz package into the JS layer. Organization is additive — older
// clients ignoring the field stay working.
type meResponse struct {
	UserID         string                `json:"user_id"`
	OrganizationID string                `json:"organization_id"`
	Email          string                `json:"email"`
	// Name is the user's display name as stored in users.name. Always
	// present in the response (empty string when unset). Surfaces on the
	// dashboard's Profile page; the same value drives invitation emails
	// and audit log entries via separate API paths. Editable form is
	// tracked under issue #78 (PATCH /v1/users/me).
	Name           string                `json:"name"`
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

	// Display name — best-effort. Fetch the user row by id (org-agnostic
	// so cross-org members work the same from any org context). Failures
	// degrade to an empty Name rather than 500ing /v1/me — the dashboard
	// renders "—" in that case and the rest of the page still works.
	if uid != "" {
		if u, err := h.store.GetUserByID(ctx, uid); err == nil {
			resp.Name = u.Name
		} else {
			slog.Warn("me: get user by id failed; serving empty name",
				"user_id", uid, "error", err)
		}
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

// updateCurrentUser handles PATCH /v1/users/me. Self-service display-name
// edit (issue #78). Authn-only — every authenticated user can rename
// themselves; the userID is the capability.
//
// Body: {"name": string}. Trimmed; empty allowed (unset); length capped at
// displayNameMaxLen runes; rejects control characters. Mirrors the
// updateCurrentOrganization validator shape.
//
// Audit row written under the caller's current org context with metadata
// {old_name, new_name} — symmetric with AuditActionOrganizationRenamed.
// Returns the updated meResponse so the dashboard can refresh in one
// round-trip without a follow-up GET /v1/me.
func (h *Handler) updateCurrentUser(w http.ResponseWriter, r *http.Request) {
	uid := middleware.UserID(r.Context())
	if uid == "" {
		http.Error(w, "forbidden", http.StatusForbidden)
		return
	}

	var req struct {
		Name string `json:"name"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "invalid JSON body", http.StatusBadRequest)
		return
	}
	req.Name = strings.TrimSpace(req.Name)
	if !validDisplayName(req.Name) {
		http.Error(w, "name must be 0..120 visible characters", http.StatusBadRequest)
		return
	}

	// No-op short-circuit: if the caller posts the name they already have,
	// skip the UPDATE and the audit row entirely. Frontend's `dirty` check
	// guards the normal path; a raw curl or a rapid double-submit can
	// otherwise land here and write a spurious audit row with
	// old_name == new_name. One extra SELECT per PATCH is cheap (this is
	// a rare endpoint), and worth keeping audit_log signal-dense.
	if current, err := h.store.GetUserByID(r.Context(), uid); err == nil && current.Name == req.Name {
		writeJSON(w, updateUserResponse{Name: current.Name})
		return
	}

	oldName, err := h.store.UpdateUserName(r.Context(), uid, req.Name)
	if err != nil {
		switch {
		case errors.Is(err, storage.ErrUserNotFound):
			http.Error(w, "user not found", http.StatusNotFound)
		default:
			slog.Error("users: update name failed", "error", err, "user_id", uid)
			http.Error(w, "internal error", http.StatusInternalServerError)
		}
		return
	}

	audit.Record(r, h.store, model.AuditEvent{
		Action:       model.AuditActionUserNameChanged,
		ResourceType: "user",
		ResourceID:   uid,
		Metadata: map[string]any{
			"old_name": oldName,
			"new_name": req.Name,
		},
	})

	// Respond with just the updated field, not the full /v1/me shape.
	// Earlier revisions delegated to h.getMe so the dashboard could refresh
	// in one round-trip; but that path could 500 *after* the UPDATE and
	// audit had committed, leaving the client to retry and write a duplicate
	// audit row. The dashboard's MeContext refetches /v1/me on its own
	// after the mutation succeeds, so this minimal shape is sufficient.
	writeJSON(w, updateUserResponse{Name: req.Name})
}

// updateUserResponse is the slim wire shape returned by PATCH /v1/users/me.
// The frontend doesn't read it today (it calls MeContext.refresh() instead),
// but a stable shape keeps `curl`-bypass-of-the-frontend honest.
type updateUserResponse struct {
	Name string `json:"name"`
}

// validDisplayName enforces 0..displayNameMaxLen runes and rejects control
// characters. Empty is intentionally allowed — the user choosing to unset
// their name is a valid state (frontend falls back to email).
func validDisplayName(s string) bool {
	if utf8.RuneCountInString(s) > displayNameMaxLen {
		return false
	}
	for _, r := range s {
		if unicode.IsControl(r) {
			return false
		}
	}
	return true
}
