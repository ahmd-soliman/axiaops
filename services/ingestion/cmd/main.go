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
	end := time.Now().UTC().Truncate(24 * time.Hour)
	start := end.AddDate(0, -1, 0)

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
	api.New(ghosts, summary).Register(mux)

	log.Printf("api: listening on %s  →  GET /ghosts  GET /summary", addr)
	if err := http.ListenAndServe(addr, mux); err != nil {
		log.Fatalf("api: server error: %v", err)
	}
}
