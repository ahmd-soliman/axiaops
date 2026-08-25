package serverbuild_test

// build_test.go — drop-in smoke test for the pluggable extension seams (D11
// / plan §4.8.6 acceptance).
//
// The test boots ComposeServer with mock impls of all four seams and
// asserts an HTTP request gets a sane response. It proves the seams hold —
// a future implementation swap for any of Store/AuthProvider/Discoverer/
// Connector only needs to satisfy the same four interfaces.
//
// What the test does NOT do (deliberately):
//   - Spin up Postgres / Redis. The Store/Cache fields take any impl;
//     mocks with nil-method bodies are sufficient for the seam check.
//   - Exercise auth flows. The mock Provider returns a fixed Identity;
//     proving the chain wires ≠ proving the chain authenticates.
//   - Run tickers. ComposeServer doesn't start them — that's StartTickers'
//     job and the test passes a zero-value TickerOptions.
//
// If you're adding a new seam, add a mock here and a Deps field assertion
// to TestComposeServer_AcceptsAllSeamMocks. The whole point of one
// drop-in test is to catch a regression where someone tightens a seam
// from "interface" to "concrete type".

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"axiaops.io/api/internal/auth"
	"axiaops.io/api/internal/middleware"
	"axiaops.io/api/internal/serverbuild"
	"axiaops.io/api/internal/sso"
	"axiaops.io/shared/cache"
	"axiaops.io/shared/model"
	"axiaops.io/shared/storage"
)

// ─── seam mocks ─────────────────────────────────────────────────────────────

// stubStore satisfies storage.Store via the embedded-nil-interface trick.
// ComposeServer's no-network path only touches a handful of Store methods
// (init time + the one endpoint we hit). Any unexpected call panics, which
// is the right posture: a future change that adds a new Store call should
// either provide a stub or use a richer fixture, not silently NPE.
type stubStore struct {
	storage.Store
}

// stubProvider always returns a fixed Identity. The smoke test doesn't
// exercise the auth path; the mere presence of an auth.Provider impl is
// what proves the seam holds.
type stubProvider struct{ id auth.Identity }

func (p *stubProvider) Authenticate(_ *http.Request) (auth.Identity, error) {
	return p.id, nil
}

// stubDiscoverer always reports has_sso=false regardless of input. The
// canonical pre-auth endpoint /v1/sso/discover is what the smoke test
// pings — exercising the Discoverer seam end-to-end.
type stubDiscoverer struct{}

func (d *stubDiscoverer) Discover(_ context.Context, _ string) (sso.DiscoverResult, error) {
	return sso.DiscoverResult{HasSSO: false}, nil
}

// stubConnector returns sentinel errors for every method. The smoke test
// doesn't drive the Connector seam — the Discoverer endpoint covers
// pre-auth surface and the auth-protected Connector CRUD endpoints would
// require driving the auth chain. Presence of the impl is the seam check.
type stubConnector struct{}

func (c *stubConnector) Save(_ context.Context, _ model.SSOConnection) (model.SSOConnection, error) {
	return model.SSOConnection{}, errors.New("stubConnector: Save not implemented in smoke test")
}
func (c *stubConnector) Delete(_ context.Context, _ string) error {
	return errors.New("stubConnector: Delete not implemented in smoke test")
}
func (c *stubConnector) Get(_ context.Context, _ string) (model.SSOConnection, error) {
	return model.SSOConnection{}, errors.New("stubConnector: Get not implemented in smoke test")
}
func (c *stubConnector) List(_ context.Context) ([]model.SSOConnection, error) {
	return nil, errors.New("stubConnector: List not implemented in smoke test")
}
func (c *stubConnector) Test(_ context.Context, _ string) (sso.TestResult, error) {
	return sso.TestResult{}, errors.New("stubConnector: Test not implemented in smoke test")
}

// nilEnforcementResolver: passing nil to ComposeServer disables the
// EnforceSSO middleware (composition root staging affordance). The smoke
// test exercises this path — we don't want the middleware short-
// circuiting requests on a stub Store.

// ─── tests ──────────────────────────────────────────────────────────────────

