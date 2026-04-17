// Package model defines shared data structures used across the ingestion
// and analysis layers.
package model

import "time"

// ResourceRecord represents a cloud resource discovered during ingestion.
// Every resource with a cost entry is stored here; IsGhost is true for
// resources that also meet the zombie detection criteria.
type ResourceRecord struct {
	Provider          string            `json:"provider"`
	AccountID         string            `json:"account_id"`          // AWS account number, GCP project, etc.
	InternalAccountID string            `json:"internal_account_id"` // UUID from accounts table
	Service           string            `json:"service"`
	Region            string            `json:"region"`
	ResourceID        string            `json:"resource_id"`
	Tags              map[string]string `json:"tags"`

	// Cost fields
	MonthlyCost float64   `json:"monthly_cost"`
	Currency    string    `json:"currency"`
	PeriodStart time.Time `json:"period_start"`
	PeriodEnd   time.Time `json:"period_end"`

	// Usage fields — empty string / 0 when no CloudWatch data is available.
	UsageMetric string  `json:"usage_metric"`
	UsageAvg    float64 `json:"usage_avg"`
	UsageUnit   string  `json:"usage_unit"`

	// Detection metadata
	IsGhost bool   `json:"is_ghost"`
	Reason  string `json:"reason"` // non-empty only when IsGhost is true
	Owner   string `json:"owner"`  // derived from tags["team"]
}
