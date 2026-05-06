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
// Local DB rename + audit. Permission gate: PermOrganizationUpdate
// (owner-only). Wired via Require.
func (h *Handler) updateCurrentOrganization(w http.ResponseWriter, r *http.Request) {
	tid := middleware.OrganizationID(r.Context())
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

	// Snapshot the current org so audit metadata carries the old name.
	current, err := h.store.GetOrganizationByID(ctx, tid)
	if err != nil {
		slog.Error("organizations: load failed", "error", err, "organization_id", tid)
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	oldName := current.Name

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

	audit.Record(r, h.store, model.AuditEvent{
		Action:       model.AuditActionOrganizationRenamed,
		ResourceType: "organization",
		ResourceID:   tid,
		Metadata: map[string]any{
			"old_name": oldName,
			"new_name": req.Name,
		},
	})

	updated, err := h.store.GetOrganizationByID(ctx, tid)
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
// language-specific rules.
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
