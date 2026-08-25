// Package httpauth implements shared-secret HMAC-SHA256 authentication for
// AxiaOps service-to-service hops. The current consumer is the api → ingestion
// hop (api signs POST /scan + POST /v1/credentials/verify; ingestion verifies).
// The package is intentionally narrow — every export is on the hot path of one
// of those two flows, plus the Redis-envelope sibling that closes the queue
// signing surface (see SignEnvelope / VerifyEnvelope below).
//
// Protocol:
//
//	canonical := unix_ts_seconds + "\n" + METHOD + "\n" + PATH + "\n" + body
//	sig       := base64.StdEncoding.EncodeToString(HMAC_SHA256(secret, canonical))
//
// Headers on the wire:
//
//	X-AxiaOps-Ingestion-Timestamp: <unix seconds>
//	X-AxiaOps-Ingestion-Signature: <base64 HMAC-SHA256>
//
package httpauth

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"strconv"
	"strings"
	"time"
)

// DefaultMaxSkew is the default replay window. Both ends use it as the
// default; the verifier takes maxSkew as a parameter so tests can shrink
// it for deterministic skew assertions.
const DefaultMaxSkew = 5 * time.Minute

// MaxBodyBytes caps the request body the verifier will read. Matches the
// H-4 64 KiB cap and prevents an attacker from sending a multi-GB request
// to OOM the verifier before the signature check.
const MaxBodyBytes = 64 << 10

// SignatureAlgorithm is the identifier surfaced in the WWW-Authenticate
// header and metric labels.
const SignatureAlgorithm = "HMAC-SHA256"

// Header names. Exported as constants so caller and receiver agree at
// compile time — never hand-type the strings.
const (
	HeaderTimestamp = "X-AxiaOps-Ingestion-Timestamp"
	HeaderSignature = "X-AxiaOps-Ingestion-Signature"
)

// envelopeSentinel is the fixed string substituted for METHOD\nPATH in the
// Redis envelope canonical form, so an envelope signature cannot be reused
// as an HTTP signature and vice versa.
const envelopeSentinel = "ENVELOPE\nQUEUE"

// Sentinel errors so callers can branch on the failure mode (the ingestion
// middleware emits one Prometheus label per kind).
var (
	ErrMissingTimestamp   = errors.New("httpauth: missing timestamp header")
	ErrMissingSignature   = errors.New("httpauth: missing signature header")
	ErrMalformedTimestamp = errors.New("httpauth: malformed timestamp header")
	ErrMalformedSignature = errors.New("httpauth: malformed signature header")
	ErrTimestampSkew      = errors.New("httpauth: timestamp outside skew window")
	ErrSignatureMismatch  = errors.New("httpauth: signature mismatch")
)

// Sign returns the base64-encoded HMAC-SHA256 of the canonical string for
// (timestamp, method, path, body). Used by callers (api side).
func Sign(secret []byte, timestamp time.Time, method, path string, body []byte) string {
	ts := strconv.FormatInt(timestamp.Unix(), 10)
	mac := hmac.New(sha256.New, secret)
	_, _ = mac.Write([]byte(ts))
	_, _ = mac.Write([]byte{'\n'})
	_, _ = mac.Write([]byte(method))
	_, _ = mac.Write([]byte{'\n'})
	_, _ = mac.Write([]byte(path))
	_, _ = mac.Write([]byte{'\n'})
	_, _ = mac.Write(body)
	return base64.StdEncoding.EncodeToString(mac.Sum(nil))
}

