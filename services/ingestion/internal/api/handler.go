// Package api exposes the analysis results over HTTP.
// Two endpoints are provided:
//   GET /ghosts   — list of all detected zombie resources
//   GET /summary  — aggregate savings figure and per-service breakdown
package api

import (
	"encoding/json"
	"net/http"

	"axiaops.io/ingestion/internal/analyzer"
	"axiaops.io/ingestion/internal/model"
)

// Handler holds pre-computed analysis results and serves them over HTTP.
type Handler struct {
	ghosts  []model.GhostResource
	summary analyzer.Summary
}

// New creates a Handler from the results of a completed analysis run.
func New(ghosts []model.GhostResource, summary analyzer.Summary) *Handler {
	return &Handler{ghosts: ghosts, summary: summary}
}

// Register attaches the routes to the given mux.
func (h *Handler) Register(mux *http.ServeMux) {
	mux.HandleFunc("GET /ghosts", h.listGhosts)
	mux.HandleFunc("GET /summary", h.getSummary)
}

func (h *Handler) listGhosts(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, h.ghosts)
}

func (h *Handler) getSummary(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, h.summary)
}

func writeJSON(w http.ResponseWriter, v any) {
	w.Header().Set("Content-Type", "application/json")
	if err := json.NewEncoder(w).Encode(v); err != nil {
		http.Error(w, "internal error", http.StatusInternalServerError)
	}
}
