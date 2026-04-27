package kinde_test

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"axiaops.io/api/internal/kinde"
)

// fakeKindeServer mints any token and accepts the three Mgmt API endpoints
// we exercise. Each handler is overridable per test.
type fakeKindeServer struct {
	*httptest.Server
	tokenHandler  http.HandlerFunc
	inviteHandler http.HandlerFunc
	removeHandler http.HandlerFunc
	renameHandler http.HandlerFunc
}

func newFakeKindeServer() *fakeKindeServer {
	f := &fakeKindeServer{}
	f.tokenHandler = func(w http.ResponseWriter, _ *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{
			"access_token": "stub-token",
			"expires_in":   3600,
			"token_type":   "Bearer",
		})
	}
	mux := http.NewServeMux()
	mux.HandleFunc("/oauth2/token", func(w http.ResponseWriter, r *http.Request) { f.tokenHandler(w, r) })
	mux.HandleFunc("/api/v1/organizations/", func(w http.ResponseWriter, r *http.Request) {
		// "/api/v1/organizations/{org_code}/users[/{user_id}]"
		switch r.Method {
		case http.MethodPost:
			f.inviteHandler(w, r)
		case http.MethodDelete:
			f.removeHandler(w, r)
		default:
			http.NotFound(w, r)
		}
	})
	mux.HandleFunc("/api/v1/organization", func(w http.ResponseWriter, r *http.Request) {
		f.renameHandler(w, r)
	})
	f.Server = httptest.NewServer(mux)
	return f
}

func newClient(t *testing.T, srv *fakeKindeServer) *kinde.HTTPClient {
	t.Helper()
	c, err := kinde.New(srv.URL, srv.URL, "id", "secret")
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	return c
}

func TestInviteUser_HappyPath(t *testing.T) {
	srv := newFakeKindeServer()
	defer srv.Close()
	srv.inviteHandler = func(w http.ResponseWriter, r *http.Request) {
		var body struct {
			Users []map[string]any `json:"users"`
		}
		_ = json.NewDecoder(r.Body).Decode(&body)
		if len(body.Users) != 1 || body.Users[0]["email"] != "alice@example.com" {
			t.Errorf("unexpected body: %+v", body)
		}
		if body.Users[0]["send_invite"] != true {
			t.Errorf("send_invite must be true, got %v", body.Users[0]["send_invite"])
		}
		_ = json.NewEncoder(w).Encode(map[string]any{
			"users_added": []map[string]string{{"user_id": "u-123", "invitation_id": "i-456"}},
		})
	}
	c := newClient(t, srv)
	invID, userID, err := c.InviteUser(context.Background(), "org-x", "alice@example.com", "Alice")
	if err != nil {
		t.Fatalf("InviteUser: %v", err)
	}
	if invID != "i-456" || userID != "u-123" {
		t.Errorf("got (%q, %q), want (i-456, u-123)", invID, userID)
	}
}

func TestInviteUser_KindeError(t *testing.T) {
	srv := newFakeKindeServer()
	defer srv.Close()
	srv.inviteHandler = func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusServiceUnavailable)
		_, _ = w.Write([]byte(`{"error":"upstream"}`))
	}
	c := newClient(t, srv)
	_, _, err := c.InviteUser(context.Background(), "org-x", "alice@example.com", "Alice")
	if err == nil {
		t.Fatal("expected error")
	}
	if !kinde.IsServerError(err) {
		t.Errorf("expected server error, got %v", err)
	}
}

func TestRemoveUser_404IsIdempotent(t *testing.T) {
	srv := newFakeKindeServer()
	defer srv.Close()
	srv.removeHandler = func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusNotFound)
	}
	c := newClient(t, srv)
	if err := c.RemoveUser(context.Background(), "org-x", "u-123"); err != nil {
		t.Fatalf("RemoveUser should treat 404 as success, got %v", err)
	}
}

