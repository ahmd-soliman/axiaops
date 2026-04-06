// main is the entry point for the AxiaOps ingestion job.
// It fetches costs and usage from configured cloud providers,
// runs zombie detection, writes results to the database, and exits.
// Designed to be triggered by a scheduler (cron, EventBridge) or manually.
//
// The API service (services/api) reads results from the database.
package main

import (
	"context"
	"fmt"
	"log"
	"os"
	"strconv"
	"time"

	"axiaops.io/ingestion/internal/provider"
	"axiaops.io/ingestion/internal/provider/aws"
	"axiaops.io/ingestion/internal/provider/filefixture"
	"axiaops.io/shared/analyzer"
	"axiaops.io/shared/model"
	"axiaops.io/shared/storage"
	"axiaops.io/shared/storage/postgres"
	"axiaops.io/shared/storage/sqlite"
)

func main() {
	ctx := context.Background()
	var err error

	// ── Storage ──────────────────────────────────────────────────────────────
	var store storage.Store
	if dbURL := os.Getenv("DATABASE_URL"); dbURL != "" {
		s, err := postgres.New(ctx, dbURL)
		if err != nil {
			log.Fatalf("storage: postgres init failed: %v", err)
		}
		defer s.Close()
		store = s
		log.Println("storage: using PostgreSQL")
	} else {
		dbPath := os.Getenv("DB_PATH")
		if dbPath == "" {
			dbPath = "axiaops.db"
		}
		s, err := sqlite.New(dbPath)
		if err != nil {
			log.Fatalf("storage: sqlite init failed: %v", err)
		}
		defer s.Close()
		store = s
		log.Println("storage: using SQLite")
	}

	// ── Providers ─────────────────────────────────────────────────────────────
	var providers []provider.Provider
	var awsClient *aws.Client

	if os.Getenv("DEV_MODE") == "true" {
		fixturePath := os.Getenv("FIXTURE_PATH")
		if fixturePath == "" {
			fixturePath = "fixtures/costs.json"
		}
		providers = append(providers, filefixture.New(fixturePath))
	} else {
		awsClient, err = aws.New(ctx)
		if err != nil {
			log.Fatalf("aws: init failed: %v", err)
		}
		providers = append(providers, awsClient)
	}

	// ── Fetch costs ───────────────────────────────────────────────────────────
	end, start := dateRange()

	var allRecords []model.CostRecord
	for _, p := range providers {
		records, err := p.FetchCosts(ctx, start, end)
		if err != nil {
			log.Printf("[%s] fetch failed: %v", p.Name(), err)
			continue
		}
		inserted, saveErr := store.Save(ctx, records)
		if saveErr != nil {
			log.Fatalf("[%s] save failed: %v", p.Name(), saveErr)
		}
		skipped := int64(len(records)) - inserted
		log.Printf("[%s] fetched %d records — inserted %d, skipped %d duplicates",
			p.Name(), len(records), inserted, skipped)
		allRecords = append(allRecords, records...)
	}

	// ── Fetch usage ───────────────────────────────────────────────────────────
	var usage []analyzer.UsageRecord

	if os.Getenv("DEV_MODE") == "true" {
		usagePath := os.Getenv("USAGE_PATH")
		if usagePath == "" {
			usagePath = "fixtures/usage.json"
		}
		usage, err = analyzer.LoadUsageFixture(usagePath)
		if err != nil {
			log.Fatalf("analyzer: load usage fixture: %v", err)
		}
		log.Printf("analysis: loaded %d usage records from fixture", len(usage))
	} else {
		usage, err = awsClient.FetchUsage(ctx, allRecords, start, end)
		if err != nil {
			log.Fatalf("analyzer: fetch usage from cloudwatch: %v", err)
		}
		log.Printf("analysis: fetched %d usage records from cloudwatch", len(usage))
	}

	// ── Detect ghosts ─────────────────────────────────────────────────────────
	ghosts := analyzer.Detect(allRecords, usage)

	// EIPs are detected separately — attachment status is the signal, not CloudWatch.
	if os.Getenv("DEV_MODE") != "true" {
		eipGhosts := aws.DiscoverUnattachedEIPs(ctx, allRecords, awsClient.AccountID(), start, end)
		ghosts = append(ghosts, eipGhosts...)
	}

	summary := analyzer.Summarize(ghosts)

	log.Printf("analysis: %d ghost resources detected — potential savings %.2f %s/month",
		summary.TotalGhosts, summary.PotentialMonthlySave, summary.Currency)
	for svc, s := range summary.ByService {
		log.Printf("  %-35s %d ghost(s), %.2f %s", svc, s.Ghosts, s.Savings, summary.Currency)
	}

	// ── Save ghosts to DB ─────────────────────────────────────────────────────
	if err := store.SaveGhosts(ctx, ghosts); err != nil {
		log.Fatalf("storage: save ghosts: %v", err)
	}
	log.Printf("storage: saved %d ghost records to database", len(ghosts))
	log.Printf("ingestion: complete — results available via the API service")
}

// dateRange returns the ingestion window.
func dateRange() (end, start time.Time) {
	const layout = "2006-01-02"
	end = time.Now().UTC().Truncate(24 * time.Hour)

	if s, e := os.Getenv("START_DATE"), os.Getenv("END_DATE"); s != "" && e != "" {
		parsed, err := time.Parse(layout, s)
		if err != nil {
			log.Fatalf("START_DATE invalid (expected YYYY-MM-DD): %v", err)
		}
		start = parsed
		parsed, err = time.Parse(layout, e)
		if err != nil {
			log.Fatalf("END_DATE invalid (expected YYYY-MM-DD): %v", err)
		}
		end = parsed
		log.Printf("date range: %s → %s (explicit)", start.Format(layout), end.Format(layout))
		return
	}

	days := 30
	if d := os.Getenv("DAYS_BACK"); d != "" {
		n, err := strconv.Atoi(d)
		if err != nil || n < 1 {
			log.Fatalf("DAYS_BACK must be a positive integer, got: %s", d)
		}
		days = n
	}
	start = end.AddDate(0, 0, -days)
	log.Printf("date range: %s → %s (%d days)", start.Format(layout), end.Format(layout), days)
	return
}

// suppress unused import warning
var _ = fmt.Sprintf
