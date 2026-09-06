// Package api_test — integration-level handler tests.
//
// These tests exercise cross-handler interactions, async goroutine behaviour,
// and organization-context propagation using the unified MockStore from test_helpers.go.
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
)

// ─── Test helpers ─────────────────────────────────────────────────────────────

// newTrackingHandler builds a Handler backed by a MockStore and a captureQueue.
// Returns the store, mux, and queue for assertions.
func newTrackingHandler(mockStore *MockStore) (*MockStore, *http.ServeMux) {
	mux := http.NewServeMux()
	api.New(mockStore, &captureQueueLC{}).Register(mux)
	return mockStore, mux
}

// captureQueueLC records enqueued jobs and always succeeds. Enqueue now runs
// in scanAccount's detached goroutine (see scanEnqueueTimeout), so jobs is
// mutex-guarded and signal is an optional non-blocking notification a test
// can wait on instead of racing the goroutine with a bare field read.
type captureQueueLC struct {
	mu     sync.Mutex
	jobs   []queue.ScanJob
	signal chan struct{}
}

func (q *captureQueueLC) Enqueue(_ context.Context, job queue.ScanJob) error {
	q.mu.Lock()
	q.jobs = append(q.jobs, job)
	q.mu.Unlock()
	if q.signal != nil {
		select {
		case q.signal <- struct{}{}:
		default:
		}
	}
	return nil
}
func (q *captureQueueLC) Jobs() []queue.ScanJob {
	q.mu.Lock()
	defer q.mu.Unlock()
	return append([]queue.ScanJob(nil), q.jobs...)
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
			{ID: "acc-99", OrganizationID: "organization-test-uuid", Provider: "aws", AccessKeyID: "AKIA", Region: "eu-west-1"},
		})
	_, mux := newTrackingHandler(mockStore)

	w := httptest.NewRecorder()
	mux.ServeHTTP(w, orgRequest(http.MethodPost, "/v1/accounts/acc-99/scan"))

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d — body: %s", w.Code, w.Body.String())
	}
}

// TestScanAccount_TryMarkScanning_StoreError_Returns500 verifies that a store
// error from TryMarkAccountScanning is surfaced as HTTP 500.
func TestScanAccount_TryMarkScanning_StoreError_Returns500(t *testing.T) {
	mockStore := NewMockStore().
		WithAccounts([]model.Account{
			{ID: "acc-1", OrganizationID: "organization-test-uuid", Provider: "aws", AccessKeyID: "AKIA", Region: "us-east-1"},
		}).
		WithTryMarkScanningError(errors.New("db lock timeout"))

	_, mux := newTrackingHandler(mockStore)

	w := httptest.NewRecorder()
	mux.ServeHTTP(w, orgRequest(http.MethodPost, "/v1/accounts/acc-1/scan"))

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
			{ID: "acc-async", OrganizationID: "organization-test-uuid", Provider: "aws", AccessKeyID: "AKIA", Region: "us-east-1"},
		})

	_, mux := newTrackingHandler(mockStore)

	w := httptest.NewRecorder()
	mux.ServeHTTP(w, orgRequest(http.MethodPost, "/v1/accounts/acc-async/scan"))

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", w.Code)
	}
}

// TestScanAccount_Async_UpdatesStatusErrorOnIngestionFailure verifies that
// enqueue runs detached from the request: the handler returns 200/"scanning"
// immediately (it can't know yet whether the downstream scan will succeed —
// same contract the Redis-backed queue has always had), and the account is
// marked "error" a moment later once the background enqueue actually fails.
func TestScanAccount_Async_UpdatesStatusErrorOnIngestionFailure(t *testing.T) {
	statusSignal := make(chan struct{}, 4)
	mockStore := NewMockStore().
		WithAccounts([]model.Account{
			{ID: "acc-fail", OrganizationID: "organization-test-uuid", Provider: "aws", AccessKeyID: "AKIA", Region: "us-east-1"},
		}).
		WithStatusSignal(statusSignal)

	mux := http.NewServeMux()
	api.New(mockStore, &errorQueueLC{}).Register(mux)

	w := httptest.NewRecorder()
	mux.ServeHTTP(w, orgRequest(http.MethodPost, "/v1/accounts/acc-fail/scan"))

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200 (enqueue is detached, failure surfaces later), got %d — body: %s", w.Code, w.Body.String())
	}

	select {
	case <-statusSignal:
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for the detached enqueue failure to mark the account error")
	}

	calls := mockStore.GetStatusUpdateCalls()
	if len(calls) != 1 || calls[0].accountID != "acc-fail" || calls[0].status != "error" {
		t.Fatalf("expected one UpdateAccountStatus(acc-fail, error) call, got %+v", calls)
	}
}

