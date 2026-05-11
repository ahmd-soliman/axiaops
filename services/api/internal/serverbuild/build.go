// Package serverbuild centralises the AxiaOps API server's wiring — handler
// construction, middleware composition, ticker startup. The composition root
// (cmd/main.go) reads env vars, builds Deps, calls ComposeServer, and runs.
//
// Plan §4.8.3 / D11 / architect S9: the seam that lets a SaaS-hosted variant
// reactivate later by writing a second composition root (cmd/api-saashosted/
// main.go) that swaps a few constructors. Every dependency that would
// diverge between self-hosted and a hypothetical SaaS reactivation crosses
// the Deps boundary as an interface — so neither the handler/business-logic
// layer nor this package needs to change.
//
// Four SaaS-extension seams cross Deps:
//   - storage.Store    — already an interface; concrete impl is postgres
//   - auth.Provider    — pluggable auth (today: native cookie sessions only;
//                        the interface stays so a SaaS reactivation can
//                        swap in a remote-IdP impl without touching the
//                        rest of the chain)
//   - sso.Discoverer   — pre-auth domain → connection lookup (B2)
//   - sso.Connector    — connection CRUD with discovery-doc validation (B2)
//
// The smoke test in build_test.go boots ComposeServer with mock impls of
// all four seams and proves the surface holds (plan §4.8.6 acceptance).
package serverbuild

import (
	"context"
	"fmt"
	"log/slog"
	"net/http"
	"strconv"
	"time"

	"github.com/prometheus/client_golang/prometheus"

	"axiaops.io/api/internal/api"
	"axiaops.io/api/internal/auth"
	"axiaops.io/api/internal/middleware"
	"axiaops.io/api/internal/sso"
	"axiaops.io/shared/cache"
	"axiaops.io/shared/observability"
	"axiaops.io/shared/queue"
	"axiaops.io/shared/storage"
)

// Config bundles the runtime knobs ComposeServer needs that aren't already
// captured by Deps. Composition roots fill these from env vars (or test
// defaults). Every field is intentionally simple — strings, durations,
// booleans — so the smoke test can construct a Config inline.
type Config struct {
	// Addr is the HTTP listen address. ":8080" if empty.
	Addr string
	// PublicHost is the externally-reachable origin (e.g. https://app.example.com).
	// Used for OOB redemption URLs and the OIDC redirect_uri the IdP must agree on.
	// May be empty in dev.
	PublicHost string

	// DevMode disables auth + uses the dev bypass middleware. When true,
	// native-auth handlers (login/bootstrap/OIDC ceremony) and the session-
	// sweep ticker are NOT wired — DevBypass takes over the auth chain.
	DevMode bool
	// DevOrganizationID, DevUserID, DevUserEmail are read by DevBypass when
	// DevMode is set. Required when DevMode=true; the composition root die()s
	// on missing DevOrganizationID.
	DevOrganizationID string
	DevUserID         string
	DevUserEmail      string

	// RedisConfigured is true when REDIS_URL was set. Drives whether
	// rate-limiting and the readyz Redis check are wired in.
	RedisConfigured bool
	// RateLimitMax is the per-minute request cap per (organization, user).
	// 0 (or negative) → middleware.DefaultRateLimitMax. Composition roots
	// parse RATE_LIMIT_MAX from the environment.
	RateLimitMax int

	// StuckScanTimeout is the cutoff the stuck-scan recovery ticker uses.
	StuckScanTimeout time.Duration
	// MigrationDatabaseURL is needed by the stuck-scan recovery goroutine
	// (it opens its own pool because the action is owner-level). Optional
	// for tests — when empty, the stuck-scan ticker is not started.
	MigrationDatabaseURL string
}

