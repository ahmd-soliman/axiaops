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
	"axiaops.io/api/internal/middleware"
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
	mux.ServeHTTP(w, orgRequestWithBody(http.MethodPost, "/v1/dismissals", body))

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
	mux.ServeHTTP(w, orgRequestWithBody(http.MethodPost, "/v1/dismissals", body))

	if w.Code != http.StatusCreated {
		t.Fatalf("expected 201, got %d — body: %s", w.Code, w.Body.String())
	}
}

func TestCreateDismissal_MissingAccountID_Returns400(t *testing.T) {
	_, mux := testHandler()
	body := `{"provider":"aws","service":"AmazonRDS","region":"eu-central-1","resource_id":"db-1","action":"dismiss","reason":"intentional"}`
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, orgRequestWithBody(http.MethodPost, "/v1/dismissals", body))
	if w.Code != http.StatusBadRequest {
		t.Errorf("expected 400, got %d", w.Code)
	}
}

func TestCreateDismissal_InvalidAction_Returns400(t *testing.T) {
	_, mux := testHandler()
	body := `{"account_id":"acc-1","provider":"aws","service":"AmazonRDS","region":"eu-central-1","resource_id":"db-1","action":"delete","reason":"intentional"}`
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, orgRequestWithBody(http.MethodPost, "/v1/dismissals", body))
	if w.Code != http.StatusBadRequest {
		t.Errorf("expected 400, got %d", w.Code)
	}
}

func TestCreateDismissal_InvalidReason_Returns400(t *testing.T) {
	_, mux := testHandler()
	body := `{"account_id":"acc-1","provider":"aws","service":"AmazonRDS","region":"eu-central-1","resource_id":"db-1","action":"dismiss","reason":"dunno"}`
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, orgRequestWithBody(http.MethodPost, "/v1/dismissals", body))
	if w.Code != http.StatusBadRequest {
		t.Errorf("expected 400, got %d", w.Code)
	}
}

func TestCreateDismissal_OtherReasonWithoutNote_Returns400(t *testing.T) {
	_, mux := testHandler()
	body := `{"account_id":"acc-1","provider":"aws","service":"AmazonRDS","region":"eu-central-1","resource_id":"db-1","action":"dismiss","reason":"other"}`
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, orgRequestWithBody(http.MethodPost, "/v1/dismissals", body))
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
	mux.ServeHTTP(w, orgRequestWithBody(http.MethodPost, "/v1/dismissals", body))
	if w.Code != http.StatusCreated {
		t.Errorf("expected 201, got %d — body: %s", w.Code, w.Body.String())
	}
}

func TestCreateDismissal_SnoozeMissingSnoozedUntil_Returns400(t *testing.T) {
	_, mux := testHandler()
	body := `{"account_id":"acc-1","provider":"aws","service":"AmazonEC2","region":"us-east-1","resource_id":"i-1","action":"snooze","reason":"scheduled_deletion"}`
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, orgRequestWithBody(http.MethodPost, "/v1/dismissals", body))
	if w.Code != http.StatusBadRequest {
		t.Errorf("expected 400 when snooze_until missing, got %d", w.Code)
	}
}

func TestCreateDismissal_SnoozeInPast_Returns400(t *testing.T) {
	_, mux := testHandler()
	past := time.Now().Add(-1 * time.Hour).UTC().Format(time.RFC3339)
	body := `{"account_id":"acc-1","provider":"aws","service":"AmazonEC2","region":"us-east-1","resource_id":"i-1","action":"snooze","reason":"scheduled_deletion","snooze_until":"` + past + `"}`
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, orgRequestWithBody(http.MethodPost, "/v1/dismissals", body))
	if w.Code != http.StatusBadRequest {
		t.Errorf("expected 400 for past snooze_until, got %d", w.Code)
	}
}

func TestCreateDismissal_SnoozeTooFar_Returns400(t *testing.T) {
	_, mux := testHandler()
	tooFar := time.Now().Add(100 * 24 * time.Hour).UTC().Format(time.RFC3339)
	body := `{"account_id":"acc-1","provider":"aws","service":"AmazonEC2","region":"us-east-1","resource_id":"i-1","action":"snooze","reason":"scheduled_deletion","snooze_until":"` + tooFar + `"}`
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, orgRequestWithBody(http.MethodPost, "/v1/dismissals", body))
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
	mux.ServeHTTP(w, orgRequestWithBody(http.MethodPost, "/v1/dismissals", body))
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
	mux.ServeHTTP(w, orgRequest(http.MethodDelete, "/v1/dismissals/1"))
	if w.Code != http.StatusNoContent {
		t.Errorf("expected 204, got %d — body: %s", w.Code, w.Body.String())
	}
}

func TestRevokeDismissal_NotFound_Returns404(t *testing.T) {
	_, mux := testHandler()
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, orgRequest(http.MethodDelete, "/v1/dismissals/999"))
	if w.Code != http.StatusNotFound {
		t.Errorf("expected 404, got %d", w.Code)
	}
}

