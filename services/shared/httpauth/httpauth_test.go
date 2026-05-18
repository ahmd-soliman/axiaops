package httpauth_test

import (
	"encoding/base64"
	"errors"
	"strconv"
	"strings"
	"testing"
	"time"

	"axiaops.io/shared/httpauth"
)

const (
	testMethod = "POST"
	testPath   = "/scan"
)

var (
	testSecret  = mustHexBytes("a1b2c3d4e5f60718293a4b5c6d7e8f902132435465768798a9b0c1d2e3f40516")
	otherSecret = mustHexBytes("fedcba9876543210fedcba9876543210fedcba9876543210fedcba9876543210")
	testBody    = []byte(`{"account_id":"acc-1","organization_id":"org-1"}`)
)

func mustHexBytes(s string) []byte {
	out := make([]byte, len(s)/2)
	for i := 0; i < len(out); i++ {
		hi := hexNibble(s[i*2])
		lo := hexNibble(s[i*2+1])
		out[i] = (hi << 4) | lo
	}
	return out
}

func hexNibble(c byte) byte {
	switch {
	case c >= '0' && c <= '9':
		return c - '0'
	case c >= 'a' && c <= 'f':
		return 10 + (c - 'a')
	case c >= 'A' && c <= 'F':
		return 10 + (c - 'A')
	}
	panic("invalid hex char")
}

func nowAt(ts time.Time) func() time.Time { return func() time.Time { return ts } }

func tsString(t time.Time) string { return strconv.FormatInt(t.Unix(), 10) }

func TestSignVerify_RoundTrip(t *testing.T) {
	now := time.Unix(1_715_740_000, 0)
	sig := httpauth.Sign(testSecret, now, testMethod, testPath, testBody)
	if err := httpauth.Verify(testSecret, time.Minute, nowAt(now),
		tsString(now), sig, testMethod, testPath, testBody); err != nil {
		t.Fatalf("Verify: %v", err)
	}
}

func TestVerify_MissingTimestamp(t *testing.T) {
	err := httpauth.Verify(testSecret, time.Minute, time.Now, "", "sig", testMethod, testPath, testBody)
	if !errors.Is(err, httpauth.ErrMissingTimestamp) {
		t.Fatalf("got %v, want ErrMissingTimestamp", err)
	}
}

func TestVerify_MissingSignature(t *testing.T) {
	err := httpauth.Verify(testSecret, time.Minute, time.Now, "1715740000", "", testMethod, testPath, testBody)
	if !errors.Is(err, httpauth.ErrMissingSignature) {
		t.Fatalf("got %v, want ErrMissingSignature", err)
	}
}

func TestVerify_MalformedTimestamp(t *testing.T) {
	err := httpauth.Verify(testSecret, time.Minute, time.Now, "not-a-number", "c2lnLi4u", testMethod, testPath, testBody)
	if !errors.Is(err, httpauth.ErrMalformedTimestamp) {
		t.Fatalf("got %v, want ErrMalformedTimestamp", err)
	}
}

func TestVerify_MalformedSignature(t *testing.T) {
	err := httpauth.Verify(testSecret, time.Minute, time.Now, "1715740000", "not%base64", testMethod, testPath, testBody)
	if !errors.Is(err, httpauth.ErrMalformedSignature) {
		t.Fatalf("got %v, want ErrMalformedSignature", err)
	}
}

func TestVerify_StaleTimestamp(t *testing.T) {
	now := time.Unix(1_715_740_000, 0)
	stale := now.Add(-2 * time.Minute)
	sig := httpauth.Sign(testSecret, stale, testMethod, testPath, testBody)
	err := httpauth.Verify(testSecret, time.Minute, nowAt(now),
		tsString(stale), sig, testMethod, testPath, testBody)
	if !errors.Is(err, httpauth.ErrTimestampSkew) {
		t.Fatalf("got %v, want ErrTimestampSkew", err)
	}
}

func TestVerify_FutureTimestamp(t *testing.T) {
	now := time.Unix(1_715_740_000, 0)
	future := now.Add(2 * time.Minute)
	sig := httpauth.Sign(testSecret, future, testMethod, testPath, testBody)
	err := httpauth.Verify(testSecret, time.Minute, nowAt(now),
		tsString(future), sig, testMethod, testPath, testBody)
	if !errors.Is(err, httpauth.ErrTimestampSkew) {
		t.Fatalf("got %v, want ErrTimestampSkew", err)
	}
}

func TestVerify_JustInsideWindow(t *testing.T) {
	now := time.Unix(1_715_740_000, 0)
	for _, delta := range []time.Duration{-59 * time.Second, 59 * time.Second} {
		ts := now.Add(delta)
		sig := httpauth.Sign(testSecret, ts, testMethod, testPath, testBody)
		if err := httpauth.Verify(testSecret, time.Minute, nowAt(now),
			tsString(ts), sig, testMethod, testPath, testBody); err != nil {
			t.Fatalf("delta=%s: %v", delta, err)
		}
	}
}

