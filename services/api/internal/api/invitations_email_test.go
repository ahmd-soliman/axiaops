package api_test

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"axiaops.io/api/internal/api"
	"axiaops.io/shared/model"
	"axiaops.io/shared/notifications"
)

// fakeInviteTransport implements both Transport and notifications.InviteSender
// so it can sit in the handler's channelTransports map under the email kind and
// capture what SendInvite was asked to deliver.
type fakeInviteTransport struct {
	called        int
	gotChannelID  string
	gotRecipient  string
	gotInvite     notifications.InviteEmail
	sendInviteErr error
}

func (f *fakeInviteTransport) Send(_ context.Context, _ model.NotificationChannel, _ notifications.Payload) (string, error) {
	return "", nil
}

func (f *fakeInviteTransport) SendInvite(_ context.Context, ch model.NotificationChannel, recipient string, inv notifications.InviteEmail) error {
	f.called++
	f.gotChannelID = ch.ID
	f.gotRecipient = recipient
	f.gotInvite = inv
	return f.sendInviteErr
}

func enabledEmailChannel(id string) model.NotificationChannel {
	return model.NotificationChannel{ID: id, Kind: model.ChannelKindEmail, Enabled: true, Label: "ops mail"}
}

// inviteEmailMux builds a handler with PUBLIC_HOST + the email transport wired,
// mirroring serverbuild so the invite-email path is exercised end to end.
func inviteEmailMux(store *MockStore, publicHost string, ft *fakeInviteTransport) *http.ServeMux {
	h := api.New(store, noopQueue()).
		WithPublicHost(publicHost).
		WithNotificationTransports(map[string]notifications.Transport{
			model.ChannelKindEmail: ft,
		})
	mux := http.NewServeMux()
	h.Register(mux)
	return mux
}

func postInvite(t *testing.T, mux *http.ServeMux, body string) map[string]any {
	t.Helper()
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, invReq(http.MethodPost, "/v1/invitations", body))
	if w.Code != http.StatusCreated && w.Code != http.StatusOK {
		t.Fatalf("invite create: expected 200/201, got %d (body: %s)", w.Code, w.Body.String())
	}
	var resp map[string]any
	if err := json.NewDecoder(w.Body).Decode(&resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	return resp
}

func TestCreateInvitation_EmailsRedemptionURL(t *testing.T) {
	store := NewMockStore().
		WithOrgName("Acme Corp").
		WithChannels([]model.NotificationChannel{enabledEmailChannel("email-1")})
	ft := &fakeInviteTransport{}
	mux := inviteEmailMux(store, "https://app.example.com", ft)

	resp := postInvite(t, mux, `{"email":"new@example.com","role":"member"}`)

	if resp["email_delivery"] != "sent" {
		t.Fatalf("email_delivery = %v, want sent (resp: %+v)", resp["email_delivery"], resp)
	}
	if ft.called != 1 {
		t.Fatalf("SendInvite called %d times, want 1", ft.called)
	}
	if ft.gotRecipient != "new@example.com" {
		t.Errorf("recipient = %q, want new@example.com", ft.gotRecipient)
	}
	if ft.gotChannelID != "email-1" {
		t.Errorf("channel = %q, want email-1", ft.gotChannelID)
	}
	if ft.gotInvite.OrganizationName != "Acme Corp" {
		t.Errorf("org name = %q, want Acme Corp", ft.gotInvite.OrganizationName)
	}
	if ft.gotInvite.Role != "member" {
		t.Errorf("role = %q, want member", ft.gotInvite.Role)
	}
	// The emailed URL must be the same absolute redemption URL the admin sees.
	redir, _ := resp["redemption_url"].(string)
	if ft.gotInvite.RedemptionURL != redir {
		t.Errorf("emailed URL %q != response redemption_url %q", ft.gotInvite.RedemptionURL, redir)
	}
	if !strings.HasPrefix(ft.gotInvite.RedemptionURL, "https://app.example.com/accept-invite?token=") {
		t.Errorf("emailed URL not the absolute accept-invite link: %q", ft.gotInvite.RedemptionURL)
	}
}

