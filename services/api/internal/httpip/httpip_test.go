package httpip_test

import (
	"net/http/httptest"
	"testing"

	"axiaops.io/api/internal/httpip"
)

// TestRequest_PrefersXRealIP — nginx sets X-Real-IP to the actual peer; a
// client-supplied X-Real-IP doesn't make it past nginx (proxy_set_header
// overwrites), so when we see one we trust it over XFF.
func TestRequest_PrefersXRealIP(t *testing.T) {
	t.Parallel()
	r := httptest.NewRequest("POST", "/v1/auth/login", nil)
	r.RemoteAddr = "127.0.0.1:54321"
	r.Header.Set("X-Real-IP", "203.0.113.7")
	r.Header.Set("X-Forwarded-For", "1.1.1.1, 203.0.113.7")
	if got := httpip.Request(r).String(); got != "203.0.113.7" {
		t.Errorf("Request = %s; want 203.0.113.7 (X-Real-IP wins)", got)
	}
}

// TestRequest_RightmostXForwardedFor — the security-critical case: a client
// that sends `X-Forwarded-For: 1.2.3.4` arrives at the API as
// `X-Forwarded-For: 1.2.3.4, <real-peer>` because nginx and App Runner both
// append. Taking the leftmost token returned the attacker-controlled value
// and let an attacker rotate spoofed IPs to defeat per-IP rate limits and
// to plant chosen IPs in audit/session forensics. We must take the rightmost.
func TestRequest_RightmostXForwardedFor(t *testing.T) {
	t.Parallel()
	r := httptest.NewRequest("POST", "/v1/auth/login", nil)
	r.RemoteAddr = "127.0.0.1:54321"
	r.Header.Set("X-Forwarded-For", "1.2.3.4, 198.51.100.42")
	if got := httpip.Request(r).String(); got != "198.51.100.42" {
		t.Errorf("Request = %s; want 198.51.100.42 (rightmost-trusted, not spoofed leftmost)", got)
	}
}

// TestRequest_SingleXForwardedForToken — no proxy chain, just one entry.
func TestRequest_SingleXForwardedForToken(t *testing.T) {
	t.Parallel()
	r := httptest.NewRequest("POST", "/v1/auth/login", nil)
	r.RemoteAddr = "127.0.0.1:54321"
	r.Header.Set("X-Forwarded-For", "203.0.113.5")
	if got := httpip.Request(r).String(); got != "203.0.113.5" {
		t.Errorf("Request = %s; want 203.0.113.5", got)
	}
}

// TestRequest_FallbackRemoteAddr — direct request to API (tests, dev mode).
func TestRequest_FallbackRemoteAddr(t *testing.T) {
	t.Parallel()
	r := httptest.NewRequest("POST", "/v1/auth/login", nil)
	r.RemoteAddr = "192.0.2.10:1234"
	if got := httpip.Request(r).String(); got != "192.0.2.10" {
		t.Errorf("Request = %s; want 192.0.2.10", got)
	}
}

// TestRequest_XRealIPPreferredEvenWithXForwardedFor — locks in the preference
// order. X-Real-IP wins when both headers are present.
func TestRequest_XRealIPPreferredEvenWithXForwardedFor(t *testing.T) {
	t.Parallel()
	r := httptest.NewRequest("POST", "/v1/auth/login", nil)
	r.RemoteAddr = "127.0.0.1:54321"
	r.Header.Set("X-Real-IP", "10.0.0.1")
	r.Header.Set("X-Forwarded-For", "1.2.3.4, 198.51.100.42")
	if got := httpip.Request(r).String(); got != "10.0.0.1" {
		t.Errorf("Request = %s; want 10.0.0.1", got)
	}
}

// TestRequest_NoHeadersBadRemoteAddr — when nothing parses, return nil so
// callers persist NULL rather than a bogus value.
func TestRequest_NoHeadersBadRemoteAddr(t *testing.T) {
	t.Parallel()
	r := httptest.NewRequest("POST", "/v1/auth/login", nil)
	r.RemoteAddr = "not-an-address"
	if got := httpip.Request(r); got != nil {
		t.Errorf("Request = %v; want nil", got)
	}
}

// TestRequest_MalformedXForwardedForFallsThrough — garbage XFF doesn't
// short-circuit; we fall through to RemoteAddr.
func TestRequest_MalformedXForwardedForFallsThrough(t *testing.T) {
	t.Parallel()
	r := httptest.NewRequest("POST", "/v1/auth/login", nil)
	r.RemoteAddr = "192.0.2.10:1234"
	r.Header.Set("X-Forwarded-For", "not-an-ip")
	if got := httpip.Request(r).String(); got != "192.0.2.10" {
		t.Errorf("Request = %s; want 192.0.2.10 (RemoteAddr fallback)", got)
	}
}
