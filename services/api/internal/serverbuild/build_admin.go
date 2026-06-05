package serverbuild

import (
	"context"
	"fmt"
	"log/slog"
	"net/http"
	"strconv"
	"time"

	"axiaops.io/api/internal/auth"
	"axiaops.io/api/internal/middleware"
	"axiaops.io/api/internal/staff"
	"axiaops.io/shared/cache"
	"axiaops.io/shared/observability"
	"axiaops.io/shared/storage"
)

// adminLoginRateLimitDefault is the per-IP-per-minute /admin/auth/login cap
// when AdminConfig.LoginRateLimitPerIP is unset. Tighter than the tenant
// login default — the staff surface has a small, known user set.
const adminLoginRateLimitDefault = 10

// newAdminLoginRateLimiter builds the login limiter under a distinct key
// prefix so its budget never overlaps the tenant auth limiters.
func newAdminLoginRateLimiter(c cache.Cache, perIPPerMin int) *auth.IPRateLimiter {
	if perIPPerMin <= 0 {
		perIPPerMin = adminLoginRateLimitDefault
	}
	return auth.NewIPRateLimiter(c, "admin:login", perIPPerMin)
}

// AdminConfig bundles the runtime knobs ComposeAdminServer needs. The admin
// plane is deliberately smaller than the tenant Config — no DevMode bypass (a
// cross-tenant plane must always require real staff auth), no SSO ceremony, no
// rate-limit-on-every-request (only the login endpoint is throttled).
type AdminConfig struct {
	// Addr is the HTTP listen address. ":8090" if empty.
	Addr string
	// StaffSessionTTL bounds how long a staff session stays valid. 0 → 8h.
	StaffSessionTTL time.Duration
	// LoginRateLimitPerIP caps /admin/auth/login attempts per IP per minute.
	// 0 → a sane default inside the limiter.
	LoginRateLimitPerIP int
	// CORSOrigin, when set, is reflected for credentialed admin-UI requests
	// from a different origin (local Vite dev). Empty → same-origin only.
	CORSOrigin string
}

// AdminDeps bundles the concrete services ComposeAdminServer plugs together.
type AdminDeps struct {
	// Store is the full data-access seam (the staff handler uses the
	// StaffStore subset; the health check uses Ping if available).
	Store storage.Store
	// Cache backs staff sessions + the login rate-limiter. Production: Redis;
	// dev: in-memory. Required (NewSessionManager needs it).
	Cache cache.Cache
	// StaffProvider authenticates admin-plane requests. Required.
	StaffProvider staff.Provider
	// StaffSessions mints/revokes staff sessions (shared with StaffProvider).
	// Required.
	StaffSessions *staff.SessionManager
	// MetricsRegistry — caller-supplied so two compose calls in one process
	// don't fight over MustRegister. nil → NewDefaultMetrics.
	MetricsRegistry *Metrics
}

// ComposeAdminServer builds the http.Handler for the platform admin plane
// (cmd/api-admin). It is a deliberately minimal sibling of ComposeServer:
// staff auth instead of tenant auth, no SSO, no DevMode. Returns an error iff
// a required Dep is missing — fail fast at boot.
//
// Middleware chain, outermost-first:
//
//	CORS (optional)
//	  → request-logging + metrics
//	    → request-id
//	      → WrapStaff (auth; public paths bypass)
//	        → mux (handlers)
func ComposeAdminServer(cfg AdminConfig, deps AdminDeps) (http.Handler, error) {
	if deps.Store == nil {
		return nil, fmt.Errorf("serverbuild: AdminDeps.Store is required")
	}
	if deps.Cache == nil {
		return nil, fmt.Errorf("serverbuild: AdminDeps.Cache is required")
	}
	if deps.StaffProvider == nil {
		return nil, fmt.Errorf("serverbuild: AdminDeps.StaffProvider is required")
	}
	if deps.StaffSessions == nil {
		return nil, fmt.Errorf("serverbuild: AdminDeps.StaffSessions is required")
	}

	mux := http.NewServeMux()

	// Infra endpoints (publicAdminPath bypasses staff auth for these).
	mux.HandleFunc("GET /livez", func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("ok"))
	})
	mux.HandleFunc("GET /readyz", func(w http.ResponseWriter, r *http.Request) {
		ctx, cancel := context.WithTimeout(r.Context(), 2*time.Second)
		defer cancel()
		if p, ok := deps.Store.(interface{ Ping(context.Context) error }); ok {
			if err := p.Ping(ctx); err != nil {
				http.Error(w, "db unreachable", http.StatusServiceUnavailable)
				return
			}
		}
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("ok"))
	})
	mux.Handle("/metrics", observability.MetricsHandler())

	// Staff handler — login rate-limit shares the IPRateLimiter machinery with
	// the tenant auth handlers, under a distinct key prefix so the budgets
	// never overlap.
	loginRate := newAdminLoginRateLimiter(deps.Cache, cfg.LoginRateLimitPerIP)
	staffH := staff.NewHandler(deps.Store, deps.StaffSessions, deps.StaffProvider, loginRate)
	staffH.Register(mux)

	// ── Middleware chain ──────────────────────────────────────────────────
	root := staff.WrapStaff(deps.StaffProvider, mux)
	withReqID := middleware.RequestID(root)

	metrics := deps.MetricsRegistry
	if metrics == nil {
		metrics = NewDefaultMetrics()
	}
	logged := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		rw := &statusWriter{ResponseWriter: w, code: 200}
		withReqID.ServeHTTP(rw, r)

		duration := time.Since(start).Seconds()
		_, route := mux.Handler(r)
		status := strconv.Itoa(rw.code)
		metrics.RequestsTotal.WithLabelValues(r.Method, route, status).Inc()
		metrics.RequestDurationSeconds.WithLabelValues(r.Method, route).Observe(duration)

		slog.Info("admin: request",
			"method", r.Method, "path", r.URL.Path, "route", route,
			"status", rw.code, "duration_ms", fmt.Sprintf("%.1f", duration*1000),
			"request_id", middleware.RequestIDFromCtx(r.Context()),
		)
	})

	return adminCORS(cfg.CORSOrigin, logged), nil
}

// adminCORS is a minimal credentialed-CORS layer. With no origin configured it
// passes through (same-origin admin UI). With one set it reflects that exact
// origin + allows credentials so the staff cookie round-trips from a separate
// dev origin.
func adminCORS(origin string, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if origin != "" && r.Header.Get("Origin") == origin {
			w.Header().Set("Access-Control-Allow-Origin", origin)
			w.Header().Set("Access-Control-Allow-Credentials", "true")
			w.Header().Set("Access-Control-Allow-Methods", "GET, POST, DELETE, OPTIONS")
			w.Header().Set("Access-Control-Allow-Headers", "Content-Type")
			w.Header().Set("Vary", "Origin")
		}
		if r.Method == http.MethodOptions {
			w.WriteHeader(http.StatusNoContent)
			return
		}
		next.ServeHTTP(w, r)
	})
}
