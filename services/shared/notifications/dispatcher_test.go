package notifications_test

import (
	"context"
	"errors"
	"sync"
	"testing"

	"axiaops.io/shared/analyzer"
	"axiaops.io/shared/model"
	"axiaops.io/shared/notifications"
	"axiaops.io/shared/storage"
)

// ── fakes ───────────────────────────────────────────────────────────────────

type fakeStore struct {
	mu         sync.Mutex
	channels   []model.NotificationChannel
	listErr    error
	saveErr    error
	dispatches []model.NotificationDispatch
}

func (f *fakeStore) ListEnabledNotificationChannels(context.Context) ([]model.NotificationChannel, error) {
	if f.listErr != nil {
		return nil, f.listErr
	}
	return f.channels, nil
}

func (f *fakeStore) SaveNotificationDispatch(_ context.Context, d model.NotificationDispatch) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.dispatches = append(f.dispatches, d)
	return f.saveErr
}

// fakeTransport needs no mutex: the dispatcher invokes transports sequentially
// and synchronously, and each test gives a channel its own transport.
type fakeTransport struct {
	calls       int
	lastChan    model.NotificationChannel
	lastPayload notifications.Payload
	externalID  string
	err         error
}

func (t *fakeTransport) Send(_ context.Context, ch model.NotificationChannel, p notifications.Payload) (string, error) {
	t.calls++
	t.lastChan = ch
	t.lastPayload = p
	return t.externalID, t.err
}

func channel(id, kind string, gate float64) model.NotificationChannel {
	return model.NotificationChannel{
		ID:      id,
		Kind:    kind,
		Enabled: true,
		TriggerRule: model.TriggerRule{
			MinMonthlySavingsUSD: gate,
			DigestTopN:           10,
			On:                   []string{"new_zombies"},
		},
	}
}

func summary(total int, savings float64) analyzer.Summary {
	return analyzer.Summary{
		TotalZombies:         total,
		PotentialMonthlySave: savings,
		Currency:             "USD",
		ByService: map[string]analyzer.ServiceSummary{
			"AmazonEC2": {Zombies: 1, Savings: savings * 0.6},
			"AmazonRDS": {Zombies: 1, Savings: savings * 0.4},
		},
	}
}

func ctxWithOrg() context.Context {
	return storage.WithOrganizationID(context.Background(), "org-1")
}

func snapshot() model.ZombieSnapshot {
	return model.ZombieSnapshot{ID: "snap-1", OrganizationID: "org-1", AccountID: "acct-1", Currency: "USD"}
}

// ── tests ───────────────────────────────────────────────────────────────────

func TestDispatch_GateMet_Sends(t *testing.T) {
	store := &fakeStore{channels: []model.NotificationChannel{channel("c1", model.ChannelKindSlack, 25)}}
	tr := &fakeTransport{externalID: ""}
	d := notifications.NewDispatcher(store, map[string]notifications.Transport{model.ChannelKindSlack: tr}, "https://app.example.com")

	d.DispatchForScan(ctxWithOrg(), snapshot(), summary(3, 100), "acct-1")

	if tr.calls != 1 {
		t.Fatalf("transport should be called once, got %d", tr.calls)
	}
	if len(store.dispatches) != 1 {
		t.Fatalf("want 1 dispatch row, got %d", len(store.dispatches))
	}
	rec := store.dispatches[0]
	if rec.Status != model.DispatchStatusSent {
		t.Errorf("status = %q, want sent", rec.Status)
	}
	if rec.DispatchedAt == nil {
		t.Error("sent row should have dispatched_at set")
	}
	if rec.MonthlySavingsCents != 10000 {
		t.Errorf("monthly_savings_cents = %d, want 10000", rec.MonthlySavingsCents)
	}
	if rec.OrganizationID != "org-1" || rec.SnapshotID != "snap-1" || rec.AccountID != "acct-1" {
		t.Errorf("dispatch row context wrong: %+v", rec)
	}
	// Payload carried the dashboard origin (trimmed) and savings-descending services.
	if tr.lastPayload.DashboardURL != "https://app.example.com" {
		t.Errorf("dashboard url = %q", tr.lastPayload.DashboardURL)
	}
	if len(tr.lastPayload.TopServices) != 2 || tr.lastPayload.TopServices[0].Service != "AmazonEC2" {
		t.Errorf("expected EC2 first by savings, got %+v", tr.lastPayload.TopServices)
	}
}

func TestDispatch_BelowGate_SkipsWithoutSendingOrRecording(t *testing.T) {
	store := &fakeStore{channels: []model.NotificationChannel{channel("c1", model.ChannelKindSlack, 25)}}
	tr := &fakeTransport{}
	d := notifications.NewDispatcher(store, map[string]notifications.Transport{model.ChannelKindSlack: tr}, "")

	d.DispatchForScan(ctxWithOrg(), snapshot(), summary(1, 10), "acct-1") // $10 < $25 gate

	if tr.calls != 0 {
		t.Errorf("transport must NOT be called below gate, got %d calls", tr.calls)
	}
	// A below-gate scan writes NO dispatch row — it's the dominant, least-useful
	// outcome and would bury real sent/failed rows in the deliveries drawer.
	if len(store.dispatches) != 0 {
		t.Fatalf("below-gate scan must not record a dispatch row, got %d", len(store.dispatches))
	}
}

