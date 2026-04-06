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
	"axiaops.io/shared/storage"
)

// stubStore is an in-memory Store for handler tests.
type stubStore struct {
	ghosts        []model.GhostResource
	accounts      []model.Account
	getAccountErr error
}

func (s *stubStore) Save(_ context.Context, _ []model.CostRecord) (int64, error) { return 0, nil }
func (s *stubStore) SaveGhosts(_ context.Context, g []model.GhostResource) error {
	s.ghosts = g
	return nil
}
func (s *stubStore) LoadGhosts(_ context.Context) ([]model.GhostResource, error) {
	return s.ghosts, nil
}
func (s *stubStore) UpsertTenant(_ context.Context, _, _ string) (model.Tenant, error) {
	return model.Tenant{}, nil
}
func (s *stubStore) UpsertUser(_ context.Context, _, _, _, _ string) (model.User, error) {
	return model.User{}, nil
}
func (s *stubStore) SaveAccount(_ context.Context, a model.Account) error {
	s.accounts = append(s.accounts, a)
	return nil
}
func (s *stubStore) ListAccounts(_ context.Context) ([]model.Account, error) {
	return s.accounts, nil
}
func (s *stubStore) GetAccount(_ context.Context, id string) (model.Account, error) {
	if s.getAccountErr != nil {
		return model.Account{}, s.getAccountErr
	}
	for _, a := range s.accounts {
		if a.ID == id {
			return a, nil
		}
	}
	return model.Account{}, errors.New("not found")
}
func (s *stubStore) DeleteAccount(_ context.Context, _ string) error          { return nil }
func (s *stubStore) UpdateAccountStatus(_ context.Context, _, _ string) error { return nil }
func (s *stubStore) Close() error                                              { return nil }

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
	store := &stubStore{ghosts: []model.GhostResource{testGhost}}
	h := api.New(store)
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

// ── GET /health ───────────────────────────────────────────────────────────────

func TestHealth_Returns200(t *testing.T) {
	_, mux := testHandler()
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/health", nil))
	if w.Code != http.StatusOK {
		t.Errorf("expected 200, got %d", w.Code)
	}
}

// ── GET /ghosts ───────────────────────────────────────────────────────────────

func TestGetGhosts_Returns200(t *testing.T) {
	_, mux := testHandler()
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, tenantRequest(http.MethodGet, "/ghosts"))
	if w.Code != http.StatusOK {
		t.Errorf("expected 200, got %d", w.Code)
	}
}

func TestGetGhosts_ContentType(t *testing.T) {
	_, mux := testHandler()
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, tenantRequest(http.MethodGet, "/ghosts"))
	if ct := w.Header().Get("Content-Type"); ct != "application/json" {
		t.Errorf("expected application/json, got %s", ct)
	}
}

func TestGetGhosts_ReturnsGhostList(t *testing.T) {
	_, mux := testHandler()
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, tenantRequest(http.MethodGet, "/ghosts"))

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
	h.Handler(mux).ServeHTTP(w, tenantRequest(http.MethodGet, "/ghosts"))
	if w.Header().Get("Access-Control-Allow-Origin") != "*" {
		t.Errorf("expected CORS header, got: %s", w.Header().Get("Access-Control-Allow-Origin"))
	}
}

func TestGetGhosts_OPTIONSPreflight(t *testing.T) {
	h, mux := testHandler()
	w := httptest.NewRecorder()
	h.Handler(mux).ServeHTTP(w, httptest.NewRequest(http.MethodOptions, "/ghosts", nil))
	if w.Code != http.StatusNoContent {
		t.Errorf("expected 204 for OPTIONS, got %d", w.Code)
	}
}

// ── GET /summary ──────────────────────────────────────────────────────────────

func TestGetSummary_Returns200(t *testing.T) {
	_, mux := testHandler()
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, tenantRequest(http.MethodGet, "/summary"))
	if w.Code != http.StatusOK {
		t.Errorf("expected 200, got %d", w.Code)
	}
}

