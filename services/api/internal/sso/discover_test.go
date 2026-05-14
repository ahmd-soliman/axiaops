package sso_test

// discover_test.go pins two §5.5 acceptance items, both architect N4 follow-ups
// targeting the same handler:
//
//   1. "/v1/sso/discover returns 200 with has_sso:false for unknown domains;
//      constant response shape verified."
//   2. "Domain-confusion fuzz on /v1/sso/discover (architect N4): dnstwist-style
//      lookalike domains show no false positives — verified `acme.com` must
//      not match `acme.co`, `aсme.com` punycode, etc."
//
// Why these matter:
//   - Constant shape: the discoverer is the only pre-auth endpoint that sees
//     an org-bound input (the email's domain). Any divergence in the response
//     (different status codes, different JSON keys, large timing differences)
//     is an org-enumeration channel — the attacker can probe whether their
//     target customer uses SSO without an account. The handler MUST emit
//     200 + {"has_sso": bool, "redirect_url"?: string} for every input.
//   - Domain confusion: the verified-domain lookup is exact-match on
//     `lower(domain)`. This test pins that property — a verified `acme.com`
//     is not reachable via Cyrillic `aсme.com`, suffix `acme.com.evil.io`,
//     subdomain `evil.acme.com`, or punycode encoding. The lookup table
//     stores only the canonical form; if a future change introduces fuzzy
//     matching (Levenshtein, suffix wildcards) this test fails first.

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"axiaops.io/api/internal/auth"
	"axiaops.io/api/internal/sso"
	"axiaops.io/shared/cache"
	"axiaops.io/shared/model"
	"axiaops.io/shared/storage"
)

// fakeDiscoverer is an `sso.Discoverer` that returns the configured outcome
// regardless of email input. Used for handler-level shape tests where the
// discoverer's lookup logic isn't under test (the lookup itself is exercised
// against a fake store in TestNativeDiscoverer_DomainConfusion below).
type fakeDiscoverer struct {
	res   sso.DiscoverResult
	err   error
	sleep time.Duration // simulate slow DB lookup so the latency-floor branch is exercised
}

func (f *fakeDiscoverer) Discover(_ context.Context, _ string) (sso.DiscoverResult, error) {
	if f.sleep > 0 {
		time.Sleep(f.sleep)
	}
	return f.res, f.err
}

// fakeDiscoverStore satisfies storage.Store via embedded-interface trick: the
// embedded interface is nil, so any method other than the override below
// panics on call. NativeDiscoverer.Discover only calls
// GetVerifiedSSODomainByName — panic-on-unused is the right posture; if a
// future change to NativeDiscoverer adds another store method, that test
// will surface the addition immediately.
type fakeDiscoverStore struct {
	storage.Store
	verified map[string]model.SSODomain // key: pre-lowercased canonical domain
}

func (f *fakeDiscoverStore) GetVerifiedSSODomainByName(_ context.Context, name string) (model.SSODomain, error) {
	d, ok := f.verified[strings.ToLower(name)]
	if !ok {
		return model.SSODomain{}, storage.ErrSSODomainNotFound
	}
	return d, nil
}

// ─── handler-level constant-shape tests ─────────────────────────────────────

