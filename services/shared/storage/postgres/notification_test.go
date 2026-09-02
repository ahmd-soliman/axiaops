package postgres_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"axiaops.io/shared/model"
	"axiaops.io/shared/storage"
)

func notificationChannel(kind, label string) model.NotificationChannel {
	return model.NotificationChannel{
		Kind:    kind,
		Label:   label,
		Enabled: true,
		TriggerRule: model.TriggerRule{
			MinMonthlySavingsUSD: 25,
			DigestTopN:           10,
			On:                   []string{"new_zombies"},
		},
		ConfigCiphertext: "deadbeefciphertext",
	}
}

func TestSaveNotificationChannel_RoundTrip(t *testing.T) {
	s := newTestStore(t)
	ctx, org := newOrgCtx(t, s)

	ch := notificationChannel(model.ChannelKindEmail, "Ops digest")
	ch.OrganizationID = org.ID
	if err := s.SaveNotificationChannel(ctx, ch); err != nil {
		t.Fatalf("SaveNotificationChannel: %v", err)
	}

	list, err := s.ListNotificationChannels(ctx)
	if err != nil {
		t.Fatalf("ListNotificationChannels: %v", err)
	}
	if len(list) != 1 {
		t.Fatalf("want 1 channel, got %d", len(list))
	}

	got := list[0]
	if got.ID == "" {
		t.Error("expected DB-assigned id, got empty")
	}
	if got.Kind != model.ChannelKindEmail || got.Label != "Ops digest" || !got.Enabled {
		t.Errorf("scalar fields mismatch: %+v", got)
	}
	if got.TriggerRule.MinMonthlySavingsUSD != 25 || got.TriggerRule.DigestTopN != 10 {
		t.Errorf("trigger_rule not round-tripped: %+v", got.TriggerRule)
	}
	if len(got.TriggerRule.On) != 1 || got.TriggerRule.On[0] != "new_zombies" {
		t.Errorf("trigger_rule.on not round-tripped: %+v", got.TriggerRule.On)
	}
	if got.ConfigCiphertext != "deadbeefciphertext" {
		t.Errorf("config_ciphertext mismatch: %q", got.ConfigCiphertext)
	}
	if got.CreatedAt.IsZero() || got.UpdatedAt.IsZero() {
		t.Error("created_at/updated_at should be set by DB defaults")
	}
}

func TestSaveNotificationChannel_RoundTrip_Teams(t *testing.T) {
	s := newTestStore(t)
	ctx, org := newOrgCtx(t, s)

	ch := notificationChannel(model.ChannelKindTeams, "Teams digest")
	ch.OrganizationID = org.ID
	if err := s.SaveNotificationChannel(ctx, ch); err != nil {
		t.Fatalf("SaveNotificationChannel: %v", err)
	}

	list, err := s.ListNotificationChannels(ctx)
	if err != nil {
		t.Fatalf("ListNotificationChannels: %v", err)
	}
	if len(list) != 1 {
		t.Fatalf("want 1 channel, got %d", len(list))
	}

	got := list[0]
	if got.ID == "" {
		t.Error("expected DB-assigned id, got empty")
	}
	if got.Kind != model.ChannelKindTeams || got.Label != "Teams digest" || !got.Enabled {
		t.Errorf("scalar fields mismatch: %+v", got)
	}
	if got.TriggerRule.MinMonthlySavingsUSD != 25 || got.TriggerRule.DigestTopN != 10 {
		t.Errorf("trigger_rule not round-tripped: %+v", got.TriggerRule)
	}
	if len(got.TriggerRule.On) != 1 || got.TriggerRule.On[0] != "new_zombies" {
		t.Errorf("trigger_rule.on not round-tripped: %+v", got.TriggerRule.On)
	}
	if got.ConfigCiphertext != "deadbeefciphertext" {
		t.Errorf("config_ciphertext mismatch: %q", got.ConfigCiphertext)
	}
	if got.CreatedAt.IsZero() || got.UpdatedAt.IsZero() {
		t.Error("created_at/updated_at should be set by DB defaults")
	}
}

