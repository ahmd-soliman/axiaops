// main is the entry point for the ingestion and analysis service.
// It initialises all configured cloud provider clients, runs an initial
// ingestion, and then serves the results via a simple HTTP API.
//
// POST /ingest triggers a fresh ingestion run without restarting the service.
package main

import (
	"context"
	"fmt"
	"log"
	"net/http"
	"os"
	"strconv"
	"time"

	"axiaops.io/ingestion/internal/analyzer"
	"axiaops.io/ingestion/internal/api"
	"axiaops.io/ingestion/internal/middleware"
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
	var awsClient *aws.Client

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
		awsClient, err = aws.New(ctx, accountID)
		if err != nil {
			log.Fatalf("aws: init failed: %v", err)
		}
		providers = append(providers, awsClient)
	}

	// ── Ingest function ───────────────────────────────────────────────────────
	// runIngestion fetches costs, usage, and runs zombie detection.
	// Called once at startup and again on every POST /ingest request.
	runIngestion := func() ([]model.GhostResource, analyzer.Summary, error) {
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
				return nil, analyzer.Summary{}, fmt.Errorf("[%s] save failed: %w", p.Name(), saveErr)
			}
			skipped := int64(len(records)) - inserted
			log.Printf("[%s] fetched %d records — inserted %d, skipped %d duplicates",
				p.Name(), len(records), inserted, skipped)
			allRecords = append(allRecords, records...)
		}

		var usage []analyzer.UsageRecord
		if os.Getenv("DEV_MODE") == "true" {
			usagePath := os.Getenv("USAGE_PATH")
			if usagePath == "" {
				usagePath = "fixtures/usage.json"
			}
			usage, err = analyzer.LoadUsageFixture(usagePath)
			if err != nil {
				return nil, analyzer.Summary{}, fmt.Errorf("analyzer: load usage fixture: %w", err)
			}
			log.Printf("analysis: loaded %d usage records from fixture", len(usage))
		} else {
			usage, err = awsClient.FetchUsage(ctx, allRecords, start, end)
			if err != nil {
				return nil, analyzer.Summary{}, fmt.Errorf("analyzer: fetch usage from cloudwatch: %w", err)
			}
			log.Printf("analysis: fetched %d usage records from cloudwatch", len(usage))
		}

		ghosts := analyzer.Detect(allRecords, usage)
		summary := analyzer.Summarize(ghosts)

		log.Printf("analysis: %d ghost resources detected — potential savings %.2f %s/month",
			summary.TotalGhosts, summary.PotentialMonthlySave, summary.Currency)
		for svc, s := range summary.ByService {
			log.Printf("  %-35s %d ghost(s), %.2f %s", svc, s.Ghosts, s.Savings, summary.Currency)
		}

		return ghosts, summary, nil
	}

	// ── Initial ingestion ─────────────────────────────────────────────────────
	ghosts, summary, err := runIngestion()
	if err != nil {
		log.Fatalf("ingestion: initial run failed: %v", err)
	}

	// ── HTTP API ──────────────────────────────────────────────────────────────
	addr := os.Getenv("API_ADDR")
	if addr == "" {
		addr = ":8080"
	}

	mux := http.NewServeMux()
	h := api.New(ghosts, summary, runIngestion)
	h.Register(mux)

	// ── Auth ──────────────────────────────────────────────────────────────────
	var root http.Handler = h.Handler(mux)
	kindeIssuer := os.Getenv("KINDE_ISSUER")
	if kindeIssuer == "" {
		log.Println("auth: KINDE_ISSUER not set — running without authentication")
	} else {
		auth, err := middleware.NewAuth(ctx, kindeIssuer)
		if err != nil {
			log.Fatalf("auth: init failed: %v", err)
		}
		log.Printf("auth: JWT verification enabled (issuer: %s)", kindeIssuer)
		root = auth.Wrap(root)
	}

	log.Printf("api: listening on %s  →  GET /ghosts  GET /summary  POST /ingest", addr)
	if err := http.ListenAndServe(addr, root); err != nil {
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
