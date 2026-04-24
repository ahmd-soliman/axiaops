package api

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"os"
	"strconv"
	"time"

	"github.com/google/uuid"

	"axiaops.io/api/internal/audit"
	"axiaops.io/api/internal/middleware"
	"axiaops.io/shared/analyzer"
	"axiaops.io/shared/cache"
	"axiaops.io/shared/crypto"
	"axiaops.io/shared/model"
	"axiaops.io/shared/queue"
	"axiaops.io/shared/storage"
)

// Handler serves zombie detection results over HTTP.
type Handler struct {
	store        storage.Store
	queue        queue.Queue
	ingestionURL string // used only by sync queue fallback

	// redisCache is the cache backend the readyz check pings. nil means
	// "Redis was not configured for this deployment" — readyz reports
	// "skipped" rather than treating it as a fault. Callers wire this only
	// when REDIS_URL is set; the wider request path uses cache.Cache directly
	// via middleware (auth JWKS, rate limiter), independent of this field.
	redisCache cache.Cache
}

// New creates a Handler backed by the given store and queue.
func New(store storage.Store, q queue.Queue) *Handler {
	ingestionURL := os.Getenv("INGESTION_URL")
	if ingestionURL == "" {
		ingestionURL = "http://localhost:8081"
	}
	return &Handler{store: store, queue: q, ingestionURL: ingestionURL}
}

// WithRedisCache wires the Redis-backed cache into the readyz check. Optional —
// if not called, readyz reports the Redis status as "skipped". Returns the
// receiver for fluent setup so main.go can chain `api.New(...).WithRedisCache(c)`.
func (h *Handler) WithRedisCache(c cache.Cache) *Handler {
	h.redisCache = c
	return h
}

// Register attaches the routes to the given mux.
func (h *Handler) Register(mux *http.ServeMux) {
	mux.HandleFunc("GET /health", h.health)
	mux.HandleFunc("GET /livez", h.livez)
	mux.HandleFunc("GET /readyz", h.readyz)
	mux.HandleFunc("GET /v1/zombies", h.listZombies)
	mux.HandleFunc("GET /v1/summary", h.getSummary)
	mux.HandleFunc("GET /v1/trend", h.getTrend)
	mux.HandleFunc("GET /v1/trend/services", h.getTrendServices)
	mux.HandleFunc("GET /v1/trend/resource-types", h.getTrendResourceTypes)
	mux.HandleFunc("GET /v1/costs", h.listCosts)
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
	// Audit trail
	mux.HandleFunc("GET /v1/audit", h.listAuditEvents)
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
//   - ?include_dismissed=true   include dismissed/snoozed zombies (default: excluded)
func (h *Handler) listZombies(w http.ResponseWriter, r *http.Request) {
	ctx := storage.WithTenantID(r.Context(), middleware.TenantID(r.Context()))
	accountID := r.URL.Query().Get("account_id")
	includeDismissed := r.URL.Query().Get("include_dismissed") == "true"

	zombies, err := h.store.LoadZombies(ctx)
	if err != nil {
		slog.Error("listZombies: load failed", "error", err)
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}

	// Filter by internal_account_id if provided.
	if accountID != "" {
		filtered := make([]model.ZombieResource, 0, len(zombies))
		for _, z := range zombies {
			if z.InternalAccountID == accountID {
				filtered = append(filtered, z)
			}
		}
		zombies = filtered
	}

	// Enrich with dismissal state and optionally filter dismissed resources.
	zombies, err = h.enrichWithDismissals(ctx, zombies, accountID, includeDismissed)
	if err != nil {
		slog.Error("listZombies: enrich dismissals failed", "error", err)
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}

	if zombies == nil {
		zombies = []model.ZombieResource{}
	}
	writeJSON(w, zombies)
}

// enrichWithDismissals loads active dismissals for the tenant and either annotates
// or removes dismissed/snoozed zombies from the list.
func (h *Handler) enrichWithDismissals(ctx context.Context, zombies []model.ZombieResource, accountID string, includeDismissed bool) ([]model.ZombieResource, error) {
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
		return zombies, nil
	}

	out := make([]model.ZombieResource, 0, len(zombies))
	for _, z := range zombies {
		d, dismissed := lookup[zombieKey(z)]
		if dismissed {
			if !includeDismissed {
				continue // omit from default response
			}
			// Annotate with dismissal info.
			z.DismissalID = &d.ID
			z.DismissAction = d.Action
			z.DismissReason = d.Reason
			z.DismissNote = d.Note
			z.SnoozedUntil = d.SnoozedUntil
		}
		out = append(out, z)
	}
	return out, nil
}