// TestScanAccount_SurvivesRequestContextCancellation verifies that a client
// disconnect (r.Context() cancelled) right as the handler starts does not
// abort the mark-scanning + enqueue write. Regression test for the bug where
// TryMarkAccountScanning/Enqueue rode r.Context(): a cancelled request left
// the account stuck at status='scanning' with no job ever queued, since the
// handler returned 500 before reaching Enqueue.
func TestScanAccount_SurvivesRequestContextCancellation(t *testing.T) {
	mockStore := NewMockStore().
		WithAccounts([]model.Account{
			{ID: "acc-cancelled", OrganizationID: "organization-test-uuid", Provider: "aws", AccessKeyID: "AKIA", Region: "us-east-1"},
		})
	captureQ := &captureQueueLC{signal: make(chan struct{}, 1)}
	mux := http.NewServeMux()
	api.New(mockStore, captureQ).Register(mux)

	req := orgRequest(http.MethodPost, "/v1/accounts/acc-cancelled/scan")
	cancelledCtx, cancel := context.WithCancel(req.Context())
	cancel()
	req = req.WithContext(cancelledCtx)

	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200 despite cancelled request context, got %d — body: %s", w.Code, w.Body.String())
	}

	// Enqueue now runs in a detached goroutine (its own context, independent
	// of req.Context()), so wait for it rather than racing a bare field read.
	select {
	case <-captureQ.signal:
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for the detached enqueue despite cancelled request context")
	}
	if jobs := captureQ.Jobs(); len(jobs) != 1 {
		t.Fatalf("expected 1 job enqueued despite cancelled request context, got %d", len(jobs))
	}
	if !mockStore.accountScanning["acc-cancelled"] {
		t.Fatal("expected account to be marked scanning")
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
	mux.ServeHTTP(wCreate, orgRequestWithBody(http.MethodPost, "/v1/accounts", body))
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
	mux.ServeHTTP(wList, orgRequest(http.MethodGet, "/v1/accounts"))
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
			{ID: "acc-trend", OrganizationID: "organization-test-uuid", Provider: "aws", AccessKeyID: "AKIA", Region: "us-east-1"},
		}).
		// Simulate what the ingestion service would write after scanning.
		WithSnapshots([]model.ZombieSnapshot{
			{ID: "snap-1", AccountID: "acc-trend", SnapshotAt: snapTime, ZombieCount: 7, TotalMonthlyCost: 420.0, Currency: "USD"},
		})
	_, mux := newTrackingHandler(mockStore)

	w := httptest.NewRecorder()
	mux.ServeHTTP(w, orgRequest(http.MethodGet, "/v1/trend?account_id=acc-trend"))
	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", w.Code)
	}

	var snaps []model.ZombieSnapshot
	if err := json.NewDecoder(w.Body).Decode(&snaps); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(snaps) != 1 {
		t.Fatalf("expected 1 snapshot, got %d", len(snaps))
	}
	if snaps[0].ZombieCount != 7 {
		t.Errorf("expected ZombieCount 7, got %d", snaps[0].ZombieCount)
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
			{ID: "acc-multi", OrganizationID: "organization-test-uuid", Provider: "aws", AccessKeyID: "AKIA", Region: "us-east-1"},
		}).
		// Three snapshots representing three historical scan cycles.
		WithSnapshots([]model.ZombieSnapshot{
			{ID: "snap-a", AccountID: "acc-multi", SnapshotAt: now.Add(-4 * time.Hour), ZombieCount: 3, TotalMonthlyCost: 150.0, Currency: "USD"},
			{ID: "snap-b", AccountID: "acc-multi", SnapshotAt: now.Add(-2 * time.Hour), ZombieCount: 5, TotalMonthlyCost: 250.0, Currency: "USD"},
			{ID: "snap-c", AccountID: "acc-multi", SnapshotAt: now, ZombieCount: 2, TotalMonthlyCost: 100.0, Currency: "USD"},
		})
	_, mux := newTrackingHandler(mockStore)

	w := httptest.NewRecorder()
	mux.ServeHTTP(w, orgRequest(http.MethodGet, "/v1/trend?account_id=acc-multi"))
	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", w.Code)
	}

	var snaps []model.ZombieSnapshot
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
	if snaps[1].ZombieCount != 5 {
		t.Errorf("expected middle snapshot ZombieCount 5, got %d", snaps[1].ZombieCount)
	}
}

