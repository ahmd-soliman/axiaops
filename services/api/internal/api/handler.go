package api

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"os"
	"strconv"
	"time"

	"github.com/google/uuid"

	"axiaops.io/api/internal/middleware"
	"axiaops.io/shared/analyzer"
	"axiaops.io/shared/crypto"
	"axiaops.io/shared/model"
	"axiaops.io/shared/queue"
	"axiaops.io/shared/storage"
)

// Handler serves ghost detection results over HTTP.
type Handler struct {
	store        storage.Store
	queue        queue.Queue
	ingestionURL string // used only by sync queue fallback
}

// New creates a Handler backed by the given store and queue.
func New(store storage.Store, q queue.Queue) *Handler {
	ingestionURL := os.Getenv("INGESTION_URL")
	if ingestionURL == "" {
		ingestionURL = "http://localhost:8081"
	}
	return &Handler{store: store, queue: q, ingestionURL: ingestionURL}
}

// Register attaches the routes to the given mux.
func (h *Handler) Register(mux *http.ServeMux) {
	mux.HandleFunc("GET /health", h.health)
	mux.HandleFunc("GET /v1/ghosts", h.listGhosts)
	mux.HandleFunc("GET /v1/summary", h.getSummary)
	mux.HandleFunc("GET /v1/trend", h.getTrend)
	mux.HandleFunc("GET /v1/trend/services", h.getTrendServices)
	mux.HandleFunc("GET /v1/trend/resource-types", h.getTrendResourceTypes)
	mux.HandleFunc("GET /v1/resources", h.listResources)
	mux.HandleFunc("GET /v1/accounts", h.listAccounts)
	mux.HandleFunc("GET /v1/accounts/{id}", h.getAccount)
	mux.HandleFunc("POST /v1/accounts", h.createAccount)
	mux.HandleFunc("PATCH /v1/accounts/{id}", h.updateAccount)
	mux.HandleFunc("DELETE /v1/accounts/{id}", h.deleteAccount)
	mux.HandleFunc("POST /v1/accounts/{id}/scan", h.scanAccount)
	// Track C — Dismiss / Snooze
	mux.HandleFunc("POST /v1/dismissals", h.createDismissal)
	mux.HandleFunc("DELETE /v1/dismissals/{id}", h.revokeDismissal)
	mux.HandleFunc("GET /v1/dismissals", h.listDismissals)
}

// cors wraps a handler with CORS headers.
// CORS_ORIGIN env var sets the allowed origin (defaults to "*" for local development).
func cors(next http.Handler) http.Handler {
	origin := os.Getenv("CORS_ORIGIN")
	if origin == "" {
		origin = "*"
	}
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Access-Control-Allow-Origin", origin)
		w.Header().Set("Access-Control-Allow-Methods", "GET, POST, PATCH, DELETE, OPTIONS")
		w.Header().Set("Access-Control-Allow-Headers", "Content-Type, Authorization")
		if r.Method == http.MethodOptions {
			w.WriteHeader(http.StatusNoContent)
			return
		}
		next.ServeHTTP(w, r)
	})
}

// Handler wraps the given handler with CORS middleware.
func (h *Handler) Handler(next http.Handler) http.Handler {
	return cors(next)
}

// Optional query params:
//   - ?account_id=<id>          filter to a single account
//   - ?include_dismissed=true   include dismissed/snoozed ghosts (default: excluded)
func (h *Handler) listGhosts(w http.ResponseWriter, r *http.Request) {
	ctx := storage.WithTenantID(r.Context(), middleware.TenantID(r.Context()))
	accountID := r.URL.Query().Get("account_id")
	includeDismissed := r.URL.Query().Get("include_dismissed") == "true"

	ghosts, err := h.store.LoadGhosts(ctx)
	if err != nil {
		slog.Error("listGhosts: load failed", "error", err)
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}

	// Filter by internal_account_id if provided.
	if accountID != "" {
		filtered := make([]model.GhostResource, 0, len(ghosts))
		for _, g := range ghosts {
			if g.InternalAccountID == accountID {
				filtered = append(filtered, g)
			}
		}
		ghosts = filtered
	}

	// Enrich with dismissal state and optionally filter dismissed resources.
	ghosts, err = h.enrichWithDismissals(ctx, ghosts, accountID, includeDismissed)
	if err != nil {
		slog.Error("listGhosts: enrich dismissals failed", "error", err)
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}

	if ghosts == nil {
		ghosts = []model.GhostResource{}
	}
	writeJSON(w, ghosts)
}