// TestComposeServer_AcceptsAllSeamMocks pins the §4.8.6 D11 acceptance:
// ComposeServer compiles AND runs against mock implementations of all
// four SaaS-extension seams (Store, AuthProvider, Discoverer, Connector).
// A single smoke request against /v1/sso/discover proves the chain
// serves traffic.
//
// If a future refactor tightens any seam from interface to concrete
// type, this test stops compiling — surfacing the regression before the
// SaaS reactivation slice.
func TestComposeServer_AcceptsAllSeamMocks(t *testing.T) {
	cfg := serverbuild.Config{
		Addr:       ":0", // unused; we don't ListenAndServe
		PublicHost: "https://app.test",
		// DevMode=true skips the AuthProvider/SessionManager/SSO* deps.
		// The native-auth branch is exercised in a separate test below.
		DevMode:           true,
		DevOrganizationID: "org-test",
		DevUserID:         "user-test",
		DevUserEmail:      "user@test",
	}
	deps := serverbuild.Deps{
		Store: &stubStore{},
		Cache: cache.New(""), // in-memory backend
		// Queue is nil — handlers that need it (POST /v1/accounts/{id}/scan)
		// aren't exercised by the smoke request.
		Queue:      nil,
		Discoverer: &stubDiscoverer{},
		Connector:  &stubConnector{},
		// MetricsRegistry isolated so this test doesn't fight the
		// global Prometheus registry with TestComposeServer_NativeAuthMode below.
		MetricsRegistry: serverbuild.NewDefaultMetrics(),
	}

	handler, err := serverbuild.ComposeServer(cfg, deps)
	if err != nil {
		t.Fatalf("ComposeServer: %v", err)
	}
	if handler == nil {
		t.Fatal("ComposeServer returned nil handler with nil error")
	}

	// Smoke request: GET /v1/sso/discover is pre-auth and constant-shape.
	// A 200 response proves the request-id + dev-bypass + rate-limit + CORS
	// chain composed correctly AND the Discoverer mock was consulted.
	req := httptest.NewRequest(http.MethodGet, "/v1/sso/discover?email=alice@example.com", nil)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("smoke request status: got %d want 200; body=%q", rec.Code, rec.Body.String())
	}
	if ct := rec.Header().Get("Content-Type"); ct != "application/json" {
		t.Errorf("smoke request content-type: got %q want application/json", ct)
	}
	if !containsHasSSO(rec.Body.String()) {
		t.Errorf("smoke response body missing has_sso key: %q", rec.Body.String())
	}
}

// TestComposeServer_MetricsExposesSharedRegistry pins the registry-merge
// fix flagged on MR !85. The shared observability package binds Global.* to
// a package-private prometheus.Registry; if /metrics is wired to
// promhttp.Handler() (default registry only) every metric in
// services/shared/observability/metrics.go vanishes from the scrape — the
// preview-env regression that surfaced license_state_info, auth_provider_*,
// http_*, db_* all silently missing. ComposeServer must use
// promhttp.HandlerFor with prometheus.Gatherers{DefaultGatherer, Registry()}.
//
// Failure mode this catches: a future refactor that swaps the merged handler
// back to promhttp.Handler() — the deletion-readiness query at plan §4.5
// line 361 (`time() - axiaops_auth_provider_last_seen_seconds{provider=...}`)
// would silently break and the alert runbook would go dark.
func TestComposeServer_MetricsExposesSharedRegistry(t *testing.T) {
	cfg := serverbuild.Config{
		Addr:              ":0",
		PublicHost:        "https://app.test",
		DevMode:           true,
		DevOrganizationID: "org-test",
		DevUserID:         "user-test",
		DevUserEmail:      "user@test",
	}
	deps := serverbuild.Deps{
		Store:           &stubStore{},
		Cache:           cache.New(""),
		Discoverer:      &stubDiscoverer{},
		Connector:       &stubConnector{},
		MetricsRegistry: serverbuild.NewDefaultMetrics(),
	}

	handler, err := serverbuild.ComposeServer(cfg, deps)
	if err != nil {
		t.Fatalf("ComposeServer: %v", err)
	}

	req := httptest.NewRequest(http.MethodGet, "/metrics", nil)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("/metrics status: got %d want 200; body=%q", rec.Code, rec.Body.String())
	}
	body := rec.Body.String()

	// Shared-registry sentinels: bare (non-Vec) metrics, which Prometheus
	// emits at zero before any sample is observed. Vec metrics (e.g.
	// auth_provider_active) only emit after a label-set is observed — useless
	// as a registry-wiring canary because they're silent on a freshly-booted
	// process. Pinning one bare metric per surface keeps the failure message
	// specific when the merge regresses.
	wantShared := []string{
		"axiaops_http_requests_total",        // §2.6 HTTP observability (Counter)
		"axiaops_db_connections_active",      // §2.6 DB observability (Gauge)
		"axiaops_application_uptime_seconds", // application observability (Gauge)
		"axiaops_session_cache_errors_total", // session cache observability (Counter)
	}
	for _, name := range wantShared {
		if !strings.Contains(body, name) {
			t.Errorf("/metrics missing shared-registry metric %q — promhttp.HandlerFor merge regressed", name)
		}
	}
}

