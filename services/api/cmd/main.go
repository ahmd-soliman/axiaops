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
	"axiaops.io/api/internal/middleware"
	"axiaops.io/shared/cache"
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

	s, err := postgres.New(ctx, dbURL)
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
	h.Register(mux)
	mux.Handle("/metrics", promhttp.Handler())

	// ── Rate Limiting ─────────────────────────────────────────────────────────
	root := http.Handler(mux)
	devMode := os.Getenv("DEV_MODE") == "true"
	rateLimitEnabled := os.Getenv("REDIS_URL") != ""
	if rateLimitEnabled {
		limiter := middleware.NewRateLimiter(c)
		root = limiter.Wrap(root)
		slog.Info("api: rate limiting enabled (60 req/min per tenant)")
	} else if !devMode && os.Getenv("REDIS_URL") == "" {
		slog.Warn("api: rate limiting disabled — REDIS_URL not set")
	}

	// ── Auth ──────────────────────────────────────────────────────────────────
	if devMode {
		devOrganizationID := os.Getenv("DEV_TENANT_ID")
		if devOrganizationID == "" {
			die("auth: DEV_MODE=true requires DEV_TENANT_ID to be set")
		}
		// Pin the dev tenant row at startup so DevBypass can inject a known id
		// without doing any DB work per request. id = org_code = name here —
		// dev mode uses DEV_TENANT_ID as the literal, stable tenant id.
		if err := store.EnsureOrganization(ctx, devOrganizationID, devOrganizationID, devOrganizationID); err != nil {
			die("auth: failed to ensure dev tenant", "tenant", devOrganizationID, "error", err)
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
		slog.Warn("auth: DEV_MODE — bypassing auth", "tenant", devOrganizationID, "user", devUserID)
		root = middleware.DevBypass(devOrganizationID, devUserID, devUserEmail, root)
	} else {
		kindeIssuer := os.Getenv("KINDE_ISSUER")
		if kindeIssuer == "" {
			slog.Warn("auth: KINDE_ISSUER not set — running without authentication")
		} else {
			auth, err := middleware.NewAuth(ctx, kindeIssuer, store, c)
			if err != nil {
				die("auth: init failed", "error", err)
			}
			slog.Info("auth: JWT verification enabled", "issuer", kindeIssuer)
			root = auth.Wrap(root)
		}
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
