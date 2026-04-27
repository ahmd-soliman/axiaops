package api

import (
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"axiaops.io/api/internal/audit"
	"axiaops.io/api/internal/middleware"
	"axiaops.io/shared/model"
	"axiaops.io/shared/storage"
)

const orgNameMaxLen = 120

// updateCurrentOrganization handles PATCH /v1/organizations/me.
//
// Two-phase commit (matches docs/onboarding-wizard.md §5.1):
//  1. Local DB rename (no-op via UPDATE — autocommitted).
//  2. Push to Kinde via kinde.RenameOrganization.
//  3. On Kinde failure, revert local rename.
//
// Permission gate: PermOrganizationUpdate (owner-only). Wired via Require.
func (h *Handler) updateCurrentOrganization(w http.ResponseWriter, r *http.Request) {
	if h.kinde == nil {
		http.Error(w, "rename not configured", http.StatusServiceUnavailable)
		return
	}

	tid := middleware.OrganizationID(r.Context())
	orgCode := middleware.OrganizationCode(r.Context())
	ctx := storage.WithOrganizationID(r.Context(), tid)

	var req struct {
		Name string `json:"name"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "invalid JSON body", http.StatusBadRequest)
		return
	}
	req.Name = strings.TrimSpace(req.Name)
	if !validOrgName(req.Name) {
		http.Error(w, "name must be 1..120 visible characters", http.StatusBadRequest)
		return
	}

	// Snapshot the current org so we can revert if Kinde rejects the rename
	// and so the audit metadata carries the old name. Read with an empty name
	// argument: the on-conflict DO UPDATE is a no-op (preserves stored name)
	// and the row already exists by the time a request reaches PATCH (auth
	// middleware called UpsertOrganization with the JWT org_name).
	current, err := h.store.UpsertOrganization(ctx, orgCode, "")
	if err != nil {
		slog.Error("organizations: load failed", "error", err, "organization_id", tid)
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	oldName := current.Name

	// Phase 1: local rename.
	if err := h.store.RenameOrganization(ctx, req.Name); err != nil {
		switch {
		case errors.Is(err, storage.ErrOrganizationNotFound):
			http.Error(w, "organization not found", http.StatusNotFound)
		default:
			slog.Error("organizations: rename failed", "error", err, "organization_id", tid)
			http.Error(w, "internal error", http.StatusInternalServerError)
		}
		return
	}

	// Phase 2: push to Kinde. On failure, revert local.
	if err := h.kinde.RenameOrganization(ctx, orgCode, req.Name); err != nil {
		slog.Error("organizations: Kinde rename failed; reverting local",
			"error", err, "organization_id", tid)
		if rerr := h.store.RenameOrganization(ctx, oldName); rerr != nil {
			slog.Error("organizations: local revert failed",
				"error", rerr, "organization_id", tid)
		}
		writeError(w, http.StatusBadGateway, "kinde_rename_failed", "failed to sync rename with Kinde; please retry")
		return
	}

	audit.Record(r, h.store, model.AuditEvent{
		Action:       model.AuditActionOrganizationRenamed,
		ResourceType: "organization",
		ResourceID:   tid,
		Metadata: map[string]any{
			"old_name":     oldName,
			"new_name":     req.Name,
			"kinde_synced": true,
		},
	})

	// Re-read to surface the updated name + onboarding flag in the response.
	updated, err := h.store.UpsertOrganization(ctx, orgCode, req.Name)
	if err != nil {
		slog.Error("organizations: re-load failed", "error", err, "organization_id", tid)
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	writeJSON(w, toOrganizationResponse(updated))
}

// completeOnboarding handles POST /v1/organizations/me/onboarding/complete.
// Idempotent — already-complete returns the existing timestamp.
func (h *Handler) completeOnboarding(w http.ResponseWriter, r *http.Request) {
	tid := middleware.OrganizationID(r.Context())
	ctx := storage.WithOrganizationID(r.Context(), tid)

	var req struct {
		StepsSkipped []string `json:"steps_skipped"`
	}
	// Body is optional; ignore decode errors.
	_ = json.NewDecoder(r.Body).Decode(&req)

	completed, err := h.store.MarkOnboardingComplete(ctx)
	if err != nil {
		switch {
		case errors.Is(err, storage.ErrOrganizationNotFound):
			http.Error(w, "organization not found", http.StatusNotFound)
		default:
			slog.Error("organizations: mark onboarding complete failed",
				"error", err, "organization_id", tid)
			http.Error(w, "internal error", http.StatusInternalServerError)
		}
		return
	}

	audit.Record(r, h.store, model.AuditEvent{
		Action:       model.AuditActionOnboardingCompleted,
		ResourceType: "organization",
		ResourceID:   tid,
		Metadata: map[string]any{
			"steps_skipped": req.StepsSkipped,
		},
	})

	writeJSON(w, map[string]any{
		"onboarding_completed_at": completed,
	})
}

// organizationResponse is the wire shape for /v1/organizations/me.
type organizationResponse struct {
	ID                    string     `json:"id"`
	Name                  string     `json:"name"`
	OnboardingCompletedAt *time.Time `json:"onboarding_completed_at"`
}

func toOrganizationResponse(o model.Organization) organizationResponse {
	return organizationResponse{
		ID:                    o.ID,
		Name:                  o.Name,
		OnboardingCompletedAt: o.OnboardingCompletedAt,
	}
}

// validOrgName enforces 1..orgNameMaxLen runes and rejects strings containing
// control characters. Whitespace inside the name is fine, and we don't impose
// language-specific rules — Kinde will perform its own server-side validation
// and bounce 4xx errors back through the two-phase commit.
func validOrgName(s string) bool {
	if s == "" {
		return false
	}
	if utf8.RuneCountInString(s) > orgNameMaxLen {
		return false
	}
	for _, r := range s {
		if unicode.IsControl(r) {
			return false
		}
	}
	return true
}