func TestRemoveUser_5xx(t *testing.T) {
	srv := newFakeKindeServer()
	defer srv.Close()
	srv.removeHandler = func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusBadGateway)
	}
	c := newClient(t, srv)
	err := c.RemoveUser(context.Background(), "org-x", "u-123")
	if !kinde.IsServerError(err) {
		t.Errorf("expected server error, got %v", err)
	}
}

func TestRemoveUser_EmptyIDsIsNoop(t *testing.T) {
	srv := newFakeKindeServer()
	defer srv.Close()
	srv.removeHandler = func(_ http.ResponseWriter, _ *http.Request) {
		t.Fatal("RemoveUser must not call Kinde when IDs are empty")
	}
	c := newClient(t, srv)
	if err := c.RemoveUser(context.Background(), "", ""); err != nil {
		t.Fatalf("expected no error for empty IDs, got %v", err)
	}
}

func TestRenameOrganization_HappyPath(t *testing.T) {
	srv := newFakeKindeServer()
	defer srv.Close()
	var receivedName, receivedCode string
	srv.renameHandler = func(w http.ResponseWriter, r *http.Request) {
		receivedCode = r.URL.Query().Get("code")
		var body struct {
			Name string `json:"name"`
		}
		_ = json.NewDecoder(r.Body).Decode(&body)
		receivedName = body.Name
		w.WriteHeader(http.StatusOK)
	}
	c := newClient(t, srv)
	if err := c.RenameOrganization(context.Background(), "org-x", "Acme Corp"); err != nil {
		t.Fatalf("RenameOrganization: %v", err)
	}
	if receivedCode != "org-x" || receivedName != "Acme Corp" {
		t.Errorf("got (%q, %q), want (org-x, Acme Corp)", receivedCode, receivedName)
	}
}

func TestRenameOrganization_4xxClassification(t *testing.T) {
	srv := newFakeKindeServer()
	defer srv.Close()
	srv.renameHandler = func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusBadRequest)
		_, _ = w.Write([]byte(`{"error":"name too long"}`))
	}
	c := newClient(t, srv)
	err := c.RenameOrganization(context.Background(), "org-x", strings.Repeat("x", 200))
	if err == nil {
		t.Fatal("expected error")
	}
	if !kinde.IsClientError(err) {
		t.Errorf("expected client error, got %v (server=%v)", err, kinde.IsServerError(err))
	}
}

func TestNew_RejectsEmptyCredentials(t *testing.T) {
	if _, err := kinde.New("https://example.kinde.com", "", "", "secret"); err == nil {
		t.Error("expected error for empty client_id")
	}
	if _, err := kinde.New("", "", "id", "secret"); err == nil {
		t.Error("expected error for empty issuer")
	}
}

func TestStub_InviteAndRename(t *testing.T) {
	stub := kinde.NewStub()
	invID, userID, err := stub.InviteUser(context.Background(), "org-x", "Alice@Example.com", "Alice")
	if err != nil {
		t.Fatalf("InviteUser: %v", err)
	}
	if invID == "" || userID == "" {
		t.Errorf("stub should return non-empty IDs")
	}
	if err := stub.RenameOrganization(context.Background(), "org-x", "Acme Corp"); err != nil {
		t.Fatalf("RenameOrganization: %v", err)
	}
	if got := stub.OrgName("org-x"); got != "Acme Corp" {
		t.Errorf("OrgName=%q, want Acme Corp", got)
	}
}

func TestStub_FailNextInvite(t *testing.T) {
	stub := kinde.NewStub()
	stub.FailNextInvite(errors.New("boom"))
	if _, _, err := stub.InviteUser(context.Background(), "org-x", "alice@example.com", ""); err == nil {
		t.Error("expected error")
	}
	if _, _, err := stub.InviteUser(context.Background(), "org-x", "alice@example.com", ""); err != nil {
		t.Errorf("second call should succeed, got %v", err)
	}
}
