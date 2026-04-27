package api

import (
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"net/mail"
	"strings"
	"time"

	"axiaops.io/api/internal/audit"
	"axiaops.io/api/internal/middleware"
	"axiaops.io/shared/authz"
	"axiaops.io/shared/model"
	"axiaops.io/shared/storage"
)

// invitationResponse is the wire shape for one pending invitation.
type invitationResponse struct {
	ID             string    `json:"id"`
	OrganizationID string    `json:"organization_id"`
	Email          string    `json:"email"`
	Role           string    `json:"role"`
	Status         string    `json:"status"`
	InvitedBy      struct {
		UserID string `json:"user_id"`
		Email  string `json:"email"`
	} `json:"invited_by"`
	ExpiresAt time.Time `json:"expires_at"`
	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`
}

func toInvitationResponse(inv model.PendingInvitation) invitationResponse {
	out := invitationResponse{
		ID:             inv.ID,
		OrganizationID: inv.OrganizationID,
		Email:          inv.Email,
		Role:           inv.Role,
		Status:         inv.Status,
		ExpiresAt:      inv.ExpiresAt,
		CreatedAt:      inv.CreatedAt,
		UpdatedAt:      inv.UpdatedAt,
	}
	out.InvitedBy.UserID = inv.InvitedByUserID
	out.InvitedBy.Email = inv.InvitedByEmail
	return out
}

// createInvitation handles POST /v1/invitations. See docs/invitation-flow.md §4.
//
// Two-phase commit: store insert → Kinde InviteUser → store kinde IDs.
// On Kinde failure the pending row is revoked (compensating action) and 502
// is returned. Permission gating is applied here on top of PermMembersInvite —
// invites at admin role need PermMembersManageAdmin (owner-only).
func (h *Handler) createInvitation(w http.ResponseWriter, r *http.Request) {
	if h.kinde == nil {
		http.Error(w, "invitations not configured", http.StatusServiceUnavailable)
		return
	}

	tid := middleware.OrganizationID(r.Context())
	uid := middleware.UserID(r.Context())
	actorEmail := middleware.UserEmail(r.Context())
	ctx := storage.WithOrganizationID(r.Context(), tid)

	var req struct {
		Email string `json:"email"`
		Role  string `json:"role"`
		Name  string `json:"name"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "invalid JSON body", http.StatusBadRequest)
		return
	}
	req.Email = strings.TrimSpace(req.Email)
	req.Role = strings.TrimSpace(req.Role)
	req.Name = strings.TrimSpace(req.Name)
	if req.Email == "" || req.Role == "" {
		http.Error(w, "email and role are required", http.StatusBadRequest)
		return
	}
	if _, err := mail.ParseAddress(req.Email); err != nil {
		http.Error(w, "invalid email", http.StatusBadRequest)
		return
	}
	if !model.ValidInvitationRoles[req.Role] {
		// Owner is excluded by ValidInvitationRoles. Match the existing
		// /v1/memberships error message so dashboards can match on it.
		if req.Role == string(authz.RoleOwner) {
			http.Error(w, "owner role can only be assigned via transfer-ownership", http.StatusBadRequest)
			return
		}
		http.Error(w, "invalid role", http.StatusBadRequest)
		return
	}

	// Inviting at admin level requires owner (members:manage_admin).
	if req.Role == string(authz.RoleAdmin) {
		callerRole, _ := h.store.RoleOf(ctx, tid, uid)
		if !authz.Allows(authz.Role(callerRole), authz.PermMembersManageAdmin) {
			http.Error(w, "forbidden", http.StatusForbidden)
			return
		}
	}

	// Phase 1: insert pending row (handles upsert + sentinel pre-checks).
	inv, inserted, err := h.store.CreatePendingInvitation(ctx, model.PendingInvitation{
		OrganizationID:  tid,
		Email:           req.Email,
		Role:            req.Role,
		InvitedByUserID: uid,
		InvitedByEmail:  actorEmail,
	})
	if err != nil {
		switch {
		case errors.Is(err, storage.ErrInvitationAlreadyMember):
			writeError(w, http.StatusConflict, "already_a_member", "user is already a member of this organization")
		case errors.Is(err, storage.ErrUserExistsNoMembership):
			writeError(w, http.StatusConflict, "user_exists_use_memberships", "user has logged in already; use POST /v1/memberships")
		default:
			slog.Error("invitations: CreatePendingInvitation failed", "error", err, "organization_id", tid)
			http.Error(w, "internal error", http.StatusInternalServerError)
		}
		return
	}

	// Phase 2: ask Kinde to send the invitation email.
	orgCode := middleware.OrganizationCode(r.Context())
	kindeInvitationID, kindeUserID, kerr := h.kinde.InviteUser(ctx, orgCode, req.Email, req.Name)
	if kerr != nil {
		// Compensate: revoke the local pending row so a retry isn't blocked
		// by the partial unique index.
		if rerr := h.store.RevokePendingInvitation(ctx, inv.ID); rerr != nil {
			slog.Error("invitations: compensating revoke failed",
				"error", rerr, "invitation_id", inv.ID)
		}
		slog.Error("invitations: Kinde InviteUser failed", "error", kerr, "organization_id", tid)
		writeError(w, http.StatusBadGateway, "kinde_invite_failed", "failed to send invitation via Kinde; please retry")
		return
	}

	// Phase 3: best-effort persist Kinde IDs. If this fails the invite was
	// still sent — log it; the row keeps status=pending and revoke will skip
	// the Kinde step gracefully (RemoveUser is a no-op when kinde_user_id="").
	if uerr := h.store.UpdateInvitationKindeIDs(ctx, inv.ID, kindeInvitationID, kindeUserID); uerr != nil {
		slog.Error("invitations: UpdateInvitationKindeIDs failed",
			"error", uerr, "invitation_id", inv.ID)
	} else {
		inv.KindeInvitationID = kindeInvitationID
		inv.KindeUserID = kindeUserID
	}

	audit.Record(r, h.store, model.AuditEvent{
		Action:       model.AuditActionMemberInvited,
		ResourceType: "invitation",
		ResourceID:   inv.ID,
		Metadata: map[string]any{
			"email":               req.Email,
			"role":                req.Role,
			"resent":              !inserted,
			"kinde_invitation_id": kindeInvitationID,
		},
	})

	// 201 on first create, 200 on re-invite (refreshed pending row).
	if inserted {
		w.WriteHeader(http.StatusCreated)
	}
	writeJSON(w, toInvitationResponse(inv))
}

