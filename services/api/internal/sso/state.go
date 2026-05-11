package sso

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"axiaops.io/shared/cache"
)

// stateTTL caps how long a generated state remains redeemable. 10min is the
// OIDC ceremony TTL chosen for design §7.5: long enough to absorb a slow IdP
// step (MFA + group-claim resolution + user re-confirm), short enough that
// an abandoned login doesn't leave litter at scale.
const stateTTL = 10 * time.Minute

// pkceVerifierBytes sizes the PKCE code_verifier. RFC 7636 §4.1 requires
// 43–128 chars from [A-Z][a-z][0-9]-._~. 96 random bytes encodes to 128
// base64url chars — the upper end of the spec for max entropy.
const pkceVerifierBytes = 96

// stateKeyPrefix namespaces ceremony state in the cache layer. Kept private
// because callers go through StateStore.Persist / Consume — encoding is an
// implementation detail.
const stateKeyPrefix = "sso:state:"

// StateData is the per-login state captured by initiate and consumed by
// callback. Persisted as JSON at sso:state:{state}. Each field is essential
// to the callback's validation surface — adding a field is a versioning
// concern (a callback running an older binary will silently drop the new
// field on Unmarshal).
type StateData struct {
	// CID binds the state to a specific connection so an attacker can't
	// redirect a state issued for connection A's callback to connection B's.
	CID string `json:"cid"`
	// CodeVerifier is the PKCE secret. The callback POSTs it to the token
	// endpoint; the IdP cross-checks against the code_challenge it stored
	// at authorize time. Without this the auth code is unredeemable.
	CodeVerifier string `json:"code_verifier"`
	// Nonce binds the ID token to the auth request. The callback passes
	// it to Validator.ValidateIDToken; mismatch is a hard reject.
	Nonce string `json:"nonce"`
	// RedirectAfterLogin is the post-login URL the callback redirects to.
	// Captured at initiate time and read FROM STATE (never from the
	// callback's query string) so an attacker can't swap it mid-flight —
	// open-redirect fuzz acceptance criterion (architect N4).
	RedirectAfterLogin string `json:"redirect_after_login,omitempty"`
	// ExpiresAt is the absolute expiry. Cache TTL is the primary cleanup
	// mechanism; this field is a defense in depth for cache-TTL drift
	// (Redis lag, clock skew) and for in-memory caches that don't TTL.
	ExpiresAt time.Time `json:"expires_at"`
}

// StateStore persists single-use OIDC ceremony state. Backed by cache.Cache;
// single-use semantics enforced by Consume (Get+Del).
type StateStore struct {
	cache cache.Cache
}

// NewStateStore wires a StateStore over the provided cache.
func NewStateStore(c cache.Cache) *StateStore {
	return &StateStore{cache: c}
}

// ErrStateNotFound is returned by Consume when the state token is unknown,
// expired, or already consumed. Callers must treat all three identically —
// distinguishing them is a side channel for an attacker probing whether a
// given state was ever issued.
var ErrStateNotFound = errors.New("sso: state not found")

// GenerateState returns a (state, StateData) pair. CodeVerifier and Nonce
// are generated fresh; the caller derives the PKCE code_challenge for the
// authorization URL via CodeChallenge(verifier). Caller persists the
// returned StateData via StateStore.Persist before redirecting to the IdP.
func GenerateState(cid, redirectAfterLogin string) (string, StateData, error) {
	state, err := randomBase64URL(32)
	if err != nil {
		return "", StateData{}, fmt.Errorf("sso: generate state: %w", err)
	}
	verifier, err := randomBase64URL(pkceVerifierBytes)
	if err != nil {
		return "", StateData{}, fmt.Errorf("sso: generate pkce verifier: %w", err)
	}
	nonce, err := randomBase64URL(32)
	if err != nil {
		return "", StateData{}, fmt.Errorf("sso: generate nonce: %w", err)
	}
	return state, StateData{
		CID:                cid,
		CodeVerifier:       verifier,
		Nonce:              nonce,
		RedirectAfterLogin: redirectAfterLogin,
		ExpiresAt:          time.Now().Add(stateTTL),
	}, nil
}

// Persist stores StateData under sso:state:{state} with stateTTL.
func (s *StateStore) Persist(ctx context.Context, state string, data StateData) error {
	if state == "" {
		return errors.New("sso: state token must be non-empty")
	}
	body, err := json.Marshal(data)
	if err != nil {
		return fmt.Errorf("sso: marshal state: %w", err)
	}
	if err := s.cache.Set(ctx, stateKeyPrefix+state, body, stateTTL); err != nil {
		return fmt.Errorf("sso: persist state: %w", err)
	}
	return nil
}

// Consume atomically reads + deletes the state. Returns ErrStateNotFound on
// miss, already-consumed, or expired. Audit M-2: two concurrent Consume
// calls on the same state token race to a single winner — the loser gets
// ErrStateNotFound, NOT a stale copy of the state.
//
// Why atomic matters: without it, a compromised IdP that issued reusable
// authorization codes could replay a callback against the same state token
// (two parallel callbacks both Get the state, both validate, both mint a
// session). Single-use of the auth code is the IdP's responsibility; we
// don't get to depend on it.
//
// Backed by cache.Cache.GetDel — Redis 6.2+ GETDEL on the redis backend,
// mutex-protected delete-after-read on the memory backend.
func (s *StateStore) Consume(ctx context.Context, state string) (StateData, error) {
	if state == "" {
		return StateData{}, ErrStateNotFound
	}
	key := stateKeyPrefix + state
	body, err := s.cache.GetDel(ctx, key)
	if errors.Is(err, cache.ErrNotFound) {
		return StateData{}, ErrStateNotFound
	}
	if err != nil {
		return StateData{}, fmt.Errorf("sso: read state: %w", err)
	}
	var data StateData
	if err := json.Unmarshal(body, &data); err != nil {
		return StateData{}, fmt.Errorf("sso: unmarshal state: %w", err)
	}
	// Defense in depth against cache-TTL drift and in-memory caches that
	// don't honour Set TTLs precisely. Under GETDEL the entry is already
	// gone, so an expired hit is effectively just a costly miss — no
	// remediation needed beyond returning ErrStateNotFound.
	if time.Now().After(data.ExpiresAt) {
		return StateData{}, ErrStateNotFound
	}
	return data, nil
}

// CodeChallenge derives the PKCE code_challenge from the verifier using S256
// per RFC 7636 §4.2: base64url(sha256(verifier)). The caller passes this to
// the IdP authorize URL alongside code_challenge_method=S256.
func CodeChallenge(verifier string) string {
	sum := sha256.Sum256([]byte(verifier))
	return base64.RawURLEncoding.EncodeToString(sum[:])
}

// randomBase64URL returns n bytes of secure random data, base64url-encoded.
func randomBase64URL(n int) (string, error) {
	buf := make([]byte, n)
	if _, err := rand.Read(buf); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(buf), nil
}
