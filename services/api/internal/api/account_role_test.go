package api_test

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
	"time"

	"axiaops.io/api/internal/api"
	"axiaops.io/shared/httpauth"
	"axiaops.io/shared/model"
)

// roleTestHandler wires a Handler whose ingestion-verify call is rerouted at
// the given httptest.Server. Mirrors the existing testHandler pattern.
func roleTestHandler(store *MockStore, ingestionURL string) (*api.Handler, *http.ServeMux) {
	h := api.New(store, noopQueue()).WithIngestionURL(ingestionURL)
	mux := http.NewServeMux()
	h.Register(mux)
	return h, mux
}

// stubIngestion returns an httptest.Server that replies to /v1/credentials/verify
// with the supplied response body verbatim.
func stubIngestion(t *testing.T, response map[string]any) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/credentials/verify" {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(response)
	}))
	t.Cleanup(srv.Close)
	return srv
}

// ── POST /v1/accounts/draft ───────────────────────────────────────────────────

func TestCreateDraftAccount_ReturnsExternalIDAndPendingStatus(t *testing.T) {
	store := NewMockStore()
	_, mux := roleTestHandler(store, "http://unused")

	body := `{"label":"prod","region":"eu-central-1"}`
	req := orgRequestWithBody(http.MethodPost, "/v1/accounts/draft", body)
	rr := httptest.NewRecorder()
	mux.ServeHTTP(rr, req)

	if rr.Code != http.StatusCreated {
		t.Fatalf("status = %d, want 201; body=%s", rr.Code, rr.Body.String())
	}

	var got model.Account
	if err := json.NewDecoder(rr.Body).Decode(&got); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if got.AuthMethod != model.AuthMethodRole {
		t.Errorf("AuthMethod = %q, want %q", got.AuthMethod, model.AuthMethodRole)
	}
	if got.Status != model.AccountStatusPendingRoleSetup {
		t.Errorf("Status = %q, want %q", got.Status, model.AccountStatusPendingRoleSetup)
	}
	if !strings.HasPrefix(got.ExternalID, "axiaops-ext-") {
		t.Errorf("ExternalID = %q, want axiaops-ext- prefix", got.ExternalID)
	}
	if len(got.ExternalID) < 30 {
		t.Errorf("ExternalID is suspiciously short: %q (length %d)", got.ExternalID, len(got.ExternalID))
	}
	if got.RoleARN != "" {
		t.Errorf("RoleARN should be empty on draft, got %q", got.RoleARN)
	}
}

func TestCreateDraftAccount_RejectsNonAWSProvider(t *testing.T) {
	store := NewMockStore()
	_, mux := roleTestHandler(store, "http://unused")

	body := `{"provider":"azure","label":"x","region":"eastus"}`
	req := orgRequestWithBody(http.MethodPost, "/v1/accounts/draft", body)
	rr := httptest.NewRecorder()
	mux.ServeHTTP(rr, req)

	if rr.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", rr.Code)
	}
}

// ── POST /v1/accounts (legacy access-key) rejects role auth ────────────────────

func TestCreateAccount_RejectsRoleAuth(t *testing.T) {
	store := NewMockStore()
	_, mux := roleTestHandler(store, "http://unused")

	body := `{"provider":"aws","auth_method":"role","label":"x","region":"eu-central-1"}`
	req := orgRequestWithBody(http.MethodPost, "/v1/accounts", body)
	rr := httptest.NewRecorder()
	mux.ServeHTTP(rr, req)

	if rr.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", rr.Code)
	}
	if !strings.Contains(rr.Body.String(), "/v1/accounts/draft") {
		t.Errorf("response should hint at /v1/accounts/draft, got: %s", rr.Body.String())
	}
}

// ── PATCH /v1/accounts/{id} verify success ────────────────────────────────────

