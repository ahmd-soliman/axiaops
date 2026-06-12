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

// TestEmailTransport_SendInvite_OrgNameHeaderInjection (audit N-2): the org
// display name is interpolated into the Subject header. A CRLF-bearing name —
// possible if any org-name write path forgets the control-character check —
// must NOT yield an injected header; headerValue collapses the newlines so the
// Subject stays one line.
func TestEmailTransport_SendInvite_OrgNameHeaderInjection(t *testing.T) {
	sender := &capturedSend{}
	tr := &EmailTransport{sendMail: sender.fn()}

	err := tr.SendInvite(context.Background(), inviteEmailCfg(), "invitee@example.com", InviteEmail{
		OrganizationName: "Evil Corp\r\nBcc: attacker@evil.test\r\nX-Injected: 1",
		RedemptionURL:    "https://app.example.com/accept-invite?token=abc123",
	})
	if err != nil {
		t.Fatalf("SendInvite: %v", err)
	}

	msg := string(sender.msg)
	headers, _, found := strings.Cut(msg, "\r\n\r\n")
	if !found {
		t.Fatalf("message has no header/body separator:\n%s", msg)
	}
	for _, line := range strings.Split(headers, "\r\n") {
		if strings.HasPrefix(line, "Bcc:") || strings.HasPrefix(line, "X-Injected:") {
			t.Errorf("org name injected a header line %q:\n%s", line, msg)
		}
	}
	// Exactly one Subject line, and the hostile text survives only as flattened
	// content within it (the attacker string ends up inside the Subject value,
	// not promoted to its own header). Splitting on \r\n already guarantees no
	// embedded newline per line; the load-bearing checks are "no injected
	// header lines" (above) and "exactly one Subject carrying the content".
	var subjectLines int
	for _, line := range strings.Split(headers, "\r\n") {
		if strings.HasPrefix(line, "Subject:") {
			subjectLines++
			if !strings.Contains(line, "attacker@evil.test") {
				t.Errorf("attacker text should be flattened into the Subject value, got %q", line)
			}
		}
	}
	if subjectLines != 1 {
		t.Errorf("want exactly 1 Subject header, got %d:\n%s", subjectLines, headers)
	}
}

// TestValidateEmailConfig_RejectsCRLF (audit N-2): the global SMTP_* env path
// reaches ValidateEmailConfig without passing the channel handler's create-time
// CRLF checks, so the shared validator must reject newlines itself.
func TestValidateEmailConfig_RejectsCRLF(t *testing.T) {
	base := func() model.EmailConfig {
		return model.EmailConfig{SMTPHost: "smtp.example.com", SMTPPort: 587, From: "ops@example.com"}
	}
	if err := ValidateEmailConfig(base()); err != nil {
		t.Fatalf("base config should validate: %v", err)
	}

	cases := []struct {
		name   string
		mutate func(*model.EmailConfig)
	}{
		{"from with CRLF", func(c *model.EmailConfig) { c.From = "ops@example.com\r\nBcc: x@evil.test" }},
		{"from with bare LF", func(c *model.EmailConfig) { c.From = "ops@example.com\nBcc: x@evil.test" }},
		{"from_name with CRLF", func(c *model.EmailConfig) { c.FromName = "Ops\r\nBcc: x@evil.test" }},
		{"recipient with CRLF", func(c *model.EmailConfig) { c.Recipients = []string{"a@b.test\r\nBcc: x@evil.test"} }},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			cfg := base()
			tc.mutate(&cfg)
			if err := ValidateEmailConfig(cfg); err == nil {
				t.Fatal("config with newline must be rejected")
			}
		})
	}
}
