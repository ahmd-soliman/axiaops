package api_test

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"axiaops.io/api/internal/api"
	"axiaops.io/shared/crypto"
	"axiaops.io/shared/model"
	"axiaops.io/shared/notifications"
)

const channelTestKey = "0000000000000000000000000000000000000000000000000000000000000000"

// recordingTransport captures the channel it was asked to send so a test can
// decrypt the stored config and assert encryption / mask-preservation.
type recordingTransport struct {
	gotChannel model.NotificationChannel
	externalID string
	err        error
	calls      int
}

func (rt *recordingTransport) Send(_ context.Context, ch model.NotificationChannel, _ notifications.Payload) (string, error) {
	rt.calls++
	rt.gotChannel = ch
	return rt.externalID, rt.err
}

type chanResp struct {
	ID          string            `json:"id"`
	Kind        string            `json:"kind"`
	Label       string            `json:"label"`
	Enabled     bool              `json:"enabled"`
	TriggerRule model.TriggerRule `json:"trigger_rule"`
	Config      map[string]any    `json:"config"`
}

func channelMux(t *testing.T, store *MockStore, transports map[string]notifications.Transport) *http.ServeMux {
	t.Helper()
	t.Setenv("ENCRYPTION_KEY", channelTestKey)
	h := api.New(store, noopQueue())
	if transports != nil {
		h = h.WithNotificationTransports(transports)
	}
	mux := http.NewServeMux()
	h.Register(mux)
	return mux
}

func do(t *testing.T, mux *http.ServeMux, method, path, body string) *httptest.ResponseRecorder {
	t.Helper()
	rr := httptest.NewRecorder()
	mux.ServeHTTP(rr, orgRequestWithBody(method, path, body))
	return rr
}

// createSlack creates an enabled-by-request slack channel and returns its id.
func createSlack(t *testing.T, mux *http.ServeMux, webhook string) string {
	t.Helper()
	body := `{"kind":"slack","label":"ops","enabled":true,"config":{"webhook_url":"` + webhook + `"}}`
	rr := do(t, mux, http.MethodPost, "/v1/channels", body)
	if rr.Code != http.StatusCreated {
		t.Fatalf("create slack: status %d, body %s", rr.Code, rr.Body.String())
	}
	var got chanResp
	if err := json.Unmarshal(rr.Body.Bytes(), &got); err != nil {
		t.Fatalf("decode: %v", err)
	}
	return got.ID
}

func decryptSlackWebhook(t *testing.T, ch model.NotificationChannel) string {
	t.Helper()
	plaintext, err := crypto.Decrypt(ch.ConfigCiphertext)
	if err != nil {
		t.Fatalf("decrypt captured config: %v", err)
	}
	var c model.SlackConfig
	if err := json.Unmarshal([]byte(plaintext), &c); err != nil {
		t.Fatalf("unmarshal slack config: %v", err)
	}
	return c.WebhookURL
}

// ── create ───────────────────────────────────────────────────────────────────

func TestCreateChannel_Slack_RedactsSecretAndDefaults(t *testing.T) {
	store := NewMockStore().WithRole("admin")
	mux := channelMux(t, store, nil)

	body := `{"kind":"slack","label":"ops","config":{"webhook_url":"https://hooks.slack.com/X"}}`
	rr := do(t, mux, http.MethodPost, "/v1/channels", body)
	if rr.Code != http.StatusCreated {
		t.Fatalf("status %d, body %s", rr.Code, rr.Body.String())
	}
	var got chanResp
	if err := json.Unmarshal(rr.Body.Bytes(), &got); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if got.ID == "" || got.Kind != "slack" || got.Label != "ops" {
		t.Errorf("unexpected response: %+v", got)
	}
	if got.Enabled {
		t.Error("enabled should default false when omitted")
	}
	if got.Config["webhook_url"] != "***" {
		t.Errorf("webhook_url should be masked, got %v", got.Config["webhook_url"])
	}
	if got.TriggerRule.MinMonthlySavingsUSD != 25 || got.TriggerRule.DigestTopN != 10 {
		t.Errorf("default trigger rule not applied: %+v", got.TriggerRule)
	}
}

