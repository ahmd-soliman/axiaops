package api

import (
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"time"

	"axiaops.io/api/internal/audit"
	"axiaops.io/api/internal/middleware"
	"axiaops.io/shared/authz"
	"axiaops.io/shared/model"
	"axiaops.io/shared/storage"
)

// membershipResponse is the wire shape for a single membership row.
type membershipResponse struct {
	ID             string    `json:"id"`
	UserID         string    `json:"user_id"`
	Email          string    `json:"email"`
	Name           string    `json:"name"`
	Role           string    `json:"role"`
	InvitedBy      string    `json:"invited_by,omitempty"`
	ProvisionedVia string    `json:"provisioned_via,omitempty"`
	CreatedAt      time.Time `json:"created_at"`
	UpdatedAt      time.Time `json:"updated_at"`
}

func toMembershipResponse(m model.MembershipWithUser) membershipResponse {
	return membershipResponse{
		ID:             m.ID,
		UserID:         m.UserID,
		Email:          m.Email,
		Name:           m.Name,
		Role:           m.Role,
		InvitedBy:      m.InvitedBy,
		ProvisionedVia: m.ProvisionedVia,
		CreatedAt:      m.CreatedAt,
		UpdatedAt:      m.UpdatedAt,
	}
}

// listMemberships returns all memberships in the caller's organization.
// Permission: members:read (every authenticated organization user).
func (h *Handler) listMemberships(w http.ResponseWriter, r *http.Request) {
	tid := middleware.OrganizationID(r.Context())
	ctx := storage.WithOrganizationID(r.Context(), tid)

	rows, err := h.store.ListMemberships(ctx)
	if err != nil {
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	out := make([]membershipResponse, 0, len(rows))
	for _, m := range rows {
		out = append(out, toMembershipResponse(m))
	}
	writeJSON(w, out)
}

// createMembership invites a user to the organization by email. The user must have
// already logged in to AxiaOps at least once — invite-by-email-before-first-
// login is deferred to Phase 2 (see docs/AUTHENTICATION.md (§2)). Admins can invite
// at member/viewer level; only owner can invite at admin level.
//
// Permission: members:invite for member/viewer roles, members:manage_admin
// for admin role.
func (h *Handler) createMembership(w http.ResponseWriter, r *http.Request) {
	tid := middleware.OrganizationID(r.Context())
	uid := middleware.UserID(r.Context())
	ctx := storage.WithOrganizationID(r.Context(), tid)

	var req struct {
		Email string `json:"email"`
		Role  string `json:"role"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "bad_request", "invalid JSON body")
		return
	}
	req.Email = strings.TrimSpace(req.Email)
	req.Role = strings.TrimSpace(req.Role)
	if req.Email == "" || req.Role == "" {
		writeError(w, http.StatusBadRequest, "bad_request", "email and role are required")
		return
	}
	if err := model.ValidateInvitableEmail(req.Email); err != nil {
		writeError(w, http.StatusBadRequest, "invalid_email", err.Error())
		return
	}
	if !validRole(req.Role) {
		writeError(w, http.StatusBadRequest, "invalid_role", "invalid role")
		return
	}
	// Owner is reserved for transfer-ownership — never created via invite.
	// Reject before the permission check so /v1/memberships {role:owner}
	// always 400s, even when the caller has manage_admin.
	if req.Role == string(authz.RoleOwner) {
		writeError(w, http.StatusBadRequest, "invalid_role", "owner role can only be assigned via transfer-ownership")
		return
	}
	// Promoting to admin requires owner.
	if req.Role == string(authz.RoleAdmin) {
		callerRole, _ := h.store.RoleOf(ctx, tid, uid)
		if !authz.Allows(authz.Role(callerRole), authz.PermMembersManageAdmin) {
			writeError(w, http.StatusForbidden, "forbidden", "inviting at admin role requires owner permission")
			return
		}
	}

	target, err := h.store.GetUserByEmail(ctx, req.Email)
	if errors.Is(err, storage.ErrUserNotFound) {
		writeError(w, http.StatusNotFound, "user_not_found", "user has not logged in to AxiaOps yet")
		return
	}
	if err != nil {
		writeError(w, http.StatusInternalServerError, "internal", "internal error")
		return
	}

	m := model.Membership{
		OrganizationID: tid,
		UserID:         target.ID,
		Role:           req.Role,
		InvitedBy:      uid,
	}
	if err := h.store.SaveMembership(ctx, m); err != nil {
		if errors.Is(err, storage.ErrMembershipExists) {
			writeError(w, http.StatusConflict, "already_a_member", "user is already a member of this organization")
			return
		}
		writeError(w, http.StatusInternalServerError, "internal", "internal error")
		return
	}

	audit.Record(r, h.store, model.AuditEvent{
		Action:       model.AuditActionMemberInvited,
		ResourceType: "membership",
		ResourceID:   target.ID,
		Metadata: map[string]any{
			"role":  req.Role,
			"email": req.Email,
		},
	})

	writeJSONStatus(w, http.StatusCreated, toMembershipResponse(model.MembershipWithUser{
		Membership: m,
		Email:      target.Email,
		Name:       target.Name,
	}))
}

// updateMembershipRole changes the role of a membership. The "stricter
// permission" rule applies: if either the current or proposed role is
// admin/owner, the caller needs members:manage_admin (owner). Otherwise
// members:manage_basic (admin) is enough.
func (h *Handler) updateMembershipRole(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	tid := middleware.OrganizationID(r.Context())
	uid := middleware.UserID(r.Context())
	ctx := storage.WithOrganizationID(r.Context(), tid)

	var req struct {
		Role string `json:"role"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "invalid JSON body", http.StatusBadRequest)
		return
	}
	req.Role = strings.TrimSpace(req.Role)
	if !validRole(req.Role) {
		http.Error(w, "invalid role", http.StatusBadRequest)
		return
	}
	// Owner is set only via transfer-ownership.
	if req.Role == string(authz.RoleOwner) {
		http.Error(w, "owner role can only be assigned via transfer-ownership", http.StatusBadRequest)
		return
	}

	current, err := h.store.GetMembership(ctx, id)
	if errors.Is(err, storage.ErrMembershipNotFound) {
		http.Error(w, "membership not found", http.StatusNotFound)
		return
	}
	if err != nil {
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}

	// Pick the stricter required permission based on current and target role.
	needsAdminPerm := isElevated(current.Role) || isElevated(req.Role)
	callerRole, _ := h.store.RoleOf(ctx, tid, uid)
	required := authz.PermMembersManageBasic
	if needsAdminPerm {
		required = authz.PermMembersManageAdmin
	}
	if !authz.Allows(authz.Role(callerRole), required) {
		http.Error(w, "forbidden", http.StatusForbidden)
		return
	}

	if err := h.store.UpdateMembershipRole(ctx, id, req.Role); err != nil {
		switch {
		case errors.Is(err, storage.ErrMembershipNotFound):
			http.Error(w, "membership not found", http.StatusNotFound)
		case errors.Is(err, storage.ErrLastOwner):
			http.Error(w, "cannot demote the last owner; transfer ownership first", http.StatusConflict)
		default:
			http.Error(w, "internal error", http.StatusInternalServerError)
		}
		return
	}

	audit.Record(r, h.store, model.AuditEvent{
		Action:       model.AuditActionMemberRoleChanged,
		ResourceType: "membership",
		ResourceID:   id,
		Metadata: map[string]any{
			"old_role": current.Role,
			"new_role": req.Role,
			"user_id":  current.UserID,
		},
	})

	w.WriteHeader(http.StatusNoContent)
}