// Deps bundles the concrete services ComposeServer plugs together. Every
// field that would differ between self-hosted and SaaS reactivation is an
// interface; concrete production impls are built by the composition root.
//
// Test affordance: every interface field MAY be nil in the smoke test
// (the composition path checks before consulting). Composition roots
// MUST populate the fields they expect to use — a nil Store, for
// example, will panic at the first request.
type Deps struct {
	// Store is the data-access seam. Production: postgres.Store.
	// Tests: any storage.Store impl, including narrow stubs.
	Store storage.Store
	// Cache backs JWKS, OIDC ceremony state, the rate-limiter counter,
	// and the session-revocation cache. Production: Redis or in-memory.
	Cache cache.Cache
	// Queue queues ingestion scan triggers (currently sync HTTP fallback
	// in self-hosted; Redis LPUSH/BRPOP in SaaS). May be nil in tests.
	Queue queue.Queue

	// AuthProvider is the auth seam. Today there's a single impl
	// (auth.NativeProvider — cookie + sessions table); the interface stays
	// so a SaaS reactivation can swap in a remote-IdP impl without
	// touching the rest of the chain. nil iff Config.DevMode (DevBypass
	// middleware replaces it).
	AuthProvider auth.Provider

	// Discoverer is the pre-auth /v1/sso/discover seam. Production: native.
	// SaaS: composite (Kinde + native). Required.
	Discoverer sso.Discoverer
	// Connector is the SSO connection-CRUD seam. Production: native.
	// SaaS: kinde-mirroring wrapper. Required.
	Connector sso.Connector

	// SessionManager is the native-auth session orchestrator. Required
	// when !Config.DevMode; nil otherwise. Used by both the native auth
	// handler and the OIDC callback (which mints sessions with
	// auth_mode='sso').
	SessionManager *auth.Manager
	// CookieConfig governs the session cookie posture. Required when
	// !Config.DevMode.
	CookieConfig auth.CookieConfig

	// EnforcementResolver looks up per-org SSO enforcement so the
	// EnforceSSO middleware can 403 password sessions on a `required`
	// org. nil disables the middleware (composition root passes nil
	// under DevMode or when SSO storage isn't wired).
	EnforcementResolver middleware.SSOEnforcementResolver

	// SSOValidator and SSOStateStore back the OIDC ceremony (initiate +
	// callback). Required when the OIDC ceremony routes are wired
	// (i.e. when !Config.DevMode).
	SSOValidator  *sso.Validator
	SSOStateStore *sso.StateStore

	// MetricsRegistry holds Prometheus counters/histograms for the
	// request-logging middleware. Caller-supplied so two ComposeServer
	// calls in the same process (e.g. side-by-side smoke tests) don't
	// fight over MustRegister. nil → defaults via prometheus.DefaultRegisterer.
	MetricsRegistry *Metrics
}

// Metrics groups the Prometheus instruments the request-logging middleware
// updates. Held as a struct so the composition root constructs them once
// (avoiding "duplicate metrics collector registration" errors) and the
// smoke test can supply its own isolated registry.
type Metrics struct {
	RequestsTotal           *prometheus.CounterVec
	RequestDurationSeconds  *prometheus.HistogramVec
}

// NewDefaultMetrics constructs the standard request metrics. Callers MUST
// register the returned vectors with their Prometheus registry — this
// function does not call MustRegister so it's safe to call multiple times
// from tests with separate registries.
func NewDefaultMetrics() *Metrics {
	return &Metrics{
		RequestsTotal: prometheus.NewCounterVec(
			prometheus.CounterOpts{
				Name: "axiaops_api_requests_total",
				Help: "Total number of API requests received.",
			},
			[]string{"method", "route", "status"},
		),
		RequestDurationSeconds: prometheus.NewHistogramVec(
			prometheus.HistogramOpts{
				Name: "axiaops_api_request_duration_seconds",
				Help: "API request latencies in seconds.",
			},
			[]string{"method", "route"},
		),
	}
}

// statusWriter captures the HTTP status code written by a handler so the
// request-logging middleware can emit it. Lifted from cmd/main.go.
type statusWriter struct {
	http.ResponseWriter
	code int
}

func (sw *statusWriter) WriteHeader(code int) {
	sw.code = code
	sw.ResponseWriter.WriteHeader(code)
}

