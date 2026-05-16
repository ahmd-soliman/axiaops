package middleware_test

// sso_enforcement_test.go pins the §5.5 acceptance "enforcement=required
// blocks native-password sessions for the org with 403". The middleware
// itself is in services/api/internal/middleware/sso_enforcement.go.
//
// Behaviour matrix the test enumerates:
//
//   enforcement | auth_mode  | path        | expected
//   ------------+------------+-------------+----------
//   required    | password   | /v1/zombies | 403 sso_required
//   required    | sso        | /v1/zombies | 200 (passthrough)
//   required    | bootstrap  | /v1/zombies | 200 (passthrough)
//   required    | password   | /v1/auth/logout (skip-path) | 200
//   preferred   | password   | /v1/zombies | 200
//   optional    | password   | /v1/zombies | 200
//   ""          | password   | /v1/zombies | 200 (no SSO connection)
//   resolver-error | password | /v1/zombies | 200 (fail open)
//   missing org | password   | /v1/zombies | 200 (defence in depth)
//   nil resolver | password  | /v1/zombies | 200 (middleware disabled)
//
// The 403 body MUST be exactly `{"error":"sso_required"}` so the
// dashboard can pivot on the discriminator.

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"axiaops.io/api/internal/middleware"
	"axiaops.io/shared/model"
)

// stubResolver returns a fixed (level, err) regardless of org. Tests that
// need per-org behaviour can swap to a map-backed shape; not needed yet.
type stubResolver struct {
	level string
	err   error
	calls int
}

func (s *stubResolver) OrgSSOEnforcement(_ context.Context, _ string) (string, error) {
	s.calls++
	return s.level, s.err
}

// enforceOKHandler is the inner handler used by these tests — returns
// 200 with a marker body. Distinct from the package-level okHandler in
// auth_test.go (which writes no body) so 403-vs-pass assertions can
// look at the response body when useful.
var enforceOKHandler = http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write([]byte(`{"ok":true}`))
})

func runEnforce(t *testing.T, resolver middleware.SSOEnforcementResolver, ctxAuthMode, ctxOrgID, path string, skipPaths ...string) *httptest.ResponseRecorder {
	t.Helper()
	h := middleware.EnforceSSO(resolver, skipPaths...)(enforceOKHandler)

	ctx := context.Background()
	if ctxOrgID != "" {
		ctx = middleware.ContextWithOrganizationID(ctx, ctxOrgID)
	}
	if ctxAuthMode != "" {
		ctx = middleware.ContextWithAuthMode(ctx, ctxAuthMode)
	}
	req := httptest.NewRequestWithContext(ctx, http.MethodGet, path, nil)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	return rec
}

func bodyString(t *testing.T, rec *httptest.ResponseRecorder) string {
	t.Helper()
	body, err := io.ReadAll(rec.Body)
	if err != nil {
		t.Fatalf("read body: %v", err)
	}
	return strings.TrimSpace(string(body))
}

// ─── core matrix: required blocks password, passes everything else ──────────

func TestEnforceSSO_RequiredBlocksPasswordSession(t *testing.T) {
	resolver := &stubResolver{level: model.SSOEnforcementRequired}
	rec := runEnforce(t, resolver, string(model.AuthModePassword), "org-acme", "/v1/zombies")

	if rec.Code != http.StatusForbidden {
		t.Fatalf("status: got %d want 403", rec.Code)
	}
	if got := bodyString(t, rec); got != `{"error":"sso_required"}` {
		t.Errorf("body: got %q want {\"error\":\"sso_required\"}", got)
	}
	if ct := rec.Header().Get("Content-Type"); ct != "application/json" {
		t.Errorf("content-type: got %q want application/json", ct)
	}
	if resolver.calls != 1 {
		t.Errorf("resolver called %d times; want 1", resolver.calls)
	}
}

func TestEnforceSSO_RequiredAllowsSSOSession(t *testing.T) {
	resolver := &stubResolver{level: model.SSOEnforcementRequired}
	rec := runEnforce(t, resolver, string(model.AuthModeSSO), "org-acme", "/v1/zombies")

	if rec.Code != http.StatusOK {
		t.Fatalf("SSO session blocked under enforcement=required: got %d body=%q", rec.Code, bodyString(t, rec))
	}
	if resolver.calls != 0 {
		t.Errorf("resolver should not be consulted for non-password sessions; calls=%d", resolver.calls)
	}
}

func TestEnforceSSO_RequiredAllowsBootstrapSession(t *testing.T) {
	// Bootstrap is the first-owner install; SSO connections don't exist
	// yet at that point. The middleware MUST NEVER 403 a bootstrap
	// session — that would brick the install flow.
	resolver := &stubResolver{level: model.SSOEnforcementRequired}
	rec := runEnforce(t, resolver, string(model.AuthModeBootstrap), "org-acme", "/v1/zombies")

	if rec.Code != http.StatusOK {
		t.Fatalf("bootstrap session blocked: got %d body=%q", rec.Code, bodyString(t, rec))
	}
	if resolver.calls != 0 {
		t.Errorf("resolver should not be consulted for non-password sessions; calls=%d", resolver.calls)
	}
}

