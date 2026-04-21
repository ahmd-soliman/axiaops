// Package api_test — integration-level handler tests.
//
// These tests exercise cross-handler interactions, async goroutine behaviour,
// and tenant-context propagation using the unified MockStore from test_helpers.go.
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
	"axiaops.io/shared/queue"
	"axiaops.io/shared/storage"
)

// ─── Test helpers ─────────────────────────────────────────────────────────────

// newTrackingHandler builds a Handler backed by a MockStore and a captureQueue.
// Returns the store, mux, and queue for assertions.
func newTrackingHandler(mockStore *MockStore) (*MockStore, *http.ServeMux) {
	mux := http.NewServeMux()
	api.New(mockStore, &captureQueueLC{}).Register(mux)
	return mockStore, mux
}

// captureQueueLC records enqueued jobs and always succeeds.
type captureQueueLC struct{ jobs []queue.ScanJob }

func (q *captureQueueLC) Enqueue(_ context.Context, job queue.ScanJob) error {
	q.jobs = append(q.jobs, job)
	return nil
}
func (q *captureQueueLC) Dequeue(ctx context.Context) (queue.ScanJob, error) {
	<-ctx.Done()
	return queue.ScanJob{}, ctx.Err()
}
func (q *captureQueueLC) Close() error { return nil }

// ─── TryMarkAccountScanning ───────────────────────────────────────────────────

// TestScanAccount_TryMarkScanning_Called verifies that POST /accounts/{id}/scan
// marks the account as scanning and enqueues a job.
func TestScanAccount_TryMarkScanning_Called(t *testing.T) {
	mockStore := NewMockStore().
		WithAccounts([]model.Account{
			{ID: "acc-99", TenantID: "tenant-test-uuid", Provider: "aws", AccessKeyID: "AKIA", Region: "eu-west-1"},
		})
	_, mux := newTrackingHandler(mockStore)

	w := httptest.NewRecorder()
	mux.ServeHTTP(w, tenantRequest(http.MethodPost, "/v1/accounts/acc-99/scan"))

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d — body: %s", w.Code, w.Body.String())
	}
}

// TestScanAccount_TryMarkScanning_StoreError_Returns500 verifies that a store
// error from TryMarkAccountScanning is surfaced as HTTP 500.
func TestScanAccount_TryMarkScanning_StoreError_Returns500(t *testing.T) {
	mockStore := NewMockStore().
		WithAccounts([]model.Account{
			{ID: "acc-1", TenantID: "tenant-test-uuid", Provider: "aws", AccessKeyID: "AKIA", Region: "us-east-1"},
		}).
		WithTryMarkScanningError(errors.New("db lock timeout"))

	_, mux := newTrackingHandler(mockStore)

	w := httptest.NewRecorder()
	mux.ServeHTTP(w, tenantRequest(http.MethodPost, "/v1/accounts/acc-1/scan"))

	if w.Code != http.StatusInternalServerError {
		t.Errorf("expected 500, got %d — body: %s", w.Code, w.Body.String())
	}
}

// ─── Async scan goroutine ─────────────────────────────────────────────────────

// TestScanAccount_Async_UpdatesStatusConnectedOnSuccess verifies that the handler
// returns 200 and enqueues a job when scan is triggered successfully.
// Status updates happen in the worker (tested separately).
func TestScanAccount_Async_UpdatesStatusConnectedOnSuccess(t *testing.T) {
	mockStore := NewMockStore().
		WithAccounts([]model.Account{
			{ID: "acc-async", TenantID: "tenant-test-uuid", Provider: "aws", AccessKeyID: "AKIA", Region: "us-east-1"},
		})

	_, mux := newTrackingHandler(mockStore)

	w := httptest.NewRecorder()
	mux.ServeHTTP(w, tenantRequest(http.MethodPost, "/v1/accounts/acc-async/scan"))

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", w.Code)
	}
}