func TestSaveNotificationChannel_UpsertPreservesCreatedBumpsUpdated(t *testing.T) {
	s := newTestStore(t)
	ctx, org := newOrgCtx(t, s)

	ch := notificationChannel(model.ChannelKindSlack, "Initial")
	ch.OrganizationID = org.ID
	if err := s.SaveNotificationChannel(ctx, ch); err != nil {
		t.Fatalf("save 1: %v", err)
	}
	first, err := s.ListNotificationChannels(ctx)
	if err != nil || len(first) != 1 {
		t.Fatalf("list 1: %v (len %d)", err, len(first))
	}
	orig := first[0]

	// Re-save with the same ID and changed fields — including an attempt to flip
	// kind, which must be ignored (kind is immutable; config_ciphertext is shaped
	// for it).
	orig.Label = "Renamed"
	orig.Enabled = false
	orig.TriggerRule.DigestTopN = 5
	orig.Kind = model.ChannelKindEmail // started as slack; must NOT take effect
	if err := s.SaveNotificationChannel(ctx, orig); err != nil {
		t.Fatalf("save 2: %v", err)
	}

	after, err := s.GetNotificationChannel(ctx, orig.ID)
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if after.Label != "Renamed" || after.Enabled || after.TriggerRule.DigestTopN != 5 {
		t.Errorf("update not applied: %+v", after)
	}
	if after.Kind != model.ChannelKindSlack {
		t.Errorf("kind must be immutable on upsert: got %q, want slack", after.Kind)
	}
	if !after.CreatedAt.Equal(orig.CreatedAt) {
		t.Errorf("created_at should be preserved: was %v now %v", orig.CreatedAt, after.CreatedAt)
	}
	if !after.UpdatedAt.After(orig.UpdatedAt) {
		t.Errorf("updated_at should advance: was %v now %v", orig.UpdatedAt, after.UpdatedAt)
	}
}

func TestListEnabledNotificationChannels_FiltersDisabled(t *testing.T) {
	s := newTestStore(t)
	ctx, org := newOrgCtx(t, s)

	on := notificationChannel(model.ChannelKindEmail, "enabled")
	on.OrganizationID = org.ID
	off := notificationChannel(model.ChannelKindSlack, "disabled")
	off.OrganizationID = org.ID
	off.Enabled = false
	if err := s.SaveNotificationChannel(ctx, on); err != nil {
		t.Fatalf("save on: %v", err)
	}
	if err := s.SaveNotificationChannel(ctx, off); err != nil {
		t.Fatalf("save off: %v", err)
	}

	enabled, err := s.ListEnabledNotificationChannels(ctx)
	if err != nil {
		t.Fatalf("ListEnabled: %v", err)
	}
	if len(enabled) != 1 || enabled[0].Label != "enabled" {
		t.Fatalf("want only the enabled channel, got %+v", enabled)
	}
}

func TestGetNotificationChannel_NotFound(t *testing.T) {
	s := newTestStore(t)
	ctx, _ := newOrgCtx(t, s)

	_, err := s.GetNotificationChannel(ctx, "does-not-exist")
	if !errors.Is(err, storage.ErrChannelNotFound) {
		t.Fatalf("want ErrChannelNotFound, got %v", err)
	}
}

func TestDeleteNotificationChannel(t *testing.T) {
	s := newTestStore(t)
	ctx, org := newOrgCtx(t, s)

	ch := notificationChannel(model.ChannelKindEmail, "to delete")
	ch.OrganizationID = org.ID
	if err := s.SaveNotificationChannel(ctx, ch); err != nil {
		t.Fatalf("save: %v", err)
	}
	list, _ := s.ListNotificationChannels(ctx)
	id := list[0].ID

	if err := s.DeleteNotificationChannel(ctx, id); err != nil {
		t.Fatalf("delete: %v", err)
	}
	if _, err := s.GetNotificationChannel(ctx, id); !errors.Is(err, storage.ErrChannelNotFound) {
		t.Errorf("want ErrChannelNotFound after delete, got %v", err)
	}
	// Second delete reports not-found.
	if err := s.DeleteNotificationChannel(ctx, id); !errors.Is(err, storage.ErrChannelNotFound) {
		t.Errorf("want ErrChannelNotFound on re-delete, got %v", err)
	}
}

func TestNotificationChannel_RLSIsolation(t *testing.T) {
	if !rlsEnforced() {
		t.Skip("DATABASE_URL not set — RLS isolation only meaningful against the app role")
	}
	s := newTestStore(t)
	ctxA, orgA := newOrgCtx(t, s)
	ctxB, _ := newOrgCtx(t, s)

	ch := notificationChannel(model.ChannelKindEmail, "org A only")
	ch.OrganizationID = orgA.ID
	if err := s.SaveNotificationChannel(ctxA, ch); err != nil {
		t.Fatalf("save: %v", err)
	}

	bList, err := s.ListNotificationChannels(ctxB)
	if err != nil {
		t.Fatalf("list B: %v", err)
	}
	if len(bList) != 0 {
		t.Errorf("org B must not see org A's channels (RLS), got %d", len(bList))
	}
}

