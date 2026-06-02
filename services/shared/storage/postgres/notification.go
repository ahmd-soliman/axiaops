package postgres

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"

	"axiaops.io/shared/model"
	"axiaops.io/shared/storage"
)

// notificationChannelSelectSQL is the column list shared by the channel reads.
// trigger_rule comes back as raw JSONB bytes and is unmarshalled in scan.
const notificationChannelSelectSQL = `
	SELECT id, organization_id, kind, label, enabled,
	       trigger_rule, config_ciphertext, created_at, updated_at
	FROM notification_channels`

func scanNotificationChannel(r rowScanner) (model.NotificationChannel, error) {
	var (
		ch          model.NotificationChannel
		triggerJSON []byte
	)
	if err := r.Scan(
		&ch.ID, &ch.OrganizationID, &ch.Kind, &ch.Label, &ch.Enabled,
		&triggerJSON, &ch.ConfigCiphertext, &ch.CreatedAt, &ch.UpdatedAt,
	); err != nil {
		return model.NotificationChannel{}, err
	}
	if err := json.Unmarshal(triggerJSON, &ch.TriggerRule); err != nil {
		return model.NotificationChannel{}, fmt.Errorf("postgres: decode trigger_rule: %w", err)
	}
	return ch, nil
}

// SaveNotificationChannel upserts a channel for the request org. An empty ID on
// first insert lets the DB default (gen_random_uuid()::text) assign one; on a
// re-save the caller-supplied ID drives the ON CONFLICT update. updated_at is
// always bumped to NOW(); created_at is preserved on conflict.
func (s *Store) SaveNotificationChannel(ctx context.Context, ch model.NotificationChannel) error {
	triggerJSON, err := json.Marshal(ch.TriggerRule)
	if err != nil {
		return fmt.Errorf("postgres: encode trigger_rule: %w", err)
	}

	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("postgres: begin tx: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	if err := setOrganization(ctx, tx); err != nil {
		return err
	}

	// NULLIF($1,'') so an empty ID falls through to the column DEFAULT instead of
	// inserting a literal '' primary key. organization_id is taken from the bound
	// RLS setting via the WITH CHECK policy, but we pass it explicitly to satisfy
	// the NOT NULL column on insert.
	//
	// kind is deliberately NOT in the ON CONFLICT update set: a channel's kind is
	// immutable once created because config_ciphertext is shaped for that kind
	// (email JSON vs slack JSON). Letting an upsert flip kind would leave the
	// blob incoherent. The PATCH handler must reject kind changes upstream too,
	// but the storage layer refuses to apply one regardless.
	_, err = tx.Exec(ctx, `
		INSERT INTO notification_channels
			(id, organization_id, kind, label, enabled, trigger_rule, config_ciphertext)
		VALUES (COALESCE(NULLIF($1,''), gen_random_uuid()::text), $2, $3, $4, $5, $6, $7)
		ON CONFLICT (id) DO UPDATE SET
			label             = EXCLUDED.label,
			enabled           = EXCLUDED.enabled,
			trigger_rule      = EXCLUDED.trigger_rule,
			config_ciphertext = EXCLUDED.config_ciphertext,
			updated_at        = NOW()`,
		ch.ID, ch.OrganizationID, ch.Kind, ch.Label, ch.Enabled, triggerJSON, ch.ConfigCiphertext,
	)
	if err != nil {
		return fmt.Errorf("postgres: save notification channel: %w", err)
	}
	return tx.Commit(ctx)
}

// ListNotificationChannels returns all channels in the request org, newest first.
func (s *Store) ListNotificationChannels(ctx context.Context) ([]model.NotificationChannel, error) {
	return s.queryNotificationChannels(ctx, notificationChannelSelectSQL+` ORDER BY created_at DESC`)
}