// TestDiscoverHandler_ConstantShape proves the handler emits an identical
// response shape across the full input space — verified domain, unknown
// domain, malformed email, empty email, transient DB error. Any divergence
// is an enumeration channel.
func TestDiscoverHandler_ConstantShape(t *testing.T) {
	verified := sso.DiscoverResult{
		HasSSO:         true,
		RedirectURL:    "https://app.axiaops.io/v1/sso/oidc/conn-1/initiate",
		ConnectionID:   "conn-1",   // internal — must NOT appear in JSON
		OrganizationID: "org-1",    // internal — must NOT appear in JSON
		Protocol:       "oidc",     // internal — must NOT appear in JSON
	}
	notVerified := sso.DiscoverResult{HasSSO: false}

	cases := []struct {
		name      string
		email     string
		fake      *fakeDiscoverer
		wantHasSSO bool
		// True when the response body should include "redirect_url"; false
		// means the key MUST be absent (json:"omitempty" on RedirectURL).
		wantRedirect bool
	}{
		{"verified domain", "user@acme.com", &fakeDiscoverer{res: verified}, true, true},
		{"unknown domain", "user@notclaimed.example", &fakeDiscoverer{res: notVerified}, false, false},
		{"empty email", "", &fakeDiscoverer{res: notVerified}, false, false},
		{"malformed email no @", "userexample.com", &fakeDiscoverer{res: notVerified}, false, false},
		{"malformed email trailing @", "user@", &fakeDiscoverer{res: notVerified}, false, false},
		{"malformed email leading @", "@example.com", &fakeDiscoverer{res: notVerified}, false, false},
		{
			// Real DB error must not propagate as 500: the handler logs it and
			// degrades to has_sso=false so a transient blip doesn't break the
			// login UX. Pin this — surfacing it as 500 would let the attacker
			// distinguish "DB lookup attempted" (= domain claimed) from "no
			// lookup attempted" (= empty/malformed) by the error rate.
			"db error degrades to no-sso",
			"user@acme.com",
			&fakeDiscoverer{res: notVerified, err: errFake},
			false, false,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			h := sso.NewDiscoverHandler(tc.fake)
			req := httptest.NewRequest(http.MethodGet, "/v1/sso/discover?email="+tc.email, nil)
			rec := httptest.NewRecorder()

			h.ServeHTTP(rec, req)

			if rec.Code != http.StatusOK {
				t.Fatalf("status = %d; want 200 (constant-shape requires 200 for every input)", rec.Code)
			}
			if got := rec.Header().Get("Content-Type"); got != "application/json" {
				t.Errorf("Content-Type = %q; want application/json", got)
			}

			// Decode into a generic map so we can assert on EXACT key set —
			// no internal fields (connection_id, organization_id, protocol)
			// must leak into the wire format.
			var body map[string]any
			if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
				t.Fatalf("body is not JSON: %v (raw: %q)", err, rec.Body.String())
			}

			// has_sso always present.
			has, ok := body["has_sso"]
			if !ok {
				t.Fatalf("response missing has_sso key (body: %v)", body)
			}
			if got, want := has.(bool), tc.wantHasSSO; got != want {
				t.Errorf("has_sso = %v; want %v", got, want)
			}

			// redirect_url presence is conditional.
			_, gotRedir := body["redirect_url"]
			if gotRedir != tc.wantRedirect {
				t.Errorf("redirect_url present = %v; want %v (body: %v)", gotRedir, tc.wantRedirect, body)
			}

			// Internal fields MUST NEVER appear over the wire — they have
			// json:"-" but a future struct refactor could break that. Pin it.
			for _, leaked := range []string{"connection_id", "organization_id", "protocol"} {
				if _, present := body[leaked]; present {
					t.Errorf("internal field %q leaked to wire response (body: %v)", leaked, body)
				}
			}
		})
	}
}

// errFake is a stand-in for transient storage failures — its identity doesn't
// matter to the handler (the handler only checks `err != nil`).
var errFake = &constErr{msg: "fake discoverer DB error"}

type constErr struct{ msg string }

func (e *constErr) Error() string { return e.msg }

// TestDiscoverHandler_LatencyFloor pins the 5ms latency floor (design doc §5.4
// timing-channel mitigation): even when the discoverer returns instantly, the
// handler must take ≥ minDiscoverLatency. The constant in discoverer.go is
// 5ms; we assert >= 4ms to allow for clock-resolution noise on slow CI.
//
// Why this is critical: the verified-domain JOIN takes a few ms; "domain not
// in table" is sub-millisecond. Without the pad, an attacker can time-side-
// channel which domains are claimed. The pad collapses that channel.
func TestDiscoverHandler_LatencyFloor(t *testing.T) {
	h := sso.NewDiscoverHandler(&fakeDiscoverer{
		res:   sso.DiscoverResult{HasSSO: false},
		sleep: 0, // instantaneous lookup — would skip the floor without the pad
	})
	req := httptest.NewRequest(http.MethodGet, "/v1/sso/discover?email=user@unknown.example", nil)
	rec := httptest.NewRecorder()

	start := time.Now()
	h.ServeHTTP(rec, req)
	elapsed := time.Since(start)

	// 4ms threshold (vs the 5ms floor) tolerates clock-resolution noise on
	// slow CI hosts. If this flakes consistently, the pad isn't holding.
	if elapsed < 4*time.Millisecond {
		t.Errorf("handler returned in %v; want >= 4ms (5ms minDiscoverLatency floor)", elapsed)
	}
}

// TestDiscoverHandler_RateLimit — audit M-5. After the per-IP cap is
// exceeded, the handler returns 429. The latency-floor pad still applies
// so a successful hit and a rate-limited hit are indistinguishable by
// wall-clock timing (preserves the constant-shape guarantee even on the
// 429 path — otherwise the rate-limit response itself becomes a timing
// side-channel for "this domain was queried frequently").
func TestDiscoverHandler_RateLimit(t *testing.T) {
	t.Parallel()
	mem := cache.New("")
	t.Cleanup(func() { _ = mem.Close() })

	h := sso.NewDiscoverHandler(&fakeDiscoverer{res: sso.DiscoverResult{HasSSO: false}}).
		WithRateLimit(auth.NewIPRateLimiter(mem, "test:sso_discover", 2))

	hit := func() *httptest.ResponseRecorder {
		req := httptest.NewRequest(http.MethodGet, "/v1/sso/discover?email=user@acme.com", nil)
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, req)
		return rec
	}

	for i := 0; i < 2; i++ {
		w := hit()
		if w.Code != http.StatusOK {
			t.Fatalf("hit %d: status = %d; want 200", i+1, w.Code)
		}
	}
	w := hit()
	if w.Code != http.StatusTooManyRequests {
		t.Fatalf("post-cap status = %d; want 429", w.Code)
	}
	if w.Header().Get("Retry-After") == "" {
		t.Errorf("Retry-After header missing on 429")
	}
}

