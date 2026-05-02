package sso

import (
	"encoding/json"
	"log/slog"
	"net/http"
	"time"
)

// DiscoverHandler is the pre-auth GET /v1/sso/discover endpoint. It accepts
// an `?email=` query parameter and returns DiscoverResult JSON. The response
// shape is constant whether or not the domain is claimed — the dashboard
// reveals the password field when has_sso=false.
//
// Public path (registered via mux.HandleFunc, NOT behind require()) — added
// to middleware/auth.go publicPath() so the auth middleware doesn't gate it.
type DiscoverHandler struct {
	discoverer Discoverer
}

// NewDiscoverHandler constructs the handler over the given discoverer.
func NewDiscoverHandler(d Discoverer) *DiscoverHandler {
	return &DiscoverHandler{discoverer: d}
}

// ServeHTTP implements the GET /v1/sso/discover handler.
func (h *DiscoverHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	// Capture the deadline BEFORE the discoverer call so a slow DB lookup
	// can't shrink the pad below minDiscoverLatency.
	deadline := time.Now().Add(minDiscoverLatency)
	defer func() { PadUntil(deadline) }()

	email := r.URL.Query().Get("email")
	// We deliberately don't 400 on empty/malformed email — the constant-shape
	// requirement extends to bad input. The discoverer normalises and returns
	// HasSSO=false on anything it can't parse.

	res, err := h.discoverer.Discover(r.Context(), email)
	if err != nil {
		// NativeDiscoverer collapses ErrSSODomainNotFound to (result, nil),
		// so any non-nil error here is a real DB issue. Log it but still
		// return the constant-shape no-SSO response so a transient blip
		// doesn't break the login UX.
		slog.Warn("sso: discover error", "error", err, "email_domain", emailDomain(email))
	}

	w.Header().Set("Content-Type", "application/json")
	if encErr := json.NewEncoder(w).Encode(res); encErr != nil {
		// Body already partially written; just log.
		slog.Warn("sso: discover encode failed", "error", encErr)
	}
}
