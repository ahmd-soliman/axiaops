// Package model defines the shared data structures used across the ingestion
// service. CostRecord is the single normalized representation of a cloud cost
// entry, regardless of which provider it originated from.
package model

import "time"

// CostRecord is the normalized cost entry across all cloud providers.
type CostRecord struct {
	Provider          string            `json:"provider"`              // aws | gcp | azure
	AccountID         string            `json:"account_id"`            // AWS account, GCP project, Azure subscription
	InternalAccountID *string           `json:"internal_account_id"`   // AxiaOps internal account UUID
	Service           string            `json:"service"`               // e.g. AmazonEC2, Cloud Storage
	Region            string            `json:"region"`
	ResourceID        string            `json:"resource_id"`
	Amount            float64           `json:"amount"`
	Currency          string            `json:"currency"`
	PeriodStart       time.Time         `json:"period_start"`
	PeriodEnd         time.Time         `json:"period_end"`
	Tags              map[string]string `json:"tags"`
	FetchedAt         time.Time         `json:"fetched_at"`
}
