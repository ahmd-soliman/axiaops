// main is the entry point for the AxiaOps ingestion service.
//
// Modes:
//  1. HTTP server (default) — listens on :8081, accepts POST /scan to run on demand.
//  2. One-shot CLI          — set RUN_ONCE=true to run a single ingestion and exit.
//
// The API service triggers scans via POST /scan with {"account_id","organization_id"}.
// Credentials for the account are read from the accounts table in the database.
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

	"axiaops.io/ingestion/internal/provider"
	"axiaops.io/ingestion/internal/provider/aws"
	"axiaops.io/shared/analyzer"
	"axiaops.io/shared/errors"
	"axiaops.io/shared/httpauth"
	"axiaops.io/shared/logging"
	"axiaops.io/shared/model"
	"axiaops.io/shared/notifications"
	"axiaops.io/shared/observability"
	"axiaops.io/shared/queue"
	"axiaops.io/shared/storage"
	"axiaops.io/shared/storage/postgres"
	"github.com/google/uuid"
	"github.com/prometheus/client_golang/prometheus"
)

// Prometheus metrics for ingestion service
var (
	// axiaops_ingestion_records_fetched_total: Total number of cost records fetched.
	// Labels: provider, organization_id.
	ingestionRecordsFetchedTotal = prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Name: "axiaops_ingestion_records_fetched_total",
			Help: "Total number of cost records fetched by the ingestion service.",
		},
		[]string{"provider", "organization_id"},
	)

	// axiaops_ingestion_records_saved_total: Total number of cost records successfully saved to the database.
	// Labels: provider, organization_id, status (inserted = brand-new row,
	// updated = existing row whose amount/tags were refreshed by the upsert,
	// discriminated via the row's xmax).
	ingestionRecordsSavedTotal = prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Name: "axiaops_ingestion_records_saved_total",
			Help: "Total number of cost records saved to the database.",
		},
		[]string{"provider", "organization_id", "status"},
	)

)

func init() {
	// Register ingestion metrics with the default Prometheus registry.
	prometheus.MustRegister(ingestionRecordsFetchedTotal)
	prometheus.MustRegister(ingestionRecordsSavedTotal)
}

// die logs a fatal error and exits with code 1.
func die(msg string, args ...any) {
	slog.Error(msg, args...)
	os.Exit(1)
}

