package filefixture_test

import (
	"context"
	"encoding/json"
	"os"
	"testing"
	"time"

	"axiaops.io/ingestion/internal/model"
	"axiaops.io/ingestion/internal/provider/filefixture"
)

func writeFixture(t *testing.T, records []model.CostRecord) string {
	t.Helper()
	f, err := os.CreateTemp("", "fixture-*.json")
	if err != nil {
		t.Fatalf("create temp file: %v", err)
	}
	if err := json.NewEncoder(f).Encode(records); err != nil {
		t.Fatalf("encode fixture: %v", err)
	}
	f.Close()
	t.Cleanup(func() { os.Remove(f.Name()) })
	return f.Name()
}

func TestFetchCosts_ReturnsRecords(t *testing.T) {
	records := []model.CostRecord{
		{
			Provider:    "aws",
			AccountID:   "000000000000",
			Service:     "AmazonEC2",
			Region:      "eu-central-1",
			ResourceID:  "i-001",
			Amount:      100.00,
			Currency:    "USD",
			PeriodStart: time.Date(2026, 3, 1, 0, 0, 0, 0, time.UTC),
			PeriodEnd:   time.Date(2026, 3, 31, 0, 0, 0, 0, time.UTC),
			Tags:        map[string]string{"team": "platform"},
			FetchedAt:   time.Now().UTC(),
		},
	}

	path := writeFixture(t, records)
	client := filefixture.New(path)

	got, err := client.FetchCosts(context.Background(), time.Now(), time.Now())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("expected 1 record, got %d", len(got))
	}
	if got[0].Service != "AmazonEC2" {
		t.Errorf("expected AmazonEC2, got %s", got[0].Service)
	}
	if got[0].Amount != 100.00 {
		t.Errorf("expected 100.00, got %f", got[0].Amount)
	}
}

func TestFetchCosts_ReturnsAllRecords(t *testing.T) {
	records := make([]model.CostRecord, 5)
	for i := range records {
		records[i] = model.CostRecord{
			Provider: "aws", AccountID: "000000000000",
			Service: "AmazonS3", Region: "eu-central-1",
			Amount: float64(i + 1), Currency: "USD",
			PeriodStart: time.Date(2026, 3, 1, 0, 0, 0, 0, time.UTC),
			PeriodEnd:   time.Date(2026, 3, 31, 0, 0, 0, 0, time.UTC),
			FetchedAt:   time.Now().UTC(),
		}
	}

	path := writeFixture(t, records)
	got, err := filefixture.New(path).FetchCosts(context.Background(), time.Now(), time.Now())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(got) != 5 {
		t.Errorf("expected 5 records, got %d", len(got))
	}
}

func TestFetchCosts_FileNotFound(t *testing.T) {
	client := filefixture.New("/nonexistent/path/fixture.json")
	_, err := client.FetchCosts(context.Background(), time.Now(), time.Now())
	if err == nil {
		t.Error("expected error for missing file, got nil")
	}
}

func TestFetchCosts_InvalidJSON(t *testing.T) {
	f, err := os.CreateTemp("", "bad-fixture-*.json")
	if err != nil {
		t.Fatalf("create temp: %v", err)
	}
	f.WriteString("not valid json")
	f.Close()
	t.Cleanup(func() { os.Remove(f.Name()) })

	client := filefixture.New(f.Name())
	_, err = client.FetchCosts(context.Background(), time.Now(), time.Now())
	if err == nil {
		t.Error("expected error for invalid JSON, got nil")
	}
}

func TestName(t *testing.T) {
	client := filefixture.New("any.json")
	if client.Name() != "filefixture" {
		t.Errorf("expected name 'filefixture', got %s", client.Name())
	}
}
