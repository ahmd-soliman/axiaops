package main

import (
	"context"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"strconv"
	"syscall"
	"time"

	"axiaops.io/api/internal/api"
	"axiaops.io/api/internal/auth"
	"axiaops.io/api/internal/kinde"
	"axiaops.io/api/internal/middleware"
	"axiaops.io/shared/cache"
	"axiaops.io/shared/license"
	"axiaops.io/shared/logging"
	"axiaops.io/shared/model"
	"axiaops.io/shared/queue"
	"axiaops.io/shared/storage"
	"axiaops.io/shared/storage/postgres"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promhttp"
)

const stuckScanTimeout = 15 * time.Minute

// statusWriter captures the HTTP status code written by a handler.
type statusWriter struct {
	http.ResponseWriter
	code int
}

func (sw *statusWriter) WriteHeader(code int) {
	sw.code = code
	sw.ResponseWriter.WriteHeader(code)
}

// Prometheus metrics
var (
	apiRequestsTotal = prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Name: "axiaops_api_requests_total",
			Help: "Total number of API requests received.",
		},
		[]string{"method", "route", "status"},
	)

	apiRequestDurationSeconds = prometheus.NewHistogramVec(
		prometheus.HistogramOpts{
			Name: "axiaops_api_request_duration_seconds",
			Help: "API request latencies in seconds.",
		},
		[]string{"method", "route"},
	)
)

func init() {
	prometheus.MustRegister(apiRequestsTotal)
	prometheus.MustRegister(apiRequestDurationSeconds)
}

// die logs a fatal error and exits with code 1.
func die(msg string, args ...any) {
	slog.Error(msg, args...)
	os.Exit(1)
}