func TestVerify_ExactlyAtWindow(t *testing.T) {
	// Pin behaviour: ts == now ± maxSkew is INSIDE the window (delta <= maxSkew).
	now := time.Unix(1_715_740_000, 0)
	for _, delta := range []time.Duration{-time.Minute, time.Minute} {
		ts := now.Add(delta)
		sig := httpauth.Sign(testSecret, ts, testMethod, testPath, testBody)
		if err := httpauth.Verify(testSecret, time.Minute, nowAt(now),
			tsString(ts), sig, testMethod, testPath, testBody); err != nil {
			t.Fatalf("delta=%s: %v (boundary should be inclusive)", delta, err)
		}
	}
}

func TestVerify_WrongSecret(t *testing.T) {
	now := time.Unix(1_715_740_000, 0)
	sig := httpauth.Sign(testSecret, now, testMethod, testPath, testBody)
	err := httpauth.Verify(otherSecret, time.Minute, nowAt(now),
		tsString(now), sig, testMethod, testPath, testBody)
	if !errors.Is(err, httpauth.ErrSignatureMismatch) {
		t.Fatalf("got %v, want ErrSignatureMismatch", err)
	}
}

func TestVerify_BodyMutation(t *testing.T) {
	now := time.Unix(1_715_740_000, 0)
	sig := httpauth.Sign(testSecret, now, testMethod, testPath, testBody)
	mutated := append([]byte{}, testBody...)
	mutated = append(mutated, 'x')
	err := httpauth.Verify(testSecret, time.Minute, nowAt(now),
		tsString(now), sig, testMethod, testPath, mutated)
	if !errors.Is(err, httpauth.ErrSignatureMismatch) {
		t.Fatalf("got %v, want ErrSignatureMismatch", err)
	}
}

func TestVerify_MethodMismatch(t *testing.T) {
	now := time.Unix(1_715_740_000, 0)
	sig := httpauth.Sign(testSecret, now, "POST", testPath, testBody)
	err := httpauth.Verify(testSecret, time.Minute, nowAt(now),
		tsString(now), sig, "PUT", testPath, testBody)
	if !errors.Is(err, httpauth.ErrSignatureMismatch) {
		t.Fatalf("got %v, want ErrSignatureMismatch", err)
	}
}

func TestVerify_PathMismatch(t *testing.T) {
	now := time.Unix(1_715_740_000, 0)
	sig := httpauth.Sign(testSecret, now, testMethod, "/scan", testBody)
	err := httpauth.Verify(testSecret, time.Minute, nowAt(now),
		tsString(now), sig, testMethod, "/scan/extra", testBody)
	if !errors.Is(err, httpauth.ErrSignatureMismatch) {
		t.Fatalf("got %v, want ErrSignatureMismatch", err)
	}
}

func TestVerify_EmptyBody(t *testing.T) {
	// Library tolerates empty body; application layer rejects via httpjson.Decode.
	now := time.Unix(1_715_740_000, 0)
	sig := httpauth.Sign(testSecret, now, testMethod, testPath, nil)
	if err := httpauth.Verify(testSecret, time.Minute, nowAt(now),
		tsString(now), sig, testMethod, testPath, nil); err != nil {
		t.Fatalf("Verify with empty body: %v", err)
	}
}

func TestVerify_LengthMismatchNoShortCircuit(t *testing.T) {
	// Belt-and-braces: a 31-byte raw sig (vs the 32-byte SHA-256 output) must
	// still surface as ErrSignatureMismatch, never panic and never a different
	// error. Catches a future refactor swapping hmac.Equal for bytes.Equal.
	now := time.Unix(1_715_740_000, 0)
	badSig := base64.StdEncoding.EncodeToString(make([]byte, 31))
	err := httpauth.Verify(testSecret, time.Minute, nowAt(now),
		tsString(now), badSig, testMethod, testPath, testBody)
	if !errors.Is(err, httpauth.ErrSignatureMismatch) {
		t.Fatalf("got %v, want ErrSignatureMismatch (length-mismatch reject path)", err)
	}
}

func TestSignVerifyEnvelope_RoundTrip(t *testing.T) {
	now := time.Unix(1_715_740_000, 0)
	payload := []byte(`{"organization_id":"org-1","account_id":"acc-1","request_id":"req-1","timestamp":1715740000,"signature":""}`)
	sig := httpauth.SignEnvelope(testSecret, now, payload)
	if err := httpauth.VerifyEnvelope(testSecret, time.Minute, nowAt(now),
		now.Unix(), payload, sig); err != nil {
		t.Fatalf("VerifyEnvelope: %v", err)
	}
}

func TestVerifyEnvelope_StaleTimestamp(t *testing.T) {
	now := time.Unix(1_715_740_000, 0)
	stale := now.Add(-2 * time.Minute)
	payload := []byte(`{"hello":"world"}`)
	sig := httpauth.SignEnvelope(testSecret, stale, payload)
	err := httpauth.VerifyEnvelope(testSecret, time.Minute, nowAt(now),
		stale.Unix(), payload, sig)
	if !errors.Is(err, httpauth.ErrTimestampSkew) {
		t.Fatalf("got %v, want ErrTimestampSkew", err)
	}
}

