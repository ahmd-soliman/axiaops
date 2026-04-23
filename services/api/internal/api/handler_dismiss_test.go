package api_test

import (
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"axiaops.io/api/internal/api"
	"axiaops.io/shared/model"
	"axiaops.io/shared/storage"
)

// ── POST /v1/dismissals ───────────────────────────────────────────────────────

func TestCreateDismissal_Returns201(t *testing.T) {
	store := NewMockStore().WithZombies([]model.ZombieResource{testZombie})
	h := newHandlerWith(store)
	mux := newMux(h)

	body := `{
		"account_id":"acc-1","provider":"aws","service":"AmazonRDS",
		"region":"eu-central-1","resource_id":"db-stag-01",
		"action":"dismiss","reason":"false_positive"
	}`
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, tenantRequestWithBody(http.MethodPost, "/v1/dismissals", body))

	if w.Code != http.StatusCreated {
		t.Fatalf("expected 201, got %d — body: %s", w.Code, w.Body.String())
	}

	var d model.DismissAction
	if err := json.NewDecoder(w.Body).Decode(&d); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if d.ID == 0 {
		t.Error("expected non-zero dismissal ID in response")
	}
	if d.Action != "dismiss" {
		t.Errorf("expected action=dismiss, got %s", d.Action)
	}
}

func TestCreateDismissal_Snooze_Returns201(t *testing.T) {
	store := NewMockStore()
	h := newHandlerWith(store)
	mux := newMux(h)

	snoozeUntil := time.Now().Add(48 * time.Hour).UTC().Format(time.RFC3339)
	body := `{
		"account_id":"acc-1","provider":"aws","service":"AmazonEC2",
		"region":"us-east-1","resource_id":"i-00000001",
		"action":"snooze","reason":"scheduled_deletion",
		"snooze_until":"` + snoozeUntil + `"
	}`
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, tenantRequestWithBody(http.MethodPost, "/v1/dismissals", body))

	if w.Code != http.StatusCreated {
		t.Fatalf("expected 201, got %d — body: %s", w.Code, w.Body.String())
	}
}

func TestCreateDismissal_MissingAccountID_Returns400(t *testing.T) {
	_, mux := testHandler()
	body := `{"provider":"aws","service":"AmazonRDS","region":"eu-central-1","resource_id":"db-1","action":"dismiss","reason":"intentional"}`
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, tenantRequestWithBody(http.MethodPost, "/v1/dismissals", body))
	if w.Code != http.StatusBadRequest {
		t.Errorf("expected 400, got %d", w.Code)
	}
}

func TestCreateDismissal_InvalidAction_Returns400(t *testing.T) {
	_, mux := testHandler()
	body := `{"account_id":"acc-1","provider":"aws","service":"AmazonRDS","region":"eu-central-1","resource_id":"db-1","action":"delete","reason":"intentional"}`
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, tenantRequestWithBody(http.MethodPost, "/v1/dismissals", body))
	if w.Code != http.StatusBadRequest {
		t.Errorf("expected 400, got %d", w.Code)
	}
}

func TestCreateDismissal_InvalidReason_Returns400(t *testing.T) {
	_, mux := testHandler()
	body := `{"account_id":"acc-1","provider":"aws","service":"AmazonRDS","region":"eu-central-1","resource_id":"db-1","action":"dismiss","reason":"dunno"}`
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, tenantRequestWithBody(http.MethodPost, "/v1/dismissals", body))
	if w.Code != http.StatusBadRequest {
		t.Errorf("expected 400, got %d", w.Code)
	}
}

func TestCreateDismissal_OtherReasonWithoutNote_Returns400(t *testing.T) {
	_, mux := testHandler()
	body := `{"account_id":"acc-1","provider":"aws","service":"AmazonRDS","region":"eu-central-1","resource_id":"db-1","action":"dismiss","reason":"other"}`
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, tenantRequestWithBody(http.MethodPost, "/v1/dismissals", body))
	if w.Code != http.StatusBadRequest {
		t.Errorf("expected 400 when reason=other and note missing, got %d", w.Code)
	}
	if !strings.Contains(w.Body.String(), "note is required") {
		t.Errorf("expected note-required message, got: %s", w.Body.String())
	}
}

