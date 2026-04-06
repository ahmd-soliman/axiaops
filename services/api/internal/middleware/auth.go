// Package middleware provides HTTP middleware for the ingestion API.
package middleware

import (
	"context"
	"fmt"
	"log"
	"net/http"
	"strings"

	"github.com/MicahParks/keyfunc/v3"
	"github.com/golang-jwt/jwt/v5"

	"axiaops.io/shared/storage"
)

type contextKey string

const (
	tenantIDKey   contextKey = "tenant_id"
	tenantNameKey contextKey = "tenant_name"
	userIDKey     contextKey = "user_id"
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
// It fetches the JWKS endpoint at startup and refreshes keys automatically.
func NewAuth(ctx context.Context, issuer string, store storage.Store) (*Auth, error) {
	jwksURL := strings.TrimRight(issuer, "/") + "/.well-known/jwks.json"
	k, err := keyfunc.NewDefaultCtx(ctx, []string{jwksURL})
	if err != nil {
		return nil, fmt.Errorf("auth: fetch JWKS from %s: %w", jwksURL, err)
	}
	return &Auth{issuer: issuer, keyfunc: k.Keyfunc, store: store}, nil
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
			http.Error(w, "missing or malformed Authorization header", http.StatusUnauthorized)
			return
		}

		claims := jwt.MapClaims{}
		_, err := jwt.ParseWithClaims(raw, claims, a.keyfunc, jwt.WithIssuer(a.issuer))
		if err != nil {
			http.Error(w, "invalid token", http.StatusUnauthorized)
			return
		}

		// Extract org_code — Kinde's organisation identifier
		orgCode, _ := claims["org_code"].(string)
		if orgCode == "" {
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

// bearerToken extracts the token from the Authorization: Bearer <token> header.
func bearerToken(r *http.Request) (string, bool) {
	h := r.Header.Get("Authorization")
	if !strings.HasPrefix(h, "Bearer ") {
		return "", false
	}
	return strings.TrimPrefix(h, "Bearer "), true
}
