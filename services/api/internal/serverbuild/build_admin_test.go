package serverbuild_test

import (
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"axiaops.io/api/internal/serverbuild"
	"axiaops.io/api/internal/staff"
	"axiaops.io/shared/cache"
)

// stubStaffProvider always rejects — enough to prove WrapStaff gates the
// surface. The compose smoke test only needs the chain to be wired, not a real
// login.
type stubStaffProvider struct{}

func (stubStaffProvider) Authenticate(*http.Request) (staff.Identity, error) {
	return staff.Identity{}, staff.ErrUnauthenticated
}

func TestComposeAdminServer_MissingDeps(t *testing.T) {
	if _, err := serverbuild.ComposeAdminServer(serverbuild.AdminConfig{}, serverbuild.AdminDeps{}); err == nil {
		t.Fatal("expected error when Store is nil")
	}
}

func TestComposeAdminServer_GatesProtectedRoutes(t *testing.T) {
	c := cache.New("")
	sessions := staff.NewSessionManager(c, time.Hour)
	handler, err := serverbuild.ComposeAdminServer(serverbuild.AdminConfig{}, serverbuild.AdminDeps{
		Store:           &stubStore{},
		Cache:           c,
		StaffProvider:   stubStaffProvider{},
		StaffSessions:   sessions,
		MetricsRegistry: serverbuild.NewDefaultMetrics(),
	})
	if err != nil {
		t.Fatalf("compose: %v", err)
	}

	// Public infra endpoint reachable without auth.
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/livez", nil))
	if rec.Code != http.StatusOK {
		t.Errorf("/livez want 200, got %d", rec.Code)
	}

	// Protected route gated by WrapStaff → 401.
	rec = httptest.NewRecorder()
	handler.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/admin/tenants", nil))
	if rec.Code != http.StatusUnauthorized {
		t.Errorf("/admin/tenants want 401, got %d", rec.Code)
	}
}
