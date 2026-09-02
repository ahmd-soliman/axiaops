package model

import "time"

// Account represents a connected cloud provider account for an organization.
//
// AuthMethod selects which credential subset is populated:
//   - "access_key": AccessKeyID + SecretEncrypted are set; RoleARN and ExternalID are empty.
//   - "role":       RoleARN + ExternalID are set; AccessKeyID and SecretEncrypted are empty.
//
// Postgres CHECK constraints (migration 019_account_role_auth) enforce the same
// invariant at the database layer.
type Account struct {
	ID                string     `json:"id"`
	OrganizationID    string     `json:"organization_id"`
	Provider          string     `json:"provider"`   // "aws", "azure", "gcp"
	Label             string     `json:"label"`      // user-defined name
	AccountID         string     `json:"account_id"` // AWS account ID (e.g., "123456789012")
	AuthMethod        string     `json:"auth_method"` // "access_key" | "role"
	AccessKeyID       string     `json:"access_key_id"`           // visible — not secret; empty for role auth
	SecretEncrypted   string     `json:"-"`                       // never sent to client; empty for role auth
	RoleARN           string     `json:"role_arn,omitempty"`      // empty for access-key auth
	ExternalID        string     `json:"external_id,omitempty"`   // empty for access-key auth; safe to display
	Region            string     `json:"region"`
	Status            string     `json:"status"`              // connected | scanning | error | pending_role_setup
	LastScannedAt     *time.Time `json:"last_scanned_at"`     // null until first scan
	ScanIntervalHours int        `json:"scan_interval_hours"` // auto-scan interval in hours; default 24
	ErrorMessage      string     `json:"error_message,omitempty"` // most recent failure reason; surfaced in dashboard
	CreatedAt         time.Time  `json:"created_at"`
	BillingSource     string     `json:"billing_source"`
	CURDatabase       *string    `json:"cur_database,omitempty"`
	CURTable          *string    `json:"cur_table,omitempty"`
	CURWorkgroup      *string    `json:"cur_workgroup,omitempty"`
	CURResultsS3      *string    `json:"cur_results_s3,omitempty"`
	CURRegion         *string    `json:"cur_region,omitempty"`
}

// AuthMethod values. The string forms must match the auth_method CHECK
// constraint in migration 019_account_role_auth.
const (
	AuthMethodAccessKey = "access_key"
	AuthMethodRole      = "role"
)

// Account status values. Strings must match the status column writes in
// services/api and services/ingestion plus the accounts_role_fields_present
// CHECK in migration 019_account_role_auth.
const (
	AccountStatusConnected         = "connected"
	AccountStatusScanning          = "scanning"
	AccountStatusError             = "error"
	AccountStatusPendingRoleSetup  = "pending_role_setup"
)

const (
	BillingSourceCostExplorer = "cost_explorer"
	BillingSourceCURAthena    = "cur_athena"
)

const AccountStatusPendingCURDelivery = "pending_cur_delivery"
