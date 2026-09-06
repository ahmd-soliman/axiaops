// Package model defines the shared data structures used across the ingestion
// service. CostRecord is the single normalized representation of a cloud cost
// entry, regardless of which provider it originated from.
package model

import (
	"fmt"
	"regexp"
	"time"
)

// CostRecord is the normalized cost entry across all cloud providers.
type CostRecord struct {
	Provider          string            `json:"provider"`            // aws | gcp | azure
	AccountID         string            `json:"account_id"`          // AWS account, GCP project, Azure subscription
	InternalAccountID *string           `json:"internal_account_id"` // AxiaOps internal account UUID
	Service           string            `json:"service"`             // e.g. AmazonEC2, Cloud Storage
	Region            string            `json:"region"`
	ResourceID        string            `json:"resource_id"`
	Amount            float64           `json:"amount"`
	Currency          string            `json:"currency"`
	PeriodStart       time.Time         `json:"period_start"`
	PeriodEnd         time.Time         `json:"period_end"`
	Tags              map[string]string `json:"tags"`
	FetchedAt         time.Time         `json:"fetched_at"`
	CostBasis         string            `json:"cost_basis"` // "billed" | "list_price"
}

// ValidationError carries the field name that failed and a human-readable
// reason. Returned by Validate methods on shared model types.
type ValidationError struct {
	Field   string
	Message string
}

func (e *ValidationError) Error() string {
	return fmt.Sprintf("invalid %s: %s", e.Field, e.Message)
}

// validProviders is the set of provider identifiers AxiaOps recognises.
var validProviders = map[string]struct{}{
	"aws":   {},
	"gcp":   {},
	"azure": {},
}

// currencyRE matches an ISO-4217 currency code: three uppercase letters.
var currencyRE = regexp.MustCompile(`^[A-Z]{3}$`)

// Validate enforces the strict invariants every CostRecord must satisfy:
//   - Provider in {aws, gcp, azure}
//   - AccountID non-empty
//   - Service registered in KnownServices
//   - Region either empty / a CE pseudo-value / matches AWSRegionLike (when AWS)
//   - Amount non-negative
//   - Currency matches ISO-4217 (^[A-Z]{3}$)
//   - PeriodStart and PeriodEnd both non-zero, with Start < End
//
// Tags are not validated — empty or arbitrary values are legal. ResourceID is
// optional (top-level CE rows have none). FetchedAt is informational and not
// checked.
//
// Callers decide whether to fail-fast or log-and-skip on a validation error.
// Tests should fail; the production scan path may prefer to drop invalid rows
// and continue (that decision lives at the call site, not here).
func (c CostRecord) Validate() error {
	if _, ok := validProviders[c.Provider]; !ok {
		return &ValidationError{Field: "provider", Message: fmt.Sprintf("%q is not in {aws, gcp, azure}", c.Provider)}
	}
	if c.AccountID == "" {
		return &ValidationError{Field: "account_id", Message: "must be non-empty"}
	}
	if !IsKnownService(c.Service) {
		return &ValidationError{Field: "service", Message: fmt.Sprintf("%q is not in KnownServices — register it in services.go", c.Service)}
	}
	if !regionAcceptable(c.Provider, c.Region) {
		return &ValidationError{Field: "region", Message: fmt.Sprintf("%q is not a valid AWS region or CE pseudo-value", c.Region)}
	}
	if c.Amount < 0 {
		return &ValidationError{Field: "amount", Message: fmt.Sprintf("%.4f is negative", c.Amount)}
	}
	if !currencyRE.MatchString(c.Currency) {
		return &ValidationError{Field: "currency", Message: fmt.Sprintf("%q is not an ISO-4217 code", c.Currency)}
	}
	if c.PeriodStart.IsZero() {
		return &ValidationError{Field: "period_start", Message: "must be non-zero"}
	}
	if c.PeriodEnd.IsZero() {
		return &ValidationError{Field: "period_end", Message: "must be non-zero"}
	}
	if !c.PeriodStart.Before(c.PeriodEnd) {
		return &ValidationError{Field: "period_end", Message: fmt.Sprintf("%s must be after period_start %s", c.PeriodEnd, c.PeriodStart)}
	}
	return nil
}

// regionAcceptable returns true if region is legitimate for the given
// provider. For AWS, that means empty / a CE pseudo-value / AWSRegionLike.
// For GCP and Azure it accepts any non-empty string (cloud-specific region
// validators can be added when those providers ship).
func regionAcceptable(provider, region string) bool {
	if provider != "aws" {
		return true // permissive until GCP/Azure validators are written
	}
	if _, isPseudo := CEPseudoRegions[region]; isPseudo {
		return true
	}
	return AWSRegionLike(region)
}

const (
	CostBasisBilled    = "billed"
	CostBasisListPrice = "list_price"
)
