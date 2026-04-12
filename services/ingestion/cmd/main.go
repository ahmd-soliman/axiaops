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
	"axiaops.io/shared/storage"
	"axiaops.io/shared/storage/postgres"
	sentry "github.com/getsentry/sentry-go"
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

// die logs a fatal error, flushes Sentry, and exits with code 1.
// Safe to call at any point — sentry.Flush is a no-op if Sentry is not initialised.
func die(msg string, args ...any) {
	slog.Error(msg, args...)
	sentry.Flush(2 * time.Second)
	os.Exit(1)
}

func main() {
	logging.Init("ingestion")
	flushSentry := logging.InitSentry("ingestion")
	defer flushSentry()

	store := newStore()

	if os.Getenv("RUN_ONCE") == "true" {
		if err := runIngestion(context.Background(), store, "", nil); err != nil {
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

		account, err := store.GetAccount(ctx, req.AccountID)
		if err != nil {
			slog.Error("scan: account not found", "account_id", req.AccountID, "error", err)
			http.Error(w, "account not found", http.StatusNotFound)
			return
		}

		if account.SecretEncrypted == "" {
			slog.Warn("scan: account has no secret configured", "account_id", req.AccountID)
			http.Error(w, "no credentials configured", http.StatusUnprocessableEntity)
			return
		}
		secret, err := crypto.Decrypt(account.SecretEncrypted)
		if err != nil {
			slog.Error("scan: decrypt failed", "account_id", req.AccountID, "error", err)
			http.Error(w, "credential error", http.StatusInternalServerError)
			return
		}

		if err := runIngestion(ctx, store, req.AccountID, &scanAWS{
			AccessKeyID: account.AccessKeyID,
			SecretKey:   secret,
			Region:      account.Region,
		}); err != nil {
			slog.Error("scan: ingestion failed", "account_id", req.AccountID, "error", err)
			http.Error(w, "ingestion failed", http.StatusInternalServerError)
			return
		}
		w.WriteHeader(http.StatusOK)
	})

	port := os.Getenv("INGESTION_PORT")
	if port == "" {
		port = "8081"
	}

	// ── Graceful Shutdown ────────────────────────────────────────────────────────
	// Set up signal handling for SIGTERM/SIGINT (App Runner sends SIGTERM on shutdown)
	shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer shutdownCancel()

	sigCtx, sigCancel := signal.NotifyContext(shutdownCtx, os.Interrupt, syscall.SIGTERM)
	defer sigCancel()

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

// scanAWS supplies per-account static credentials for POST /scan. Nil uses the default chain (env, shared config).
type scanAWS struct {
	AccessKeyID string
	SecretKey   string
	Region      string
}

// runIngestion fetches costs, detects ghosts, and writes results to the store.
// accountID is the internal DB account UUID from the accounts table; pass ""
// when running in one-shot mode (no per-account tracking).
func runIngestion(ctx context.Context, store storage.Store, accountID string, keys *scanAWS) error {
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
		die("storage: DATABASE_URL is required (SQLite is tests-only)")
	}
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
	slog.Info("storage: using PostgreSQL")
	return s
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
