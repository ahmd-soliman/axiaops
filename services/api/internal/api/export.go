// Package api — GET /v1/export.
//
// Implements the GDPR Art. 15 (access) and Art. 20 (portability) right by
// returning a single JSON document containing every per-tenant row the
// calling tenant owns. See docs/compliance/gdpr_plan.md §4.1 for the full
// product surface and acceptance criteria.
//
// Gated by PermDataExport (owner-only). The export bundles account configurations, cost/resource/zombie records, and the full audit_log of every member — granting
// a less-privileged role would broaden a single click into a download of
// data the role cannot otherwise extract in one shot. Loosening this is a
// product decision, not a code one.
package api

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"time"

	"axiaops.io/api/internal/audit"
	"axiaops.io/api/internal/middleware"
	"axiaops.io/shared/model"
	"axiaops.io/shared/observability"
	"axiaops.io/shared/storage"
)

// exportSchemaVersion is bumped when the JSON shape changes. Consumers
// (privacy lead's tooling, customer's data warehouse) can branch on it.
const exportSchemaVersion = "1"

// auditExportPageSize is the per-page cap for audit_log pagination during
// export. Sized below the Postgres store's hard cap (500) so we always send
// a request the store will accept without coercion.
const auditExportPageSize = 500

// auditExportMaxPages bounds the audit-log pagination loop so a runaway
// tenant or stuck cursor can't make a single export hold a goroutine forever.
// At 500 rows × 200 pages this caps a single export at 100k audit rows —
// well above the per-tenant volume we'd expect inside the 12-month retention
// window. If a tenant ever hits the cap, the response carries
// `audit_log_truncated: true` and the privacy lead falls back to a direct
// DB dump for the residual.
const auditExportMaxPages = 200

// tenantExportMember is the per-member projection in the export. Trims the
// internal-only fields (invited_by, updated_at, the wrapping struct) down to
// what a data subject would recognise.
type tenantExportMember struct {
	UserID   string    `json:"user_id"`
	Email    string    `json:"email,omitempty"`
	Name     string    `json:"name,omitempty"`
	Role     string    `json:"role"`
	JoinedAt time.Time `json:"joined_at"`
}

// tenantExport is the top-level JSON payload served by GET /v1/export.
//
// Field order follows the §2.1/§2.2 inventory in the GDPR plan so a reader
// can cross-reference what's present here against what the plan promises.
type tenantExport struct {
	SchemaVersion     string                 `json:"schema_version"`
	GeneratedAt       time.Time              `json:"generated_at"`
	TenantID          string                 `json:"tenant_id"`
	Notes             string                 `json:"notes,omitempty"`
	Members           []tenantExportMember   `json:"members"`
	Accounts          []model.Account        `json:"accounts"`
	Resources         []model.ResourceRecord `json:"resources"`
	Zombies           []model.ZombieResource `json:"zombies"`
	CostRecords       []model.CostRecord     `json:"cost_records"`
	Snapshots         []model.ZombieSnapshot `json:"snapshots"`
	ActiveDismissals  []model.DismissAction  `json:"active_dismissals"`
	AuditLog          []model.AuditEvent     `json:"audit_log"`
	AuditLogTruncated bool                   `json:"audit_log_truncated,omitempty"`
}

// exportTenantData handles GET /v1/export.
//
// Composes existing tenant-scoped Store methods rather than introducing a
// dedicated ExportTenant query. Each call sits under RLS via the tenant set
// on r.Context(); cross-tenant leakage would require an RLS bug, not a
// handler bug. Audit-logs the export with per-table row counts so a future
// DSR audit can show *what* a download contained, not just that it happened.
func (h *Handler) exportTenantData(w http.ResponseWriter, r *http.Request) {
	tid := middleware.TenantID(r.Context())
	if tid == "" {
		http.Error(w, "forbidden", http.StatusForbidden)
		return
	}
	ctx := storage.WithTenantID(r.Context(), tid)

	exp, err := h.buildTenantExport(ctx, tid)
	if err != nil {
		observability.Global.DataExportsTotal.WithLabelValues("failed").Inc()
		slog.Error("export tenant failed",
			"tenant_id", tid,
			"user_id", middleware.UserID(r.Context()),
			"error", err)
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}

	audit.Record(r, h.store, model.AuditEvent{
		Action:       model.AuditActionDataExported,
		ResourceType: "tenant",
		ResourceID:   tid,
		Metadata: map[string]any{
			"members":             len(exp.Members),
			"accounts":            len(exp.Accounts),
			"resources":           len(exp.Resources),
			"zombies":             len(exp.Zombies),
			"cost_records":        len(exp.CostRecords),
			"snapshots":           len(exp.Snapshots),
			"active_dismissals":   len(exp.ActiveDismissals),
			"audit_log":           len(exp.AuditLog),
			"audit_log_truncated": exp.AuditLogTruncated,
			"schema_version":      exp.SchemaVersion,
		},
	})

	filename := fmt.Sprintf("axiaops-export-%s-%s.json",
		tid, exp.GeneratedAt.UTC().Format("20060102T150405Z"))
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Content-Disposition", fmt.Sprintf(`attachment; filename="%s"`, filename))

	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")
	if err := enc.Encode(exp); err != nil {
		// Headers are already flushed if the body is non-trivial — there is
		// nothing useful we can send back to the client. Log and bump the
		// failure counter; the partial body is the user-visible signal.
		observability.Global.DataExportsTotal.WithLabelValues("failed").Inc()
		slog.Error("export tenant: encode failed",
			"tenant_id", tid,
			"error", err)
		return
	}

	observability.Global.DataExportsTotal.WithLabelValues("ok").Inc()
	slog.Info("tenant data exported",
		"tenant_id", tid,
		"user_id", middleware.UserID(r.Context()),
		"actor_email", middleware.UserEmail(r.Context()),
		"members", len(exp.Members),
		"audit_rows", len(exp.AuditLog),
		"audit_truncated", exp.AuditLogTruncated)
}

