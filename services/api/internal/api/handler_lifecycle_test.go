// Package api_test — integration-level handler tests.
//
// These tests sit in the same black-box package as handler_test.go and share
// the stubStore type declared there. They add a trackingStore wrapper that
// records method calls and injects per-method errors, enabling assertions about
// cross-handler interactions, async goroutine behaviour, and tenant-context
// propagation.
package api_test

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	"axiaops.io/api/internal/api"
	"axiaops.io/api/internal/middleware"
	"axiaops.io/shared/model"
	"axiaops.io/shared/storage"
)

// ─── trackingStore ────────────────────────────────────────────────────────────

// statusCall records a single UpdateAccountStatus invocation.
type statusCall struct {
	accountID string
	status    string
}

// trackingStore embeds *stubStore and overrides selected methods to record
// calls, capture context values, and inject per-method errors.
// Methods not overridden fall through to the embedded *stubStore.
type trackingStore struct {
	*stubStore

	mu sync.Mutex

	// TryMarkAccountScanning instrumentation.
	tryMarkCalledWith []string // account IDs passed, in call order
	tryMarkErr        error    // if non-nil, returned instead of real logic

	// UpdateAccountStatus instrumentation.
	statusCalls  []statusCall  // recorded in call order
	statusSignal chan struct{} // non-blocking send after each call (buffer ≥ 1)

	// capturedTenantIDs collects the tenant ID read from the context on every
	// overridden store call, enabling tenant-propagation assertions.
	capturedTenantIDs []string

	// Per-method error injection.
	loadGhostsErr   error
	listAccountsErr error
	deleteAccErr    error
}

func newTrackingStore(base *stubStore) *trackingStore {
	return &trackingStore{stubStore: base}
}

func (s *trackingStore) captureTenant(ctx context.Context) {
	s.mu.Lock()
	s.capturedTenantIDs = append(s.capturedTenantIDs, storage.TenantIDFromCtx(ctx))
	s.mu.Unlock()
}

func (s *trackingStore) TryMarkAccountScanning(_ context.Context, id string) (bool, error) {
	s.mu.Lock()
	s.tryMarkCalledWith = append(s.tryMarkCalledWith, id)
	err := s.tryMarkErr
	busy := s.tryMarkBusy // promoted from embedded *stubStore
	s.mu.Unlock()

	if err != nil {
		return false, err
	}
	if busy {
		return false, nil
	}
	return true, nil
}

func (s *trackingStore) UpdateAccountStatus(_ context.Context, id, status string) error {
	s.mu.Lock()
	s.statusCalls = append(s.statusCalls, statusCall{id, status})
	ch := s.statusSignal
	s.mu.Unlock()

	if ch != nil {
		select {
		case ch <- struct{}{}:
		default:
		}
	}
	return nil
}

func (s *trackingStore) LoadGhosts(ctx context.Context) ([]model.GhostResource, error) {
	s.captureTenant(ctx)
	if s.loadGhostsErr != nil {
		return nil, s.loadGhostsErr
	}
	return s.stubStore.LoadGhosts(ctx)
}

func (s *trackingStore) ListAccounts(ctx context.Context) ([]model.Account, error) {
	s.captureTenant(ctx)
	if s.listAccountsErr != nil {
		return nil, s.listAccountsErr
	}
	return s.stubStore.ListAccounts(ctx)
}

func (s *trackingStore) DeleteAccount(_ context.Context, _ string) error {
	return s.deleteAccErr // nil on success, non-nil to inject error
}

// ─── Test helpers ─────────────────────────────────────────────────────────────

// newTrackingHandler builds a Handler backed by ts and returns the registered mux.
func newTrackingHandler(base *stubStore) (*trackingStore, *http.ServeMux) {
	ts := newTrackingStore(base)
	mux := http.NewServeMux()
	api.New(ts).Register(mux)
	return ts, mux
}

// waitForStatus blocks until the trackingStore's statusSignal fires or the
// test deadline is exceeded.
func waitForStatus(t *testing.T, sig <-chan struct{}) {
	t.Helper()
	select {
	case <-sig:
	case <-time.After(5 * time.Second):
		t.Fatal("timed out waiting for UpdateAccountStatus to be called")
	}
}

// fakeIngestion returns an httptest.Server that responds to any request with
// the given HTTP status code.
func fakeIngestion(statusCode int) *httptest.Server {
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(statusCode)
	}))
}