func TestNotificationDispatch_SaveAndList(t *testing.T) {
	s := newTestStore(t)
	ctx, org := newOrgCtx(t, s)

	ch := notificationChannel(model.ChannelKindSlack, "dispatch target")
	ch.OrganizationID = org.ID
	if err := s.SaveNotificationChannel(ctx, ch); err != nil {
		t.Fatalf("save channel: %v", err)
	}
	list, _ := s.ListNotificationChannels(ctx)
	channelID := list[0].ID

	// snapshot_id / account_id deliberately empty → persisted as NULL.
	for i, status := range []string{
		model.DispatchStatusSkippedThreshold,
		model.DispatchStatusFailed,
		model.DispatchStatusSent,
	} {
		d := model.NotificationDispatch{
			OrganizationID:      org.ID,
			ChannelID:           channelID,
			Status:              status,
			ZombieCount:         i,
			MonthlySavingsCents: int64(i * 1000),
		}
		if status == model.DispatchStatusFailed {
			d.Error = "transport: 500 from relay"
		}
		if err := s.SaveNotificationDispatch(ctx, d); err != nil {
			t.Fatalf("save dispatch %d: %v", i, err)
		}
	}

	got, err := s.ListNotificationDispatches(ctx, channelID, 10)
	if err != nil {
		t.Fatalf("list dispatches: %v", err)
	}
	if len(got) != 3 {
		t.Fatalf("want 3 dispatches, got %d", len(got))
	}
	// Newest first: the last inserted (sent) leads.
	if got[0].Status != model.DispatchStatusSent {
		t.Errorf("want newest-first ordering, leading status = %q", got[0].Status)
	}
	if got[0].SnapshotID != "" || got[0].AccountID != "" {
		t.Errorf("empty FKs should read back as empty strings, got snapshot=%q account=%q", got[0].SnapshotID, got[0].AccountID)
	}
	// Source was left empty on save → the column default 'scan' applies.
	if got[0].Source != model.DispatchSourceScan {
		t.Errorf("unset source should default to scan, got %q", got[0].Source)
	}

	// Limit is honoured.
	limited, err := s.ListNotificationDispatches(ctx, channelID, 2)
	if err != nil {
		t.Fatalf("list limited: %v", err)
	}
	if len(limited) != 2 {
		t.Errorf("want 2 with limit=2, got %d", len(limited))
	}
}

func TestDeleteOldNotificationDispatches(t *testing.T) {
	s := newTestStore(t)
	ctx, org := newOrgCtx(t, s)

	ch := notificationChannel(model.ChannelKindSlack, "retention target")
	ch.OrganizationID = org.ID
	if err := s.SaveNotificationChannel(ctx, ch); err != nil {
		t.Fatalf("save channel: %v", err)
	}
	list, _ := s.ListNotificationChannels(ctx)
	channelID := list[0].ID

	if err := s.SaveNotificationDispatch(ctx, model.NotificationDispatch{
		OrganizationID: org.ID, ChannelID: channelID, Status: model.DispatchStatusSent,
	}); err != nil {
		t.Fatalf("save dispatch: %v", err)
	}

	// A cutoff in the past must not touch a row created just now (created_at >= cutoff).
	deleted, err := s.DeleteOldNotificationDispatches(ctx, time.Now().Add(-1*time.Hour))
	if err != nil {
		t.Fatalf("delete (past cutoff): %v", err)
	}
	if deleted != 0 {
		t.Errorf("past cutoff should delete nothing, deleted %d", deleted)
	}
	if got, _ := s.ListNotificationDispatches(ctx, channelID, 10); len(got) != 1 {
		t.Fatalf("row should survive past-cutoff sweep, have %d", len(got))
	}

	// A cutoff in the future is newer than the row → it gets swept.
	deleted, err = s.DeleteOldNotificationDispatches(ctx, time.Now().Add(1*time.Hour))
	if err != nil {
		t.Fatalf("delete (future cutoff): %v", err)
	}
	if deleted != 1 {
		t.Errorf("future cutoff should delete the row, deleted %d", deleted)
	}
	if got, _ := s.ListNotificationDispatches(ctx, channelID, 10); len(got) != 0 {
		t.Errorf("row should be gone after future-cutoff sweep, have %d", len(got))
	}
}

