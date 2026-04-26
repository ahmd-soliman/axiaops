package model

import "time"

// Account represents a connected cloud provider account for an organization.
type Account struct {
	ID                string     `json:"id"`
	OrganizationID    string     `json:"organization_id"`
	Provider          string     `json:"provider"`      // "aws", "azure", "gcp"
	Label             string     `json:"label"`         // user-defined name
	AccountID         string     `json:"account_id"`    // AWS account ID (e.g., "123456789012")
	AccessKeyID       string     `json:"access_key_id"` // visible — not secret
	SecretEncrypted   string     `json:"-"`             // never sent to client
	Region            string     `json:"region"`
	Status            string     `json:"status"`              // connected | scanning | error
	LastScannedAt     *time.Time `json:"last_scanned_at"`     // null until first scan
	ScanIntervalHours int        `json:"scan_interval_hours"` // auto-scan interval in hours; default 24
	CreatedAt         time.Time  `json:"created_at"`
}