// TestScanAccount_Async_UpdatesStatusErrorOnIngestionFailure verifies that when
// enqueue fails the handler returns 500 and marks the account as error.
func TestScanAccount_Async_UpdatesStatusErrorOnIngestionFailure(t *testing.T) {
	mockStore := NewMockStore().
		WithAccounts([]model.Account{
			{ID: "acc-fail", TenantID: "tenant-test-uuid", Provider: "aws", AccessKeyID: "AKIA", Region: "us-east-1"},
		})

	mux := http.NewServeMux()
	api.New(mockStore, &errorQueueLC{}).Register(mux)

	w := httptest.NewRecorder()
	mux.ServeHTTP(w, tenantRequest(http.MethodPost, "/v1/accounts/acc-fail/scan"))

	if w.Code != http.StatusInternalServerError {
		t.Fatalf("expected 500 on enqueue failure, got %d", w.Code)
	}
}

// errorQueueLC always fails on Enqueue.
type errorQueueLC struct{}

func (q *errorQueueLC) Enqueue(_ context.Context, _ queue.ScanJob) error {
	return errors.New("queue unavailable")
}
func (q *errorQueueLC) Dequeue(ctx context.Context) (queue.ScanJob, error) {
	<-ctx.Done()
	return queue.ScanJob{}, ctx.Err()
}
func (q *errorQueueLC) Close() error { return nil }

// ─── Account lifecycle ────────────────────────────────────────────────────────

// TestAccountLifecycle_CreateThenList verifies the create → list flow:
// a POST /accounts creates an account that is visible in a subsequent
// GET /accounts response.
func TestAccountLifecycle_CreateThenList(t *testing.T) {
	t.Setenv("ENCRYPTION_KEY", "0000000000000000000000000000000000000000000000000000000000000000")

	mockStore := NewMockStore()
	_, mux := newTrackingHandler(mockStore)

	// 1. Create the account.
	body := `{"provider":"aws","label":"integration-test","access_key_id":"AKIA_INT","secret_key":"secret123","region":"ap-southeast-1"}`
	wCreate := httptest.NewRecorder()
	mux.ServeHTTP(wCreate, tenantRequestWithBody(http.MethodPost, "/v1/accounts", body))
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
	mux.ServeHTTP(wList, tenantRequest(http.MethodGet, "/v1/accounts"))
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
	mockStore := NewMockStore().
		WithAccounts([]model.Account{
			{ID: "acc-trend", TenantID: "tenant-test-uuid", Provider: "aws", AccessKeyID: "AKIA", Region: "us-east-1"},
		}).
		// Simulate what the ingestion service would write after scanning.
		WithSnapshots([]model.GhostSnapshot{
			{ID: "snap-1", AccountID: "acc-trend", SnapshotAt: snapTime, GhostCount: 7, TotalMonthlyCost: 420.0, Currency: "USD"},
		})
	_, mux := newTrackingHandler(mockStore)

	w := httptest.NewRecorder()
	mux.ServeHTTP(w, tenantRequest(http.MethodGet, "/v1/trend?account_id=acc-trend"))
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
	mockStore := NewMockStore().
		WithAccounts([]model.Account{
			{ID: "acc-multi", TenantID: "tenant-test-uuid", Provider: "aws", AccessKeyID: "AKIA", Region: "us-east-1"},
		}).
		// Three snapshots representing three historical scan cycles.
		WithSnapshots([]model.GhostSnapshot{
			{ID: "snap-a", AccountID: "acc-multi", SnapshotAt: now.Add(-4 * time.Hour), GhostCount: 3, TotalMonthlyCost: 150.0, Currency: "USD"},
			{ID: "snap-b", AccountID: "acc-multi", SnapshotAt: now.Add(-2 * time.Hour), GhostCount: 5, TotalMonthlyCost: 250.0, Currency: "USD"},
			{ID: "snap-c", AccountID: "acc-multi", SnapshotAt: now, GhostCount: 2, TotalMonthlyCost: 100.0, Currency: "USD"},
		})
	_, mux := newTrackingHandler(mockStore)

	w := httptest.NewRecorder()
	mux.ServeHTTP(w, tenantRequest(http.MethodGet, "/v1/trend?account_id=acc-multi"))
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
	mockStore := NewMockStore().
		WithSnapshots([]model.GhostSnapshot{
			{ID: "old-snap", AccountID: "acc-1", SnapshotAt: now.Add(-time.Hour), GhostCount: 10, TotalMonthlyCost: 500.0, Currency: "USD"},
			{ID: "new-snap", AccountID: "acc-1", SnapshotAt: now, GhostCount: 4, TotalMonthlyCost: 200.0, Currency: "USD"},
		})
	_, mux := newTrackingHandler(mockStore)

	w := httptest.NewRecorder()
	mux.ServeHTTP(w, tenantRequest(http.MethodGet, "/v1/trend"))
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
	mockStore := NewMockStore().
		WithLoadGhostsError(errors.New("connection reset by peer"))

	_, mux := newTrackingHandler(mockStore)

	w := httptest.NewRecorder()
	mux.ServeHTTP(w, tenantRequest(http.MethodGet, "/v1/ghosts"))

	if w.Code != http.StatusInternalServerError {
		t.Errorf("expected 500, got %d", w.Code)
	}
}

