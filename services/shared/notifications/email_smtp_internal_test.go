package notifications

import (
	"context"
	"encoding/json"
	"errors"
	"net/mail"
	"net/smtp"
	"strings"
	"sync"
	"testing"

	"axiaops.io/shared/crypto"
	"axiaops.io/shared/model"
)

// White-box test: it injects the unexported sendMail seam to assert message
// composition / recipients / auth / ctx handling without a live SMTP relay.

const emailTestKey = "00112233445566778899aabbccddeeff00112233445566778899aabbccddeeff"

func encEmailConfig(t *testing.T, cfg model.EmailConfig) string {
	t.Helper()
	t.Setenv("ENCRYPTION_KEY", emailTestKey)
	raw, _ := json.Marshal(cfg)
	ct, err := crypto.Encrypt(string(raw))
	if err != nil {
		t.Fatalf("encrypt: %v", err)
	}
	return ct
}

func emailPayload() Payload {
	return Payload{
		AccountID:      "acct-1",
		ZombieCount:    3,
		MonthlySavings: 100,
		Currency:       "USD",
		TopServices:    []ServiceRow{{Service: "AmazonEC2", Count: 2, Savings: 80}},
		DashboardURL:   "https://app.example.com",
	}
}

type capturedSend struct {
	mu         sync.Mutex
	called     bool
	addr, from string
	to         []string
	msg        []byte
	authNil    bool
	returnErr  error
	block      chan struct{} // when non-nil, blocks until closed (ctx tests)
}

func (c *capturedSend) fn() func(string, smtp.Auth, string, []string, []byte) error {
	return func(addr string, a smtp.Auth, from string, to []string, msg []byte) error {
		if c.block != nil {
			<-c.block
		}
		c.mu.Lock()
		c.called = true
		c.addr, c.from, c.to, c.msg, c.authNil = addr, from, to, msg, a == nil
		c.mu.Unlock()
		return c.returnErr
	}
}

func emailChannel(t *testing.T, cfg model.EmailConfig) model.NotificationChannel {
	return model.NotificationChannel{ID: "email-1", Kind: model.ChannelKindEmail, ConfigCiphertext: encEmailConfig(t, cfg)}
}

func TestEmailTransport_Success_ComposesAndAuths(t *testing.T) {
	sender := &capturedSend{}
	tr := &EmailTransport{sendMail: sender.fn()}
	cfg := model.EmailConfig{
		SMTPHost: "smtp.example.com", SMTPPort: 587,
		SMTPUser: "user", SMTPPass: "secretpass",
		From: "ops@example.com", Recipients: []string{"a@example.com", "b@example.com"},
	}

	ext, err := tr.Send(context.Background(), emailChannel(t, cfg), emailPayload())
	if err != nil {
		t.Fatalf("Send: %v", err)
	}
	if ext != "" {
		t.Errorf("email externalID should be empty, got %q", ext)
	}
	if sender.addr != "smtp.example.com:587" {
		t.Errorf("addr = %q", sender.addr)
	}
	if sender.from != "ops@example.com" || len(sender.to) != 2 {
		t.Errorf("from/to wrong: %q %v", sender.from, sender.to)
	}
	if sender.authNil {
		t.Error("auth should be set when smtp_user present")
	}
	msg := string(sender.msg)
	if !strings.Contains(msg, "Subject: AxiaOps:") || !strings.Contains(msg, "$100.00") {
		t.Errorf("composed message missing subject/savings:\n%s", msg)
	}
	if !strings.Contains(msg, "To: a@example.com, b@example.com") {
		t.Errorf("To header missing recipients:\n%s", msg)
	}
	if !strings.Contains(msg, "AmazonEC2") {
		t.Errorf("body missing service breakdown:\n%s", msg)
	}
}

func TestEmailTransport_NoAuthWhenNoUser(t *testing.T) {
	sender := &capturedSend{}
	tr := &EmailTransport{sendMail: sender.fn()}
	cfg := model.EmailConfig{SMTPHost: "h", SMTPPort: 25, From: "f@x.com", Recipients: []string{"r@x.com"}}

	if _, err := tr.Send(context.Background(), emailChannel(t, cfg), emailPayload()); err != nil {
		t.Fatalf("Send: %v", err)
	}
	if !sender.authNil {
		t.Error("auth must be nil when smtp_user is empty")
	}
}