// Verify checks an inbound request's signature against the supplied secret.
// Returns nil on success, one of the sentinel errors above on failure.
// `now` is injected so tests can drive deterministic skew assertions —
// production callers pass time.Now.
func Verify(
	secret []byte,
	maxSkew time.Duration,
	now func() time.Time,
	timestampHeader, signatureHeader string,
	method, path string,
	body []byte,
) error {
	if timestampHeader == "" {
		return ErrMissingTimestamp
	}
	if signatureHeader == "" {
		return ErrMissingSignature
	}
	tsSecs, err := strconv.ParseInt(timestampHeader, 10, 64)
	if err != nil {
		return ErrMalformedTimestamp
	}
	provided, err := base64.StdEncoding.DecodeString(signatureHeader)
	if err != nil {
		return ErrMalformedSignature
	}
	ts := time.Unix(tsSecs, 0)
	cur := now()
	delta := cur.Sub(ts)
	if delta < 0 {
		delta = -delta
	}
	if delta > maxSkew {
		return ErrTimestampSkew
	}
	expected := signRaw(secret, ts, method, path, body)
	if !hmac.Equal(expected, provided) {
		return ErrSignatureMismatch
	}
	return nil
}

// signRaw returns the unencoded MAC bytes for (timestamp, method, path, body).
// Internal — callers use Sign for the base64 wire form.
func signRaw(secret []byte, timestamp time.Time, method, path string, body []byte) []byte {
	ts := strconv.FormatInt(timestamp.Unix(), 10)
	mac := hmac.New(sha256.New, secret)
	_, _ = mac.Write([]byte(ts))
	_, _ = mac.Write([]byte{'\n'})
	_, _ = mac.Write([]byte(method))
	_, _ = mac.Write([]byte{'\n'})
	_, _ = mac.Write([]byte(path))
	_, _ = mac.Write([]byte{'\n'})
	_, _ = mac.Write(body)
	return mac.Sum(nil)
}

// SignEnvelope signs a non-HTTP payload (Redis queue envelope today). The
// canonical encoding mirrors Sign but substitutes a fixed sentinel for the
// METHOD+PATH block so an envelope sig cannot be reused as an HTTP sig.
//
// Layout:
//
//	canonical := unix_ts_seconds + "\n" + "ENVELOPE\nQUEUE" + "\n" + payload
func SignEnvelope(secret []byte, timestamp time.Time, payload []byte) string {
	ts := strconv.FormatInt(timestamp.Unix(), 10)
	mac := hmac.New(sha256.New, secret)
	_, _ = mac.Write([]byte(ts))
	_, _ = mac.Write([]byte{'\n'})
	_, _ = mac.Write([]byte(envelopeSentinel))
	_, _ = mac.Write([]byte{'\n'})
	_, _ = mac.Write(payload)
	return base64.StdEncoding.EncodeToString(mac.Sum(nil))
}

// VerifyEnvelope checks a Redis queue envelope's signature. timestamp is the
// caller-provided unix seconds; payload is the serialised envelope WITHOUT the
// signature field (the caller blanks it before re-serialising — see
// services/shared/queue for the "blank-then-resign" pattern).
func VerifyEnvelope(
	secret []byte,
	maxSkew time.Duration,
	now func() time.Time,
	timestamp int64,
	payload []byte,
	signature string,
) error {
	if signature == "" {
		return ErrMissingSignature
	}
	provided, err := base64.StdEncoding.DecodeString(signature)
	if err != nil {
		return ErrMalformedSignature
	}
	ts := time.Unix(timestamp, 0)
	cur := now()
	delta := cur.Sub(ts)
	if delta < 0 {
		delta = -delta
	}
	if delta > maxSkew {
		return ErrTimestampSkew
	}
	mac := hmac.New(sha256.New, secret)
	tsStr := strconv.FormatInt(timestamp, 10)
	_, _ = mac.Write([]byte(tsStr))
	_, _ = mac.Write([]byte{'\n'})
	_, _ = mac.Write([]byte(envelopeSentinel))
	_, _ = mac.Write([]byte{'\n'})
	_, _ = mac.Write(payload)
	expected := mac.Sum(nil)
	if !hmac.Equal(expected, provided) {
		return ErrSignatureMismatch
	}
	return nil
}