func TestUpdateAccount_RoleVerify_Success(t *testing.T) {
	draft := model.Account{
		ID:             "acct-1",
		OrganizationID: "organization-test-uuid",
		Provider:       "aws",
		AuthMethod:     model.AuthMethodRole,
		ExternalID:     "axiaops-ext-secret-value",
		Region:         "eu-central-1",
		Status:         model.AccountStatusPendingRoleSetup,
	}
	store := NewMockStore().WithAccounts([]model.Account{draft})
	srv := stubIngestion(t, map[string]any{
		"ok":         true,
		"account_id": "123456789012",
	})
	_, mux := roleTestHandler(store, srv.URL)

	body := `{"role_arn":"arn:aws:iam::123456789012:role/AxiaOpsIntegrationRole"}`
	req := orgRequestWithBody(http.MethodPatch, "/v1/accounts/acct-1", body)
	rr := httptest.NewRecorder()
	mux.ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		body, _ := io.ReadAll(rr.Body)
		t.Fatalf("status = %d, want 200; body=%s", rr.Code, string(body))
	}

	var got model.Account
	if err := json.NewDecoder(rr.Body).Decode(&got); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if got.Status != model.AccountStatusConnected {
		t.Errorf("Status = %q, want %q", got.Status, model.AccountStatusConnected)
	}
	if got.RoleARN != "arn:aws:iam::123456789012:role/AxiaOpsIntegrationRole" {
		t.Errorf("RoleARN = %q", got.RoleARN)
	}
	if got.AccountID != "123456789012" {
		t.Errorf("AccountID = %q, want 123456789012 (resolved by ingestion's GetCallerIdentity)", got.AccountID)
	}
	if got.ErrorMessage != "" {
		t.Errorf("ErrorMessage should be cleared on success, got %q", got.ErrorMessage)
	}
}

// ── POST /v1/accounts/{id}/scan rejects pending_role_setup ────────────────────

func TestScanAccount_RejectsPendingRoleSetup(t *testing.T) {
	// Drafts have no role_arn yet — running ingestion would fail immediately
	// and leave the row stuck in 'scanning'. Handler must short-circuit with 409.
	draft := model.Account{
		ID:             "acct-draft",
		OrganizationID: "organization-test-uuid",
		Provider:       "aws",
		AuthMethod:     model.AuthMethodRole,
		ExternalID:     "axiaops-ext-x",
		Region:         "eu-central-1",
		Status:         model.AccountStatusPendingRoleSetup,
	}
	store := NewMockStore().WithAccounts([]model.Account{draft})
	_, mux := roleTestHandler(store, "http://unused")

	req := orgRequest(http.MethodPost, "/v1/accounts/acct-draft/scan")
	rr := httptest.NewRecorder()
	mux.ServeHTTP(rr, req)

	if rr.Code != http.StatusConflict {
		t.Fatalf("status = %d, want 409 (pending_role_setup must not be scannable)", rr.Code)
	}
	// The mock's TryMarkAccountScanning must NOT have been called — the
	// handler must short-circuit before touching the scan lock.
	if store.IsAccountScanning("acct-draft") {
		t.Error("draft should not be marked as scanning")
	}
}

// ── PATCH /v1/accounts/{id} ingestion unreachable ────────────────────────────

func TestUpdateAccount_RoleVerify_IngestionUnreachable(t *testing.T) {
	draft := model.Account{
		ID:             "acct-3",
		OrganizationID: "organization-test-uuid",
		Provider:       "aws",
		AuthMethod:     model.AuthMethodRole,
		ExternalID:     "axiaops-ext-x",
		Region:         "eu-central-1",
		Status:         model.AccountStatusPendingRoleSetup,
	}
	store := NewMockStore().WithAccounts([]model.Account{draft})
	// Point ingestionURL at an unreachable port so http.Do returns an error.
	_, mux := roleTestHandler(store, "http://127.0.0.1:1")

	body := `{"role_arn":"arn:aws:iam::123:role/X"}`
	req := orgRequestWithBody(http.MethodPatch, "/v1/accounts/acct-3", body)
	rr := httptest.NewRecorder()
	mux.ServeHTTP(rr, req)

	if rr.Code != http.StatusBadGateway {
		t.Fatalf("status = %d, want 502 (ingestion unreachable)", rr.Code)
	}
}

// ── PATCH /v1/accounts/{id} re-verify of connected account fails ─────────────

