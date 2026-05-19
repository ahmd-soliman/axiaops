package redis_test

import (
	"encoding/json"
	"errors"
	"testing"
	"time"

	"axiaops.io/shared/httpauth"
	redisqueue "axiaops.io/shared/queue/redis"
)

var testSecret = []byte("0123456789abcdef0123456789abcdef") // 32 bytes

// signedJobBytes is the round-trip envelope-serialise helper the tests use to
// emulate what the Enqueue path writes onto the wire. It is intentionally
// independent of the production Enqueue so a test failure here pins a regression
// in either the public sign protocol or the round-trip behaviour.
func signedJobBytes(t *testing.T, secret []byte, base redisqueue.ScanJob, now time.Time) ([]byte, redisqueue.ScanJob) {
	t.Helper()
	base.Timestamp = now.Unix()
	base.Signature = ""
	payload, err := json.Marshal(base)
	if err != nil {
		t.Fatalf("marshal-for-sign: %v", err)
	}
	base.Signature = httpauth.SignEnvelope(secret, now, payload)
	wire, err := json.Marshal(base)
	if err != nil {
		t.Fatalf("marshal-for-wire: %v", err)
	}
	return wire, base
}

func TestVerifyEnvelope_RoundTrip(t *testing.T) {
	now := time.Unix(1_715_740_000, 0)
	base := redisqueue.ScanJob{
		OrganizationID: "org-1",
		AccountID:      "acc-1",
		EnqueuedAt:     now,
		RequestID:      "req-1",
	}
	wire, signed := signedJobBytes(t, testSecret, base, now)

	// Decode back exactly like Dequeue does.
	var got redisqueue.ScanJob
	if err := json.Unmarshal(wire, &got); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if got.Signature != signed.Signature {
		t.Fatalf("signature lost on round-trip: got %q want %q", got.Signature, signed.Signature)
	}

	if err := redisqueue.VerifyEnvelope(testSecret, time.Minute,
		func() time.Time { return now }, got); err != nil {
		t.Fatalf("VerifyEnvelope: %v", err)
	}
}

func TestVerifyEnvelope_MissingSignature(t *testing.T) {
	now := time.Unix(1_715_740_000, 0)
	job := redisqueue.ScanJob{OrganizationID: "o", AccountID: "a", Timestamp: now.Unix()}
	err := redisqueue.VerifyEnvelope(testSecret, time.Minute,
		func() time.Time { return now }, job)
	if !errors.Is(err, httpauth.ErrMissingSignature) {
		t.Fatalf("got %v, want ErrMissingSignature", err)
	}
}

func TestVerifyEnvelope_TamperedField(t *testing.T) {
	now := time.Unix(1_715_740_000, 0)
	base := redisqueue.ScanJob{
		OrganizationID: "org-1",
		AccountID:      "acc-1",
		EnqueuedAt:     now,
		RequestID:      "req-1",
	}
	wire, _ := signedJobBytes(t, testSecret, base, now)

	var got redisqueue.ScanJob
	if err := json.Unmarshal(wire, &got); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	got.OrganizationID = "attacker-org" // tamper after signing

	err := redisqueue.VerifyEnvelope(testSecret, time.Minute,
		func() time.Time { return now }, got)
	if !errors.Is(err, httpauth.ErrSignatureMismatch) {
		t.Fatalf("got %v, want ErrSignatureMismatch", err)
	}
}

func TestVerifyEnvelope_NilSecretPassthrough(t *testing.T) {
	// DEV_MODE: nil secret means both ends agreed to no-sign. Verify must
	// tolerate any input (incl. missing signature) so worker startup
	// against an unsigned queue keeps working.
	now := time.Unix(1_715_740_000, 0)
	job := redisqueue.ScanJob{OrganizationID: "o", AccountID: "a"}
	if err := redisqueue.VerifyEnvelope(nil, time.Minute,
		func() time.Time { return now }, job); err != nil {
		t.Fatalf("nil-secret passthrough should return nil, got %v", err)
	}
}

func TestVerifyEnvelope_StaleTimestamp(t *testing.T) {
	now := time.Unix(1_715_740_000, 0)
	stale := now.Add(-2 * time.Minute)
	base := redisqueue.ScanJob{OrganizationID: "o", AccountID: "a"}
	wire, _ := signedJobBytes(t, testSecret, base, stale)

	var got redisqueue.ScanJob
	if err := json.Unmarshal(wire, &got); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	err := redisqueue.VerifyEnvelope(testSecret, time.Minute,
		func() time.Time { return now }, got)
	if !errors.Is(err, httpauth.ErrTimestampSkew) {
		t.Fatalf("got %v, want ErrTimestampSkew", err)
	}
}