// enrichWithDismissals loads active dismissals for the tenant and either annotates
// or removes dismissed/snoozed ghosts from the list.
func (h *Handler) enrichWithDismissals(ctx context.Context, ghosts []model.GhostResource, accountID string, includeDismissed bool) ([]model.GhostResource, error) {
	dismissals, err := h.store.ListActiveDismissals(ctx, accountID)
	if err != nil {
		return nil, err
	}

	// Build fingerprint → dismissal lookup.
	lookup := make(map[string]model.DismissAction, len(dismissals))
	for _, d := range dismissals {
		lookup[dismissalKey(d)] = d
	}

	if len(lookup) == 0 {
		return ghosts, nil
	}

	out := make([]model.GhostResource, 0, len(ghosts))
	for _, g := range ghosts {
		d, dismissed := lookup[ghostKey(g)]
		if dismissed {
			if !includeDismissed {
				continue // omit from default response
			}
			// Annotate with dismissal info.
			g.DismissalID = &d.ID
			g.DismissAction = d.Action
			g.DismissReason = d.Reason
			g.DismissNote = d.Note
			g.SnoozedUntil = d.SnoozedUntil
		}
		out = append(out, g)
	}
	return out, nil
}

// ghostKey returns a stable fingerprint string for a GhostResource.
func ghostKey(g model.GhostResource) string {
	return g.InternalAccountID + "|" + g.Provider + "|" + g.Service + "|" + g.Region + "|" + g.ResourceID
}

// dismissalKey returns the same fingerprint for a DismissAction.
func dismissalKey(d model.DismissAction) string {
	return d.AccountID + "|" + d.Provider + "|" + d.Service + "|" + d.Region + "|" + d.ResourceID
}

