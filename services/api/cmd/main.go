package main

import (
	"bytes"
	"context"
	"encoding/json"
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
	"axiaops.io/shared/logging"
	"axiaops.io/shared/storage"
	"axiaops.io/shared/storage/postgres"
	sentry "github.com/getsentry/sentry-go"
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

// die logs a fatal error, flushes Sentry, and exits with code 1.
// Safe to call at any point — sentry.Flush is a no-op if Sentry is not initialised.
func die(msg string, args ...any) {
	slog.Error(msg, args...)
	sentry.Flush(2 * time.Second)
	os.Exit(1)
}

// scanScheduledAccounts checks all accounts and triggers scans for those that are overdue.
// An account is eligible if: scan_interval_hours >= 0 AND (never scanned OR last_scanned_at + interval < now) AND status != 'scanning'
// scan_interval_hours=0 means "always eligible for scheduled scan" (triggers on every check).
func scanScheduledAccounts(ctx context.Context, store storage.Store, h *api.Handler) {
	// List all accounts across all tenants using ListAllAccounts, which explicitly bypasses RLS.
	// This is the correct method for background jobs that operate across tenants.
	accounts, err := store.ListAllAccounts(ctx)
	if err != nil {
		slog.Error("scan-scheduler: failed to list accounts", "error", err)
		return
	}

	if len(accounts) == 0 {
		return // Nothing to do
	}

	now := time.Now()
	for _, acc := range accounts {
		// Skip if account is already scanning
		if acc.Status == "scanning" {
			slog.Debug("scan.skipped_already_running", "account_id", acc.ID, "tenant_id", acc.TenantID)
			continue
		}

		// Skip if scan_interval_hours is negative (invalid configuration)
		if acc.ScanIntervalHours < 0 {
			slog.Warn("scan-scheduler: skipping account with negative interval", "account_id", acc.ID, "interval", acc.ScanIntervalHours)
			continue
		}

		// Check if account is overdue for a scan
		isOverdue := false
		if acc.LastScannedAt == nil {
			// Never scanned — overdue immediately
			isOverdue = true
		} else {
			// Calculate next scan time: last_scanned_at + (scan_interval_hours * time.Hour)
			// If scan_interval_hours=0, next_scan equals last_scanned_at, so now.After(last_scanned_at) is true (always eligible)
			nextScan := acc.LastScannedAt.Add(time.Duration(acc.ScanIntervalHours) * time.Hour)
			isOverdue = now.After(nextScan)
		}

		if !isOverdue {
			continue
		}

		// Trigger scan via HTTP POST to ingestion service
		err := triggerScheduledScan(ctx, acc.ID, acc.TenantID)
		if err != nil {
			slog.Error("scan.failed_to_trigger",
				"account_id", acc.ID,
				"tenant_id", acc.TenantID,
				"error", err,
			)
			continue
		}

		slog.Info("scan.scheduled",
			"account_id", acc.ID,
			"tenant_id", acc.TenantID,
			"last_scanned_at", acc.LastScannedAt,
			"interval_hours", acc.ScanIntervalHours,
		)
	}
}

// triggerScheduledScan fires a POST request to the ingestion service to start a scan.
func triggerScheduledScan(ctx context.Context, accountID, tenantID string) error {
	ingestionURL := os.Getenv("INGESTION_URL")
	if ingestionURL == "" {
		ingestionURL = "http://localhost:8081"
	}

	scanURL := ingestionURL + "/scan"

	// Marshal request body as JSON to safely handle any special characters in IDs
	body := map[string]string{
		"account_id": accountID,
		"tenant_id":  tenantID,
	}
	bodyBytes, err := json.Marshal(body)
	if err != nil {
		return fmt.Errorf("marshal request: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, scanURL, bytes.NewReader(bodyBytes))
	if err != nil {
		return fmt.Errorf("create request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")

	client := &http.Client{Timeout: 30 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return fmt.Errorf("post request failed: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode >= 400 {
		return fmt.Errorf("ingestion returned status %d", resp.StatusCode)
	}

	return nil
}

func main() {
	logging.Init("api")
	flushSentry := logging.InitSentry("api")
	defer flushSentry()

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
	if err := postgres.Migrate(migrationURL); err != nil {
		die("storage: migration failed", "error", err)
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

	// ── HTTP API ──────────────────────────────────────────────────────────────
	addr := os.Getenv("API_ADDR")
	if addr == "" {
		addr = ":8080"
	}

	mux := http.NewServeMux()
	h := api.New(store)
	h.Register(mux)
	mux.Handle("/metrics", promhttp.Handler())

	// ── Rate Limiting ─────────────────────────────────────────────────────────
	root := http.Handler(mux)
	if os.Getenv("DEV_MODE") != "true" {
		// 60 requests per minute = 1 req/sec rate, max burst of 60.
		limiter := middleware.NewRateLimiter(1.0, 60.0)
		root = limiter.Wrap(root)
		slog.Info("api: rate limiting enabled (60 req/min per tenant)")

		// Background ticker: clean up stale tenant buckets every 5 minutes.
		// Prevents memory leak in long-running instances. Temporary until Phase 2.14 (Redis).
		go func() {
			ticker := time.NewTicker(5 * time.Minute)
			defer ticker.Stop()
			for range ticker.C {
				limiter.CleanupStaleBuckets(1 * time.Hour) // Remove buckets inactive >1h
			}
		}()
	}

	// ── Auth ──────────────────────────────────────────────────────────────────
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

	// Background ticker: trigger scheduled auto-scans every 60 minutes.
	go func() {
		ticker := time.NewTicker(60 * time.Minute)
		defer ticker.Stop()
		for range ticker.C {
			scanScheduledAccounts(context.Background(), store, h)
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
