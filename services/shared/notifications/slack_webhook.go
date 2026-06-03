package notifications

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"

	"axiaops.io/shared/crypto"
	"axiaops.io/shared/model"
)

// SlackTransport posts a digest to a Slack incoming webhook. The webhook URL is
// a bearer token: it lives encrypted in config_ciphertext, is redacted in API
// responses, and is scrubbed from any error this transport returns.
type SlackTransport struct {
	client *http.Client
}

// NewSlackTransport builds a Slack transport. A nil client falls back to a
// default with a 10s timeout (belt-and-braces alongside the dispatcher's
// per-transport context deadline, per the Send contract).
func NewSlackTransport(client *http.Client) *SlackTransport {
	if client == nil {
		client = &http.Client{Timeout: DefaultSendTimeout}
	}
	return &SlackTransport{client: client}
}

// Send implements Transport. externalID is always empty for Slack (fire-and-forget).
func (t *SlackTransport) Send(ctx context.Context, channel model.NotificationChannel, payload Payload) (string, error) {
	plaintext, err := crypto.Decrypt(channel.ConfigCiphertext)
	if err != nil {
		return "", fmt.Errorf("slack: decrypt config: %w", err)
	}
	var cfg model.SlackConfig
	if err := json.Unmarshal([]byte(plaintext), &cfg); err != nil {
		return "", fmt.Errorf("slack: decode config: %w", err)
	}
	if cfg.WebhookURL == "" {
		return "", fmt.Errorf("slack: webhook_url is empty")
	}

	body, err := json.Marshal(map[string]string{"text": renderSlackText(payload)})
	if err != nil {
		return "", fmt.Errorf("slack: encode payload: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, cfg.WebhookURL, bytes.NewReader(body))
	if err != nil {
		// url.Parse echoes the full (secret) webhook URL in its error and may
		// normalise it so a scrub could miss — and the URL is the only signal
		// here anyway. Return a fixed message that carries no URL at all.
		return "", errors.New("slack: invalid webhook URL")
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := t.client.Do(req)
	if err != nil {
		// net/http wraps the URL into the error ("Post \"https://...\": ..."), so
		// scrub it, then re-wrap so the error stays a %w chain per repo convention.
		return "", fmt.Errorf("slack: post: %w", errors.New(scrubSecrets(err.Error(), cfg.WebhookURL)))
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		// Slack's error body sometimes echoes the webhook URL — scrub it. %s is
		// correct here (not %w): the snippet is a plain string, not an error, so
		// there is no chain to preserve. Do not "fix" this to %w.
		snippet, _ := io.ReadAll(io.LimitReader(resp.Body, 512))
		return "", fmt.Errorf("slack: status %d: %s", resp.StatusCode,
			scrubSecrets(strings.TrimSpace(string(snippet)), cfg.WebhookURL))
	}
	return "", nil
}

// renderSlackText builds the plaintext digest body for a Slack message.
func renderSlackText(p Payload) string {
	var b strings.Builder
	fmt.Fprintf(&b, ":ghost: AxiaOps found %d idle resource(s) — %s/mo potential savings",
		p.ZombieCount, formatMoney(p.MonthlySavings, p.Currency))
	if p.AccountID != "" {
		fmt.Fprintf(&b, " on account `%s`", p.AccountID)
	}
	for _, s := range p.TopServices {
		fmt.Fprintf(&b, "\n• %s — %d (%s/mo)", s.Service, s.Count, formatMoney(s.Savings, p.Currency))
	}
	if p.DashboardURL != "" {
		fmt.Fprintf(&b, "\n<%s|View in AxiaOps>", p.DashboardURL)
	}
	return b.String()
}
