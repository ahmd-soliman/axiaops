package api_test

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"axiaops.io/api/internal/api"
	"axiaops.io/shared/analyzer"
	"axiaops.io/shared/model"
	"axiaops.io/shared/queue"
	"axiaops.io/shared/storage"
)

var testGhost = model.GhostResource{
	Provider:    "aws",
	AccountID:   "000000000000",
	Service:     "AmazonRDS",
	Region:      "eu-central-1",
	ResourceID:  "db-stag-01",
	Tags:        map[string]string{"team": "backend", "env": "staging"},
	MonthlyCost: 210.00,
	Currency:    "USD",
	PeriodStart: time.Date(2026, 3, 1, 0, 0, 0, 0, time.UTC),
	PeriodEnd:   time.Date(2026, 3, 31, 0, 0, 0, 0, time.UTC),
	UsageMetric: "DatabaseConnections",
	UsageAvg:    0,
	UsageUnit:   "Count",
	Reason:      "RDS instance has zero database connections — likely abandoned",
	Owner:       "backend",
}

func testHandler() (*api.Handler, *http.ServeMux) {
	store := NewMockStore().WithGhosts([]model.GhostResource{testGhost})
	h := api.New(store, noopQueue())
	mux := http.NewServeMux()
	h.Register(mux)
	return h, mux
}

// tenantRequest creates a request with tenant_id in context (simulates auth middleware).
func tenantRequest(method, path string) *http.Request {
	r := httptest.NewRequest(method, path, nil)
	ctx := storage.WithTenantID(r.Context(), "tenant-test-uuid")
	return r.WithContext(ctx)
}

func tenantRequestWithBody(method, path, body string) *http.Request {
	r := httptest.NewRequest(method, path, strings.NewReader(body))
	r.Header.Set("Content-Type", "application/json")
	ctx := storage.WithTenantID(r.Context(), "tenant-test-uuid")
	return r.WithContext(ctx)
}

// mockStoreWithFailingPing wraps MockStore and overrides Ping to simulate database failure.
type mockStoreWithFailingPing struct {
	*MockStore
	pingErr error
}

func (s *mockStoreWithFailingPing) Ping(_ context.Context) error {
	return s.pingErr
}

// ── GET /health ───────────────────────────────────────────────────────────────

func TestHealth_Returns200(t *testing.T) {
	_, mux := testHandler()
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/health", nil))
	if w.Code != http.StatusOK {
		t.Errorf("expected 200, got %d", w.Code)
	}
}

func TestHealth_DatabasePingFails_Returns503(t *testing.T) {
	store := &mockStoreWithFailingPing{
		MockStore: NewMockStore().WithGhosts([]model.GhostResource{testGhost}),
		pingErr:   errors.New("connection refused"),
	}
	h := api.New(store, noopQueue())
	mux := http.NewServeMux()
	h.Register(mux)

	w := httptest.NewRecorder()
	mux.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/health", nil))

	if w.Code != http.StatusServiceUnavailable {
		t.Errorf("expected 503, got %d", w.Code)
	}

	if !strings.Contains(w.Body.String(), "unreachable") {
		t.Errorf("expected 'unreachable' in response body, got: %s", w.Body.String())
	}
}

// ── GET /ghosts ───────────────────────────────────────────────────────────────

func TestGetGhosts_Returns200(t *testing.T) {
	_, mux := testHandler()
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, tenantRequest(http.MethodGet, "/v1/ghosts"))
	if w.Code != http.StatusOK {
		t.Errorf("expected 200, got %d", w.Code)
	}
}

func TestGetGhosts_ContentType(t *testing.T) {
	_, mux := testHandler()
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, tenantRequest(http.MethodGet, "/v1/ghosts"))
	if ct := w.Header().Get("Content-Type"); ct != "application/json" {
		t.Errorf("expected application/json, got %s", ct)
	}
}