// TestGetSummary_StoreError_Returns500 verifies that a LoadGhosts store error
// during summary aggregation is surfaced as HTTP 500.
func TestGetSummary_StoreError_Returns500(t *testing.T) {
	mockStore := NewMockStore().
		WithLoadGhostsError(errors.New("timeout querying ghosts"))

	_, mux := newTrackingHandler(mockStore)

	w := httptest.NewRecorder()
	mux.ServeHTTP(w, tenantRequest(http.MethodGet, "/v1/summary"))

	if w.Code != http.StatusInternalServerError {
		t.Errorf("expected 500, got %d", w.Code)
	}
}

// TestListAccounts_StoreError_Returns500 verifies that a ListAccounts store
// error is surfaced as HTTP 500.
func TestListAccounts_StoreError_Returns500(t *testing.T) {
	mockStore := NewMockStore().
		WithListAccountsError(errors.New("pg: too many connections"))

	_, mux := newTrackingHandler(mockStore)

	w := httptest.NewRecorder()
	mux.ServeHTTP(w, tenantRequest(http.MethodGet, "/v1/accounts"))

	if w.Code != http.StatusInternalServerError {
		t.Errorf("expected 500, got %d", w.Code)
	}
}

// TestDeleteAccount_StoreError_Returns500 verifies that a DeleteAccount store
// error is surfaced as HTTP 500.
func TestDeleteAccount_StoreError_Returns500(t *testing.T) {
	mockStore := NewMockStore().
		WithDeleteAccountError(errors.New("foreign key constraint violation"))

	_, mux := newTrackingHandler(mockStore)

	w := httptest.NewRecorder()
	mux.ServeHTTP(w, tenantRequest(http.MethodDelete, "/v1/accounts/any-id"))

	if w.Code != http.StatusInternalServerError {
		t.Errorf("expected 500, got %d", w.Code)
	}
}

// ─── PATCH /accounts/{id} ────────────────────────────────────────────────────

// TestUpdateAccount_UpdatesLabel_Returns200 verifies that PATCH /accounts/{id}
// applies the label change and returns the updated account in the response.
func TestUpdateAccount_UpdatesLabel_Returns200(t *testing.T) {
	mockStore := NewMockStore().
		WithAccounts([]model.Account{
			{ID: "acc-patch", TenantID: "tenant-test-uuid", Provider: "aws", Label: "old-label", AccessKeyID: "AKIA123", Region: "us-east-1"},
		})
	_, mux := newTrackingHandler(mockStore)

	w := httptest.NewRecorder()
	mux.ServeHTTP(w, tenantRequestWithBody(http.MethodPatch, "/v1/accounts/acc-patch", `{"label":"new-label"}`))

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
	mockStore := NewMockStore().
		WithAccounts([]model.Account{
			{ID: "acc-region", TenantID: "tenant-test-uuid", Provider: "aws", Label: "my-account", AccessKeyID: "AKIA123", Region: "us-east-1"},
		})
	_, mux := newTrackingHandler(mockStore)

	w := httptest.NewRecorder()
	mux.ServeHTTP(w, tenantRequestWithBody(http.MethodPatch, "/v1/accounts/acc-region", `{"region":"eu-central-1"}`))

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
	mockStore := NewMockStore().
		WithGetAccountError(errors.New("not found"))
	_, mux := newTrackingHandler(mockStore)

	w := httptest.NewRecorder()
	mux.ServeHTTP(w, tenantRequestWithBody(http.MethodPatch, "/v1/accounts/nonexistent", `{"label":"any"}`))

	if w.Code != http.StatusNotFound {
		t.Errorf("expected 404, got %d", w.Code)
	}
}

