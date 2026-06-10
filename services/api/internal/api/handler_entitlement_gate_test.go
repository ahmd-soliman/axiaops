package api_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"axiaops.io/api/internal/api"
	"axiaops.io/shared/model"
	"axiaops.io/shared/storage"
)

// stubEntitlementResolver drives the SaaS scan-gate path of scanAccount without
// a DB. It satisfies entitlement.Resolver structurally.
type stubEntitlementResolver struct {
	ent model.Entitlement
	err error
}

func (s stubEntitlementResolver) GetEntitlement(context.Context, string) (model.Entitlement, error) {
	return s.ent, s.err
}

// entitlementGateMux builds a handler wired with the entitlement resolver (the
// SaaS posture: cmd/api-saashosted sets this; the license is bypassed). With a
// resolver present, scanAccount gates on entitlement regardless of license state.
func entitlementGateMux(resolver stubEntitlementResolver) *http.ServeMux {
	mux := http.NewServeMux()
	mockStore := NewMockStore().WithAccounts([]model.Account{
		{ID: "acc-99", OrganizationID: "organization-test-uuid", Provider: "aws", AccessKeyID: "AKIA", Region: "eu-west-1"},
	})
	api.New(mockStore, &captureQueueLC{}).
		WithEntitlementResolver(resolver, 21*24*time.Hour).
		Register(mux)
	return mux
}

func TestScanAccount_EntitlementGate(t *testing.T) {
	period := time.Now().Add(30 * 24 * time.Hour)
	cases := []struct {
		name     string
		resolver stubEntitlementResolver
		wantCode int
		wantErr  string // "" when allowed
	}{
		{"active allows", stubEntitlementResolver{ent: model.Entitlement{Status: model.StatusActive}}, http.StatusOK, ""},
		{"trialing allows", stubEntitlementResolver{ent: model.Entitlement{Status: model.StatusTrialing}}, http.StatusOK, ""},
		{"suspended blocks", stubEntitlementResolver{ent: model.Entitlement{Status: model.StatusSuspended}}, http.StatusForbidden, "not_entitled"},
		{"canceled blocks", stubEntitlementResolver{ent: model.Entitlement{Status: model.StatusCanceled}}, http.StatusForbidden, "not_entitled"},
		{"past_due in grace allows", stubEntitlementResolver{ent: model.Entitlement{Status: model.StatusPastDue, CurrentPeriodEnd: &period}}, http.StatusOK, ""},
		{"missing row blocks (fail closed)", stubEntitlementResolver{err: storage.ErrEntitlementNotFound}, http.StatusForbidden, "not_entitled"},
		{"lookup error blocks (fail closed)", stubEntitlementResolver{err: context.DeadlineExceeded}, http.StatusForbidden, "entitlement_lookup_error"},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			mux := entitlementGateMux(tc.resolver)
			w := httptest.NewRecorder()
			mux.ServeHTTP(w, orgRequest(http.MethodPost, "/v1/accounts/acc-99/scan"))

			if w.Code != tc.wantCode {
				t.Fatalf("status = %d, want %d — body: %s", w.Code, tc.wantCode, w.Body.String())
			}
			if tc.wantErr == "" {
				return
			}
			if ct := w.Header().Get("Content-Type"); ct != "application/json" {
				t.Errorf("Content-Type = %q, want application/json", ct)
			}
			var body map[string]string
			if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
				t.Fatalf("body not JSON: %v — %s", err, w.Body.String())
			}
			if body["error"] != tc.wantErr {
				t.Errorf("error = %q, want %q", body["error"], tc.wantErr)
			}
		})
	}
}