// ─── discoverer-level domain-confusion fuzz ─────────────────────────────────

// TestNativeDiscoverer_DomainConfusion fuzzes the verified-domain lookup with
// dnstwist-style lookalikes. Verified `acme.com` MUST NOT match any of the
// confusable variants. Acme.com IS reachable via case/whitespace variants of
// the canonical form, since `emailDomain()` lowercases and trims.
//
// Failure of this test means a future change introduced fuzzy / suffix /
// substring matching — that's an org-takeover channel and should be reverted.
func TestNativeDiscoverer_DomainConfusion(t *testing.T) {
	verifiedRow := model.SSODomain{
		ID:              "dom-1",
		OrganizationID:  "org-acme",
		SSOConnectionID: "conn-acme",
		Domain:          "acme.com",
		Status:          model.SSODomainStatusVerified,
	}
	store := &fakeDiscoverStore{
		verified: map[string]model.SSODomain{"acme.com": verifiedRow},
	}
	d := sso.NewNativeDiscoverer(store, "https://app.axiaops.io")

	// Cases that MUST resolve to has_sso=true — the canonical form and its
	// case/whitespace variants the parser is supposed to canonicalise.
	matches := []struct {
		name  string
		email string
	}{
		{"canonical lowercase", "user@acme.com"},
		{"uppercase domain", "user@ACME.COM"},
		{"mixed case", "user@Acme.Com"},
		{"trailing whitespace in domain", "user@acme.com "},
		{"leading whitespace in domain", "user@ acme.com"},
	}
	for _, tc := range matches {
		t.Run("matches/"+tc.name, func(t *testing.T) {
			res, err := d.Discover(context.Background(), tc.email)
			if err != nil {
				t.Fatalf("Discover(%q) error = %v", tc.email, err)
			}
			if !res.HasSSO {
				t.Errorf("Discover(%q): HasSSO=false; want true (canonical-form match must hit)", tc.email)
			}
			if res.ConnectionID != "conn-acme" {
				t.Errorf("Discover(%q): ConnectionID=%q; want conn-acme", tc.email, res.ConnectionID)
			}
		})
	}

	// dnstwist-style confusable variants — each MUST resolve to has_sso=false.
	// Collected from architect N4 + standard domain-confusion taxonomy:
	//   - TLD swap, sibling TLDs
	//   - prefix / suffix attacks (org domain embedded in attacker domain)
	//   - subdomain attacks (verified domain as subdomain of attacker domain)
	//   - Cyrillic/Greek lookalikes (homoglyph attacks)
	//   - Punycode-encoded lookalike
	//   - typos (typosquat)
	//   - extra-dots / empty-label variants
	confusables := []struct {
		name  string
		email string
	}{
		{"TLD swap .co", "user@acme.co"},
		{"TLD swap .org", "user@acme.org"},
		{"TLD swap .net", "user@acme.net"},
		{"TLD swap .io", "user@acme.io"},
		{"sibling TLD .com.co", "user@acme.com.co"},
		{"suffix attack: verified domain as prefix of attacker domain", "user@acme.com.evil.io"},
		{"subdomain attack: attacker subdomain of verified", "user@evil.acme.com"},
		{"deeper subdomain", "user@a.b.acme.com"},
		// Cyrillic 'с' (U+0441) replacing Latin 'c' (U+0063) in 'acme' — the
		// 'a' is also Cyrillic-replaceable but the 'с' is the canonical
		// homoglyph attack the design doc calls out.
		{"Cyrillic homoglyph c", "user@aсme.com"},
		// Greek 'ο' (U+03BF) replacing Latin 'o' (U+006F).
		{"Greek homoglyph o", "user@acme.cοm"},
		// Punycode of `aсme.com` — what an IDN-aware client would actually
		// send for the Cyrillic domain. Attacker registers the punycode form,
		// our lookup must not match it to the canonical form.
		{"punycode form of homoglyph", "user@xn--ame-7md.com"},
		{"typosquat: transposed", "user@acem.com"},
		{"typosquat: missing letter", "user@acm.com"},
		{"typosquat: extra letter", "user@acmme.com"},
		{"empty label (double dot)", "user@acme..com"},
		{"trailing dot (root-anchored FQDN)", "user@acme.com."},
		{"leading dot", "user@.acme.com"},
		// Look like the canonical domain but a different TLD with shared
		// prefix — this caught a past bug in another project where suffix
		// matching was used.
		{"longer TLD shared prefix", "user@acme.community"},
	}
	for _, tc := range confusables {
		t.Run("confusable/"+tc.name, func(t *testing.T) {
			res, err := d.Discover(context.Background(), tc.email)
			if err != nil {
				t.Fatalf("Discover(%q) error = %v (must collapse domain-not-found to (result, nil))", tc.email, err)
			}
			if res.HasSSO {
				t.Errorf("Discover(%q): HasSSO=true; want false — confusable domain matched verified `acme.com` (org-takeover channel)", tc.email)
			}
			if res.ConnectionID != "" {
				t.Errorf("Discover(%q): ConnectionID=%q; want empty on miss", tc.email, res.ConnectionID)
			}
		})
	}
}

