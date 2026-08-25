package model

import "time"

// Notification channel kinds. Strings must match the `kind` CHECK constraint in
// migration 031_notification_channels. Only email + slack ship in v1; teams and
// jira are pre-provisioned in the enum for follow-up MRs (#114 / #113).
const (
	ChannelKindEmail = "email"
	ChannelKindSlack = "slack"
	ChannelKindTeams = "teams"
	ChannelKindJira  = "jira"
)

// Trigger events for TriggerRule.On. v1 recognises only the scan-complete
// digest; per-zombie alerts are a v2 event.
const TriggerEventNewZombies = "new_zombies"

// Dispatch status values. Strings must match the `status` CHECK constraint in
// migration 031_notification_channels.
const (
	DispatchStatusQueued           = "queued"
	DispatchStatusSent             = "sent"
	DispatchStatusFailed           = "failed"
	DispatchStatusSkippedThreshold = "skipped_threshold"
)

// Dispatch source values — what triggered the dispatch. Strings must match the
// `source` CHECK constraint in migration 031_notification_channels.
const (
	DispatchSourceScan = "scan" // a completed ingestion scan
	DispatchSourceTest = "test" // an admin's POST /v1/channels/{id}/test
)

// TriggerRule controls when a channel fires and how much detail the message
// carries. Stored as JSONB in notification_channels.trigger_rule. The two knobs
// are deliberately decoupled (the "first-scan storm" problem):
//
//   - MinMonthlySavingsUSD is the GATE — "is this scan worth notifying about?".
//   - DigestTopN is the BODY TRIM — "how many findings to list in the message".
//
// Conflating them would let a high gate silence the small-org demos where the
// first scan surfaces $40 of real findings, which is the product's whole value.
type TriggerRule struct {
	MinMonthlySavingsUSD float64  `json:"min_monthly_savings_usd"`
	DigestTopN           int      `json:"digest_top_n"`
	On                   []string `json:"on"` // events this channel reacts to; v1 recognises "new_zombies"
}

// NotificationChannel is one configured outbound destination for an organization.
//
// ConfigCiphertext holds the kind-specific transport config (SMTP creds, webhook
// URL, …) AES-256-GCM-encrypted via crypto.Encrypt — the same posture as
// Account.SecretEncrypted. It is never serialised to the client (`json:"-"`); the
// API layer redacts it on read and only re-encrypts on a non-mask PATCH, exactly
// like accounts.secret_encrypted.
type NotificationChannel struct {
	ID               string      `json:"id"`
	OrganizationID   string      `json:"organization_id"`
	Kind             string      `json:"kind"`  // ChannelKind*
	Label            string      `json:"label"` // user-defined name
	Enabled          bool        `json:"enabled"`
	TriggerRule      TriggerRule `json:"trigger_rule"`
	ConfigCiphertext string      `json:"-"` // never sent to client
	CreatedAt        time.Time   `json:"created_at"`
	UpdatedAt        time.Time   `json:"updated_at"`
}

// NotificationDispatch is one send attempt against a channel — the
// audit/visibility trail surfaced in the dashboard's "Recent deliveries" drawer.
// One row is written per attempt (sent | failed | skipped_threshold).
type NotificationDispatch struct {
	ID                  string     `json:"id"`
	OrganizationID      string     `json:"organization_id"`
	ChannelID           string     `json:"channel_id"`
	Source              string     `json:"source"`                // DispatchSource* — "scan" | "test"
	SnapshotID          string     `json:"snapshot_id,omitempty"` // empty if the source snapshot was later deleted
	AccountID           string     `json:"account_id,omitempty"`  // empty if the source account was later deleted
	Status              string     `json:"status"`                // DispatchStatus*
	ZombieCount         int        `json:"zombie_count"`
	MonthlySavingsCents int64      `json:"monthly_savings_cents"`
	Attempts            int        `json:"attempts"`
	ExternalTicketID    string     `json:"external_ticket_id,omitempty"` // Jira/Linear/GitHub key; empty for email/slack
	Error               string     `json:"error,omitempty"`
	DispatchedAt        *time.Time `json:"dispatched_at"` // null until the transport call completes
	CreatedAt           time.Time  `json:"created_at"`
}

// EmailConfig is the decrypted shape of an email channel's ConfigCiphertext.
// Marshalled to JSON before crypto.Encrypt, unmarshalled after crypto.Decrypt.
type EmailConfig struct {
	SMTPHost   string   `json:"smtp_host"`
	SMTPPort   int      `json:"smtp_port"`
	SMTPUser   string   `json:"smtp_user"`
	SMTPPass   string   `json:"smtp_pass"`
	From       string   `json:"from"`      // bare envelope address — also MAIL FROM + HELO domain
	FromName   string   `json:"from_name"` // optional display name for the From: header (e.g. "AxiaOps")
	Recipients []string `json:"recipients"`
}

// SlackConfig is the decrypted shape of a slack channel's ConfigCiphertext.
// The whole webhook URL is a bearer token — it is encrypted at rest, redacted in
// API responses, and must be scrubbed from any error string before persisting a
// failed dispatch row (Slack's 404 body sometimes echoes the URL).
type SlackConfig struct {
	WebhookURL string `json:"webhook_url"`
}