func TestDispatch_TransportError_RecordsFailedNonFatal(t *testing.T) {
	store := &fakeStore{channels: []model.NotificationChannel{channel("c1", model.ChannelKindSlack, 25)}}
	tr := &fakeTransport{err: errors.New("slack: 500 from hooks.slack.com")}
	d := notifications.NewDispatcher(store, map[string]notifications.Transport{model.ChannelKindSlack: tr}, "")

	d.DispatchForScan(ctxWithOrg(), snapshot(), summary(3, 100), "acct-1")

	if len(store.dispatches) != 1 {
		t.Fatalf("want 1 failed row, got %d", len(store.dispatches))
	}
	rec := store.dispatches[0]
	if rec.Status != model.DispatchStatusFailed {
		t.Errorf("status = %q, want failed", rec.Status)
	}
	if rec.Error == "" {
		t.Error("failed row should carry the transport error")
	}
	if rec.DispatchedAt == nil {
		t.Error("failed row should still record dispatched_at (an attempt was made)")
	}
}

func TestDispatch_UnknownKind_RecordsFailed(t *testing.T) {
	store := &fakeStore{channels: []model.NotificationChannel{channel("c1", model.ChannelKindJira, 25)}}
	d := notifications.NewDispatcher(store, map[string]notifications.Transport{}, "") // no transports registered

	d.DispatchForScan(ctxWithOrg(), snapshot(), summary(3, 100), "acct-1")

	if len(store.dispatches) != 1 {
		t.Fatalf("want 1 row, got %d", len(store.dispatches))
	}
	if store.dispatches[0].Status != model.DispatchStatusFailed {
		t.Errorf("unknown kind should record failed, got %q", store.dispatches[0].Status)
	}
}

func TestDispatch_MultipleChannels_IndependentOutcomes(t *testing.T) {
	store := &fakeStore{channels: []model.NotificationChannel{
		channel("email-low", model.ChannelKindEmail, 5),    // gate met → sent + 1 row
		channel("slack-high", model.ChannelKindSlack, 500), // gate not met → no send, no row
		channel("teams-mid", model.ChannelKindTeams, 50),   // gate met → sent + 1 row
	}}
	email := &fakeTransport{}
	slack := &fakeTransport{}
	teams := &fakeTransport{}
	d := notifications.NewDispatcher(store, map[string]notifications.Transport{
		model.ChannelKindEmail: email,
		model.ChannelKindSlack: slack,
		model.ChannelKindTeams: teams,
	}, "")

	d.DispatchForScan(ctxWithOrg(), snapshot(), summary(3, 100), "acct-1")

	if email.calls != 1 {
		t.Errorf("email (gate $5, savings $100) should send, calls=%d", email.calls)
	}
	if slack.calls != 0 {
		t.Errorf("slack (gate $500) should be skipped, calls=%d", slack.calls)
	}
	if teams.calls != 1 {
		t.Errorf("teams (gate $50, savings $100) should send, calls=%d", teams.calls)
	}
	// The two sent channels record a row; the below-gate channel records nothing.
	if len(store.dispatches) != 2 {
		t.Fatalf("want 2 dispatch rows (only the sent channels), got %d", len(store.dispatches))
	}
	for _, d := range store.dispatches {
		if d.Status != model.DispatchStatusSent {
			t.Errorf("the recorded row should be sent, got %q", d.Status)
		}
	}
}

func TestDispatch_ListError_NoRowsNoPanic(t *testing.T) {
	store := &fakeStore{listErr: errors.New("db down")}
	tr := &fakeTransport{}
	d := notifications.NewDispatcher(store, map[string]notifications.Transport{model.ChannelKindSlack: tr}, "")

	d.DispatchForScan(ctxWithOrg(), snapshot(), summary(3, 100), "acct-1")

	if tr.calls != 0 || len(store.dispatches) != 0 {
		t.Errorf("a channel-load error must short-circuit cleanly: calls=%d rows=%d", tr.calls, len(store.dispatches))
	}
}

func TestDispatch_SaveError_NonFatal(t *testing.T) {
	// A dispatch-row write failure must not abort the scan: DispatchForScan has
	// no return value and must not panic. The send still happens.
	store := &fakeStore{
		channels: []model.NotificationChannel{channel("c1", model.ChannelKindSlack, 25)},
		saveErr:  errors.New("db write failed"),
	}
	tr := &fakeTransport{}
	d := notifications.NewDispatcher(store, map[string]notifications.Transport{model.ChannelKindSlack: tr}, "")

	d.DispatchForScan(ctxWithOrg(), snapshot(), summary(3, 100), "acct-1") // must return cleanly

	if tr.calls != 1 {
		t.Errorf("send should still be attempted despite save failure, calls=%d", tr.calls)
	}
}

func TestBuildPayload_TrimsAndSorts(t *testing.T) {
	s := analyzer.Summary{
		TotalZombies:         6,
		PotentialMonthlySave: 600,
		Currency:             "USD",
		ByService: map[string]analyzer.ServiceSummary{
			"AmazonEC2": {Zombies: 1, Savings: 300},
			"AmazonRDS": {Zombies: 1, Savings: 200},
			"AWSLambda": {Zombies: 1, Savings: 100},
			"AmazonS3":  {Zombies: 1, Savings: 50},
		},
	}
	p := notifications.BuildPayload(snapshot(), s, 2, "https://app.example.com/")

	if len(p.TopServices) != 2 {
		t.Fatalf("topN=2 should trim to 2, got %d", len(p.TopServices))
	}
	if p.TopServices[0].Service != "AmazonEC2" || p.TopServices[1].Service != "AmazonRDS" {
		t.Errorf("expected top-2 by savings descending, got %+v", p.TopServices)
	}
	if p.DashboardURL != "https://app.example.com" {
		t.Errorf("trailing slash should be trimmed, got %q", p.DashboardURL)
	}
	if p.ZombieCount != 6 || p.MonthlySavings != 600 {
		t.Errorf("aggregate fields wrong: %+v", p)
	}
}