func TestCreateDismissal_OtherReasonWithNote_Returns201(t *testing.T) {
	_, mux := testHandler()
	body := `{"account_id":"acc-1","provider":"aws","service":"AmazonRDS","region":"eu-central-1","resource_id":"db-1","action":"dismiss","reason":"other","note":"keeping for audit"}`
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, tenantRequestWithBody(http.MethodPost, "/v1/dismissals", body))
	if w.Code != http.StatusCreated {
		t.Errorf("expected 201, got %d — body: %s", w.Code, w.Body.String())
	}
}

func TestCreateDismissal_SnoozeMissingSnoozedUntil_Returns400(t *testing.T) {
	_, mux := testHandler()
	body := `{"account_id":"acc-1","provider":"aws","service":"AmazonEC2","region":"us-east-1","resource_id":"i-1","action":"snooze","reason":"scheduled_deletion"}`
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, tenantRequestWithBody(http.MethodPost, "/v1/dismissals", body))
	if w.Code != http.StatusBadRequest {
		t.Errorf("expected 400 when snooze_until missing, got %d", w.Code)
	}
}

func TestCreateDismissal_SnoozeInPast_Returns400(t *testing.T) {
	_, mux := testHandler()
	past := time.Now().Add(-1 * time.Hour).UTC().Format(time.RFC3339)
	body := `{"account_id":"acc-1","provider":"aws","service":"AmazonEC2","region":"us-east-1","resource_id":"i-1","action":"snooze","reason":"scheduled_deletion","snooze_until":"` + past + `"}`
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, tenantRequestWithBody(http.MethodPost, "/v1/dismissals", body))
	if w.Code != http.StatusBadRequest {
		t.Errorf("expected 400 for past snooze_until, got %d", w.Code)
	}
}

func TestCreateDismissal_SnoozeTooFar_Returns400(t *testing.T) {
	_, mux := testHandler()
	tooFar := time.Now().Add(100 * 24 * time.Hour).UTC().Format(time.RFC3339)
	body := `{"account_id":"acc-1","provider":"aws","service":"AmazonEC2","region":"us-east-1","resource_id":"i-1","action":"snooze","reason":"scheduled_deletion","snooze_until":"` + tooFar + `"}`
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, tenantRequestWithBody(http.MethodPost, "/v1/dismissals", body))
	if w.Code != http.StatusBadRequest {
		t.Errorf("expected 400 for snooze >90 days, got %d", w.Code)
	}
}

func TestCreateDismissal_AlreadyDismissed_Returns409(t *testing.T) {
	store := NewMockStore().WithDismissZombieError(storage.ErrAlreadyDismissed)
	h := newHandlerWith(store)
	mux := newMux(h)

	body := `{"account_id":"acc-1","provider":"aws","service":"AmazonRDS","region":"eu-central-1","resource_id":"db-1","action":"dismiss","reason":"intentional"}`
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, tenantRequestWithBody(http.MethodPost, "/v1/dismissals", body))
	if w.Code != http.StatusConflict {
		t.Errorf("expected 409, got %d", w.Code)
	}
}

// ── DELETE /v1/dismissals/{id} ────────────────────────────────────────────────

func TestRevokeDismissal_Returns204(t *testing.T) {
	store := NewMockStore().WithDismissals([]model.DismissAction{
		{ID: 1, AccountID: "acc-1", Action: "dismiss", Reason: "intentional"},
	})
	h := newHandlerWith(store)
	mux := newMux(h)

	w := httptest.NewRecorder()
	mux.ServeHTTP(w, tenantRequest(http.MethodDelete, "/v1/dismissals/1"))
	if w.Code != http.StatusNoContent {
		t.Errorf("expected 204, got %d — body: %s", w.Code, w.Body.String())
	}
}

func TestRevokeDismissal_NotFound_Returns404(t *testing.T) {
	_, mux := testHandler()
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, tenantRequest(http.MethodDelete, "/v1/dismissals/999"))
	if w.Code != http.StatusNotFound {
		t.Errorf("expected 404, got %d", w.Code)
	}
}

func TestRevokeDismissal_InvalidID_Returns400(t *testing.T) {
	_, mux := testHandler()
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, tenantRequest(http.MethodDelete, "/v1/dismissals/not-a-number"))
	if w.Code != http.StatusBadRequest {
		t.Errorf("expected 400 for non-numeric id, got %d", w.Code)
	}
}

// ── GET /v1/dismissals ────────────────────────────────────────────────────────

func TestListDismissals_Returns200(t *testing.T) {
	_, mux := testHandler()
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, tenantRequest(http.MethodGet, "/v1/dismissals"))
	if w.Code != http.StatusOK {
		t.Errorf("expected 200, got %d", w.Code)
	}
}

