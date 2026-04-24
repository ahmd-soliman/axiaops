// Package model — AuditEvent is a single row in the audit_log table, recording
// one user-initiated mutation. See docs/audit_trail_plan.md for the full design.
package model

import (
	"net"
	"time"
)

// AuditEvent is a single audit_log row. Only user-initiated mutating actions
// are recorded — reads and scheduled/automated scans are excluded.
type AuditEvent struct {
	ID           int64          `json:"id"`
	TenantID     string         `json:"tenant_id,omitempty"`
	UserID       string         `json:"user_id,omitempty"`      // NULL after GDPR anonymisation
	ActorEmail   string         `json:"actor_email"`            // captured at event time
	Action       string         `json:"action"`                 // one of AuditAction* constants
	ResourceType string         `json:"resource_type,omitempty"`
	ResourceID   string         `json:"resource_id,omitempty"`
	Reason       string         `json:"reason,omitempty"`
	Metadata     map[string]any `json:"metadata,omitempty"`
	RequestID    string         `json:"request_id,omitempty"`
	IPAddress    net.IP         `json:"ip_address,omitempty"`
	UserAgent    string         `json:"user_agent,omitempty"`
	CreatedAt    time.Time      `json:"created_at"`
}

// Audit action constants. Values match the action column in audit_log.
// Keep this list in sync with docs/audit_trail_plan.md §3.2.
const (
	AuditActionDismissZombie     = "dismiss_zombie"
	AuditActionSnoozeZombie      = "snooze_zombie"
	AuditActionRevokeDismissal   = "revoke_dismissal"
	AuditActionViewRemediation   = "view_remediation"
	AuditActionScanTriggered     = "scan_triggered"
	AuditActionAccountConnected  = "account_connected"
	AuditActionAccountUpdated    = "account_updated"
	AuditActionAccountDeleted    = "account_deleted"
)

// ValidAuditActions is the authoritative set of action codes accepted on write
// and returned by GET /v1/audit filters.
var ValidAuditActions = map[string]bool{
	AuditActionDismissZombie:    true,
	AuditActionSnoozeZombie:     true,
	AuditActionRevokeDismissal:  true,
	AuditActionViewRemediation:  true,
	AuditActionScanTriggered:    true,
	AuditActionAccountConnected: true,
	AuditActionAccountUpdated:   true,
	AuditActionAccountDeleted:   true,
}

// AuditFilter parameterises AuditLogList queries. Zero-value fields are not
// applied — a zero filter returns the full tenant timeline (bounded by Limit).
type AuditFilter struct {
	UserID       string
	ResourceType string
	ResourceID   string
	Action       string
	Since        time.Time
	Until        time.Time
	// Limit is the max rows returned. Zero lets the store pick its maximum
	// (500). Callers that display to a user should set a sensible default —
	// the HTTP handler uses 50.
	Limit int
	// Cursor is the opaque pagination token from the previous page's
	// next_cursor; zero means "start from the newest row".
	Cursor AuditCursor
}

// AuditCursor is a (created_at, id) pair for stable pagination under concurrent
// inserts. A zero cursor means "start from the newest row".
//
// JSON tags intentionally omit `omitempty` — encoding a cursor with ID=0
// (unlikely but possible if audit_log_id_seq is ever reset in a test DB) must
// round-trip through JSON intact, not silently collapse to a "start over" cursor.
type AuditCursor struct {
	CreatedAt time.Time `json:"created_at"`
	ID        int64     `json:"id"`
}

// IsZero reports whether the cursor has been set.
func (c AuditCursor) IsZero() bool {
	return c.ID == 0 && c.CreatedAt.IsZero()
}