func TestGetSummary_ReturnsSavings(t *testing.T) {
	_, mux := testHandler()
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, tenantRequest(http.MethodGet, "/summary"))

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
	mux.ServeHTTP(w, tenantRequest(http.MethodGet, "/accounts"))
	if w.Code != http.StatusOK {
		t.Errorf("expected 200, got %d", w.Code)
	}
}

func TestListAccounts_EmptyStoreReturnsEmptyArray(t *testing.T) {
	_, mux := testHandler()
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, tenantRequest(http.MethodGet, "/accounts"))

	var accounts []model.Account
	if err := json.NewDecoder(w.Body).Decode(&accounts); err != nil {
		t.Fatalf("failed to decode response: %v", err)
	}
	if len(accounts) != 0 {
		t.Errorf("expected empty array, got %d accounts", len(accounts))
	}
}

func TestListAccounts_ReturnsStoredAccounts(t *testing.T) {
	store := &stubStore{
		accounts: []model.Account{
			{ID: "acc-1", Provider: "aws", Label: "prod", AccessKeyID: "AKIA123", Region: "us-east-1", Status: "connected"},
		},
	}
	h := api.New(store)
	mux := http.NewServeMux()
	h.Register(mux)

	w := httptest.NewRecorder()
	mux.ServeHTTP(w, tenantRequest(http.MethodGet, "/accounts"))

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
	store := &stubStore{
		accounts: []model.Account{
			{ID: "acc-1", SecretEncrypted: "super-secret-value", AccessKeyID: "AKIA123"},
		},
	}
	h := api.New(store)
	mux := http.NewServeMux()
	h.Register(mux)

	w := httptest.NewRecorder()
	mux.ServeHTTP(w, tenantRequest(http.MethodGet, "/accounts"))

	if strings.Contains(w.Body.String(), "super-secret-value") {
		t.Error("response must not contain SecretEncrypted value")
	}
}

// ── POST /accounts ────────────────────────────────────────────────────────────

func TestCreateAccount_Returns201(t *testing.T) {
	t.Setenv("ENCRYPTION_KEY", "0000000000000000000000000000000000000000000000000000000000000000")

	store := &stubStore{}
	h := api.New(store)
	mux := http.NewServeMux()
	h.Register(mux)

	body := `{"provider":"aws","label":"prod","access_key_id":"AKIAIOSFODNN7EXAMPLE","secret_key":"wJalrXUtnFEMI/K7MDENG/bPxRfiCYEXAMPLEKEY","region":"us-east-1"}`
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, tenantRequestWithBody(http.MethodPost, "/accounts", body))

	if w.Code != http.StatusCreated {
		t.Errorf("expected 201, got %d — body: %s", w.Code, w.Body.String())
	}
}

func TestCreateAccount_ReturnsAccountJSON(t *testing.T) {
	t.Setenv("ENCRYPTION_KEY", "0000000000000000000000000000000000000000000000000000000000000000")

	store := &stubStore{}
	h := api.New(store)
	mux := http.NewServeMux()
	h.Register(mux)

	body := `{"provider":"aws","label":"prod","access_key_id":"AKIAIOSFODNN7EXAMPLE","secret_key":"wJalrXUtnFEMI","region":"us-east-1"}`
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, tenantRequestWithBody(http.MethodPost, "/accounts", body))

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

func TestCreateAccount_DefaultsProviderAndRegion(t *testing.T) {
	t.Setenv("ENCRYPTION_KEY", "0000000000000000000000000000000000000000000000000000000000000000")

	store := &stubStore{}
	h := api.New(store)
	mux := http.NewServeMux()
	h.Register(mux)

	body := `{"access_key_id":"AKIAIOSFODNN7EXAMPLE","secret_key":"wJalrXUtnFEMI"}`
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, tenantRequestWithBody(http.MethodPost, "/accounts", body))

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

	store := &stubStore{}
	h := api.New(store)
	mux := http.NewServeMux()
	h.Register(mux)

	secret := "wJalrXUtnFEMI/K7MDENG/bPxRfiCYEXAMPLEKEY"
	body := `{"access_key_id":"AKIAIOSFODNN7EXAMPLE","secret_key":"` + secret + `"}`
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, tenantRequestWithBody(http.MethodPost, "/accounts", body))

	if strings.Contains(w.Body.String(), secret) {
		t.Error("response must not contain the plaintext secret_key")
	}
}