// zombieKey returns a stable fingerprint string for a ZombieResource.
func zombieKey(z model.ZombieResource) string {
	return z.InternalAccountID + "|" + z.Provider + "|" + z.Service + "|" + z.Region + "|" + z.ResourceID
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
	zombies, err := h.store.LoadZombies(ctx)
	if err != nil {
		slog.Error("getSummary: load failed", "error", err)
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	if accountID != "" {
		filtered := make([]model.ZombieResource, 0, len(zombies))
		for _, z := range zombies {
			if z.InternalAccountID == accountID {
				filtered = append(filtered, z)
			}
		}
		zombies = filtered
	}
	// Exclude dismissed/snoozed resources from the savings summary.
	zombies, err = h.enrichWithDismissals(ctx, zombies, accountID, false)
	if err != nil {
		slog.Error("getSummary: enrich dismissals failed", "error", err)
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	writeJSON(w, analyzer.Summarize(zombies))
}

// getTrend returns zombie snapshots for the tenant, ordered oldest-first.
// Optional query params: ?account_id=<id>, ?service=<name>.
// When service is set, returns per-service data from zombie_snapshot_services.
func (h *Handler) getTrend(w http.ResponseWriter, r *http.Request) {
	ctx := storage.WithTenantID(r.Context(), middleware.TenantID(r.Context()))
	accountID := r.URL.Query().Get("account_id")
	service := r.URL.Query().Get("service")
	resourceType := r.URL.Query().Get("resource_type")

	var snaps []model.ZombieSnapshot
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
		snaps = []model.ZombieSnapshot{}
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

// listCosts returns cost records for the tenant, filtered by account, service, and time window.
// Optional query params: ?account_id=<id>, ?service=<name>, ?days=<int> (default 30).
// account_id can be either the internal AxiaOps account UUID or the AWS account ID.
func (h *Handler) listCosts(w http.ResponseWriter, r *http.Request) {
	ctx := storage.WithTenantID(r.Context(), middleware.TenantID(r.Context()))
	days, _ := strconv.Atoi(r.URL.Query().Get("days"))

	accountIDParam := r.URL.Query().Get("account_id")
	filter := storage.CostFilter{
		Service: r.URL.Query().Get("service"),
		Days:    days,
	}

	// account_id parameter can be either the internal UUID or the AWS account ID.
	// Strategy:
	// 1. If it looks like a UUID, try to look it up as internal account ID first
	// 2. If found, use both internal_account_id and account_id for filtering
	// 3. If not found or looks like AWS ID, use as account_id filter
	if accountIDParam != "" {
		// Try to look it up as an internal account ID (UUID)
		account, err := h.store.GetAccount(ctx, accountIDParam)
		if err == nil {
			// Found by internal UUID
			filter.InternalAccountID = accountIDParam
			if account.AccountID != "" {
				filter.AWSAccountID = account.AccountID
			}
			slog.Info("listCosts: filtered by internal account UUID", "internal_id", accountIDParam, "aws_id", account.AccountID)
		} else {
			// Account not found - it could be:
			// 1. An internal UUID that hasn't been added to accounts table yet (newly created)
			// 2. An AWS account ID
			// Try both approaches:
			filter.InternalAccountID = accountIDParam  // Try as internal UUID
			filter.AWSAccountID = accountIDParam       // Also try as AWS account ID
			slog.Info("listCosts: filtered by parameter (either internal UUID or AWS ID)", "account_id", accountIDParam)
		}
	}

	records, err := h.store.ListCostRecords(ctx, filter)
	if err != nil {
		slog.Error("listCosts: load failed", "error", err)
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	if records == nil {
		records = []model.CostRecord{}
	}
	writeJSON(w, records)
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

// livez answers the liveness question: "is this process responsive enough to
// take traffic?" Always returns 200 unless the Go runtime is so wedged it
// cannot reply at all (in which case the request times out and the orchestrator
// kills the instance — that's the only failure mode for this endpoint).
//
// No DB ping, no Redis ping, no cross-service check. App Runner / k8s should
// wire their *instance* health probe to this so a transient DB blip doesn't
// trigger pod restarts. Deep dependency checks belong in /readyz.
func (h *Handler) livez(w http.ResponseWriter, _ *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write([]byte(`{"status":"ok"}`))
}

// readyz answers the readiness question: "should the load balancer route
// requests to me right now?" Pings PostgreSQL (required — we can't serve
// anything without it) and reports Redis status (informational — degraded but
// not blocking, since the cache/queue/rate-limiter all have in-memory
// fallbacks).
//
// Returns 503 only when PostgreSQL is unreachable. Redis being down keeps the
// status code at 200 with the body field set to "unreachable" — pulling the
// instance out of rotation for a degraded mode would be worse than the
// degradation itself. Monitoring can alert on the body field.
//
// Deliberately does NOT check ingestion. The API serves all read endpoints
// from PostgreSQL alone; ingestion-down means scans are delayed, not that
// requests should stop being routed here. Once ingestion moves to Lambda
// (Phase 4) "is ingestion reachable" stops being a meaningful question
// anyway — the answer is in CloudWatch metrics, not HTTP.
func (h *Handler) readyz(w http.ResponseWriter, r *http.Request) {
	// Cap the deep check so a slow Postgres or Redis can't tie up readyz
	// past what App Runner's check timeout will tolerate.
	ctx, cancel := context.WithTimeout(r.Context(), 1500*time.Millisecond)
	defer cancel()

	body := map[string]string{
		"status": "ok",
		"db":     "ok",
		"redis":  "skipped",
	}
	overallOK := true

	if p, ok := h.store.(Pinger); ok {
		if err := p.Ping(ctx); err != nil {
			slog.Error("readyz: db ping failed", "error", err)
			body["db"] = "unreachable"
			body["status"] = "error"
			overallOK = false
		}
	}

	if h.redisCache != nil {
		body["redis"] = "ok"
		if err := h.redisCache.Ping(ctx); err != nil {
			slog.Warn("readyz: redis ping failed", "error", err)
			body["redis"] = "unreachable"
			// Status stays "ok" if DB is fine — degraded but serving.
			if overallOK {
				body["status"] = "degraded"
			}
		}
	}

	w.Header().Set("Content-Type", "application/json")
	if !overallOK {
		w.WriteHeader(http.StatusServiceUnavailable)
	} else {
		w.WriteHeader(http.StatusOK)
	}
	_ = json.NewEncoder(w).Encode(body)
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
		http.Error(w, "account not found", http.StatusNotFound)
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

	audit.Record(r, h.store, model.AuditEvent{
		Action:       model.AuditActionAccountConnected,
		ResourceType: "account",
		ResourceID:   account.ID,
		Metadata: map[string]any{
			"provider": account.Provider,
			"label":    account.Label,
			"region":   account.Region,
		},
	})

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

	// Record which field names were changed — not the values, since old/new
	// secret_key must never leak into the audit table. Note req != nil checks
	// are what the handler already used above to decide which columns to update.
	changed := make([]string, 0, 5)
	if req.Label != nil {
		changed = append(changed, "label")
	}
	if req.AccessKeyID != nil {
		changed = append(changed, "access_key_id")
	}
	if req.SecretKey != nil && *req.SecretKey != "" {
		changed = append(changed, "secret_key")
	}
	if req.Region != nil {
		changed = append(changed, "region")
	}
	if req.ScanIntervalHours != nil {
		changed = append(changed, "scan_interval_hours")
	}
	audit.Record(r, h.store, model.AuditEvent{
		Action:       model.AuditActionAccountUpdated,
		ResourceType: "account",
		ResourceID:   existing.ID,
		Metadata: map[string]any{
			"fields_changed": changed,
		},
	})

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

	audit.Record(r, h.store, model.AuditEvent{
		Action:       model.AuditActionAccountDeleted,
		ResourceType: "account",
		ResourceID:   id,
	})

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

	audit.Record(r, h.store, model.AuditEvent{
		Action:       model.AuditActionScanTriggered,
		ResourceType: "account",
		ResourceID:   id,
		Metadata: map[string]any{
			"account_label": account.Label,
			"region":        account.Region,
			"on_demand":     true,
		},
	})

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
		DismissedBy:  dismissActor(r.Context()),
	}

	id, err := h.store.DismissZombie(ctx, d)
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

	action := model.AuditActionDismissZombie
	if d.Action == model.DismissActionSnooze {
		action = model.AuditActionSnoozeZombie
	}
	audit.Record(r, h.store, model.AuditEvent{
		Action:       action,
		ResourceType: "dismissal",
		ResourceID:   strconv.FormatInt(id, 10),
		Reason:       d.Reason,
		Metadata: map[string]any{
			"provider":      d.Provider,
			"service":       d.Service,
			"region":        d.Region,
			"resource_id":   d.ResourceID,
			"note":          d.Note,
			"snoozed_until": d.SnoozedUntil,
		},
	})

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

	revokedBy := dismissActor(r.Context())
	if err := h.store.RevokeDismissal(ctx, id, revokedBy); err != nil {
		slog.Error("revokeDismissal: failed", "id", id, "error", err)
		http.Error(w, "dismissal not found or already revoked", http.StatusNotFound)
		return
	}

	audit.Record(r, h.store, model.AuditEvent{
		Action:       model.AuditActionRevokeDismissal,
		ResourceType: "dismissal",
		ResourceID:   strconv.FormatInt(id, 10),
	})

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

// listAuditEvents handles GET /v1/audit.
// Query params (all optional): user_id, resource_type, resource_id, action,
// since (RFC3339), until (RFC3339), limit (1..500, default 50), cursor
// (opaque token from a previous response's next_cursor).
// Response: { "events": [...], "next_cursor": "<base64>" | "" }.
func (h *Handler) listAuditEvents(w http.ResponseWriter, r *http.Request) {
	ctx := storage.WithTenantID(r.Context(), middleware.TenantID(r.Context()))

	q := r.URL.Query()
	filter := model.AuditFilter{
		UserID:       q.Get("user_id"),
		ResourceType: q.Get("resource_type"),
		ResourceID:   q.Get("resource_id"),
		Action:       q.Get("action"),
		Limit:        50,
	}
	if filter.Action != "" && !model.ValidAuditActions[filter.Action] {
		http.Error(w, "invalid action", http.StatusBadRequest)
		return
	}
	if raw := q.Get("since"); raw != "" {
		t, err := time.Parse(time.RFC3339, raw)
		if err != nil {
			http.Error(w, "since must be RFC3339 timestamp", http.StatusBadRequest)
			return
		}
		filter.Since = t
	}
	if raw := q.Get("until"); raw != "" {
		t, err := time.Parse(time.RFC3339, raw)
		if err != nil {
			http.Error(w, "until must be RFC3339 timestamp", http.StatusBadRequest)
			return
		}
		filter.Until = t
	}
	if raw := q.Get("limit"); raw != "" {
		n, err := strconv.Atoi(raw)
		if err != nil || n < 1 || n > 500 {
			http.Error(w, "limit must be between 1 and 500", http.StatusBadRequest)
			return
		}
		filter.Limit = n
	}
	if raw := q.Get("cursor"); raw != "" {
		cur, err := decodeAuditCursor(raw)
		if err != nil {
			http.Error(w, "invalid cursor", http.StatusBadRequest)
			return
		}
		filter.Cursor = cur
	}

	events, err := h.store.AuditLogList(ctx, filter)
	if err != nil {
		slog.Error("listAuditEvents: failed", "error", err)
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}

	resp := struct {
		Events     []model.AuditEvent `json:"events"`
		NextCursor string             `json:"next_cursor,omitempty"`
	}{Events: events}
	if len(events) == filter.Limit {
		// There may be more — encode the last row as the cursor. When the caller
		// reaches the true end they'll get a short page and next_cursor == "".
		last := events[len(events)-1]
		resp.NextCursor = encodeAuditCursor(model.AuditCursor{
			CreatedAt: last.CreatedAt,
			ID:        last.ID,
		})
	}
	if resp.Events == nil {
		resp.Events = []model.AuditEvent{}
	}
	writeJSON(w, resp)
}

// encodeAuditCursor / decodeAuditCursor turn an (created_at, id) pair into
// an opaque base64 JSON token. Treating it as opaque leaves room to change the
// pagination key in the future without breaking clients that round-trip it.
func encodeAuditCursor(c model.AuditCursor) string {
	b, err := json.Marshal(c)
	if err != nil {
		// Unreachable today — AuditCursor holds only a time.Time and an int64.
		// If a future change to the struct breaks this, a log entry is cheaper
		// than discovering the regression via silent "end of results" responses.
		slog.Error("audit: encode cursor failed", "error", err)
		return ""
	}
	return base64.RawURLEncoding.EncodeToString(b)
}

func decodeAuditCursor(s string) (model.AuditCursor, error) {
	b, err := base64.RawURLEncoding.DecodeString(s)
	if err != nil {
		return model.AuditCursor{}, err
	}
	var c model.AuditCursor
	if err := json.Unmarshal(b, &c); err != nil {
		return model.AuditCursor{}, err
	}
	return c, nil
}

// dismissActor returns the identifier stored in dismissed_by / revoked_by.
// Prefers the stable user id (immutable UUID) so the stored value doesn't drift
// when a user's email changes in Kinde. Falls back to email when the user id is
// unavailable (e.g. Auth.Wrap with store=nil in tests), and finally to tenant
// id so rows are never written with "".
func dismissActor(ctx context.Context) string {
	if id := middleware.UserID(ctx); id != "" {
		return id
	}
	if email := middleware.UserEmail(ctx); email != "" {
		return email
	}
	return middleware.TenantID(ctx)
}

func writeJSON(w http.ResponseWriter, v any) {
	w.Header().Set("Content-Type", "application/json")
	if err := json.NewEncoder(w).Encode(v); err != nil {
		http.Error(w, "internal error", http.StatusInternalServerError)
	}
}