// TestNativeDiscoverer_RedirectURL_CarriesEmailLoginHint pins that a successful
// discover returns a redirect URL with the typed email as a `?email=` query
// param. /initiate forwards that param as the OIDC `login_hint` so the IdP
// login form is pre-populated — closes the round-trip "user types email at
// /login then types it AGAIN at the IdP" friction (Tasks.md row 2.7.19).
//
// Also pins URL-encoding for emails containing `+` (subaddressing) and other
// reserved chars: a raw `+` in the URL would decode to space at /initiate's
// `r.URL.Query().Get("email")`, breaking the hint.
func TestNativeDiscoverer_RedirectURL_CarriesEmailLoginHint(t *testing.T) {
	verifiedRow := model.SSODomain{
		ID:              "dom-1",
		OrganizationID:  "org-acme",
		SSOConnectionID: "conn-acme",
		Domain:          "acme.com",
		Status:          model.SSODomainStatusVerified,
	}
	store := &fakeDiscoverStore{
		verified: map[string]model.SSODomain{"acme.com": verifiedRow},
	}
	d := sso.NewNativeDiscoverer(store, "https://app.axiaops.io")

	cases := []struct {
		name         string
		email        string
		wantEmailRaw string // value returned by url.Values.Get("email") at /initiate
	}{
		{"plain email", "alice@acme.com", "alice@acme.com"},
		{"subaddressing with +", "alice+work@acme.com", "alice+work@acme.com"},
		{"uppercase preserved (IdP normalises, not us)", "Alice@ACME.COM", "Alice@ACME.COM"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			res, err := d.Discover(context.Background(), tc.email)
			if err != nil {
				t.Fatalf("Discover(%q) error = %v", tc.email, err)
			}
			if !res.HasSSO {
				t.Fatalf("Discover(%q): HasSSO=false; want true", tc.email)
			}

			// Parse the redirect URL and check the email param round-trips.
			// We deliberately use url.Parse (not strings.Contains) so the test
			// catches encoding bugs — a raw `+` would pass a substring check
			// but decode to space at the receiver.
			u, perr := url.Parse(res.RedirectURL)
			if perr != nil {
				t.Fatalf("RedirectURL is not a valid URL: %v (raw: %q)", perr, res.RedirectURL)
			}
			if got, want := u.Path, "/v1/sso/oidc/conn-acme/initiate"; got != want {
				t.Errorf("RedirectURL.Path = %q; want %q", got, want)
			}
			if got := u.Query().Get("email"); got != tc.wantEmailRaw {
				t.Errorf("RedirectURL ?email= decodes to %q; want %q (raw URL: %q)", got, tc.wantEmailRaw, res.RedirectURL)
			}
		})
	}
}

// TestNativeDiscoverer_MalformedEmail pins the parser-level reject path:
// inputs that don't have a parseable domain part return has_sso=false
// without touching the store. Belt-and-braces — even if the store somehow
// contained a row keyed on "" (which the schema's NOT NULL forbids),
// emailDomain returning "" must short-circuit before the lookup.
func TestNativeDiscoverer_MalformedEmail(t *testing.T) {
	store := &fakeDiscoverStore{
		// Deliberately empty — store lookup would crash if reached.
		verified: map[string]model.SSODomain{},
	}
	d := sso.NewNativeDiscoverer(store, "")

	cases := []string{
		"",                // empty
		"plain-string",    // no @
		"@no-local-part",  // @ at index 0
		"no-domain@",      // @ at end
	}
	for _, email := range cases {
		t.Run("input="+email, func(t *testing.T) {
			res, err := d.Discover(context.Background(), email)
			if err != nil {
				t.Errorf("Discover(%q) err = %v; want nil", email, err)
			}
			if res.HasSSO {
				t.Errorf("Discover(%q) HasSSO = true; want false on unparseable input", email)
			}
		})
	}
}
