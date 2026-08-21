package staff

import (
	"errors"
	"log/slog"
	"net/http"
	"time"

	"axiaops.io/shared/model"
	"axiaops.io/shared/storage"
)

// tenantListItem is the per-org row in GET /admin/tenants. Metadata only — NOT
// tenant FinOps data (design §7.5: the summary is not a break-glass read).
type tenantListItem struct {
	OrganizationID string     `json:"organization_id"`
	OrgCode        string     `json:"org_code"`
	Name           string     `json:"name"`
	CreatedAt      time.Time  `json:"created_at"`
	OnboardedAt    *time.Time `json:"onboarded_at"`
}

func (h *Handler) listTenants(w http.ResponseWriter, r *http.Request) {
	orgs, err := h.store.ListAllOrganizations(r.Context())
	if err != nil {
		slog.Error("staff: list tenants", "error", err)
		writeError(w, http.StatusInternalServerError, "internal_error", "could not list tenants")
		return
	}
	items := make([]tenantListItem, 0, len(orgs))
	for _, o := range orgs {
		items = append(items, tenantListItem{
			OrganizationID: o.ID, OrgCode: o.OrgCode, Name: o.Name,
			CreatedAt: o.CreatedAt, OnboardedAt: o.OnboardingCompletedAt,
		})
	}
	writeJSON(w, http.StatusOK, map[string]any{"tenants": items})
}

// tenantDetail is GET /admin/tenants/{id}. Metadata only — NOT tenant FinOps
// data (design §7.5: the summary is not a break-glass read).
type tenantDetail struct {
	OrganizationID         string     `json:"organization_id"`
	OrgCode                string     `json:"org_code"`
	Name                   string     `json:"name"`
	CreatedAt              time.Time  `json:"created_at"`
	OnboardedAt            *time.Time `json:"onboarded_at"`
	AccountCount           int        `json:"account_count"`
	LastScanAt             *time.Time `json:"last_scan_at"`
	LatestTotalZombies     int        `json:"latest_total_zombies"`
	LatestPotentialSavings float64    `json:"latest_potential_savings"`
}

func (h *Handler) getTenant(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	sum, err := h.store.StaffTenantSummary(r.Context(), id)
	if errors.Is(err, storage.ErrOrganizationNotFound) {
		writeError(w, http.StatusNotFound, "not_found", "no such tenant")
		return
	}
	if err != nil {
		slog.Error("staff: get tenant", "error", err, "organization_id", id)
		writeError(w, http.StatusInternalServerError, "internal_error", "could not load tenant")
		return
	}
	writeJSON(w, http.StatusOK, summaryToDetail(sum))
}

func summaryToDetail(s model.StaffTenantSummary) tenantDetail {
	return tenantDetail{
		OrganizationID:         s.OrganizationID,
		OrgCode:                s.OrgCode,
		Name:                   s.Name,
		CreatedAt:              s.CreatedAt,
		OnboardedAt:            s.OnboardingCompletedAt,
		AccountCount:           s.AccountCount,
		LastScanAt:             s.LastScanAt,
		LatestTotalZombies:     s.LatestTotalZombies,
		LatestPotentialSavings: s.LatestPotentialSavings,
	}
}
