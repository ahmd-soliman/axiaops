package notifications

import (
	"context"
	"crypto/tls"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/mail"
	"net/smtp"
	"strconv"
	"strings"
	"time"

	"axiaops.io/shared/crypto"
	"axiaops.io/shared/model"
)

// EmailTransport sends a digest via SMTP — SES or any relay. No third-party SDK;
// stdlib net/smtp. The sendMail seam is injectable so tests can assert the
// composed message + recipients + auth without a live relay.
type EmailTransport struct {
	// sendMail mirrors smtp.SendMail. Overridden in tests.
	sendMail func(addr string, a smtp.Auth, from string, to []string, msg []byte) error
}

// NewEmailTransport builds an email transport backed by dialingSendMail — a
// timeout-bounded reimplementation of smtp.SendMail (stdlib SendMail uses a
// deadline-less net.Dial, so a black-holed relay hangs the send goroutine on
// the OS connect timeout for tens of seconds, leaking a socket per scan).
func NewEmailTransport() *EmailTransport {
	return &EmailTransport{sendMail: dialingSendMail}
}

// smtpDeadline bounds the whole SMTP exchange (dial + handshake + data). Set on
// the connection so the send goroutine self-terminates within this window
// instead of outliving the dispatcher's per-transport timeout.
const smtpDeadline = DefaultSendTimeout

// dialingSendMail mirrors net/smtp.SendMail's flow (EHLO → STARTTLS if offered →
// AUTH if offered → MAIL/RCPT/DATA → QUIT) but dials with a timeout and sets an
// overall connection deadline, so no I/O step can block unboundedly.
func dialingSendMail(addr string, auth smtp.Auth, from string, to []string, msg []byte) error {
	host, _, err := net.SplitHostPort(addr)
	if err != nil {
		return err
	}
	conn, err := net.DialTimeout("tcp", addr, smtpDeadline)
	if err != nil {
		return err
	}
	// Bound every subsequent read/write; a wedged-after-connect relay can't hang.
	_ = conn.SetDeadline(time.Now().Add(smtpDeadline))

	c, err := smtp.NewClient(conn, host)
	if err != nil {
		_ = conn.Close()
		return err
	}
	defer func() { _ = c.Close() }()

	// EHLO with the sender's domain instead of stdlib's default "localhost".
	// Hardened relays (Postfix reject_unknown_helo_hostname, some cloud relays)
	// drop a connection that greets as "localhost", which surfaces here as a
	// post-greeting EOF. Must run before the first c.Extension() call, which
	// otherwise triggers a lazy EHLO with the default name; StartTLS re-EHLOs
	// with this same name afterward.
	if err := c.Hello(heloName(from)); err != nil {
		return err
	}

	if ok, _ := c.Extension("STARTTLS"); ok {
		if err := c.StartTLS(&tls.Config{ServerName: host}); err != nil {
			return err
		}
	}
	if auth != nil {
		if ok, _ := c.Extension("AUTH"); ok {
			if err := c.Auth(auth); err != nil {
				return err
			}
		}
	}
	if err := c.Mail(from); err != nil {
		return err
	}
	for _, rcpt := range to {
		if err := c.Rcpt(rcpt); err != nil {
			return err
		}
	}
	w, err := c.Data()
	if err != nil {
		return err
	}
	if _, err := w.Write(msg); err != nil {
		return err
	}
	if err := w.Close(); err != nil {
		return err
	}
	// DATA was accepted (w.Close returned the relay's final 250) — the message
	// is committed. Some relays close the connection without answering QUIT,
	// which makes c.Quit() return io.EOF; treat that as non-fatal so a delivered
	// message isn't reported as a failed send. The deferred c.Close() still
	// releases the socket.
	if err := c.Quit(); err != nil {
		slog.Debug("email: QUIT after accepted message failed (treating as sent)", "error", err)
	}
	return nil
}

// heloName derives the EHLO/HELO hostname from the envelope sender's domain,
// falling back to "localhost" for a malformed/domainless from. A real domain
// keeps strict relays from dropping the connection on an unknown HELO.
func heloName(from string) string {
	if at := strings.LastIndex(from, "@"); at >= 0 && at < len(from)-1 {
		return from[at+1:]
	}
	return "localhost"
}

