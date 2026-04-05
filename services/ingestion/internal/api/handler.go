// Package api exposes the analysis results over HTTP.
// Endpoints:
//   GET  /ghosts   — list of all detected zombie resources
//   GET  /summary  — aggregate savings figure and per-service breakdown
//   POST /ingest   — trigger a fresh ingestion and analysis run
package api

import (
	"encoding/json"
	"net/http"
	"sync"

	"axiaops.io/ingestion/internal/analyzer"
	"axiaops.io/ingestion/internal/model"
)

// IngestFunc is called by the POST /ingest endpoint to trigger a fresh
// ingestion and analysis run. It returns the updated ghosts and summary.
type IngestFunc func() ([]model.GhostResource, analyzer.Summary, error)

// Handler holds analysis results and serves them over HTTP.
// Results are protected by a mutex so POST /ingest can update them safely
// while GET /ghosts and GET /summary are being served.
type Handler struct {
	mu      sync.RWMutex
	ghosts  []model.GhostResource
	summary analyzer.Summary
	ingest  IngestFunc
}

// New creates a Handler from the results of a completed analysis run.
// ingest is called on POST /ingest to refresh the results.
func New(ghosts []model.GhostResource, summary analyzer.Summary, ingest IngestFunc) *Handler {
	return &Handler{ghosts: ghosts, summary: summary, ingest: ingest}
}

// Register attaches the routes to the given mux.
func (h *Handler) Register(mux *http.ServeMux) {
	mux.HandleFunc("GET /health", h.health)
	mux.HandleFunc("GET /ghosts", h.listGhosts)
	mux.HandleFunc("GET /summary", h.getSummary)
	mux.HandleFunc("POST /ingest", h.triggerIngest)
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
	h.mu.RLock()
	ghosts := h.ghosts
	h.mu.RUnlock()
	writeJSON(w, ghosts)
}

func (h *Handler) getSummary(w http.ResponseWriter, r *http.Request) {
	h.mu.RLock()
	summary := h.summary
	h.mu.RUnlock()
	writeJSON(w, summary)
}

func (h *Handler) triggerIngest(w http.ResponseWriter, r *http.Request) {
	ghosts, summary, err := h.ingest()
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	h.mu.Lock()
	h.ghosts = ghosts
	h.summary = summary
	h.mu.Unlock()
	writeJSON(w, summary)
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
