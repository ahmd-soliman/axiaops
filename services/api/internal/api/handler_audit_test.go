package api_test

import (
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"axiaops.io/api/internal/middleware"
	"axiaops.io/shared/model"
)

// ── Audit emission: dismiss / snooze / revoke ────────────────────────────────

func TestAuditEmission_CreateDismissal_RecordsDismissZombie(t *testing.T) {
	store := NewMockStore().WithZombies([]model.ZombieResource{testZombie})
	handler := middleware.DevBypass(
		"organization-audit", "user-audit", "audit@axiaops.local", newMux(newHandlerWith(store)),
	)

	body := `{
		"account_id":"acc-1","provider":"aws","service":"AmazonEC2",
		"region":"us-east-1","resource_id":"i-audit",
		"action":"dismiss","reason":"intentional"
	}`
	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodPost, "/v1/dismissals", strings.NewReader(body))
	r.Header.Set("Content-Type", "application/json")
	handler.ServeHTTP(w, r)

	if w.Code != http.StatusCreated {
		t.Fatalf("expected 201, got %d — body: %s", w.Code, w.Body.String())
	}

	events := store.GetAuditEvents()
	if len(events) != 1 {
		t.Fatalf("expected 1 audit event, got %d", len(events))
	}
	got := events[0]
	if got.Action != model.AuditActionDismissZombie {
		t.Errorf("action: got %q, want dismiss_zombie", got.Action)
	}
	if got.UserID != "user-audit" {
		t.Errorf("user_id: got %q, want user-audit", got.UserID)
	}
	if got.ActorEmail != "audit@axiaops.local" {
		t.Errorf("actor_email: got %q, want audit@axiaops.local", got.ActorEmail)
	}
	if got.Reason != "intentional" {
		t.Errorf("reason: got %q, want intentional", got.Reason)
	}
	if got.ResourceType != "dismissal" {
		t.Errorf("resource_type: got %q, want dismissal", got.ResourceType)
	}
	if got.Metadata["service"] != "AmazonEC2" {
		t.Errorf("metadata.service: got %v, want AmazonEC2", got.Metadata["service"])
	}
}

func TestAuditEmission_SnoozeDismissal_RecordsSnoozeZombie(t *testing.T) {
	store := NewMockStore()
	handler := middleware.DevBypass(
		"organization-audit", "user-audit", "audit@axiaops.local", newMux(newHandlerWith(store)),
	)

	snoozeUntil := time.Now().Add(24 * time.Hour).UTC().Format(time.RFC3339)
	body := `{
		"account_id":"acc-1","provider":"aws","service":"AmazonRDS",
		"region":"eu-central-1","resource_id":"db-snooze",
		"action":"snooze","reason":"scheduled_deletion","snooze_until":"` + snoozeUntil + `"
	}`
	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodPost, "/v1/dismissals", strings.NewReader(body))
	r.Header.Set("Content-Type", "application/json")
	handler.ServeHTTP(w, r)

	events := store.GetAuditEvents()
	if len(events) != 1 {
		t.Fatalf("expected 1 audit event, got %d", len(events))
	}
	if events[0].Action != model.AuditActionSnoozeZombie {
		t.Errorf("action: got %q, want snooze_zombie", events[0].Action)
	}
}

func TestAuditEmission_RevokeDismissal_RecordsRevoke(t *testing.T) {
	store := NewMockStore().WithDismissals([]model.DismissAction{
		{ID: 7, AccountID: "acc-1", Action: "dismiss", Reason: "intentional"},
	})
	handler := middleware.DevBypass(
		"organization-audit", "user-audit", "audit@axiaops.local", newMux(newHandlerWith(store)),
	)

	w := httptest.NewRecorder()
	handler.ServeHTTP(w, httptest.NewRequest(http.MethodDelete, "/v1/dismissals/7", nil))

	if w.Code != http.StatusNoContent {
		t.Fatalf("expected 204, got %d", w.Code)
	}
	events := store.GetAuditEvents()
	if len(events) != 1 {
		t.Fatalf("expected 1 audit event, got %d", len(events))
	}
	if events[0].Action != model.AuditActionRevokeDismissal {
		t.Errorf("action: got %q, want revoke_dismissal", events[0].Action)
	}
	if events[0].ResourceID != "7" {
		t.Errorf("resource_id: got %q, want 7", events[0].ResourceID)
	}
}

