// Package middleware provides HTTP middleware for the ingestion API.
package middleware

import (
	"context"
	"fmt"
	"log/slog"
	"net/http"
	"strings"

	"github.com/golang-jwt/jwt/v5"

	"axiaops.io/shared/cache"
	"axiaops.io/shared/jwks"
	"axiaops.io/shared/storage"
)

type contextKey string

const (
	organizationIDKey   contextKey = "organization_id"
	organizationCodeKey contextKey = "organization_code"
	userIDKey           contextKey = "user_id"
	userEmailKey        contextKey = "user_email"
	roleKey             contextKey = "role"
	authModeKey         contextKey = "auth_mode"
)

// Auth verifies Kinde JWTs on every request.
// It fetches the JWKS from the Kinde issuer and caches the keys.
// Requests without a valid Bearer token receive 401.
type Auth struct {
	issuer  string
	keyfunc jwt.Keyfunc
	store   storage.Store
}

// NewAuth creates an Auth middleware from the Kinde issuer URL.
// JWKS are fetched (and cached for 1 hour) via the shared jwks package; on
// cache miss or cache error the JWKS are fetched live — auth never fails
// due to cache.
func NewAuth(ctx context.Context, issuer string, store storage.Store, c cache.Cache) (*Auth, error) {
	jwksURL := strings.TrimRight(issuer, "/") + "/.well-known/jwks.json"
	kf, err := jwks.FromCache(ctx, issuer, jwksURL, c)
	if err != nil {
		return nil, fmt.Errorf("auth: fetch JWKS from %s: %w", jwksURL, err)
	}
	return &Auth{issuer: issuer, keyfunc: kf, store: store}, nil
}

// newWithKeyfunc creates an Auth with a custom keyfunc — used in tests only.
func newWithKeyfunc(issuer string, kf jwt.Keyfunc) *Auth {
	return &Auth{issuer: issuer, keyfunc: kf}
}

// publicPath reports whether the path bypasses authentication.
// Three families bypass:
//
//  1. Infra: /metrics, /health, /livez, /readyz — must remain reachable
//     from container orchestration and Prometheus without a session.
//  2. Auth ceremony: /v1/auth/bootstrap, /v1/auth/login,
//     /v1/auth/invitations/redeem, /v1/auth/password-reset/redeem —
//     the endpoints used to *acquire* authentication. /v1/auth/logout
//     is also bypassed (the handler tolerates a missing/invalid cookie
//     and clears whatever's there).
//
// Plan §4.2 lists rate-limiting requirements (10/min/IP for login, etc.)
// — those land in a follow-up slice; the bypass here is the routing
// layer, not the abuse-protection layer.
func publicPath(p string) bool {
	switch p {
	case "/health", "/livez", "/readyz", "/metrics":
		return true
	}
	return strings.HasPrefix(p, "/v1/auth/")
}

// Wrap returns an http.Handler that enforces JWT authentication.
func (a *Auth) Wrap(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// OPTIONS preflight and public infra paths — no auth needed
		if r.Method == http.MethodOptions || publicPath(r.URL.Path) {
			next.ServeHTTP(w, r)
			return
		}

		raw, ok := bearerToken(r)
		if !ok {
			slog.Warn("auth: missing or malformed Authorization header", "method", r.Method, "path", r.URL.Path)
			http.Error(w, "missing or malformed Authorization header", http.StatusUnauthorized)
			return
		}

		claims := jwt.MapClaims{}
		_, err := jwt.ParseWithClaims(raw, claims, a.keyfunc, jwt.WithIssuer(a.issuer))
		if err != nil {
			slog.Warn("auth: invalid token", "method", r.Method, "path", r.URL.Path, "error", err)
			http.Error(w, "invalid token", http.StatusUnauthorized)
			return
		}

		// Extract org_code — Kinde's organisation identifier
		orgCode, _ := claims["org_code"].(string)
		if orgCode == "" {
			slog.Warn("auth: token missing org_code claim", "method", r.Method, "path", r.URL.Path)
			http.Error(w, "token missing org_code claim", http.StatusUnauthorized)
			return
		}

		sub, _ := claims["sub"].(string)
		email, _ := claims["email"].(string)
		name, _ := claims["name"].(string)
		orgName, _ := claims["org_name"].(string)

		ctx := r.Context()

		// Persist organization and user on every authenticated request.
		// UpsertOrganization/UpsertUser are idempotent — safe to call repeatedly.
		if a.store != nil {
			organization, err := a.store.UpsertOrganization(ctx, orgCode, orgName)
			if err != nil {
				slog.Error("auth: UpsertOrganization failed", "error", err)
				http.Error(w, "internal error", http.StatusInternalServerError)
				return
			}

			user, err := a.store.UpsertUser(ctx, organization.ID, sub, email, name)
			if err != nil {
				slog.Error("auth: UpsertUser failed", "error", err)
				http.Error(w, "internal error", http.StatusInternalServerError)
				return
			}

			// Brand-new Kinde org: auto-promote the first authenticator to
			// owner. The partial unique index in migration 015 backstops
			// concurrent first-logins — only one INSERT wins. Subsequent
			// users to the organization get inserted = false and rely on explicit
			// invitation; their request still succeeds at the auth layer
			// but the Require decorator on protected routes will 403 since
			// they have no membership row.
			if _, err := a.store.EnsureFirstMembership(ctx, organization.ID, user.ID); err != nil {
				slog.Error("auth: EnsureFirstMembership failed", "error", err)
				http.Error(w, "internal error", http.StatusInternalServerError)
				return
			}

			// Best-effort invitation redemption. EnsureFirstMembership above is a
			// no-op for invited users (their org already has owners); this step
			// converts a matching pending_memberships row into a real membership.
			// Soft-fail on error: a missing membership leaves the user at 403,
			// which is correct fallback behaviour. See docs/invitation-flow.md §5.
			if user.Email != "" {
				redeemCtx := storage.WithOrganizationID(ctx, organization.ID)
				redeemed, rerr := a.store.RedeemPendingInvitation(redeemCtx, organization.ID, user.ID, user.Email)
				if rerr != nil {
					slog.Error("auth: RedeemPendingInvitation failed",
						"error", rerr,
						"user_id", user.ID,
						"organization_id", organization.ID)
				} else if redeemed {
					slog.Info("auth: invitation redeemed",
						"user_id", user.ID,
						"organization_id", organization.ID,
						"email", user.Email)
				}
			}

			ctx = context.WithValue(ctx, organizationIDKey, organization.ID)
			ctx = context.WithValue(ctx, organizationCodeKey, organization.OrgCode)
			ctx = context.WithValue(ctx, userIDKey, user.ID)
			ctx = context.WithValue(ctx, userEmailKey, user.Email)
		} else {
			// store == nil is reachable only via newWithKeyfunc in tests.
			// In this branch userIDKey receives the raw Kinde sub (e.g. "kp_abc")
			// instead of a users.id UUID, so anything using UserID(ctx) as a FK
			// will fail. Never wire this path into production — it exists purely
			// so middleware tests can exercise JWT parsing without a DB.
			ctx = context.WithValue(ctx, organizationIDKey, orgCode)
			ctx = context.WithValue(ctx, organizationCodeKey, orgCode)
			ctx = context.WithValue(ctx, userIDKey, sub)
			ctx = context.WithValue(ctx, userEmailKey, email)
		}

		next.ServeHTTP(w, r.WithContext(ctx))
	})
}

