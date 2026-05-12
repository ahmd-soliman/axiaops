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
// against 127.0.0.1 / localhost. That's not on the wire, so it's safe.
func TestRequireHTTPS_AllowsLoopback(t *testing.T) {
	t.Parallel()
	cases := []string{
		"http://localhost:8080/.well-known/openid-configuration",
		"http://localhost/.well-known/openid-configuration",
		"http://127.0.0.1:8080/.well-known/openid-configuration",
		"HTTP://LOCALHOST/.well-known/openid-configuration", // case-insensitive scheme
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
func TestRequireHTTPS_RejectsExoticSchemes(t *testing.T) {
	t.Parallel()
	cases := []string{
		"file:///etc/passwd",
		"ftp://idp.example.com/discovery",
		"javascript:alert(1)",
		"//idp.example.com/discovery",        // protocol-relative
		"idp.example.com/discovery",          // no scheme
		"http://attacker.evil/localhost.txt", // not loopback — substring trick
	}
	for _, raw := range cases {
		t.Run(raw, func(t *testing.T) {
			if err := sso.RequireHTTPS(raw, "oidc_discovery_url"); err == nil {
				t.Errorf("exotic scheme %q accepted; want rejection", raw)
			}
		})
	}
}
