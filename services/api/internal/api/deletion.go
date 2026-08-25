// Package api — DELETE /v1/users/me and DELETE /v1/organizations/me.
//
// These two endpoints implement the GDPR right-to-erasure flow described in
// docs/ARCHITECTURE.md (§6, Right-to-erasure paths). Both are guarded
// by middleware (authn for /users/me, PermOrganizationDelete for /organizations/me) and
// both bump a Prometheus counter so the act of deletion has an operational
// trail that survives the audit_log purge.
package api

import (
	"errors"
	"log/slog"
	"net/http"

	"axiaops.io/api/internal/audit"
	"axiaops.io/api/internal/middleware"
	"axiaops.io/shared/model"
	"axiaops.io/shared/observability"
	"axiaops.io/shared/storage"
)

// deleteCurrentUser handles DELETE /v1/users/me.
//
// Authn-only: any logged-in user can delete themselves. The store enforces
// the sole-owner guard — a user who is the only owner of any organization gets
// 409 Conflict and is told to transfer or delete the organization first.
//
// Not audit-logged in audit_log: the user is leaving and audit_log is
// per-organization, so picking one organization to record the event is arbitrary. The
// Prometheus counter and slog line are the durable record.
func (h *Handler) deleteCurrentUser(w http.ResponseWriter, r *http.Request) {
	uid := middleware.UserID(r.Context())
	if uid == "" {
		http.Error(w, "forbidden", http.StatusForbidden)
		return
	}

	if err := h.store.DeleteUser(r.Context(), uid); err != nil {
		switch {
		case errors.Is(err, storage.ErrLastOwner):
			observability.Global.UserDeletionsTotal.WithLabelValues("conflict").Inc()
			http.Error(w,
				"you are the sole owner of one or more organizations — transfer ownership or delete those organizations first",
				http.StatusConflict)
		default:
			observability.Global.UserDeletionsTotal.WithLabelValues("failed").Inc()
			slog.Error("delete user failed",
				"user_id", uid,
				"actor_email", middleware.UserEmail(r.Context()),
				"error", err)
			http.Error(w, "internal error", http.StatusInternalServerError)
		}
		return
	}

	observability.Global.UserDeletionsTotal.WithLabelValues("ok").Inc()
	slog.Info("user deleted",
		"user_id", uid,
		"actor_email", middleware.UserEmail(r.Context()))
	w.WriteHeader(http.StatusNoContent)
}

// deleteCurrentOrganization handles DELETE /v1/organizations/me.
//
// Permission gate: PermOrganizationDelete (owner-only). The audit_log entry is
// written BEFORE the cascade so the row exists momentarily — it will be
// purged alongside the rest of the organization's data, but the audit-write
// counter and slog line endure.
func (h *Handler) deleteCurrentOrganization(w http.ResponseWriter, r *http.Request) {
	tid := middleware.OrganizationID(r.Context())
	if tid == "" {
		http.Error(w, "forbidden", http.StatusForbidden)
		return
	}

	// Best-effort audit. Written before the cascade so it lands in the
	// timeline; the cascade itself purges audit_log so the row will be gone
	// by the time the request returns. The slog/Prometheus side is the
	// permanent ops trail.
	audit.Record(r, h.store, model.AuditEvent{
		Action:       model.AuditActionOrganizationDeleted,
		ResourceType: "organization",
		ResourceID:   tid,
	})

	if err := h.store.DeleteOrganizationCascade(r.Context(), tid); err != nil {
		observability.Global.OrganizationDeletionsTotal.WithLabelValues("failed").Inc()
		slog.Error("delete organization failed",
			"organization_id", tid,
			"user_id", middleware.UserID(r.Context()),
			"actor_email", middleware.UserEmail(r.Context()),
			"error", err)
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}

	observability.Global.OrganizationDeletionsTotal.WithLabelValues("ok").Inc()
	slog.Info("organization deleted",
		"organization_id", tid,
		"user_id", middleware.UserID(r.Context()),
		"actor_email", middleware.UserEmail(r.Context()))
	w.WriteHeader(http.StatusNoContent)
}
