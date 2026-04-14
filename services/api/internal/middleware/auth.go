// Package middleware provides HTTP middleware for the ingestion API.
package middleware

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log"
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
	tenantIDKey   contextKey = "tenant_id"
	tenantNameKey contextKey = "tenant_name"
	userIDKey     contextKey = "user_id"

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
		resp, err := http.Get(jwksURL) //nolint:noctx
		if err != nil {
			return nil, err
		}
		defer resp.Body.Close()
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

// Wrap returns an http.Handler that enforces JWT authentication.
func (a *Auth) Wrap(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// OPTIONS preflight and health check — no auth needed
		if r.Method == http.MethodOptions || r.URL.Path == "/health" {
			next.ServeHTTP(w, r)
			return
		}

		raw, ok := bearerToken(r)
		if !ok {
			log.Printf("auth: missing or malformed Authorization header: %s %s", r.Method, r.URL.Path)
			http.Error(w, "missing or malformed Authorization header", http.StatusUnauthorized)
			return
		}

		claims := jwt.MapClaims{}
		_, err := jwt.ParseWithClaims(raw, claims, a.keyfunc, jwt.WithIssuer(a.issuer))
		if err != nil {
			log.Printf("auth: invalid token: %s %s: %v", r.Method, r.URL.Path, err)
			http.Error(w, "invalid token", http.StatusUnauthorized)
			return
		}

		// Extract org_code — Kinde's organisation identifier
		orgCode, _ := claims["org_code"].(string)
		if orgCode == "" {
			log.Printf("auth: token missing org_code claim: %s %s", r.Method, r.URL.Path)
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
				log.Printf("auth: UpsertTenant error: %v", err)
				http.Error(w, "internal error", http.StatusInternalServerError)
				return
			}

			user, err := a.store.UpsertUser(ctx, tenant.ID, sub, email, name)
			if err != nil {
				log.Printf("auth: UpsertUser error: %v", err)
				http.Error(w, "internal error", http.StatusInternalServerError)
				return
			}

			ctx = context.WithValue(ctx, tenantIDKey, tenant.ID)
			ctx = context.WithValue(ctx, tenantNameKey, tenant.Name)
			ctx = context.WithValue(ctx, userIDKey, user.ID)
		} else {
			ctx = context.WithValue(ctx, tenantIDKey, orgCode)
		}

		next.ServeHTTP(w, r.WithContext(ctx))
	})
}

// TenantID returns the internal tenant UUID from the request context.
func TenantID(ctx context.Context) string {
	id, _ := ctx.Value(tenantIDKey).(string)
	return id
}

// TenantName returns the tenant display name from the request context.
func TenantName(ctx context.Context) string {
	name, _ := ctx.Value(tenantNameKey).(string)
	return name
}

// UserID returns the internal user UUID from the request context.
func UserID(ctx context.Context) string {
	id, _ := ctx.Value(userIDKey).(string)
	return id
}

// DevBypass injects a fixed tenant ID into every request context.
// Only active when KINDE_ISSUER is unset — local development without auth.
func DevBypass(tenantID string, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodOptions || r.URL.Path == "/health" {
			next.ServeHTTP(w, r)
			return
		}
		ctx := context.WithValue(r.Context(), tenantIDKey, tenantID)
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