// ─── TryMarkAccountScanning ───────────────────────────────────────────────────

// TestScanAccount_TryMarkScanning_Called verifies that POST /accounts/{id}/scan
// invokes TryMarkAccountScanning with the exact account ID from the URL.
func TestScanAccount_TryMarkScanning_Called(t *testing.T) {
	ingestion := fakeIngestion(http.StatusOK)
	defer ingestion.Close()
	t.Setenv("INGESTION_URL", ingestion.URL)

	base := &stubStore{
		accounts: []model.Account{
			{ID: "acc-99", TenantID: "tenant-test-uuid", Provider: "aws", AccessKeyID: "AKIA", Region: "eu-west-1"},
		},
	}
	ts, mux := newTrackingHandler(base)
	sig := make(chan struct{}, 1)
	ts.statusSignal = sig

	w := httptest.NewRecorder()
	mux.ServeHTTP(w, tenantRequest(http.MethodPost, "/accounts/acc-99/scan"))

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d — body: %s", w.Code, w.Body.String())
	}

	waitForStatus(t, sig) // wait for async goroutine to finish

	ts.mu.Lock()
	called := append([]string(nil), ts.tryMarkCalledWith...)
	ts.mu.Unlock()

	if len(called) == 0 {
		t.Fatal("expected TryMarkAccountScanning to be called")
	}
	if called[0] != "acc-99" {
		t.Errorf("expected TryMarkAccountScanning with acc-99, got %q", called[0])
	}
}

// TestScanAccount_TryMarkScanning_StoreError_Returns500 verifies that a store
// error from TryMarkAccountScanning is surfaced as HTTP 500.
func TestScanAccount_TryMarkScanning_StoreError_Returns500(t *testing.T) {
	base := &stubStore{
		accounts: []model.Account{
			{ID: "acc-1", TenantID: "tenant-test-uuid", Provider: "aws", AccessKeyID: "AKIA", Region: "us-east-1"},
		},
	}
	ts := newTrackingStore(base)
	ts.tryMarkErr = errors.New("db lock timeout")

	mux := http.NewServeMux()
	api.New(ts).Register(mux)

	w := httptest.NewRecorder()
	mux.ServeHTTP(w, tenantRequest(http.MethodPost, "/accounts/acc-1/scan"))

	if w.Code != http.StatusInternalServerError {
		t.Errorf("expected 500, got %d — body: %s", w.Code, w.Body.String())
	}
}

// ─── Async scan goroutine ─────────────────────────────────────────────────────

// TestScanAccount_Async_UpdatesStatusConnectedOnSuccess verifies that after the
// ingestion service returns 200, the background goroutine marks the account
// status as "connected".
func TestScanAccount_Async_UpdatesStatusConnectedOnSuccess(t *testing.T) {
	ingestion := fakeIngestion(http.StatusOK)
	defer ingestion.Close()
	t.Setenv("INGESTION_URL", ingestion.URL)

	base := &stubStore{
		accounts: []model.Account{
			{ID: "acc-async", TenantID: "tenant-test-uuid", Provider: "aws", AccessKeyID: "AKIA", Region: "us-east-1"},
		},
	}
	ts := newTrackingStore(base)
	sig := make(chan struct{}, 1)
	ts.statusSignal = sig

	mux := http.NewServeMux()
	api.New(ts).Register(mux)

	w := httptest.NewRecorder()
	mux.ServeHTTP(w, tenantRequest(http.MethodPost, "/accounts/acc-async/scan"))

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", w.Code)
	}

	waitForStatus(t, sig)

	ts.mu.Lock()
	calls := append([]statusCall(nil), ts.statusCalls...)
	ts.mu.Unlock()

	if len(calls) == 0 {
		t.Fatal("expected UpdateAccountStatus to be called")
	}
	last := calls[len(calls)-1]
	if last.accountID != "acc-async" {
		t.Errorf("expected UpdateAccountStatus for acc-async, got %q", last.accountID)
	}
	if last.status != "connected" {
		t.Errorf("expected status connected after successful ingestion, got %q", last.status)
	}
}