func TestGetGhosts_ReturnsGhostList(t *testing.T) {
	_, mux := testHandler()
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, tenantRequest(http.MethodGet, "/v1/ghosts"))

	var ghosts []model.GhostResource
	if err := json.NewDecoder(w.Body).Decode(&ghosts); err != nil {
		t.Fatalf("failed to decode response: %v", err)
	}
	if len(ghosts) != 1 {
		t.Fatalf("expected 1 ghost, got %d", len(ghosts))
	}
	if ghosts[0].ResourceID != "db-stag-01" {
		t.Errorf("expected resource db-stag-01, got %s", ghosts[0].ResourceID)
	}
	if ghosts[0].MonthlyCost != 210.00 {
		t.Errorf("expected cost 210.00, got %f", ghosts[0].MonthlyCost)
	}
}

func TestGetGhosts_CORSHeader(t *testing.T) {
	h, mux := testHandler()
	w := httptest.NewRecorder()
	h.Handler(mux).ServeHTTP(w, tenantRequest(http.MethodGet, "/v1/ghosts"))
	if w.Header().Get("Access-Control-Allow-Origin") != "*" {
		t.Errorf("expected CORS header, got: %s", w.Header().Get("Access-Control-Allow-Origin"))
	}
}

func TestGetGhosts_OPTIONSPreflight(t *testing.T) {
	h, mux := testHandler()
	w := httptest.NewRecorder()
	h.Handler(mux).ServeHTTP(w, httptest.NewRequest(http.MethodOptions, "/v1/ghosts", nil))
	if w.Code != http.StatusNoContent {
		t.Errorf("expected 204 for OPTIONS, got %d", w.Code)
	}
}

// ── GET /summary ──────────────────────────────────────────────────────────────

func TestGetSummary_Returns200(t *testing.T) {
	_, mux := testHandler()
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, tenantRequest(http.MethodGet, "/v1/summary"))
	if w.Code != http.StatusOK {
		t.Errorf("expected 200, got %d", w.Code)
	}
}

func TestGetSummary_ReturnsSavings(t *testing.T) {
	_, mux := testHandler()
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, tenantRequest(http.MethodGet, "/v1/summary"))

	var summary analyzer.Summary
	if err := json.NewDecoder(w.Body).Decode(&summary); err != nil {
		t.Fatalf("failed to decode response: %v", err)
	}
	if summary.TotalGhosts != 1 {
		t.Errorf("expected 1 ghost, got %d", summary.TotalGhosts)
	}
	if summary.PotentialMonthlySave != 210.00 {
		t.Errorf("expected savings 210.00, got %f", summary.PotentialMonthlySave)
	}
	if summary.Currency != "USD" {
		t.Errorf("expected USD, got %s", summary.Currency)
	}
}

// ── GET /accounts ─────────────────────────────────────────────────────────────

func TestListAccounts_Returns200(t *testing.T) {
	_, mux := testHandler()
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, tenantRequest(http.MethodGet, "/v1/accounts"))
	if w.Code != http.StatusOK {
		t.Errorf("expected 200, got %d", w.Code)
	}
}

func TestListAccounts_EmptyStoreReturnsEmptyArray(t *testing.T) {
	_, mux := testHandler()
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, tenantRequest(http.MethodGet, "/v1/accounts"))

	var accounts []model.Account
	if err := json.NewDecoder(w.Body).Decode(&accounts); err != nil {
		t.Fatalf("failed to decode response: %v", err)
	}
	if len(accounts) != 0 {
		t.Errorf("expected empty array, got %d accounts", len(accounts))
	}
}

func TestListAccounts_ReturnsStoredAccounts(t *testing.T) {
	store := NewMockStore().WithAccounts([]model.Account{
		{ID: "acc-1", Provider: "aws", Label: "prod", AccessKeyID: "AKIA123", Region: "us-east-1", Status: "connected"},
	})
	h := api.New(store, noopQueue())
	mux := http.NewServeMux()
	h.Register(mux)

	w := httptest.NewRecorder()
	mux.ServeHTTP(w, tenantRequest(http.MethodGet, "/v1/accounts"))

	var accounts []model.Account
	if err := json.NewDecoder(w.Body).Decode(&accounts); err != nil {
		t.Fatalf("failed to decode response: %v", err)
	}
	if len(accounts) != 1 {
		t.Fatalf("expected 1 account, got %d", len(accounts))
	}
	if accounts[0].ID != "acc-1" {
		t.Errorf("expected acc-1, got %s", accounts[0].ID)
	}
	if accounts[0].AccessKeyID != "AKIA123" {
		t.Errorf("expected AKIA123, got %s", accounts[0].AccessKeyID)
	}
}