func main() {
	logging.Init("api")

	ctx := context.Background()

	// ── License (B1.6) ───────────────────────────────────────────────────────
	// Verify the self-hosted license JWT before any further startup work.
	// DEV_MODE skips the check entirely (per plan §4.9.2 step 1). Past-grace
	// licenses fail-fast here so the operator sees one clear refusal rather
	// than the binary half-starting and exposing partially-initialised state.
	// Slice 4 will start the runtime ticker after ComposeServer returns.
	if err := license.VerifyAtBoot(os.Getenv("DEV_MODE") == "true"); err != nil {
		die("license: refusing to start", "error", err.Error())
	}

	// ── Storage ──────────────────────────────────────────────────────────────
	dbURL := os.Getenv("DATABASE_URL")
	if dbURL == "" {
		die("storage: DATABASE_URL is required")
	}
	migrationURL := os.Getenv("MIGRATION_DATABASE_URL")
	if migrationURL == "" {
		migrationURL = dbURL
	}

	// Startup recovery: reset any accounts left in "scanning" from a previous crash.
	if n, err := postgres.ResetStuckScans(ctx, migrationURL, stuckScanTimeout); err != nil {
		slog.Warn("startup: failed to reset stuck scans", "error", err)
	} else if n > 0 {
		slog.Warn("startup: reset stuck scanning accounts", "count", n)
	}

	s, err := postgres.NewWithOwner(ctx, dbURL, migrationURL)
	if err != nil {
		die("storage: postgres init failed", "error", err)
	}
	defer func() {
		if err := s.Close(); err != nil {
			slog.Error("storage: close error", "error", err)
		}
	}()
	store := storage.Store(s)
	slog.Info("storage: using PostgreSQL")

	// ── Cache ─────────────────────────────────────────────────────────────────
	c := cache.New(os.Getenv("REDIS_URL"))
	defer func() { _ = c.Close() }()

	// ── Queue ─────────────────────────────────────────────────────────────────
	ingestionURL := os.Getenv("INGESTION_URL")
	if ingestionURL == "" {
		ingestionURL = "http://localhost:8081"
	}
	q := queue.New(os.Getenv("REDIS_URL"), ingestionURL)
	defer func() { _ = q.Close() }()

	// ── HTTP API ──────────────────────────────────────────────────────────────
	addr := os.Getenv("API_ADDR")
	if addr == "" {
		addr = ":8080"
	}

	mux := http.NewServeMux()
	h := api.New(store, q)
	// Wire the cache into the readyz check only when REDIS_URL was actually
	// set. cache.New silently falls back to in-memory when Redis is
	// unconfigured; we want readyz to report "skipped" in that case rather
	// than pinging an in-process map and reporting "ok".
	if os.Getenv("REDIS_URL") != "" {
		h = h.WithRedisCache(c)
	}
	// Wire the Kinde Mgmt API client. DEV_MODE=true uses an in-memory stub so
	// invitations work locally without real Kinde credentials; production
	// requires KINDE_M2M_CLIENT_ID + KINDE_M2M_CLIENT_SECRET. Without either,
	// /v1/invitations and PATCH /v1/organizations/me return 503.
	h = h.WithKinde(buildKindeClient())

	// Switch /v1/invitations to the native (token-bearing) path when
	// AUTH_PROVIDER selects it. PUBLIC_HOST is the externally-reachable
	// origin used to build redemption URLs; empty produces relative
	// URLs the frontend resolves against window.location.origin.
	//
	// DEV_MODE skips this entirely: DevBypass injects a fixed org so
	// nothing exercises invitations, and the Kinde-stub path keeps the
	// existing local-dev behaviour. Run `start-dev` with explicit
	// AUTH_PROVIDER=native + DEV_MODE=false to exercise the native
	// invitation flow against a fresh DB.
	if mode := os.Getenv("AUTH_PROVIDER"); os.Getenv("DEV_MODE") != "true" && (mode == "" || mode == "native" || mode == "both") {
		h = h.WithNativeInvitations(true, os.Getenv("PUBLIC_HOST"))
	}

	h.Register(mux)

	// Native-auth ceremony endpoints (POST /v1/auth/{bootstrap,login,logout}).
	// These are reachable only under AUTH_PROVIDER=native|both — under
	// AUTH_PROVIDER=kinde we register them anyway because publicPath
	// bypasses /v1/auth/* in the middleware regardless, and the handlers
	// will simply return 401/409 if invoked. Cleaner to keep the routing
	// surface uniform. DEV_MODE skips registration entirely — DevBypass
	// makes them moot.
	devMode := os.Getenv("DEV_MODE") == "true"
	nativeAuthActive := false
	if !devMode {
		mode := os.Getenv("AUTH_PROVIDER")
		if mode == "" {
			mode = "native"
		}
		if mode == "native" || mode == "both" {
			nativeAuthActive = true
			authMgr := buildSessionManager(store, c)
			authH := auth.NewHandler(store, authMgr, auth.NewCookieConfig(), auth.NewAuditWriter(store)).
				WithLoginRateLimit(auth.NewLoginRateLimiter(c))
			authH.Register(mux)
			// First-owner install-token generator. No-op when an
			// organization already exists.
			if res, err := auth.MaybeGenerateInstallToken(ctx, store); err != nil {
				slog.Error("auth: install token generator failed", "err", err)
			} else {
				slog.Info("auth: install token state",
					"generated", res.Generated,
					"skipped", res.Skipped,
					"file", res.FilePath)
			}
		}
	}

	mux.Handle("/metrics", promhttp.Handler())

	// ── Rate Limiting ─────────────────────────────────────────────────────────
	root := http.Handler(mux)
	rateLimitEnabled := os.Getenv("REDIS_URL") != ""
	if rateLimitEnabled {
		limiter := middleware.NewRateLimiter(c)
		root = limiter.Wrap(root)
		slog.Info("api: rate limiting enabled (60 req/min per organization)")
	} else if !devMode && os.Getenv("REDIS_URL") == "" {
		slog.Warn("api: rate limiting disabled — REDIS_URL not set")
	}

	// ── Auth ──────────────────────────────────────────────────────────────────
	// DEV_MODE bypass takes precedence over AUTH_PROVIDER — local dev never
	// pays the cost of cookie/JWT validation.
	if devMode {
		devOrganizationID := os.Getenv("DEV_ORGANIZATION_ID")
		if devOrganizationID == "" {
			die("auth: DEV_MODE=true requires DEV_ORGANIZATION_ID to be set")
		}
		// Pin the dev organization row at startup so DevBypass can inject a known id
		// without doing any DB work per request. id = org_code = name here —
		// dev mode uses DEV_ORGANIZATION_ID as the literal, stable organization id.
		if err := store.EnsureOrganization(ctx, devOrganizationID, devOrganizationID, devOrganizationID); err != nil {
			die("auth: failed to ensure dev organization", "organization", devOrganizationID, "error", err)
		}
		// Pin the dev user row so audit rows, dismissal actors, and future
		// RBAC lookups have a real FK target. DevBypass injects this same id
		// onto every request's context.
		devUserID := os.Getenv("DEV_USER_ID")
		if devUserID == "" {
			devUserID = "dev-user-axiaops"
		}
		devUserEmail := os.Getenv("DEV_USER_EMAIL")
		if devUserEmail == "" {
			devUserEmail = "dev@axiaops.local"
		}
		if err := store.EnsureUser(ctx, model.User{
			ID:             devUserID,
			OrganizationID: devOrganizationID,
			Email:          devUserEmail,
			Name:           "Dev User",
		}); err != nil {
			die("auth: failed to ensure dev user", "user", devUserID, "error", err)
		}
		// Pin the dev membership as owner so DevBypass requests pass every
		// permission check via the Require decorator. RBAC Phase 1.
		if err := store.EnsureDevMembership(ctx, devOrganizationID, devUserID, "owner"); err != nil {
			die("auth: failed to ensure dev membership", "user", devUserID, "error", err)
		}
		slog.Warn("auth: DEV_MODE — bypassing auth", "organization", devOrganizationID, "user", devUserID)
		root = middleware.DevBypass(devOrganizationID, devUserID, devUserEmail, root)
	} else {
		// Three-state strangler machine (D1, plan §4.5 / §4.8.1):
		//
		//   AUTH_PROVIDER=native  cookie + sessions table only (terminal state, default)
		//   AUTH_PROVIDER=both    cookie OR Bearer JWT (transitional — required during
		//                         the rolling deploy from kinde → native)
		//   AUTH_PROVIDER=kinde   Bearer JWT only (legacy, deleted at D2 = 2026-10-30)
		//
		// The runbook MUST move kinde → both → native in that order;
		// jumping straight from kinde to native causes auth flapping
		// because mid-rolling-restart replicas land on different values.
		mode := os.Getenv("AUTH_PROVIDER")
		if mode == "" {
			mode = "native"
		}

		var provider auth.Provider
		switch mode {
		case "native":
			mgr := buildSessionManager(store, c)
			provider = auth.NewNativeProvider(mgr, membershipLookup(store))
			slog.Info("auth: native provider enabled (cookie + sessions table)")
		case "kinde":
			kindeAuth := mustNewKindeAuth(ctx, store, c)
			provider = middleware.NewKindeProvider(kindeAuth)
			slog.Info("auth: kinde provider enabled (Bearer JWT)")
		case "both":
			mgr := buildSessionManager(store, c)
			kindeAuth := mustNewKindeAuth(ctx, store, c)
			provider = auth.NewCompositeProvider(
				auth.NewNativeProvider(mgr, membershipLookup(store)),
				middleware.NewKindeProvider(kindeAuth),
			)
			slog.Warn("auth: composite provider enabled (native + kinde) — transitional only")
		default:
			die("auth: invalid AUTH_PROVIDER", "value", mode, "expected", "native|kinde|both")
		}
		root = middleware.WrapNative(provider, root)
	}

	// Request logger + metrics — outermost layer so every request is recorded.
	withReqID := middleware.RequestID(root)
	logged := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		rw := &statusWriter{ResponseWriter: w, code: 200}
		withReqID.ServeHTTP(rw, r)

		duration := time.Since(start).Seconds()
		// Use the matched route pattern to avoid high-cardinality label values
		// (e.g. "/accounts/{id}/scan" instead of "/accounts/abc-123/scan").
		_, route := mux.Handler(r)
		status := strconv.Itoa(rw.code)
		apiRequestsTotal.WithLabelValues(r.Method, route, status).Inc()
		apiRequestDurationSeconds.WithLabelValues(r.Method, route).Observe(duration)

		slog.Info("api: request",
			"method", r.Method,
			"path", r.URL.Path,
			"route", route,
			"status", rw.code,
			"duration_ms", fmt.Sprintf("%.1f", duration*1000),
			"request_id", middleware.RequestIDFromCtx(r.Context()),
		)
	})

	// Background ticker: reset accounts stuck in "scanning" every 5 minutes.
	go func() {
		ticker := time.NewTicker(5 * time.Minute)
		defer ticker.Stop()
		for range ticker.C {
			n, err := postgres.ResetStuckScans(context.Background(), migrationURL, stuckScanTimeout)
			if err != nil {
				slog.Warn("scan-recovery: failed to reset stuck scans", "error", err)
				continue
			}
			if n > 0 {
				slog.Warn("scan-recovery: reset stuck scanning accounts", "count", n)
			}
		}
	}()

	// Background ticker: re-classify the loaded license every hour so the
	// Prometheus gauges advance with the wall clock and a slog.Warn fires
	// on grace/expired transitions. Never calls os.Exit — mid-flight
	// transitions are observability events; slice 5's scan-gate is what
	// actually blocks behaviour. No-op under DEV_MODE (no license loaded).
	//
	// Uses context.Background() to match the existing stuck-scan + session-
	// sweep tickers below. No in-flight work here risks corruption on
	// process termination (read-only Prometheus + slog), so the pattern is
	// safe — but inconsistent with the ingestion-side license ticker which
	// uses sigCtx. Consolidating all three tickers in this file onto sigCtx
	// is a clean follow-up; not blocking B1.6.
	go license.RunTicker(context.Background(), license.DefaultTickerInterval)

	// Background ticker: hard-delete sessions where expires_at OR
	// revoked_at is older than 7 days. Bounds growth of the sessions
	// table without affecting active users — by the time a row is
	// older than 7 days it cannot be live (max TTL is 24h). Only
	// runs when native auth is actually in use; under
	// AUTH_PROVIDER=kinde the table stays empty and there's no work.
	if nativeAuthActive {
		go func() {
			ticker := time.NewTicker(time.Hour)
			defer ticker.Stop()
			for range ticker.C {
				cutoff := time.Now().UTC().Add(-7 * 24 * time.Hour)
				n, err := store.SweepExpiredSessions(context.Background(), cutoff)
				if err != nil {
					slog.Warn("session-sweep: failed", "error", err)
					continue
				}
				if n > 0 {
					slog.Info("session-sweep: deleted expired/revoked sessions", "count", n)
				}
			}
		}()
	}

	// ── Graceful Shutdown ────────────────────────────────────────────────────────
	// Wait for SIGTERM/SIGINT indefinitely (App Runner sends SIGTERM on shutdown).
	// The 30-second timeout applies only to the drain phase, not to the signal wait.
	sigCtx, sigCancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer sigCancel()

	// Start HTTP server in a goroutine
	server := &http.Server{
		Addr:    addr,
		Handler: h.Handler(logged), // CORS outermost — headers always set before auth/rate-limit can reject
	}

	// Run server in background; will block until signal or error
	errCh := make(chan error, 1)
	go func() {
		slog.Info("api: listening", "addr", addr)
		errCh <- server.ListenAndServe()
	}()

	// Wait for either: (1) shutdown signal received, or (2) server error
	select {
	case err := <-errCh:
		// Server exited with error
		if err != nil && err != http.ErrServerClosed {
			die("api: server error", "error", err)
		}
	case <-sigCtx.Done():
		// SIGTERM or SIGINT received — graceful shutdown
		slog.Warn("api: shutdown signal received, draining requests")
		shutdownStart := time.Now()

		// Give in-flight requests up to 30 seconds to complete
		shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer shutdownCancel()

		// server.Shutdown waits for all in-flight requests to complete (with timeout)
		if err := server.Shutdown(shutdownCtx); err != nil && err != context.DeadlineExceeded {
			slog.Error("api: shutdown error", "error", err)
		}

		// Close database connection pool
		if err := s.Close(); err != nil {
			slog.Error("api: db close error", "error", err)
		}

		shutdownDuration := time.Since(shutdownStart).Seconds()
		slog.Info("api: shutdown complete", "duration_seconds", fmt.Sprintf("%.2f", shutdownDuration))
	}
}

