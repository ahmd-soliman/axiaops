package api_test

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"axiaops.io/api/internal/api"
	"axiaops.io/shared/model"
)

// expHandler builds a Handler around the given store. Mirrors delHandler.
func expHandler(store *MockStore) *http.ServeMux {
	h := api.New(store, noopQueue())
	mux := http.NewServeMux()
	h.Register(mux)
	return mux
}

func TestExport_Owner_200_HappyPath(t *testing.T) {
	now := time.Now().UTC()
	store := NewMockStore().
		WithRole("owner").
		WithMemberships([]model.MembershipWithUser{
			{
				Membership: model.Membership{
					ID: "m-1", OrganizationID: "tenant-me", UserID: "user-me", Role: "owner",
					CreatedAt: now, UpdatedAt: now,
				},
				Email: "me@example.com",
			},
		}).
		WithAccounts([]model.Account{
			{ID: "acct-1", OrganizationID: "tenant-me", Provider: "aws", Label: "prod", AccessKeyID: "AKIA...", SecretEncrypted: "ENCRYPTED-DO-NOT-LEAK", Region: "eu-central-1"},
		}).
		WithAuditEvents([]model.AuditEvent{
			{ID: 1, OrganizationID: "tenant-me", UserID: "user-me", ActorEmail: "me@example.com", Action: model.AuditActionAccountConnected, CreatedAt: now},
		})

	mux := expHandler(store)
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, meRequest(http.MethodGet, "/v1/export"))

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d (body: %s)", w.Code, w.Body.String())
	}

	if ct := w.Header().Get("Content-Type"); ct != "application/json" {
		t.Errorf("Content-Type: want application/json, got %q", ct)
	}
	if cd := w.Header().Get("Content-Disposition"); !strings.HasPrefix(cd, "attachment; filename=") {
		t.Errorf("Content-Disposition missing or wrong: %q", cd)
	}

	body := w.Body.String()
	// SecretEncrypted is `json:"-"` on model.Account — but if anyone ever
	// removes the tag, the assertion below catches it before the next release.
	if strings.Contains(body, "ENCRYPTED-DO-NOT-LEAK") {
		t.Fatalf("export leaked encrypted secret in body")
	}

	var doc map[string]any
	if err := json.Unmarshal(w.Body.Bytes(), &doc); err != nil {
		t.Fatalf("decode: %v\nbody: %s", err, body)
	}

	if doc["schema_version"] != "1" {
		t.Errorf("schema_version: want \"1\", got %v", doc["schema_version"])
	}
	if doc["tenant_id"] != "tenant-me" {
		t.Errorf("tenant_id: want tenant-me, got %v", doc["tenant_id"])
	}

	for _, key := range []string{"members", "accounts", "resources", "zombies", "cost_records", "snapshots", "active_dismissals", "audit_log"} {
		if _, ok := doc[key]; !ok {
			t.Errorf("missing top-level key %q", key)
		}
	}

	members, _ := doc["members"].([]any)
	if len(members) != 1 {
		t.Fatalf("want 1 member, got %d", len(members))
	}
	m0, _ := members[0].(map[string]any)
	if m0["email"] != "me@example.com" || m0["role"] != "owner" {
		t.Errorf("member shape wrong: %+v", m0)
	}

	accts, _ := doc["accounts"].([]any)
	if len(accts) != 1 {
		t.Fatalf("want 1 account, got %d", len(accts))
	}
	if a0, _ := accts[0].(map[string]any); a0["secret_encrypted"] != nil {
		t.Errorf("secret_encrypted leaked: %v", a0["secret_encrypted"])
	}

	auditRows, _ := doc["audit_log"].([]any)
	if len(auditRows) != 1 {
		t.Fatalf("want 1 audit row in payload, got %d", len(auditRows))
	}

	// Audit-log side-effect: an `data_exported` row should have been written
	// for this request, with row counts in the metadata.
	gotEvents := store.GetAuditEvents()
	var found *model.AuditEvent
	for i := range gotEvents {
		if gotEvents[i].Action == model.AuditActionDataExported {
			found = &gotEvents[i]
			break
		}
	}
	if found == nil {
		t.Fatalf("expected a %q audit row, got %d total events: %+v",
			model.AuditActionDataExported, len(gotEvents), gotEvents)
	}
	if found.ResourceType != "tenant" || found.ResourceID != "tenant-me" {
		t.Errorf("audit row resource fields wrong: type=%q id=%q", found.ResourceType, found.ResourceID)
	}
	if found.Metadata == nil || found.Metadata["accounts"] == nil {
		t.Errorf("audit row metadata missing row counts: %+v", found.Metadata)
	}
}

