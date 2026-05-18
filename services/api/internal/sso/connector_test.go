package sso_test

import (
	"strings"
	"testing"

	"axiaops.io/api/internal/sso"
)

// TestRequireHTTPS_AcceptsHTTPS — the happy path.
func TestRequireHTTPS_AcceptsHTTPS(t *testing.T) {
	t.Parallel()
	if err := sso.RequireHTTPS("https://idp.example.com/.well-known/openid-configuration", "oidc_discovery_url"); err != nil {
		t.Errorf("https URL rejected: %v", err)
	}
}

// TestRequireHTTPS_AcceptsEmpty — the field is optional; empty must pass.
func TestRequireHTTPS_AcceptsEmpty(t *testing.T) {
	t.Parallel()
	if err := sso.RequireHTTPS("", "oidc_discovery_url"); err != nil {
		t.Errorf("empty URL rejected: %v", err)
	}
	if err := sso.RequireHTTPS("   ", "oidc_discovery_url"); err != nil {
		t.Errorf("whitespace-only URL rejected: %v", err)
	}
}

// TestRequireHTTPS_RejectsPlainHTTP — audit H-3 core case: a non-loopback
// http:// URL leaks client_secret on the token POST.
func TestRequireHTTPS_RejectsPlainHTTP(t *testing.T) {
	t.Parallel()
	err := sso.RequireHTTPS("http://idp.example.com/.well-known/openid-configuration", "oidc_discovery_url")
	if err == nil {
		t.Fatal("plain http URL accepted; want rejection")
	}
	if !strings.Contains(err.Error(), "oidc_discovery_url") {
		t.Errorf("error doesn't name the field: %v", err)
	}
	if !strings.Contains(err.Error(), "https://") {
		t.Errorf("error doesn't tell operator the fix: %v", err)
	}
}

// TestRequireHTTPS_AllowsLoopback — local fake-IdP fixtures use plain HTTP
// against 127.0.0.1 / localhost / [::1]. That's not on the wire, so it's safe.
//
// IPv6 loopback variants matter because RFC 4291 defines several textual
// equivalents (compressed ::1, leading-zero ::0001, uncompressed
// 0:0:0:0:0:0:0:1). The validator uses net.ParseIP so all three normalise
// to the same address; this test pins that behaviour. Audit follow-up
// H-3-IPv6 (issue #94).
func TestRequireHTTPS_AllowsLoopback(t *testing.T) {
	t.Parallel()
	cases := []string{
		"http://localhost:8080/.well-known/openid-configuration",
		"http://localhost/.well-known/openid-configuration",
		"http://127.0.0.1:8080/.well-known/openid-configuration",
		"HTTP://LOCALHOST/.well-known/openid-configuration", // case-insensitive scheme
		// IPv6 loopback variants — bracketed per RFC 3986 §3.2.2.
		"http://[::1]/.well-known/openid-configuration",
		"http://[::1]:8080/.well-known/openid-configuration",
		"http://[::1]",                                              // no path, no port
		"http://[::0001]/.well-known/openid-configuration",          // leading-zero compressed form
		"http://[0:0:0:0:0:0:0:1]/.well-known/openid-configuration", // uncompressed RFC 4291
		"HTTP://[::1]/.well-known/openid-configuration",             // case-insensitive scheme + IPv6
		// IPv4 inside 127.0.0.0/8 — loopback per net.IP.IsLoopback.
		"http://127.0.0.2/discovery",
	}
	for _, raw := range cases {
		t.Run(raw, func(t *testing.T) {
			if err := sso.RequireHTTPS(raw, "oidc_discovery_url"); err != nil {
				t.Errorf("loopback URL %q rejected: %v", raw, err)
			}
		})
	}
}

// TestRequireHTTPS_RejectsExoticSchemes — file://, ftp://, javascript:, etc.
// must NOT pass as "not http and not https so… maybe?". Anything that isn't
// https or http-loopback is rejected outright.
//
// The IPv6 cases here pin the audit H-3-IPv6 contract: only loopback IPv6
// addresses are exempt — public IPv6 (e.g. 2001:db8::/32 documentation range)
// must still be rejected, and a path-segment containing "[::1]" must not
// trick the validator (the substring-trick path mirrors the IPv4
// "/localhost.txt" case already in the set).
func TestRequireHTTPS_RejectsExoticSchemes(t *testing.T) {
	t.Parallel()
	cases := []string{
		"file:///etc/passwd",
		"ftp://idp.example.com/discovery",
		"javascript:alert(1)",
		"//idp.example.com/discovery",          // protocol-relative
		"idp.example.com/discovery",            // no scheme
		"http://attacker.evil/localhost.txt",   // not loopback — substring trick (IPv4 form)
		"http://attacker.evil/[::1].txt",       // not loopback — substring trick (IPv6 form)
		"http://[2001:db8::1]/discovery",       // public IPv6 (documentation range, RFC 3849)
		"http://[2001:db8::1]:8080/discovery",  // public IPv6 with port
		"http://[fe80::1]/discovery",           // link-local IPv6 — not loopback
		"http://[::1].attacker.evil/discovery", // host is attacker.evil, not [::1] — DNS label trick
	}
	for _, raw := range cases {
		t.Run(raw, func(t *testing.T) {
			if err := sso.RequireHTTPS(raw, "oidc_discovery_url"); err == nil {
				t.Errorf("exotic scheme %q accepted; want rejection", raw)
			}
		})
	}
}