// buildKindeClient picks the Kinde Management API client based on environment.
//
//	DEV_MODE=true                 → in-memory stub (no network)
//	KINDE_USE_STUB=true           → in-memory stub (opt-in for staging without real M2M)
//	M2M creds set                 → real HTTPClient
//	M2M creds unset, no opt-in    → nil (handlers return 503 until configured)
//
// KINDE_USE_STUB is the escape hatch for `make start-staging` when the operator
// hasn't wired up a real Kinde M2M app yet — invitation/rename flows succeed
// locally without sending real Kinde calls. Never set this in production.
func buildKindeClient() kinde.Client {
	if os.Getenv("DEV_MODE") == "true" {
		slog.Info("kinde: DEV_MODE — using in-memory stub")
		return kinde.NewStub()
	}
	if os.Getenv("KINDE_USE_STUB") == "true" {
		slog.Warn("kinde: KINDE_USE_STUB=true — using in-memory stub. Invitation emails and Kinde-side org renames are NO-OPS. Do not enable in production.")
		return kinde.NewStub()
	}
	issuer := os.Getenv("KINDE_ISSUER")
	mgmtURL := os.Getenv("KINDE_MGMT_API_URL")
	clientID := os.Getenv("KINDE_M2M_CLIENT_ID")
	clientSecret := os.Getenv("KINDE_M2M_CLIENT_SECRET")
	if issuer == "" || clientID == "" || clientSecret == "" {
		slog.Warn("kinde: KINDE_M2M_CLIENT_ID/SECRET unset — invitations will return 503. Set KINDE_USE_STUB=true for local staging.")
		return nil
	}
	c, err := kinde.New(issuer, mgmtURL, clientID, clientSecret)
	if err != nil {
		slog.Error("kinde: client init failed", "error", err)
		return nil
	}
	slog.Info("kinde: management API client initialised")
	return c
}

