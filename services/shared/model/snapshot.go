package model

import "time"

// GhostSnapshot records the state of ghost detection at the time of a scan.
// One snapshot is written per ingestion run, forming the savings history series.
type GhostSnapshot struct {
	ID               string    `json:"id"`
	TenantID         string    `json:"-"`
	AccountID        string    `json:"account_id"`
	SnapshotAt       time.Time `json:"snapshot_at"`
	GhostCount       int       `json:"ghost_count"`
	TotalMonthlyCost float64   `json:"total_monthly_cost"`
	Currency         string    `json:"currency"`
}
