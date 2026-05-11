package auth_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"axiaops.io/api/internal/auth"
	"axiaops.io/shared/cache"
	"axiaops.io/shared/model"
)

// erroringCache is a cache.Cache that fails every operation. Stands in
// for a Redis backend that's unreachable / returning errors (network
// blip, OOM, password rotation, etc). The session cache-aside path
// must degrade gracefully: log + count + fall through to PG, never
// surface the error to the caller. Plan §4.6 acceptance — Redis
// outage simulation.
type erroringCache struct{}

var errCacheOutage = errors.New("simulated cache outage")

func (erroringCache) Get(context.Context, string) ([]byte, error)              { return nil, errCacheOutage }
func (erroringCache) Set(context.Context, string, []byte, time.Duration) error { return errCacheOutage }
func (erroringCache) Del(context.Context, string) error                        { return errCacheOutage }
func (erroringCache) GetDel(context.Context, string) ([]byte, error)           { return nil, errCacheOutage }
func (erroringCache) Incr(context.Context, string, time.Duration) (int64, error) {
	return 0, errCacheOutage
}
func (erroringCache) Ping(context.Context) error { return errCacheOutage }
func (erroringCache) Close() error               { return nil }

func TestSessionValidation_FailsOpenOnCacheOutage(t *testing.T) {
	// Plan §4.6 / architect cache-outage acceptance: when the cache
	// backend is unreachable, ValidateSession must still succeed via
	// the PG-only path. The user response is unaffected; the
	// degradation is invisible at the wire and only surfaces in the
	// log + the SessionCacheErrorsTotal counter (asserted by the
	// counter integration in slice 2).
	t.Parallel()
	store := newFakeStore()
	mgr := auth.NewManager(store, auth.NewSessionCache(erroringCache{}), auth.Config{
		TTL:             time.Hour,
		SessionsPerUser: 0,
	})

	// MintSession also goes through the cache (Put on success); the
	// erroringCache fails the Set silently. The session row IS
	// created in PG via the store, so the next Validate works.
	mr, err := mgr.MintSession(context.Background(), auth.MintRequest{
		UserID: "u-outage", OrganizationID: "org-outage", AuthMode: model.AuthModePassword,
	})
	if err != nil {
		t.Fatalf("MintSession with outaged cache: %v (must fail open)", err)
	}

	// Validate must succeed via PG fallthrough — cache.Get errors,
	// the helper returns ErrSessionCacheMiss, the manager SELECTs PG.
	got, err := mgr.ValidateSession(context.Background(), mr.PlaintextToken)
	if err != nil {
		t.Fatalf("ValidateSession with outaged cache: %v (must fail open)", err)
	}
	if got.ID != mr.Session.ID {
		t.Errorf("validated session id = %q; want %q", got.ID, mr.Session.ID)
	}

	// Revoke also goes through the cache (Del). The Del errors but
	// the PG write must still succeed — RevokeSession returns nil
	// regardless of cache state.
	if err := mgr.RevokeSession(context.Background(), mr.Session.ID, mr.Session.SessionTokenHash, auth.RevokeReasonLogout); err != nil {
		t.Fatalf("RevokeSession with outaged cache: %v (must fail open)", err)
	}
	// And the next Validate must reflect the revoke (PG row is
	// authoritative; cache miss + Live() check on PG row catches it).
	if _, err := mgr.ValidateSession(context.Background(), mr.PlaintextToken); err == nil {
		t.Error("expected ValidateSession to reject revoked session even with outaged cache")
	}
}

// Compile-time guarantee that erroringCache satisfies cache.Cache.
var _ cache.Cache = erroringCache{}
