// Package api — GET /v1/export.
//
// Implements the GDPR Art. 15 (access) and Art. 20 (portability) right by
// returning a single JSON document containing every per-organization row the
// calling organization owns.
//
// Gated by PermDataExport (owner-only). The export bundles billing data,
// the full audit_log of every member, and account configurations — granting
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

	"golang.org/x/sync/errgroup"

	"axiaops.io/api/internal/audit"
	"axiaops.io/api/internal/middleware"
	"axiaops.io/shared/model"
	"axiaops.io/shared/observability"
	"axiaops.io/shared/storage"
)

// exportSchemaVersion is bumped when the JSON shape changes. Consumers
// (privacy lead's tooling, customer's data warehouse) can branch on it.
const exportSchemaVersion = "1"

// auditExportPageSize is sized below the Postgres store's hard cap (500)
// so requests never get coerced.
const auditExportPageSize = 500

// auditExportMaxPages bounds the audit-log pagination loop so a runaway
// organization or stuck cursor can't make a single export hold a goroutine forever.
// At 500 rows × 200 pages this caps a single export at 100k audit rows. If a
// organization ever hits the cap, the response carries `audit_log_truncated: true`
// and the privacy lead falls back to a direct DB dump for the residual.
const auditExportMaxPages = 200

// exportConcurrency caps how many of the eight organization-scoped reads run in
// parallel. Each acquires its own pgx transaction (RLS sets app.organization_id
// per-tx), so unbounded fan-out × concurrent exports could starve the pool.
// Four keeps the parallelism win (~4× over serial) without monopolising
// connections under load.
const exportConcurrency = 4

type orgExportMember struct {
	UserID   string    `json:"user_id"`
	Email    string    `json:"email,omitempty"`
	Name     string    `json:"name,omitempty"`
	Role     string    `json:"role"`
	JoinedAt time.Time `json:"joined_at"`
}

type orgExport struct {
	SchemaVersion     string                 `json:"schema_version"`
	GeneratedAt       time.Time              `json:"generated_at"`
	OrganizationID    string                 `json:"organization_id"`
	Notes             string                 `json:"notes,omitempty"`
	Members           []orgExportMember      `json:"members"`
	Accounts          []model.Account        `json:"accounts"`
	Resources         []model.ResourceRecord `json:"resources"`
	Zombies           []model.ZombieResource `json:"zombies"`
	CostRecords       []model.CostRecord     `json:"cost_records"`
	Snapshots         []model.ZombieSnapshot `json:"snapshots"`
	ActiveDismissals  []model.DismissAction  `json:"active_dismissals"`
	AuditLog          []model.AuditEvent     `json:"audit_log"`
	AuditLogTruncated bool                   `json:"audit_log_truncated,omitempty"`
}

