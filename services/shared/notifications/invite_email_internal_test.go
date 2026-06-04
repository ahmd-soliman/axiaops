package notifications

import (
	"context"
	"strings"
	"testing"
	"time"

	"axiaops.io/shared/model"
)

// White-box: injects the unexported sendMail seam to assert the invite message
// targets the invitee (not the channel's digest recipients) and carries the
// redemption URL, without a live relay. Shares capturedSend / encEmailConfig /
// emailTestKey with email_smtp_internal_test.go.

func inviteEmailCfg() model.EmailConfig {
	return model.EmailConfig{
		SMTPHost: "smtp.example.com", SMTPPort: 587,
		SMTPUser: "user", SMTPPass: "secretpass",
		From: "ops@example.com", FromName: "AxiaOps",
		// Digest recipients — must NOT receive the invite.
		Recipients: []string{"digest@example.com"},
	}
}

func TestEmailTransport_SendInvite_TargetsInviteeOnly(t *testing.T) {
	sender := &capturedSend{}
	tr := &EmailTransport{sendMail: sender.fn()}

	err := tr.SendInvite(context.Background(), inviteEmailCfg(), "invitee@example.com", InviteEmail{
		OrganizationName: "Acme Corp",
		Role:             "member",
		InviterEmail:     "admin@acme.test",
		RedemptionURL:    "https://app.example.com/accept-invite?token=abc123",
		ExpiresAt:        time.Date(2026, 7, 1, 0, 0, 0, 0, time.UTC),
	})
	if err != nil {
		t.Fatalf("SendInvite: %v", err)
	}
	if !sender.called {
		t.Fatal("sendMail was not called")
	}
	if len(sender.to) != 1 || sender.to[0] != "invitee@example.com" {
		t.Errorf("envelope recipients = %v, want only [invitee@example.com]", sender.to)
	}
	if sender.from != "ops@example.com" {
		t.Errorf("envelope from = %q, want bare address", sender.from)
	}
	msg := string(sender.msg)
	checks := []string{
		"To: <invitee@example.com>",
		"Subject: You've been invited to Acme Corp on AxiaOps",
		"admin@acme.test",
		"as a member",
		"https://app.example.com/accept-invite?token=abc123",
		"expires on 1 July 2026",
	}
	for _, want := range checks {
		if !strings.Contains(msg, want) {
			t.Errorf("invite message missing %q:\n%s", want, msg)
		}
	}
	if strings.Contains(msg, "digest@example.com") {
		t.Errorf("invite leaked the channel's digest recipient:\n%s", msg)
	}
}

func TestEmailTransport_SendInvite_BlankOrgAndExpiry(t *testing.T) {
	sender := &capturedSend{}
	tr := &EmailTransport{sendMail: sender.fn()}

	// No org name, no inviter, no expiry — the generic fallbacks must hold and
	// the message must not render "to ." or a bogus expiry line.
	err := tr.SendInvite(context.Background(), inviteEmailCfg(), "invitee@example.com", InviteEmail{
		RedemptionURL: "https://app.example.com/accept-invite?token=z",
	})
	if err != nil {
		t.Fatalf("SendInvite: %v", err)
	}
	msg := string(sender.msg)
	if !strings.Contains(msg, "Subject: You've been invited to "+inviteOrgFallback) {
		t.Errorf("blank org should fall back to %q:\n%s", inviteOrgFallback, msg)
	}
	if strings.Contains(msg, "expires on") {
		t.Errorf("zero expiry should omit the expiry line:\n%s", msg)
	}
}

func TestEmailTransport_SendInvite_RequiresRecipient(t *testing.T) {
	sender := &capturedSend{}
	tr := &EmailTransport{sendMail: sender.fn()}

	if err := tr.SendInvite(context.Background(), inviteEmailCfg(), "   ", InviteEmail{RedemptionURL: "x"}); err == nil ||
		!strings.Contains(err.Error(), "recipient") {
		t.Fatalf("want recipient-required error, got %v", err)
	}
	if sender.called {
		t.Error("sendMail must not be called with a blank recipient")
	}
}

func TestEmailTransport_SendInvite_ScrubsPasswordFromError(t *testing.T) {
	const pass = "secretpass"
	sender := &capturedSend{returnErr: errStr("535 rejected pass=" + pass)}
	tr := &EmailTransport{sendMail: sender.fn()}

	err := tr.SendInvite(context.Background(), inviteEmailCfg(), "invitee@example.com", InviteEmail{RedemptionURL: "x"})
	if err == nil {
		t.Fatal("expected send error")
	}
	if strings.Contains(err.Error(), pass) {
		t.Errorf("error leaked the SMTP password: %q", err)
	}
}

// EmailTransport must satisfy InviteSender — the API handler type-asserts this.
var _ InviteSender = (*EmailTransport)(nil)

type errStr string

func (e errStr) Error() string { return string(e) }
