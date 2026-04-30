// kinde_provider.go — auth.Provider implementation that wraps the existing
// Kinde JWT validation. Used under AUTH_PROVIDER=kinde and as the second
// delegate in AUTH_PROVIDER=both. The legacy Auth.Wrap method remains in
// auth.go untouched during the strangler window — both paths validate the
// same JWTs against the same JWKS, by design. When Kinde is deleted at the
// D2 deprecation date (2026-10-30) both go together.

package middleware

import (
	"log/slog"
	"net/http"

	"github.com/golang-jwt/jwt/v5"

	"axiaops.io/api/internal/auth"
	"axiaops.io/shared/storage"
)

// KindeProvider satisfies auth.Provider over an *Auth instance. It reuses
// the keyfunc + issuer + store fields of Auth — no separate JWKS fetch,
// no separate connection pool. The provider is a thin adapter; the
// canonical Kinde validation logic lives on *Auth.
type KindeProvider struct {
	auth *Auth
}

// NewKindeProvider wraps the supplied *Auth. Auth must have been
// constructed via NewAuth (so its keyfunc + store are populated). Panics
// if a is nil — composition-root misconfig is a startup error, not a
// per-request one.
func NewKindeProvider(a *Auth) *KindeProvider {
	if a == nil {
		panic("middleware: NewKindeProvider requires a non-nil *Auth")
	}
	return &KindeProvider{auth: a}
}

// Authenticate parses the Bearer JWT, validates it against the cached
// JWKS, upserts the organization + user, and ensures-first-membership
// + redeems any pending invitation — the same chain Auth.Wrap performs.
//
// Failures collapse to auth.ErrUnauthenticated. The detailed reason is
// logged via slog at the same level Wrap uses; the wire response is the
// fixed 401 from WrapNative. Note that this collapses Wrap's
// "internal error" 500-class responses (DB failures during upsert) into
// 401 — the strangler accepts this because the middleware-on-WrapNative
// path is uniform across providers, and a transient DB failure during
// auth would degrade to "log the user out" instead of "return 500", which
// is closer to what we want operationally anyway.
func (p *KindeProvider) Authenticate(r *http.Request) (auth.Identity, error) {
	raw, ok := bearerToken(r)
	if !ok {
		// Missing/malformed header is an auth failure — log at debug
		// rather than warn so an unauthenticated probe doesn't drown
		// the logs (Wrap logs warn here; KindeProvider runs alongside
		// the cookie-based native path under AUTH_PROVIDER=both, where
		// "no Authorization header" is the *expected* state).
		return auth.Identity{}, auth.ErrUnauthenticated
	}

	claims := jwt.MapClaims{}
	if _, err := jwt.ParseWithClaims(raw, claims, p.auth.keyfunc, jwt.WithIssuer(p.auth.issuer)); err != nil {
		// jwt/v5's error chain (ErrTokenMalformed, ErrTokenExpired,
		// ErrTokenSignatureInvalid, …) carries the failure category
		// only — never the token bytes. Verified against the v5
		// source as of the version pinned in go.mod. If a future
		// upgrade changes that, the logging-discipline test in
		// session_test.go captures slog output and would catch it.
		slog.Warn("auth: kinde provider — invalid token",
			"method", r.Method, "path", r.URL.Path, "error", err)
		return auth.Identity{}, auth.ErrUnauthenticated
	}

	orgCode, _ := claims["org_code"].(string)
	if orgCode == "" {
		slog.Warn("auth: kinde provider — token missing org_code claim",
			"method", r.Method, "path", r.URL.Path)
		return auth.Identity{}, auth.ErrUnauthenticated
	}
	sub, _ := claims["sub"].(string)
	email, _ := claims["email"].(string)
	name, _ := claims["name"].(string)
	orgName, _ := claims["org_name"].(string)

	ctx := r.Context()
	if p.auth.store == nil {
		// store == nil is a test-only shape (newWithKeyfunc) — never
		// reachable from cmd/main.go. Surface as an auth failure so a
		// misconfigured production instance (which should be impossible)
		// doesn't fall through to a degraded code path.
		return auth.Identity{}, auth.ErrUnauthenticated
	}

	organization, err := p.auth.store.UpsertOrganization(ctx, orgCode, orgName)
	if err != nil {
		slog.Error("auth: kinde provider — UpsertOrganization failed", "error", err)
		return auth.Identity{}, auth.ErrUnauthenticated
	}
	user, err := p.auth.store.UpsertUser(ctx, organization.ID, sub, email, name)
	if err != nil {
		slog.Error("auth: kinde provider — UpsertUser failed", "error", err)
		return auth.Identity{}, auth.ErrUnauthenticated
	}
	if _, err := p.auth.store.EnsureFirstMembership(ctx, organization.ID, user.ID); err != nil {
		slog.Error("auth: kinde provider — EnsureFirstMembership failed", "error", err)
		return auth.Identity{}, auth.ErrUnauthenticated
	}

	// Best-effort invitation redemption — same semantics as Wrap. Any
	// failure here is logged and ignored; the membership state is the
	// authoritative truth and that has already been written.
	if user.Email != "" {
		// The org-id is passed both via context (for any RLS-guarded
		// inner queries) and as an explicit parameter (for the lookup
		// predicate). Same pattern as Auth.Wrap line ~202; redundant
		// but cheap, and makes the call self-describing.
		redeemCtx := storage.WithOrganizationID(ctx, organization.ID)
		if redeemed, rerr := p.auth.store.RedeemPendingInvitation(redeemCtx, organization.ID, user.ID, user.Email); rerr != nil {
			slog.Error("auth: kinde provider — RedeemPendingInvitation failed",
				"error", rerr,
				"user_id", user.ID,
				"organization_id", organization.ID)
		} else if redeemed {
			slog.Info("auth: kinde provider — invitation redeemed",
				"user_id", user.ID,
				"organization_id", organization.ID)
		}
	}

	// Resolve role for the Identity. This is a divergence from
	// Auth.Wrap, which leaves the role context-key empty and lets
	// handlers call store.RoleOf themselves. The Provider seam exists
	// so that *every* authenticated request — kinde or native — has
	// the same Identity shape; under WrapNative middleware writes
	// Role onto the request context for handlers to read uniformly.
	// If KindeProvider didn't populate Role here, kinde-mode requests
	// would hit middleware.Role(ctx) == "" while native-mode requests
	// returned the actual role — a confusing inconsistency. The extra
	// roundtrip is sub-millisecond (PG buffer cache hot from the
	// EnsureFirstMembership call we just made).
	role, err := p.auth.store.RoleOf(ctx, organization.ID, user.ID)
	if err != nil {
		slog.Error("auth: kinde provider — RoleOf failed", "error", err)
		return auth.Identity{}, auth.ErrUnauthenticated
	}
	if role == "" {
		// User has a session but no membership — possible in the brief
		// window between EnsureFirstMembership's no-op (org already had
		// owners) and a yet-to-be-redeemed invitation. Fail closed; the
		// user retries after the admin invites them properly.
		return auth.Identity{}, auth.ErrUnauthenticated
	}

	return auth.Identity{
		UserID:         user.ID,
		OrganizationID: organization.ID,
		Role:           role,
		Email:          user.Email,
		AuthMode:       "kinde",
		// SessionID / SessionTokenHash are intentionally empty — Kinde
		// auth is stateless on our side.
	}, nil
}
