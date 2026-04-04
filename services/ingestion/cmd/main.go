// main is the entry point for the ingestion service. It initializes all
// configured cloud provider clients, fetches cost data for the last 30 days,
// and stores the normalized records in SQLite. New providers are registered
// in the providers slice — no other changes are required.
package main

import (
	"context"
	"log"
	"os"
	"time"

	"axiaops.io/ingestion/internal/provider"
	"axiaops.io/ingestion/internal/provider/aws"
	"axiaops.io/ingestion/internal/provider/filefixture"
	"axiaops.io/ingestion/internal/storage/sqlite"
)

func main() {
	ctx := context.Background()

	// Storage
	dbPath := os.Getenv("DB_PATH")
	if dbPath == "" {
		dbPath = "axiaops.db"
	}
	store, err := sqlite.New(dbPath)
	if err != nil {
		log.Fatalf("storage: init failed: %v", err)
	}
	defer store.Close()

	// Providers
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

	end := time.Now().UTC().Truncate(24 * time.Hour)
	start := end.AddDate(0, -1, 0)

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
		log.Printf("[%s] fetched %d records — inserted %d, skipped %d duplicates", p.Name(), len(records), inserted, skipped)
	}
}