// ── Audit emission: account CRUD + scan ──────────────────────────────────────

func TestAuditEmission_CreateAccount_RecordsAccountConnected(t *testing.T) {
	t.Setenv("ENCRYPTION_KEY", "0000000000000000000000000000000000000000000000000000000000000000")
	store := NewMockStore()
	handler := middleware.DevBypass(
		"organization-audit", "user-audit", "audit@axiaops.local", newMux(newHandlerWith(store)),
	)

	body := `{"provider":"aws","label":"prod","access_key_id":"AKIA","secret_key":"s","region":"eu-central-1"}`
	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodPost, "/v1/accounts", strings.NewReader(body))
	r.Header.Set("Content-Type", "application/json")
	handler.ServeHTTP(w, r)

	if w.Code != http.StatusCreated {
		t.Fatalf("account creation failed with %d: %s", w.Code, w.Body.String())
	}
	events := store.GetAuditEvents()
	if len(events) != 1 || events[0].Action != model.AuditActionAccountConnected {
		t.Fatalf("expected account_connected event, got %+v", events)
	}
	if events[0].Metadata["label"] != "prod" {
		t.Errorf("metadata.label: got %v, want prod", events[0].Metadata["label"])
	}
	if _, present := events[0].Metadata["secret_key"]; present {
		t.Error("metadata must NEVER contain secret_key")
	}
}

func TestAuditEmission_UpdateAccount_RecordsAccountUpdated(t *testing.T) {
	t.Setenv("ENCRYPTION_KEY", "0000000000000000000000000000000000000000000000000000000000000000")
	store := NewMockStore().WithAccounts([]model.Account{{ID: "acc-up", OrganizationID: "organization-audit"}})
	handler := middleware.DevBypass(
		"organization-audit", "user-audit", "audit@axiaops.local", newMux(newHandlerWith(store)),
	)

	body := `{"label":"renamed","region":"us-east-1"}`
	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodPatch, "/v1/accounts/acc-up", strings.NewReader(body))
	r.Header.Set("Content-Type", "application/json")
	handler.ServeHTTP(w, r)

	if w.Code != http.StatusOK {
		t.Fatalf("update failed: %d — %s", w.Code, w.Body.String())
	}
	events := store.GetAuditEvents()
	if len(events) != 1 || events[0].Action != model.AuditActionAccountUpdated {
		t.Fatalf("expected account_updated event, got %+v", events)
	}
	changed, _ := events[0].Metadata["fields_changed"].([]string)
	if len(changed) != 2 {
		t.Errorf("fields_changed: expected 2 entries, got %v", changed)
	}
}

func TestAuditEmission_ScanAccount_RecordsScanTriggered(t *testing.T) {
	store := NewMockStore().WithAccounts([]model.Account{{ID: "acc-scan", OrganizationID: "organization-audit", Label: "prod", Region: "eu-central-1"}})
	handler := middleware.DevBypass(
		"organization-audit", "user-audit", "audit@axiaops.local", newMux(newHandlerWith(store)),
	)

	w := httptest.NewRecorder()
	handler.ServeHTTP(w, httptest.NewRequest(http.MethodPost, "/v1/accounts/acc-scan/scan", nil))

	if w.Code != http.StatusOK {
		t.Fatalf("scan trigger failed: %d — %s", w.Code, w.Body.String())
	}
	events := store.GetAuditEvents()
	if len(events) != 1 || events[0].Action != model.AuditActionScanTriggered {
		t.Fatalf("expected scan_triggered event, got %+v", events)
	}
	if events[0].Metadata["on_demand"] != true {
		t.Errorf("metadata.on_demand: got %v, want true", events[0].Metadata["on_demand"])
	}
	if events[0].ResourceID != "acc-scan" {
		t.Errorf("resource_id: got %q, want acc-scan", events[0].ResourceID)
	}
}