func TestUpdateAccount_RoleVerify_ConnectedAccountFailsToError(t *testing.T) {
	// A connected role-based account whose customer-side trust policy was
	// modified must transition to status='error' (not stay 'connected') when
	// re-verification fails — otherwise the dashboard would show a green row
	// while every scan silently breaks.
	connected := model.Account{
		ID:             "acct-4",
		OrganizationID: "organization-test-uuid",
		Provider:       "aws",
		AuthMethod:     model.AuthMethodRole,
		RoleARN:        "arn:aws:iam::123:role/Old",
		ExternalID:     "axiaops-ext-x",
		Region:         "eu-central-1",
		Status:         model.AccountStatusConnected,
		AccountID:      "123456789012",
	}
	store := NewMockStore().WithAccounts([]model.Account{connected})
	srv := stubIngestion(t, map[string]any{
		"ok":     false,
		"code":   "role_assume_failed",
		"reason": "trust_policy_mismatch",
	})
	_, mux := roleTestHandler(store, srv.URL)

	body := `{"role_arn":"arn:aws:iam::123:role/Replacement"}`
	req := orgRequestWithBody(http.MethodPatch, "/v1/accounts/acct-4", body)
	rr := httptest.NewRecorder()
	mux.ServeHTTP(rr, req)

	if rr.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", rr.Code)
	}

	persisted := store.AccountByID("acct-4")
	if persisted == nil {
		t.Fatal("account vanished from store")
	}
	if persisted.Status != model.AccountStatusError {
		t.Errorf("Status = %q, want %q (a connected account that fails re-verify must flip to error)",
			persisted.Status, model.AccountStatusError)
	}
	if !strings.Contains(persisted.ErrorMessage, "trust_policy_mismatch") {
		t.Errorf("ErrorMessage = %q, want it to mention trust_policy_mismatch", persisted.ErrorMessage)
	}
}

// ── PATCH /v1/accounts/{id} verify failure (draft path) ──────────────────────

func TestUpdateAccount_RoleVerify_TrustPolicyMismatch(t *testing.T) {
	draft := model.Account{
		ID:             "acct-2",
		OrganizationID: "organization-test-uuid",
		Provider:       "aws",
		AuthMethod:     model.AuthMethodRole,
		ExternalID:     "axiaops-ext-x",
		Region:         "eu-central-1",
		Status:         model.AccountStatusPendingRoleSetup,
	}
	store := NewMockStore().WithAccounts([]model.Account{draft})
	srv := stubIngestion(t, map[string]any{
		"ok":     false,
		"code":   "role_assume_failed",
		"reason": "trust_policy_mismatch",
		"detail": "User is not authorized to perform: sts:AssumeRole",
	})
	_, mux := roleTestHandler(store, srv.URL)

	body := `{"role_arn":"arn:aws:iam::123:role/Wrong"}`
	req := orgRequestWithBody(http.MethodPatch, "/v1/accounts/acct-2", body)
	rr := httptest.NewRecorder()
	mux.ServeHTTP(rr, req)

	if rr.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400 (verify failure surfaces as 400)", rr.Code)
	}

	var errBody map[string]string
	if err := json.NewDecoder(rr.Body).Decode(&errBody); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if errBody["reason"] != "trust_policy_mismatch" {
		t.Errorf("reason = %q, want trust_policy_mismatch", errBody["reason"])
	}

	// The row in the store must have stayed in pending_role_setup with
	// error_message populated, so the dashboard can render the failure.
	persisted := store.AccountByID("acct-2")
	if persisted == nil {
		t.Fatal("account was deleted from store, expected it to remain in pending_role_setup")
	}
	if persisted.Status != model.AccountStatusPendingRoleSetup {
		t.Errorf("Status = %q, want %q", persisted.Status, model.AccountStatusPendingRoleSetup)
	}
	if !strings.Contains(persisted.ErrorMessage, "trust_policy_mismatch") {
		t.Errorf("ErrorMessage = %q, want it to contain trust_policy_mismatch", persisted.ErrorMessage)
	}
}

// ── HMAC: api → ingestion verify call carries signed headers ─────────────────