// ComposeServer builds the http.Handler that serves the API surface.
// Composition roots wrap the result in &http.Server{Handler: ...} and run
// ListenAndServe. Returns an error iff a required Dep is missing — so a
// misconfigured composition root fails fast at boot rather than silently
// 500ing later.
//
// The returned handler is the full middleware chain, outermost-first:
//   request-logging + metrics
//     → request-id
//       → auth (DevBypass | WrapNative + EnforceSSO)
//         → rate-limiter
//           → CORS
//             → mux (handlers)
//
// Tickers (stuck-scan, license, session-sweep, sso-sweep) are NOT started
// here — composition roots own goroutine lifecycle so a test can build
// the handler without spawning long-lived background work. See
// StartTickers below for the production wiring.
func ComposeServer(cfg Config, deps Deps) (http.Handler, error) {
	if deps.Store == nil {
		return nil, fmt.Errorf("serverbuild: Deps.Store is required")
	}
	if deps.Discoverer == nil {
		return nil, fmt.Errorf("serverbuild: Deps.Discoverer is required (sso seam S4)")
	}
	if deps.Connector == nil {
		return nil, fmt.Errorf("serverbuild: Deps.Connector is required (sso seam S8)")
	}
	if !cfg.DevMode {
		if deps.AuthProvider == nil {
			return nil, fmt.Errorf("serverbuild: Deps.AuthProvider is required when Config.DevMode=false")
		}
		if deps.SessionManager == nil {
			return nil, fmt.Errorf("serverbuild: Deps.SessionManager is required when Config.DevMode=false")
		}
		if deps.SSOValidator == nil || deps.SSOStateStore == nil {
			return nil, fmt.Errorf("serverbuild: Deps.SSOValidator and SSOStateStore are required when Config.DevMode=false")
		}
	}

	mux := http.NewServeMux()

	// ── Core API handler ──────────────────────────────────────────────────
	apiH := api.New(deps.Store, deps.Queue).WithPublicHost(cfg.PublicHost)
	if cfg.RedisConfigured && deps.Cache != nil {
		apiH = apiH.WithRedisCache(deps.Cache)
	}
	apiH.Register(mux)

	// ── SSO handlers ──────────────────────────────────────────────────────
	// Authenticated CRUD (connections, domains, group mappings).
	ssoH := sso.New(deps.Store, deps.Connector, deps.Discoverer)
	ssoH.Register(mux)
	// Pre-auth /v1/sso/discover — mounted directly because publicPath
	// bypasses it in middleware/auth.go.
	mux.Handle("GET /v1/sso/discover", sso.NewDiscoverHandler(deps.Discoverer))

	// ── Native-auth + OIDC ceremony ───────────────────────────────────────
	// Wire when not in DevMode. DevBypass replaces the entire auth chain.
	if !cfg.DevMode {
		authH := auth.NewHandler(deps.Store, deps.SessionManager, deps.CookieConfig, auth.NewAuditWriter(deps.Store))
		if deps.Cache != nil {
			authH = authH.WithLoginRateLimit(auth.NewLoginRateLimiter(deps.Cache))
		}
		authH.Register(mux)

		if cfg.PublicHost == "" {
			slog.Error("sso: ceremony: PUBLIC_HOST is empty — IdP-registered redirect_uri will not match the URL the callback receives")
		}
		mux.Handle("GET /v1/sso/oidc/{cid}/initiate",
			sso.NewInitiateHandler(deps.Store, deps.SSOValidator, deps.SSOStateStore, cfg.PublicHost))
		callback := sso.NewCallbackHandler(sso.CallbackOptions{
			Store:        deps.Store,
			Validator:    deps.SSOValidator,
			StateStore:   deps.SSOStateStore,
			Sessions:     deps.SessionManager,
			CookieConfig: deps.CookieConfig,
			PublicHost:   cfg.PublicHost,
		})
		// Standard, connection-agnostic callback URL (Tasks.md 2.7.22). Connection
		// identity flows through the state parameter rather than the path. This
		// is the redirect URI initiate puts in `redirect_uri`, and the one
		// customers register in their IdP.
		mux.Handle("GET "+sso.CallbackPath, callback)
		// Legacy path-cid callback. Kept for one release as a deprecation
		// window so already-registered IdP redirect URIs keep working while
		// customers update their app registrations. Hits surface via
		// axiaops_sso_legacy_callback_total{cid}; remove when the rate
		// stays at zero across all customers for a release.
		mux.Handle("GET /v1/sso/oidc/{cid}/callback", callback)
	}

	// Prometheus scrape endpoint — outside auth so the scrape worker
	// doesn't need a session. observability.MetricsHandler merges the
	// default registry (request-logging counters MustRegister'd in
	// cmd/main.go) with the observability package's private registry
	// (Global.* — HTTP, DB, AWS, scan, license, auth_provider). See the
	// helper's doc for why promhttp.Handler() alone is wrong.
	mux.Handle("/metrics", observability.MetricsHandler())

	// ── Middleware chain (innermost → outermost) ──────────────────────────
	root := http.Handler(mux)

	// Rate limiter — only when Redis is wired (the limiter needs durable
	// counters; the in-memory cache fallback would be per-replica which
	// is meaningless under autoscaling).
	if cfg.RedisConfigured && deps.Cache != nil {
		limiter := middleware.NewRateLimiter(deps.Cache, cfg.RateLimitMax)
		root = limiter.Wrap(root)
	}

	// Auth: DevBypass overrides everything else. Otherwise the auth →
	// enforcement chain wraps the rest.
	if cfg.DevMode {
		root = middleware.DevBypass(cfg.DevOrganizationID, cfg.DevUserID, cfg.DevUserEmail, root)
	} else {
		// EnforceSSO runs AFTER auth resolves the identity (enforcement
		// reads organization_id + auth_mode from context). /v1/auth/logout
		// in the skip-set so a blocked password user can clean-end.
		root = middleware.EnforceSSO(deps.EnforcementResolver, "/v1/auth/logout")(root)
		root = middleware.WrapNative(deps.AuthProvider, root)
	}

	// Request-id + logging + metrics — outermost so every request is
	// recorded regardless of which inner branch handled it.
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
		// Use the matched route pattern to avoid high-cardinality label
		// values (e.g. "/accounts/{id}/scan" rather than the per-id path).
		_, route := mux.Handler(r)
		status := strconv.Itoa(rw.code)
		metrics.RequestsTotal.WithLabelValues(r.Method, route, status).Inc()
		metrics.RequestDurationSeconds.WithLabelValues(r.Method, route).Observe(duration)

		slog.Info("api: request",
			"method", r.Method,
			"path", r.URL.Path,
			"route", route,
			"status", rw.code,
			"duration_ms", fmt.Sprintf("%.1f", duration*1000),
			"request_id", middleware.RequestIDFromCtx(r.Context()),
		)
	})

	// CORS lives outermost — headers must be set before auth/rate-limit
	// can reject. apiH.Handler wraps the full chain in the CORS layer.
	return apiH.Handler(logged), nil
}

