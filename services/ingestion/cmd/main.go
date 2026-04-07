// main is the entry point for the AxiaOps ingestion service.
//
// Modes:
//   1. HTTP server (default) — listens on :8081, accepts POST /scan to run on demand.
//   2. One-shot CLI          — set RUN_ONCE=true to run a single ingestion and exit.
//
// The API service triggers scans via POST /scan with {"account_id","tenant_id"}.
// Credentials for the account are read from the accounts table in the database.
package main

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"os"
	"strconv"
	"time"

	"axiaops.io/ingestion/internal/provider"
	"axiaops.io/ingestion/internal/provider/aws"
	"axiaops.io/shared/analyzer"
	"axiaops.io/shared/crypto"
	"axiaops.io/shared/model"
	"axiaops.io/shared/storage"
	"axiaops.io/shared/storage/postgres"
	"axiaops.io/shared/storage/sqlite"
)

func main() {
	store := newStore()

	if os.Getenv("RUN_ONCE") == "true" {
		if err := runIngestion(context.Background(), store); err != nil {
			log.Fatalf("ingestion: %v", err)
		}
		return
	}

	// HTTP server mode — stays running, accepts scan requests from the API.
	mux := http.NewServeMux()
	mux.HandleFunc("GET /health", func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	})
	mux.HandleFunc("POST /scan", func(w http.ResponseWriter, r *http.Request) {
		var req struct {
			AccountID string `json:"account_id"`
			TenantID  string `json:"tenant_id"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			http.Error(w, "invalid request", http.StatusBadRequest)
			return
		}

		ctx := storage.WithTenantID(context.Background(), req.TenantID)

		account, err := store.GetAccount(ctx, req.AccountID)
		if err != nil {
			log.Printf("scan: account %s not found: %v", req.AccountID, err)
			http.Error(w, "account not found", http.StatusNotFound)
			return
		}

		if account.SecretEncrypted == "" {
			log.Printf("scan: account %s has no secret configured", req.AccountID)
			http.Error(w, "no credentials configured", http.StatusUnprocessableEntity)
			return
		}
		secret, err := crypto.Decrypt(account.SecretEncrypted)
		if err != nil {
			log.Printf("scan: decrypt failed: %v", err)
			http.Error(w, "credential error", http.StatusInternalServerError)
			return
		}
		os.Setenv("AWS_ACCESS_KEY_ID", account.AccessKeyID)
		os.Setenv("AWS_SECRET_ACCESS_KEY", secret)
		os.Setenv("AWS_DEFAULT_REGION", account.Region)

		if err := runIngestion(ctx, store); err != nil {
			log.Printf("scan: ingestion failed: %v", err)
			http.Error(w, "ingestion failed", http.StatusInternalServerError)
			return
		}
		w.WriteHeader(http.StatusOK)
	})

	port := os.Getenv("INGESTION_PORT")
	if port == "" {
		port = "8081"
	}
	log.Printf("ingestion: listening on :%s", port)
	if err := http.ListenAndServe(":"+port, mux); err != nil {
		log.Fatalf("ingestion: server failed: %v", err)
	}
}

// runIngestion fetches costs, detects ghosts, and writes results to the store.
func runIngestion(ctx context.Context, store storage.Store) error {
	var providers []provider.Provider

	awsClient, err := aws.New(ctx)
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
		log.Printf("ingestion: using auto-created tenant %s (%s)", tenantID, orgCode)
	}

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
			return fmt.Errorf("[%s] save failed: %w", p.Name(), saveErr)
		}
		skipped := int64(len(records)) - inserted
		log.Printf("[%s] fetched %d records — inserted %d, skipped %d duplicates",
			p.Name(), len(records), inserted, skipped)
		allRecords = append(allRecords, records...)
	}

	usage, err := awsClient.FetchUsage(ctx, allRecords, start, end)
	if err != nil {
		return fmt.Errorf("fetch usage from cloudwatch: %w", err)
	}
	log.Printf("analysis: fetched %d usage records from cloudwatch", len(usage))

	ghosts := analyzer.Detect(allRecords, usage)

	eipGhosts := aws.DiscoverUnattachedEIPs(ctx, allRecords, awsClient.AccountID(), start, end)
	ghosts = append(ghosts, eipGhosts...)

	summary := analyzer.Summarize(ghosts)
	log.Printf("analysis: %d ghost resources — potential savings %.2f %s/month",
		summary.TotalGhosts, summary.PotentialMonthlySave, summary.Currency)

	if err := store.SaveGhosts(ctx, ghosts); err != nil {
		return fmt.Errorf("save ghosts: %w", err)
	}
	log.Printf("storage: saved %d ghost records", len(ghosts))

	resources := analyzer.AnnotateAll(allRecords, usage, ghosts)
	if err := store.SaveResources(ctx, resources); err != nil {
		return fmt.Errorf("save resources: %w", err)
	}
	log.Printf("storage: saved %d resource records (%d ghosts)", len(resources), len(ghosts))
	return nil
}

func newStore() storage.Store {
	ctx := context.Background()
	if dbURL := os.Getenv("DATABASE_URL"); dbURL != "" {
		migrationURL := os.Getenv("MIGRATION_DATABASE_URL")
		if migrationURL == "" {
			migrationURL = dbURL
		}
		if err := postgres.Migrate(migrationURL); err != nil {
			log.Fatalf("storage: migration failed: %v", err)
		}
		s, err := postgres.New(ctx, dbURL)
		if err != nil {
			log.Fatalf("storage: postgres init failed: %v", err)
		}
		log.Println("storage: using PostgreSQL")
		return s
	}
	dbPath := os.Getenv("DB_PATH")
	if dbPath == "" {
		dbPath = "axiaops.db"
	}
	s, err := sqlite.New(dbPath)
	if err != nil {
		log.Fatalf("storage: sqlite init failed: %v", err)
	}
	log.Println("storage: using SQLite")
	return s
}

func dateRange() (end, start time.Time) {
	const layout = "2006-01-02"
	end = time.Now().UTC().Truncate(24 * time.Hour)

	if s, e := os.Getenv("START_DATE"), os.Getenv("END_DATE"); s != "" && e != "" {
		parsed, err := time.Parse(layout, s)
		if err != nil {
			log.Fatalf("START_DATE invalid: %v", err)
		}
		start = parsed
		parsed, err = time.Parse(layout, e)
		if err != nil {
			log.Fatalf("END_DATE invalid: %v", err)
		}
		end = parsed
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
	return
}