func TestExport_NonOwnerRoles_403(t *testing.T) {
	for _, role := range []string{"admin", "member", "viewer", ""} {
		t.Run("role="+role, func(t *testing.T) {
			store := NewMockStore().WithRole(role)
			mux := expHandler(store)
			w := httptest.NewRecorder()
			mux.ServeHTTP(w, meRequest(http.MethodGet, "/v1/export"))

			if w.Code != http.StatusForbidden {
				t.Errorf("role=%q: expected 403, got %d (body: %s)", role, w.Code, w.Body.String())
			}
		})
	}
}

func TestExport_NoIdentity_403(t *testing.T) {
	store := NewMockStore().WithRole("owner")
	mux := expHandler(store)

	// Plain request — no DevBypass, so middleware.Require rejects on missing
	// identity before the handler runs.
	req := httptest.NewRequest(http.MethodGet, "/v1/export", nil)
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)

	if w.Code != http.StatusForbidden {
		t.Fatalf("expected 403 without identity, got %d", w.Code)
	}
}

func TestExport_AuditLog_PagesPastSinglePage(t *testing.T) {
	// Seed 1500 audit events (3 × the 500-row page size) so the export must
	// loop through cursor pagination at least three times. Distinct IDs +
	// monotonically increasing CreatedAt make the DESC-sorted predicate
	// strictly orderable, mirroring postgres.
	const total = 1500
	events := make([]model.AuditEvent, 0, total)
	base := time.Now().UTC().Add(-time.Hour)
	for i := 0; i < total; i++ {
		events = append(events, model.AuditEvent{
			ID:             int64(i + 1),
			OrganizationID: "tenant-me",
			UserID:         "user-me",
			ActorEmail:     "me@example.com",
			Action:         model.AuditActionAccountConnected,
			CreatedAt:      base.Add(time.Duration(i) * time.Millisecond),
		})
	}

	store := NewMockStore().WithRole("owner").WithAuditEvents(events)
	mux := expHandler(store)
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, meRequest(http.MethodGet, "/v1/export"))

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d (body bytes: %d)", w.Code, w.Body.Len())
	}

	var doc struct {
		AuditLog          []model.AuditEvent `json:"audit_log"`
		AuditLogTruncated bool               `json:"audit_log_truncated"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &doc); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(doc.AuditLog) != total {
		t.Fatalf("audit_log length: want %d (proves pagination loop terminated cleanly), got %d", total, len(doc.AuditLog))
	}
	if doc.AuditLogTruncated {
		t.Errorf("audit_log_truncated should be false at %d rows (well under the 100k cap)", total)
	}

	// IDs should be unique — duplication would mean the cursor isn't advancing
	// and the loop returned the same page repeatedly.
	seen := make(map[int64]bool, total)
	for _, e := range doc.AuditLog {
		if seen[e.ID] {
			t.Fatalf("duplicate audit ID %d in export — pagination cursor not advancing", e.ID)
		}
		seen[e.ID] = true
	}

	// Chronological (oldest-first) order is the export contract — a privacy
	// lead reads the file end-to-end. Pagination collects newest-first; the
	// handler reverses. The seed assigned increasing IDs to increasing
	// timestamps, so ascending IDs prove ascending CreatedAt.
	for i := 1; i < len(doc.AuditLog); i++ {
		if doc.AuditLog[i].ID <= doc.AuditLog[i-1].ID {
			t.Fatalf("audit_log not chronological at index %d: id %d follows id %d",
				i, doc.AuditLog[i].ID, doc.AuditLog[i-1].ID)
		}
	}
}

func TestExport_EmptyTenant_200(t *testing.T) {
	// Owner with nothing in any table — every collection should encode as [],
	// not null, so consumers can iterate without nil-checks.
	store := NewMockStore().WithRole("owner")
	mux := expHandler(store)
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, meRequest(http.MethodGet, "/v1/export"))

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d (body: %s)", w.Code, w.Body.String())
	}

	body := w.Body.String()
	for _, k := range []string{`"members": []`, `"accounts": []`, `"audit_log": []`} {
		if !strings.Contains(body, k) {
			t.Errorf("body should contain %q to keep consumers null-safe; body: %s", k, body)
		}
	}
}