// TestUpdateAccount_RoleVerify_SendsHMACHeaders pins the C-1 wiring on the
// api side: when WithIngestionSecret is configured, the outbound
// /v1/credentials/verify call MUST carry both X-AxiaOps-Ingestion-Timestamp
// and X-AxiaOps-Ingestion-Signature, and the signature MUST verify against
// the same secret.
func TestUpdateAccount_RoleVerify_SendsHMACHeaders(t *testing.T) {
	draft := model.Account{
		ID:             "acct-hmac",
		OrganizationID: "organization-test-uuid",
		Provider:       "aws",
		AuthMethod:     model.AuthMethodRole,
		ExternalID:     "axops-ext-hmac",
		Region:         "eu-central-1",
		Status:         model.AccountStatusPendingRoleSetup,
	}
	store := NewMockStore().WithAccounts([]model.Account{draft})

	secret := []byte("0123456789abcdef0123456789abcdef") // 32 bytes
	var (
		gotTimestamp string
		gotSignature string
		gotBody      []byte
	)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotTimestamp = r.Header.Get(httpauth.HeaderTimestamp)
		gotSignature = r.Header.Get(httpauth.HeaderSignature)
		gotBody, _ = io.ReadAll(r.Body)
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{"ok": true, "account_id": "123456789012"})
	}))
	defer srv.Close()

	h := api.New(store, noopQueue()).
		WithIngestionURL(srv.URL).
		WithIngestionSecret(secret)
	mux := http.NewServeMux()
	h.Register(mux)

	body := `{"role_arn":"arn:aws:iam::123456789012:role/AxiaOpsIntegrationRole"}`
	req := orgRequestWithBody(http.MethodPatch, "/v1/accounts/acct-hmac", body)
	rr := httptest.NewRecorder()
	mux.ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body=%s", rr.Code, rr.Body.String())
	}
	if gotTimestamp == "" || gotSignature == "" {
		t.Fatalf("expected signed headers, got timestamp=%q signature=%q",
			gotTimestamp, gotSignature)
	}
	tsSecs, err := strconv.ParseInt(gotTimestamp, 10, 64)
	if err != nil {
		t.Fatalf("timestamp not parseable: %v", err)
	}
	// Sanity: timestamp is recent (within ±60s of now).
	if dt := time.Since(time.Unix(tsSecs, 0)).Seconds(); dt < -60 || dt > 60 {
		t.Fatalf("timestamp drift %.0fs (now=%d, header=%d)", dt, time.Now().Unix(), tsSecs)
	}
	if vErr := httpauth.Verify(secret, time.Minute, time.Now,
		gotTimestamp, gotSignature,
		http.MethodPost, "/v1/credentials/verify", gotBody); vErr != nil {
		t.Fatalf("server-side Verify failed: %v", vErr)
	}
}

// TestUpdateAccount_RoleVerify_NoSecret_NoHeaders pins the DEV_MODE shape:
// when no secret is configured, the outbound call must NOT carry HMAC
// headers (so the receiving DEV_MODE ingestion's passthrough doesn't
// trigger its one-shot warning erroneously).
func TestUpdateAccount_RoleVerify_NoSecret_NoHeaders(t *testing.T) {
	draft := model.Account{
		ID:             "acct-no-secret",
		OrganizationID: "organization-test-uuid",
		Provider:       "aws",
		AuthMethod:     model.AuthMethodRole,
		ExternalID:     "axops-ext-x",
		Region:         "eu-central-1",
		Status:         model.AccountStatusPendingRoleSetup,
	}
	store := NewMockStore().WithAccounts([]model.Account{draft})

	var (
		gotTimestamp string
		gotSignature string
	)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotTimestamp = r.Header.Get(httpauth.HeaderTimestamp)
		gotSignature = r.Header.Get(httpauth.HeaderSignature)
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{"ok": true, "account_id": "1"})
	}))
	defer srv.Close()

	h := api.New(store, noopQueue()).WithIngestionURL(srv.URL) // no WithIngestionSecret
	mux := http.NewServeMux()
	h.Register(mux)

	req := orgRequestWithBody(http.MethodPatch, "/v1/accounts/acct-no-secret",
		`{"role_arn":"arn:aws:iam::1:role/X"}`)
	rr := httptest.NewRecorder()
	mux.ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body=%s", rr.Code, rr.Body.String())
	}
	if gotTimestamp != "" || gotSignature != "" {
		t.Fatalf("expected no auth headers in DEV_MODE, got timestamp=%q signature=%q",
			gotTimestamp, gotSignature)
	}
}
