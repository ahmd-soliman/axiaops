package sso

import (
	"encoding/json"
	"log/slog"
	"net/http"
	"strconv"
	"time"

	"axiaops.io/api/internal/auth"
	"axiaops.io/api/internal/httpip"
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
	rateLimit  *auth.IPRateLimiter // nil → no per-IP cap (dev fallback)
}

// NewDiscoverHandler constructs the handler over the given discoverer.
func NewDiscoverHandler(d Discoverer) *DiscoverHandler {
	return &DiscoverHandler{discoverer: d}
}

// WithRateLimit attaches a per-IP rate limiter (audit M-5). Pass nil to
// disable. Separate budget from /v1/auth/login so hammering /sso/discover
// can't consume the login limiter's budget.
func (h *DiscoverHandler) WithRateLimit(rl *auth.IPRateLimiter) *DiscoverHandler {
	h.rateLimit = rl
	return h
}

// ServeHTTP implements the GET /v1/sso/discover handler.
func (h *DiscoverHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	// Capture the deadline BEFORE the discoverer call so a slow DB lookup
	// can't shrink the pad below minDiscoverLatency.
	deadline := time.Now().Add(minDiscoverLatency)
	defer func() { PadUntil(deadline) }()

	// Per-IP rate limit (audit M-5). The constant-shape latency pad keeps
	// the response uninformative on a single hit, but unlimited request
	// rate still lets an attacker enumerate verified domains in bulk.
	// The cap closes that off. The latency-pad still applies on the 429
	// path so a successful hit and a rate-limited hit are indistinguishable
	// by wall-clock timing.
	if h.rateLimit != nil {
		outcome := h.rateLimit.Allow(r.Context(), httpip.Request(r))
		if !outcome.Allowed {
			retry := int(outcome.RetryAfter.Seconds())
			if retry < 1 {
				retry = 1
			}
			w.Header().Set("Retry-After", strconv.Itoa(retry))
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusTooManyRequests)
			_, _ = w.Write([]byte(`{"error":"rate_limited"}`))
			return
		}
	}

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
