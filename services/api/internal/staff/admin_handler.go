package staff

import (
	"errors"
	"log/slog"
	"net/http"

	"axiaops.io/api/internal/auth"
	"axiaops.io/api/internal/httpjson"
	"axiaops.io/shared/model"
	"axiaops.io/shared/storage"
)

// requireSuperadmin returns the caller's identity if it holds superadmin, else
// writes the appropriate 401/403 and returns ok=false. Used by the staff-
// management routes, which ServeMux registers as bare HandleFuncs (so the
// role gate is enforced in-handler rather than via RequireRole middleware).
func (h *Handler) requireSuperadmin(w http.ResponseWriter, r *http.Request) (Identity, bool) {
	id, ok := FromContext(r.Context())
	if !ok {
		writeError(w, http.StatusUnauthorized, "unauthenticated", "staff authentication required")
		return Identity{}, false
	}
	if !id.HasRole(model.StaffRoleSuperadmin) {
		writeError(w, http.StatusForbidden, "forbidden", "superadmin role required")
		return Identity{}, false
	}
	return id, true
}

type staffListItem struct {
	StaffUserID string   `json:"staff_user_id"`
	Email       string   `json:"email"`
	Name        string   `json:"name"`
	Status      string   `json:"status"`
	Roles       []string `json:"roles"`
}

func (h *Handler) listStaff(w http.ResponseWriter, r *http.Request) {
	if _, ok := h.requireSuperadmin(w, r); !ok {
		return
	}
	users, grantsByUser, err := h.store.ListStaffUsers(r.Context())
	if err != nil {
		slog.Error("staff: list staff", "error", err)
		writeError(w, http.StatusInternalServerError, "internal_error", "could not list staff")
		return
	}
	items := make([]staffListItem, 0, len(users))
	for i, u := range users {
		items = append(items, staffListItem{
			StaffUserID: u.ID, Email: u.Email, Name: u.Name, Status: u.Status,
			Roles: roleStrings(grantsToRoles(grantsByUser[i])),
		})
	}
	writeJSON(w, http.StatusOK, map[string]any{"staff": items})
}

type createStaffRequest struct {
	Email    string   `json:"email"`
	Name     string   `json:"name"`
	Password string   `json:"password"`
	Roles    []string `json:"roles"`
}

func (h *Handler) createStaff(w http.ResponseWriter, r *http.Request) {
	actor, ok := h.requireSuperadmin(w, r)
	if !ok {
		return
	}
	var req createStaffRequest
	if err := httpjson.Decode(w, r, &req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid_request", "malformed body")
		return
	}
	if req.Email == "" {
		writeError(w, http.StatusBadRequest, "invalid_request", "email required")
		return
	}
	if err := auth.CheckPolicyWithIdentity(req.Password, auth.PolicyContext{Email: req.Email, Name: req.Name}); err != nil {
		writeError(w, http.StatusBadRequest, "weak_password", err.Error())
		return
	}
	roles, err := parseRoles(req.Roles)
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid_role", err.Error())
		return
	}

	hash, err := auth.Hash(req.Password)
	if err != nil {
		slog.Error("staff: hash password", "error", err)
		writeError(w, http.StatusInternalServerError, "internal_error", "could not create staff")
		return
	}
	created, err := h.store.CreateStaffUser(r.Context(), storage.CreateStaffUserInput{
		Email: req.Email, Name: req.Name, PasswordHash: hash, Roles: roles, GrantedBy: actor.StaffUserID,
	})
	if errors.Is(err, storage.ErrStaffEmailExists) {
		writeError(w, http.StatusConflict, "staff_email_taken", "a staff user with that email exists")
		return
	}
	if err != nil {
		slog.Error("staff: create staff", "error", err)
		writeError(w, http.StatusInternalServerError, "internal_error", "could not create staff")
		return
	}
	slog.Info("staff: created", "staff_user_id", created.ID, "by", actor.StaffUserID)
	writeJSON(w, http.StatusCreated, staffListItem{
		StaffUserID: created.ID, Email: created.Email, Name: created.Name,
		Status: created.Status, Roles: roleStrings(roles),
	})
}

type roleRequest struct {
	Role string `json:"role"`
}

func (h *Handler) grantRole(w http.ResponseWriter, r *http.Request) {
	actor, ok := h.requireSuperadmin(w, r)
	if !ok {
		return
	}
	targetID := r.PathValue("id")
	var req roleRequest
	if err := httpjson.Decode(w, r, &req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid_request", "malformed body")
		return
	}
	role := model.StaffRole(req.Role)
	if !model.ValidStaffRole(role) {
		writeError(w, http.StatusBadRequest, "invalid_role", "unknown staff role")
		return
	}
	if err := h.store.GrantStaffRole(r.Context(), targetID, role, actor.StaffUserID); errors.Is(err, storage.ErrStaffNotFound) {
		writeError(w, http.StatusNotFound, "not_found", "no such staff user")
		return
	} else if err != nil {
		slog.Error("staff: grant role", "error", err)
		writeError(w, http.StatusInternalServerError, "internal_error", "could not grant role")
		return
	}
	slog.Info("staff: role granted", "target", targetID, "role", role, "by", actor.StaffUserID)
	w.WriteHeader(http.StatusNoContent)
}

func (h *Handler) revokeRole(w http.ResponseWriter, r *http.Request) {
	actor, ok := h.requireSuperadmin(w, r)
	if !ok {
		return
	}
	targetID := r.PathValue("id")
	role := model.StaffRole(r.PathValue("role"))
	if !model.ValidStaffRole(role) {
		writeError(w, http.StatusBadRequest, "invalid_role", "unknown staff role")
		return
	}

	// The last-superadmin guard is enforced atomically inside the store
	// (race-free row lock) — see RevokeStaffRole. The handler only maps its
	// sentinel to a 409.
	if err := h.store.RevokeStaffRole(r.Context(), targetID, role); errors.Is(err, storage.ErrLastStaffSuperadmin) {
		writeError(w, http.StatusConflict, "last_superadmin", "cannot revoke the last superadmin")
		return
	} else if err != nil {
		slog.Error("staff: revoke role", "error", err)
		writeError(w, http.StatusInternalServerError, "internal_error", "could not revoke role")
		return
	}
	slog.Info("staff: role revoked", "target", targetID, "role", role, "by", actor.StaffUserID)
	w.WriteHeader(http.StatusNoContent)
}

// parseRoles validates + dedups a role-string slice. At least one role is
// required — a staff user with no roles can authenticate but do nothing.
func parseRoles(in []string) ([]model.StaffRole, error) {
	if len(in) == 0 {
		return nil, errors.New("at least one role required")
	}
	seen := make(map[model.StaffRole]bool, len(in))
	out := make([]model.StaffRole, 0, len(in))
	for _, s := range in {
		role := model.StaffRole(s)
		if !model.ValidStaffRole(role) {
			return nil, errors.New("unknown staff role: " + s)
		}
		if !seen[role] {
			seen[role] = true
			out = append(out, role)
		}
	}
	return out, nil
}