func TestListAccounts_SecretNotExposed(t *testing.T) {
	store := NewMockStore().WithAccounts([]model.Account{
		{ID: "acc-1", SecretEncrypted: "super-secret-value", AccessKeyID: "AKIA123"},
	})
	h := api.New(store, noopQueue())
	mux := http.NewServeMux()
	h.Register(mux)

	w := httptest.NewRecorder()
	mux.ServeHTTP(w, tenantRequest(http.MethodGet, "/v1/accounts"))

	if strings.Contains(w.Body.String(), "super-secret-value") {
		t.Error("response must not contain SecretEncrypted value")
	}
}

// ── POST /accounts ────────────────────────────────────────────────────────────

func TestCreateAccount_Returns201(t *testing.T) {
	t.Setenv("ENCRYPTION_KEY", "0000000000000000000000000000000000000000000000000000000000000000")

	store := NewMockStore()
	h := api.New(store, noopQueue())
	mux := http.NewServeMux()
	h.Register(mux)

	body := `{"provider":"aws","label":"prod","access_key_id":"AKIAIOSFODNN7EXAMPLE","secret_key":"wJalrXUtnFEMI/K7MDENG/bPxRfiCYEXAMPLEKEY","region":"us-east-1"}`
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, tenantRequestWithBody(http.MethodPost, "/v1/accounts", body))

	if w.Code != http.StatusCreated {
		t.Errorf("expected 201, got %d — body: %s", w.Code, w.Body.String())
	}
}

func TestCreateAccount_ReturnsAccountJSON(t *testing.T) {
	t.Setenv("ENCRYPTION_KEY", "0000000000000000000000000000000000000000000000000000000000000000")

	store := NewMockStore()
	h := api.New(store, noopQueue())
	mux := http.NewServeMux()
	h.Register(mux)

	body := `{"provider":"aws","label":"prod","access_key_id":"AKIAIOSFODNN7EXAMPLE","secret_key":"wJalrXUtnFEMI","region":"us-east-1"}`
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, tenantRequestWithBody(http.MethodPost, "/v1/accounts", body))

	var account model.Account
	if err := json.NewDecoder(w.Body).Decode(&account); err != nil {
		t.Fatalf("failed to decode response: %v", err)
	}
	if account.ID == "" {
		t.Error("expected non-empty ID")
	}
	if account.Provider != "aws" {
		t.Errorf("expected aws, got %s", account.Provider)
	}
	if account.Status != "connected" {
		t.Errorf("expected connected, got %s", account.Status)
	}
}

func TestCreateAccount_DefaultsScanIntervalHoursTo24(t *testing.T) {
	t.Setenv("ENCRYPTION_KEY", "0000000000000000000000000000000000000000000000000000000000000000")

	store := NewMockStore()
	h := api.New(store, noopQueue())
	mux := http.NewServeMux()
	h.Register(mux)

	body := `{"access_key_id":"AKIAIOSFODNN7EXAMPLE","secret_key":"wJalrXUtnFEMI"}`
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, tenantRequestWithBody(http.MethodPost, "/v1/accounts", body))

	var account model.Account
	if err := json.NewDecoder(w.Body).Decode(&account); err != nil {
		t.Fatalf("failed to decode response: %v", err)
	}
	if account.ScanIntervalHours != 24 {
		t.Errorf("expected default ScanIntervalHours 24, got %d", account.ScanIntervalHours)
	}
}

func TestCreateAccount_DefaultsProviderAndRegion(t *testing.T) {
	t.Setenv("ENCRYPTION_KEY", "0000000000000000000000000000000000000000000000000000000000000000")

	store := NewMockStore()
	h := api.New(store, noopQueue())
	mux := http.NewServeMux()
	h.Register(mux)

	body := `{"access_key_id":"AKIAIOSFODNN7EXAMPLE","secret_key":"wJalrXUtnFEMI"}`
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, tenantRequestWithBody(http.MethodPost, "/v1/accounts", body))

	var account model.Account
	if err := json.NewDecoder(w.Body).Decode(&account); err != nil {
		t.Fatalf("failed to decode response: %v", err)
	}
	if account.Provider != "aws" {
		t.Errorf("expected default provider aws, got %s", account.Provider)
	}
	if account.Region != "us-east-1" {
		t.Errorf("expected default region us-east-1, got %s", account.Region)
	}
}