func main() {
	logging.Init("ingestion")

	// ── Ingestion shared-secret HMAC (C-1, plan §3.4) ─────────────────────
	// Two-secret slot list (current + previous) from day 1 — the operator
	// can stage a `_NEXT` value on ingestion before flipping api over.
	// DEV_MODE allows empty: both ends fall back to a passthrough that
	// surfaces a one-shot warning if a signed request arrives anyway.
	ingestionSecrets, hmacSoftEnforce := loadIngestionSecrets(devModeEnabled())
	hmacMaxSkew := httpauth.LoadMaxSkew("INGESTION_HMAC_MAX_SKEW_SECONDS", httpauth.DefaultMaxSkew)
	observability.SetHMACEnforceMode(hmacSoftEnforce)
	// Emit the secret-slot fingerprint (lengths only, never bytes) so an
	// operator inspecting a running container can confirm rotation is staged
	// correctly without exposing the secret to log scrapers. See plan §4.5.
	slog.Info("hmac: secret slots loaded", "fingerprint", secretsFingerprint(ingestionSecrets))
	if hmacSoftEnforce && !devModeEnabled() {
		slog.Warn("hmac: SOFT_ENFORCE active — failures are logged but NOT rejected. " +
			"Transition flag only; flip INGESTION_HMAC_SOFT_ENFORCE=false after one stable cycle.")
	}

	store := newStore()

	retentionDays := 90
	if v := os.Getenv("COST_RECORDS_RETENTION_DAYS"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 {
			retentionDays = n
		}
	}

	dispatchRetentionDays := 90
	if v := os.Getenv("NOTIFICATION_DISPATCH_RETENTION_DAYS"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 {
			dispatchRetentionDays = n
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
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"status":"ok"}`))
	})
	// Metrics handler — observability.MetricsHandler merges the default
	// registry (this file's MustRegister'd ingestion counters) with the
	// observability package's private registry (Global.* — scan, license,
	// AWS, DB). See the helper's doc for why promhttp.Handler() alone is
	// wrong.
	mux.Handle("GET /metrics", observability.MetricsHandler())

	// Protected handlers are wrapped individually via httpauth.Middleware so
	// /health, /metrics, /livez, /readyz stay reachable by docker healthchecks
	// and Prometheus scrapers (no shared secret). Future ingestion endpoints
	// should explicitly opt in via protect() rather than opt out of a global
	// allowlist.
	protect := composeHMACProtect(ingestionSecrets, hmacMaxSkew, hmacSoftEnforce)
	mux.Handle("POST /scan", protect(http.HandlerFunc(scanHandler(store))))

	// Cross-account role verification — synchronous AssumeRole probe used by
	// the dashboard's "Verify and connect" flow. Stateless: no DB writes.
	mux.Handle("POST /v1/credentials/verify", protect(http.HandlerFunc(handleVerifyCredentials)))

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
	// Pass the *first* secret (the current one) so envelope-signed enqueues
	// from this binary's scheduler match what the worker verifies. Rotation
	// only affects the verifier side — the signer always uses current.
	q := queue.New(redisURL, ingestionURL, primarySecret(ingestionSecrets))
	defer func() { _ = q.Close() }()
	if redisURL != "" {
		startWorker(sigCtx, q, store, ingestionSecrets, hmacMaxSkew, hmacSoftEnforce)
		slog.Info("worker: started")
	} else {
		slog.Info("worker: skipped_no_redis")
	}

	// Background ticker: trigger scheduled auto-scans across all organizations.
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

	// Background ticker: expire past snoozes so dismissed_zombies stays accurate.
	// Runs immediately on startup then every SNOOZE_EXPIRY_INTERVAL (default 10 min).
	go func() {
		snoozeInterval := 10 * time.Minute
		if v := os.Getenv("SNOOZE_EXPIRY_INTERVAL"); v != "" {
			if d, err := time.ParseDuration(v); err == nil {
				snoozeInterval = d
			}
		}
		expireSnoozes(context.Background(), store)
		ticker := time.NewTicker(snoozeInterval)
		defer ticker.Stop()
		for {
			select {
			case <-sigCtx.Done():
				return
			case <-ticker.C:
				expireSnoozes(context.Background(), store)
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

	// Daily retention cleanup — runs at midnight UTC. Sweeps cost_records and
	// notification_dispatches in the same pass (both are global, RLS-bypass
	// deletes keyed on an age cutoff).
	go func() {
		for {
			now := time.Now().UTC()
			next := time.Date(now.Year(), now.Month(), now.Day()+1, 0, 0, 0, 0, time.UTC)
			select {
			case <-sigCtx.Done():
				return
			case <-time.After(time.Until(next)):
			}

			// Use sigCtx so an in-flight sweep is cancelled on shutdown rather
			// than outliving the graceful-drain window on an uncancellable DELETE.
			start := time.Now()
			cutoff := time.Now().UTC().AddDate(0, 0, -retentionDays)
			deleted, err := store.DeleteOldCostRecords(sigCtx, cutoff)
			if err != nil {
				slog.Error("cost_records.cleanup failed", "error", err)
			} else {
				slog.Info("cost_records.cleanup", "rows_deleted", deleted, "duration_ms", time.Since(start).Milliseconds())
			}

			start = time.Now()
			dispatchCutoff := time.Now().UTC().AddDate(0, 0, -dispatchRetentionDays)
			dDeleted, err := store.DeleteOldNotificationDispatches(sigCtx, dispatchCutoff)
			if err != nil {
				slog.Error("notification_dispatches.cleanup failed", "error", err)
			} else {
				slog.Info("notification_dispatches.cleanup", "rows_deleted", dDeleted, "duration_ms", time.Since(start).Milliseconds())
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
// runScan fetches costs, detects zombies, and writes results to the store.
// accountID is the internal DB account UUID; pass "" for one-shot/dev mode.
// Credentials are loaded from the DB when accountID is non-empty — and either
// decrypted (access-key auth) or assumed via STS (role auth) inside
// aws.NewForAccount, so this function no longer cares about the difference.
func runScan(ctx context.Context, store storage.Store, accountID string) error {
	// Production: require database credentials only
	if accountID == "" {
		if devModeEnabled() {
			slog.Warn("dev mode: using environment credentials - not recommended for production")
		} else {
			return fmt.Errorf("account ID required - database credentials only in production")
		}
	}

	var account model.Account
	var hasAccount bool

	if accountID != "" {
		var err error
		account, err = store.GetAccount(ctx, accountID)
		if err != nil {
			return fmt.Errorf("get account: %w", err)
		}
		hasAccount = true
	}

	// Before scanning, populate account_id (the customer's AWS account number)
	// if it is missing. Both auth paths return it via GetCallerIdentity inside
	// NewForAccount, so we just construct a client once and ask.
	if hasAccount && account.AccountID == "" {
		awsClient, err := aws.NewForAccount(ctx, account)
		if err == nil {
			account.AccountID = awsClient.AccountID()
			if err := store.SaveAccount(ctx, account); err != nil {
				return fmt.Errorf("runScan: failed to persist account_id for %s: %w", accountID, err)
			}
			slog.Info("runScan: populated account_id", "account_id", accountID, "aws_account_id", account.AccountID)
		}
	}

	var accountPtr *model.Account
	if hasAccount {
		accountPtr = &account
	}
	return runIngestionCore(ctx, store, accountID, accountPtr)
}

// runIngestionCore is the shared implementation used by runScan and the HTTP handler.
func runIngestionCore(ctx context.Context, store storage.Store, accountID string, account *model.Account) error {
	// In DEV_MODE, skip scan only if no account is configured
	if devModeEnabled() && account == nil {
		slog.Info("ingestion: DEV_MODE — skipping AWS scan (no account)", "account_id", accountID)
		return nil
	}

	// Require an account row for any real scan
	if account == nil {
		return fmt.Errorf("AWS credentials required - configure account in database")
	}

	var providers []provider.Provider

	awsClient, err := aws.NewForAccount(ctx, *account)
	if err != nil {
		return fmt.Errorf("aws init: %w", err)
	}
	providers = append(providers, awsClient)

	organizationID := storage.OrganizationIDFromCtx(ctx)
	if organizationID == "" {
		orgCode := "aws-" + awsClient.AccountID()
		org, err := store.UpsertOrganization(ctx, orgCode, orgCode)
		if err != nil {
			return fmt.Errorf("upsert organization: %w", err)
		}
		organizationID = org.ID
		ctx = storage.WithOrganizationID(ctx, organizationID)
		slog.Info("ingestion: using auto-created organization", "organization_id", organizationID, "org_code", orgCode)
	}

	end, start := dateRange()

	var allRecords []model.CostRecord
	for _, p := range providers {
		// Check for cancellation before each provider
		select {
		case <-ctx.Done():
			return ctx.Err()
		default:
		}

		records, err := p.FetchCosts(ctx, start, end)
		if err != nil {
			catErr := errors.Categorize(err, "fetch_costs")
			slog.Error("fetch failed",
				"provider", p.Name(),
				"error", err,
				"category", catErr.Category,
				"should_fail_scan", catErr.Category.ShouldFailScan(),
			)

			// Fail entire scan for credential/permission errors
			if catErr.Category.ShouldFailScan() {
				return fmt.Errorf("[%s] %w", p.Name(), catErr)
			}
			continue
		}

		// Populate internal_account_id for filtering on dashboard
		for i := range records {
			records[i].InternalAccountID = &accountID
		}

		inserted, updated, saveErr := store.Save(ctx, records)
		if saveErr != nil {
			return fmt.Errorf("[%s] save failed: %w", p.Name(), saveErr)
		}
		slog.Info("fetched records", "provider", p.Name(), "total", len(records), "inserted", inserted, "updated", updated)
		ingestionRecordsFetchedTotal.WithLabelValues(p.Name(), organizationID).Add(float64(len(records)))
		ingestionRecordsSavedTotal.WithLabelValues(p.Name(), organizationID, "inserted").Add(float64(inserted))
		ingestionRecordsSavedTotal.WithLabelValues(p.Name(), organizationID, "updated").Add(float64(updated))

		allRecords = append(allRecords, records...)
	}

	// Check for cancellation before usage fetching
	select {
	case <-ctx.Done():
		return ctx.Err()
	default:
	}

	// Fetch resource-level costs for services that support per-resource CE data.
	// These records have ResourceID populated, enabling Detect() to join with
	// usage AND per-resource drill-downs in the dashboard.
	resourceCosts, _ := awsClient.FetchResourceCosts(ctx, start, end)
	if len(resourceCosts) > 0 {
		slog.Info("fetched resource-level costs", "count", len(resourceCosts))
		for i := range resourceCosts {
			resourceCosts[i].InternalAccountID = &accountID
		}
		inserted, updated, saveErr := store.Save(ctx, resourceCosts)
		if saveErr != nil {
			return fmt.Errorf("save resource costs failed: %w", saveErr)
		}
		slog.Info("saved resource-level costs", "total", len(resourceCosts), "inserted", inserted, "updated", updated)
		ingestionRecordsFetchedTotal.WithLabelValues("aws", organizationID).Add(float64(len(resourceCosts)))
		ingestionRecordsSavedTotal.WithLabelValues("aws", organizationID, "inserted").Add(float64(inserted))
		ingestionRecordsSavedTotal.WithLabelValues("aws", organizationID, "updated").Add(float64(updated))
		allRecords = append(allRecords, resourceCosts...)
	}

	// Fetch Cost Explorer API costs (from Cost & Usage API).
	// This tracks the cost of AxiaOps's own Cost Explorer API calls.
	apiCosts, _ := awsClient.FetchCostExplorerAPICosts(ctx, start, end)
	if len(apiCosts) > 0 {
		slog.Info("fetched Cost Explorer API costs", "count", len(apiCosts))
		allRecords = append(allRecords, apiCosts...)
	}

	var usage []analyzer.UsageRecord
	var usageErr error
	if len(allRecords) > 0 {
		usage, usageErr = awsClient.FetchUsage(ctx, allRecords, start, end)
		if usageErr != nil {
			catErr := errors.Categorize(usageErr, "fetch_usage")
			slog.Error("fetch usage from cloudwatch failed, continuing without usage data",
				"error", usageErr,
				"category", catErr.Category,
			)
			// Continue with empty usage - zombie detection will work with cost data only
		} else {
			slog.Info("analysis: fetched usage records", "count", len(usage))
		}
	}

	// Use resource-level costs for Detect() — these have ResourceID populated
	// so they can be joined with usage data. Fall back to aggregate records if
	// resource-level fetch returned nothing.
	detectRecords := resourceCosts
	if len(detectRecords) == 0 {
		detectRecords = allRecords
	}
	zombies := analyzer.Detect(detectRecords, usage, accountID)

	// API-only zombie checks — each is non-fatal; a failure is logged and the
	// scan continues so that a single permissions gap doesn't block all findings.

	// Unattached Elastic IPs ($0.005/hour idle charge).
	eipZombies, eipErr := aws.DiscoverUnattachedEIPs(ctx, allRecords, awsClient, start, end, accountID)
	if eipErr != nil {
		catErr := errors.Categorize(eipErr, "discover_eips")
		slog.Error("discover unattached EIPs failed, continuing without EIP data",
			"error", eipErr,
			"category", catErr.Category,
		)
	} else {
		zombies = append(zombies, eipZombies...)
	}

	// Unattached EBS volumes (state=available, $0.08/GB-month for gp3).
	ebsVolZombies, ebsVolErr := aws.DiscoverUnattachedEBSVolumes(ctx, allRecords, awsClient, start, end, accountID)
	if ebsVolErr != nil {
		catErr := errors.Categorize(ebsVolErr, "discover_ebs_volumes")
		slog.Error("discover unattached EBS volumes failed, continuing",
			"error", ebsVolErr,
			"category", catErr.Category,
		)
	} else {
		zombies = append(zombies, ebsVolZombies...)
	}

	// Orphaned EBS snapshots (source volume deleted, not backing any AMI).
	snapZombies, snapErr := aws.DiscoverOrphanedEBSSnapshots(ctx, allRecords, awsClient, start, end, accountID)
	if snapErr != nil {
		catErr := errors.Categorize(snapErr, "discover_ebs_snapshots")
		slog.Error("discover orphaned EBS snapshots failed, continuing",
			"error", snapErr,
			"category", catErr.Category,
		)
	} else {
		zombies = append(zombies, snapZombies...)
	}

	// Stopped EC2 instances idle for more than 30 days (EBS storage still bills).
	stoppedZombies, stoppedErr := aws.DiscoverLongStoppedInstances(ctx, allRecords, awsClient, start, end, accountID)
	if stoppedErr != nil {
		catErr := errors.Categorize(stoppedErr, "discover_stopped_instances")
		slog.Error("discover long-stopped EC2 instances failed, continuing",
			"error", stoppedErr,
			"category", catErr.Category,
		)
	} else {
		zombies = append(zombies, stoppedZombies...)
	}

	// Old AMIs (>90 days, not in use) and their backing EBS snapshots.
	amiZombies, amiErr := aws.DiscoverOldAMIs(ctx, allRecords, awsClient, start, end, accountID)
	if amiErr != nil {
		catErr := errors.Categorize(amiErr, "discover_old_amis")
		slog.Error("discover old AMIs failed, continuing",
			"error", amiErr,
			"category", catErr.Category,
		)
	} else {
		zombies = append(zombies, amiZombies...)
	}

	// Wasteful CloudWatch Log Groups (no retention policy or zero stored bytes).
	logGroupZombies, logGroupErr := aws.DiscoverWastefulLogGroups(ctx, allRecords, awsClient, start, end, accountID)
	if logGroupErr != nil {
		catErr := errors.Categorize(logGroupErr, "discover_log_groups")
		slog.Error("discover wasteful CloudWatch log groups failed, continuing",
			"error", logGroupErr,
			"category", catErr.Category,
		)
	} else {
		zombies = append(zombies, logGroupZombies...)
	}

	// Orphaned manual RDS snapshots (source DB deleted, older than 30 days).
	rdsSnapZombies, rdsSnapErr := aws.DiscoverOrphanedRDSSnapshots(ctx, allRecords, awsClient, start, end, accountID)
	if rdsSnapErr != nil {
		catErr := errors.Categorize(rdsSnapErr, "discover_rds_snapshots")
		slog.Error("discover orphaned RDS snapshots failed, continuing",
			"error", rdsSnapErr,
			"category", catErr.Category,
		)
	} else {
		zombies = append(zombies, rdsSnapZombies...)
	}

	// Stale ECR images (untagged or older than 90 days, summarized per repository).
	ecrZombies, ecrErr := aws.DiscoverStaleECRImages(ctx, allRecords, awsClient, start, end, accountID)
	if ecrErr != nil {
		catErr := errors.Categorize(ecrErr, "discover_ecr_images")
		slog.Error("discover stale ECR images failed, continuing",
			"error", ecrErr,
			"category", catErr.Category,
		)
	} else {
		zombies = append(zombies, ecrZombies...)
	}

	// Unused Secrets Manager secrets (not accessed for >90 days, $0.40/secret/month).
	secretZombies, secretErr := aws.DiscoverUnusedSecrets(ctx, allRecords, awsClient, start, end, accountID)
	if secretErr != nil {
		catErr := errors.Categorize(secretErr, "discover_unused_secrets")
		slog.Error("discover unused secrets failed, continuing",
			"error", secretErr,
			"category", catErr.Category,
		)
	} else {
		zombies = append(zombies, secretZombies...)
	}

	// Idle CloudFront distributions (zero requests in lookback window).
	cfZombies, cfErr := aws.DiscoverIdleCloudFrontDistributions(ctx, allRecords, awsClient, start, end, accountID)
	if cfErr != nil {
		catErr := errors.Categorize(cfErr, "discover_cloudfront")
		slog.Error("discover idle CloudFront distributions failed, continuing",
			"error", cfErr,
			"category", catErr.Category,
		)
	} else {
		zombies = append(zombies, cfZombies...)
	}

	// Idle Kinesis data streams (zero incoming records in lookback window).
	kinesisZombies, kinesisErr := aws.DiscoverIdleKinesisStreams(ctx, allRecords, awsClient, start, end, accountID)
	if kinesisErr != nil {
		catErr := errors.Categorize(kinesisErr, "discover_kinesis")
		slog.Error("discover idle Kinesis streams failed, continuing",
			"error", kinesisErr,
			"category", catErr.Category,
		)
	} else {
		zombies = append(zombies, kinesisZombies...)
	}

	// Idle S3 buckets (zero requests, requires request metrics enabled).
	s3Zombies, s3Err := aws.DiscoverIdleS3Buckets(ctx, allRecords, awsClient, start, end, accountID)
	if s3Err != nil {
		catErr := errors.Categorize(s3Err, "discover_s3")
		slog.Error("discover idle S3 buckets failed, continuing",
			"error", s3Err,
			"category", catErr.Category,
		)
	} else {
		zombies = append(zombies, s3Zombies...)
	}

	// Incomplete S3 multipart uploads (aborted uploads >7 days old).
	s3MultipartZombies, s3MultipartErr := aws.DiscoverIncompleteMultipartUploads(ctx, allRecords, awsClient, start, end, accountID)
	if s3MultipartErr != nil {
		catErr := errors.Categorize(s3MultipartErr, "discover_s3_multipart")
		slog.Error("discover incomplete S3 multipart uploads failed, continuing",
			"error", s3MultipartErr,
			"category", catErr.Category,
		)
	} else {
		zombies = append(zombies, s3MultipartZombies...)
	}

	// Unused Route53 hosted zones (default records only, zero query activity).
	r53Zombies, r53Err := aws.DiscoverUnusedHostedZones(ctx, allRecords, awsClient, start, end, accountID)
	if r53Err != nil {
		catErr := errors.Categorize(r53Err, "discover_route53")
		slog.Error("discover unused Route53 hosted zones failed, continuing",
			"error", r53Err,
			"category", catErr.Category,
		)
	} else {
		zombies = append(zombies, r53Zombies...)
	}

	// Classify each zombie into a resource sub-type from its (service, usage
	// metric) pair. Detect() already sets this for CloudWatch-based zombies; the
	// API-only discoverers (EIP, EBS volume/snapshot, AMI, log group, …) don't,
	// so backfill any that are still empty. This populates zombie_records and —
	// via the per-(service, resource_type) breakdown below — the trend filter.
	for i := range zombies {
		if zombies[i].ResourceType == "" {
			zombies[i].ResourceType = analyzer.ResourceType(zombies[i].Service, zombies[i].UsageMetric)
		}
	}

	summary := analyzer.Summarize(zombies)
	slog.Info("analysis: detected zombie resources", "total", summary.TotalZombies, "potential_savings", fmt.Sprintf("%.2f %s/month", summary.PotentialMonthlySave, summary.Currency))
	observability.Global.ZombiesDetected.WithLabelValues(awsClient.Name(), organizationID).Set(float64(summary.TotalZombies))
	observability.Global.PotentialMonthlySaving.WithLabelValues(awsClient.Name(), organizationID).Set(summary.PotentialMonthlySave)

	if err := store.SaveZombies(ctx, zombies); err != nil {
		return fmt.Errorf("save zombies: %w", err)
	}
	slog.Info("storage: saved zombie records", "count", len(zombies))

	snap := model.ZombieSnapshot{
		ID:               uuid.New().String(),
		OrganizationID:   organizationID,
		AccountID:        accountID,
		SnapshotAt:       time.Now().UTC(),
		ZombieCount:      summary.TotalZombies,
		TotalMonthlyCost: summary.PotentialMonthlySave,
		Currency:         summary.Currency,
	}
	if snap.Currency == "" {
		snap.Currency = "USD"
	}
	if err := store.SaveSnapshot(ctx, snap); err != nil {
		// Non-fatal: log and continue — zombie records are already saved.
		slog.Error("storage: save snapshot failed", "error", err)
	} else {
		slog.Info("storage: saved zombie snapshot", "zombie_count", snap.ZombieCount, "total_monthly_cost", snap.TotalMonthlyCost)

		// Persist the per-(service, resource_type) breakdown for trend filtering.
		// One row per sub-type (e.g. AmazonEC2/volume, AmazonEC2/instance) so the
		// trend resource-type filter can scope a service's history to one kind;
		// ListSnapshotsByService SUMs across sub-types when no resource_type is given.
		var svcRows []model.SnapshotService
		for _, b := range analyzer.SummarizeByServiceResourceType(zombies) {
			svcRows = append(svcRows, model.SnapshotService{
				ID:           uuid.New().String(),
				SnapshotID:   snap.ID,
				Service:      b.Service,
				ResourceType: b.ResourceType,
				ZombieCount:  b.Zombies,
				MonthlyCost:  b.Savings,
				Currency:     snap.Currency,
			})
		}
		if err := store.SaveSnapshotServices(ctx, svcRows); err != nil {
			slog.Error("storage: save snapshot services failed", "error", err)
		} else {
			slog.Info("storage: saved snapshot services", "count", len(svcRows))
		}

		// Notify the org's enabled channels about the completed scan. Placed
		// inside the snapshot-saved branch so the dispatch row's snapshot_id FK
		// always resolves. Best-effort + non-fatal — see DispatchForScan.
		dispatchNotifications(ctx, store, snap, summary, accountID)
	}

	// allRecords already contains resourceCosts (appended in the FetchResourceCosts
	// branch above) plus the per-provider FetchCosts results plus apiCosts.
	// AnnotateAll over the full union surfaces individual resources in /resources.
	resources := analyzer.AnnotateAll(allRecords, usage, zombies)
	// Set internal_account_id on all resources
	for i := range resources {
		resources[i].InternalAccountID = accountID
	}
	if err := store.SaveResources(ctx, resources); err != nil {
		return fmt.Errorf("save resources: %w", err)
	}
	slog.Info("storage: saved resource records", "total", len(resources), "zombies", len(zombies))
	return nil
}

// dispatchNotifications fans a completed scan out to the org's enabled
// notification channels. Best-effort + non-fatal: DispatchForScan logs and
// records every error internally and never returns one, so a notification
// problem can't fail a scan. The transports are stateless (they decrypt config
// per-call via ENCRYPTION_KEY), so constructing them per scan is cheap.
// PUBLIC_HOST builds the dashboard deep-link; empty omits it.
func dispatchNotifications(ctx context.Context, store storage.Store, snap model.ZombieSnapshot, summary analyzer.Summary, accountID string) {
	transports := notifications.DefaultTransports(notifications.NewEmailTransport())
	notifications.NewDispatcher(store, transports, os.Getenv("PUBLIC_HOST")).
		DispatchForScan(ctx, snap, summary, accountID)
}

func newStore() storage.Store {
	ctx := context.Background()
	dbURL := os.Getenv("DATABASE_URL")
	if dbURL == "" {
		die("storage: DATABASE_URL is required")
	}
	runtimeAdminURL := os.Getenv("RUNTIME_ADMIN_DATABASE_URL")
	if runtimeAdminURL == "" {
		// Symmetric with the api: the bypass pool (adminPool) handles the
		// scheduled-scan account enumeration (ListAllAccounts) across all orgs.
		// With no runtime-admin URL, NewWithRuntimeAdmin falls back to the
		// RLS-bound app pool and the scan loop silently enumerates zero
		// accounts. DEV_MODE runs a single shared pool by design; refuse to
		// start in any other build. See docs/AUTHENTICATION.md (§5).
		if !devModeEnabled() {
			die("storage: RUNTIME_ADMIN_DATABASE_URL is required outside DEV_MODE — without the RLS-bypass connection the pool falls back to the app pool and scheduled scans silently enumerate zero accounts")
		}
		runtimeAdminURL = dbURL
	}
	s, err := postgres.NewWithRuntimeAdmin(ctx, dbURL, runtimeAdminURL)
	if err != nil {
		die("storage: postgres init failed", "error", err)
	}
	slog.Info("storage: using PostgreSQL")
	return s
}

// expireSnoozes marks snoozed dismissals whose snoozed_until has passed as revoked.
func expireSnoozes(ctx context.Context, store storage.Store) {
	start := time.Now()
	n, err := store.ExpireSnoozes(ctx)
	if err != nil {
		slog.Error("snooze_expiry: failed", "error", err)
		return
	}
	if n > 0 {
		slog.Info("snooze_expiry: expired", "count", n, "duration_ms", time.Since(start).Milliseconds())
	}
}

// scanScheduledAccounts checks all accounts across all organizations and
// triggers scans for those overdue. Every connected account is scanned on
// schedule, unconditionally.
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
		// Drafts have no credentials yet — every tick would re-enqueue a
		// guaranteed-to-fail scan and stuck-mark the row. Skip until the
		// customer finishes the role-onboarding flow (PATCH /v1/accounts/{id}).
		if acc.Status == model.AccountStatusPendingRoleSetup {
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
			OrganizationID: acc.OrganizationID,
			AccountID:      acc.ID,
			EnqueuedAt:     time.Now().UTC(),
		}
		if err := q.Enqueue(ctx, job); err != nil {
			slog.Error("scan.failed_to_trigger", "account_id", acc.ID, "organization_id", acc.OrganizationID, "error", err)
			continue
		}
		slog.Info("scan.scheduled", "account_id", acc.ID, "organization_id", acc.OrganizationID, "interval_hours", acc.ScanIntervalHours)
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
