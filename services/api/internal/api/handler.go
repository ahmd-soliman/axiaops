// Package api exposes the analysis results over HTTP.
// Endpoints:
//   GET  /ghosts   — list of all detected zombie resources
//   GET  /summary  — aggregate savings figure and per-service breakdown
//   POST /ingest   — trigger a fresh ingestion and analysis run
package api

import (
	"encoding/json"
	"net/http"

	"axiaops.io/api/internal/middleware"
	"axiaops.io/shared/analyzer"
	"axiaops.io/shared/model"
	"axiaops.io/shared/storage"
)

// Handler serves ghost detection results over HTTP.
// Ghosts are loaded per-request from the store so RLS filters by tenant.
type Handler struct {
	store storage.Store
}

// New creates a Handler backed by the given store.
func New(store storage.Store) *Handler {
	return &Handler{store: store}
}

// Register attaches the routes to the given mux.
func (h *Handler) Register(mux *http.ServeMux) {
	mux.HandleFunc("GET /health", h.health)
	mux.HandleFunc("GET /me", h.getMe)
	mux.HandleFunc("GET /ghosts", h.listGhosts)
	mux.HandleFunc("GET /summary", h.getSummary)
}

// cors wraps a handler with permissive CORS headers for local development.
func cors(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Access-Control-Allow-Origin", "*")
		w.Header().Set("Access-Control-Allow-Methods", "GET, POST, OPTIONS")
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
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	if ghosts == nil {
		ghosts = []model.GhostResource{}
	}
	writeJSON(w, ghosts)
}

func (h *Handler) getSummary(w http.ResponseWriter, r *http.Request) {
	ctx := storage.WithTenantID(r.Context(), middleware.TenantID(r.Context()))
	ghosts, err := h.store.LoadGhosts(ctx)
	if err != nil {
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	writeJSON(w, analyzer.Summarize(ghosts))
}

func (h *Handler) getMe(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, map[string]string{
		"org_name": middleware.TenantName(r.Context()),
	})
}

func (h *Handler) health(w http.ResponseWriter, r *http.Request) {
	w.WriteHeader(http.StatusOK)
}

func writeJSON(w http.ResponseWriter, v any) {
	w.Header().Set("Content-Type", "application/json")
	if err := json.NewEncoder(w).Encode(v); err != nil {
		http.Error(w, "internal error", http.StatusInternalServerError)
	}
}
