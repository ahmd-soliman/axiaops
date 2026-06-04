package api_test

import (
	"context"
	"encoding/json"
	"errors"
	"testing"

	"axiaops.io/api/internal/api"
	"axiaops.io/shared/crypto"
	"axiaops.io/shared/model"
	"axiaops.io/shared/notifications"
)

const inviteMailerKey = "00112233445566778899aabbccddeeff00112233445566778899aabbccddeeff"

// fakeInviteSender records the resolved config + recipient the mailer hands the
// transport, so a test can assert which source (channel vs global) won.
type fakeInviteSender struct {
	called       int
	gotCfg       model.EmailConfig
	gotRecipient string
	gotInvite    notifications.InviteEmail
	err          error
}

func (f *fakeInviteSender) SendInvite(_ context.Context, cfg model.EmailConfig, recipient string, inv notifications.InviteEmail) error {
	f.called++
	f.gotCfg = cfg
	f.gotRecipient = recipient
	f.gotInvite = inv
	return f.err
}

// encChannel returns an enabled email channel whose ciphertext decrypts to cfg.
func encChannel(t *testing.T, id string, cfg model.EmailConfig) model.NotificationChannel {
	t.Helper()
	t.Setenv("ENCRYPTION_KEY", inviteMailerKey)
	raw, _ := json.Marshal(cfg)
	ct, err := crypto.Encrypt(string(raw))
	if err != nil {
		t.Fatalf("encrypt channel config: %v", err)
	}
	return model.NotificationChannel{ID: id, Kind: model.ChannelKindEmail, Enabled: true, ConfigCiphertext: ct}
}

func req(recipient string) api.InviteMailRequest {
	return api.InviteMailRequest{
		OrganizationID: "organization-me",
		Recipient:      recipient,
		Role:           "member",
		RedemptionURL:  "https://app.example.com/accept-invite?token=t",
	}
}

const gmailRelayHost = "smtp-relay.gmail.com"

func globalGmailCfg() model.EmailConfig {
	return model.EmailConfig{SMTPHost: gmailRelayHost, SMTPPort: 587, SMTPUser: "relay@axiaops.io", SMTPPass: "app-pw", From: "noreply@axiaops.io"}
}

func TestInviteMailer_ChannelFirst(t *testing.T) {
	chanCfg := model.EmailConfig{SMTPHost: "smtp.acme.test", SMTPPort: 587, From: "ops@acme.test"}
	store := NewMockStore().
		WithOrgName("Acme Corp").
		WithChannels([]model.NotificationChannel{encChannel(t, "email-1", chanCfg)})
	ft := &fakeInviteSender{}
	// Global config is ALSO set — channel must win.
	mailer := api.NewInviteMailer(store, ft, globalGmailCfg(), "https://app.example.com")

	if out := mailer.SendInvite(context.Background(), req("new@example.com")); out != "sent" {
		t.Fatalf("outcome = %q, want sent", out)
	}
	if ft.gotCfg.SMTPHost != "smtp.acme.test" {
		t.Errorf("used SMTP host %q, want the channel's smtp.acme.test (channel-first)", ft.gotCfg.SMTPHost)
	}
	if ft.gotRecipient != "new@example.com" {
		t.Errorf("recipient = %q", ft.gotRecipient)
	}
	if ft.gotInvite.OrganizationName != "Acme Corp" {
		t.Errorf("org name = %q, want Acme Corp", ft.gotInvite.OrganizationName)
	}
}

func TestInviteMailer_GlobalFallback(t *testing.T) {
	store := NewMockStore().WithOrgName("Acme Corp") // no channels
	ft := &fakeInviteSender{}
	mailer := api.NewInviteMailer(store, ft, globalGmailCfg(), "https://app.example.com")

	if out := mailer.SendInvite(context.Background(), req("new@example.com")); out != "sent" {
		t.Fatalf("outcome = %q, want sent", out)
	}
	if ft.gotCfg.SMTPHost != gmailRelayHost {
		t.Errorf("used SMTP host %q, want the global %s (fallback)", ft.gotCfg.SMTPHost, gmailRelayHost)
	}
}

