package api

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"axiaops.io/api/internal/audit"
	"axiaops.io/api/internal/auth"
	"axiaops.io/api/internal/middleware"
	"axiaops.io/shared/authz"
	"axiaops.io/shared/model"
	"axiaops.io/shared/storage"
)

// invitationResponse is the wire shape for one pending invitation.
type invitationResponse struct {
	ID             string `json:"id"`
	OrganizationID string `json:"organization_id"`
	Email          string `json:"email"`
	Role           string `json:"role"`
	Status         string `json:"status"`
	InvitedBy      struct {
		UserID string `json:"user_id"`
		Email  string `json:"email"`
	} `json:"invited_by"`
	ExpiresAt time.Time `json:"expires_at"`
	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`

	// RedemptionURL is set only on POST /v1/invitations. The admin shares
	// this URL OOB with the invitee. Empty (omitted) on list/get responses
	// — a previously-minted token's plaintext can't be reconstructed from
	// the stored hash.
	RedemptionURL string `json:"redemption_url,omitempty"`

	// EmailDelivery reports whether the redemption URL was emailed to the
	// invitee. Set only on POST /v1/invitations (omitted on list/get), and only
	// when an InviteMailer is wired. One of: "sent", "failed",
	// "skipped_no_transport" (no enabled email channel and no global SMTP
	// config), "skipped_no_public_host" (PUBLIC_HOST unset → no absolute link to
	// mail), "error" (transient internal failure resolving the transport). The
	// redemption URL is always returned regardless, so OOB sharing remains the
	// durable fallback when delivery is skipped or fails.
	EmailDelivery string `json:"email_delivery,omitempty"`

	// EnforcementHint is set to "sso_required" when the inviter's org has
	// an active OIDC connection with enforcement="required". The
	// redemption URL still mints a password session — the row is needed
	// for cross-org / IdP-outage break-glass — but EnforceSSO will 403
	// every authed request the invitee makes after redeeming, bricking
	// them on the second hop. The dashboard reads this field to render a
	// yellow callout on the invite-success modal so the admin knows to
	// tell the invitee to use SSO instead. Empty (omitted) when the org
	// is on optional / preferred / no enforcement, or when no active OIDC
	// connection is configured. Snapshot at invite-creation time, not a
	// live join — the frontend is free to lag if enforcement flips.
	// Tasks.md row 2.7.20.
	EnforcementHint string `json:"enforcement_hint,omitempty"`
}

// invitationEnforcementHintRequired is the literal string the dashboard
// pivots on. Constant rather than inlined so the test pin and the
// frontend reference resolve through the same symbol.
const invitationEnforcementHintRequired = "sso_required"

// Invite-email delivery outcomes reported via invitationResponse.EmailDelivery
// and the axiaops_auth_invite_email_total{outcome} metric. Shared with the
// default InviteMailer in invite_mailer.go (same package).
const (
	inviteEmailSent   = "sent"
	inviteEmailFailed = "failed"
	// inviteEmailSkippedNoTransport: the org has no enabled email channel and
	// no global env/SSM SMTP config is set — nothing to send through.
	inviteEmailSkippedNoTransport = "skipped_no_transport"
	inviteEmailSkippedNoHost      = "skipped_no_public_host"
	// inviteEmailError marks a transient internal failure while resolving the
	// transport (e.g. the channel-list DB read errored). Distinct from
	// skipped_no_transport so an operator can tell "nothing configured" apart
	// from "we couldn't check".
	inviteEmailError = "error"
)

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

// createInvitation handles POST /v1/invitations.
//
// Generates a token, persists its hash via Store.CreateNativeInvitation,
// and returns the redemption URL in the response body. The plaintext
// token never leaves this handler — never logged, never persisted
// server-side. Permission gating is applied here on top of
// PermMembersInvite — invites at admin role need PermMembersManageAdmin
// (owner-only).
func (h *Handler) createInvitation(w http.ResponseWriter, r *http.Request) {
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
	if !model.ValidInvitationRoles[req.Role] {
		if req.Role == string(authz.RoleOwner) {
			writeError(w, http.StatusBadRequest, "invalid_role", "owner role can only be assigned via transfer-ownership")
			return
		}
		writeError(w, http.StatusBadRequest, "invalid_role", "invalid role")
		return
	}

	if req.Role == string(authz.RoleAdmin) {
		callerRole, _ := h.store.RoleOf(ctx, tid, uid)
		if !authz.Allows(authz.Role(callerRole), authz.PermMembersManageAdmin) {
			writeError(w, http.StatusForbidden, "forbidden", "inviting at admin role requires owner permission")
			return
		}
	}

	plaintext, err := newInvitationToken()
	if err != nil {
		slog.Error("invitations: token mint failed", "error", err)
		writeError(w, http.StatusInternalServerError, "internal", "internal error")
		return
	}

	inv, inserted, err := h.store.CreateNativeInvitation(ctx, model.PendingInvitation{
		OrganizationID:  tid,
		Email:           req.Email,
		Role:            req.Role,
		InvitedByUserID: uid,
		InvitedByEmail:  actorEmail,
		InviteTokenHash: hashInvitationToken(plaintext),
	})
	if err != nil {
		switch {
		case errors.Is(err, storage.ErrInvitationAlreadyMember):
			writeError(w, http.StatusConflict, "already_a_member", "user is already a member of this organization")
		case errors.Is(err, storage.ErrUserExistsNoMembership):
			writeError(w, http.StatusConflict, "user_exists_use_memberships", "user has logged in already; use POST /v1/memberships")
		default:
			slog.Error("invitations: CreateNativeInvitation failed", "error", err, "organization_id", tid)
			writeError(w, http.StatusInternalServerError, "internal", "internal error")
		}
		return
	}

	resp := toInvitationResponse(inv)
	resp.RedemptionURL = h.buildRedemptionURL(plaintext)
	if h.orgHasRequiredSSO(ctx) {
		resp.EnforcementHint = invitationEnforcementHintRequired
	}

	// Best-effort: email the redemption URL to the invitee. Routed through the
	// InviteMailer seam (channel-first, global-SMTP fallback). Never fatal — the
	// URL is already in the response for OOB sharing, so a missing transport or
	// a relay failure must not fail the invitation. nil mailer (unwired in some
	// tests) ⇒ no delivery attempt, EmailDelivery omitted.
	if h.inviteMailer != nil {
		resp.EmailDelivery = h.inviteMailer.SendInvite(ctx, InviteMailRequest{
			OrganizationID: tid,
			Recipient:      req.Email,
			Role:           req.Role,
			InviterEmail:   actorEmail,
			RedemptionURL:  resp.RedemptionURL,
			ExpiresAt:      inv.ExpiresAt,
		})
	}

	audit.Record(r, h.store, model.AuditEvent{
		Action:       model.AuditActionMemberInvited,
		ResourceType: "invitation",
		ResourceID:   inv.ID,
		Metadata: map[string]any{
			"email":          req.Email,
			"role":           req.Role,
			"resent":         !inserted,
			"native":         true,
			"email_delivery": resp.EmailDelivery,
		},
	})

	status := http.StatusOK
	if inserted {
		status = http.StatusCreated
	}
	writeJSONStatus(w, status, resp)
}

// orgHasRequiredSSO reports whether the request-scoped org has at least
// one active OIDC connection with enforcement="required". Mirrors the
// posture that middleware.EnforceSSO actually gates on (org-wide,
// independent of the invitee's email domain — the invitee's domain
// doesn't enter the EnforceSSO decision, so it must not enter this
// hint either, or the dashboard would silently miss footgun cases).
//
// Failure-mode posture: any store error → false (no hint). The hint is
// pure UX clarity, not a security boundary; missing it on a transient
// DB hiccup is strictly better than failing the invitation creation.
func (h *Handler) orgHasRequiredSSO(ctx context.Context) bool {
	conns, err := h.store.ListSSOConnections(ctx)
	if err != nil {
		// Debug, not Warn: this fires on every invite create in every org
		// regardless of SSO posture, so a transient pool blip during a
		// high-volume window would otherwise spam Warn with no actionable
		// signal. The failure-mode is already pure UX (no hint = same
		// posture as a non-SSO org), not a security boundary.
		slog.Debug("invitations: enforcement-hint probe failed", "error", err)
		return false
	}
	for _, c := range conns {
		if c.Status != model.SSOStatusActive || c.Protocol != model.SSOProtocolOIDC {
			continue
		}
		if c.Enforcement == model.SSOEnforcementRequired {
			return true
		}
	}
	return false
}

// buildRedemptionURL composes the OOB URL the admin shares with the
// invitee. Empty publicHost emits a relative URL (the frontend resolves
// against window.location.origin); non-empty produces an absolute URL.
func (h *Handler) buildRedemptionURL(token string) string {
	const path = "/accept-invite?token="
	if h.publicHost == "" {
		return path + token
	}
	return h.publicHost + path + token
}

// newInvitationToken returns 32 bytes of CSPRNG entropy hex-encoded —
// 256 bits, well above the 128-bit floor for capability tokens.
// Plaintext is returned to the caller; the store gets only the hash.
func newInvitationToken() (string, error) {
	buf := make([]byte, 32)
	if _, err := rand.Read(buf); err != nil {
		return "", err
	}
	return hex.EncodeToString(buf), nil
}

// hashInvitationToken delegates to auth.HashToken so the on-disk hash
// format is identical between session tokens and invitation tokens.
// Centralising the format means a future migration to a different
// hash function (e.g. BLAKE3) only changes one place.
func hashInvitationToken(plaintext string) string {
	return auth.HashToken(plaintext)
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
// Flips the local row to revoked. 404 if not found, 410 if already revoked.
// The OOB redemption URL becomes invalid the moment the row is revoked —
// the redemption handler rejects revoked rows.
func (h *Handler) revokeInvitation(w http.ResponseWriter, r *http.Request) {
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

// writeError writes a JSON error envelope. Used by handlers in the api package
// that need to give the dashboard a structured `error` code (invitations,
// member-add validation, future handlers). Routes through writeJSONStatus so
// the Content-Type header ordering, encode-error logging, and any future
// writeJSON(Status) instrumentation apply uniformly. Lives in invitations.go
// for historical reasons; not invitation-specific.
func writeError(w http.ResponseWriter, status int, code, message string) {
	writeJSONStatus(w, status, map[string]any{
		"error":   code,
		"message": message,
	})
}