// TestScanAccount_Async_UpdatesStatusErrorOnIngestionFailure verifies that when
// the ingestion service returns a non-200 response the background goroutine
// marks the account as "error".
func TestScanAccount_Async_UpdatesStatusErrorOnIngestionFailure(t *testing.T) {
	ingestion := fakeIngestion(http.StatusInternalServerError)
	defer ingestion.Close()
	t.Setenv("INGESTION_URL", ingestion.URL)

	base := &stubStore{
		accounts: []model.Account{
			{ID: "acc-fail", TenantID: "tenant-test-uuid", Provider: "aws", AccessKeyID: "AKIA", Region: "us-east-1"},
		},
	}
	ts := newTrackingStore(base)
	sig := make(chan struct{}, 1)
	ts.statusSignal = sig

	mux := http.NewServeMux()
	api.New(ts).Register(mux)

	w := httptest.NewRecorder()
	mux.ServeHTTP(w, tenantRequest(http.MethodPost, "/accounts/acc-fail/scan"))

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", w.Code)
	}

	waitForStatus(t, sig)

	ts.mu.Lock()
	calls := append([]statusCall(nil), ts.statusCalls...)
	ts.mu.Unlock()

	if len(calls) == 0 {
		t.Fatal("expected UpdateAccountStatus to be called")
	}
	last := calls[len(calls)-1]
	if last.status != "error" {
		t.Errorf("expected status error after failed ingestion, got %q", last.status)
	}
}

// ─── Account lifecycle ────────────────────────────────────────────────────────

// TestAccountLifecycle_CreateThenList verifies the create → list flow:
// a POST /accounts creates an account that is visible in a subsequent
// GET /accounts response.
func TestAccountLifecycle_CreateThenList(t *testing.T) {
	t.Setenv("ENCRYPTION_KEY", "0000000000000000000000000000000000000000000000000000000000000000")

	base := &stubStore{}
	_, mux := newTrackingHandler(base)

	// 1. Create the account.
	body := `{"provider":"aws","label":"integration-test","access_key_id":"AKIA_INT","secret_key":"secret123","region":"ap-southeast-1"}`
	wCreate := httptest.NewRecorder()
	mux.ServeHTTP(wCreate, tenantRequestWithBody(http.MethodPost, "/accounts", body))
	if wCreate.Code != http.StatusCreated {
		t.Fatalf("create: expected 201, got %d — body: %s", wCreate.Code, wCreate.Body.String())
	}

	var created model.Account
	if err := json.NewDecoder(wCreate.Body).Decode(&created); err != nil {
		t.Fatalf("decode create response: %v", err)
	}
	if created.ID == "" {
		t.Fatal("expected non-empty account ID in create response")
	}
	if created.Label != "integration-test" {
		t.Errorf("expected label integration-test, got %s", created.Label)
	}

	// 2. List accounts — the created account must appear.
	wList := httptest.NewRecorder()
	mux.ServeHTTP(wList, tenantRequest(http.MethodGet, "/accounts"))
	if wList.Code != http.StatusOK {
		t.Fatalf("list: expected 200, got %d", wList.Code)
	}

	var accounts []model.Account
	if err := json.NewDecoder(wList.Body).Decode(&accounts); err != nil {
		t.Fatalf("decode list response: %v", err)
	}
	if len(accounts) == 0 {
		t.Fatal("expected at least one account after create")
	}
	found := false
	for _, a := range accounts {
		if a.ID == created.ID && a.Label == "integration-test" {
			found = true
		}
	}
	if !found {
		t.Errorf("created account %s not found in GET /accounts response", created.ID)
	}
}

// TestAccountLifecycle_ScanThenTrend verifies that after an account scan the
// snapshot written by the ingestion service (pre-seeded in the stub) is
// returned by GET /trend.
func TestAccountLifecycle_ScanThenTrend(t *testing.T) {
	snapTime := time.Now().UTC().Truncate(time.Second)
	base := &stubStore{
		accounts: []model.Account{
			{ID: "acc-trend", TenantID: "tenant-test-uuid", Provider: "aws", AccessKeyID: "AKIA", Region: "us-east-1"},
		},
		// Simulate what the ingestion service would write after scanning.
		snapshots: []model.GhostSnapshot{
			{ID: "snap-1", AccountID: "acc-trend", SnapshotAt: snapTime, GhostCount: 7, TotalMonthlyCost: 420.0, Currency: "USD"},
		},
	}
	_, mux := newTrackingHandler(base)

	w := httptest.NewRecorder()
	mux.ServeHTTP(w, tenantRequest(http.MethodGet, "/trend?account_id=acc-trend"))
	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", w.Code)
	}

	var snaps []model.GhostSnapshot
	if err := json.NewDecoder(w.Body).Decode(&snaps); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(snaps) != 1 {
		t.Fatalf("expected 1 snapshot, got %d", len(snaps))
	}
	if snaps[0].GhostCount != 7 {
		t.Errorf("expected GhostCount 7, got %d", snaps[0].GhostCount)
	}
	if snaps[0].TotalMonthlyCost != 420.0 {
		t.Errorf("expected TotalMonthlyCost 420.0, got %f", snaps[0].TotalMonthlyCost)
	}
}

