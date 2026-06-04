package api_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"axiaops.io/api/internal/api"
)

// fakeInviteMailer is a handler-level stand-in for the InviteMailer seam: it
// records the request and returns a canned outcome, so the handler test asserts
// only that createInvitation routes through the seam and surfaces its outcome.
// The channel-vs-global resolution logic is tested separately against the real
// mailer in invite_mailer_test.go.
type fakeInviteMailer struct {
	outcome string
	called  int
	gotReq  api.InviteMailRequest
}

func (f *fakeInviteMailer) SendInvite(_ context.Context, req api.InviteMailRequest) string {
	f.called++
	f.gotReq = req
	return f.outcome
}

func inviteMailerMux(store *MockStore, publicHost string, m api.InviteMailer) *http.ServeMux {
	h := api.New(store, noopQueue()).WithPublicHost(publicHost)
	if m != nil {
		h = h.WithInviteMailer(m)
	}
	mux := http.NewServeMux()
	h.Register(mux)
	return mux
}

func TestCreateInvitation_RoutesThroughInviteMailer(t *testing.T) {
	store := NewMockStore()
	fm := &fakeInviteMailer{outcome: "sent"}
	mux := inviteMailerMux(store, "https://app.example.com", fm)

	w := httptest.NewRecorder()
	mux.ServeHTTP(w, invReq(http.MethodPost, "/v1/invitations", `{"email":"new@example.com","role":"member"}`))
	if w.Code != http.StatusCreated {
		t.Fatalf("expected 201, got %d (body: %s)", w.Code, w.Body.String())
	}
	var resp map[string]any
	_ = json.NewDecoder(w.Body).Decode(&resp)

	if resp["email_delivery"] != "sent" {
		t.Errorf("email_delivery = %v, want sent", resp["email_delivery"])
	}
	if fm.called != 1 {
		t.Fatalf("InviteMailer called %d times, want 1", fm.called)
	}
	if fm.gotReq.Recipient != "new@example.com" {
		t.Errorf("recipient = %q, want new@example.com", fm.gotReq.Recipient)
	}
	if fm.gotReq.Role != "member" {
		t.Errorf("role = %q, want member", fm.gotReq.Role)
	}
	// The mailer gets the same absolute URL the admin sees in the response.
	redir, _ := resp["redemption_url"].(string)
	if fm.gotReq.RedemptionURL != redir {
		t.Errorf("mailer URL %q != response redemption_url %q", fm.gotReq.RedemptionURL, redir)
	}
	if !strings.HasPrefix(fm.gotReq.RedemptionURL, "https://app.example.com/accept-invite?token=") {
		t.Errorf("mailer got non-absolute URL: %q", fm.gotReq.RedemptionURL)
	}
}

// The outcome string is surfaced verbatim — a delivery failure must not change
// the 201 nor drop the redemption URL.
func TestCreateInvitation_MailerFailure_StillCreatesInvite(t *testing.T) {
	store := NewMockStore()
	fm := &fakeInviteMailer{outcome: "failed"}
	mux := inviteMailerMux(store, "https://app.example.com", fm)

	w := httptest.NewRecorder()
	mux.ServeHTTP(w, invReq(http.MethodPost, "/v1/invitations", `{"email":"new@example.com","role":"member"}`))
	if w.Code != http.StatusCreated {
		t.Fatalf("a mailer failure must not fail the invite; got %d", w.Code)
	}
	var resp map[string]any
	_ = json.NewDecoder(w.Body).Decode(&resp)
	if resp["email_delivery"] != "failed" {
		t.Errorf("email_delivery = %v, want failed", resp["email_delivery"])
	}
	if redir, _ := resp["redemption_url"].(string); redir == "" {
		t.Error("redemption_url must still be returned when delivery fails")
	}
}

// No mailer wired (e.g. a stripped composition) ⇒ no delivery attempt and the
// email_delivery field is omitted, not an empty/garbage value.
func TestCreateInvitation_NoMailer_OmitsDeliveryField(t *testing.T) {
	store := NewMockStore()
	mux := inviteMailerMux(store, "https://app.example.com", nil)

	w := httptest.NewRecorder()
	mux.ServeHTTP(w, invReq(http.MethodPost, "/v1/invitations", `{"email":"new@example.com","role":"member"}`))
	if w.Code != http.StatusCreated {
		t.Fatalf("expected 201, got %d", w.Code)
	}
	var resp map[string]any
	_ = json.NewDecoder(w.Body).Decode(&resp)
	if _, present := resp["email_delivery"]; present {
		t.Errorf("email_delivery must be omitted when no mailer is wired, got %v", resp["email_delivery"])
	}
	if redir, _ := resp["redemption_url"].(string); redir == "" {
		t.Error("redemption_url must still be returned")
	}
}
