// Package model defines shared data structures used across the ingestion
// and analysis layers.
package model

import "time"

// GhostResource represents a cloud resource that is incurring cost but shows
// no meaningful usage — a zombie resource that is safe to review for removal.
type GhostResource struct {
	Provider   string            `json:"provider"`
	AccountID  string            `json:"account_id"`
	Service    string            `json:"service"`
	Region     string            `json:"region"`
	ResourceID string            `json:"resource_id"`
	Tags       map[string]string `json:"tags"`

	// Cost fields
	MonthlyCost float64 `json:"monthly_cost"`
	Currency    string  `json:"currency"`
	PeriodStart time.Time `json:"period_start"`
	PeriodEnd   time.Time `json:"period_end"`

	// Usage fields
	UsageMetric string  `json:"usage_metric"` // e.g. "CPUUtilization"
	UsageAvg    float64 `json:"usage_avg"`    // average value over the period
	UsageUnit   string  `json:"usage_unit"`   // e.g. "Percent", "Count"

	// Detection metadata
	Reason string `json:"reason"` // human-readable explanation
	Owner  string `json:"owner"`  // derived from tags["team"]
}