func TestCreateAccount_SecretNotInResponse(t *testing.T) {
	t.Setenv("ENCRYPTION_KEY", "0000000000000000000000000000000000000000000000000000000000000000")

	store := NewMockStore()
	h := api.New(store, noopQueue())
	mux := http.NewServeMux()
	h.Register(mux)

	secret := "wJalrXUtnFEMI/K7MDENG/bPxRfiCYEXAMPLEKEY"
	body := `{"access_key_id":"AKIAIOSFODNN7EXAMPLE","secret_key":"` + secret + `"}`
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, tenantRequestWithBody(http.MethodPost, "/v1/accounts", body))

	if strings.Contains(w.Body.String(), secret) {
		t.Error("response must not contain the plaintext secret_key")
	}
}

func TestCreateAccount_MissingAccessKeyID_Returns400(t *testing.T) {
	t.Setenv("ENCRYPTION_KEY", "0000000000000000000000000000000000000000000000000000000000000000")

	_, mux := testHandler()
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, tenantRequestWithBody(http.MethodPost, "/v1/accounts", `{"secret_key":"somekey"}`))

	if w.Code != http.StatusBadRequest {
		t.Errorf("expected 400, got %d", w.Code)
	}
}

func TestCreateAccount_MissingSecretKey_Returns400(t *testing.T) {
	t.Setenv("ENCRYPTION_KEY", "0000000000000000000000000000000000000000000000000000000000000000")

	_, mux := testHandler()
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, tenantRequestWithBody(http.MethodPost, "/v1/accounts", `{"access_key_id":"AKIA123"}`))

	if w.Code != http.StatusBadRequest {
		t.Errorf("expected 400, got %d", w.Code)
	}
}

func TestCreateAccount_InvalidJSON_Returns400(t *testing.T) {
	_, mux := testHandler()
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, tenantRequestWithBody(http.MethodPost, "/v1/accounts", `not-json`))

	if w.Code != http.StatusBadRequest {
		t.Errorf("expected 400, got %d", w.Code)
	}
}

// ── DELETE /accounts/{id} ─────────────────────────────────────────────────────

// ── PATCH /accounts/{id} ────────────────────────────────────────────────────

func TestUpdateAccount_UpdatesScanIntervalHours(t *testing.T) {
	store := NewMockStore().WithAccounts([]model.Account{
		{ID: "acc-1", TenantID: "tenant-test-uuid", Provider: "aws", Label: "prod", AccessKeyID: "AKIA123", Region: "us-east-1", ScanIntervalHours: 24},
	})
	h := api.New(store, noopQueue())
	mux := http.NewServeMux()
	h.Register(mux)

	body := `{"scan_interval_hours":12}`
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, tenantRequestWithBody(http.MethodPatch, "/v1/accounts/acc-1", body))

	if w.Code != http.StatusOK {
		t.Errorf("expected 200, got %d — body: %s", w.Code, w.Body.String())
	}

	var account model.Account
	if err := json.NewDecoder(w.Body).Decode(&account); err != nil {
		t.Fatalf("failed to decode response: %v", err)
	}
	if account.ScanIntervalHours != 12 {
		t.Errorf("expected ScanIntervalHours 12, got %d", account.ScanIntervalHours)
	}
}

func TestUpdateAccount_UpdatesMultipleFields(t *testing.T) {
	store := NewMockStore().WithAccounts([]model.Account{
		{ID: "acc-1", TenantID: "tenant-test-uuid", Provider: "aws", Label: "prod", AccessKeyID: "AKIA123", Region: "us-east-1", ScanIntervalHours: 24},
	})
	h := api.New(store, noopQueue())
	mux := http.NewServeMux()
	h.Register(mux)

	body := `{"label":"staging","region":"eu-west-1","scan_interval_hours":6}`
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, tenantRequestWithBody(http.MethodPatch, "/v1/accounts/acc-1", body))

	if w.Code != http.StatusOK {
		t.Errorf("expected 200, got %d", w.Code)
	}

	var account model.Account
	if err := json.NewDecoder(w.Body).Decode(&account); err != nil {
		t.Fatalf("failed to decode response: %v", err)
	}
	if account.Label != "staging" {
		t.Errorf("expected label staging, got %s", account.Label)
	}
	if account.Region != "eu-west-1" {
		t.Errorf("expected region eu-west-1, got %s", account.Region)
	}
	if account.ScanIntervalHours != 6 {
		t.Errorf("expected ScanIntervalHours 6, got %d", account.ScanIntervalHours)
	}
}

