// main is the entry point for the AxiaOps ingestion service.
//
// Modes:
//  1. HTTP server (default) — listens on :8081, accepts POST /scan to run on demand.
//  2. One-shot CLI          — set RUN_ONCE=true to run a single ingestion and exit.
//
// The API service triggers scans via POST /scan with {"account_id","tenant_id"}.
// Credentials for the account are read from the accounts table in the database.
package main

import (
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

	"axiaops.io/ingestion/internal/provider"
	"axiaops.io/ingestion/internal/provider/aws"
	"axiaops.io/shared/analyzer"
	"axiaops.io/shared/crypto"
	"axiaops.io/shared/logging"
	"axiaops.io/shared/model"
	"axiaops.io/shared/queue"
	"axiaops.io/shared/storage"
	"axiaops.io/shared/storage/postgres"
	"github.com/google/uuid"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promhttp"
)

// Prometheus metrics for ingestion service
var (
	// axiaops_ingestion_records_fetched_total: Total number of cost records fetched.
	// Labels: provider, tenant_id.
	ingestionRecordsFetchedTotal = prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Name: "axiaops_ingestion_records_fetched_total",
			Help: "Total number of cost records fetched by the ingestion service.",
		},
		[]string{"provider", "tenant_id"},
	)

	// axiaops_ingestion_records_saved_total: Total number of cost records successfully saved to the database.
	// Labels: provider, tenant_id, status (inserted/skipped).
	ingestionRecordsSavedTotal = prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Name: "axiaops_ingestion_records_saved_total",
			Help: "Total number of cost records saved to the database.",
		},
		[]string{"provider", "tenant_id", "status"},
	)

	// axiaops_ghosts_detected_total: Total number of ghost resources detected in the current scan.
	// Labels: tenant_id, provider.
	ingestionGhostsDetectedTotal = prometheus.NewGaugeVec(
		prometheus.GaugeOpts{
			Name: "axiaops_ingestion_ghosts_detected_total",
			Help: "Total number of ghost resources detected in the current scan.",
		},
		[]string{"tenant_id", "provider"},
	)

	// axiaops_potential_monthly_savings_usd: Current potential monthly savings in USD.
	// Labels: tenant_id, provider.
	ingestionPotentialMonthlySavings = prometheus.NewGaugeVec(
		prometheus.GaugeOpts{
			Name: "axiaops_potential_monthly_savings_usd",
			Help: "Current potential monthly savings in USD.",
		},
		[]string{"tenant_id", "provider"},
	)
)

func init() {
	// Register ingestion metrics with the default Prometheus registry.
	prometheus.MustRegister(ingestionRecordsFetchedTotal)
	prometheus.MustRegister(ingestionRecordsSavedTotal)
	prometheus.MustRegister(ingestionGhostsDetectedTotal)
	prometheus.MustRegister(ingestionPotentialMonthlySavings)
}

// die logs a fatal error and exits with code 1.
func die(msg string, args ...any) {
	slog.Error(msg, args...)
	os.Exit(1)
}

