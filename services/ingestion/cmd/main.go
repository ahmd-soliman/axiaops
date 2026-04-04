// main is the entry point for the ingestion service. It initializes all
// configured cloud provider clients, fetches cost data for the last 30 days,
// and writes the normalized records as JSON to stdout. New providers are
// registered in the providers slice — no other changes are required.
package main

import (
	"context"
	"encoding/json"
	"log"
	"os"
	"time"

	"axiaops.io/ingestion/internal/provider"
	"axiaops.io/ingestion/internal/provider/aws"
	"axiaops.io/ingestion/internal/provider/s3fixture"
)

func main() {
	ctx := context.Background()

	var providers []provider.Provider

	if os.Getenv("DEV_MODE") == "true" {
		// Dev: read fixture data from LocalStack S3
		s3, err := s3fixture.New(ctx)
		if err != nil {
			log.Fatalf("s3fixture: init failed: %v", err)
		}
		providers = append(providers, s3)
	} else {
		// Production: real AWS Cost Explorer
		accountID := os.Getenv("AWS_ACCOUNT_ID")
		if accountID == "" {
			log.Fatal("AWS_ACCOUNT_ID is required")
		}
		awsClient, err := aws.New(ctx, accountID)
		if err != nil {
			log.Fatalf("aws: init failed: %v", err)
		}
		providers = append(providers, awsClient)
		// gcp.New(...) — add more providers here
	}

	end := time.Now().UTC().Truncate(24 * time.Hour)
	start := end.AddDate(0, -1, 0) // last 30 days

	enc := json.NewEncoder(os.Stdout)
	enc.SetIndent("", "  ")

	for _, p := range providers {
		records, err := p.FetchCosts(ctx, start, end)
		if err != nil {
			log.Printf("[%s] fetch failed: %v", p.Name(), err)
			continue
		}
		if err := enc.Encode(records); err != nil {
			log.Fatalf("encode failed: %v", err)
		}
	}
}