func TestVerifyEnvelope_WrongSecret(t *testing.T) {
	now := time.Unix(1_715_740_000, 0)
	payload := []byte(`{"hello":"world"}`)
	sig := httpauth.SignEnvelope(testSecret, now, payload)
	err := httpauth.VerifyEnvelope(otherSecret, time.Minute, nowAt(now),
		now.Unix(), payload, sig)
	if !errors.Is(err, httpauth.ErrSignatureMismatch) {
		t.Fatalf("got %v, want ErrSignatureMismatch", err)
	}
}

func TestVerifyEnvelope_PayloadMutation(t *testing.T) {
	now := time.Unix(1_715_740_000, 0)
	payload := []byte(`{"hello":"world"}`)
	sig := httpauth.SignEnvelope(testSecret, now, payload)
	mutated := append([]byte{}, payload...)
	mutated[1] = 'X'
	err := httpauth.VerifyEnvelope(testSecret, time.Minute, nowAt(now),
		now.Unix(), mutated, sig)
	if !errors.Is(err, httpauth.ErrSignatureMismatch) {
		t.Fatalf("got %v, want ErrSignatureMismatch", err)
	}
}

func TestSignEnvelope_DistinctFromHTTPSign(t *testing.T) {
	// Belt-and-braces: an envelope signature MUST NOT collide with an
	// HTTP signature over the same bytes — the envelope canonical form
	// substitutes "ENVELOPE\nQUEUE" for METHOD\nPATH. If they ever
	// collided, an attacker could lift one and replay as the other.
	now := time.Unix(1_715_740_000, 0)
	payload := []byte(`{"hello":"world"}`)
	httpSig := httpauth.Sign(testSecret, now, "POST", "/scan", payload)
	envSig := httpauth.SignEnvelope(testSecret, now, payload)
	if httpSig == envSig {
		t.Fatalf("httpSig and envSig must differ; got %q for both", httpSig)
	}
}

func TestLoadFromEnv_Missing(t *testing.T) {
	const name = "AXIAOPS_TEST_HMAC_MISSING_VAR"
	t.Setenv(name, "")
	// allowEmpty=true returns nil, nil
	secret, err := httpauth.LoadFromEnv(name, true)
	if err != nil || secret != nil {
		t.Fatalf("allowEmpty path: got (%v,%v), want (nil,nil)", secret, err)
	}
	// allowEmpty=false returns descriptive error
	_, err = httpauth.LoadFromEnv(name, false)
	if err == nil {
		t.Fatalf("disallow-empty path: expected error, got nil")
	}
	if !strings.Contains(err.Error(), name) {
		t.Fatalf("error should name the env var, got %v", err)
	}
}

func TestLoadFromEnv_Malformed(t *testing.T) {
	const name = "AXIAOPS_TEST_HMAC_BAD_VAR"
	t.Setenv(name, "not-hex-at-all")
	_, err := httpauth.LoadFromEnv(name, false)
	if err == nil {
		t.Fatalf("expected error for non-hex value")
	}
}

func TestLoadFromEnv_TooShort(t *testing.T) {
	const name = "AXIAOPS_TEST_HMAC_SHORT_VAR"
	t.Setenv(name, "deadbeef") // 4 bytes, < 32
	_, err := httpauth.LoadFromEnv(name, false)
	if err == nil {
		t.Fatalf("expected error for short secret")
	}
}

func TestLoadFromEnv_OK(t *testing.T) {
	const name = "AXIAOPS_TEST_HMAC_OK_VAR"
	t.Setenv(name, "a1b2c3d4e5f60718293a4b5c6d7e8f902132435465768798a9b0c1d2e3f40516")
	secret, err := httpauth.LoadFromEnv(name, false)
	if err != nil {
		t.Fatalf("LoadFromEnv: %v", err)
	}
	if len(secret) != 32 {
		t.Fatalf("expected 32 bytes, got %d", len(secret))
	}
}

func TestLoadMaxSkew_Default(t *testing.T) {
	const name = "AXIAOPS_TEST_HMAC_SKEW_UNSET"
	t.Setenv(name, "")
	got := httpauth.LoadMaxSkew(name, 7*time.Second)
	if got != 7*time.Second {
		t.Fatalf("got %v, want 7s", got)
	}
}

func TestLoadMaxSkew_Override(t *testing.T) {
	const name = "AXIAOPS_TEST_HMAC_SKEW_OK"
	t.Setenv(name, "42")
	got := httpauth.LoadMaxSkew(name, time.Minute)
	if got != 42*time.Second {
		t.Fatalf("got %v, want 42s", got)
	}
}

func TestLoadMaxSkew_Malformed(t *testing.T) {
	const name = "AXIAOPS_TEST_HMAC_SKEW_BAD"
	t.Setenv(name, "not-a-number")
	got := httpauth.LoadMaxSkew(name, 11*time.Second)
	if got != 11*time.Second {
		t.Fatalf("got %v, want 11s (default fallback)", got)
	}
}
