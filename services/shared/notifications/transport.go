// Package notifications dispatches post-scan digest messages to an organization's
// configured channels (email, Slack, …).
//
// The package owns three concerns: the Transport seam (one method per wire
// format), payload assembly (renderer.go), and the Dispatcher that gates on the
// channel's trigger rule, enforces a per-transport timeout, and records one
// notification_dispatches row per attempt. Concrete transports live in
// email_smtp.go / slack_webhook.go.
package notifications

import (
	"context"

	"axiaops.io/shared/model"
)

// ServiceRow is one line in a digest: a service and its zombie tally for the scan.
type ServiceRow struct {
	Service string  `json:"service"`
	Count   int     `json:"count"`
	Savings float64 `json:"savings"` // USD/month
}

// Payload is the rendered, transport-agnostic content for one scan notification.
// Transports turn it into their own wire format (HTML email, Slack blocks, …).
type Payload struct {
	OrganizationID string
	AccountID      string
	ZombieCount    int
	MonthlySavings float64 // USD/month, total across the scan
	Currency       string
	TopServices    []ServiceRow // trimmed to the channel's digest_top_n, savings-descending
	DashboardURL   string       // org's dashboard origin, built from PUBLIC_HOST; empty when unset
	SnapshotID     string
}

// Transport sends a Payload to a single channel.
//
// Contract for implementations:
//   - Decrypt the channel's ConfigCiphertext yourself — the config shape is
//     kind-specific (model.EmailConfig vs model.SlackConfig), so the dispatcher
//     stays oblivious to it.
//   - Respect the ctx deadline; the dispatcher wraps each call in a per-transport
//     timeout. Propagate ctx into any outbound HTTP/SMTP call.
//   - Do NOT retry inside Send. A transport-level failure returns err; the
//     dispatcher records a status=failed row and the admin re-sends via /test.
//   - Scrub bearer-token URLs (Slack webhook, etc.) from any returned error —
//     Slack's 404 body sometimes echoes the URL, which would otherwise land in
//     notification_dispatches.error and the dashboard's deliveries drawer.
//
// externalID is empty for fire-and-forget kinds (email/slack) and the external
// issue key for ticketing kinds (Jira, #113); the dispatcher persists it in
// notification_dispatches.external_ticket_id for dedup on re-dispatch.
type Transport interface {
	Send(ctx context.Context, channel model.NotificationChannel, payload Payload) (externalID string, err error)
}
