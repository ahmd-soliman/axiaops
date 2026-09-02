package notifications_test

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"axiaops.io/shared/model"
	"axiaops.io/shared/notifications"
)

func teamsChannel(t *testing.T, webhookURL string) model.NotificationChannel {
	return model.NotificationChannel{
		ID:               "teams-1",
		Kind:             model.ChannelKindTeams,
		ConfigCiphertext: encConfig(t, model.TeamsConfig{WebhookURL: webhookURL}),
	}
}

func TestTeamsTransport_Success(t *testing.T) {
	setupCrypto(t)

	var gotBody map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		raw, _ := io.ReadAll(r.Body)
		_ = json.Unmarshal(raw, &gotBody)
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("ok"))
	}))
	defer srv.Close()

	tr := notifications.NewTeamsTransport(srv.Client())
	ext, err := tr.Send(t.Context(), teamsChannel(t, srv.URL), samplePayload())
	if err != nil {
		t.Fatalf("Send: %v", err)
	}
	if ext != "" {
		t.Errorf("teams externalID should be empty, got %q", ext)
	}

	// Assert the Adaptive Card envelope
	if gotBody["type"] != "message" {
		t.Errorf("expected type 'message', got %v", gotBody["type"])
	}
	attachments, ok := gotBody["attachments"].([]any)
	if !ok || len(attachments) != 1 {
		t.Fatalf("expected 1 attachment, got %v", gotBody["attachments"])
	}
	attachment := attachments[0].(map[string]any)
	if attachment["contentType"] != "application/vnd.microsoft.card.adaptive" {
		t.Errorf("unexpected contentType: %v", attachment["contentType"])
	}
	card, ok := attachment["content"].(map[string]any)
	if !ok || card["type"] != "AdaptiveCard" {
		t.Fatalf("expected AdaptiveCard content, got %v", attachment["content"])
	}

	// Assert wrap: true on the text blocks
	body, ok := card["body"].([]any)
	if !ok || len(body) != 2 {
		t.Fatalf("expected 2 body items, got %v", card["body"])
	}
	for i, item := range body {
		block := item.(map[string]any)
		if block["wrap"] != true {
			t.Errorf("expected wrap: true on TextBlock %d", i)
		}
	}
}

func TestTeamsTransport_Non2xx_ScrubsWebhookURL(t *testing.T) {
	setupCrypto(t)

	var webhookURL string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNotFound)
		_, _ = fmt.Fprintf(w, "no_service for %s", webhookURL)
	}))
	defer srv.Close()
	webhookURL = srv.URL

	tr := notifications.NewTeamsTransport(srv.Client())
	_, err := tr.Send(t.Context(), teamsChannel(t, webhookURL), samplePayload())
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

func TestTeamsTransport_NetworkError_ScrubsWebhookURL(t *testing.T) {
	setupCrypto(t)

	srv := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))
	webhookURL := srv.URL
	client := srv.Client()
	srv.Close()

	tr := notifications.NewTeamsTransport(client)
	_, err := tr.Send(t.Context(), teamsChannel(t, webhookURL), samplePayload())
	if err == nil {
		t.Fatal("expected a network error")
	}
	if strings.Contains(err.Error(), webhookURL) {
		t.Errorf("network error must not leak the webhook URL: %q", err)
	}
	if !strings.Contains(err.Error(), "***") {
		t.Errorf("webhook URL should be scrubbed to ***: %q", err)
	}
}

func TestTeamsTransport_EmptyWebhookURL(t *testing.T) {
	setupCrypto(t)
	tr := notifications.NewTeamsTransport(nil)
	_, err := tr.Send(t.Context(), teamsChannel(t, ""), samplePayload())
	if err == nil || !strings.Contains(err.Error(), "webhook_url") {
		t.Fatalf("want empty-webhook error, got %v", err)
	}
}

func TestTeamsTransport_BadCiphertext(t *testing.T) {
	setupCrypto(t)
	ch := model.NotificationChannel{ID: "teams-1", Kind: model.ChannelKindTeams, ConfigCiphertext: "not-hex"}
	tr := notifications.NewTeamsTransport(nil)
	_, err := tr.Send(t.Context(), ch, samplePayload())
	if err == nil || !strings.Contains(err.Error(), "decrypt") {
		t.Fatalf("want decrypt error, got %v", err)
	}
}