func TestRevokeDismissal_InvalidID_Returns400(t *testing.T) {
	_, mux := testHandler()
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, orgRequest(http.MethodDelete, "/v1/dismissals/not-a-number"))
	if w.Code != http.StatusBadRequest {
		t.Errorf("expected 400 for non-numeric id, got %d", w.Code)
	}
}

// ── GET /v1/dismissals ────────────────────────────────────────────────────────

func TestListDismissals_Returns200(t *testing.T) {
	_, mux := testHandler()
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, orgRequest(http.MethodGet, "/v1/dismissals"))
	if w.Code != http.StatusOK {
		t.Errorf("expected 200, got %d", w.Code)
	}
}

func TestListDismissals_EmptyList(t *testing.T) {
	_, mux := testHandler()
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, orgRequest(http.MethodGet, "/v1/dismissals"))

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
	mux.ServeHTTP(w, orgRequest(http.MethodGet, "/v1/dismissals"))

	var dismissals []model.DismissAction
	if err := json.NewDecoder(w.Body).Decode(&dismissals); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(dismissals) != 2 {
		t.Fatalf("expected 2 dismissals, got %d", len(dismissals))
	}
}

func TestListDismissals_RoundTripsLastKnownCost(t *testing.T) {
	cost := 42.50
	store := NewMockStore().WithDismissals([]model.DismissAction{
		{ID: 1, AccountID: "acc-1", Action: "dismiss", Reason: "intentional", MonthlyCost: &cost, Currency: "USD"},
		{ID: 2, AccountID: "acc-1", Action: "dismiss", Reason: "intentional"}, // orphaned — nil/empty
	})
	h := newHandlerWith(store)
	mux := newMux(h)

	w := httptest.NewRecorder()
	mux.ServeHTTP(w, orgRequest(http.MethodGet, "/v1/dismissals"))

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", w.Code)
	}
	// Inspect raw JSON: orphan row must omit monthly_cost / currency entirely
	// (omitempty), priced row must include both.
	body := w.Body.String()
	if !strings.Contains(body, `"monthly_cost":42.5`) {
		t.Errorf("expected monthly_cost in priced row, body: %s", body)
	}
	if !strings.Contains(body, `"currency":"USD"`) {
		t.Errorf("expected currency in priced row, body: %s", body)
	}
	if strings.Count(body, `"monthly_cost"`) != 1 {
		t.Errorf("expected monthly_cost only on the priced row (orphan must omit), body: %s", body)
	}
	if strings.Count(body, `"currency"`) != 1 {
		t.Errorf("expected currency only on the priced row (orphan must omit), body: %s", body)
	}
}

func TestListDismissals_StoreError_Returns500(t *testing.T) {
	store := NewMockStore().WithListActiveDismissalsError(errors.New("db down"))
	h := newHandlerWith(store)
	mux := newMux(h)

	w := httptest.NewRecorder()
	mux.ServeHTTP(w, orgRequest(http.MethodGet, "/v1/dismissals"))
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
	mux.ServeHTTP(w, orgRequest(http.MethodGet, "/v1/zombies"))

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
	mux.ServeHTTP(w, orgRequest(http.MethodGet, "/v1/zombies?include_dismissed=true"))

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

// TestCreateDismissal_RecordsUserIdentityViaDevBypass verifies that when the
// request flows through DevBypass (not just the storage.WithOrganizationID helper),
// the stable user_id — not the organization_id or email — lands in dismissed_by.
// Guards against regressing the pre-audit-trail bug where dismissed_by held
// organization_id because user identity was never on the context.
func TestCreateDismissal_RecordsUserIdentityViaDevBypass(t *testing.T) {
	store := NewMockStore().WithZombies([]model.ZombieResource{testZombie})
	mux := newMux(newHandlerWith(store))
	handler := middleware.DevBypass("organization-actor-uuid", "user-actor-uuid", "dev@axiaops.local", mux)

	body := `{
		"account_id":"acc-1","provider":"aws","service":"AmazonRDS",
		"region":"eu-central-1","resource_id":"db-actor-01",
		"action":"dismiss","reason":"false_positive"
	}`
	r := httptest.NewRequest(http.MethodPost, "/v1/dismissals", strings.NewReader(body))
	r.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, r)

	if w.Code != http.StatusCreated {
		t.Fatalf("expected 201, got %d — body: %s", w.Code, w.Body.String())
	}

	dismissals := store.GetDismissals()
	if len(dismissals) != 1 {
		t.Fatalf("expected 1 dismissal recorded, got %d", len(dismissals))
	}
	if got, want := dismissals[0].DismissedBy, "user-actor-uuid"; got != want {
		t.Errorf("dismissed_by: want stable user id %q, got %q", want, got)
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