func TestListDismissals_EmptyList(t *testing.T) {
	_, mux := testHandler()
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, tenantRequest(http.MethodGet, "/v1/dismissals"))

	var dismissals []model.DismissAction
	if err := json.NewDecoder(w.Body).Decode(&dismissals); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(dismissals) != 0 {
		t.Errorf("expected 0 dismissals, got %d", len(dismissals))
	}
}

func TestListDismissals_ReturnsDismissals(t *testing.T) {
	store := NewMockStore().WithDismissals([]model.DismissAction{
		{ID: 1, AccountID: "acc-1", Action: "dismiss", Reason: "intentional"},
		{ID: 2, AccountID: "acc-1", Action: "snooze", Reason: "scheduled_deletion"},
	})
	h := newHandlerWith(store)
	mux := newMux(h)

	w := httptest.NewRecorder()
	mux.ServeHTTP(w, tenantRequest(http.MethodGet, "/v1/dismissals"))

	var dismissals []model.DismissAction
	if err := json.NewDecoder(w.Body).Decode(&dismissals); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(dismissals) != 2 {
		t.Fatalf("expected 2 dismissals, got %d", len(dismissals))
	}
}

func TestListDismissals_StoreError_Returns500(t *testing.T) {
	store := NewMockStore().WithListActiveDismissalsError(errors.New("db down"))
	h := newHandlerWith(store)
	mux := newMux(h)

	w := httptest.NewRecorder()
	mux.ServeHTTP(w, tenantRequest(http.MethodGet, "/v1/dismissals"))
	if w.Code != http.StatusInternalServerError {
		t.Errorf("expected 500, got %d", w.Code)
	}
}

// ── GET /v1/zombies with dismissal filtering ──────────────────────────────────

func TestListZombies_ExcludesDismissedByDefault(t *testing.T) {
	zombie := testZombie
	store := NewMockStore().
		WithZombies([]model.ZombieResource{zombie}).
		WithDismissals([]model.DismissAction{
			{
				ID:         1,
				AccountID:  zombie.InternalAccountID,
				Provider:   zombie.Provider,
				Service:    zombie.Service,
				Region:     zombie.Region,
				ResourceID: zombie.ResourceID,
				Action:     "dismiss",
				Reason:     "intentional",
			},
		})
	h := newHandlerWith(store)
	mux := newMux(h)

	w := httptest.NewRecorder()
	mux.ServeHTTP(w, tenantRequest(http.MethodGet, "/v1/zombies"))

	var zombies []model.ZombieResource
	if err := json.NewDecoder(w.Body).Decode(&zombies); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(zombies) != 0 {
		t.Errorf("expected dismissed zombie to be filtered out, got %d zombies", len(zombies))
	}
}

func TestListZombies_IncludeDismissedQueryParam(t *testing.T) {
	zombie := testZombie
	store := NewMockStore().
		WithZombies([]model.ZombieResource{zombie}).
		WithDismissals([]model.DismissAction{
			{
				ID:         1,
				AccountID:  zombie.InternalAccountID,
				Provider:   zombie.Provider,
				Service:    zombie.Service,
				Region:     zombie.Region,
				ResourceID: zombie.ResourceID,
				Action:     "dismiss",
				Reason:     "intentional",
			},
		})
	h := newHandlerWith(store)
	mux := newMux(h)

	w := httptest.NewRecorder()
	mux.ServeHTTP(w, tenantRequest(http.MethodGet, "/v1/zombies?include_dismissed=true"))

	var zombies []model.ZombieResource
	if err := json.NewDecoder(w.Body).Decode(&zombies); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(zombies) != 1 {
		t.Fatalf("expected 1 zombie with include_dismissed=true, got %d", len(zombies))
	}
	if zombies[0].DismissAction != "dismiss" {
		t.Errorf("expected dismiss_action=dismiss annotation, got %q", zombies[0].DismissAction)
	}
	if zombies[0].DismissalID == nil || *zombies[0].DismissalID != 1 {
		t.Errorf("expected dismissal_id=1, got %v", zombies[0].DismissalID)
	}
}

// ── helpers ───────────────────────────────────────────────────────────────────

func newHandlerWith(store *MockStore) *api.Handler {
	return api.New(store, noopQueue())
}

func newMux(h *api.Handler) *http.ServeMux {
	mux := http.NewServeMux()
	h.Register(mux)
	return mux
}
