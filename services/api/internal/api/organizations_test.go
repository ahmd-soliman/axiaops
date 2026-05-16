package api_test

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"axiaops.io/api/internal/api"
)

// orgHandler builds a Handler ready to serve org routes.
func orgHandler(store *MockStore) *http.ServeMux {
	h := api.New(store, noopQueue())
	mux := http.NewServeMux()
	h.Register(mux)
	return mux
}

func orgReq(method, path, body string) *http.Request {
	src := httptest.NewRequest(method, path, strings.NewReader(body))
	if body != "" {
		src.Header.Set("Content-Type", "application/json")
	}
	return src.WithContext(meRequest(method, path).Context())
}

// ── PATCH /v1/organizations/me ──────────────────────────────────────────────

func TestPatchOrganization_HappyPath_LocalOnly(t *testing.T) {
	store := NewMockStore()
	mux := orgHandler(store)

	body := `{"name":"Acme Corp"}`
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, orgReq(http.MethodPatch, "/v1/organizations/me", body))

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d (body: %s)", w.Code, w.Body.String())
	}
	var resp map[string]any
	_ = json.NewDecoder(w.Body).Decode(&resp)
	if resp["name"] != "Acme Corp" {
		t.Errorf("response name=%v, want Acme Corp", resp["name"])
	}
}

func TestPatchOrganization_NameTooLong_400(t *testing.T) {
	store := NewMockStore()
	mux := orgHandler(store)

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
	mux := orgHandler(store)

	w := httptest.NewRecorder()
	mux.ServeHTTP(w, orgReq(http.MethodPatch, "/v1/organizations/me", `{"name":""}`))

	if w.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d", w.Code)
	}
}

func TestPatchOrganization_ControlChars_400(t *testing.T) {
	store := NewMockStore()
	mux := orgHandler(store)

	body := "{\"name\":\"Acme\aCorp\"}" // U+0007 bell — must be rejected as a control char
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, orgReq(http.MethodPatch, "/v1/organizations/me", body))

	if w.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d", w.Code)
	}
}

func TestPatchOrganization_NonOwner_403(t *testing.T) {
	store := NewMockStore().WithRole("admin")
	mux := orgHandler(store)

	body := `{"name":"Acme Corp"}`
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, orgReq(http.MethodPatch, "/v1/organizations/me", body))

	if w.Code != http.StatusForbidden {
		t.Fatalf("admin should not be able to rename, got %d", w.Code)
	}
}

// ── POST /v1/organizations/me/onboarding/complete ───────────────────────────

func TestCompleteOnboarding_HappyPath(t *testing.T) {
	store := NewMockStore()
	mux := orgHandler(store)

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
	mux := orgHandler(store)

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
	mux := orgHandler(store)

	w := httptest.NewRecorder()
	mux.ServeHTTP(w, orgReq(http.MethodPost, "/v1/organizations/me/onboarding/complete", "{}"))

	if w.Code != http.StatusForbidden {
		t.Fatalf("expected 403, got %d", w.Code)
	}
}