func TestUpdateAccount_ScanIntervalHoursZero(t *testing.T) {
	store := NewMockStore().WithAccounts([]model.Account{
		{ID: "acc-1", TenantID: "tenant-test-uuid", Provider: "aws", Label: "prod", AccessKeyID: "AKIA123", Region: "us-east-1", ScanIntervalHours: 24},
	})
	h := api.New(store, noopQueue())
	mux := http.NewServeMux()
	h.Register(mux)

	body := `{"scan_interval_hours":0}`
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, tenantRequestWithBody(http.MethodPatch, "/v1/accounts/acc-1", body))

	if w.Code != http.StatusOK {
		t.Errorf("expected 200 for zero interval (always eligible per scheduled tick), got %d", w.Code)
	}

	var account model.Account
	if err := json.NewDecoder(w.Body).Decode(&account); err != nil {
		t.Fatalf("failed to decode response: %v", err)
	}
	if account.ScanIntervalHours != 0 {
		t.Errorf("expected ScanIntervalHours 0, got %d", account.ScanIntervalHours)
	}
}

func TestUpdateAccount_NegativeScanIntervalHours_Returns400(t *testing.T) {
	store := NewMockStore().WithAccounts([]model.Account{
		{ID: "acc-1", TenantID: "tenant-test-uuid", Provider: "aws", Label: "prod", AccessKeyID: "AKIA123", Region: "us-east-1", ScanIntervalHours: 24},
	})
	h := api.New(store, noopQueue())
	mux := http.NewServeMux()
	h.Register(mux)

	body := `{"scan_interval_hours":-1}`
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, tenantRequestWithBody(http.MethodPatch, "/v1/accounts/acc-1", body))

	if w.Code != http.StatusBadRequest {
		t.Errorf("expected 400 for negative interval, got %d", w.Code)
	}

	if !strings.Contains(w.Body.String(), "must be >= 0") {
		t.Errorf("expected error message containing 'must be >= 0', got: %s", w.Body.String())
	}
}

func TestUpdateAccount_AccountNotFound_Returns404(t *testing.T) {
	store := NewMockStore().WithGetAccountError(errors.New("not found"))
	h := api.New(store, noopQueue())
	mux := http.NewServeMux()
	h.Register(mux)

	body := `{"scan_interval_hours":12}`
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, tenantRequestWithBody(http.MethodPatch, "/v1/accounts/nonexistent", body))

	if w.Code != http.StatusNotFound {
		t.Errorf("expected 404, got %d", w.Code)
	}
}

// ── DELETE /accounts/{id} ────────────────────────────────────────────────────

func TestDeleteAccount_Returns204(t *testing.T) {
	_, mux := testHandler()
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, tenantRequest(http.MethodDelete, "/v1/accounts/acc-1"))

	if w.Code != http.StatusNoContent {
		t.Errorf("expected 204, got %d", w.Code)
	}
}

// ── POST /accounts/{id}/scan ──────────────────────────────────────────────────

func TestScanAccount_Returns200(t *testing.T) {
	store := NewMockStore().WithAccounts([]model.Account{
		{ID: "acc-1", TenantID: "tenant-test-uuid", Provider: "aws", AccessKeyID: "AKIA123", Region: "us-east-1"},
	})
	h := api.New(store, &testCaptureQueue{})
	mux := http.NewServeMux()
	h.Register(mux)

	w := httptest.NewRecorder()
	mux.ServeHTTP(w, tenantRequest(http.MethodPost, "/v1/accounts/acc-1/scan"))

	if w.Code != http.StatusOK {
		t.Errorf("expected 200, got %d — body: %s", w.Code, w.Body.String())
	}
}

