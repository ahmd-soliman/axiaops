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

// TeamsTransport posts a digest to a Teams Workflows webhook using an Adaptive Card.
type TeamsTransport struct {
	client *http.Client
}

// NewTeamsTransport builds a Teams transport.
func NewTeamsTransport(client *http.Client) *TeamsTransport {
	if client == nil {
		client = &http.Client{Timeout: DefaultSendTimeout}
	}
	return &TeamsTransport{client: client}
}

// Send implements Transport. externalID is always empty for Teams (fire-and-forget).
func (t *TeamsTransport) Send(ctx context.Context, channel model.NotificationChannel, payload Payload) (string, error) {
	plaintext, err := crypto.Decrypt(channel.ConfigCiphertext)
	if err != nil {
		return "", fmt.Errorf("teams: decrypt config: %w", err)
	}
	var cfg model.TeamsConfig
	if err := json.Unmarshal([]byte(plaintext), &cfg); err != nil {
		return "", fmt.Errorf("teams: decode config: %w", err)
	}
	if cfg.WebhookURL == "" {
		return "", fmt.Errorf("teams: webhook_url is empty")
	}

	card := map[string]any{
		"$schema": "http://adaptivecards.io/schemas/adaptive-card.json",
		"type":    "AdaptiveCard",
		"version": "1.4", // safe floor for Teams; do not chase newer without a reason
		"body": []map[string]any{
			{
				"type":   "TextBlock",
				"size":   "Medium",
				"weight": "Bolder",
				"wrap":   true,
				"text":   fmt.Sprintf("AxiaOps — %d idle resource(s)", payload.ZombieCount),
			},
			{
				"type": "TextBlock",
				"wrap": true, // without this the digest is truncated to one line
				"text": renderTeamsText(payload),
			},
		},
	}
	env := map[string]any{
		"type": "message",
		"attachments": []map[string]any{{
			"contentType": "application/vnd.microsoft.card.adaptive",
			"contentUrl":  nil,
			"content":     card,
		}},
	}
	body, err := json.Marshal(env)
	if err != nil {
		return "", fmt.Errorf("teams: encode payload: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, cfg.WebhookURL, bytes.NewReader(body))
	if err != nil {
		// url.Parse echoes the full (secret) webhook URL in its error and may
		// normalise it so a scrub could miss — and the URL is the only signal
		// here anyway. Return a fixed message that carries no URL at all.
		return "", errors.New("teams: invalid webhook URL")
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := t.client.Do(req)
	if err != nil {
		// net/http wraps the URL into the error ("Post \"https://...\": ..."), so
		// scrub it, then re-wrap so the error stays a %w chain per repo convention.
		return "", fmt.Errorf("teams: post: %w", errors.New(scrubSecrets(err.Error(), cfg.WebhookURL)))
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		// Teams' error body can echo the webhook URL — scrub it. %s is
		// correct here (not %w): the snippet is a plain string, not an error, so
		// there is no chain to preserve. Do not "fix" this to %w.
		snippet, _ := io.ReadAll(io.LimitReader(resp.Body, 512))
		return "", fmt.Errorf("teams: status %d: %s", resp.StatusCode,
			scrubSecrets(strings.TrimSpace(string(snippet)), cfg.WebhookURL))
	}
	return "", nil
}

// renderTeamsText builds the plaintext digest body for a Teams message.
func renderTeamsText(p Payload) string {
	var b strings.Builder
	fmt.Fprintf(&b, "👻 AxiaOps found %d idle resource(s) — %s/mo potential savings",
		p.ZombieCount, formatMoney(p.MonthlySavings, p.Currency))
	if p.AccountID != "" {
		fmt.Fprintf(&b, " on account **%s**", p.AccountID)
	}
	for _, s := range p.TopServices {
		fmt.Fprintf(&b, "\n\n- %s — %d (%s/mo)", s.Service, s.Count, formatMoney(s.Savings, p.Currency))
	}
	if p.DashboardURL != "" {
		fmt.Fprintf(&b, "\n\n[View in AxiaOps](%s)", p.DashboardURL)
	}
	return b.String()
}