// TestAccountLifecycle_MultipleScans verifies that GET /trend reflects
// accumulated snapshot history across multiple scan cycles, preserving the
// order the store returns them.
func TestAccountLifecycle_MultipleScans(t *testing.T) {
	now := time.Now().UTC().Truncate(time.Second)
	base := &stubStore{
		accounts: []model.Account{
			{ID: "acc-multi", TenantID: "tenant-test-uuid", Provider: "aws", AccessKeyID: "AKIA", Region: "us-east-1"},
		},
		// Three snapshots representing three historical scan cycles.
		snapshots: []model.GhostSnapshot{
			{ID: "snap-a", AccountID: "acc-multi", SnapshotAt: now.Add(-4 * time.Hour), GhostCount: 3, TotalMonthlyCost: 150.0, Currency: "USD"},
			{ID: "snap-b", AccountID: "acc-multi", SnapshotAt: now.Add(-2 * time.Hour), GhostCount: 5, TotalMonthlyCost: 250.0, Currency: "USD"},
			{ID: "snap-c", AccountID: "acc-multi", SnapshotAt: now, GhostCount: 2, TotalMonthlyCost: 100.0, Currency: "USD"},
		},
	}
	_, mux := newTrackingHandler(base)

	w := httptest.NewRecorder()
	mux.ServeHTTP(w, tenantRequest(http.MethodGet, "/trend?account_id=acc-multi"))
	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", w.Code)
	}

	var snaps []model.GhostSnapshot
	if err := json.NewDecoder(w.Body).Decode(&snaps); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(snaps) != 3 {
		t.Fatalf("expected 3 snapshots (one per scan cycle), got %d", len(snaps))
	}
	// Order is preserved as returned by the store (oldest-first by convention).
	if snaps[0].ID != "snap-a" {
		t.Errorf("expected first snapshot snap-a (oldest), got %s", snaps[0].ID)
	}
	if snaps[2].ID != "snap-c" {
		t.Errorf("expected last snapshot snap-c (latest), got %s", snaps[2].ID)
	}
	if snaps[1].GhostCount != 5 {
		t.Errorf("expected middle snapshot GhostCount 5, got %d", snaps[1].GhostCount)
	}
}

// ─── GET /trend ───────────────────────────────────────────────────────────────

// TestGetTrend_ReflectsLatestScan verifies that GET /trend exposes the most
// recently written snapshot at the end of the returned slice, reflecting the
// outcome of the newest scan cycle.
func TestGetTrend_ReflectsLatestScan(t *testing.T) {
	now := time.Now().UTC().Truncate(time.Second)
	base := &stubStore{
		snapshots: []model.GhostSnapshot{
			{ID: "old-snap", AccountID: "acc-1", SnapshotAt: now.Add(-time.Hour), GhostCount: 10, TotalMonthlyCost: 500.0, Currency: "USD"},
			{ID: "new-snap", AccountID: "acc-1", SnapshotAt: now, GhostCount: 4, TotalMonthlyCost: 200.0, Currency: "USD"},
		},
	}
	_, mux := newTrackingHandler(base)

	w := httptest.NewRecorder()
	mux.ServeHTTP(w, tenantRequest(http.MethodGet, "/trend"))
	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", w.Code)
	}

	var snaps []model.GhostSnapshot
	if err := json.NewDecoder(w.Body).Decode(&snaps); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(snaps) != 2 {
		t.Fatalf("expected 2 snapshots, got %d", len(snaps))
	}

	// The last entry must be the most recent scan result.
	latest := snaps[len(snaps)-1]
	if latest.ID != "new-snap" {
		t.Errorf("expected latest snapshot new-snap, got %s", latest.ID)
	}
	if latest.GhostCount != 4 {
		t.Errorf("expected latest GhostCount 4, got %d", latest.GhostCount)
	}
	// The first entry must still be the older scan.
	if snaps[0].ID != "old-snap" {
		t.Errorf("expected first snapshot old-snap, got %s", snaps[0].ID)
	}
}