func TestCreateChannel_RejectsUnsupportedKind(t *testing.T) {
	store := NewMockStore().WithRole("admin")
	mux := channelMux(t, store, nil)
	rr := do(t, mux, http.MethodPost, "/v1/channels",
		`{"kind":"teams","label":"x","config":{"webhook_url":"u"}}`)
	if rr.Code != http.StatusBadRequest {
		t.Fatalf("teams should be rejected in v1, got %d", rr.Code)
	}
}

func TestCreateChannel_EmailValidation(t *testing.T) {
	store := NewMockStore().WithRole("admin")
	mux := channelMux(t, store, nil)
	// Missing recipients.
	rr := do(t, mux, http.MethodPost, "/v1/channels",
		`{"kind":"email","label":"x","config":{"smtp_host":"h","smtp_port":587,"from":"f@x.com"}}`)
	if rr.Code != http.StatusBadRequest {
		t.Fatalf("incomplete email config should be 400, got %d body %s", rr.Code, rr.Body.String())
	}
}

func TestCreateChannel_RequiresConfig(t *testing.T) {
	store := NewMockStore().WithRole("admin")
	mux := channelMux(t, store, nil)
	rr := do(t, mux, http.MethodPost, "/v1/channels", `{"kind":"slack","label":"x"}`)
	if rr.Code != http.StatusBadRequest {
		t.Fatalf("missing config should be 400, got %d", rr.Code)
	}
}

// ── list ─────────────────────────────────────────────────────────────────────

func TestListChannels_MasksSecrets(t *testing.T) {
	store := NewMockStore().WithRole("admin")
	mux := channelMux(t, store, nil)
	createSlack(t, mux, "https://hooks.slack.com/Y")

	rr := do(t, mux, http.MethodGet, "/v1/channels", "")
	if rr.Code != http.StatusOK {
		t.Fatalf("status %d", rr.Code)
	}
	var list []chanResp
	if err := json.Unmarshal(rr.Body.Bytes(), &list); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(list) != 1 || list[0].Config["webhook_url"] != "***" {
		t.Errorf("expected one masked channel, got %+v", list)
	}
}

// ── update: encryption + mask-preservation, verified by decrypting via /test ──

func TestUpdateChannel_PreservesAndRotatesSecret(t *testing.T) {
	store := NewMockStore().WithRole("admin")
	rt := &recordingTransport{}
	mux := channelMux(t, store, map[string]notifications.Transport{model.ChannelKindSlack: rt})

	id := createSlack(t, mux, "https://hooks.slack.com/ORIGINAL")

	// PATCH only the label + mask the secret → stored webhook must be unchanged.
	rr := do(t, mux, http.MethodPatch, "/v1/channels/"+id,
		`{"label":"renamed","config":{"webhook_url":"***"}}`)
	if rr.Code != http.StatusOK {
		t.Fatalf("patch status %d body %s", rr.Code, rr.Body.String())
	}
	do(t, mux, http.MethodPost, "/v1/channels/"+id+"/test", "")
	if got := decryptSlackWebhook(t, rt.gotChannel); got != "https://hooks.slack.com/ORIGINAL" {
		t.Errorf("masked PATCH must preserve secret, got %q", got)
	}

	// PATCH a genuinely new webhook → stored secret rotates.
	rr = do(t, mux, http.MethodPatch, "/v1/channels/"+id,
		`{"config":{"webhook_url":"https://hooks.slack.com/ROTATED"}}`)
	if rr.Code != http.StatusOK {
		t.Fatalf("patch status %d", rr.Code)
	}
	do(t, mux, http.MethodPost, "/v1/channels/"+id+"/test", "")
	if got := decryptSlackWebhook(t, rt.gotChannel); got != "https://hooks.slack.com/ROTATED" {
		t.Errorf("non-mask PATCH must rotate secret, got %q", got)
	}
}

func TestUpdateChannel_NotFound(t *testing.T) {
	store := NewMockStore().WithRole("admin")
	mux := channelMux(t, store, nil)
	rr := do(t, mux, http.MethodPatch, "/v1/channels/nope", `{"label":"x"}`)
	if rr.Code != http.StatusNotFound {
		t.Fatalf("want 404, got %d", rr.Code)
	}
}

// ── test endpoint ─────────────────────────────────────────────────────────────