// buildTenantExport composes the export payload from existing Store methods.
// Failures short-circuit; partial exports would be worse than a 500 because
// the user can't tell what's missing.
func (h *Handler) buildTenantExport(ctx context.Context, tenantID string) (*tenantExport, error) {
	exp := &tenantExport{
		SchemaVersion: exportSchemaVersion,
		GeneratedAt:   time.Now().UTC(),
		TenantID:      tenantID,
		Notes:         "Encrypted account credentials and internal-only audit fields are excluded. See docs/compliance/gdpr_plan.md §4.2.",
	}

	memberships, err := h.store.ListMemberships(ctx)
	if err != nil {
		return nil, fmt.Errorf("list memberships: %w", err)
	}
	exp.Members = make([]tenantExportMember, 0, len(memberships))
	for _, m := range memberships {
		exp.Members = append(exp.Members, tenantExportMember{
			UserID:   m.UserID,
			Email:    m.Email,
			Name:     m.Name,
			Role:     m.Role,
			JoinedAt: m.CreatedAt,
		})
	}

	if exp.Accounts, err = h.store.ListAccounts(ctx); err != nil {
		return nil, fmt.Errorf("list accounts: %w", err)
	}
	if exp.Accounts == nil {
		exp.Accounts = []model.Account{}
	}

	if exp.Resources, err = h.store.LoadResources(ctx); err != nil {
		return nil, fmt.Errorf("load resources: %w", err)
	}
	if exp.Resources == nil {
		exp.Resources = []model.ResourceRecord{}
	}

	if exp.Zombies, err = h.store.LoadZombies(ctx); err != nil {
		return nil, fmt.Errorf("load zombies: %w", err)
	}
	if exp.Zombies == nil {
		exp.Zombies = []model.ZombieResource{}
	}

	// CostFilter zero-value returns the full window the store applies by
	// default. The store layer caps lookback (Days=0 means "store default",
	// which is the 90-day retention window from gdpr_plan.md §5).
	if exp.CostRecords, err = h.store.ListCostRecords(ctx, storage.CostFilter{}); err != nil {
		return nil, fmt.Errorf("list cost records: %w", err)
	}
	if exp.CostRecords == nil {
		exp.CostRecords = []model.CostRecord{}
	}

	if exp.Snapshots, err = h.store.ListSnapshots(ctx, ""); err != nil {
		return nil, fmt.Errorf("list snapshots: %w", err)
	}
	if exp.Snapshots == nil {
		exp.Snapshots = []model.ZombieSnapshot{}
	}

	// Active dismissals only — revoked rows are deleted by the revoke handler,
	// so there is nothing else for us to hold.
	if exp.ActiveDismissals, err = h.store.ListActiveDismissals(ctx, ""); err != nil {
		return nil, fmt.Errorf("list dismissals: %w", err)
	}
	if exp.ActiveDismissals == nil {
		exp.ActiveDismissals = []model.DismissAction{}
	}

	// Page through the audit log so a 12-month-deep tenant doesn't get
	// silently truncated at the store's per-call cap.
	exp.AuditLog = []model.AuditEvent{}
	cursor := model.AuditCursor{}
	for page := 0; page < auditExportMaxPages; page++ {
		batch, err := h.store.AuditLogList(ctx, model.AuditFilter{
			Limit:  auditExportPageSize,
			Cursor: cursor,
		})
		if err != nil {
			return nil, fmt.Errorf("list audit (page %d): %w", page, err)
		}
		if len(batch) == 0 {
			return exp, nil
		}
		exp.AuditLog = append(exp.AuditLog, batch...)
		if len(batch) < auditExportPageSize {
			return exp, nil
		}
		last := batch[len(batch)-1]
		cursor = model.AuditCursor{CreatedAt: last.CreatedAt, ID: last.ID}
	}
	exp.AuditLogTruncated = true
	return exp, nil
}
