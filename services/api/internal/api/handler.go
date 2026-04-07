// Package api exposes the analysis results over HTTP.
// Endpoints:
//   GET    /health
//   GET    /ghosts
//   GET    /summary
//   GET    /accounts
//   POST   /accounts
//   PATCH  /accounts/{id}
//   DELETE /accounts/{id}
//   POST   /accounts/{id}/scan
package api

import (
	"encoding/json"
	"log"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/google/uuid"

	"axiaops.io/shared/crypto"
	"axiaops.io/api/internal/middleware"
	"axiaops.io/shared/analyzer"
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
	mux.HandleFunc("GET /ghosts", h.listGhosts)
	mux.HandleFunc("GET /summary", h.getSummary)
	mux.HandleFunc("GET /resources", h.listResources)
	mux.HandleFunc("GET /accounts", h.listAccounts)
	mux.HandleFunc("POST /accounts", h.createAccount)
	mux.HandleFunc("PATCH /accounts/{id}", h.updateAccount)
	mux.HandleFunc("DELETE /accounts/{id}", h.deleteAccount)
	mux.HandleFunc("POST /accounts/{id}/scan", h.scanAccount)
}

// cors wraps a handler with permissive CORS headers for local development.
func cors(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Access-Control-Allow-Origin", "*")
		w.Header().Set("Access-Control-Allow-Methods", "GET, POST, PATCH, DELETE, OPTIONS")
		w.Header().Set("Access-Control-Allow-Headers", "Content-Type, Authorization")
		if r.Method == http.MethodOptions {
			w.WriteHeader(http.StatusNoContent)
			return
		}
		next.ServeHTTP(w, r)
	})
}

// Handler returns the mux wrapped with CORS middleware.
func (h *Handler) Handler(mux *http.ServeMux) http.Handler {
	return cors(mux)
}

func (h *Handler) listGhosts(w http.ResponseWriter, r *http.Request) {
	ctx := storage.WithTenantID(r.Context(), middleware.TenantID(r.Context()))
	ghosts, err := h.store.LoadGhosts(ctx)
	if err != nil {
		log.Printf("listGhosts error: %v", err)
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
		log.Printf("listResources error: %v", err)
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
		log.Printf("getSummary error: %v", err)
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	writeJSON(w, analyzer.Summarize(ghosts))
}

func (h *Handler) health(w http.ResponseWriter, r *http.Request) {
	w.WriteHeader(http.StatusOK)
}

// listAccounts returns connected accounts for the tenant (secrets masked).
func (h *Handler) listAccounts(w http.ResponseWriter, r *http.Request) {
	ctx := storage.WithTenantID(r.Context(), middleware.TenantID(r.Context()))
	accounts, err := h.store.ListAccounts(ctx)
	if err != nil {
		log.Printf("listAccounts error: %v", err)
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
		ID:              uuid.New().String(),
		TenantID:        tenantID,
		Provider:        req.Provider,
		Label:           req.Label,
		AccessKeyID:     req.AccessKeyID,
		SecretEncrypted: secretEncrypted,
		Region:          req.Region,
		Status:          "connected",
		CreatedAt:       time.Now().UTC(),
	}

	ctx := storage.WithTenantID(r.Context(), tenantID)
	if err := h.store.SaveAccount(ctx, account); err != nil {
		log.Printf("createAccount error: %v", err)
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}

	w.WriteHeader(http.StatusCreated)
	writeJSON(w, account)
}

// updateAccount edits the label, access_key_id, region, and/or secret_key of an account.
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
		Label       *string `json:"label"`
		AccessKeyID *string `json:"access_key_id"`
		SecretKey   *string `json:"secret_key"`
		Region      *string `json:"region"`
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

	if err := h.store.SaveAccount(ctx, existing); err != nil {
		log.Printf("updateAccount error: %v", err)
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
		log.Printf("deleteAccount: failed to delete %s: %v", id, err)
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
		log.Printf("scanAccount: account %s not found: %v", id, err)
		http.Error(w, "account not found", http.StatusNotFound)
		return
	}

	if err := h.store.UpdateAccountStatus(ctx, id, "scanning"); err != nil {
		log.Printf("scanAccount: update status failed for %s: %v", id, err)
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}

	// Trigger ingestion asynchronously.
	go func() {
		body := strings.NewReader(`{"account_id":"` + account.ID + `","tenant_id":"` + account.TenantID + `"}`)
		resp, err := http.Post(h.ingestionURL+"/scan", "application/json", body)
		if err != nil || resp.StatusCode != http.StatusOK {
			log.Printf("scanAccount: ingestion request failed for %s: %v", id, err)
			_ = h.store.UpdateAccountStatus(ctx, id, "error")
			return
		}
		_ = h.store.UpdateAccountStatus(ctx, id, "connected")
	}()

	writeJSON(w, map[string]string{"status": "scanning"})
}

func writeJSON(w http.ResponseWriter, v any) {
	w.Header().Set("Content-Type", "application/json")
	if err := json.NewEncoder(w).Encode(v); err != nil {
		http.Error(w, "internal error", http.StatusInternalServerError)
	}
}
