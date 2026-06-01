package notifications

import (
	"context"
	"log/slog"
	"math"
	"time"

	"axiaops.io/shared/analyzer"
	"axiaops.io/shared/model"
	"axiaops.io/shared/storage"
)

// DefaultSendTimeout caps a single transport call. Generous for an SMTP relay or
// a Slack/Teams webhook; longer than this and the dispatch is marked failed so a
// flaky transport can't add tens of seconds of latency to every scan. No
// in-process retry in v1 — surface the failure in the UI and let the admin
// re-send via POST /v1/channels/{id}/test. Exported so the API's /test endpoint
// uses the same budget as a real dispatch.
const DefaultSendTimeout = 10 * time.Second

// ChannelStore is the slice of storage.Store the dispatcher needs. Narrow on
// purpose: it keeps the package testable with a fake and documents that the
// dispatcher only reads enabled channels and writes dispatch rows — both on the
// RLS-bound pool, never the admin pool.
type ChannelStore interface {
	ListEnabledNotificationChannels(ctx context.Context) ([]model.NotificationChannel, error)
	SaveNotificationDispatch(ctx context.Context, d model.NotificationDispatch) error
}

// Dispatcher fans a completed scan out to an organization's enabled channels.
// Constructed once at service startup and invoked from the ingestion scan hook.
type Dispatcher struct {
	store      ChannelStore
	transports map[string]Transport // keyed by model.ChannelKind*
	publicHost string               // PUBLIC_HOST, used to build the dashboard link
	timeout    time.Duration        // per-transport send timeout
}

// NewDispatcher wires a dispatcher. transports maps a channel kind
// (model.ChannelKind*) to its Transport; kinds with no registered transport
// produce a failed dispatch row rather than a silent drop.
func NewDispatcher(store ChannelStore, transports map[string]Transport, publicHost string) *Dispatcher {
	return &Dispatcher{
		store:      store,
		transports: transports,
		publicHost: publicHost,
		timeout:    DefaultSendTimeout,
	}
}

// DispatchForScan notifies every enabled channel about a just-completed scan.
//
// Synchronous and non-fatal: it is called from runIngestionCore right after the
// snapshot is persisted (ctx already carries organization_id), and any error —
// channel load, transport send, dispatch-row write — is logged and recorded but
// never propagated, so a notification problem can't fail a scan. See the plan's
// "Dispatch seam" section.
func (d *Dispatcher) DispatchForScan(ctx context.Context, snap model.ZombieSnapshot, summary analyzer.Summary, accountID string) {
	channels, err := d.store.ListEnabledNotificationChannels(ctx)
	if err != nil {
		slog.ErrorContext(ctx, "notifications: list enabled channels", "error", err, "account_id", accountID)
		return
	}
	for _, ch := range channels {
		d.dispatchOne(ctx, ch, snap, summary, accountID)
	}
}

func (d *Dispatcher) dispatchOne(ctx context.Context, ch model.NotificationChannel, snap model.ZombieSnapshot, summary analyzer.Summary, accountID string) {
	organizationID := storage.OrganizationIDFromCtx(ctx)
	base := model.NotificationDispatch{
		OrganizationID:      organizationID,
		ChannelID:           ch.ID,
		Source:              model.DispatchSourceScan,
		SnapshotID:          snap.ID,
		AccountID:           accountID,
		ZombieCount:         summary.TotalZombies,
		MonthlySavingsCents: usdToCents(summary.PotentialMonthlySave),
		Attempts:            1,
	}

	// trigger_rule.On is reserved for v2 (per-zombie alerts joining the previous
	// snapshot). v1 has exactly one event — scan-complete digest — and
	// DispatchForScan is only ever called on scan completion, so On is not
	// consulted here. Do not add an On gate without the v2 event plumbing, or
	// admins' On config would silently start suppressing the v1 digest.

	// Gate: is this scan worth notifying about at all? If not, do NOT write a
	// dispatch row. A below-gate scan is the common case for a quiet org and is
	// the least interesting outcome ("we looked, nothing worth telling you") —
	// persisting one row per (channel × scan) would dominate notification_dispatches
	// growth and bury the genuine sent/failed rows out of the deliveries drawer's
	// window. The "channel is alive but suppressed" signal is covered by the Test
	// button + the drawer's empty state; the aggregate suppression rate, if ever
	// needed, belongs in a metric, not a per-row audit. (DispatchStatusSkippedThreshold
	// is retained in the model + CHECK enum for forward-compat — e.g. a future
	// state-transition mode that records "went quiet" once rather than every scan.)
	if summary.PotentialMonthlySave < ch.TriggerRule.MinMonthlySavingsUSD {
		slog.DebugContext(ctx, "notifications: scan below channel gate, skipping",
			"channel_id", ch.ID, "kind", ch.Kind,
			"savings", summary.PotentialMonthlySave, "gate", ch.TriggerRule.MinMonthlySavingsUSD)
		return
	}

	transport, ok := d.transports[ch.Kind]
	if !ok {
		// An enabled channel of a kind we can't send (e.g. a teams/jira row that
		// slipped past the API gate). Surface it as a failed dispatch rather than
		// dropping it silently.
		slog.WarnContext(ctx, "notifications: no transport for channel kind",
			"kind", ch.Kind, "channel_id", ch.ID)
		rec := base
		rec.Status = model.DispatchStatusFailed
		rec.Error = "no transport registered for channel kind " + ch.Kind
		d.record(ctx, rec)
		return
	}

	payload := BuildPayload(snap, summary, ch.TriggerRule.DigestTopN, d.publicHost)

	// Capture the attempt time before the call so dispatched_at reflects when we
	// dispatched, not when the (possibly timed-out) transport responded.
	now := time.Now().UTC()
	sendCtx, cancel := context.WithTimeout(ctx, d.timeout)
	defer cancel()
	externalID, sendErr := transport.Send(sendCtx, ch, payload)

	rec := base
	rec.DispatchedAt = &now
	rec.ExternalTicketID = externalID
	if sendErr != nil {
		rec.Status = model.DispatchStatusFailed
		rec.Error = sendErr.Error() // transport is responsible for scrubbing secrets
		slog.ErrorContext(ctx, "notifications: transport send failed",
			"kind", ch.Kind, "channel_id", ch.ID, "error", sendErr)
	} else {
		rec.Status = model.DispatchStatusSent
	}
	d.record(ctx, rec)
}

// record persists a dispatch row. A write failure is logged but not returned —
// the scan must not fail because we couldn't record a notification attempt.
func (d *Dispatcher) record(ctx context.Context, rec model.NotificationDispatch) {
	if err := d.store.SaveNotificationDispatch(ctx, rec); err != nil {
		slog.ErrorContext(ctx, "notifications: save dispatch row",
			"error", err, "channel_id", rec.ChannelID, "status", rec.Status)
	}
}

// usdToCents converts a USD float to integer cents, rounding to the nearest cent.
func usdToCents(usd float64) int64 {
	return int64(math.Round(usd * 100))
}