func TestCreateInvitation_NoEmailChannel_SkipsDelivery(t *testing.T) {
	store := NewMockStore() // no channels
	ft := &fakeInviteTransport{}
	mux := inviteEmailMux(store, "https://app.example.com", ft)

	resp := postInvite(t, mux, `{"email":"new@example.com","role":"member"}`)

	if resp["email_delivery"] != "skipped_no_channel" {
		t.Errorf("email_delivery = %v, want skipped_no_channel", resp["email_delivery"])
	}
	if ft.called != 0 {
		t.Errorf("SendInvite must not be called with no email channel (called %d)", ft.called)
	}
	if redir, _ := resp["redemption_url"].(string); redir == "" {
		t.Error("redemption_url must still be returned as the OOB fallback")
	}
}

func TestCreateInvitation_NoPublicHost_SkipsDelivery(t *testing.T) {
	store := NewMockStore().WithChannels([]model.NotificationChannel{enabledEmailChannel("email-1")})
	ft := &fakeInviteTransport{}
	mux := inviteEmailMux(store, "", ft) // PUBLIC_HOST unset → relative URL, can't email

	resp := postInvite(t, mux, `{"email":"new@example.com","role":"member"}`)

	if resp["email_delivery"] != "skipped_no_public_host" {
		t.Errorf("email_delivery = %v, want skipped_no_public_host", resp["email_delivery"])
	}
	if ft.called != 0 {
		t.Errorf("SendInvite must not be called without an absolute URL (called %d)", ft.called)
	}
}

func TestCreateInvitation_EmailFailure_StillCreatesInvite(t *testing.T) {
	store := NewMockStore().WithChannels([]model.NotificationChannel{enabledEmailChannel("email-1")})
	ft := &fakeInviteTransport{sendInviteErr: errors.New("email: send: relay refused")}
	mux := inviteEmailMux(store, "https://app.example.com", ft)

	w := httptest.NewRecorder()
	mux.ServeHTTP(w, invReq(http.MethodPost, "/v1/invitations", `{"email":"new@example.com","role":"member"}`))
	if w.Code != http.StatusCreated {
		t.Fatalf("a mail failure must not fail the invite; got %d (body: %s)", w.Code, w.Body.String())
	}
	var resp map[string]any
	_ = json.NewDecoder(w.Body).Decode(&resp)
	if resp["email_delivery"] != "failed" {
		t.Errorf("email_delivery = %v, want failed", resp["email_delivery"])
	}
	if redir, _ := resp["redemption_url"].(string); redir == "" {
		t.Error("redemption_url must still be returned when delivery fails")
	}
}

func TestCreateInvitation_ChannelLookupError_ReportsError(t *testing.T) {
	store := NewMockStore().
		WithChannels([]model.NotificationChannel{enabledEmailChannel("email-1")}).
		WithListEnabledChannelsError(errors.New("pool exhausted"))
	ft := &fakeInviteTransport{}
	mux := inviteEmailMux(store, "https://app.example.com", ft)

	resp := postInvite(t, mux, `{"email":"new@example.com","role":"member"}`)

	// A DB blip resolving the channel must be distinguishable from "no channel".
	if resp["email_delivery"] != "error" {
		t.Errorf("email_delivery = %v, want error", resp["email_delivery"])
	}
	if ft.called != 0 {
		t.Errorf("SendInvite must not be called when channel lookup errors (called %d)", ft.called)
	}
	if redir, _ := resp["redemption_url"].(string); redir == "" {
		t.Error("redemption_url must still be returned on a channel-lookup error")
	}
}

// A disabled email channel is not used for invite delivery — the enabled toggle
// gates invites just as it gates digests.
func TestCreateInvitation_DisabledEmailChannel_Skips(t *testing.T) {
	disabled := enabledEmailChannel("email-1")
	disabled.Enabled = false
	store := NewMockStore().WithChannels([]model.NotificationChannel{disabled})
	ft := &fakeInviteTransport{}
	mux := inviteEmailMux(store, "https://app.example.com", ft)

	resp := postInvite(t, mux, `{"email":"new@example.com","role":"member"}`)

	if resp["email_delivery"] != "skipped_no_channel" {
		t.Errorf("email_delivery = %v, want skipped_no_channel for a disabled channel", resp["email_delivery"])
	}
	if ft.called != 0 {
		t.Errorf("SendInvite must not use a disabled channel (called %d)", ft.called)
	}
}
