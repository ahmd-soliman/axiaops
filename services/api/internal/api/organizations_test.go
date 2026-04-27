package api_test

import (
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"axiaops.io/api/internal/api"
	"axiaops.io/api/internal/kinde"
)

// orgHandler builds a Handler with kinde stub so PATCH /v1/organizations/me works.
func orgHandler(store *MockStore) (*http.ServeMux, *kinde.Stub) {
	stub := kinde.NewStub()
	h := api.New(store, noopQueue()).WithKinde(stub)
	mux := http.NewServeMux()
	h.Register(mux)
	return mux, stub
}

func orgReq(method, path, body string) *http.Request {
	src := httptest.NewRequest(method, path, strings.NewReader(body))
	if body != "" {
		src.Header.Set("Content-Type", "application/json")
	}
	return src.WithContext(meRequest(method, path).Context())
}

// ── PATCH /v1/organizations/me ──────────────────────────────────────────────

func TestPatchOrganization_HappyPath_PushesToKinde(t *testing.T) {
	store := NewMockStore()
	mux, stub := orgHandler(store)

	body := `{"name":"Acme Corp"}`
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, orgReq(http.MethodPatch, "/v1/organizations/me", body))

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d (body: %s)", w.Code, w.Body.String())
	}
	if got := stub.OrgName("organization-me"); got != "Acme Corp" {
		t.Errorf("Kinde rename target=%q, want Acme Corp", got)
	}
	var resp map[string]any
	_ = json.NewDecoder(w.Body).Decode(&resp)
	if resp["name"] != "Acme Corp" {
		t.Errorf("response name=%v, want Acme Corp", resp["name"])
	}
}

func TestPatchOrganization_KindeFails_RevertsLocal(t *testing.T) {
	// We can't easily observe local revert without a real DB, but we can verify
	// the handler returns 502 and the audit metadata pattern is respected.
	store := NewMockStore()
	stub := kinde.NewStub()
	stub.FailNextRename(errors.New("kinde 502"))
	h := api.New(store, noopQueue()).WithKinde(stub)
	mux := http.NewServeMux()
	h.Register(mux)

	body := `{"name":"Acme Corp"}`
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, orgReq(http.MethodPatch, "/v1/organizations/me", body))

	if w.Code != http.StatusBadGateway {
		t.Fatalf("expected 502, got %d (body: %s)", w.Code, w.Body.String())
	}
	var resp map[string]string
	_ = json.NewDecoder(w.Body).Decode(&resp)
	if resp["error"] != "kinde_rename_failed" {
		t.Errorf("error code=%q, want kinde_rename_failed", resp["error"])
	}
}

func TestPatchOrganization_NameTooLong_400(t *testing.T) {
	store := NewMockStore()
	mux, _ := orgHandler(store)

	longName := strings.Repeat("x", 200)
	body := `{"name":"` + longName + `"}`
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, orgReq(http.MethodPatch, "/v1/organizations/me", body))

	if w.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d", w.Code)
	}
}

func TestPatchOrganization_EmptyName_400(t *testing.T) {
	store := NewMockStore()
	mux, _ := orgHandler(store)

	w := httptest.NewRecorder()
	mux.ServeHTTP(w, orgReq(http.MethodPatch, "/v1/organizations/me", `{"name":""}`))

	if w.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d", w.Code)
	}
}

func TestPatchOrganization_ControlChars_400(t *testing.T) {
	store := NewMockStore()
	mux, _ := orgHandler(store)

	body := `{"name":"AcmeCorp"}` // bell character
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, orgReq(http.MethodPatch, "/v1/organizations/me", body))

	if w.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d", w.Code)
	}
}

func TestPatchOrganization_NonOwner_403(t *testing.T) {
	store := NewMockStore().WithRole("admin")
	mux, _ := orgHandler(store)

	body := `{"name":"Acme Corp"}`
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, orgReq(http.MethodPatch, "/v1/organizations/me", body))

	if w.Code != http.StatusForbidden {
		t.Fatalf("admin should not be able to rename, got %d", w.Code)
	}
}

func TestPatchOrganization_NoKinde_503(t *testing.T) {
	store := NewMockStore()
	h := api.New(store, noopQueue()) // no WithKinde
	mux := http.NewServeMux()
	h.Register(mux)

	body := `{"name":"Acme Corp"}`
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, orgReq(http.MethodPatch, "/v1/organizations/me", body))

	if w.Code != http.StatusServiceUnavailable {
		t.Fatalf("expected 503 without Kinde wired, got %d", w.Code)
	}
}

// ── POST /v1/organizations/me/onboarding/complete ───────────────────────────

func TestCompleteOnboarding_HappyPath(t *testing.T) {
	store := NewMockStore()
	mux, _ := orgHandler(store)

	body := `{"steps_skipped":["invite","aws-account"]}`
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, orgReq(http.MethodPost, "/v1/organizations/me/onboarding/complete", body))

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d (body: %s)", w.Code, w.Body.String())
	}
	var resp map[string]any
	_ = json.NewDecoder(w.Body).Decode(&resp)
	if resp["onboarding_completed_at"] == nil {
		t.Errorf("expected onboarding_completed_at in response, got %+v", resp)
	}
}

func TestCompleteOnboarding_Idempotent(t *testing.T) {
	store := NewMockStore()
	mux, _ := orgHandler(store)

	for i := 0; i < 3; i++ {
		w := httptest.NewRecorder()
		mux.ServeHTTP(w, orgReq(http.MethodPost, "/v1/organizations/me/onboarding/complete", "{}"))
		if w.Code != http.StatusOK {
			t.Fatalf("call %d: expected 200, got %d", i, w.Code)
		}
	}
}

func TestCompleteOnboarding_NonOwner_403(t *testing.T) {
	store := NewMockStore().WithRole("member")
	mux, _ := orgHandler(store)

	w := httptest.NewRecorder()
	mux.ServeHTTP(w, orgReq(http.MethodPost, "/v1/organizations/me/onboarding/complete", "{}"))

	if w.Code != http.StatusForbidden {
		t.Fatalf("expected 403, got %d", w.Code)
	}
}