// ListEnabledNotificationChannels returns only enabled channels for the request
// org — the dispatcher hot path. RLS-bound on purpose (see the Store doc).
func (s *Store) ListEnabledNotificationChannels(ctx context.Context) ([]model.NotificationChannel, error) {
	return s.queryNotificationChannels(ctx, notificationChannelSelectSQL+` WHERE enabled = TRUE ORDER BY created_at DESC`)
}

func (s *Store) queryNotificationChannels(ctx context.Context, query string) ([]model.NotificationChannel, error) {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return nil, fmt.Errorf("postgres: begin tx: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	if err := setOrganization(ctx, tx); err != nil {
		return nil, err
	}

	rows, err := tx.Query(ctx, query)
	if err != nil {
		return nil, fmt.Errorf("postgres: list notification channels: %w", err)
	}
	defer rows.Close()

	var channels []model.NotificationChannel
	for rows.Next() {
		ch, err := scanNotificationChannel(rows)
		if err != nil {
			return nil, fmt.Errorf("postgres: scan notification channel: %w", err)
		}
		channels = append(channels, ch)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("postgres: iterate notification channels: %w", err)
	}
	return channels, tx.Commit(ctx)
}

// GetNotificationChannel returns a single channel by ID. ErrChannelNotFound on miss.
func (s *Store) GetNotificationChannel(ctx context.Context, id string) (model.NotificationChannel, error) {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return model.NotificationChannel{}, fmt.Errorf("postgres: begin tx: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	if err := setOrganization(ctx, tx); err != nil {
		return model.NotificationChannel{}, err
	}

	row := tx.QueryRow(ctx, notificationChannelSelectSQL+` WHERE id = $1`, id)
	ch, err := scanNotificationChannel(row)
	if errors.Is(err, pgx.ErrNoRows) {
		return model.NotificationChannel{}, storage.ErrChannelNotFound
	}
	if err != nil {
		return model.NotificationChannel{}, fmt.Errorf("postgres: get notification channel: %w", err)
	}
	return ch, tx.Commit(ctx)
}

