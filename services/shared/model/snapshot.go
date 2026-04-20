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

// SnapshotService records per-service breakdown for a single ghost snapshot.
type SnapshotService struct {
	ID           string  `json:"id"`
	SnapshotID   string  `json:"snapshot_id"`
	Service      string  `json:"service"`
	ResourceType string  `json:"resource_type"` // sub-classification (e.g. "volume", "snapshot", "ami")
	GhostCount   int     `json:"ghost_count"`
	MonthlyCost  float64 `json:"monthly_cost"`
	Currency     string  `json:"currency"`
}