// Send implements Transport. externalID is always empty for email.
func (t *EmailTransport) Send(ctx context.Context, channel model.NotificationChannel, payload Payload) (string, error) {
	cfg, err := DecodeEmailConfig(channel)
	if err != nil {
		return "", err
	}
	if len(cfg.Recipients) == 0 {
		return "", fmt.Errorf("email: at least one recipient is required")
	}
	return "", t.deliver(ctx, cfg, cfg.Recipients, buildEmailMessage(cfg, payload))
}

// DecodeEmailConfig decrypts a channel's config blob into an EmailConfig and
// validates the SMTP transport fields shared by every email send (digest or
// invite). Recipient validation is left to the caller — a digest fans out to
// cfg.Recipients, an invite targets a single supplied address. Exported so the
// api-layer invite mailer can resolve a channel into a plaintext config and
// hand it to SendInvite, the same way the digest path does internally.
func DecodeEmailConfig(channel model.NotificationChannel) (model.EmailConfig, error) {
	plaintext, err := crypto.Decrypt(channel.ConfigCiphertext)
	if err != nil {
		return model.EmailConfig{}, fmt.Errorf("email: decrypt config: %w", err)
	}
	var cfg model.EmailConfig
	if err := json.Unmarshal([]byte(plaintext), &cfg); err != nil {
		return model.EmailConfig{}, fmt.Errorf("email: decode config: %w", err)
	}
	if err := ValidateEmailConfig(cfg); err != nil {
		return model.EmailConfig{}, err
	}
	return cfg, nil
}

// ValidateEmailConfig checks the SMTP transport fields every email send needs.
// Used both by DecodeEmailConfig (channel-sourced config) and directly by the
// invite mailer when it sources config from the global env/SSM SMTP settings.
//
// CR/LF rejection (audit N-2): From/FromName/Recipients are interpolated into
// RFC 5322 headers. The channel CRUD already rejects newlines at create time,
// but the global SMTP_* env path reaches this validator without ever passing
// through that handler — so the header-injection check must live here too.
func ValidateEmailConfig(cfg model.EmailConfig) error {
	if cfg.SMTPHost == "" || cfg.SMTPPort == 0 {
		return fmt.Errorf("email: smtp_host and smtp_port are required")
	}
	if cfg.From == "" {
		return fmt.Errorf("email: from is required")
	}
	if strings.ContainsAny(cfg.From, "\r\n") {
		return fmt.Errorf("email: from must not contain newlines")
	}
	if strings.ContainsAny(cfg.FromName, "\r\n") {
		return fmt.Errorf("email: from_name must not contain newlines")
	}
	for _, r := range cfg.Recipients {
		if strings.ContainsAny(r, "\r\n") {
			return fmt.Errorf("email: recipient must not contain newlines")
		}
	}
	return nil
}

// headerValue makes s safe for direct interpolation into a single RFC 5322
// header line: CR and LF collapse to spaces. Last line of defence (audit N-2)
// — upstream validation rejects newlines in every admin-settable field, but
// header composition must never trust that every future caller validated.
// Values routed through net/mail.Address don't need this (it encodes); only
// raw fmt.Fprintf interpolations do.
func headerValue(s string) string {
	if !strings.ContainsAny(s, "\r\n") {
		return s
	}
	s = strings.ReplaceAll(s, "\r", " ")
	return strings.ReplaceAll(s, "\n", " ")
}