// DeleteNotificationChannel removes a channel by ID; dispatch rows cascade.
// ErrChannelNotFound when no row matched in the request org.
func (s *Store) DeleteNotificationChannel(ctx context.Context, id string) error {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("postgres: begin tx: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	if err := setOrganization(ctx, tx); err != nil {
		return err
	}

	tag, err := tx.Exec(ctx, `DELETE FROM notification_channels WHERE id = $1`, id)
	if err != nil {
		return fmt.Errorf("postgres: delete notification channel: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return storage.ErrChannelNotFound
	}
	return tx.Commit(ctx)
}

// notificationDispatchSelectSQL — COALESCE turns the nullable snapshot_id /
// account_id / error columns into empty strings, and zombie_count /
// monthly_savings_cents into 0, so callers keep plain (non-pointer) fields.
const notificationDispatchSelectSQL = `
	SELECT id, organization_id, channel_id, source,
	       COALESCE(snapshot_id, '')        AS snapshot_id,
	       COALESCE(account_id, '')         AS account_id,
	       status,
	       COALESCE(zombie_count, 0)          AS zombie_count,
	       COALESCE(monthly_savings_cents, 0) AS monthly_savings_cents,
	       attempts,
	       COALESCE(external_ticket_id, '') AS external_ticket_id,
	       COALESCE(error, '')              AS error,
	       dispatched_at, created_at
	FROM notification_dispatches`

func scanNotificationDispatch(r rowScanner) (model.NotificationDispatch, error) {
	var d model.NotificationDispatch
	if err := r.Scan(
		&d.ID, &d.OrganizationID, &d.ChannelID, &d.Source,
		&d.SnapshotID, &d.AccountID, &d.Status,
		&d.ZombieCount, &d.MonthlySavingsCents, &d.Attempts,
		&d.ExternalTicketID, &d.Error, &d.DispatchedAt, &d.CreatedAt,
	); err != nil {
		return model.NotificationDispatch{}, err
	}
	return d, nil
}

// SaveNotificationDispatch inserts one dispatch row. Nullable columns use
// NULLIF so empty strings persist as NULL (honouring the ON DELETE SET NULL FKs
// on snapshot_id / account_id and the partial unique index on external_ticket_id).
// maxDispatchErrorLen caps the persisted error string. Transports scrub their
// own secrets, but a relay can still echo an arbitrarily long body; clamp on
// write so a single failed send can't bloat the row (the drawer only needs the
// leading, human-readable part of the message).
const maxDispatchErrorLen = 1000

func (s *Store) SaveNotificationDispatch(ctx context.Context, d model.NotificationDispatch) error {
	d.Error = clampError(d.Error)

	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("postgres: begin tx: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	if err := setOrganization(ctx, tx); err != nil {
		return err
	}

	// COALESCE(NULLIF($,''), 'scan') lets a caller leave Source empty and fall
	// back to the column default rather than violating the NOT NULL / CHECK.
	_, err = tx.Exec(ctx, `
		INSERT INTO notification_dispatches
			(id, organization_id, channel_id, source, snapshot_id, account_id, status,
			 zombie_count, monthly_savings_cents, attempts, external_ticket_id,
			 error, dispatched_at)
		VALUES (COALESCE(NULLIF($1,''), gen_random_uuid()::text), $2, $3,
			COALESCE(NULLIF($4,''), 'scan'), NULLIF($5,''), NULLIF($6,''), $7,
			$8, $9, $10, NULLIF($11,''),
			NULLIF($12,''), $13)`,
		d.ID, d.OrganizationID, d.ChannelID, d.Source, d.SnapshotID, d.AccountID, d.Status,
		d.ZombieCount, d.MonthlySavingsCents, d.Attempts, d.ExternalTicketID,
		d.Error, d.DispatchedAt,
	)
	if err != nil {
		return fmt.Errorf("postgres: save notification dispatch: %w", err)
	}
	return tx.Commit(ctx)
}

// clampError truncates an error string to maxDispatchErrorLen, on a rune
// boundary, appending an ellipsis when it cuts. Empty in → empty out.
func clampError(s string) string {
	if len(s) <= maxDispatchErrorLen {
		return s
	}
	r := []rune(s)
	if len(r) <= maxDispatchErrorLen {
		return s
	}
	return string(r[:maxDispatchErrorLen]) + "…"
}

const (
	defaultDispatchListLimit = 50
	maxDispatchListLimit     = 200 // hard ceiling, mirrors auditListMaxLimit's posture
)

// ListNotificationDispatches returns the newest dispatch rows for a channel,
// capped at limit (<=0 → defaultDispatchListLimit; anything over
// maxDispatchListLimit is clamped down so a caller can't request an unbounded
// result set).
func (s *Store) ListNotificationDispatches(ctx context.Context, channelID string, limit int) ([]model.NotificationDispatch, error) {
	if limit <= 0 {
		limit = defaultDispatchListLimit
	}
	if limit > maxDispatchListLimit {
		limit = maxDispatchListLimit
	}

	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return nil, fmt.Errorf("postgres: begin tx: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	if err := setOrganization(ctx, tx); err != nil {
		return nil, err
	}

	rows, err := tx.Query(ctx,
		notificationDispatchSelectSQL+` WHERE channel_id = $1 ORDER BY created_at DESC LIMIT $2`,
		channelID, limit)
	if err != nil {
		return nil, fmt.Errorf("postgres: list notification dispatches: %w", err)
	}
	defer rows.Close()

	var dispatches []model.NotificationDispatch
	for rows.Next() {
		d, err := scanNotificationDispatch(rows)
		if err != nil {
			return nil, fmt.Errorf("postgres: scan notification dispatch: %w", err)
		}
		dispatches = append(dispatches, d)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("postgres: iterate notification dispatches: %w", err)
	}
	return dispatches, tx.Commit(ctx)
}