func main() {
	logging.Init("ingestion")

	store := newStore()

	retentionDays := 90
	if v := os.Getenv("COST_RECORDS_RETENTION_DAYS"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 {
			retentionDays = n
		}
	}

	if os.Getenv("RUN_ONCE") == "true" {
		if err := runScan(context.Background(), store, ""); err != nil {
			die("ingestion: one-shot run failed", "error", err)
		}
		return
	}

	// HTTP server mode — stays running, accepts scan requests from the API.
	mux := http.NewServeMux()
	mux.HandleFunc("GET /health", func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	})
	// Metrics handler
	mux.Handle("GET /metrics", promhttp.Handler())

	mux.HandleFunc("POST /scan", func(w http.ResponseWriter, r *http.Request) {
		var req struct {
			AccountID string `json:"account_id"`
			TenantID  string `json:"tenant_id"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			slog.Error("scan: invalid request", "error", err)
			http.Error(w, "invalid request", http.StatusBadRequest)
			return
		}

		ctx := storage.WithTenantID(context.Background(), req.TenantID)

		if err := runScan(ctx, store, req.AccountID); err != nil {
			slog.Error("scan: ingestion failed", "account_id", req.AccountID, "error", err)
			_ = store.UpdateAccountStatus(ctx, req.AccountID, "error")
			http.Error(w, "ingestion failed", http.StatusInternalServerError)
			return
		}
		_ = store.UpdateAccountStatus(ctx, req.AccountID, "connected")
		w.WriteHeader(http.StatusOK)
	})

	port := os.Getenv("INGESTION_PORT")
	if port == "" {
		port = "8081"
	}

	// ── Queue Worker ──────────────────────────────────────────────────────────
	// When REDIS_URL is set, start a worker that dequeues and executes scan jobs.
	// When unset, the API uses the sync fallback (POST /scan) — worker is a no-op.
	sigCtx, sigCancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer sigCancel()

	redisURL := os.Getenv("REDIS_URL")
	ingestionURL := "http://localhost:" + port
	q := queue.New(redisURL, ingestionURL)
	defer func() { _ = q.Close() }()
	if redisURL != "" {
		startWorker(sigCtx, q, store)
		slog.Info("worker: started")
	} else {
		slog.Info("worker: skipped_no_redis")
	}

	// Background ticker: trigger scheduled auto-scans across all tenants.
	go func() {
		scanInterval := 60 * time.Minute
		if v := os.Getenv("SCAN_INTERVAL"); v != "" {
			if d, err := time.ParseDuration(v); err == nil {
				scanInterval = d
			}
		}
		scanScheduledAccounts(context.Background(), store, q)
		ticker := time.NewTicker(scanInterval)
		defer ticker.Stop()
		for {
			select {
			case <-sigCtx.Done():
				return
			case <-ticker.C:
				scanScheduledAccounts(context.Background(), store, q)
			}
		}
	}()

	// Start HTTP server in a goroutine
	server := &http.Server{
		Addr:    ":" + port,
		Handler: mux,
	}

	// Run server in background; will block until signal or error
	errCh := make(chan error, 1)
	go func() {
		slog.Info("ingestion: listening", "port", port)
		errCh <- server.ListenAndServe()
	}()

	// Daily cost_records retention cleanup — runs at midnight UTC.
	go func() {
		for {
			now := time.Now().UTC()
			next := time.Date(now.Year(), now.Month(), now.Day()+1, 0, 0, 0, 0, time.UTC)
			select {
			case <-sigCtx.Done():
				return
			case <-time.After(time.Until(next)):
			}
			start := time.Now()
			cutoff := time.Now().UTC().AddDate(0, 0, -retentionDays)
			deleted, err := store.DeleteOldCostRecords(context.Background(), cutoff)
			if err != nil {
				slog.Error("cost_records.cleanup failed", "error", err)
			} else {
				slog.Info("cost_records.cleanup", "rows_deleted", deleted, "duration_ms", time.Since(start).Milliseconds())
			}
		}
	}()

	// Wait for either: (1) shutdown signal received, or (2) server error
	select {
	case err := <-errCh:
		// Server exited with error
		if err != nil && err != http.ErrServerClosed {
			die("ingestion: server failed", "error", err)
		}
	case <-sigCtx.Done():
		// SIGTERM or SIGINT received — graceful shutdown
		slog.Warn("ingestion: shutdown signal received, draining requests")
		shutdownStart := time.Now()

		// Give in-flight requests up to 30 seconds to complete
		shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer shutdownCancel()

		// server.Shutdown waits for all in-flight requests to complete (with timeout)
		if err := server.Shutdown(shutdownCtx); err != nil && err != context.DeadlineExceeded {
			slog.Error("ingestion: shutdown error", "error", err)
		}

		// Close database connection pool
		if closer, ok := store.(interface{ Close() error }); ok {
			if err := closer.Close(); err != nil {
				slog.Error("ingestion: db close error", "error", err)
			}
		}

		shutdownDuration := time.Since(shutdownStart).Seconds()
		slog.Info("ingestion: shutdown complete", "duration_seconds", fmt.Sprintf("%.2f", shutdownDuration))
	}
}

// scanAWS supplies per-account static credentials. Nil uses the default chain.
type scanAWS struct {
	AccessKeyID string
	SecretKey   string
	Region      string
}

// runScan fetches costs, detects ghosts, and writes results to the store.
// accountID is the internal DB account UUID; pass "" for one-shot/dev mode.
// Credentials are loaded from the DB when accountID is non-empty.
func runScan(ctx context.Context, store storage.Store, accountID string) error {
	var keys *scanAWS

	if accountID != "" {
		account, err := store.GetAccount(ctx, accountID)
		if err != nil {
			return fmt.Errorf("get account: %w", err)
		}
		if account.SecretEncrypted == "" {
			return fmt.Errorf("account %s has no credentials configured", accountID)
		}
		secret, err := crypto.Decrypt(account.SecretEncrypted)
		if err != nil {
			return fmt.Errorf("decrypt credentials: %w", err)
		}
		keys = &scanAWS{
			AccessKeyID: account.AccessKeyID,
			SecretKey:   secret,
			Region:      account.Region,
		}
	}

	return runIngestionCore(ctx, store, accountID, keys)
}

// runIngestionCore is the shared implementation used by runScan and the HTTP handler.
func runIngestionCore(ctx context.Context, store storage.Store, accountID string, keys *scanAWS) error {
	if os.Getenv("DEV_MODE") == "true" {
		slog.Info("ingestion: DEV_MODE — skipping AWS scan", "account_id", accountID)
		return nil
	}
	var providers []provider.Provider

	var awsClient *aws.Client
	var err error
	if keys != nil {
		awsClient, err = aws.NewWithStaticCredentials(ctx, keys.AccessKeyID, keys.SecretKey, keys.Region)
	} else {
		awsClient, err = aws.New(ctx)
	}
	if err != nil {
		return fmt.Errorf("aws init: %w", err)
	}
	providers = append(providers, awsClient)

	tenantID := storage.TenantIDFromCtx(ctx)
	if tenantID == "" {
		orgCode := "aws-" + awsClient.AccountID()
		tenant, err := store.UpsertTenant(ctx, orgCode, orgCode)
		if err != nil {
			return fmt.Errorf("upsert tenant: %w", err)
		}
		tenantID = tenant.ID
		ctx = storage.WithTenantID(ctx, tenantID)
		slog.Info("ingestion: using auto-created tenant", "tenant_id", tenantID, "org_code", orgCode)
	}

	end, start := dateRange()

	var allRecords []model.CostRecord
	for _, p := range providers {
		records, err := p.FetchCosts(ctx, start, end)
		if err != nil {
			slog.Error("fetch failed", "provider", p.Name(), "error", err)
			continue
		}
		inserted, saveErr := store.Save(ctx, records)
		if saveErr != nil {
			return fmt.Errorf("[%s] save failed: %w", p.Name(), saveErr)
		}
		skipped := int64(len(records)) - inserted
		slog.Info("fetched records", "provider", p.Name(), "total", len(records), "inserted", inserted, "skipped", skipped)
		ingestionRecordsFetchedTotal.WithLabelValues(p.Name(), tenantID).Add(float64(len(records)))
		ingestionRecordsSavedTotal.WithLabelValues(p.Name(), tenantID, "inserted").Add(float64(inserted))
		ingestionRecordsSavedTotal.WithLabelValues(p.Name(), tenantID, "skipped").Add(float64(skipped))

		allRecords = append(allRecords, records...)
	}

	usage, err := awsClient.FetchUsage(ctx, allRecords, start, end)
	if err != nil {
		return fmt.Errorf("fetch usage from cloudwatch: %w", err)
	}
	slog.Info("analysis: fetched usage records", "count", len(usage))

	ghosts := analyzer.Detect(allRecords, usage)

	eipGhosts := aws.DiscoverUnattachedEIPs(ctx, allRecords, awsClient.AccountID(), start, end)
	ghosts = append(ghosts, eipGhosts...)

	summary := analyzer.Summarize(ghosts)
	slog.Info("analysis: detected ghost resources", "total", summary.TotalGhosts, "potential_savings", fmt.Sprintf("%.2f %s/month", summary.PotentialMonthlySave, summary.Currency))
	ingestionGhostsDetectedTotal.WithLabelValues(tenantID, awsClient.Name()).Set(float64(summary.TotalGhosts))
	ingestionPotentialMonthlySavings.WithLabelValues(tenantID, awsClient.Name()).Set(summary.PotentialMonthlySave)

	if err := store.SaveGhosts(ctx, ghosts); err != nil {
		return fmt.Errorf("save ghosts: %w", err)
	}
	slog.Info("storage: saved ghost records", "count", len(ghosts))

	snap := model.GhostSnapshot{
		ID:               uuid.New().String(),
		AccountID:        accountID,
		SnapshotAt:       time.Now().UTC(),
		GhostCount:       summary.TotalGhosts,
		TotalMonthlyCost: summary.PotentialMonthlySave,
		Currency:         summary.Currency,
	}
	if snap.Currency == "" {
		snap.Currency = "USD"
	}
	if err := store.SaveSnapshot(ctx, snap); err != nil {
		// Non-fatal: log and continue — ghost records are already saved.
		slog.Error("storage: save snapshot failed", "error", err)
	} else {
		slog.Info("storage: saved ghost snapshot", "ghost_count", snap.GhostCount, "total_monthly_cost", snap.TotalMonthlyCost)
	}

	resources := analyzer.AnnotateAll(allRecords, usage, ghosts)
	if err := store.SaveResources(ctx, resources); err != nil {
		return fmt.Errorf("save resources: %w", err)
	}
	slog.Info("storage: saved resource records", "total", len(resources), "ghosts", len(ghosts))
	return nil
}

func newStore() storage.Store {
	ctx := context.Background()
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
	s, err := postgres.NewWithOwner(ctx, dbURL, migrationURL)
	if err != nil {
		die("storage: postgres init failed", "error", err)
	}
	slog.Info("storage: using PostgreSQL")
	return s
}

// scanScheduledAccounts checks all accounts across all tenants and triggers scans for those overdue.
func scanScheduledAccounts(ctx context.Context, store storage.Store, q queue.Queue) {
	accounts, err := store.ListAllAccounts(ctx)
	if err != nil {
		slog.Error("scan-scheduler: failed to list accounts", "error", err)
		return
	}
	now := time.Now()
	for _, acc := range accounts {
		if acc.Status == "scanning" {
			continue
		}
		if acc.ScanIntervalHours < 0 {
			continue
		}
		isOverdue := acc.LastScannedAt == nil
		if !isOverdue {
			nextScan := acc.LastScannedAt.Add(time.Duration(acc.ScanIntervalHours) * time.Hour)
			isOverdue = now.After(nextScan)
		}
		if !isOverdue {
			continue
		}
		job := queue.ScanJob{
			TenantID:   acc.TenantID,
			AccountID:  acc.ID,
			EnqueuedAt: time.Now().UTC(),
		}
		if err := q.Enqueue(ctx, job); err != nil {
			slog.Error("scan.failed_to_trigger", "account_id", acc.ID, "tenant_id", acc.TenantID, "error", err)
			continue
		}
		slog.Info("scan.scheduled", "account_id", acc.ID, "tenant_id", acc.TenantID, "interval_hours", acc.ScanIntervalHours)
	}
}

func dateRange() (end, start time.Time) {
	const layout = "2006-01-02"
	end = time.Now().UTC().Truncate(24 * time.Hour)

	if s, e := os.Getenv("START_DATE"), os.Getenv("END_DATE"); s != "" && e != "" {
		parsed, err := time.Parse(layout, s)
		if err != nil {
			die("START_DATE invalid", "date", s, "error", err)
		}
		start = parsed
		parsed, err = time.Parse(layout, e)
		if err != nil {
			die("END_DATE invalid", "date", e, "error", err)
		}
		end = parsed
		return
	}

	days := 30
	if d := os.Getenv("DAYS_BACK"); d != "" {
		n, err := strconv.Atoi(d)
		if err != nil || n < 1 {
			die("DAYS_BACK must be a positive integer", "value", d)
		}
		days = n
	}
	start = end.AddDate(0, 0, -days)
	return
}