func TestCreateAccount_MissingAccessKeyID_Returns400(t *testing.T) {
	t.Setenv("ENCRYPTION_KEY", "0000000000000000000000000000000000000000000000000000000000000000")

	_, mux := testHandler()
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, tenantRequestWithBody(http.MethodPost, "/accounts", `{"secret_key":"somekey"}`))

	if w.Code != http.StatusBadRequest {
		t.Errorf("expected 400, got %d", w.Code)
	}
}

func TestCreateAccount_MissingSecretKey_Returns400(t *testing.T) {
	t.Setenv("ENCRYPTION_KEY", "0000000000000000000000000000000000000000000000000000000000000000")

	_, mux := testHandler()
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, tenantRequestWithBody(http.MethodPost, "/accounts", `{"access_key_id":"AKIA123"}`))

	if w.Code != http.StatusBadRequest {
		t.Errorf("expected 400, got %d", w.Code)
	}
}

func TestCreateAccount_InvalidJSON_Returns400(t *testing.T) {
	_, mux := testHandler()
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, tenantRequestWithBody(http.MethodPost, "/accounts", `not-json`))

	if w.Code != http.StatusBadRequest {
		t.Errorf("expected 400, got %d", w.Code)
	}
}

// ── DELETE /accounts/{id} ─────────────────────────────────────────────────────

func TestDeleteAccount_Returns204(t *testing.T) {
	_, mux := testHandler()
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, tenantRequest(http.MethodDelete, "/accounts/acc-1"))

	if w.Code != http.StatusNoContent {
		t.Errorf("expected 204, got %d", w.Code)
	}
}

// ── POST /accounts/{id}/scan ──────────────────────────────────────────────────

func TestScanAccount_Returns200(t *testing.T) {
	// Fake ingestion service that accepts the scan request.
	ingestion := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer ingestion.Close()
	t.Setenv("INGESTION_URL", ingestion.URL)

	store := &stubStore{
		accounts: []model.Account{
			{ID: "acc-1", TenantID: "tenant-test-uuid", Provider: "aws", AccessKeyID: "AKIA123", Region: "us-east-1"},
		},
	}
	h := api.New(store)
	mux := http.NewServeMux()
	h.Register(mux)

	w := httptest.NewRecorder()
	mux.ServeHTTP(w, tenantRequest(http.MethodPost, "/accounts/acc-1/scan"))

	if w.Code != http.StatusOK {
		t.Errorf("expected 200, got %d — body: %s", w.Code, w.Body.String())
	}
}

func TestScanAccount_ReturnsScanningStatus(t *testing.T) {
	ingestion := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer ingestion.Close()
	t.Setenv("INGESTION_URL", ingestion.URL)

	store := &stubStore{
		accounts: []model.Account{
			{ID: "acc-1", TenantID: "tenant-test-uuid", Provider: "aws", AccessKeyID: "AKIA123", Region: "us-east-1"},
		},
	}
	h := api.New(store)
	mux := http.NewServeMux()
	h.Register(mux)

	w := httptest.NewRecorder()
	mux.ServeHTTP(w, tenantRequest(http.MethodPost, "/accounts/acc-1/scan"))

	var resp map[string]string
	if err := json.NewDecoder(w.Body).Decode(&resp); err != nil {
		t.Fatalf("failed to decode response: %v", err)
	}
	if resp["status"] != "scanning" {
		t.Errorf("expected status scanning, got %s", resp["status"])
	}
}

func TestScanAccount_AccountNotFound_Returns404(t *testing.T) {
	store := &stubStore{getAccountErr: errors.New("not found")}
	h := api.New(store)
	mux := http.NewServeMux()
	h.Register(mux)

	w := httptest.NewRecorder()
	mux.ServeHTTP(w, tenantRequest(http.MethodPost, "/accounts/nonexistent/scan"))

	if w.Code != http.StatusNotFound {
		t.Errorf("expected 404, got %d", w.Code)
	}
}