// A disabled digest channel must not block invites — the global mailer takes
// over (the exact gap the architecture review flagged).
func TestInviteMailer_DisabledChannel_FallsBackToGlobal(t *testing.T) {
	chanCfg := model.EmailConfig{SMTPHost: "smtp.acme.test", SMTPPort: 587, From: "ops@acme.test"}
	disabled := encChannel(t, "email-1", chanCfg)
	disabled.Enabled = false
	store := NewMockStore().WithChannels([]model.NotificationChannel{disabled})
	ft := &fakeInviteSender{}
	mailer := api.NewInviteMailer(store, ft, globalGmailCfg(), "https://app.example.com")

	if out := mailer.SendInvite(context.Background(), req("new@example.com")); out != "sent" {
		t.Fatalf("outcome = %q, want sent", out)
	}
	if ft.gotCfg.SMTPHost != gmailRelayHost {
		t.Errorf("used %q, want global fallback when the only channel is disabled", ft.gotCfg.SMTPHost)
	}
}

// A corrupt first email channel must not strand the invite on the global
// fallback when a valid second email channel exists — the loop tries the next
// one before falling through.
func TestInviteMailer_CorruptFirstChannel_UsesNextChannel(t *testing.T) {
	good := encChannel(t, "email-2", model.EmailConfig{SMTPHost: "smtp.good.test", SMTPPort: 587, From: "ops@good.test"})
	corrupt := model.NotificationChannel{ID: "email-1", Kind: model.ChannelKindEmail, Enabled: true, ConfigCiphertext: "not-decryptable"}
	store := NewMockStore().WithChannels([]model.NotificationChannel{corrupt, good})
	ft := &fakeInviteSender{}
	mailer := api.NewInviteMailer(store, ft, globalGmailCfg(), "https://app.example.com")

	if out := mailer.SendInvite(context.Background(), req("new@example.com")); out != "sent" {
		t.Fatalf("outcome = %q, want sent", out)
	}
	if ft.gotCfg.SMTPHost != "smtp.good.test" {
		t.Errorf("used SMTP host %q, want the valid second channel smtp.good.test (not the global fallback)", ft.gotCfg.SMTPHost)
	}
}

func TestInviteMailer_NoTransportConfigured(t *testing.T) {
	store := NewMockStore() // no channels
	ft := &fakeInviteSender{}
	// Zero global config ⇒ nothing to send through.
	mailer := api.NewInviteMailer(store, ft, model.EmailConfig{}, "https://app.example.com")

	if out := mailer.SendInvite(context.Background(), req("new@example.com")); out != "skipped_no_transport" {
		t.Errorf("outcome = %q, want skipped_no_transport", out)
	}
	if ft.called != 0 {
		t.Errorf("transport must not be called with no config (called %d)", ft.called)
	}
}

func TestInviteMailer_NoPublicHost(t *testing.T) {
	store := NewMockStore().WithChannels([]model.NotificationChannel{encChannel(t, "email-1", globalGmailCfg())})
	ft := &fakeInviteSender{}
	mailer := api.NewInviteMailer(store, ft, globalGmailCfg(), "") // PUBLIC_HOST unset

	if out := mailer.SendInvite(context.Background(), req("new@example.com")); out != "skipped_no_public_host" {
		t.Errorf("outcome = %q, want skipped_no_public_host", out)
	}
	if ft.called != 0 {
		t.Errorf("transport must not be called without an absolute URL (called %d)", ft.called)
	}
}

func TestInviteMailer_ChannelLookupError(t *testing.T) {
	store := NewMockStore().WithListEnabledChannelsError(errors.New("pool exhausted"))
	ft := &fakeInviteSender{}
	mailer := api.NewInviteMailer(store, ft, globalGmailCfg(), "https://app.example.com")

	if out := mailer.SendInvite(context.Background(), req("new@example.com")); out != "error" {
		t.Errorf("outcome = %q, want error (distinct from skipped_no_transport)", out)
	}
	if ft.called != 0 {
		t.Errorf("transport must not be called on a channel-lookup error (called %d)", ft.called)
	}
}

func TestInviteMailer_SendFailure(t *testing.T) {
	store := NewMockStore()
	ft := &fakeInviteSender{err: errors.New("email: send: relay refused")}
	mailer := api.NewInviteMailer(store, ft, globalGmailCfg(), "https://app.example.com")

	if out := mailer.SendInvite(context.Background(), req("new@example.com")); out != "failed" {
		t.Errorf("outcome = %q, want failed", out)
	}
	if ft.called != 1 {
		t.Errorf("transport should have been called once, got %d", ft.called)
	}
}
