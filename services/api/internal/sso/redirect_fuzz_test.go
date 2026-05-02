package sso_test

// redirect_fuzz_test.go pins the §5.5 / architect N4 acceptance:
//
//   "Open-redirect fuzz on OIDC `state` (architect N4): fuzz the `state`
//    parameter with `https://evil.com`, `//evil.com`, `javascript:` URLs;
//    all must redirect to the fixed `/dashboard` path regardless of `state`
//    content."
//
// The actual attack surface is `?return_to=` on /v1/sso/oidc/{cid}/initiate,
// not `state` itself (state is server-issued and opaque). The user-controlled
// value is the return_to query param; it lands in StateData.RedirectAfterLogin
// and is later read by the callback to build the post-login Location header.
//
// The §5.5 phrase "regardless of state content" is the key: even if the
// state record were corrupted between Persist and Consume — storage bug,
// hostile cache write, post-validation tampering — the callback must still
// not be coerced into redirecting off-origin. Defense-in-depth is
// implemented by re-validating RedirectAfterLogin at the redirect site
// in oidc_callback.go via the exported sso.ValidatedReturnTo.
//
// This file pins three layers:
//
//   Layer 1 — boundary at initiate: TestValidatedReturnTo_Boundary covers
//             ValidatedReturnTo directly with an extensive hostile-shape
//             list (URL-encoded schemes, mixed-case scheme, tab/VT/CR/LF
//             whitespace, length cap, authority confusion, exotic schemes).
//
//   Layer 2 — defense-in-depth at callback: TestSSO_OpenRedirect_DefenseInDepth
//             manually persists a state record with a hostile
//             RedirectAfterLogin (bypassing the initiate boundary), runs the
//             callback against it, asserts the final Location is /dashboard.
//             Without the callback-side ValidatedReturnTo call this test
//             FAILS — the callback would 302 to the hostile target.
//
//   Layer 3 — happy path through both layers: TestSSO_OpenRedirect_HappyPath
//             confirms a legitimate /dashboard/zombies path round-trips
//             intact through both validations.

import (
	"context"
	"net/url"
	"strings"
	"testing"

	"axiaops.io/api/internal/sso"
	"github.com/golang-jwt/jwt/v5"
)

// ─── layer 1: boundary at initiate ──────────────────────────────────────────