func TestEmailTransport_ValidationErrors(t *testing.T) {
	cases := []struct {
		name string
		cfg  model.EmailConfig
		want string
	}{
		{"no host", model.EmailConfig{SMTPPort: 25, From: "f@x.com", Recipients: []string{"r@x.com"}}, "smtp_host"},
		{"no port", model.EmailConfig{SMTPHost: "h", From: "f@x.com", Recipients: []string{"r@x.com"}}, "smtp_port"},
		{"no from", model.EmailConfig{SMTPHost: "h", SMTPPort: 25, Recipients: []string{"r@x.com"}}, "from"},
		{"no recipients", model.EmailConfig{SMTPHost: "h", SMTPPort: 25, From: "f@x.com"}, "recipient"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			sender := &capturedSend{}
			tr := &EmailTransport{sendMail: sender.fn()}
			_, err := tr.Send(context.Background(), emailChannel(t, tc.cfg), emailPayload())
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("want error containing %q, got %v", tc.want, err)
			}
			if sender.called {
				t.Error("sendMail must not be called when config is invalid")
			}
		})
	}
}

func TestEmailTransport_ScrubsPasswordFromError(t *testing.T) {
	const pass = "supersecret123"
	sender := &capturedSend{returnErr: errors.New("535 auth rejected pass=" + pass)}
	tr := &EmailTransport{sendMail: sender.fn()}
	cfg := model.EmailConfig{SMTPHost: "h", SMTPPort: 25, SMTPUser: "u", SMTPPass: pass, From: "f@x.com", Recipients: []string{"r@x.com"}}

	_, err := tr.Send(context.Background(), emailChannel(t, cfg), emailPayload())
	if err == nil {
		t.Fatal("expected send error")
	}
	if strings.Contains(err.Error(), pass) {
		t.Errorf("error leaked the SMTP password: %q", err)
	}
	if !strings.Contains(err.Error(), "***") {
		t.Errorf("password should be scrubbed to ***: %q", err)
	}
}

func TestEmailTransport_RespectsContextDeadline(t *testing.T) {
	sender := &capturedSend{block: make(chan struct{})}
	t.Cleanup(func() { close(sender.block) }) // release the leaked goroutine
	tr := &EmailTransport{sendMail: sender.fn()}
	cfg := model.EmailConfig{SMTPHost: "h", SMTPPort: 25, From: "f@x.com", Recipients: []string{"r@x.com"}}

	ctx, cancel := context.WithCancel(context.Background())
	cancel() // already done

	_, err := tr.Send(ctx, emailChannel(t, cfg), emailPayload())
	if err == nil || !strings.Contains(err.Error(), "context canceled") {
		t.Fatalf("want context-canceled error, got %v", err)
	}
}

func TestFormatFromHeader(t *testing.T) {
	cases := []struct {
		name              string
		cfg               model.EmailConfig
		wantName, wantAdr string
	}{
		{"explicit name", model.EmailConfig{FromName: "AxiaOps", From: "noreply@example.com"}, "AxiaOps", "noreply@example.com"},
		{"blank falls back to brand", model.EmailConfig{From: "noreply@example.com"}, defaultSenderName, "noreply@example.com"},
		// A name with a comma/period is not a bare atom; mail.Address must quote
		// it so the header still parses — the whole reason we render via
		// mail.Address instead of fmt.Sprintf.
		{"name needing quotes", model.EmailConfig{FromName: "AxiaOps, Inc.", From: "noreply@example.com"}, "AxiaOps, Inc.", "noreply@example.com"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := formatFromHeader(tc.cfg)
			addr, err := mail.ParseAddress(got)
			if err != nil {
				t.Fatalf("formatFromHeader(%+v) = %q, not a parseable address: %v", tc.cfg, got, err)
			}
			if addr.Name != tc.wantName {
				t.Errorf("name = %q, want %q (header %q)", addr.Name, tc.wantName, got)
			}
			if addr.Address != tc.wantAdr {
				t.Errorf("address = %q, want %q (header %q)", addr.Address, tc.wantAdr, got)
			}
		})
	}
}

