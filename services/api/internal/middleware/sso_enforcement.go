// sso_enforcement.go — middleware that enforces per-organisation SSO
// policy on already-authenticated requests. Implements the §5.5
// acceptance "enforcement=required blocks native-password sessions for
// the org with 403" (design doc §11.2 / plan §5.2).
//
// Routing posture: this middleware MUST run AFTER WrapNative (which
// resolves the Identity and stamps organization_id + auth_mode onto the
// request context). It MUST run BEFORE business handlers. Public
// /v1/sso/oidc/* and /v1/auth/* paths bypass authentication entirely
// via publicPath / publicNativePath, so this middleware is only ever
// reached by sessions that already authenticated successfully.
//
// Why a SEPARATE middleware (rather than inlining the check into
// WrapNative): the enforcement decision needs a per-request DB lookup
// (the org's SSO connection's enforcement value). Keeping it on its own
// makes the auth path testable without standing up the SSO storage
// layer — WrapNative tests don't need to know enforcement exists, and
// enforcement tests don't need to know how authentication works.

package middleware

import (
	"context"
	"net/http"

	"axiaops.io/shared/model"
	"axiaops.io/shared/storage"
)

// SSOEnforcementResolver returns the per-organisation SSO enforcement
// level. The production impl wraps storage.Store and reads the highest
// `enforcement` value across the org's `active`+`oidc` connections (a
// deactivated or draft connection MUST NOT contribute — otherwise an
// admin who toggles a connection to draft mid-investigation could
// accidentally re-enable native-password access for the org).
//
// Returning ("", nil) is treated identically to enforcement="optional":
// the request passes through. This is the safe default — a transient
// store error or an org with zero SSO connections must never
// accidentally lock users out.
type SSOEnforcementResolver interface {
	OrgSSOEnforcement(ctx context.Context, organizationID string) (string, error)
}

// EnforceSSO returns a middleware that 403s any authenticated request
// whose `auth_mode` is "password" while the org's resolved enforcement
// level is `required`. SSO sessions and bootstrap sessions pass through
// regardless of enforcement.
//
// The middleware fails OPEN (request passes through) on:
//   - Resolver errors — a transient store outage must not produce a
//     mass 403. The Authentication layer already established a valid
//     session; refusing to serve it on a backend hiccup is worse than
//     letting the request through.
//   - Missing organization_id on context — defence in depth; if the
//     caller forgot to wrap with WrapNative the enforcement check
//     can't run.
//   - Empty or unknown enforcement values — only the literal string
//     `model.SSOEnforcementRequired` triggers the 403.
//
// The 403 body is `{"error":"sso_required"}` so the dashboard can
// distinguish it from generic 403s and route the user to a "your org
// requires SSO" screen.
//
// skipPaths is the exact-match set of request paths that bypass the
// check entirely. Wire `/v1/auth/logout` here so a blocked
// password-session user can still cleanly end their session — without
// the bypass they're stuck holding a cookie they cannot retire via the
// API. (The cookie eventually expires via SESSION_TTL_HOURS, but
// blocking logout-on-403 is needlessly hostile.)
func EnforceSSO(resolver SSOEnforcementResolver, skipPaths ...string) func(http.Handler) http.Handler {
	skip := make(map[string]struct{}, len(skipPaths))
	for _, p := range skipPaths {
		skip[p] = struct{}{}
	}
	return func(next http.Handler) http.Handler {
		if resolver == nil {
			// No resolver = enforcement disabled (tests, dev mode, the
			// strangler tier where the SSO storage isn't wired yet).
			// Fail open so production can stage this middleware in
			// safely before the Store impl lands.
			return next
		}
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if _, ok := skip[r.URL.Path]; ok {
				next.ServeHTTP(w, r)
				return
			}

			authMode := AuthMode(r.Context())
			// SSO and bootstrap sessions are unaffected by enforcement.
			// Bootstrap is the first-owner install flow and, by
			// definition, runs before any SSO connection exists; it
			// must always pass.
			if authMode != string(model.AuthModePassword) {
				next.ServeHTTP(w, r)
				return
			}

			orgID := OrganizationID(r.Context())
			if orgID == "" {
				next.ServeHTTP(w, r)
				return
			}

			level, err := resolver.OrgSSOEnforcement(r.Context(), orgID)
			if err != nil {
				// Resolver errors fail open — see commentary above.
				next.ServeHTTP(w, r)
				return
			}
			if level != model.SSOEnforcementRequired {
				next.ServeHTTP(w, r)
				return
			}

			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusForbidden)
			// Body is a fixed string — never built from user input,
			// never includes a reason code beyond the discriminator.
			// Frontend pivots on `error == "sso_required"`.
			_, _ = w.Write([]byte(`{"error":"sso_required"}`))
		})
	}
}

// NewStoreEnforcementResolver builds the production SSOEnforcementResolver
// over a storage.Store. Returns the highest enforcement level among the
// org's `active`+`oidc` connections — a draft / disabled / non-OIDC
// connection MUST NOT contribute (otherwise an admin who toggles a
// connection mid-investigation could accidentally re-enable native-
// password access).
//
// Reads are RLS-scoped via storage.WithOrganizationID so a buggy caller
// can't leak enforcement levels across orgs even if the request context
// is malformed.
func NewStoreEnforcementResolver(store storage.Store) SSOEnforcementResolver {
	return &storeEnforcementResolver{store: store}
}

type storeEnforcementResolver struct {
	store storage.Store
}

func (r *storeEnforcementResolver) OrgSSOEnforcement(ctx context.Context, organizationID string) (string, error) {
	if organizationID == "" {
		return "", nil
	}
	scoped := storage.WithOrganizationID(ctx, organizationID)
	conns, err := r.store.ListSSOConnections(scoped)
	if err != nil {
		return "", err
	}
	highest := ""
	for _, c := range conns {
		if c.Status != model.SSOStatusActive || c.Protocol != model.SSOProtocolOIDC {
			continue
		}
		if enforcementRank(c.Enforcement) > enforcementRank(highest) {
			highest = c.Enforcement
		}
	}
	return highest, nil
}

// enforcementRank gives a strict total order on the enforcement levels
// so the resolver can pick the highest across multiple connections.
// Unknown / empty values rank below "optional" so a malformed row
// degrades to no-enforce rather than locking users out.
func enforcementRank(level string) int {
	switch level {
	case model.SSOEnforcementRequired:
		return 3
	case model.SSOEnforcementPreferred:
		return 2
	case model.SSOEnforcementOptional:
		return 1
	default:
		return 0
	}
}
