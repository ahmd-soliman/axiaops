// main is the entry point for the ingestion and analysis service.
// It initialises all configured cloud provider clients, fetches cost data for
// the last 30 days, stores the normalised records in SQLite, runs zombie
// detection, and then serves the results via a simple HTTP API.
//
// New providers are registered in the providers slice — no other changes are
// required.
package main

import (
	"context"
	"log"
	"net/http"
	"os"
	"strconv"
	"time"

	"axiaops.io/ingestion/internal/analyzer"
	"axiaops.io/ingestion/internal/api"
	"axiaops.io/ingestion/internal/model"
	"axiaops.io/ingestion/internal/provider"
	"axiaops.io/ingestion/internal/provider/aws"
	"axiaops.io/ingestion/internal/provider/filefixture"
	"axiaops.io/ingestion/internal/storage/sqlite"
)

func main() {
	ctx := context.Background()

	// ── Storage ──────────────────────────────────────────────────────────────
	dbPath := os.Getenv("DB_PATH")
	if dbPath == "" {
		dbPath = "axiaops.db"
	}
	store, err := sqlite.New(dbPath)
	if err != nil {
		log.Fatalf("storage: init failed: %v", err)
	}
	defer store.Close()

	// ── Providers ─────────────────────────────────────────────────────────────
	var providers []provider.Provider

	if os.Getenv("DEV_MODE") == "true" {
		fixturePath := os.Getenv("FIXTURE_PATH")
		if fixturePath == "" {
			fixturePath = "fixtures/costs.json"
		}
		providers = append(providers, filefixture.New(fixturePath))
	} else {
		accountID := os.Getenv("AWS_ACCOUNT_ID")
		if accountID == "" {
			log.Fatal("AWS_ACCOUNT_ID is required")
		}
		awsClient, err := aws.New(ctx, accountID)
		if err != nil {
			log.Fatalf("aws: init failed: %v", err)
		}
		providers = append(providers, awsClient)
	}

	// ── Ingestion ─────────────────────────────────────────────────────────────
	end, start := dateRange()

	var allRecords []model.CostRecord
	for _, p := range providers {
		records, err := p.FetchCosts(ctx, start, end)
		if err != nil {
			log.Printf("[%s] fetch failed: %v", p.Name(), err)
			continue
		}
		inserted, err := store.Save(ctx, records)
		if err != nil {
			log.Fatalf("[%s] save failed: %v", p.Name(), err)
		}
		skipped := int64(len(records)) - inserted
		log.Printf("[%s] fetched %d records — inserted %d, skipped %d duplicates",
			p.Name(), len(records), inserted, skipped)
		allRecords = append(allRecords, records...)
	}

	// ── Analysis ──────────────────────────────────────────────────────────────
	usagePath := os.Getenv("USAGE_PATH")
	if usagePath == "" {
		usagePath = "fixtures/usage.json"
	}
	usage, err := analyzer.LoadUsageFixture(usagePath)
	if err != nil {
		log.Fatalf("analyzer: load usage: %v", err)
	}

	ghosts := analyzer.Detect(allRecords, usage)
	summary := analyzer.Summarize(ghosts)

	log.Printf("analysis: %d ghost resources detected — potential savings %.2f %s/month",
		summary.TotalGhosts, summary.PotentialMonthlySave, summary.Currency)
	for svc, s := range summary.ByService {
		log.Printf("  %-35s %d ghost(s), %.2f %s", svc, s.Ghosts, s.Savings, summary.Currency)
	}

	// ── HTTP API ──────────────────────────────────────────────────────────────
	addr := os.Getenv("API_ADDR")
	if addr == "" {
		addr = ":8080"
	}

	mux := http.NewServeMux()
	h := api.New(ghosts, summary)
	h.Register(mux)

	log.Printf("api: listening on %s  →  GET /ghosts  GET /summary", addr)
	if err := http.ListenAndServe(addr, h.Handler(mux)); err != nil {
		log.Fatalf("api: server error: %v", err)
	}
}

// dateRange returns the ingestion window.
//
// Priority:
//  1. START_DATE + END_DATE env vars (format: 2006-01-02) — explicit range
//  2. DAYS_BACK env var — number of days back from today (default 30)
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