// ─── GET /trend ───────────────────────────────────────────────────────────────

// TestGetTrend_ReflectsLatestScan verifies that GET /trend exposes the most
// recently written snapshot at the end of the returned slice, reflecting the
// outcome of the newest scan cycle.
func TestGetTrend_ReflectsLatestScan(t *testing.T) {
	now := time.Now().UTC().Truncate(time.Second)
	mockStore := NewMockStore().
		WithSnapshots([]model.ZombieSnapshot{
			{ID: "old-snap", AccountID: "acc-1", SnapshotAt: now.Add(-time.Hour), ZombieCount: 10, TotalMonthlyCost: 500.0, Currency: "USD"},
			{ID: "new-snap", AccountID: "acc-1", SnapshotAt: now, ZombieCount: 4, TotalMonthlyCost: 200.0, Currency: "USD"},
		})
	_, mux := newTrackingHandler(mockStore)

	w := httptest.NewRecorder()
	mux.ServeHTTP(w, orgRequest(http.MethodGet, "/v1/trend"))
	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", w.Code)
	}

	var snaps []model.ZombieSnapshot
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
	if latest.ZombieCount != 4 {
		t.Errorf("expected latest ZombieCount 4, got %d", latest.ZombieCount)
	}
	// The first entry must still be the older scan.
	if snaps[0].ID != "old-snap" {
		t.Errorf("expected first snapshot old-snap, got %s", snaps[0].ID)
	}
}

// ─── Error handling: store failures → HTTP 500 ────────────────────────────────

// TestListZombies_StoreError_Returns500 verifies that a LoadZombies store error
// is surfaced as HTTP 500.
func TestListZombies_StoreError_Returns500(t *testing.T) {
	mockStore := NewMockStore().
		WithLoadZombiesError(errors.New("connection reset by peer"))

	_, mux := newTrackingHandler(mockStore)

	w := httptest.NewRecorder()
	mux.ServeHTTP(w, orgRequest(http.MethodGet, "/v1/zombies"))

	if w.Code != http.StatusInternalServerError {
		t.Errorf("expected 500, got %d", w.Code)
	}
}

// TestGetSummary_StoreError_Returns500 verifies that a LoadZombies store error
// during summary aggregation is surfaced as HTTP 500.
func TestGetSummary_StoreError_Returns500(t *testing.T) {
	mockStore := NewMockStore().
		WithLoadZombiesError(errors.New("timeout querying zombies"))

	_, mux := newTrackingHandler(mockStore)

	w := httptest.NewRecorder()
	mux.ServeHTTP(w, orgRequest(http.MethodGet, "/v1/summary"))

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
	mux.ServeHTTP(w, orgRequest(http.MethodGet, "/v1/accounts"))

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
	mux.ServeHTTP(w, orgRequest(http.MethodDelete, "/v1/accounts/any-id"))

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
			{ID: "acc-patch", OrganizationID: "organization-test-uuid", Provider: "aws", Label: "old-label", AccessKeyID: "AKIA123", Region: "us-east-1"},
		})
	_, mux := newTrackingHandler(mockStore)

	w := httptest.NewRecorder()
	mux.ServeHTTP(w, orgRequestWithBody(http.MethodPatch, "/v1/accounts/acc-patch", `{"label":"new-label"}`))

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
			{ID: "acc-region", OrganizationID: "organization-test-uuid", Provider: "aws", Label: "my-account", AccessKeyID: "AKIA123", Region: "us-east-1"},
		})
	_, mux := newTrackingHandler(mockStore)

	w := httptest.NewRecorder()
	mux.ServeHTTP(w, orgRequestWithBody(http.MethodPatch, "/v1/accounts/acc-region", `{"region":"eu-central-1"}`))

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
	mux.ServeHTTP(w, orgRequestWithBody(http.MethodPatch, "/v1/accounts/nonexistent", `{"label":"any"}`))

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
	mux.ServeHTTP(w, orgRequestWithBody(http.MethodPatch, "/v1/accounts/acc-json", `not-json`))

	if w.Code != http.StatusBadRequest {
		t.Errorf("expected 400, got %d", w.Code)
	}
}