func (h *Handler) exportOrganizationData(w http.ResponseWriter, r *http.Request) {
	tid := middleware.OrganizationID(r.Context())
	if tid == "" {
		http.Error(w, "forbidden", http.StatusForbidden)
		return
	}
	ctx := storage.WithOrganizationID(r.Context(), tid)

	exp, err := h.buildOrgExport(ctx, tid)
	if err != nil {
		observability.Global.DataExportsTotal.WithLabelValues("failed").Inc()
		slog.Error("export organization failed",
			"organization_id", tid,
			"user_id", middleware.UserID(r.Context()),
			"error", err)
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}

	// Audit before encode, deliberately. If the response stream fails
	// mid-body the row over-counts (we logged a "successful" export the
	// client may not have fully received) — for GDPR that's safer than
	// under-counting, since the privacy lead would rather verify a recorded
	// export than miss a leak. Don't move this below enc.Encode.
	audit.Record(r, h.store, model.AuditEvent{
		Action:       model.AuditActionDataExported,
		ResourceType: "organization",
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

	// Bypasses writeJSON because the export is downloaded as a file and a
	// privacy lead may inspect it by hand — pretty-printing earns its keep
	// here in a way that doesn't apply to the regular JSON API.
	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")
	if err := enc.Encode(exp); err != nil {
		// Headers are already flushed if the body is non-trivial — there is
		// nothing useful we can send back to the client. Log and bump the
		// failure counter; the partial body is the user-visible signal.
		observability.Global.DataExportsTotal.WithLabelValues("failed").Inc()
		slog.Error("export organization: encode failed", "organization_id", tid, "error", err)
		return
	}

	observability.Global.DataExportsTotal.WithLabelValues("ok").Inc()
	slog.Info("organization data exported",
		"organization_id", tid,
		"user_id", middleware.UserID(r.Context()),
		"actor_email", middleware.UserEmail(r.Context()),
		"members", len(exp.Members),
		"audit_rows", len(exp.AuditLog),
		"audit_truncated", exp.AuditLogTruncated)
}

// buildOrgExport runs the eight organization-scoped reads concurrently. Any
// goroutine failure cancels the rest via gctx and short-circuits — partial
// exports are worse than a 500 because the user can't tell what's missing.
func (h *Handler) buildOrgExport(ctx context.Context, organizationID string) (*orgExport, error) {
	exp := &orgExport{
		SchemaVersion:  exportSchemaVersion,
		GeneratedAt:    time.Now().UTC(),
		OrganizationID: organizationID,
		Notes:          "Encrypted account credentials, internal-only audit fields, and Stripe billing records (held by Stripe under §147 AO) are excluded.",
	}

	g, gctx := errgroup.WithContext(ctx)
	g.SetLimit(exportConcurrency)
	var memberships []model.MembershipWithUser

	g.Go(func() error {
		var err error
		memberships, err = h.store.ListMemberships(gctx)
		return wrapErr("list memberships", err)
	})
	g.Go(func() error {
		var err error
		exp.Accounts, err = h.store.ListAccounts(gctx)
		return wrapErr("list accounts", err)
	})
	g.Go(func() error {
		var err error
		exp.Resources, err = h.store.LoadResources(gctx)
		return wrapErr("load resources", err)
	})
	g.Go(func() error {
		var err error
		exp.Zombies, err = h.store.LoadZombies(gctx)
		return wrapErr("load zombies", err)
	})
	g.Go(func() error {
		var err error
		// CostFilter zero-value applies the store's default 90-day window
		// (gdpr_plan.md §5).
		exp.CostRecords, err = h.store.ListCostRecords(gctx, storage.CostFilter{})
		return wrapErr("list cost records", err)
	})
	g.Go(func() error {
		var err error
		exp.Snapshots, err = h.store.ListSnapshots(gctx, "")
		return wrapErr("list snapshots", err)
	})
	g.Go(func() error {
		var err error
		exp.ActiveDismissals, err = h.store.ListActiveDismissals(gctx, "")
		return wrapErr("list dismissals", err)
	})
	g.Go(func() error {
		return h.loadAuditLog(gctx, exp)
	})

	if err := g.Wait(); err != nil {
		return nil, err
	}

	exp.Members = make([]orgExportMember, 0, len(memberships))
	for _, m := range memberships {
		exp.Members = append(exp.Members, orgExportMember{
			UserID:   m.UserID,
			Email:    m.Email,
			Name:     m.Name,
			Role:     m.Role,
			JoinedAt: m.CreatedAt,
		})
	}

	emptyIfNil(&exp.Accounts)
	emptyIfNil(&exp.Resources)
	emptyIfNil(&exp.Zombies)
	emptyIfNil(&exp.CostRecords)
	emptyIfNil(&exp.Snapshots)
	emptyIfNil(&exp.ActiveDismissals)
	if exp.AuditLog == nil {
		exp.AuditLog = []model.AuditEvent{}
	}
	// loadAuditLog returns newest-first (mirrors the /v1/audit endpoint and
	// the postgres ORDER BY); for a privacy lead reading the export end-to-end
	// chronological order is more natural. Reverse in place — pages are
	// already individually sorted, so a single pass suffices.
	for i, j := 0, len(exp.AuditLog)-1; i < j; i, j = i+1, j-1 {
		exp.AuditLog[i], exp.AuditLog[j] = exp.AuditLog[j], exp.AuditLog[i]
	}

	return exp, nil
}

// loadAuditLog pages through the audit log so a 12-month-deep organization doesn't
// get silently truncated at the store's per-call cap. Sets AuditLogTruncated
// if the page ceiling is hit before the cursor exhausts.
func (h *Handler) loadAuditLog(ctx context.Context, exp *orgExport) error {
	cursor := model.AuditCursor{}
	for page := 0; page < auditExportMaxPages; page++ {
		batch, err := h.store.AuditLogList(ctx, model.AuditFilter{
			Limit:  auditExportPageSize,
			Cursor: cursor,
		})
		if err != nil {
			return fmt.Errorf("list audit (page %d): %w", page, err)
		}
		if len(batch) == 0 {
			return nil
		}
		exp.AuditLog = append(exp.AuditLog, batch...)
		if len(batch) < auditExportPageSize {
			return nil
		}
		last := batch[len(batch)-1]
		cursor = model.AuditCursor{CreatedAt: last.CreatedAt, ID: last.ID}
	}
	exp.AuditLogTruncated = true
	return nil
}

func wrapErr(op string, err error) error {
	if err == nil {
		return nil
	}
	return fmt.Errorf("%s: %w", op, err)
}

func emptyIfNil[T any](s *[]T) {
	if *s == nil {
		*s = []T{}
	}
}