func TestScanAccount_ReturnsScanningStatus(t *testing.T) {
	store := NewMockStore().WithAccounts([]model.Account{
		{ID: "acc-1", TenantID: "tenant-test-uuid", Provider: "aws", AccessKeyID: "AKIA123", Region: "us-east-1"},
	})
	h := api.New(store, &testCaptureQueue{})
	mux := http.NewServeMux()
	h.Register(mux)

	w := httptest.NewRecorder()
	mux.ServeHTTP(w, tenantRequest(http.MethodPost, "/v1/accounts/acc-1/scan"))

	var resp map[string]string
	if err := json.NewDecoder(w.Body).Decode(&resp); err != nil {
		t.Fatalf("failed to decode response: %v", err)
	}
	if resp["status"] != "scanning" {
		t.Errorf("expected status scanning, got %s", resp["status"])
	}
}

func TestScanAccount_AccountNotFound_Returns404(t *testing.T) {
	store := NewMockStore().WithGetAccountError(errors.New("not found"))
	h := api.New(store, noopQueue())
	mux := http.NewServeMux()
	h.Register(mux)

	w := httptest.NewRecorder()
	mux.ServeHTTP(w, tenantRequest(http.MethodPost, "/v1/accounts/nonexistent/scan"))

	if w.Code != http.StatusNotFound {
		t.Errorf("expected 404, got %d", w.Code)
	}
}

func TestScanAccount_ScanAlreadyInProgress_Returns409(t *testing.T) {
	store := NewMockStore().
		WithAccounts([]model.Account{
			{ID: "acc-1", TenantID: "tenant-test-uuid", Provider: "aws", AccessKeyID: "AKIA123", Region: "us-east-1", Status: "scanning"},
		}).
		WithAccountAlreadyScanning("acc-1")
	h := api.New(store, noopQueue())
	mux := http.NewServeMux()
	h.Register(mux)

	w := httptest.NewRecorder()
	mux.ServeHTTP(w, tenantRequest(http.MethodPost, "/v1/accounts/acc-1/scan"))

	if w.Code != http.StatusConflict {
		t.Fatalf("expected 409, got %d — body: %s", w.Code, w.Body.String())
	}
	if !strings.Contains(w.Body.String(), "scan already in progress") {
		t.Errorf("expected conflict message in body, got: %s", w.Body.String())
	}
}

// ── GET /trend ────────────────────────────────────────────────────────────────

func TestGetTrend_Returns200(t *testing.T) {
	_, mux := testHandler()
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, tenantRequest(http.MethodGet, "/v1/trend"))
	if w.Code != http.StatusOK {
		t.Errorf("expected 200, got %d", w.Code)
	}
}

func TestGetTrend_ContentType(t *testing.T) {
	_, mux := testHandler()
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, tenantRequest(http.MethodGet, "/v1/trend"))
	if ct := w.Header().Get("Content-Type"); ct != "application/json" {
		t.Errorf("expected application/json, got %s", ct)
	}
}

func TestGetTrend_EmptyStoreReturnsEmptyArray(t *testing.T) {
	// testHandler stub returns nil snapshots — handler must coerce nil → [].
	_, mux := testHandler()
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, tenantRequest(http.MethodGet, "/v1/trend"))

	var snaps []model.GhostSnapshot
	if err := json.NewDecoder(w.Body).Decode(&snaps); err != nil {
		t.Fatalf("failed to decode response: %v", err)
	}
	if snaps == nil {
		t.Error("expected non-nil empty array, got JSON null")
	}
	if len(snaps) != 0 {
		t.Errorf("expected 0 snapshots, got %d", len(snaps))
	}
}