// TestEmailTransport_DisplayNameHeaderOnly proves the sender display name lands
// in the From: header but the SMTP envelope (MAIL FROM) stays the bare address —
// the split that keeps strict relays from rejecting the send.
func TestEmailTransport_DisplayNameHeaderOnly(t *testing.T) {
	sender := &capturedSend{}
	tr := &EmailTransport{sendMail: sender.fn()}
	cfg := model.EmailConfig{
		SMTPHost: "smtp.example.com", SMTPPort: 587,
		From: "noreply@example.com", FromName: "AxiaOps",
		Recipients: []string{"r@example.com"},
	}
	if _, err := tr.Send(context.Background(), emailChannel(t, cfg), emailPayload()); err != nil {
		t.Fatalf("Send: %v", err)
	}
	if sender.from != "noreply@example.com" {
		t.Errorf("envelope from = %q, want bare address noreply@example.com", sender.from)
	}
	var fromHeader string
	for _, line := range strings.Split(string(sender.msg), "\r\n") {
		if rest, ok := strings.CutPrefix(line, "From: "); ok {
			fromHeader = rest
			break
		}
	}
	addr, err := mail.ParseAddress(fromHeader)
	if err != nil {
		t.Fatalf("From header %q not parseable: %v", fromHeader, err)
	}
	if addr.Name != "AxiaOps" || addr.Address != "noreply@example.com" {
		t.Errorf("From header = %q, want display name AxiaOps over bare address", fromHeader)
	}
}

// TestEmailTransport_BlankFromNameDefaultsBrand pins the fallback at the
// transport level: a channel with no from_name still composes a From: header
// carrying the "AxiaOps" brand, not a bare address.
func TestEmailTransport_BlankFromNameDefaultsBrand(t *testing.T) {
	sender := &capturedSend{}
	tr := &EmailTransport{sendMail: sender.fn()}
	cfg := model.EmailConfig{
		SMTPHost: "smtp.example.com", SMTPPort: 587,
		From: "noreply@example.com", // no FromName
		Recipients: []string{"r@example.com"},
	}
	if _, err := tr.Send(context.Background(), emailChannel(t, cfg), emailPayload()); err != nil {
		t.Fatalf("Send: %v", err)
	}
	var fromHeader string
	for _, line := range strings.Split(string(sender.msg), "\r\n") {
		if rest, ok := strings.CutPrefix(line, "From: "); ok {
			fromHeader = rest
			break
		}
	}
	addr, err := mail.ParseAddress(fromHeader)
	if err != nil {
		t.Fatalf("From header %q not parseable: %v", fromHeader, err)
	}
	if addr.Name != defaultSenderName {
		t.Errorf("From header = %q, want default brand name %q", fromHeader, defaultSenderName)
	}
}

func TestHeloName(t *testing.T) {
	cases := []struct{ from, want string }{
		{"ops@example.com", "example.com"},
		{"notifications@axiaops.io", "axiaops.io"},
		{"user@sub.corp.example.com", "sub.corp.example.com"},
		{"no-at-sign", "localhost"}, // malformed: no domain
		{"trailing@", "localhost"},  // empty domain after @
		{"", "localhost"},           // empty from
	}
	for _, tc := range cases {
		if got := heloName(tc.from); got != tc.want {
			t.Errorf("heloName(%q) = %q, want %q", tc.from, got, tc.want)
		}
	}
}

func TestEmailTransport_BadCiphertext(t *testing.T) {
	t.Setenv("ENCRYPTION_KEY", emailTestKey)
	tr := &EmailTransport{sendMail: (&capturedSend{}).fn()}
	ch := model.NotificationChannel{ID: "email-1", Kind: model.ChannelKindEmail, ConfigCiphertext: "not-hex"}
	if _, err := tr.Send(context.Background(), ch, emailPayload()); err == nil || !strings.Contains(err.Error(), "decrypt") {
		t.Fatalf("want decrypt error, got %v", err)
	}
}
