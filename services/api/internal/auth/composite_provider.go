package auth

import (
	"net/http"
)

// CompositeProvider tries each delegate Provider in order and returns the
// first non-error Identity. The transitional `AUTH_PROVIDER=both` mode
// composes [nativeProvider, kindeProvider] so a rolling deploy can land
// without flapping: replicas that have already cut over to native still
// honour Kinde Bearer tokens issued by replicas that haven't.
//
// Architect S1 / plan §4.5: `both` is the *only* safe transitional value
// during a rolling restart from `kinde` → `native`. The deploy runbook
// must move kinde → both → native, never kinde → native directly.
//
// Failure semantics: if every delegate returns an error, CompositeProvider
// returns ErrUnauthenticated. Per-delegate error reasons stay internal —
// the middleware (WrapNative) maps the final error to HTTP 401 with no
// body content beyond "unauthenticated".
type CompositeProvider struct {
	providers []Provider
}

// NewCompositeProvider chains the supplied providers. Order matters: the
// first provider that successfully authenticates wins. For the strangler
// `both` mode, pass [native, kinde] — the cookie-bearing requests are the
// hot path and should be tried first.
//
// Panics if no providers are supplied — a CompositeProvider with zero
// delegates would 401 every request, which is a config bug, not a
// runtime condition worth surfacing as a request error.
func NewCompositeProvider(providers ...Provider) *CompositeProvider {
	if len(providers) == 0 {
		panic("auth: CompositeProvider requires at least one delegate Provider")
	}
	return &CompositeProvider{providers: providers}
}

// Authenticate runs each delegate in turn. Returns on the first success.
// All failures collapse to ErrUnauthenticated regardless of the
// underlying reason — the rationale is the same as in NativeProvider:
// don't leak which path failed (an attacker with a stolen native cookie
// shouldn't learn anything from probing with a malformed JWT).
func (c *CompositeProvider) Authenticate(r *http.Request) (Identity, error) {
	for _, p := range c.providers {
		id, err := p.Authenticate(r)
		if err == nil {
			return id, nil
		}
	}
	return Identity{}, ErrUnauthenticated
}