func TestListNotificationDispatches_ClampsToCeiling(t *testing.T) {
	s := newTestStore(t)
	ctx, org := newOrgCtx(t, s)

	ch := notificationChannel(model.ChannelKindSlack, "noisy")
	ch.OrganizationID = org.ID
	if err := s.SaveNotificationChannel(ctx, ch); err != nil {
		t.Fatalf("save channel: %v", err)
	}
	list, _ := s.ListNotificationChannels(ctx)
	channelID := list[0].ID

	// 201 rows, then ask for more than the ceiling — the impl must clamp to 200.
	for i := 0; i < 201; i++ {
		if err := s.SaveNotificationDispatch(ctx, model.NotificationDispatch{
			OrganizationID: org.ID,
			ChannelID:      channelID,
			Status:         model.DispatchStatusSent,
		}); err != nil {
			t.Fatalf("save dispatch %d: %v", i, err)
		}
	}

	got, err := s.ListNotificationDispatches(ctx, channelID, 1000)
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(got) != 200 {
		t.Errorf("limit should clamp to the 200 ceiling, got %d", len(got))
	}
}

func TestNotificationDispatch_DispatchedAtRoundTrip(t *testing.T) {
	s := newTestStore(t)
	ctx, org := newOrgCtx(t, s)

	ch := notificationChannel(model.ChannelKindSlack, "ts target")
	ch.OrganizationID = org.ID
	if err := s.SaveNotificationChannel(ctx, ch); err != nil {
		t.Fatalf("save channel: %v", err)
	}
	list, _ := s.ListNotificationChannels(ctx)
	channelID := list[0].ID

	when := time.Date(2026, 6, 1, 12, 30, 0, 0, time.UTC)
	if err := s.SaveNotificationDispatch(ctx, model.NotificationDispatch{
		OrganizationID: org.ID,
		ChannelID:      channelID,
		Status:         model.DispatchStatusSent,
		DispatchedAt:   &when,
	}); err != nil {
		t.Fatalf("save dispatch: %v", err)
	}

	got, err := s.ListNotificationDispatches(ctx, channelID, 10)
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("want 1 dispatch, got %d", len(got))
	}
	if got[0].DispatchedAt == nil {
		t.Fatal("dispatched_at should read back non-nil")
	}
	if !got[0].DispatchedAt.Equal(when) {
		t.Errorf("dispatched_at round-trip: want %v, got %v", when, got[0].DispatchedAt)
	}
}

func TestNotificationDispatch_RLSIsolation(t *testing.T) {
	if !rlsEnforced() {
		t.Skip("DATABASE_URL not set — RLS isolation only meaningful against the app role")
	}
	s := newTestStore(t)
	ctxA, orgA := newOrgCtx(t, s)
	ctxB, _ := newOrgCtx(t, s)

	ch := notificationChannel(model.ChannelKindEmail, "org A channel")
	ch.OrganizationID = orgA.ID
	if err := s.SaveNotificationChannel(ctxA, ch); err != nil {
		t.Fatalf("save channel: %v", err)
	}
	list, _ := s.ListNotificationChannels(ctxA)
	channelID := list[0].ID
	if err := s.SaveNotificationDispatch(ctxA, model.NotificationDispatch{
		OrganizationID: orgA.ID,
		ChannelID:      channelID,
		Status:         model.DispatchStatusSent,
	}); err != nil {
		t.Fatalf("save dispatch: %v", err)
	}

	// Org B asks for org A's channel's dispatches — RLS must yield nothing.
	bDispatches, err := s.ListNotificationDispatches(ctxB, channelID, 10)
	if err != nil {
		t.Fatalf("list B: %v", err)
	}
	if len(bDispatches) != 0 {
		t.Errorf("org B must not see org A's dispatches (RLS), got %d", len(bDispatches))
	}
}

func TestDeleteOrganizationCascade_RemovesNotifications(t *testing.T) {
	s := newTestStore(t)
	ctx, org := newOrgCtx(t, s)

	ch := notificationChannel(model.ChannelKindEmail, "cascade me")
	ch.OrganizationID = org.ID
	if err := s.SaveNotificationChannel(ctx, ch); err != nil {
		t.Fatalf("save channel: %v", err)
	}
	list, _ := s.ListNotificationChannels(ctx)
	if err := s.SaveNotificationDispatch(ctx, model.NotificationDispatch{
		OrganizationID: org.ID,
		ChannelID:      list[0].ID,
		Status:         model.DispatchStatusSent,
	}); err != nil {
		t.Fatalf("save dispatch: %v", err)
	}

	// Cascade purge must drop dispatches before channels (FK order) without error.
	if err := s.DeleteOrganizationCascade(context.Background(), org.ID); err != nil {
		t.Fatalf("DeleteOrganizationCascade: %v", err)
	}

	remaining, err := s.ListNotificationChannels(ctx)
	if err != nil {
		t.Fatalf("list after cascade: %v", err)
	}
	if len(remaining) != 0 {
		t.Errorf("channels should be gone after cascade, got %d", len(remaining))
	}
}