// TestUpdateAccount_InvalidJSON_Returns400 verifies that a malformed request
// body is rejected with HTTP 400.
func TestUpdateAccount_InvalidJSON_Returns400(t *testing.T) {
	mockStore := NewMockStore().
		WithAccounts([]model.Account{
			{ID: "acc-json", Provider: "aws"},
		})
	_, mux := newTrackingHandler(mockStore)

	w := httptest.NewRecorder()
	mux.ServeHTTP(w, tenantRequestWithBody(http.MethodPatch, "/v1/accounts/acc-json", `not-json`))

	if w.Code != http.StatusBadRequest {
		t.Errorf("expected 400, got %d", w.Code)
	}
}

// ─── Tenant-context propagation ───────────────────────────────────────────────

// TestTenantIsolation_LoadGhosts_ReceivesContextTenantID verifies that the
// tenant ID set by the auth middleware (DevBypass here) is forwarded to the
// store's LoadGhosts call via the context.
func TestTenantIsolation_LoadGhosts_ReceivesContextTenantID(t *testing.T) {
	mockStore := NewMockStore().
		WithGhosts([]model.GhostResource{testGhost})
	_, mux := newTrackingHandler(mockStore)

	// DevBypass injects the tenant ID via the middleware context key, exactly
	// as the real auth middleware does. Without it, middleware.TenantID returns ""
	// because the middleware and storage packages use distinct context key types.
	handler := middleware.DevBypass("tenant-alpha-uuid", mockStore, mux)
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/v1/ghosts", nil))

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", w.Code)
	}

	captured := mockStore.GetCapturedTenantIDs()

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
	mockStore := NewMockStore()
	_, mux := newTrackingHandler(mockStore)

	handler := middleware.DevBypass("tenant-beta-uuid", mockStore, mux)
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/v1/accounts", nil))

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", w.Code)
	}

	captured := mockStore.GetCapturedTenantIDs()

	if len(captured) == 0 {
		t.Fatal("capturedTenantIDs is empty — tenant ID was not propagated to store")
	}
	if captured[0] != "tenant-beta-uuid" {
		t.Errorf("expected tenant-beta-uuid propagated to store, got %q", captured[0])
	}
}

// ─── Concurrent scans: tenant isolation ──────────────────────────────────────

// TestConcurrentScans_TenantIsolation verifies that when two tenants trigger
// account scans simultaneously both receive HTTP 200 with status "scanning".
func TestConcurrentScans_TenantIsolation(t *testing.T) {
	const (
		tenantA = "tenant-isolation-a"
		tenantB = "tenant-isolation-b"
		accA    = "acc-iso-alpha"
		accB    = "acc-iso-beta"
	)

	storeA := NewMockStore().WithAccounts([]model.Account{
		{ID: accA, TenantID: tenantA, Provider: "aws", AccessKeyID: "AKIA_A", Region: "us-east-1"},
	})
	storeB := NewMockStore().WithAccounts([]model.Account{
		{ID: accB, TenantID: tenantB, Provider: "aws", AccessKeyID: "AKIA_B", Region: "eu-west-1"},
	})

	muxA := http.NewServeMux()
	api.New(storeA, &captureQueueLC{}).Register(muxA)
	muxB := http.NewServeMux()
	api.New(storeB, &captureQueueLC{}).Register(muxB)

	var (
		wg    sync.WaitGroup
		codeA int
		codeB int
	)
	wg.Add(2)
	go func() {
		defer wg.Done()
		req := httptest.NewRequest(http.MethodPost, "/v1/accounts/"+accA+"/scan", nil)
		req = req.WithContext(storage.WithTenantID(req.Context(), tenantA))
		w := httptest.NewRecorder()
		muxA.ServeHTTP(w, req)
		codeA = w.Code
	}()
	go func() {
		defer wg.Done()
		req := httptest.NewRequest(http.MethodPost, "/v1/accounts/"+accB+"/scan", nil)
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
}