func TestTestChannel_SentAndFailedRecordDispatch(t *testing.T) {
	store := NewMockStore().WithRole("admin")
	rt := &recordingTransport{externalID: ""}
	mux := channelMux(t, store, map[string]notifications.Transport{model.ChannelKindSlack: rt})
	id := createSlack(t, mux, "https://hooks.slack.com/Z")

	// Success.
	rr := do(t, mux, http.MethodPost, "/v1/channels/"+id+"/test", "")
	if rr.Code != http.StatusOK {
		t.Fatalf("status %d body %s", rr.Code, rr.Body.String())
	}
	var out map[string]any
	_ = json.Unmarshal(rr.Body.Bytes(), &out)
	if out["status"] != "sent" {
		t.Errorf("want status sent, got %v", out)
	}

	// Failure.
	rt.err = errors.New("slack: status 404: ***")
	rr = do(t, mux, http.MethodPost, "/v1/channels/"+id+"/test", "")
	if rr.Code != http.StatusOK {
		t.Fatalf("failed test should still be 200, got %d", rr.Code)
	}
	_ = json.Unmarshal(rr.Body.Bytes(), &out)
	if out["status"] != "failed" || out["error"] == "" {
		t.Errorf("want failed+error, got %v", out)
	}

	// Both attempts should be recorded as dispatch rows.
	rr = do(t, mux, http.MethodGet, "/v1/channels/"+id+"/dispatches", "")
	if rr.Code != http.StatusOK {
		t.Fatalf("dispatches status %d", rr.Code)
	}
	var rows []model.NotificationDispatch
	_ = json.Unmarshal(rr.Body.Bytes(), &rows)
	if len(rows) != 2 {
		t.Fatalf("want 2 dispatch rows, got %d", len(rows))
	}
	for _, d := range rows {
		if d.Source != model.DispatchSourceTest {
			t.Errorf("a /test dispatch should have source=test, got %q", d.Source)
		}
	}
}

func TestTestChannel_NoTransportsConfigured(t *testing.T) {
	store := NewMockStore().WithRole("admin")
	mux := channelMux(t, store, nil) // no transports
	id := createSlack(t, mux, "https://hooks.slack.com/Z")
	rr := do(t, mux, http.MethodPost, "/v1/channels/"+id+"/test", "")
	if rr.Code != http.StatusInternalServerError {
		t.Fatalf("want 500 when transports unset, got %d", rr.Code)
	}
}

// ── delete ───────────────────────────────────────────────────────────────────

func TestDeleteChannel(t *testing.T) {
	store := NewMockStore().WithRole("admin")
	mux := channelMux(t, store, nil)
	id := createSlack(t, mux, "https://hooks.slack.com/Z")

	if rr := do(t, mux, http.MethodDelete, "/v1/channels/"+id, ""); rr.Code != http.StatusNoContent {
		t.Fatalf("delete status %d", rr.Code)
	}
	if rr := do(t, mux, http.MethodDelete, "/v1/channels/"+id, ""); rr.Code != http.StatusNotFound {
		t.Fatalf("re-delete should 404, got %d", rr.Code)
	}
}

// ── authz ────────────────────────────────────────────────────────────────────

func TestChannels_ViewerCannotManage(t *testing.T) {
	store := NewMockStore().WithRole("viewer")
	mux := channelMux(t, store, nil)

	if rr := do(t, mux, http.MethodGet, "/v1/channels", ""); rr.Code != http.StatusOK {
		t.Errorf("viewer should read channels, got %d", rr.Code)
	}
	if rr := do(t, mux, http.MethodPost, "/v1/channels",
		`{"kind":"slack","label":"x","config":{"webhook_url":"u"}}`); rr.Code != http.StatusForbidden {
		t.Errorf("viewer must not create, got %d", rr.Code)
	}
}

func TestChannels_MemberCannotManage(t *testing.T) {
	// channels:manage is admin+, not member — a member can write accounts but
	// must not manage credential-bearing channels.
	store := NewMockStore().WithRole("member")
	mux := channelMux(t, store, nil)

	if rr := do(t, mux, http.MethodPost, "/v1/channels",
		`{"kind":"slack","label":"x","config":{"webhook_url":"u"}}`); rr.Code != http.StatusForbidden {
		t.Errorf("member must not create a channel, got %d", rr.Code)
	}
	if rr := do(t, mux, http.MethodDelete, "/v1/channels/whatever", ""); rr.Code != http.StatusForbidden {
		t.Errorf("member must not delete a channel, got %d", rr.Code)
	}
}
