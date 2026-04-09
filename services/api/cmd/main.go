// main is the entry point for the AxiaOps API service.
// It reads ghost detection results from the database and serves them over HTTP.
// Ingestion is handled by a separate service (services/ingestion).
package main

import (
	"context"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"strconv"
	"time"

	sentry "github.com/getsentry/sentry-go"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promhttp"
	"axiaops.io/api/internal/api"
	"axiaops.io/api/internal/middleware"
	"axiaops.io/shared/logging"
	"axiaops.io/shared/storage"
	"axiaops.io/shared/storage/postgres"
	"axiaops.io/shared/storage/sqlite"
)

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

// die logs a fatal error, flushes Sentry, and exits with code 1.
// Safe to call at any point — sentry.Flush is a no-op if Sentry is not initialised.
func die(msg string, args ...any) {
	slog.Error(msg, args...)
	sentry.Flush(2 * time.Second)
	os.Exit(1)
}

func main() {
	logging.Init()
	flushSentry := logging.InitSentry("api")
	defer flushSentry()

	ctx := context.Background()

	// ── Storage ──────────────────────────────────────────────────────────────
	var store storage.Store
	if dbURL := os.Getenv("DATABASE_URL"); dbURL != "" {
		migrationURL := os.Getenv("MIGRATION_DATABASE_URL")
		if migrationURL == "" {
			migrationURL = dbURL
		}
		if err := postgres.Migrate(migrationURL); err != nil {
			die("storage: migration failed", "error", err)
		}
		s, err := postgres.New(ctx, dbURL)
		if err != nil {
			die("storage: postgres init failed", "error", err)
		}
		defer s.Close()
		store = s
		slog.Info("storage: using PostgreSQL")
	} else {
		dbPath := os.Getenv("DB_PATH")
		if dbPath == "" {
			dbPath = "axiaops.db"
		}
		s, err := sqlite.New(dbPath)
		if err != nil {
			die("storage: sqlite init failed", "error", err)
		}
		defer s.Close()
		store = s
		slog.Info("storage: using SQLite")
	}

	// ── HTTP API ──────────────────────────────────────────────────────────────
	addr := os.Getenv("API_ADDR")
	if addr == "" {
		addr = ":8080"
	}

	mux := http.NewServeMux()
	h := api.New(store)
	h.Register(mux)
	mux.Handle("/metrics", promhttp.Handler())

	// ── Auth ──────────────────────────────────────────────────────────────────
	var root http.Handler = h.Handler(mux)
	if os.Getenv("DEV_MODE") == "true" {
		devTenantID := os.Getenv("DEV_TENANT_ID")
		if devTenantID == "" {
			die("auth: DEV_MODE=true requires DEV_TENANT_ID to be set")
		}
		slog.Warn("auth: DEV_MODE — bypassing auth", "tenant", devTenantID)
		root = middleware.DevBypass(devTenantID, root)
	} else {
		kindeIssuer := os.Getenv("KINDE_ISSUER")
		if kindeIssuer == "" {
			slog.Warn("auth: KINDE_ISSUER not set — running without authentication")
		} else {
			auth, err := middleware.NewAuth(ctx, kindeIssuer, store)
			if err != nil {
				die("auth: init failed", "error", err)
			}
			slog.Info("auth: JWT verification enabled", "issuer", kindeIssuer)
			root = auth.Wrap(root)
		}
	}

	// Request logger + metrics — outermost layer so every request is recorded.
	logged := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		rw := &statusWriter{ResponseWriter: w, code: 200}
		root.ServeHTTP(rw, r)

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
		)
	})

	slog.Info("api: listening", "addr", addr)
	if err := http.ListenAndServe(addr, logged); err != nil {
		die("api: server error", "error", err)
	}
}
