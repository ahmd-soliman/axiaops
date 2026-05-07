package model_test

import (
	"errors"
	"testing"
	"time"

	"axiaops.io/shared/model"
)

func validCost() model.CostRecord {
	return model.CostRecord{
		Provider:    "aws",
		AccountID:   "000000000000",
		Service:     "AmazonEC2",
		Region:      "eu-central-1",
		Amount:      12.34,
		Currency:    "USD",
		PeriodStart: time.Date(2026, 4, 1, 0, 0, 0, 0, time.UTC),
		PeriodEnd:   time.Date(2026, 4, 2, 0, 0, 0, 0, time.UTC),
	}
}

func TestCostRecord_Validate_HappyPath(t *testing.T) {
	if err := validCost().Validate(); err != nil {
		t.Fatalf("expected nil, got %v", err)
	}
}

func TestCostRecord_Validate_PseudoRegionsAccepted(t *testing.T) {
	for _, region := range []string{"", "global", "NoRegion"} {
		c := validCost()
		c.Region = region
		if err := c.Validate(); err != nil {
			t.Errorf("region %q: expected nil, got %v", region, err)
		}
	}
}

func TestCostRecord_Validate_GCPProviderSkipsRegionShape(t *testing.T) {
	c := validCost()
	c.Provider = "gcp"
	c.Service = "AmazonS3" // GCP service set not yet defined; reuse known name
	c.Region = "us-central1"
	if err := c.Validate(); err != nil {
		t.Errorf("expected nil for non-AWS region, got %v", err)
	}
}

func TestCostRecord_Validate_RejectsBadFields(t *testing.T) {
	cases := []struct {
		name  string
		mut   func(*model.CostRecord)
		field string
	}{
		{"unknown provider", func(c *model.CostRecord) { c.Provider = "ibm" }, "provider"},
		{"empty account_id", func(c *model.CostRecord) { c.AccountID = "" }, "account_id"},
		{"unknown service", func(c *model.CostRecord) { c.Service = "EC2" }, "service"},
		{"bad aws region", func(c *model.CostRecord) { c.Region = "nope" }, "region"},
		{"negative amount", func(c *model.CostRecord) { c.Amount = -0.01 }, "amount"},
		{"lowercase currency", func(c *model.CostRecord) { c.Currency = "usd" }, "currency"},
		{"4-letter currency", func(c *model.CostRecord) { c.Currency = "USDX" }, "currency"},
		{"zero period_start", func(c *model.CostRecord) { c.PeriodStart = time.Time{} }, "period_start"},
		{"zero period_end", func(c *model.CostRecord) { c.PeriodEnd = time.Time{} }, "period_end"},
		{"start equals end", func(c *model.CostRecord) { c.PeriodEnd = c.PeriodStart }, "period_end"},
		{"start after end", func(c *model.CostRecord) { c.PeriodEnd = c.PeriodStart.Add(-time.Hour) }, "period_end"},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			c := validCost()
			tc.mut(&c)
			err := c.Validate()
			if err == nil {
				t.Fatalf("expected error, got nil")
			}
			var ve *model.ValidationError
			if !errors.As(err, &ve) {
				t.Fatalf("expected *ValidationError, got %T (%v)", err, err)
			}
			if ve.Field != tc.field {
				t.Errorf("Field = %q, want %q", ve.Field, tc.field)
			}
		})
	}
}

func TestIsKnownService(t *testing.T) {
	if !model.IsKnownService("AmazonEC2") {
		t.Error("AmazonEC2 should be known")
	}
	if model.IsKnownService("EC2") {
		t.Error("EC2 should NOT be known (wrong format)")
	}
	if model.IsKnownService("") {
		t.Error("empty string should NOT be known")
	}
}

func TestAWSRegionLike(t *testing.T) {
	good := []string{"us-east-1", "eu-central-1", "ap-southeast-2", "ca-central-1"}
	bad := []string{"", "us", "global", "NoRegion", "us-east", "us-east-x"}

	for _, r := range good {
		if !model.AWSRegionLike(r) {
			t.Errorf("AWSRegionLike(%q) = false, want true", r)
		}
	}
	for _, r := range bad {
		if model.AWSRegionLike(r) {
			t.Errorf("AWSRegionLike(%q) = true, want false", r)
		}
	}
}