// LoadFromEnv reads a hex-encoded shared secret from envName and decodes it
// into raw bytes. When allowEmpty is true and the env var is unset / empty,
// returns (nil, nil) and emits a slog.Warn (composition root logs that the
// DEV_MODE bypass is engaged). Otherwise, an unset or malformed value
// returns a descriptive error so the composition root can die() loudly.
//
// The returned secret is guaranteed to be ≥ 32 bytes (256 bits) when non-nil.
func LoadFromEnv(envName string, allowEmpty bool) ([]byte, error) {
	v := os.Getenv(envName)
	if v == "" {
		if allowEmpty {
			slog.Warn("httpauth: shared secret empty — caller-allowed (DEV_MODE)", "var", envName)
			return nil, nil
		}
		return nil, fmt.Errorf("httpauth: %s is required (set to 32-byte hex; generate with `openssl rand -hex 32`)", envName)
	}
	secret, err := hex.DecodeString(v)
	if err != nil {
		return nil, fmt.Errorf("httpauth: %s must be hex (got %d chars): %w", envName, len(v), err)
	}
	if len(secret) < 32 {
		return nil, fmt.Errorf("httpauth: %s must decode to ≥ 32 bytes (64 hex chars), got %d", envName, len(secret))
	}
	// Safe diagnostic: length + first/last 4 hex chars, NEVER the full secret.
	slog.Info("httpauth: shared secret loaded",
		"var", envName,
		"bytes", len(secret),
		"fingerprint", v[:4]+"..."+v[len(v)-4:],
	)
	return secret, nil
}

// LoadMaxSkew reads a duration-tunable env var (in whole seconds) and
// returns the parsed value or the supplied default. Malformed values fall
// back to the default with a slog.Warn so a typo never wedges enforcement
// at boot.
func LoadMaxSkew(envName string, defaultSkew time.Duration) time.Duration {
	v := strings.TrimSpace(os.Getenv(envName))
	if v == "" {
		return defaultSkew
	}
	secs, err := strconv.Atoi(v)
	if err != nil || secs <= 0 {
		slog.Warn("httpauth: invalid max-skew env, using default",
			"var", envName,
			"value", v,
			"default_seconds", int(defaultSkew/time.Second),
		)
		return defaultSkew
	}
	return time.Duration(secs) * time.Second
}

// reasonLabel reports the metric label for one of the sentinel errors.
// "missing_header" collapses ErrMissingTimestamp and ErrMissingSignature
// (operators care that "a header was absent," not which one).
func reasonLabel(err error) string {
	switch {
	case errors.Is(err, ErrMissingTimestamp), errors.Is(err, ErrMissingSignature):
		return "missing_header"
	case errors.Is(err, ErrMalformedTimestamp), errors.Is(err, ErrMalformedSignature):
		return "malformed"
	case errors.Is(err, ErrTimestampSkew):
		return "timestamp_skew"
	case errors.Is(err, ErrSignatureMismatch):
		return "signature_mismatch"
	default:
		return "unknown"
	}
}

// ResponseBodyTooLarge is the JSON body returned when the request exceeded
// MaxBodyBytes. Kept as a constant so callers and tests share the spelling.
const ResponseBodyTooLarge = `{"error":"request_body_too_large"}`

// ResponseBadRequest is the JSON body for a generic body-read failure
// (non-413 read error from the body cap reader).
const ResponseBadRequest = `{"error":"bad_request"}`

// ResponseUnauthorised is the JSON body returned on any HMAC verification
// failure. Opaque on purpose — the failure mode is internal-observability
// only (slog + Prometheus), never echoed to the caller.
const ResponseUnauthorised = `{"error":"ingestion_unauthorised"}`

// writeUnauthorised writes the standard 401 response with the
// WWW-Authenticate hint. Kept tiny so the middleware reads as intent.
func writeUnauthorised(w http.ResponseWriter) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("WWW-Authenticate", SignatureAlgorithm)
	w.WriteHeader(http.StatusUnauthorized)
	_, _ = w.Write([]byte(ResponseUnauthorised))
}