func TestGetTrend_ReturnsSnapshots(t *testing.T) {
	now := time.Now().UTC().Truncate(time.Second)
	store := NewMockStore().
		WithGhosts([]model.GhostResource{testGhost}).
		WithSnapshots([]model.GhostSnapshot{
			{ID: "snap-1", AccountID: "acc-1", SnapshotAt: now.Add(-2 * time.Hour), GhostCount: 3, TotalMonthlyCost: 150.00, Currency: "USD"},
			{ID: "snap-2", AccountID: "acc-1", SnapshotAt: now, GhostCount: 5, TotalMonthlyCost: 300.00, Currency: "USD"},
		})
	h := api.New(store, noopQueue())
	mux := http.NewServeMux()
	h.Register(mux)

	w := httptest.NewRecorder()
	mux.ServeHTTP(w, tenantRequest(http.MethodGet, "/v1/trend"))

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", w.Code)
	}

	var snaps []model.GhostSnapshot
	if err := json.NewDecoder(w.Body).Decode(&snaps); err != nil {
		t.Fatalf("failed to decode response: %v", err)
	}
	if len(snaps) != 2 {
		t.Fatalf("expected 2 snapshots, got %d", len(snaps))
	}
	if snaps[0].ID != "snap-1" {
		t.Errorf("expected first snapshot ID snap-1, got %s", snaps[0].ID)
	}
	if snaps[1].TotalMonthlyCost != 300.00 {
		t.Errorf("expected second snapshot cost 300.00, got %f", snaps[1].TotalMonthlyCost)
	}
}

func TestGetTrend_SnapshotTenantIDNotExposed(t *testing.T) {
	store := NewMockStore().
		WithGhosts([]model.GhostResource{testGhost}).
		WithSnapshots([]model.GhostSnapshot{
			{ID: "snap-1", AccountID: "acc-1", TenantID: "secret-tenant", GhostCount: 1, TotalMonthlyCost: 50.00, Currency: "USD"},
		})
	h := api.New(store, noopQueue())
	mux := http.NewServeMux()
	h.Register(mux)

	w := httptest.NewRecorder()
	mux.ServeHTTP(w, tenantRequest(http.MethodGet, "/v1/trend"))

	if strings.Contains(w.Body.String(), "secret-tenant") {
		t.Error("response must not expose tenant_id (json:\"-\" tag)")
	}
}

func TestGetTrend_StoreError_Returns500(t *testing.T) {
	store := NewMockStore().
		WithGhosts([]model.GhostResource{testGhost}).
		WithListSnapshotsError(errors.New("db connection lost"))
	h := api.New(store, noopQueue())
	mux := http.NewServeMux()
	h.Register(mux)

	w := httptest.NewRecorder()
	mux.ServeHTTP(w, tenantRequest(http.MethodGet, "/v1/trend"))

	if w.Code != http.StatusInternalServerError {
		t.Errorf("expected 500, got %d", w.Code)
	}
}

func TestGetTrend_AccountIDQueryParamPassedToStore(t *testing.T) {
	store := NewMockStore().WithGhosts([]model.GhostResource{testGhost})
	h := api.New(store, noopQueue())
	mux := http.NewServeMux()
	h.Register(mux)

	w := httptest.NewRecorder()
	mux.ServeHTTP(w, tenantRequest(http.MethodGet, "/v1/trend?account_id=acc-42"))

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", w.Code)
	}
	if store.GetLastListSnapshotsAccountID() != "acc-42" {
		t.Errorf("expected account_id acc-42 forwarded to store, got %q", store.GetLastListSnapshotsAccountID())
	}
}

func TestGetTrend_NoAccountIDQueryParam_PassesEmptyString(t *testing.T) {
	store := NewMockStore().WithGhosts([]model.GhostResource{testGhost})
	h := api.New(store, noopQueue())
	mux := http.NewServeMux()
	h.Register(mux)

	w := httptest.NewRecorder()
	mux.ServeHTTP(w, tenantRequest(http.MethodGet, "/v1/trend"))

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", w.Code)
	}
	if store.GetLastListSnapshotsAccountID() != "" {
		t.Errorf("expected empty account_id forwarded to store, got %q", store.GetLastListSnapshotsAccountID())
	}
}

// noopQueue is a queue.Queue that does nothing — used in tests that don't exercise scan.
func noopQueue() queue.Queue { return &testCaptureQueue{} }

// testCaptureQueue records enqueued jobs and always succeeds.
type testCaptureQueue struct{ jobs []queue.ScanJob }

func (q *testCaptureQueue) Enqueue(_ context.Context, job queue.ScanJob) error {
	q.jobs = append(q.jobs, job)
	return nil
}
func (q *testCaptureQueue) Dequeue(ctx context.Context) (queue.ScanJob, error) {
	<-ctx.Done()
	return queue.ScanJob{}, ctx.Err()
}
func (q *testCaptureQueue) Close() error { return nil }