func TestAuditEmission_DeleteAccount_RecordsAccountDeleted(t *testing.T) {
	store := NewMockStore().WithAccounts([]model.Account{{ID: "acc-gone", OrganizationID: "organization-audit"}})
	handler := middleware.DevBypass(
		"organization-audit", "user-audit", "audit@axiaops.local", newMux(newHandlerWith(store)),
	)

	w := httptest.NewRecorder()
	handler.ServeHTTP(w, httptest.NewRequest(http.MethodDelete, "/v1/accounts/acc-gone", nil))

	events := store.GetAuditEvents()
	if len(events) != 1 || events[0].Action != model.AuditActionAccountDeleted {
		t.Fatalf("expected account_deleted event, got %+v", events)
	}
	if events[0].ResourceID != "acc-gone" {
		t.Errorf("resource_id: got %q, want acc-gone", events[0].ResourceID)
	}
}

// ── Best-effort semantics: user op succeeds even if audit write fails ─────────

func TestAudit_WriteFailure_DoesNotBreakUserOperation(t *testing.T) {
	store := NewMockStore().
		WithZombies([]model.ZombieResource{testZombie}).
		WithAuditWriteError(errors.New("simulated audit failure"))
	handler := middleware.DevBypass(
		"organization-audit", "user-audit", "audit@axiaops.local", newMux(newHandlerWith(store)),
	)

	body := `{
		"account_id":"acc-1","provider":"aws","service":"AmazonEC2",
		"region":"us-east-1","resource_id":"i-resilient",
		"action":"dismiss","reason":"intentional"
	}`
	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodPost, "/v1/dismissals", strings.NewReader(body))
	r.Header.Set("Content-Type", "application/json")
	handler.ServeHTTP(w, r)

	if w.Code != http.StatusCreated {
		t.Fatalf("dismiss must succeed even when audit write fails — got %d, body: %s", w.Code, w.Body.String())
	}
	if len(store.GetDismissals()) != 1 {
		t.Errorf("dismissal was not persisted despite successful response")
	}
}

// ── GET /v1/audit endpoint ────────────────────────────────────────────────────

func TestListAuditEvents_ReturnsWrittenEvents(t *testing.T) {
	store := NewMockStore().WithAuditEvents([]model.AuditEvent{
		{ID: 1, Action: model.AuditActionDismissZombie, UserID: "u1", CreatedAt: time.Now()},
		{ID: 2, Action: model.AuditActionAccountConnected, UserID: "u2", CreatedAt: time.Now()},
	})
	handler := middleware.DevBypass(
		"organization-audit", "user-audit", "audit@axiaops.local", newMux(newHandlerWith(store)),
	)

	w := httptest.NewRecorder()
	handler.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/v1/audit", nil))

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d — body: %s", w.Code, w.Body.String())
	}
	var resp struct {
		Events     []model.AuditEvent `json:"events"`
		NextCursor string             `json:"next_cursor"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(resp.Events) != 2 {
		t.Errorf("expected 2 events, got %d", len(resp.Events))
	}
}

func TestListAuditEvents_InvalidAction_Returns400(t *testing.T) {
	store := NewMockStore()
	handler := middleware.DevBypass(
		"organization-audit", "user-audit", "audit@axiaops.local", newMux(newHandlerWith(store)),
	)

	w := httptest.NewRecorder()
	handler.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/v1/audit?action=not_a_real_action", nil))

	if w.Code != http.StatusBadRequest {
		t.Errorf("expected 400 for bogus action, got %d", w.Code)
	}
}

func TestListAuditEvents_InvalidSince_Returns400(t *testing.T) {
	store := NewMockStore()
	handler := middleware.DevBypass(
		"organization-audit", "user-audit", "audit@axiaops.local", newMux(newHandlerWith(store)),
	)

	w := httptest.NewRecorder()
	handler.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/v1/audit?since=not-a-timestamp", nil))

	if w.Code != http.StatusBadRequest {
		t.Errorf("expected 400 for malformed since, got %d", w.Code)
	}
}

func TestListAuditEvents_LimitOutOfRange_Returns400(t *testing.T) {
	store := NewMockStore()
	handler := middleware.DevBypass(
		"organization-audit", "user-audit", "audit@axiaops.local", newMux(newHandlerWith(store)),
	)

	w := httptest.NewRecorder()
	handler.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/v1/audit?limit=9999", nil))

	if w.Code != http.StatusBadRequest {
		t.Errorf("expected 400 for limit > 500, got %d", w.Code)
	}
}
