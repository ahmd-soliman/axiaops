package sso_test

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"errors"
	"strings"
	"testing"

	"axiaops.io/api/internal/sso"
)

// ─── PKCE primitives ────────────────────────────────────────────────────────

func TestCodeChallenge_MatchesRFC7636S256(t *testing.T) {
	// RFC 7636 §4.2: code_challenge = BASE64URL(SHA256(code_verifier)).
	verifier := "dBjftJeZ4CVP-mB92K27uhbUJU1p1r_wW1gFWFOEjXk"
	want := base64.RawURLEncoding.EncodeToString(sha256ash(verifier))
	got := sso.CodeChallenge(verifier)
	if got != want {
		t.Fatalf("CodeChallenge mismatch:\n got=%q\nwant=%q", got, want)
	}
}

func sha256ash(s string) []byte {
	h := sha256.Sum256([]byte(s))
	return h[:]
}

// ─── GenerateState ──────────────────────────────────────────────────────────

func TestGenerateState_ReturnsDistinctTokens(t *testing.T) {
	state1, data1, err := sso.GenerateState("conn-1", "")
	if err != nil {
		t.Fatalf("GenerateState: %v", err)
	}
	state2, data2, err := sso.GenerateState("conn-1", "")
	if err != nil {
		t.Fatalf("GenerateState: %v", err)
	}
	if state1 == state2 {
		t.Errorf("two GenerateState calls returned the same state: %q", state1)
	}
	if data1.CodeVerifier == data2.CodeVerifier {
		t.Errorf("two GenerateState calls returned the same code_verifier")
	}
	if data1.Nonce == data2.Nonce {
		t.Errorf("two GenerateState calls returned the same nonce")
	}
}

func TestGenerateState_VerifierIsRFCCompliant(t *testing.T) {
	// RFC 7636 §4.1: verifier must be 43–128 chars from the unreserved set
	// [A-Z a-z 0-9 - . _ ~]. randomBase64URL uses the base64url alphabet
	// [A-Z a-z 0-9 - _], a strict subset — so the assertion is positive.
	_, data, err := sso.GenerateState("conn-1", "")
	if err != nil {
		t.Fatalf("GenerateState: %v", err)
	}
	n := len(data.CodeVerifier)
	if n < 43 || n > 128 {
		t.Errorf("verifier length %d outside RFC 7636 range [43,128]", n)
	}
	const allowed = "ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz0123456789-_"
	for i, r := range data.CodeVerifier {
		if !strings.ContainsRune(allowed, r) {
			t.Errorf("verifier char %d (%q) not in RFC 7636 unreserved set: %q", i, r, data.CodeVerifier)
			break
		}
	}
}

func TestGenerateState_PropagatesCIDAndRedirect(t *testing.T) {
	_, data, err := sso.GenerateState("conn-42", "/dashboard/zombies")
	if err != nil {
		t.Fatalf("GenerateState: %v", err)
	}
	if data.CID != "conn-42" {
		t.Errorf("CID: got %q want conn-42", data.CID)
	}
	if data.RedirectAfterLogin != "/dashboard/zombies" {
		t.Errorf("RedirectAfterLogin: got %q want /dashboard/zombies", data.RedirectAfterLogin)
	}
	if data.ExpiresAt.IsZero() {
		t.Errorf("ExpiresAt was zero")
	}
}

// ─── Persist + Consume ──────────────────────────────────────────────────────

func TestStateStore_PersistAndConsume_RoundTrips(t *testing.T) {
	ss := sso.NewStateStore(newMockCache())
	state, data, err := sso.GenerateState("conn-1", "/x")
	if err != nil {
		t.Fatalf("GenerateState: %v", err)
	}
	if err := ss.Persist(context.Background(), state, data); err != nil {
		t.Fatalf("Persist: %v", err)
	}
	got, err := ss.Consume(context.Background(), state)
	if err != nil {
		t.Fatalf("Consume: %v", err)
	}
	if got.CID != data.CID || got.CodeVerifier != data.CodeVerifier ||
		got.Nonce != data.Nonce || got.RedirectAfterLogin != data.RedirectAfterLogin {
		t.Errorf("StateData round-trip mismatch: got %+v want %+v", got, data)
	}
}

func TestStateStore_Consume_IsSingleUse(t *testing.T) {
	ss := sso.NewStateStore(newMockCache())
	state, data, err := sso.GenerateState("conn-1", "")
	if err != nil {
		t.Fatalf("GenerateState: %v", err)
	}
	if err := ss.Persist(context.Background(), state, data); err != nil {
		t.Fatalf("Persist: %v", err)
	}
	if _, err := ss.Consume(context.Background(), state); err != nil {
		t.Fatalf("first Consume: %v", err)
	}
	_, err = ss.Consume(context.Background(), state)
	if !errors.Is(err, sso.ErrStateNotFound) {
		t.Fatalf("second Consume should return ErrStateNotFound, got %v", err)
	}
}

func TestStateStore_Consume_UnknownStateReturnsNotFound(t *testing.T) {
	ss := sso.NewStateStore(newMockCache())
	_, err := ss.Consume(context.Background(), "never-issued")
	if !errors.Is(err, sso.ErrStateNotFound) {
		t.Fatalf("Consume on unknown state: got %v want ErrStateNotFound", err)
	}
}

func TestStateStore_Consume_EmptyStateReturnsNotFound(t *testing.T) {
	ss := sso.NewStateStore(newMockCache())
	_, err := ss.Consume(context.Background(), "")
	if !errors.Is(err, sso.ErrStateNotFound) {
		t.Fatalf("Consume on empty state: got %v want ErrStateNotFound", err)
	}
}

func TestStateStore_Persist_RejectsEmptyState(t *testing.T) {
	ss := sso.NewStateStore(newMockCache())
	err := ss.Persist(context.Background(), "", sso.StateData{})
	if err == nil {
		t.Fatalf("Persist with empty state should error")
	}
}