// buildSessionManager wires the native-auth session orchestrator. Reads
// SESSION_TTL_HOURS and SESSIONS_PER_USER_CAP — defaults match
// docs/sso-implementation-plan.md §4.5 (24h TTL, cap 10).
func buildSessionManager(store storage.Store, c cache.Cache) *auth.Manager {
	cfg := auth.Config{
		TTL:             auth.DefaultSessionTTL,
		SessionsPerUser: auth.DefaultSessionsPerUserCap,
	}
	if v := os.Getenv("SESSION_TTL_HOURS"); v != "" {
		if h, err := strconv.Atoi(v); err == nil && h > 0 {
			cfg.TTL = time.Duration(h) * time.Hour
		} else {
			slog.Warn("auth: invalid SESSION_TTL_HOURS, using default", "value", v)
		}
	}
	if v := os.Getenv("SESSIONS_PER_USER_CAP"); v != "" {
		// SESSIONS_PER_USER_CAP=0 disables the cap (matches Config doc).
		if n, err := strconv.Atoi(v); err == nil && n >= 0 {
			cfg.SessionsPerUser = n
		} else {
			slog.Warn("auth: invalid SESSIONS_PER_USER_CAP, using default", "value", v)
		}
	}
	return auth.NewManager(store, auth.NewSessionCache(c), cfg)
}

