package notifications_test

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"axiaops.io/shared/crypto"
	"axiaops.io/shared/model"
	"axiaops.io/shared/notifications"
)

// testEncryptionKey is a fixed 32-byte (64 hex) key for crypto round-trips.
const testEncryptionKey = "00112233445566778899aabbccddeeff00112233445566778899aabbccddeeff"

func setupCrypto(t *testing.T) {
	t.Helper()
	t.Setenv("ENCRYPTION_KEY", testEncryptionKey)
}

func encConfig(t *testing.T, v any) string {
	t.Helper()
	raw, err := json.Marshal(v)
	if err != nil {
		t.Fatalf("marshal config: %v", err)
	}
	ct, err := crypto.Encrypt(string(raw))
	if err != nil {
		t.Fatalf("encrypt config: %v", err)
	}
	return ct
}

func slackChannel(t *testing.T, webhookURL string) model.NotificationChannel {
	return model.NotificationChannel{
		ID:               "slack-1",
		Kind:             model.ChannelKindSlack,
		ConfigCiphertext: encConfig(t, model.SlackConfig{WebhookURL: webhookURL}),
	}
}

func samplePayload() notifications.Payload {
	return notifications.Payload{
		AccountID:      "acct-1",
		ZombieCount:    3,
		MonthlySavings: 100,
		Currency:       "USD",
		TopServices:    []notifications.ServiceRow{{Service: "AmazonEC2", Count: 2, Savings: 80}},
		DashboardURL:   "https://app.example.com",
	}
}

func TestSlackTransport_Success(t *testing.T) {
	setupCrypto(t)

	var gotBody map[string]string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		raw, _ := io.ReadAll(r.Body)
		_ = json.Unmarshal(raw, &gotBody)
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("ok"))
	}))
	defer srv.Close()

	tr := notifications.NewSlackTransport(srv.Client())
	ext, err := tr.Send(t.Context(), slackChannel(t, srv.URL), samplePayload())
	if err != nil {
		t.Fatalf("Send: %v", err)
	}
	if ext != "" {
		t.Errorf("slack externalID should be empty, got %q", ext)
	}
	if !strings.Contains(gotBody["text"], "AxiaOps") || !strings.Contains(gotBody["text"], "$100.00") {
		t.Errorf("posted text missing digest content: %q", gotBody["text"])
	}
}

func TestSlackTransport_Non2xx_ScrubsWebhookURL(t *testing.T) {
	setupCrypto(t)

	var webhookURL string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNotFound)
		// Echo the secret URL the way Slack's 404 page sometimes does.
		_, _ = fmt.Fprintf(w, "no_service for %s", webhookURL)
	}))
	defer srv.Close()
	webhookURL = srv.URL

	tr := notifications.NewSlackTransport(srv.Client())
	_, err := tr.Send(t.Context(), slackChannel(t, webhookURL), samplePayload())
	if err == nil {
		t.Fatal("expected error on 404")
	}
	if strings.Contains(err.Error(), webhookURL) {
		t.Errorf("error must not leak the webhook URL: %q", err)
	}
	if !strings.Contains(err.Error(), "***") || !strings.Contains(err.Error(), "404") {
		t.Errorf("error should be scrubbed and carry status: %q", err)
	}
}

func TestSlackTransport_NetworkError_ScrubsWebhookURL(t *testing.T) {
	setupCrypto(t)

	// Stand a server up, capture its URL, then close it so the POST fails at the
	// transport layer — net/http wraps the (secret) URL into the dial error.
	srv := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))
	webhookURL := srv.URL
	client := srv.Client()
	srv.Close()

	tr := notifications.NewSlackTransport(client)
	_, err := tr.Send(t.Context(), slackChannel(t, webhookURL), samplePayload())
	if err == nil {
		t.Fatal("expected a network error against the closed server")
	}
	if strings.Contains(err.Error(), webhookURL) {
		t.Errorf("network error must not leak the webhook URL: %q", err)
	}
	if !strings.Contains(err.Error(), "***") {
		t.Errorf("webhook URL should be scrubbed to ***: %q", err)
	}
}

func TestSlackTransport_EmptyWebhookURL(t *testing.T) {
	setupCrypto(t)
	tr := notifications.NewSlackTransport(nil)
	_, err := tr.Send(t.Context(), slackChannel(t, ""), samplePayload())
	if err == nil || !strings.Contains(err.Error(), "webhook_url") {
		t.Fatalf("want empty-webhook error, got %v", err)
	}
}

func TestSlackTransport_BadCiphertext(t *testing.T) {
	setupCrypto(t)
	ch := model.NotificationChannel{ID: "slack-1", Kind: model.ChannelKindSlack, ConfigCiphertext: "not-hex"}
	tr := notifications.NewSlackTransport(nil)
	_, err := tr.Send(t.Context(), ch, samplePayload())
	if err == nil || !strings.Contains(err.Error(), "decrypt") {
		t.Fatalf("want decrypt error, got %v", err)
	}
}
