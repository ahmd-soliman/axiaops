// Package middleware provides HTTP middleware for the ingestion API.
package middleware

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"github.com/MicahParks/keyfunc/v3"
	"github.com/golang-jwt/jwt/v5"

	"axiaops.io/shared/cache"
	"axiaops.io/shared/storage"
)

type contextKey string

const (
	tenantIDKey  contextKey = "tenant_id"
	userIDKey    contextKey = "user_id"
	userEmailKey contextKey = "user_email"

	jwksTTL = time.Hour
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
// JWKS are cached in the provided cache.Cache under key "jwks:{issuer}" for 1 hour.
// On cache miss or cache error the JWKS are fetched live — auth never fails due to cache.
func NewAuth(ctx context.Context, issuer string, store storage.Store, c cache.Cache) (*Auth, error) {
	jwksURL := strings.TrimRight(issuer, "/") + "/.well-known/jwks.json"
	kf, err := keyfuncFromCache(ctx, issuer, jwksURL, c)
	if err != nil {
		return nil, fmt.Errorf("auth: fetch JWKS from %s: %w", jwksURL, err)
	}
	return &Auth{issuer: issuer, keyfunc: kf, store: store}, nil
}

// keyfuncFromCache returns a jwt.Keyfunc backed by a cached JWKS.
// Cache miss → fetch live → populate cache.
// Cache error → log warn → fetch live (never block auth).
func keyfuncFromCache(ctx context.Context, issuer, jwksURL string, c cache.Cache) (jwt.Keyfunc, error) {
	cacheKey := "jwks:" + issuer

	fetchAndCache := func() (jwt.Keyfunc, error) {
		fetchCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
		defer cancel()
		req, err := http.NewRequestWithContext(fetchCtx, http.MethodGet, jwksURL, nil)
		if err != nil {
			return nil, err
		}
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			return nil, err
		}
		defer func() { _ = resp.Body.Close() }()
		body, err := io.ReadAll(resp.Body)
		if err != nil {
			return nil, err
		}
		if c != nil {
			if setErr := c.Set(ctx, cacheKey, body, jwksTTL); setErr != nil {
				slog.Warn("auth: failed to cache JWKS", "err", setErr)
			}
		}
		return keyfuncFromBytes(body)
	}

	if c != nil {
		data, err := c.Get(ctx, cacheKey)
		if err == nil {
			slog.Info("auth: JWKS cache hit", "issuer", issuer)
			kf, parseErr := keyfuncFromBytes(data)
			if parseErr == nil {
				return kf, nil
			}
			slog.Warn("auth: cached JWKS invalid, re-fetching", "err", parseErr)
		} else if !errors.Is(err, cache.ErrNotFound) {
			slog.Warn("auth: cache error, falling back to live fetch", "err", err)
		}
	}

	return fetchAndCache()
}

// keyfuncFromBytes parses a raw JWKS JSON payload into a jwt.Keyfunc.
func keyfuncFromBytes(data []byte) (jwt.Keyfunc, error) {
	k, err := keyfunc.NewJWKSetJSON(data)
	if err != nil {
		return nil, err
	}
	return k.Keyfunc, nil
}

// newWithKeyfunc creates an Auth with a custom keyfunc — used in tests only.
func newWithKeyfunc(issuer string, kf jwt.Keyfunc) *Auth {
	return &Auth{issuer: issuer, keyfunc: kf}
}

// publicPath reports whether the path bypasses authentication.
// /metrics, /health, /livez, /readyz must remain reachable from
// container orchestration and Prometheus without a JWT.
func publicPath(p string) bool {
	switch p {
	case "/health", "/livez", "/readyz", "/metrics":
		return true
	}
	return false
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

		// Persist tenant and user on every authenticated request.
		// UpsertTenant/UpsertUser are idempotent — safe to call repeatedly.
		if a.store != nil {
			tenant, err := a.store.UpsertTenant(ctx, orgCode, orgName)
			if err != nil {
				slog.Error("auth: UpsertTenant failed", "error", err)
				http.Error(w, "internal error", http.StatusInternalServerError)
				return
			}

			user, err := a.store.UpsertUser(ctx, tenant.ID, sub, email, name)
			if err != nil {
				slog.Error("auth: UpsertUser failed", "error", err)
				http.Error(w, "internal error", http.StatusInternalServerError)
				return
			}

			// Brand-new Kinde org: auto-promote the first authenticator to
			// owner. The partial unique index in migration 015 backstops
			// concurrent first-logins — only one INSERT wins. Subsequent
			// users to the tenant get inserted = false and rely on explicit
			// invitation; their request still succeeds at the auth layer
			// but the Require decorator on protected routes will 403 since
			// they have no membership row.
			if _, err := a.store.EnsureFirstMembership(ctx, tenant.ID, user.ID); err != nil {
				slog.Error("auth: EnsureFirstMembership failed", "error", err)
				http.Error(w, "internal error", http.StatusInternalServerError)
				return
			}

			ctx = context.WithValue(ctx, tenantIDKey, tenant.ID)
			ctx = context.WithValue(ctx, userIDKey, user.ID)
			ctx = context.WithValue(ctx, userEmailKey, user.Email)
		} else {
			// store == nil is reachable only via newWithKeyfunc in tests.
			// In this branch userIDKey receives the raw Kinde sub (e.g. "kp_abc")
			// instead of a users.id UUID, so anything using UserID(ctx) as a FK
			// will fail. Never wire this path into production — it exists purely
			// so middleware tests can exercise JWT parsing without a DB.
			ctx = context.WithValue(ctx, tenantIDKey, orgCode)
			ctx = context.WithValue(ctx, userIDKey, sub)
			ctx = context.WithValue(ctx, userEmailKey, email)
		}

		next.ServeHTTP(w, r.WithContext(ctx))
	})
}

// TenantID returns the internal tenant UUID from the request context.
func TenantID(ctx context.Context) string {
	id, _ := ctx.Value(tenantIDKey).(string)
	return id
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

// DevBypass injects a fixed tenant + user identity into every request context.
// Only active when DEV_MODE=true — local development without auth.
// The tenant and user rows are ensured once at service startup (see cmd/main.go),
// so this middleware does no DB work per request.
func DevBypass(tenantID, userID, userEmail string, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodOptions || publicPath(r.URL.Path) {
			next.ServeHTTP(w, r)
			return
		}
		ctx := r.Context()
		ctx = context.WithValue(ctx, tenantIDKey, tenantID)
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