// membershipLookup returns an auth.MembershipLookup bound to the given
// store. Single SELECT joining memberships + users — see
// services/shared/storage/postgres/native_auth.go.
func membershipLookup(store storage.Store) auth.MembershipLookup {
	return func(ctx context.Context, organizationID, userID string) (auth.MembershipDetails, error) {
		role, email, err := store.LookupMembership(ctx, organizationID, userID)
		if err != nil {
			return auth.MembershipDetails{}, err
		}
		return auth.MembershipDetails{Role: role, Email: email}, nil
	}
}

// mustNewKindeAuth constructs a *middleware.Auth or fatally exits. Used
// under AUTH_PROVIDER=kinde and AUTH_PROVIDER=both — both modes require
// KINDE_ISSUER + working JWKS fetch.
func mustNewKindeAuth(ctx context.Context, store storage.Store, c cache.Cache) *middleware.Auth {
	issuer := os.Getenv("KINDE_ISSUER")
	if issuer == "" {
		die("auth: AUTH_PROVIDER set to kinde or both requires KINDE_ISSUER to be set")
	}
	a, err := middleware.NewAuth(ctx, issuer, store, c)
	if err != nil {
		die("auth: kinde init failed", "error", err)
	}
	slog.Info("auth: kinde JWT verification ready", "issuer", issuer)
	return a
}