func TestEnforceSSO_PreferredAndOptionalDoNotBlock(t *testing.T) {
	// "preferred" is a hint to the dashboard, not a server-side gate.
	// "optional" is the default and must be a no-op.
	for _, level := range []string{model.SSOEnforcementPreferred, model.SSOEnforcementOptional, ""} {
		t.Run("level="+level, func(t *testing.T) {
			resolver := &stubResolver{level: level}
			rec := runEnforce(t, resolver, string(model.AuthModePassword), "org-acme", "/v1/zombies")
			if rec.Code != http.StatusOK {
				t.Fatalf("password session blocked under enforcement=%q: got %d body=%q", level, rec.Code, bodyString(t, rec))
			}
		})
	}
}

// ─── fail-open paths: never accidentally 403 on infrastructure issues ───────

func TestEnforceSSO_FailsOpenOnResolverError(t *testing.T) {
	// A transient store outage MUST NOT cascade into a mass 403 storm.
	// The user already authenticated; refusing to serve them on a
	// backend hiccup is worse than letting the request through. The
	// architect-N6 "transient errors don't lock users out" posture.
	resolver := &stubResolver{err: errors.New("storage offline")}
	rec := runEnforce(t, resolver, string(model.AuthModePassword), "org-acme", "/v1/zombies")
	if rec.Code != http.StatusOK {
		t.Fatalf("resolver error produced %d; expected fail-open 200 (body=%q)", rec.Code, bodyString(t, rec))
	}
}

func TestEnforceSSO_FailsOpenOnMissingOrgID(t *testing.T) {
	// Defence in depth: if WrapNative is somehow not in the chain (or
	// got mis-ordered), the enforcement check must not fire on an
	// empty organization ID — otherwise unrelated bugs cascade into
	// mass 403s.
	resolver := &stubResolver{level: model.SSOEnforcementRequired}
	rec := runEnforce(t, resolver, string(model.AuthModePassword), "", "/v1/zombies")
	if rec.Code != http.StatusOK {
		t.Fatalf("missing-org request 403'd; expected fail-open 200 (body=%q)", bodyString(t, rec))
	}
	if resolver.calls != 0 {
		t.Errorf("resolver consulted with empty orgID; calls=%d", resolver.calls)
	}
}

func TestEnforceSSO_NilResolverDisablesMiddleware(t *testing.T) {
	// Passing a nil resolver = enforcement disabled. Lets the
	// composition root stage this middleware in safely before the
	// production resolver impl lands (or in dev mode where the SSO
	// store isn't wired).
	rec := runEnforce(t, nil, string(model.AuthModePassword), "org-acme", "/v1/zombies")
	if rec.Code != http.StatusOK {
		t.Fatalf("nil resolver produced %d; expected fail-open 200", rec.Code)
	}
}

// ─── skip-path bypass: blocked users can still log out ──────────────────────

func TestEnforceSSO_SkipPathBypassesEnforcement(t *testing.T) {
	// /v1/auth/logout is the canonical skip-path: a password-session
	// user under enforcement=required must still be able to clean-end
	// their session. Without the bypass they'd hold a cookie they
	// can't retire via the API.
	resolver := &stubResolver{level: model.SSOEnforcementRequired}
	rec := runEnforce(t, resolver, string(model.AuthModePassword), "org-acme", "/v1/auth/logout", "/v1/auth/logout")
	if rec.Code != http.StatusOK {
		t.Fatalf("skip-path /v1/auth/logout 403'd: got %d body=%q", rec.Code, bodyString(t, rec))
	}
	if resolver.calls != 0 {
		t.Errorf("resolver consulted on skip-path; calls=%d", resolver.calls)
	}

	// Non-skipped path under the same setup is still blocked.
	rec2 := runEnforce(t, resolver, string(model.AuthModePassword), "org-acme", "/v1/zombies", "/v1/auth/logout")
	if rec2.Code != http.StatusForbidden {
		t.Fatalf("non-skipped path passed through: got %d (skip-paths must be exact-match, not prefix-match)", rec2.Code)
	}
}

func TestEnforceSSO_SkipPathExactMatchOnly(t *testing.T) {
	// Skip-path is exact-match — `/v1/auth/logout` must NOT also bypass
	// `/v1/auth/logout/something` or `/v1/auth/logoutX`. Pin this so a
	// future "make it prefix" refactor breaks the test before it ships.
	resolver := &stubResolver{level: model.SSOEnforcementRequired}
	for _, p := range []string{"/v1/auth/logout/extra", "/v1/auth/logoutX", "/v1/auth/logout/"} {
		t.Run(p, func(t *testing.T) {
			rec := runEnforce(t, resolver, string(model.AuthModePassword), "org-acme", p, "/v1/auth/logout")
			if rec.Code != http.StatusForbidden {
				t.Errorf("path %q bypassed enforcement (got %d); skip-paths must be exact-match", p, rec.Code)
			}
		})
	}
}
