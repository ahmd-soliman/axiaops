// Package model — DismissAction represents an organization's decision to dismiss or
// snooze a zombie resource.  Dismissals survive scan cycles because zombie_records
// are replaced wholesale on every scan; the stable fingerprint is
// (organization_id, account_id, provider, service, region, resource_id).
package model

import "time"

// DismissAction is a single dismiss-or-snooze record stored in dismissed_zombies.
//
// MonthlyCost / Currency are last-known cost from the most recent zombie_records
// row that matches this dismissal's fingerprint. nil when the underlying resource
// has been deleted upstream and no longer appears in any scan (orphaned dismissal).
// Source: LEFT JOIN against zombie_records in ListActiveDismissals.
type DismissAction struct {
	ID           int64      `json:"id"`
	AccountID    string     `json:"account_id"` // internal account UUID
	Provider     string     `json:"provider"`
	Service      string     `json:"service"`
	Region       string     `json:"region"`
	ResourceID   string     `json:"resource_id"`
	Action       string     `json:"action"` // "dismiss" | "snooze"
	Reason       string     `json:"reason"` // see constants below
	Note         string     `json:"note,omitempty"`
	SnoozedUntil *time.Time `json:"snoozed_until,omitempty"` // nil when action="dismiss"
	DismissedBy  string     `json:"dismissed_by,omitempty"`  // email / user identifier
	CreatedAt    time.Time  `json:"created_at"`
	RevokedAt    *time.Time `json:"revoked_at,omitempty"`
	RevokedBy    string     `json:"revoked_by,omitempty"`
	MonthlyCost  *float64   `json:"monthly_cost,omitempty"`
	Currency     string     `json:"currency,omitempty"`
}

// Valid action values.
const (
	DismissActionDismiss = "dismiss"
	DismissActionSnooze  = "snooze"
)

// Valid reason codes.
const (
	DismissReasonIntentional       = "intentional"
	DismissReasonScheduledDeletion = "scheduled_deletion"
	DismissReasonFalsePositive     = "false_positive"
	DismissReasonCostAccepted      = "cost_accepted"
	DismissReasonOther             = "other"
)

// ValidDismissReasons is the authoritative set of accepted reason codes.
var ValidDismissReasons = map[string]bool{
	DismissReasonIntentional:       true,
	DismissReasonScheduledDeletion: true,
	DismissReasonFalsePositive:     true,
	DismissReasonCostAccepted:      true,
	DismissReasonOther:             true,
}

// MaxSnoozeDays is the maximum number of days a resource may be snoozed.
const MaxSnoozeDays = 90
