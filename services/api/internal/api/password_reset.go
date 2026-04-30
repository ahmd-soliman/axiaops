package api

import (
	"crypto/rand"
	"encoding/hex"
	"log/slog"
	"net/http"
	"os"
	"strconv"
	"time"

	"github.com/google/uuid"

	"axiaops.io/api/internal/audit"
	"axiaops.io/api/internal/auth"
	"axiaops.io/api/internal/middleware"
	"axiaops.io/shared/authz"
	"axiaops.io/shared/model"
	"axiaops.io/shared/storage"
)

// passwordResetIssueResponse is the body returned to the admin who
// requested the reset. The plaintext token appears only here — once,
// in the response — and never elsewhere on the server (DB stores hash,
// no slog call carries it). The admin shares the URL OOB with the user.
type passwordResetIssueResponse struct {
	UserID        string    `json:"user_id"`
	RedemptionURL string    `json:"redemption_url"`
	ExpiresAt     time.Time `json:"expires_at"`
}

// issuePasswordReset handles POST /v1/users/{id}/password-reset.
//
// Permission: PermMembersManageBasic (admin+). Owners can reset anyone
// in their organization including each other; admins cannot reset an
// owner's password (the role check on the target enforces this). Plan
// §4.2 acceptance: returns redemption URL + expires_at; redemption
// invalidates the token and revokes other sessions for the user (the
// latter is the redeem handler's job).
func (h *Handler) issuePasswordReset(w http.ResponseWriter, r *http.Request) {
	tid := middleware.OrganizationID(r.Context())
	actorID := middleware.UserID(r.Context())
	ctx := storage.WithOrganizationID(r.Context(), tid)

	targetUserID := r.PathValue("id")
	if targetUserID == "" {
		writeError(w, http.StatusBadRequest, "bad_request", "user id is required")
		return
	}

	// Resolve target's role to enforce the cross-tier rule: admins
	// can't reset owner passwords (otherwise an admin could escalate
	// by resetting the only owner). Owners can reset everyone.
	targetRole, err := h.store.RoleOf(ctx, tid, targetUserID)
	if err != nil {
		slog.Error("password-reset: RoleOf target failed", "error", err, "user_id", targetUserID)
		writeError(w, http.StatusInternalServerError, "internal", "internal error")
		return
	}
	if targetRole == "" {
		writeError(w, http.StatusNotFound, "not_found", "user not found in this organization")
		return
	}
	if targetRole == "owner" {
		// Defence in depth: only owners reset another owner's password.
		// PermMembersManageBasic (the outer gate) admits admins, who
		// lack the manage_admin tier — without this inner check an
		// admin could reset an owner's password and intercept the
		// redemption URL before sharing it OOB. Use the canonical
		// permission table so a future role-model change inherits the
		// same gating automatically.
		actorRole, err := h.store.RoleOf(ctx, tid, actorID)
		if err != nil {
			slog.Error("password-reset: RoleOf actor failed", "error", err, "actor_id", actorID)
			writeError(w, http.StatusInternalServerError, "internal", "internal error")
			return
		}
		if !authz.Allows(authz.Role(actorRole), authz.PermMembersManageAdmin) {
			writeError(w, http.StatusForbidden, "forbidden", "only owners can reset an owner's password")
			return
		}
	}

	plaintext, err := newResetToken()
	if err != nil {
		slog.Error("password-reset: token mint failed", "error", err)
		writeError(w, http.StatusInternalServerError, "internal", "internal error")
		return
	}
	expires := time.Now().UTC().Add(passwordResetTTL())

	resetID := uuid.New().String()
	if err := h.store.CreatePasswordReset(ctx, resetID, targetUserID, tid, auth.HashToken(plaintext), actorID, expires); err != nil {
		slog.Error("password-reset: CreatePasswordReset failed", "error", err, "user_id", targetUserID)
		writeError(w, http.StatusInternalServerError, "internal", "internal error")
		return
	}

	audit.Record(r, h.store, model.AuditEvent{
		Action:       model.AuditActionUserPasswordResetIssued,
		ResourceType: "user",
		ResourceID:   targetUserID,
		Metadata: map[string]any{
			"reset_id":   resetID,
			"expires_at": expires,
		},
	})

	writeJSON(w, passwordResetIssueResponse{
		UserID:        targetUserID,
		RedemptionURL: h.buildPasswordResetURL(plaintext),
		ExpiresAt:     expires,
	})
}

// buildPasswordResetURL composes the OOB URL admins share with users.
// Mirrors invitations' buildRedemptionURL: empty publicHost emits a
// relative URL the frontend resolves against window.location.origin.
func (h *Handler) buildPasswordResetURL(token string) string {
	const path = "/password-reset?token="
	if h.publicHost == "" {
		return path + token
	}
	return h.publicHost + path + token
}

// newResetToken returns 32 bytes of CSPRNG entropy hex-encoded — same
// shape as invitation tokens. The plaintext is returned to the caller
// once; the store gets only the hash.
func newResetToken() (string, error) {
	buf := make([]byte, 32)
	if _, err := rand.Read(buf); err != nil {
		return "", err
	}
	return hex.EncodeToString(buf), nil
}

// passwordResetTTL reads PASSWORD_RESET_TTL_HOURS from env (plan §4.5,
// default 4h). Short by design — admin-issued tokens are expected to
// be redeemed promptly. Warns once per malformed value so an operator
// who fat-fingers the env var sees something in the logs.
func passwordResetTTL() time.Duration {
	const defaultTTL = 4 * time.Hour
	v := os.Getenv("PASSWORD_RESET_TTL_HOURS")
	if v == "" {
		return defaultTTL
	}
	h, err := strconv.Atoi(v)
	if err != nil || h <= 0 {
		slog.Warn("auth: invalid PASSWORD_RESET_TTL_HOURS, using default",
			"value", v, "default_hours", int(defaultTTL/time.Hour))
		return defaultTTL
	}
	return time.Duration(h) * time.Hour
}

