// Package api — DELETE /v1/users/me and DELETE /v1/tenants/me.
//
// These two endpoints implement the GDPR right-to-erasure flow described in
// docs/rbac-design.md §10 and docs/audit_trail_plan.md §7. Both are guarded
// by middleware (authn for /users/me, PermOrganizationDelete for /tenants/me) and
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
// the sole-owner guard — a user who is the only owner of any tenant gets
// 409 Conflict and is told to transfer or delete the tenant first.
//
// Not audit-logged in audit_log: the user is leaving and audit_log is
// per-tenant, so picking one tenant to record the event is arbitrary. The
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
				"you are the sole owner of one or more tenants — transfer ownership or delete those tenants first",
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

// deleteCurrentTenant handles DELETE /v1/tenants/me.
//
// Permission gate: PermOrganizationDelete (owner-only). The audit_log entry is
// written BEFORE the cascade so the row exists momentarily — it will be
// purged alongside the rest of the tenant's data, but the audit-write
// counter and slog line endure.
func (h *Handler) deleteCurrentTenant(w http.ResponseWriter, r *http.Request) {
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
		Action:       model.AuditActionTenantDeleted,
		ResourceType: "tenant",
		ResourceID:   tid,
	})

	if err := h.store.DeleteOrganizationCascade(r.Context(), tid); err != nil {
		observability.Global.OrganizationDeletionsTotal.WithLabelValues("failed").Inc()
		slog.Error("delete tenant failed",
			"organization_id", tid,
			"user_id", middleware.UserID(r.Context()),
			"actor_email", middleware.UserEmail(r.Context()),
			"error", err)
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}

	observability.Global.OrganizationDeletionsTotal.WithLabelValues("ok").Inc()
	slog.Info("tenant deleted",
		"organization_id", tid,
		"user_id", middleware.UserID(r.Context()),
		"actor_email", middleware.UserEmail(r.Context()))
	w.WriteHeader(http.StatusNoContent)
}
