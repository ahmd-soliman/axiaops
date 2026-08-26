package middleware_test

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/prometheus/client_golang/prometheus/testutil"

	"axiaops.io/api/internal/auth"
	"axiaops.io/api/internal/middleware"
	"axiaops.io/shared/observability"
)

// fakeProvider returns a fixed Identity (or an error). Tests inject this
// instead of the real auth.NativeProvider so they don't need a Manager,
// store, or cookie machinery.
type fakeProvider struct {
	id  auth.Identity
	err error
}

func (f fakeProvider) Authenticate(*http.Request) (auth.Identity, error) {
	return f.id, f.err
}

// terminalHandler captures the per-request context that the middleware
// passed downstream so tests can assert on it.
type terminalHandler struct {
	got struct {
		organizationID string
		userID         string
		userEmail      string
		role           string
		authMode       string
	}
	called bool
}

func (h *terminalHandler) ServeHTTP(_ http.ResponseWriter, r *http.Request) {
	h.called = true
	h.got.organizationID = middleware.OrganizationID(r.Context())
	h.got.userID = middleware.UserID(r.Context())
	h.got.userEmail = middleware.UserEmail(r.Context())
	h.got.role = middleware.Role(r.Context())
	h.got.authMode = middleware.AuthMode(r.Context())
}

func TestWrapNativeHappyPath(t *testing.T) {
	t.Parallel()
	prov := fakeProvider{
		id: auth.Identity{
			UserID:         "user-1",
			OrganizationID: "org-1",
			Role:           "admin",
			AuthMode:       "password",
			Email:          "user@example.com",
		},
	}
	term := &terminalHandler{}
	h := middleware.WrapNative(prov, term)

	w := httptest.NewRecorder()
	r := httptest.NewRequest("GET", "/v1/zombies", nil)
	h.ServeHTTP(w, r)

	if !term.called {
		t.Fatal("downstream handler not invoked on successful auth")
	}
	if term.got.organizationID != "org-1" {
		t.Errorf("organization_id = %q; want org-1", term.got.organizationID)
	}
	if term.got.userID != "user-1" {
		t.Errorf("user_id = %q; want user-1", term.got.userID)
	}
	if term.got.userEmail != "user@example.com" {
		t.Errorf("user_email = %q; want user@example.com", term.got.userEmail)
	}
	if term.got.role != "admin" {
		t.Errorf("role = %q; want admin", term.got.role)
	}
	if term.got.authMode != "password" {
		t.Errorf("auth_mode = %q; want password", term.got.authMode)
	}
	if w.Result().StatusCode != http.StatusOK {
		t.Errorf("status = %d; want 200", w.Result().StatusCode)
	}
}

func TestWrapNativeAuthFailureReturns401(t *testing.T) {
	t.Parallel()
	prov := fakeProvider{err: auth.ErrUnauthenticated}
	term := &terminalHandler{}
	h := middleware.WrapNative(prov, term)

	w := httptest.NewRecorder()
	r := httptest.NewRequest("GET", "/v1/zombies", nil)
	h.ServeHTTP(w, r)

	if term.called {
		t.Fatal("downstream handler must not be called on auth failure")
	}
	if w.Result().StatusCode != http.StatusUnauthorized {
		t.Errorf("status = %d; want 401", w.Result().StatusCode)
	}
}

func TestWrapNativeBypassesPublicPaths(t *testing.T) {
	t.Parallel()
	for _, path := range []string{"/health", "/livez", "/readyz", "/metrics"} {
		t.Run(path, func(t *testing.T) {
			t.Parallel()
			prov := fakeProvider{err: auth.ErrUnauthenticated} // would 401 if invoked
			term := &terminalHandler{}
			h := middleware.WrapNative(prov, term)

			w := httptest.NewRecorder()
			r := httptest.NewRequest("GET", path, nil)
			h.ServeHTTP(w, r)
			if !term.called {
				t.Errorf("public path %q must reach downstream without auth", path)
			}
			if w.Result().StatusCode != http.StatusOK {
				t.Errorf("public path %q status = %d; want 200", path, w.Result().StatusCode)
			}
		})
	}
}

func TestWrapNativeBypassesOptions(t *testing.T) {
	t.Parallel()
	prov := fakeProvider{err: auth.ErrUnauthenticated}
	term := &terminalHandler{}
	h := middleware.WrapNative(prov, term)

	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodOptions, "/v1/zombies", nil)
	h.ServeHTTP(w, r)

	if !term.called {
		t.Fatal("OPTIONS preflight must reach downstream without auth")
	}
}

func TestWrapNativeNilProviderPanics(t *testing.T) {
	t.Parallel()
	defer func() {
		if recover() == nil {
			t.Fatal("WrapNative(nil, ...) must panic")
		}
	}()
	middleware.WrapNative(nil, &terminalHandler{})
}

func TestWrapNativeTelemetryEmitsTierLabel(t *testing.T) {
	// Verifies the AuthMode → tier mapping: password/sso/bootstrap →
	// "native"; empty or any unrecognised mode → "unknown" (the metric
	// label diverges from /v1/me here so a Provider returning an Identity
	// with no AuthMode surfaces in metrics rather than silently bleeding
	// into native). Reads the singleton counter via testutil.ToFloat64 —
	// not parallel-safe with other tests that increment the same series,
	// so kept serial.
	cases := []struct {
		name     string
		authMode string
		wantTier string
	}{
		{"password→native", "password", "native"},
		{"sso→native", "sso", "native"},
		{"bootstrap→native", "bootstrap", "native"},
		{"unrecognised→unknown", "future-provider-mode", "unknown"},
		{"empty→unknown", "", "unknown"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			counter := observability.Global.AuthProviderActive.WithLabelValues(tc.wantTier)
			before := testutil.ToFloat64(counter)

			prov := fakeProvider{id: auth.Identity{
				UserID: "u", OrganizationID: "o", Role: "viewer", AuthMode: tc.authMode,
			}}
			h := middleware.WrapNative(prov, &terminalHandler{})
			w := httptest.NewRecorder()
			r := httptest.NewRequest("GET", "/v1/zombies", nil)
			h.ServeHTTP(w, r)

			after := testutil.ToFloat64(counter)
			if after-before != 1 {
				t.Errorf("counter{provider=%q} delta = %v; want 1", tc.wantTier, after-before)
			}
		})
	}
}