// TestValidatedReturnTo_Boundary fuzzes the exported ValidatedReturnTo with
// an extensive list of confusable shapes. Goes beyond the 8-value list in
// initiate_test.go's TestInitiate_ReturnTo_DropsHostileValues by covering
// percent-encoded schemes, mixed-case schemes, exotic schemes (file, vbscript,
// data), tab / vertical-tab whitespace, length-cap boundary, authority
// confusion (https://evil.com@app.example.com), and the idempotency
// invariant for legitimate same-origin paths.
func TestValidatedReturnTo_Boundary(t *testing.T) {
	type tc struct {
		name string
		in   string
		want string
	}

	// Cases that MUST be dropped to "" — the entire hostile zoo.
	hostile := []tc{
		// Absolute URLs — the canonical open-redirect shape.
		{"https absolute", "https://evil.com", ""},
		{"http absolute", "http://evil.com", ""},
		{"https with path", "https://evil.com/login", ""},
		// Protocol-relative — //host parses to a URL whose Host is set.
		{"protocol-relative", "//evil.com", ""},
		{"protocol-relative with path", "//evil.com/dashboard", ""},
		// Non-http schemes. The boundary rejects on non-empty Scheme.
		{"javascript:", "javascript:alert(1)", ""},
		{"data:", "data:text/html,<script>", ""},
		{"vbscript:", "vbscript:msgbox(1)", ""},
		{"file:", "file:///etc/passwd", ""},
		{"about:blank", "about:blank", ""},
		// Mixed-case scheme — url.Parse normalises ToLower for the Scheme
		// detection, so HTTPS://evil.com still has non-empty Scheme.
		{"mixed-case https", "HTTPS://evil.com", ""},
		{"mixed-case JavaScript", "JavaScript:alert(1)", ""},
		// Authority confusion — browsers parse https://evil.com@app.example.com
		// as a request to evil.com with userinfo "app.example.com". Treat as
		// hostile because the perceived host (rightmost label of userinfo +
		// real host) confuses end users in the URL bar.
		{"authority confusion", "https://evil.com@app.example.com/path", ""},
		// Bare host without scheme: still has Host populated by url.Parse
		// when the input starts with "//", which we already cover. A bare
		// "evil.com/path" has no Scheme/Host (it parses as Path) — the
		// "must start with /" check catches it.
		{"bare host no scheme", "evil.com/path", ""},
		// Doesn't start with /.
		{"plain word", "foo", ""},
		{"relative path no slash", "dashboard/zombies", ""},
		// Control chars that some browsers normalise away or render oddly.
		{"null byte", "/path\x00ok", ""},
		{"newline", "/path\nfoo", ""},
		{"carriage return", "/path\rfoo", ""},
		{"backslash", "/path\\../", ""},
		// Length cap (>1024 chars). The boundary rejects to keep state
		// records small and to avoid pathological URLs.
		{"over length cap", "/" + strings.Repeat("x", 1024), ""},
		// Empty.
		{"empty", "", ""},
	}

	// Cases that MUST pass through unchanged — the canonical accepted shape.
	accepted := []tc{
		{"root path", "/", "/"},
		{"single segment", "/dashboard", "/dashboard"},
		{"deep path", "/dashboard/zombies/by-account", "/dashboard/zombies/by-account"},
		{"with query string", "/dashboard?account=123", "/dashboard?account=123"},
		{"with hash fragment", "/dashboard#savings", "/dashboard#savings"},
		// Boundary case for the length cap: 1024 exactly is accepted (cap is
		// > 1024, strict-less-than). Pin this so a future "tighten to <=" change
		// is visible.
		{"at length cap (1024)", "/" + strings.Repeat("x", 1023), "/" + strings.Repeat("x", 1023)},
	}

	t.Run("hostile dropped", func(t *testing.T) {
		for _, c := range hostile {
			t.Run(c.name, func(t *testing.T) {
				got := sso.ValidatedReturnTo(c.in)
				if got != c.want {
					t.Errorf("ValidatedReturnTo(%q) = %q; want %q", c.in, got, c.want)
				}
			})
		}
	})

	t.Run("accepted preserved", func(t *testing.T) {
		for _, c := range accepted {
			t.Run(c.name, func(t *testing.T) {
				got := sso.ValidatedReturnTo(c.in)
				if got != c.want {
					t.Errorf("ValidatedReturnTo(%q) = %q; want %q", c.in, got, c.want)
				}
			})
		}
	})

	// Idempotency: a once-validated value passing through a second time must
	// produce the same output. This matters because oidc_callback.go calls
	// ValidatedReturnTo on stateData.RedirectAfterLogin which was already
	// validated at initiate time — the second call must not corrupt the
	// happy path.
	t.Run("idempotent on accepted", func(t *testing.T) {
		for _, c := range accepted {
			first := sso.ValidatedReturnTo(c.in)
			second := sso.ValidatedReturnTo(first)
			if first != second {
				t.Errorf("not idempotent for %q: first=%q second=%q", c.in, first, second)
			}
		}
	})
}

// ─── layer 2: defense-in-depth at callback ──────────────────────────────────