// deliver runs the timeout-bounded, secret-scrubbing SMTP send shared by the
// digest and invite paths. msg is a fully-composed RFC 5322 message; to is the
// envelope recipient set.
func (t *EmailTransport) deliver(ctx context.Context, cfg model.EmailConfig, to []string, msg []byte) error {
	addr := net.JoinHostPort(cfg.SMTPHost, strconv.Itoa(cfg.SMTPPort))

	var auth smtp.Auth
	if cfg.SMTPUser != "" {
		auth = smtp.PlainAuth("", cfg.SMTPUser, cfg.SMTPPass, cfg.SMTPHost)
	}

	// net/smtp.SendMail is blocking and ctx-unaware. Run it in a goroutine and
	// race it against the caller's deadline so a wedged relay can't stall the
	// scan (or the invite request) past the per-transport timeout. `done` is
	// buffered so the goroutine can always send and exit even after we've
	// returned on the ctx path — it outlives this call but never blocks
	// (acceptable for v1: single attempt, no retry — see
	// docs/notifications-plan.md "Risks + deferred → Retry / DLQ").
	done := make(chan error, 1)
	go func() {
		done <- t.sendMail(addr, auth, cfg.From, to, msg)
	}()

	select {
	case <-ctx.Done():
		// addr (the SMTP host) is internal infra — keep it out of the dispatch
		// error record; ctx.Err() carries no secret and stays a %w chain.
		return fmt.Errorf("email: send timed out: %w", ctx.Err())
	case err := <-done:
		if err != nil {
			// Scrub the SMTP password in case a relay error echoes the auth line,
			// then re-wrap so the error stays a %w chain per repo convention.
			return fmt.Errorf("email: send: %w", errors.New(scrubSecrets(err.Error(), cfg.SMTPPass)))
		}
		return nil
	}
}

// buildEmailMessage composes an RFC 5322 message (headers + plaintext body).
// v1 is plaintext only — HTML is deferred per the plan (Outlook CSS / MIME risk).
func buildEmailMessage(cfg model.EmailConfig, p Payload) []byte {
	var b strings.Builder
	fmt.Fprintf(&b, "From: %s\r\n", formatFromHeader(cfg))
	fmt.Fprintf(&b, "To: %s\r\n", headerValue(strings.Join(cfg.Recipients, ", ")))
	fmt.Fprintf(&b, "Subject: %s\r\n", headerValue(renderEmailSubject(p)))
	b.WriteString("MIME-Version: 1.0\r\n")
	b.WriteString("Content-Type: text/plain; charset=\"utf-8\"\r\n")
	b.WriteString("\r\n")
	b.WriteString(renderEmailBody(p))
	return []byte(b.String())
}

// defaultSenderName is the From: display name used when a channel leaves
// from_name blank — the product brand, so a fresh channel is on-brand out of the
// box without the admin having to know to type it.
const defaultSenderName = "AxiaOps"

// formatFromHeader builds the RFC 5322 From: header value, rendering
// `"AxiaOps" <noreply@example.com>` (mail.Address quotes/encodes the name as
// needed). A blank from_name falls back to defaultSenderName so the brand is
// guaranteed even if an admin clears the field. The envelope sender (MAIL FROM)
// always stays cfg.From — the display name is header-only.
func formatFromHeader(cfg model.EmailConfig) string {
	name := headerValue(cfg.FromName) // mail.Address encodes, but never hand it a newline
	if name == "" {
		name = defaultSenderName
	}
	return (&mail.Address{Name: name, Address: cfg.From}).String()
}

func renderEmailSubject(p Payload) string {
	return fmt.Sprintf("AxiaOps: %d idle resource(s), %s/mo potential savings",
		p.ZombieCount, formatMoney(p.MonthlySavings, p.Currency))
}

func renderEmailBody(p Payload) string {
	var b strings.Builder
	fmt.Fprintf(&b, "A completed scan found %d idle resource(s) with %s/mo of potential savings",
		p.ZombieCount, formatMoney(p.MonthlySavings, p.Currency))
	if p.AccountID != "" {
		fmt.Fprintf(&b, " on account %s", p.AccountID)
	}
	b.WriteString(".\r\n")
	if len(p.TopServices) > 0 {
		b.WriteString("\r\nTop services:\r\n")
		for _, s := range p.TopServices {
			fmt.Fprintf(&b, "  - %s: %d resource(s), %s/mo\r\n", s.Service, s.Count, formatMoney(s.Savings, p.Currency))
		}
	}
	if p.DashboardURL != "" {
		fmt.Fprintf(&b, "\r\nView in AxiaOps: %s\r\n", p.DashboardURL)
	}
	return b.String()
}