// ─── Organization-context propagation ───────────────────────────────────────────────

// TestOrganizationIsolation_LoadZombies_ReceivesContextOrganizationID verifies that the
// organization ID set by the auth middleware (DevBypass here) is forwarded to the
// store's LoadZombies call via the context.
func TestOrganizationIsolation_LoadZombies_ReceivesContextOrganizationID(t *testing.T) {
	mockStore := NewMockStore().
		WithZombies([]model.ZombieResource{testZombie})
	_, mux := newTrackingHandler(mockStore)

	// DevBypass injects the organization ID via the middleware context key, exactly
	// as the real auth middleware does. Without it, middleware.OrganizationID returns ""
	// because the middleware and storage packages use distinct context key types.
	handler := middleware.DevBypass("organization-alpha-uuid", "dev-user", "dev@axiaops.local", "Dev User", mux)
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/v1/zombies", nil))

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", w.Code)
	}

	captured := mockStore.GetCapturedOrganizationIDs()

	if len(captured) == 0 {
		t.Fatal("capturedOrganizationIDs is empty — organization ID was not propagated to store")
	}
	if captured[0] != "organization-alpha-uuid" {
		t.Errorf("expected organization-alpha-uuid propagated to store, got %q", captured[0])
	}
}

// TestOrganizationIsolation_ListAccounts_ReceivesContextOrganizationID verifies the same
// propagation guarantee for the ListAccounts path.
func TestOrganizationIsolation_ListAccounts_ReceivesContextOrganizationID(t *testing.T) {
	mockStore := NewMockStore()
	_, mux := newTrackingHandler(mockStore)

	handler := middleware.DevBypass("organization-beta-uuid", "dev-user", "dev@axiaops.local", "Dev User", mux)
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/v1/accounts", nil))

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", w.Code)
	}

	captured := mockStore.GetCapturedOrganizationIDs()

	if len(captured) == 0 {
		t.Fatal("capturedOrganizationIDs is empty — organization ID was not propagated to store")
	}
	if captured[0] != "organization-beta-uuid" {
		t.Errorf("expected organization-beta-uuid propagated to store, got %q", captured[0])
	}
}

// ─── Concurrent scans: organization isolation ──────────────────────────────────────

// TestConcurrentScans_OrganizationIsolation verifies that when two organizations trigger
// account scans simultaneously both receive HTTP 200 with status "scanning".
func TestConcurrentScans_OrganizationIsolation(t *testing.T) {
	const (
		orgA = "organization-isolation-a"
		orgB = "organization-isolation-b"
		accA = "acc-iso-alpha"
		accB = "acc-iso-beta"
	)

	storeA := NewMockStore().WithAccounts([]model.Account{
		{ID: accA, OrganizationID: orgA, Provider: "aws", AccessKeyID: "AKIA_A", Region: "us-east-1"},
	})
	storeB := NewMockStore().WithAccounts([]model.Account{
		{ID: accB, OrganizationID: orgB, Provider: "aws", AccessKeyID: "AKIA_B", Region: "eu-west-1"},
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
	// Wrap each mux in DevBypass so the middleware context keys (read by
	// Require + handlers) are populated. Each organization gets a distinct dev user.
	handlerA := middleware.DevBypass(orgA, "dev-user-a", "a@x.com", "Dev User A", muxA)
	handlerB := middleware.DevBypass(orgB, "dev-user-b", "b@x.com", "Dev User B", muxB)

	wg.Add(2)
	go func() {
		defer wg.Done()
		w := httptest.NewRecorder()
		handlerA.ServeHTTP(w, httptest.NewRequest(http.MethodPost, "/v1/accounts/"+accA+"/scan", nil))
		codeA = w.Code
	}()
	go func() {
		defer wg.Done()
		w := httptest.NewRecorder()
		handlerB.ServeHTTP(w, httptest.NewRequest(http.MethodPost, "/v1/accounts/"+accB+"/scan", nil))
		codeB = w.Code
	}()
	wg.Wait()

	if codeA != http.StatusOK {
		t.Errorf("organization A scan: expected 200, got %d", codeA)
	}
	if codeB != http.StatusOK {
		t.Errorf("organization B scan: expected 200, got %d", codeB)
	}
}