// TickerOptions wraps the optional background-goroutine inputs StartTickers
// needs. Composition roots construct one from env; tests pass a zero-value
// to skip ticker startup.
type TickerOptions struct {
	// MigrationDatabaseURL is the owner-pool URL the stuck-scan ticker
	// uses (it opens its own short-lived pool). Empty → ticker not started.
	MigrationDatabaseURL string
	// StuckScanTimeout is the cutoff for "this account has been scanning
	// too long; reset it". Zero → ticker not started.
	StuckScanTimeout time.Duration
	// NativeAuthActive controls whether the session-sweep ticker runs.
	NativeAuthActive bool
}

// StartTickers spins up the long-lived background goroutines:
//   - Stuck-scan recovery (every 5 minutes): resets accounts left in
//     status='scanning' for longer than StuckScanTimeout.
//   - License re-classification (hourly): keeps Prometheus license-state
//     gauges fresh so a grace/expired transition isn't masked.
//   - Session sweep (hourly, only when NativeAuthActive): hard-deletes
//     sessions where expires_at OR revoked_at is older than 7 days.
//   - SSO sweep (24h): marks expired verified sso_domains as stale.
//
// ctx is the cancellation source for every ticker — production wires
// the signal context so SIGTERM/SIGINT shuts them down with the HTTP
// server; tests pass a cancellable context to stop after assertions.
//
// Composition roots call this AFTER ComposeServer and BEFORE
// http.Server.ListenAndServe.
func StartTickers(ctx context.Context, store storage.Store, opts TickerOptions) {
	if opts.MigrationDatabaseURL != "" && opts.StuckScanTimeout > 0 {
		go runStuckScanTicker(ctx, opts.MigrationDatabaseURL, opts.StuckScanTimeout)
	}

	// License ticker: re-classify every hour. No-op under DEV_MODE (no
	// license loaded) — the package itself short-circuits.
	go runLicenseTicker(ctx)

	if opts.NativeAuthActive {
		go runSessionSweepTicker(ctx, store)
	}

	go sso.NewSweeper(store, 0).Run(ctx)
}