// OrganizationID returns the internal organization UUID from the request context.
func OrganizationID(ctx context.Context) string {
	id, _ := ctx.Value(organizationIDKey).(string)
	return id
}

// OrganizationCode returns the Kinde org_code claim from the request context.
// Used by handlers that need to call Kinde Mgmt API endpoints scoped by
// organization (invitations, rename). Returns "" under DevBypass with
// no DEV_ORG_CODE configured — handlers must guard for that.
func OrganizationCode(ctx context.Context) string {
	code, _ := ctx.Value(organizationCodeKey).(string)
	return code
}

// UserID returns the stable user identifier from the request context.
// In production this is the UUID from the users table (set after UpsertUser);
// under DevBypass it is DEV_USER_ID. Returns "" if unset.
func UserID(ctx context.Context) string {
	id, _ := ctx.Value(userIDKey).(string)
	return id
}

// UserEmail returns the authenticated user's email from the request context.
// Captured from Kinde claims in production or from DEV_USER_EMAIL under
// DevBypass. Returns "" if unset.
func UserEmail(ctx context.Context) string {
	email, _ := ctx.Value(userEmailKey).(string)
	return email
}

// Role returns the membership role ("owner"|"admin"|"member"|"viewer")
// resolved by the auth middleware for the bound (organization, user)
// pair. Empty under the legacy Kinde Wrap path (which doesn't preload
// role) and under DevBypass.
//
// Handlers that need role for authorization decisions can either read
// this value (when populated by WrapNative / native provider) or fall
// back to store.RoleOf — the latter is what existing Kinde-path handlers
// already do and remains correct.
func Role(ctx context.Context) string {
	role, _ := ctx.Value(roleKey).(string)
	return role
}

// AuthMode returns the auth_mode of the active session: "password",
// "sso", "bootstrap", or "kinde" (the legacy Bearer JWT path during the
// strangler window). Empty when no auth has run on the request.
//
// Used by handlers that need to enforce SSO requirement (B2 §5.2 will
// 403 native-password sessions for orgs whose enforcement is "required").
func AuthMode(ctx context.Context) string {
	mode, _ := ctx.Value(authModeKey).(string)
	return mode
}

// DevBypass injects a fixed organization + user identity into every request context.
// Only active when DEV_MODE=true — local development without auth.
// The organization and user rows are ensured once at service startup (see cmd/main.go),
// so this middleware does no DB work per request.
//
// The organization_code (Kinde org_code claim equivalent) is set to
// organizationID — in dev mode they're equivalent by convention (EnsureOrganization
// pins id == org_code), and handlers that hit the Kinde Mgmt API are guarded
// by the kinde stub when DEV_MODE=true.
func DevBypass(organizationID, userID, userEmail string, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodOptions || publicPath(r.URL.Path) {
			next.ServeHTTP(w, r)
			return
		}
		ctx := r.Context()
		ctx = context.WithValue(ctx, organizationIDKey, organizationID)
		ctx = context.WithValue(ctx, organizationCodeKey, organizationID)
		ctx = context.WithValue(ctx, userIDKey, userID)
		ctx = context.WithValue(ctx, userEmailKey, userEmail)
		next.ServeHTTP(w, r.WithContext(ctx))
	})
}

// bearerToken extracts the token from the Authorization: Bearer <token> header.
func bearerToken(r *http.Request) (string, bool) {
	h := r.Header.Get("Authorization")
	if !strings.HasPrefix(h, "Bearer ") {
		return "", false
	}
	return strings.TrimPrefix(h, "Bearer "), true
}