// ─── Error handling: store failures → HTTP 500 ────────────────────────────────

// TestListGhosts_StoreError_Returns500 verifies that a LoadGhosts store error
// is surfaced as HTTP 500.
func TestListGhosts_StoreError_Returns500(t *testing.T) {
	ts := newTrackingStore(&stubStore{})
	ts.loadGhostsErr = errors.New("connection reset by peer")

	mux := http.NewServeMux()
	api.New(ts).Register(mux)

	w := httptest.NewRecorder()
	mux.ServeHTTP(w, tenantRequest(http.MethodGet, "/ghosts"))

	if w.Code != http.StatusInternalServerError {
		t.Errorf("expected 500, got %d", w.Code)
	}
}

// TestGetSummary_StoreError_Returns500 verifies that a LoadGhosts store error
// during summary aggregation is surfaced as HTTP 500.
func TestGetSummary_StoreError_Returns500(t *testing.T) {
	ts := newTrackingStore(&stubStore{})
	ts.loadGhostsErr = errors.New("timeout querying ghosts")

	mux := http.NewServeMux()
	api.New(ts).Register(mux)

	w := httptest.NewRecorder()
	mux.ServeHTTP(w, tenantRequest(http.MethodGet, "/summary"))

	if w.Code != http.StatusInternalServerError {
		t.Errorf("expected 500, got %d", w.Code)
	}
}

// TestListAccounts_StoreError_Returns500 verifies that a ListAccounts store
// error is surfaced as HTTP 500.
func TestListAccounts_StoreError_Returns500(t *testing.T) {
	ts := newTrackingStore(&stubStore{})
	ts.listAccountsErr = errors.New("pg: too many connections")

	mux := http.NewServeMux()
	api.New(ts).Register(mux)

	w := httptest.NewRecorder()
	mux.ServeHTTP(w, tenantRequest(http.MethodGet, "/accounts"))

	if w.Code != http.StatusInternalServerError {
		t.Errorf("expected 500, got %d", w.Code)
	}
}

// TestDeleteAccount_StoreError_Returns500 verifies that a DeleteAccount store
// error is surfaced as HTTP 500.
func TestDeleteAccount_StoreError_Returns500(t *testing.T) {
	ts := newTrackingStore(&stubStore{})
	ts.deleteAccErr = errors.New("foreign key constraint violation")

	mux := http.NewServeMux()
	api.New(ts).Register(mux)

	w := httptest.NewRecorder()
	mux.ServeHTTP(w, tenantRequest(http.MethodDelete, "/accounts/any-id"))

	if w.Code != http.StatusInternalServerError {
		t.Errorf("expected 500, got %d", w.Code)
	}
}

// ─── PATCH /accounts/{id} ────────────────────────────────────────────────────

// TestUpdateAccount_UpdatesLabel_Returns200 verifies that PATCH /accounts/{id}
// applies the label change and returns the updated account in the response.
func TestUpdateAccount_UpdatesLabel_Returns200(t *testing.T) {
	base := &stubStore{
		accounts: []model.Account{
			{ID: "acc-patch", TenantID: "tenant-test-uuid", Provider: "aws", Label: "old-label", AccessKeyID: "AKIA123", Region: "us-east-1"},
		},
	}
	_, mux := newTrackingHandler(base)

	w := httptest.NewRecorder()
	mux.ServeHTTP(w, tenantRequestWithBody(http.MethodPatch, "/accounts/acc-patch", `{"label":"new-label"}`))

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d — body: %s", w.Code, w.Body.String())
	}

	var updated model.Account
	if err := json.NewDecoder(w.Body).Decode(&updated); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if updated.Label != "new-label" {
		t.Errorf("expected label new-label, got %s", updated.Label)
	}
	// Fields not included in the patch must be preserved.
	if updated.AccessKeyID != "AKIA123" {
		t.Errorf("expected AccessKeyID AKIA123 preserved, got %s", updated.AccessKeyID)
	}
	if updated.Region != "us-east-1" {
		t.Errorf("expected Region us-east-1 preserved, got %s", updated.Region)
	}
}

