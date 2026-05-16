package auth_test

import (
	"context"
	"net"
	"testing"

	"axiaops.io/api/internal/auth"
	"axiaops.io/shared/cache"
)

// newRateLimiterTest gives each test a fresh in-memory cache so the
// minute-bucket key space doesn't leak across tests in the same run.
// All tests use the cache.New("") fallback (memory backend) — Redis
// is exercised by integration tests, not unit ones.
func newRateLimiterTest(t *testing.T, perIP, perEmail int) *auth.LoginRateLimiter {
	t.Helper()
	mem := cache.New("")
	t.Cleanup(func() { _ = mem.Close() })
	return auth.NewLoginRateLimiter(mem).WithLimits(perIP, perEmail)
}

func TestLoginRateLimiterAllowsBelowCap(t *testing.T) {
	t.Parallel()
	rl := newRateLimiterTest(t, 10, 5)
	ip := net.ParseIP("203.0.113.7")
	for i := 0; i < 5; i++ {
		out := rl.Allow(context.Background(), ip, "alice@example.com")
		if !out.Allowed {
			t.Fatalf("attempt #%d unexpectedly blocked: %+v", i+1, out)
		}
	}
}

func TestLoginRateLimiterBlocksAtPerIPCap(t *testing.T) {
	t.Parallel()
	// perIP=3, perEmail=100 — drives the IP cap independently of email.
	rl := newRateLimiterTest(t, 3, 100)
	ip := net.ParseIP("198.51.100.42")
	// Each request uses a distinct email so the per-email counter
	// doesn't trip first; the IP cap is what we're testing.
	emails := []string{"a@x.com", "b@x.com", "c@x.com", "d@x.com"}

	for i, e := range emails[:3] {
		out := rl.Allow(context.Background(), ip, e)
		if !out.Allowed {
			t.Fatalf("attempt %d unexpectedly blocked: %+v", i+1, out)
		}
	}
	out := rl.Allow(context.Background(), ip, emails[3])
	if out.Allowed {
		t.Fatalf("attempt 4 should be blocked by per-IP cap; got %+v", out)
	}
	if out.Reason != "ip" {
		t.Errorf("reason = %q; want ip", out.Reason)
	}
	if out.RetryAfter <= 0 {
		t.Errorf("retry_after = %v; want > 0", out.RetryAfter)
	}
}

func TestLoginRateLimiterBlocksAtPerEmailCap(t *testing.T) {
	t.Parallel()
	// perIP=100, perEmail=3 — drives the email cap independently of IP.
	rl := newRateLimiterTest(t, 100, 3)
	email := "victim@example.com"
	ips := []net.IP{
		net.ParseIP("203.0.113.1"),
		net.ParseIP("203.0.113.2"),
		net.ParseIP("203.0.113.3"),
		net.ParseIP("203.0.113.4"),
	}
	// Each guess from a different IP — the IP cap never trips, but
	// the email cap does. This is the credential-stuffing case.
	for i := 0; i < 3; i++ {
		out := rl.Allow(context.Background(), ips[i], email)
		if !out.Allowed {
			t.Fatalf("attempt %d unexpectedly blocked: %+v", i+1, out)
		}
	}
	out := rl.Allow(context.Background(), ips[3], email)
	if out.Allowed {
		t.Fatalf("4th attempt against same email should be blocked; got %+v", out)
	}
	if out.Reason != "email" {
		t.Errorf("reason = %q; want email", out.Reason)
	}
}

func TestLoginRateLimiterDistinctEmailsIndependent(t *testing.T) {
	// Two different emails from the same IP each get their own
	// per-email counter — only the per-IP cap can connect them.
	t.Parallel()
	rl := newRateLimiterTest(t, 100, 3)
	ip := net.ParseIP("198.51.100.99")
	for i := 0; i < 3; i++ {
		if out := rl.Allow(context.Background(), ip, "a@example.com"); !out.Allowed {
			t.Fatalf("a attempt %d blocked: %+v", i+1, out)
		}
	}
	// `a@example.com` is now at its per-email cap. `b@example.com`
	// must still be allowed — its counter is separate.
	if out := rl.Allow(context.Background(), ip, "b@example.com"); !out.Allowed {
		t.Fatalf("b attempt unexpectedly blocked by a's saturation: %+v", out)
	}
}

func TestLoginRateLimiterCaseAndSpaceNormalised(t *testing.T) {
	// "Alice@Example.com  " and "alice@example.com" must hit the
	// same per-email counter — otherwise an attacker could trivially
	// bypass the email cap by varying case/whitespace.
	t.Parallel()
	rl := newRateLimiterTest(t, 100, 2)
	ip := net.ParseIP("203.0.113.50")
	for _, email := range []string{"alice@example.com", "Alice@Example.com  "} {
		if out := rl.Allow(context.Background(), ip, email); !out.Allowed {
			t.Fatalf("variant %q unexpectedly blocked: %+v", email, out)
		}
	}
	// 3rd attempt across either casing should be blocked.
	out := rl.Allow(context.Background(), ip, "ALICE@example.com")
	if out.Allowed {
		t.Fatalf("3rd attempt across case-variants must hit the email cap; got %+v", out)
	}
	if out.Reason != "email" {
		t.Errorf("reason = %q; want email", out.Reason)
	}
}

func TestLoginRateLimiterNilCacheFailsOpen(t *testing.T) {
	// Defensive: if the constructor ever receives a nil cache (edge
	// case in tests; shouldn't happen via cache.New), Allow must
	// return Allowed=true rather than crashing.
	t.Parallel()
	rl := auth.NewLoginRateLimiter(nil)
	out := rl.Allow(context.Background(), net.ParseIP("203.0.113.1"), "x@y.com")
	if !out.Allowed {
		t.Fatalf("nil cache should fail open; got %+v", out)
	}
}

func TestLoginRateLimiterNilReceiverFailsOpen(t *testing.T) {
	// Defensive: cmd/main.go could (in some failure scenario) leave
	// the limiter as a nil pointer. Allow must tolerate the nil
	// receiver and return Allowed=true. The handler already guards
	// `if h.loginLimit != nil`, but defence-in-depth.
	t.Parallel()
	var rl *auth.LoginRateLimiter
	out := rl.Allow(context.Background(), nil, "x@y.com")
	if !out.Allowed {
		t.Fatalf("nil receiver should fail open; got %+v", out)
	}
}