// TestSSO_OpenRedirect_DefenseInDepth manually persists a state record with
// a hostile RedirectAfterLogin (BYPASSING the initiate-time ValidatedReturnTo
// boundary), runs the callback against it, and asserts the final Location is
// /dashboard.
//
// This is the test the architect N4 "regardless of state content" acceptance
// is really after: even if a hostile value somehow lands in
// StateData.RedirectAfterLogin (storage bug, cache tampering, future bug
// that bypasses the initiate validation), the callback must still default
// to /dashboard. Without the defense-in-depth re-validation in
// oidc_callback.go, this test fails (callback 302s to the hostile target).
//
// We re-use the callbackTest fixture from oidc_callback_test.go so this test
// exercises the real callback path end-to-end (token exchange, ID-token
// validation, JIT, audit). Only the state-record contents are crafted to
// inject the hostile value.
func TestSSO_OpenRedirect_DefenseInDepth(t *testing.T) {
	hostileShapes := []string{
		"https://evil.com",
		"https://evil.com/dashboard",
		"//evil.com",
		"//evil.com/path",
		"javascript:alert(1)",
		"data:text/html,<script>alert(1)</script>",
		"vbscript:msgbox(1)",
		"file:///etc/passwd",
		"HTTPS://evil.com",                  // mixed-case scheme
		"https://evil.com@app.example.com/", // authority confusion
		"foo",                               // doesn't start with /
		"/path\x00null",
		"/path\nnewline",
		"/path\rcr",
		"/path\\backslash",
	}

	for _, hostile := range hostileShapes {
		t.Run(hostile, func(t *testing.T) {
			ct := newCallbackTest(t)

			// Generate a fresh state then OVERWRITE the RedirectAfterLogin
			// with the hostile value before persisting. This is the
			// "post-validation tampering" scenario the defense-in-depth
			// guards against.
			state, data, err := sso.GenerateState("conn-1", "")
			if err != nil {
				t.Fatalf("GenerateState: %v", err)
			}
			data.RedirectAfterLogin = hostile
			if err := ct.state.Persist(context.Background(), state, data); err != nil {
				t.Fatalf("Persist: %v", err)
			}

			ct.idp.SetNextToken(ct.claimsFor(data.Nonce))

			rec := ct.hit("conn-1", "auth-code-xyz", state)

			// Callback success path returns 302 with Location set. The
			// absolute requirement: Location MUST be the safe default
			// /dashboard, regardless of the hostile state-record content.
			if rec.Code != 302 {
				t.Fatalf("status: got %d want 302; body=%q", rec.Code, rec.Body.String())
			}
			loc := rec.Header().Get("Location")
			if loc != "/dashboard" {
				t.Errorf("hostile RedirectAfterLogin %q leaked into Location: got %q; want /dashboard",
					hostile, loc)
			}

			// Belt-and-braces parse: even if the string looks like /dashboard
			// the URL parser must agree it's same-origin. Catches a
			// theoretical future bug where the literal string starts with
			// /dashboard but contains a confusing payload.
			parsed, err := url.Parse(loc)
			if err != nil {
				t.Errorf("Location %q failed to parse: %v", loc, err)
			}
			if parsed.Scheme != "" || parsed.Host != "" {
				t.Errorf("Location %q has Scheme=%q Host=%q; want both empty (relative path)",
					loc, parsed.Scheme, parsed.Host)
			}

			// Sanity: the rest of the ceremony still happened (session
			// minted, audit recorded). Defense-in-depth must NOT be a hard
			// fail that blocks a legitimate login — the user who tampered
			// with their OWN state record still completes the login, just
			// to /dashboard instead of their target.
			if !ct.minter.called {
				t.Error("session was not minted — defense-in-depth must not block successful auth")
			}
		})
	}
}

// ─── layer 3: happy path through both layers ────────────────────────────────

// TestSSO_OpenRedirect_HappyPath confirms a legitimate same-origin path
// round-trips intact through both ValidatedReturnTo calls (initiate
// boundary + callback defense-in-depth). Pin the integration so a future
// over-tightening of ValidatedReturnTo (e.g. dropping query strings)
// surfaces here as a happy-path regression.
func TestSSO_OpenRedirect_HappyPath(t *testing.T) {
	ct := newCallbackTest(t)

	// Manually construct the state record exactly as the initiate handler
	// would — through GenerateState with a validated return_to. This is
	// the "expected" persisted shape after a clean initiate call.
	wantPath := "/dashboard/zombies?account=acct-1"
	state, data, err := sso.GenerateState("conn-1", sso.ValidatedReturnTo(wantPath))
	if err != nil {
		t.Fatalf("GenerateState: %v", err)
	}
	if data.RedirectAfterLogin != wantPath {
		t.Fatalf("ValidatedReturnTo dropped legitimate path %q; got %q", wantPath, data.RedirectAfterLogin)
	}
	if err := ct.state.Persist(context.Background(), state, data); err != nil {
		t.Fatalf("Persist: %v", err)
	}

	ct.idp.SetNextToken(ct.claimsFor(data.Nonce))

	rec := ct.hit("conn-1", "auth-code-xyz", state)

	if rec.Code != 302 {
		t.Fatalf("status: got %d want 302; body=%q", rec.Code, rec.Body.String())
	}
	if loc := rec.Header().Get("Location"); loc != wantPath {
		t.Errorf("legit path mangled by callback: got %q; want %q", loc, wantPath)
	}
}

// claimsFor is provided by callbackTest, but jwt.MapClaims must be imported
// somewhere in this file for the import to be considered used by go vet.
// The import is exercised when callbackTest.claimsFor is called above; this
// reference keeps gopls happy if the helper is ever inlined.
var _ = jwt.MapClaims{}