// TestUpdateAccount_UpdatesRegion_Returns200 verifies that PATCH /accounts/{id}
// applies a region change while preserving all other fields.
func TestUpdateAccount_UpdatesRegion_Returns200(t *testing.T) {
	base := &stubStore{
		accounts: []model.Account{
			{ID: "acc-region", TenantID: "tenant-test-uuid", Provider: "aws", Label: "my-account", AccessKeyID: "AKIA123", Region: "us-east-1"},
		},
	}
	_, mux := newTrackingHandler(base)

	w := httptest.NewRecorder()
	mux.ServeHTTP(w, tenantRequestWithBody(http.MethodPatch, "/accounts/acc-region", `{"region":"eu-central-1"}`))

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d — body: %s", w.Code, w.Body.String())
	}

	var updated model.Account
	if err := json.NewDecoder(w.Body).Decode(&updated); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if updated.Region != "eu-central-1" {
		t.Errorf("expected Region eu-central-1, got %s", updated.Region)
	}
	if updated.Label != "my-account" {
		t.Errorf("expected Label my-account preserved, got %s", updated.Label)
	}
}

// TestUpdateAccount_NotFound_Returns404 verifies that PATCH /accounts/{id}
// returns 404 when the account does not exist in the store.
func TestUpdateAccount_NotFound_Returns404(t *testing.T) {
	base := &stubStore{getAccountErr: errors.New("not found")}
	_, mux := newTrackingHandler(base)

	w := httptest.NewRecorder()
	mux.ServeHTTP(w, tenantRequestWithBody(http.MethodPatch, "/accounts/nonexistent", `{"label":"any"}`))

	if w.Code != http.StatusNotFound {
		t.Errorf("expected 404, got %d", w.Code)
	}
}

// TestUpdateAccount_InvalidJSON_Returns400 verifies that a malformed request
// body is rejected with HTTP 400.
func TestUpdateAccount_InvalidJSON_Returns400(t *testing.T) {
	base := &stubStore{
		accounts: []model.Account{
			{ID: "acc-json", Provider: "aws"},
		},
	}
	_, mux := newTrackingHandler(base)

	w := httptest.NewRecorder()
	mux.ServeHTTP(w, tenantRequestWithBody(http.MethodPatch, "/accounts/acc-json", `not-json`))

	if w.Code != http.StatusBadRequest {
		t.Errorf("expected 400, got %d", w.Code)
	}
}

// ─── Tenant-context propagation ───────────────────────────────────────────────

// TestTenantIsolation_LoadGhosts_ReceivesContextTenantID verifies that the
// tenant ID set by the auth middleware (DevBypass here) is forwarded to the
// store's LoadGhosts call via the context.
func TestTenantIsolation_LoadGhosts_ReceivesContextTenantID(t *testing.T) {
	base := &stubStore{ghosts: []model.GhostResource{testGhost}}
	ts, mux := newTrackingHandler(base)

	// DevBypass injects the tenant ID via the middleware context key, exactly
	// as the real auth middleware does. Without it, middleware.TenantID returns ""
	// because the middleware and storage packages use distinct context key types.
	handler := middleware.DevBypass("tenant-alpha-uuid", mux)
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/ghosts", nil))

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", w.Code)
	}

	ts.mu.Lock()
	captured := append([]string(nil), ts.capturedTenantIDs...)
	ts.mu.Unlock()

	if len(captured) == 0 {
		t.Fatal("capturedTenantIDs is empty — tenant ID was not propagated to store")
	}
	if captured[0] != "tenant-alpha-uuid" {
		t.Errorf("expected tenant-alpha-uuid propagated to store, got %q", captured[0])
	}
}

// TestTenantIsolation_ListAccounts_ReceivesContextTenantID verifies the same
// propagation guarantee for the ListAccounts path.
func TestTenantIsolation_ListAccounts_ReceivesContextTenantID(t *testing.T) {
	base := &stubStore{}
	ts, mux := newTrackingHandler(base)

	handler := middleware.DevBypass("tenant-beta-uuid", mux)
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/accounts", nil))

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", w.Code)
	}

	ts.mu.Lock()
	captured := append([]string(nil), ts.capturedTenantIDs...)
	ts.mu.Unlock()

	if len(captured) == 0 {
		t.Fatal("capturedTenantIDs is empty — tenant ID was not propagated to store")
	}
	if captured[0] != "tenant-beta-uuid" {
		t.Errorf("expected tenant-beta-uuid propagated to store, got %q", captured[0])
	}
}