// Optional query param: ?account_id=<id> to filter to a single account.
func (h *Handler) listResources(w http.ResponseWriter, r *http.Request) {
	ctx := storage.WithTenantID(r.Context(), middleware.TenantID(r.Context()))
	accountID := r.URL.Query().Get("account_id")
	resources, err := h.store.LoadResources(ctx)
	if err != nil {
		slog.Error("listResources: load failed", "error", err)
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	
	// Filter by internal_account_id if provided
	if accountID != "" {
		filtered := make([]model.ResourceRecord, 0)
		for _, resource := range resources {
			if resource.InternalAccountID == accountID {
				filtered = append(filtered, resource)
			}
		}
		resources = filtered
	}
	
	if resources == nil {
		resources = []model.ResourceRecord{}
	}
	writeJSON(w, resources)
}

func (h *Handler) getSummary(w http.ResponseWriter, r *http.Request) {
	ctx := storage.WithTenantID(r.Context(), middleware.TenantID(r.Context()))
	accountID := r.URL.Query().Get("account_id")
	ghosts, err := h.store.LoadGhosts(ctx)
	if err != nil {
		slog.Error("getSummary: load failed", "error", err)
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	if accountID != "" {
		filtered := make([]model.GhostResource, 0, len(ghosts))
		for _, g := range ghosts {
			if g.InternalAccountID == accountID {
				filtered = append(filtered, g)
			}
		}
		ghosts = filtered
	}
	// Exclude dismissed/snoozed resources from the savings summary.
	ghosts, err = h.enrichWithDismissals(ctx, ghosts, accountID, false)
	if err != nil {
		slog.Error("getSummary: enrich dismissals failed", "error", err)
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	writeJSON(w, analyzer.Summarize(ghosts))
}

// getTrend returns ghost snapshots for the tenant, ordered oldest-first.
// Optional query params: ?account_id=<id>, ?service=<name>.
// When service is set, returns per-service data from ghost_snapshot_services.
func (h *Handler) getTrend(w http.ResponseWriter, r *http.Request) {
	ctx := storage.WithTenantID(r.Context(), middleware.TenantID(r.Context()))
	accountID := r.URL.Query().Get("account_id")
	service := r.URL.Query().Get("service")
	resourceType := r.URL.Query().Get("resource_type")

	var snaps []model.GhostSnapshot
	var err error

	if service != "" {
		snaps, err = h.store.ListSnapshotsByService(ctx, service, resourceType, accountID)
	} else {
		snaps, err = h.store.ListSnapshots(ctx, accountID)
	}
	if err != nil {
		slog.Error("getTrend: load failed", "error", err)
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	if snaps == nil {
		snaps = []model.GhostSnapshot{}
	}
	writeJSON(w, snaps)
}

// getTrendServices returns distinct services available in snapshot data.
func (h *Handler) getTrendServices(w http.ResponseWriter, r *http.Request) {
	ctx := storage.WithTenantID(r.Context(), middleware.TenantID(r.Context()))
	services, err := h.store.ListTrendServices(ctx)
	if err != nil {
		slog.Error("getTrendServices: load failed", "error", err)
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	if services == nil {
		services = []string{}
	}
	writeJSON(w, services)
}

// getTrendResourceTypes returns distinct resource types for a given service.
func (h *Handler) getTrendResourceTypes(w http.ResponseWriter, r *http.Request) {
	ctx := storage.WithTenantID(r.Context(), middleware.TenantID(r.Context()))
	service := r.URL.Query().Get("service")
	if service == "" {
		writeJSON(w, []string{})
		return
	}
	types, err := h.store.ListTrendResourceTypes(ctx, service)
	if err != nil {
		slog.Error("getTrendResourceTypes: load failed", "error", err)
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	if types == nil {
		types = []string{}
	}
	writeJSON(w, types)
}

// Pinger is satisfied by *postgres.Store (which embeds pgxpool.Pool).
type Pinger interface {
	Ping(ctx context.Context) error
}

func (h *Handler) health(w http.ResponseWriter, r *http.Request) {
	if p, ok := h.store.(Pinger); ok {
		if err := p.Ping(r.Context()); err != nil {
			slog.Error("health: db ping failed", "error", err)
			http.Error(w, `{"status":"error","db":"unreachable"}`, http.StatusServiceUnavailable)
			return
		}
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write([]byte(`{"status":"ok"}`))
}

// listAccounts returns connected accounts for the tenant (secrets masked).
func (h *Handler) listAccounts(w http.ResponseWriter, r *http.Request) {
	ctx := storage.WithTenantID(r.Context(), middleware.TenantID(r.Context()))
	accounts, err := h.store.ListAccounts(ctx)
	if err != nil {
		slog.Error("listAccounts: load failed", "error", err)
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	if accounts == nil {
		accounts = []model.Account{}
	}
	writeJSON(w, accounts)
}

func (h *Handler) getAccount(w http.ResponseWriter, r *http.Request) {
	ctx := storage.WithTenantID(r.Context(), middleware.TenantID(r.Context()))
	account, err := h.store.GetAccount(ctx, r.PathValue("id"))
	if err != nil {
		slog.Error("getAccount: load failed", "error", err)
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	writeJSON(w, account)
}

// createAccount saves a new cloud account with encrypted credentials.
func (h *Handler) createAccount(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Provider    string `json:"provider"`
		Label       string `json:"label"`
		AccessKeyID string `json:"access_key_id"`
		SecretKey   string `json:"secret_key"`
		Region      string `json:"region"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "invalid request body", http.StatusBadRequest)
		return
	}
	if req.AccessKeyID == "" || req.SecretKey == "" {
		http.Error(w, "access_key_id and secret_key are required", http.StatusBadRequest)
		return
	}
	if req.Provider == "" {
		req.Provider = "aws"
	}
	if req.Region == "" {
		req.Region = "eu-central-1"
	}

	secretEncrypted, err := crypto.Encrypt(req.SecretKey)
	if err != nil {
		http.Error(w, "encryption failed", http.StatusInternalServerError)
		return
	}

	tenantID := middleware.TenantID(r.Context())
	account := model.Account{
		ID:                uuid.New().String(),
		TenantID:          tenantID,
		Provider:          req.Provider,
		Label:             req.Label,
		AccessKeyID:       req.AccessKeyID,
		SecretEncrypted:   secretEncrypted,
		Region:            req.Region,
		Status:            "connected",
		ScanIntervalHours: 24,
		CreatedAt:         time.Now().UTC(),
	}

	ctx := storage.WithTenantID(r.Context(), tenantID)
	if err := h.store.SaveAccount(ctx, account); err != nil {
		slog.Error("createAccount: save failed", "error", err)
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}

	w.WriteHeader(http.StatusCreated)
	writeJSON(w, account)
}

// updateAccount edits the label, access_key_id, region, secret_key, and/or scan_interval_hours of an account.
// secret_key is only re-encrypted when a non-empty value is provided.
func (h *Handler) updateAccount(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	tenantID := middleware.TenantID(r.Context())
	ctx := storage.WithTenantID(r.Context(), tenantID)

	existing, err := h.store.GetAccount(ctx, id)
	if err != nil {
		http.Error(w, "account not found", http.StatusNotFound)
		return
	}

	var req struct {
		Label             *string `json:"label"`
		AccessKeyID       *string `json:"access_key_id"`
		SecretKey         *string `json:"secret_key"`
		Region            *string `json:"region"`
		ScanIntervalHours *int    `json:"scan_interval_hours"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "invalid request body", http.StatusBadRequest)
		return
	}

	if req.Label != nil {
		existing.Label = *req.Label
	}
	if req.AccessKeyID != nil {
		existing.AccessKeyID = *req.AccessKeyID
	}
	if req.SecretKey != nil && *req.SecretKey != "" {
		encrypted, err := crypto.Encrypt(*req.SecretKey)
		if err != nil {
			http.Error(w, "encryption failed", http.StatusInternalServerError)
			return
		}
		existing.SecretEncrypted = encrypted
	}
	if req.Region != nil {
		existing.Region = *req.Region
	}
	if req.ScanIntervalHours != nil {
		if *req.ScanIntervalHours < 0 {
			http.Error(w, "scan_interval_hours must be >= 0", http.StatusBadRequest)
			return
		}
		existing.ScanIntervalHours = *req.ScanIntervalHours
	}

	if err := h.store.SaveAccount(ctx, existing); err != nil {
		slog.Error("updateAccount: save failed", "error", err)
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}

	writeJSON(w, existing)
}

// deleteAccount removes a connected account.
func (h *Handler) deleteAccount(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	ctx := storage.WithTenantID(r.Context(), middleware.TenantID(r.Context()))
	if err := h.store.DeleteAccount(ctx, id); err != nil {
		slog.Error("deleteAccount: failed", "account_id", id, "error", err)
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// scanAccount triggers an ingestion run for the given account.
func (h *Handler) scanAccount(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	tenantID := middleware.TenantID(r.Context())
	ctx := storage.WithTenantID(r.Context(), tenantID)

	account, err := h.store.GetAccount(ctx, id)
	if err != nil {
		slog.Error("scanAccount: account not found", "account_id", id, "error", err)
		http.Error(w, "account not found", http.StatusNotFound)
		return
	}

	ok, err := h.store.TryMarkAccountScanning(ctx, id)
	if err != nil {
		slog.Error("scanAccount: mark scanning failed", "account_id", id, "error", err)
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	if !ok {
		http.Error(w, "scan already in progress", http.StatusConflict)
		return
	}

	job := queue.ScanJob{
		TenantID:   account.TenantID,
		AccountID:  account.ID,
		EnqueuedAt: time.Now().UTC(),
		RequestID:  middleware.RequestIDFromCtx(r.Context()),
	}
	if err := h.queue.Enqueue(ctx, job); err != nil {
		slog.Error("scan.enqueue_failed", "account_id", id, "error", err)
		_ = h.store.UpdateAccountStatus(ctx, id, "error")
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	slog.Info("scan.enqueued", "account_id", id, "tenant_id", tenantID)
	writeJSON(w, map[string]string{"status": "scanning"})
}

// createDismissal handles POST /v1/dismissals.
// Body: { account_id, provider, service, region, resource_id, action, reason, note?, snooze_until? }
func (h *Handler) createDismissal(w http.ResponseWriter, r *http.Request) {
	ctx := storage.WithTenantID(r.Context(), middleware.TenantID(r.Context()))

	var req struct {
		AccountID  string     `json:"account_id"`
		Provider   string     `json:"provider"`
		Service    string     `json:"service"`
		Region     string     `json:"region"`
		ResourceID string     `json:"resource_id"`
		Action     string     `json:"action"`
		Reason     string     `json:"reason"`
		Note       string     `json:"note"`
		SnoozedUntil *time.Time `json:"snooze_until"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "invalid request body", http.StatusBadRequest)
		return
	}

	// Validate required fields.
	if req.AccountID == "" || req.Provider == "" || req.Service == "" ||
		req.Region == "" || req.ResourceID == "" {
		http.Error(w, "account_id, provider, service, region, resource_id are required", http.StatusBadRequest)
		return
	}
	if req.Action != model.DismissActionDismiss && req.Action != model.DismissActionSnooze {
		http.Error(w, "action must be 'dismiss' or 'snooze'", http.StatusBadRequest)
		return
	}
	if !model.ValidDismissReasons[req.Reason] {
		http.Error(w, "invalid reason code", http.StatusBadRequest)
		return
	}
	if req.Reason == model.DismissReasonOther && req.Note == "" {
		http.Error(w, "note is required when reason is 'other'", http.StatusBadRequest)
		return
	}
	if req.Action == model.DismissActionSnooze {
		if req.SnoozedUntil == nil {
			http.Error(w, "snooze_until is required for snooze action", http.StatusBadRequest)
			return
		}
		if !req.SnoozedUntil.After(time.Now()) {
			http.Error(w, "snooze_until must be in the future", http.StatusBadRequest)
			return
		}
		maxSnooze := time.Now().AddDate(0, 0, model.MaxSnoozeDays)
		if req.SnoozedUntil.After(maxSnooze) {
			http.Error(w, "snooze_until must be within 90 days", http.StatusBadRequest)
			return
		}
	}

	d := model.DismissAction{
		AccountID:    req.AccountID,
		Provider:     req.Provider,
		Service:      req.Service,
		Region:       req.Region,
		ResourceID:   req.ResourceID,
		Action:       req.Action,
		Reason:       req.Reason,
		Note:         req.Note,
		SnoozedUntil: req.SnoozedUntil,
		DismissedBy:  middleware.TenantID(r.Context()), // tenant as identifier; swap for user email when available
	}

	id, err := h.store.DismissGhost(ctx, d)
	if err != nil {
		if errors.Is(err, storage.ErrAlreadyDismissed) {
			http.Error(w, "resource already has an active dismissal", http.StatusConflict)
			return
		}
		slog.Error("createDismissal: failed", "error", err)
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}

	d.ID = id
	w.WriteHeader(http.StatusCreated)
	writeJSON(w, d)
}

// revokeDismissal handles DELETE /v1/dismissals/{id}.
func (h *Handler) revokeDismissal(w http.ResponseWriter, r *http.Request) {
	ctx := storage.WithTenantID(r.Context(), middleware.TenantID(r.Context()))

	raw := r.PathValue("id")
	id, err := strconv.ParseInt(raw, 10, 64)
	if err != nil || id <= 0 {
		http.Error(w, "invalid dismissal id", http.StatusBadRequest)
		return
	}

	revokedBy := middleware.TenantID(r.Context())
	if err := h.store.RevokeDismissal(ctx, id, revokedBy); err != nil {
		slog.Error("revokeDismissal: failed", "id", id, "error", err)
		http.Error(w, "dismissal not found or already revoked", http.StatusNotFound)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// listDismissals handles GET /v1/dismissals.
// Optional query param: ?account_id=<id>
func (h *Handler) listDismissals(w http.ResponseWriter, r *http.Request) {
	ctx := storage.WithTenantID(r.Context(), middleware.TenantID(r.Context()))
	accountID := r.URL.Query().Get("account_id")

	dismissals, err := h.store.ListActiveDismissals(ctx, accountID)
	if err != nil {
		slog.Error("listDismissals: failed", "error", err)
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	if dismissals == nil {
		dismissals = []model.DismissAction{}
	}
	writeJSON(w, dismissals)
}

func writeJSON(w http.ResponseWriter, v any) {
	w.Header().Set("Content-Type", "application/json")
	if err := json.NewEncoder(w).Encode(v); err != nil {
		http.Error(w, "internal error", http.StatusInternalServerError)
	}
}