// listInvitations handles GET /v1/invitations[?status=pending|expired|revoked].
func (h *Handler) listInvitations(w http.ResponseWriter, r *http.Request) {
	tid := middleware.OrganizationID(r.Context())
	ctx := storage.WithOrganizationID(r.Context(), tid)

	status := strings.TrimSpace(r.URL.Query().Get("status"))
	if status == "" {
		status = model.InvitationStatusPending
	}
	if !model.ValidInvitationStatuses[status] {
		http.Error(w, "invalid status", http.StatusBadRequest)
		return
	}

	rows, err := h.store.ListPendingInvitations(ctx, status)
	if err != nil {
		slog.Error("invitations: list failed", "error", err)
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	out := make([]invitationResponse, 0, len(rows))
	for _, inv := range rows {
		out = append(out, toInvitationResponse(inv))
	}
	writeJSON(w, out)
}

// revokeInvitation handles DELETE /v1/invitations/{id}.
//
// Calls Kinde's RemoveUser first to invalidate the email link, then flips the
// local row to revoked. 410 on already-revoked, 502 on Kinde 5xx (local row
// stays pending so retry works). Idempotent on Kinde 404 (treated as success
// inside the kinde client).
func (h *Handler) revokeInvitation(w http.ResponseWriter, r *http.Request) {
	if h.kinde == nil {
		http.Error(w, "invitations not configured", http.StatusServiceUnavailable)
		return
	}
	id := r.PathValue("id")
	tid := middleware.OrganizationID(r.Context())
	ctx := storage.WithOrganizationID(r.Context(), tid)

	inv, err := h.store.GetPendingInvitation(ctx, id)
	if errors.Is(err, storage.ErrInvitationNotFound) {
		http.Error(w, "invitation not found", http.StatusNotFound)
		return
	}
	if err != nil {
		slog.Error("invitations: get failed", "error", err, "invitation_id", id)
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}

	// Permission tier: revoking an admin invitation needs members:manage_admin
	// (mirrors the existing membership-management rule).
	uid := middleware.UserID(r.Context())
	required := authz.PermMembersInvite
	if inv.Role == string(authz.RoleAdmin) {
		required = authz.PermMembersManageAdmin
	}
	callerRole, _ := h.store.RoleOf(ctx, tid, uid)
	if !authz.Allows(authz.Role(callerRole), required) {
		http.Error(w, "forbidden", http.StatusForbidden)
		return
	}

	if inv.Status != model.InvitationStatusPending {
		http.Error(w, "invitation is not in pending state", http.StatusGone)
		return
	}

	orgCode := middleware.OrganizationCode(r.Context())
	if err := h.kinde.RemoveUser(ctx, orgCode, inv.KindeUserID); err != nil {
		// 5xx — leave local row pending so a retry hits Kinde again.
		slog.Error("invitations: Kinde RemoveUser failed",
			"error", err, "invitation_id", inv.ID)
		writeError(w, http.StatusBadGateway, "kinde_revoke_failed", "failed to revoke invitation via Kinde; please retry")
		return
	}

	if err := h.store.RevokePendingInvitation(ctx, id); err != nil {
		switch {
		case errors.Is(err, storage.ErrInvitationNotFound):
			http.Error(w, "invitation not found", http.StatusNotFound)
		case errors.Is(err, storage.ErrInvitationNotPending):
			http.Error(w, "invitation is not in pending state", http.StatusGone)
		default:
			slog.Error("invitations: revoke failed", "error", err, "invitation_id", id)
			http.Error(w, "internal error", http.StatusInternalServerError)
		}
		return
	}

	audit.Record(r, h.store, model.AuditEvent{
		Action:       model.AuditActionMemberRemoved,
		ResourceType: "invitation",
		ResourceID:   inv.ID,
		Metadata: map[string]any{
			"email": inv.Email,
			"phase": "invitation_revoked",
		},
	})

	w.WriteHeader(http.StatusNoContent)
}

// writeError writes a JSON error envelope. Used by invitation handlers to give
// the dashboard a structured code for the user_exists_use_memberships /
// already_a_member / kinde_invite_failed cases.
func writeError(w http.ResponseWriter, status int, code, message string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(map[string]any{
		"error":   code,
		"message": message,
	})
}