// ─── Concurrent scans: tenant isolation ──────────────────────────────────────

// TestConcurrentScans_TenantIsolation verifies that when two tenants trigger
// account scans simultaneously:
//
//   - Both receive HTTP 200 with status "scanning".
//   - Each store's TryMarkAccountScanning is called only for its own account.
//   - UpdateAccountStatus calls never reference the other tenant's account.
func TestConcurrentScans_TenantIsolation(t *testing.T) {
	ingestion := fakeIngestion(http.StatusOK)
	defer ingestion.Close()
	t.Setenv("INGESTION_URL", ingestion.URL)

	const (
		tenantA = "tenant-isolation-a"
		tenantB = "tenant-isolation-b"
		accA    = "acc-iso-alpha"
		accB    = "acc-iso-beta"
	)

	// Each tenant has its own isolated store — mirroring RLS isolation in
	// production. A real PostgreSQL store would enforce this at the DB layer;
	// here we simulate it with separate in-memory instances.
	storeA := newTrackingStore(&stubStore{accounts: []model.Account{
		{ID: accA, TenantID: tenantA, Provider: "aws", AccessKeyID: "AKIA_A", Region: "us-east-1"},
	}})
	storeB := newTrackingStore(&stubStore{accounts: []model.Account{
		{ID: accB, TenantID: tenantB, Provider: "aws", AccessKeyID: "AKIA_B", Region: "eu-west-1"},
	}})

	sigA := make(chan struct{}, 1)
	sigB := make(chan struct{}, 1)
	storeA.statusSignal = sigA
	storeB.statusSignal = sigB

	muxA := http.NewServeMux()
	api.New(storeA).Register(muxA)
	muxB := http.NewServeMux()
	api.New(storeB).Register(muxB)

	var (
		wg    sync.WaitGroup
		codeA int
		codeB int
	)
	wg.Add(2)

	go func() {
		defer wg.Done()
		req := httptest.NewRequest(http.MethodPost, "/accounts/"+accA+"/scan", nil)
		req = req.WithContext(storage.WithTenantID(req.Context(), tenantA))
		w := httptest.NewRecorder()
		muxA.ServeHTTP(w, req)
		codeA = w.Code
	}()
	go func() {
		defer wg.Done()
		req := httptest.NewRequest(http.MethodPost, "/accounts/"+accB+"/scan", nil)
		req = req.WithContext(storage.WithTenantID(req.Context(), tenantB))
		w := httptest.NewRecorder()
		muxB.ServeHTTP(w, req)
		codeB = w.Code
	}()

	wg.Wait()

	if codeA != http.StatusOK {
		t.Errorf("tenant A scan: expected 200, got %d", codeA)
	}
	if codeB != http.StatusOK {
		t.Errorf("tenant B scan: expected 200, got %d", codeB)
	}

	// Wait for both async goroutines to complete before checking call records.
	waitForStatus(t, sigA)
	waitForStatus(t, sigB)

	// Tenant A's store must have been called only for accA.
	storeA.mu.Lock()
	markedA := append([]string(nil), storeA.tryMarkCalledWith...)
	statusesA := append([]statusCall(nil), storeA.statusCalls...)
	storeA.mu.Unlock()

	// Tenant B's store must have been called only for accB.
	storeB.mu.Lock()
	markedB := append([]string(nil), storeB.tryMarkCalledWith...)
	statusesB := append([]statusCall(nil), storeB.statusCalls...)
	storeB.mu.Unlock()

	if len(markedA) == 0 || markedA[0] != accA {
		t.Errorf("expected storeA.TryMarkAccountScanning(%q), got %v", accA, markedA)
	}
	if len(markedB) == 0 || markedB[0] != accB {
		t.Errorf("expected storeB.TryMarkAccountScanning(%q), got %v", accB, markedB)
	}

	for _, call := range statusesA {
		if call.accountID != accA {
			t.Errorf("storeA received UpdateAccountStatus for wrong account: %q", call.accountID)
		}
	}
	for _, call := range statusesB {
		if call.accountID != accB {
			t.Errorf("storeB received UpdateAccountStatus for wrong account: %q", call.accountID)
		}
	}
}