// deleteMembership removes a user from the organization. Self-leave bypasses the
// permission check (you don't need members:manage_* to leave an organization). The
// last-owner guard still applies — a sole owner must transfer first.
func (h *Handler) deleteMembership(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	tid := middleware.OrganizationID(r.Context())
	uid := middleware.UserID(r.Context())
	ctx := storage.WithOrganizationID(r.Context(), tid)

	target, err := h.store.GetMembership(ctx, id)
	if errors.Is(err, storage.ErrMembershipNotFound) {
		http.Error(w, "membership not found", http.StatusNotFound)
		return
	}
	if err != nil {
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}

	// Self-leave is always allowed (subject to the last-owner guard at the
	// store layer).
	if target.UserID != uid {
		callerRole, _ := h.store.RoleOf(ctx, tid, uid)
		required := authz.PermMembersManageBasic
		if isElevated(target.Role) {
			required = authz.PermMembersManageAdmin
		}
		if !authz.Allows(authz.Role(callerRole), required) {
			http.Error(w, "forbidden", http.StatusForbidden)
			return
		}
	}

	if err := h.store.DeleteMembership(ctx, id); err != nil {
		switch {
		case errors.Is(err, storage.ErrMembershipNotFound):
			http.Error(w, "membership not found", http.StatusNotFound)
		case errors.Is(err, storage.ErrLastOwner):
			http.Error(w, "cannot remove the last owner; transfer ownership first", http.StatusConflict)
		default:
			http.Error(w, "internal error", http.StatusInternalServerError)
		}
		return
	}

	audit.Record(r, h.store, model.AuditEvent{
		Action:       model.AuditActionMemberRemoved,
		ResourceType: "membership",
		ResourceID:   id,
		Metadata: map[string]any{
			"user_id":    target.UserID,
			"role":       target.Role,
			"self_leave": target.UserID == uid,
		},
	})

	w.WriteHeader(http.StatusNoContent)
}

// transferOwnership atomically demotes the current owner to admin and
// promotes the target user to owner. Permission: organization:transfer (owner only).
func (h *Handler) transferOwnership(w http.ResponseWriter, r *http.Request) {
	tid := middleware.OrganizationID(r.Context())
	ctx := storage.WithOrganizationID(r.Context(), tid)

	var req struct {
		ToUserID string `json:"to_user_id"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "invalid JSON body", http.StatusBadRequest)
		return
	}
	req.ToUserID = strings.TrimSpace(req.ToUserID)
	if req.ToUserID == "" {
		http.Error(w, "to_user_id is required", http.StatusBadRequest)
		return
	}

	if err := h.store.TransferOwnership(ctx, req.ToUserID); err != nil {
		switch {
		case errors.Is(err, storage.ErrMembershipNotFound):
			http.Error(w, "target user is not a member of this organization", http.StatusNotFound)
		default:
			http.Error(w, "internal error", http.StatusInternalServerError)
		}
		return
	}

	audit.Record(r, h.store, model.AuditEvent{
		Action:       model.AuditActionOwnershipTransferred,
		ResourceType: "organization",
		ResourceID:   tid,
		Metadata: map[string]any{
			"to_user_id": req.ToUserID,
		},
	})

	w.WriteHeader(http.StatusNoContent)
}

func validRole(role string) bool {
	switch role {
	case string(authz.RoleOwner), string(authz.RoleAdmin), string(authz.RoleMember), string(authz.RoleViewer):
		return true
	}
	return false
}

func isElevated(role string) bool {
	return role == string(authz.RoleAdmin) || role == string(authz.RoleOwner)
}
