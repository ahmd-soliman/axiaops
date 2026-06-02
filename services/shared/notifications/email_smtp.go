package notifications

import (
	"context"
	"crypto/tls"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net"
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
	plaintext, err := crypto.Decrypt(channel.ConfigCiphertext)
	if err != nil {
		return "", fmt.Errorf("email: decrypt config: %w", err)
	}
	var cfg model.EmailConfig
	if err := json.Unmarshal([]byte(plaintext), &cfg); err != nil {
		return "", fmt.Errorf("email: decode config: %w", err)
	}
	if cfg.SMTPHost == "" || cfg.SMTPPort == 0 {
		return "", fmt.Errorf("email: smtp_host and smtp_port are required")
	}
	if cfg.From == "" {
		return "", fmt.Errorf("email: from is required")
	}
	if len(cfg.Recipients) == 0 {
		return "", fmt.Errorf("email: at least one recipient is required")
	}

	msg := buildEmailMessage(cfg, payload)
	addr := net.JoinHostPort(cfg.SMTPHost, strconv.Itoa(cfg.SMTPPort))

	var auth smtp.Auth
	if cfg.SMTPUser != "" {
		auth = smtp.PlainAuth("", cfg.SMTPUser, cfg.SMTPPass, cfg.SMTPHost)
	}

	// net/smtp.SendMail is blocking and ctx-unaware. Run it in a goroutine and
	// race it against the dispatcher's deadline so a wedged relay can't stall the
	// scan past the per-transport timeout. `done` is buffered so the goroutine
	// can always send and exit even after we've returned on the ctx path — it
	// outlives this call but never blocks (acceptable for v1: single attempt, no
	// retry — see docs/notifications-plan.md "Risks + deferred → Retry / DLQ").
	done := make(chan error, 1)
	go func() {
		done <- t.sendMail(addr, auth, cfg.From, cfg.Recipients, msg)
	}()

	select {
	case <-ctx.Done():
		// addr (the SMTP host) is internal infra — keep it out of the dispatch
		// error record; ctx.Err() carries no secret and stays a %w chain.
		return "", fmt.Errorf("email: send timed out: %w", ctx.Err())
	case err := <-done:
		if err != nil {
			// Scrub the SMTP password in case a relay error echoes the auth line,
			// then re-wrap so the error stays a %w chain per repo convention.
			return "", fmt.Errorf("email: send: %w", errors.New(scrubSecrets(err.Error(), cfg.SMTPPass)))
		}
		return "", nil
	}
}

// buildEmailMessage composes an RFC 5322 message (headers + plaintext body).
// v1 is plaintext only — HTML is deferred per the plan (Outlook CSS / MIME risk).
func buildEmailMessage(cfg model.EmailConfig, p Payload) []byte {
	var b strings.Builder
	fmt.Fprintf(&b, "From: %s\r\n", cfg.From)
	fmt.Fprintf(&b, "To: %s\r\n", strings.Join(cfg.Recipients, ", "))
	fmt.Fprintf(&b, "Subject: %s\r\n", renderEmailSubject(p))
	b.WriteString("MIME-Version: 1.0\r\n")
	b.WriteString("Content-Type: text/plain; charset=\"utf-8\"\r\n")
	b.WriteString("\r\n")
	b.WriteString(renderEmailBody(p))
	return []byte(b.String())
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