// TestComposeServer_NativeAuthMode exercises the !DevMode branch,
// requiring AuthProvider + SessionManager + SSOValidator + SSOStateStore.
// Proves the (4-seam + 3-supporting-deps) construction holds — a strict
// upgrade over the DevMode test above which short-circuits a lot of paths.
func TestComposeServer_NativeAuthMode(t *testing.T) {
	t.Setenv("ENCRYPTION_KEY", "0000000000000000000000000000000000000000000000000000000000000000")

	cache := cache.New("")
	store := &stubStore{}
	cfg := serverbuild.Config{
		Addr:            ":0",
		PublicHost:      "https://app.test",
		DevMode:         false,
		RedisConfigured: false,
	}
	deps := serverbuild.Deps{
		Store:        store,
		Cache:        cache,
		AuthProvider: &stubProvider{id: auth.Identity{UserID: "u1", OrganizationID: "o1", AuthMode: string(model.AuthModePassword), Role: "owner"}},
		Discoverer:   &stubDiscoverer{},
		Connector:    &stubConnector{},
		// Native-auth-required deps:
		SessionManager: auth.NewManager(store, auth.NewSessionCache(cache), auth.Config{
			TTL:             time.Hour,
			SessionsPerUser: 10,
		}),
		CookieConfig:        auth.NewCookieConfig(),
		EnforcementResolver: nil, // nil disables EnforceSSO; covered by sso_enforcement_test
		SSOValidator:        sso.NewValidator(cache),
		SSOStateStore:       sso.NewStateStore(cache),
		MetricsRegistry:     serverbuild.NewDefaultMetrics(),
	}

	handler, err := serverbuild.ComposeServer(cfg, deps)
	if err != nil {
		t.Fatalf("ComposeServer (native mode): %v", err)
	}
	if handler == nil {
		t.Fatal("nil handler")
	}

	req := httptest.NewRequest(http.MethodGet, "/v1/sso/discover?email=bob@example.com", nil)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("smoke request status (native mode): got %d want 200; body=%q", rec.Code, rec.Body.String())
	}
}

// TestComposeServer_RejectsMissingRequiredDeps pins the fail-fast posture:
// a misconfigured composition root should die at boot rather than silently
// 500ing on the first request.
func TestComposeServer_RejectsMissingRequiredDeps(t *testing.T) {
	cases := []struct {
		name    string
		mutate  func(d *serverbuild.Deps)
		wantErr string
	}{
		{
			name:    "missing Store",
			mutate:  func(d *serverbuild.Deps) { d.Store = nil },
			wantErr: "Deps.Store",
		},
		{
			name:    "missing Discoverer",
			mutate:  func(d *serverbuild.Deps) { d.Discoverer = nil },
			wantErr: "Deps.Discoverer",
		},
		{
			name:    "missing Connector",
			mutate:  func(d *serverbuild.Deps) { d.Connector = nil },
			wantErr: "Deps.Connector",
		},
	}
	cfg := serverbuild.Config{
		DevMode:           true,
		DevOrganizationID: "org",
		DevUserID:         "user",
		DevUserEmail:      "u@t",
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			deps := serverbuild.Deps{
				Store:           &stubStore{},
				Cache:           cache.New(""),
				Discoverer:      &stubDiscoverer{},
				Connector:       &stubConnector{},
				MetricsRegistry: serverbuild.NewDefaultMetrics(),
			}
			tc.mutate(&deps)
			_, err := serverbuild.ComposeServer(cfg, deps)
			if err == nil {
				t.Fatalf("ComposeServer succeeded with %s; want error containing %q", tc.name, tc.wantErr)
			}
			if !contains(err.Error(), tc.wantErr) {
				t.Errorf("error %q missing %q", err.Error(), tc.wantErr)
			}
		})
	}
}

// TestComposeServer_RejectsMissingAuthProviderInProdMode pins that
// non-DevMode requires AuthProvider. A composition-root bug that forgot
// to wire it would surface here at boot.
func TestComposeServer_RejectsMissingAuthProviderInProdMode(t *testing.T) {
	cfg := serverbuild.Config{
		DevMode: false,
	}
	deps := serverbuild.Deps{
		Store:           &stubStore{},
		Cache:           cache.New(""),
		Discoverer:      &stubDiscoverer{},
		Connector:       &stubConnector{},
		MetricsRegistry: serverbuild.NewDefaultMetrics(),
		// AuthProvider intentionally nil
	}
	_, err := serverbuild.ComposeServer(cfg, deps)
	if err == nil {
		t.Fatal("ComposeServer succeeded without AuthProvider in prod mode; want error")
	}
	if !contains(err.Error(), "AuthProvider") {
		t.Errorf("error missing AuthProvider hint: %q", err.Error())
	}
}

// ─── tiny string helpers (package strings would import-sort awkwardly) ──────

func contains(haystack, needle string) bool {
	for i := 0; i+len(needle) <= len(haystack); i++ {
		if haystack[i:i+len(needle)] == needle {
			return true
		}
	}
	return false
}

func containsHasSSO(s string) bool { return contains(s, `"has_sso"`) }

// silence unused-import if a helper is removed during refactoring
var _ middleware.SSOEnforcementResolver = nil
