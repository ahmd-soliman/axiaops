// Package api exposes the analysis results over HTTP.
// Endpoints:
//
//	GET    /health
//	GET    /ghosts
//	GET    /summary
//	GET    /accounts
//	POST   /accounts
//	PATCH  /accounts/{id}
//	DELETE /accounts/{id}
//	POST   /accounts/{id}/scan
package api

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"os"
	"time"

	"github.com/google/uuid"

	"axiaops.io/api/internal/middleware"
	"axiaops.io/shared/analyzer"
	"axiaops.io/shared/crypto"
	"axiaops.io/shared/model"
	"axiaops.io/shared/storage"
)

// Handler serves ghost detection results over HTTP.
type Handler struct {
	store        storage.Store
	ingestionURL string // URL of the ingestion service scan endpoint
}

// New creates a Handler backed by the given store.
// ingestionURL is the base URL of the ingestion service (e.g. http://localhost:8081).
func New(store storage.Store) *Handler {
	ingestionURL := os.Getenv("INGESTION_URL")
	if ingestionURL == "" {
		ingestionURL = "http://localhost:8081"
	}
	return &Handler{store: store, ingestionURL: ingestionURL}
}

// Register attaches the routes to the given mux.
func (h *Handler) Register(mux *http.ServeMux) {
	mux.HandleFunc("GET /health", h.health)
	mux.HandleFunc("GET /v1/ghosts", h.listGhosts)
	mux.HandleFunc("GET /v1/summary", h.getSummary)
	mux.HandleFunc("GET /v1/trend", h.getTrend)
	mux.HandleFunc("GET /v1/resources", h.listResources)
	mux.HandleFunc("GET /v1/accounts", h.listAccounts)
	mux.HandleFunc("POST /v1/accounts", h.createAccount)
	mux.HandleFunc("PATCH /v1/accounts/{id}", h.updateAccount)
	mux.HandleFunc("DELETE /v1/accounts/{id}", h.deleteAccount)
	mux.HandleFunc("POST /v1/accounts/{id}/scan", h.scanAccount)
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

func (h *Handler) listGhosts(w http.ResponseWriter, r *http.Request) {
	ctx := storage.WithTenantID(r.Context(), middleware.TenantID(r.Context()))
	ghosts, err := h.store.LoadGhosts(ctx)
	if err != nil {
		slog.Error("listGhosts: load failed", "error", err)
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	if ghosts == nil {
		ghosts = []model.GhostResource{}
	}
	writeJSON(w, ghosts)
}

func (h *Handler) listResources(w http.ResponseWriter, r *http.Request) {
	ctx := storage.WithTenantID(r.Context(), middleware.TenantID(r.Context()))
	resources, err := h.store.LoadResources(ctx)
	if err != nil {
		slog.Error("listResources: load failed", "error", err)
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	if resources == nil {
		resources = []model.ResourceRecord{}
	}
	writeJSON(w, resources)
}

func (h *Handler) getSummary(w http.ResponseWriter, r *http.Request) {
	ctx := storage.WithTenantID(r.Context(), middleware.TenantID(r.Context()))
	ghosts, err := h.store.LoadGhosts(ctx)
	if err != nil {
		slog.Error("getSummary: load failed", "error", err)
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	writeJSON(w, analyzer.Summarize(ghosts))
}

// getTrend returns ghost snapshots for the tenant, ordered oldest-first.
// Optional query param: ?account_id=<id> to filter to a single account.
func (h *Handler) getTrend(w http.ResponseWriter, r *http.Request) {
	ctx := storage.WithTenantID(r.Context(), middleware.TenantID(r.Context()))
	accountID := r.URL.Query().Get("account_id")
	snaps, err := h.store.ListSnapshots(ctx, accountID)
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
		req.Region = "us-east-1"
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

	payload, err := json.Marshal(map[string]string{
		"account_id": account.ID,
		"tenant_id":  account.TenantID,
	})
	if err != nil {
		slog.Error("scanAccount: marshal body failed", "account_id", id, "error", err)
		_ = h.store.UpdateAccountStatus(ctx, id, "error")
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}

	ingestionURL := h.ingestionURL
	// Use a detached context: the request context may be cancelled when the handler returns.
	// Cap at 15 minutes — ingestion service is expected to finish well within that.
	bg, cancel := context.WithTimeout(
		storage.WithTenantID(context.Background(), tenantID),
		15*time.Minute,
	)
	slog.Info("scan.started", "account_id", id, "tenant_id", tenantID)
	go func() {
		defer cancel()
		req, err := http.NewRequestWithContext(bg, http.MethodPost, ingestionURL+"/scan", bytes.NewReader(payload))
		if err != nil {
			slog.Error("scan.failed", "account_id", id, "error", err)
			_ = h.store.UpdateAccountStatus(context.Background(), id, "error")
			return
		}
		req.Header.Set("Content-Type", "application/json")
		start := time.Now()
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			slog.Error("scan.failed", "account_id", id, "error", err)
			_ = h.store.UpdateAccountStatus(context.Background(), id, "error")
			return
		}
		_, _ = io.Copy(io.Discard, resp.Body)
		if err := resp.Body.Close(); err != nil {
			slog.Warn("scan: failed to close response body", "error", err)
		}
		if resp.StatusCode != http.StatusOK {
			slog.Error("scan.failed", "account_id", id, "status", resp.StatusCode)
			_ = h.store.UpdateAccountStatus(context.Background(), id, "error")
			return
		}
		slog.Info("scan.completed", "account_id", id, "duration_ms", time.Since(start).Milliseconds())
		_ = h.store.UpdateAccountStatus(context.Background(), id, "connected")
	}()

	writeJSON(w, map[string]string{"status": "scanning"})
}

func writeJSON(w http.ResponseWriter, v any) {
	w.Header().Set("Content-Type", "application/json")
	if err := json.NewEncoder(w).Encode(v); err != nil {
		http.Error(w, "internal error", http.StatusInternalServerError)
	}
}
