// Package jwks provides cached JWKS retrieval and jwt.Keyfunc construction
// for any JWT verifier in the AxiaOps codebase.
//
// Two consumers (B2 onwards):
//   - The legacy Kinde JWT validation in services/api/internal/middleware
//     (one issuer-bound JWKS endpoint) under AUTH_PROVIDER=kinde|both.
//   - The native OIDC RP per-connection JWKS lookup (B2 phase).
//
// The cache key is opaque from this package's perspective — callers pass
// whatever stable string distinguishes their JWKS source (Kinde issuer URL,
// SSO connection ID, etc.). We prepend "jwks:" to keep cache keys namespaced.
//
// Auth never blocks on cache: cache miss or cache error falls through to a
// live HTTP fetch, and a successful live fetch repopulates the cache
// best-effort.
package jwks

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"time"

	"github.com/MicahParks/keyfunc/v3"
	"github.com/golang-jwt/jwt/v5"

	"axiaops.io/shared/cache"
)

// DefaultTTL is the cache lifetime for fetched JWKS payloads.
// One hour matches the Kinde-era behaviour and is short enough that an IdP
// key rotation propagates within an acceptable window without forcing a
// fetch on every request.
const DefaultTTL = time.Hour

const cacheKeyPrefix = "jwks:"

// FromCache returns a jwt.Keyfunc backed by a cached JWKS payload.
//
// cacheID is the caller-supplied identifier used to namespace the cache
// entry — pass the issuer URL for issuer-bound JWKS (Kinde), the SSO
// connection ID for per-connection JWKS (OIDC).
//
// jwksURL is the absolute URL to fetch when the cache is cold.
//
// c may be nil — the function then always fetches live and never caches.
//
// Behaviour:
//   - Cache hit and parse OK → returned keyfunc; no HTTP fetch.
//   - Cache hit but parse fails → log warn, fall through to live fetch.
//   - Cache miss → live fetch, populate cache best-effort.
//   - Cache get error (not "not found") → log warn, fall through to live fetch.
//
// The function never returns a partial keyfunc on error — either a working
// jwt.Keyfunc or an error.
func FromCache(ctx context.Context, cacheID, jwksURL string, c cache.Cache) (jwt.Keyfunc, error) {
	cacheKey := cacheKeyPrefix + cacheID

	fetchAndCache := func() (jwt.Keyfunc, error) {
		fetchCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
		defer cancel()
		req, err := http.NewRequestWithContext(fetchCtx, http.MethodGet, jwksURL, nil)
		if err != nil {
			return nil, fmt.Errorf("jwks: build request: %w", err)
		}
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			return nil, fmt.Errorf("jwks: fetch %s: %w", jwksURL, err)
		}
		defer func() { _ = resp.Body.Close() }()
		body, err := io.ReadAll(resp.Body)
		if err != nil {
			return nil, fmt.Errorf("jwks: read body: %w", err)
		}
		if c != nil {
			if setErr := c.Set(ctx, cacheKey, body, DefaultTTL); setErr != nil {
				slog.Warn("jwks: failed to cache JWKS", "cache_id", cacheID, "err", setErr)
			}
		}
		return FromBytes(body)
	}

	if c != nil {
		data, err := c.Get(ctx, cacheKey)
		if err == nil {
			slog.Info("jwks: cache hit", "cache_id", cacheID)
			kf, parseErr := FromBytes(data)
			if parseErr == nil {
				return kf, nil
			}
			slog.Warn("jwks: cached JWKS invalid, re-fetching", "cache_id", cacheID, "err", parseErr)
		} else if !errors.Is(err, cache.ErrNotFound) {
			slog.Warn("jwks: cache error, falling back to live fetch", "cache_id", cacheID, "err", err)
		}
	}

	return fetchAndCache()
}

// FromBytes parses a raw JWKS JSON payload into a jwt.Keyfunc.
// Used by FromCache and exposed for callers that already have the bytes
// (e.g. an OIDC discovery doc that inlines its JWKS).
func FromBytes(data []byte) (jwt.Keyfunc, error) {
	k, err := keyfunc.NewJWKSetJSON(data)
	if err != nil {
		return nil, fmt.Errorf("jwks: parse: %w", err)
	}
	return k.Keyfunc, nil
}
