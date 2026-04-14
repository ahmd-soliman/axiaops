// Package cache provides a unified caching abstraction for AxiaOps services.
// When REDIS_URL is set, a Redis-backed implementation is used.
// When unset, an in-memory fallback is used so local dev works without Redis.
package cache

import (
	"context"
	"errors"
	"log/slog"
	"time"

	memorycache "axiaops.io/shared/cache/memory"
	rediscache "axiaops.io/shared/cache/redis"
)

// ErrNotFound is returned by Get when the key does not exist or has expired.
var ErrNotFound = errors.New("cache: key not found")

// Cache is the unified caching interface used across AxiaOps services.
type Cache interface {
	// Get retrieves a value. Returns ErrNotFound on miss.
	Get(ctx context.Context, key string) ([]byte, error)
	// Set stores a value with the given TTL.
	Set(ctx context.Context, key string, value []byte, ttl time.Duration) error
	// Del removes a key.
	Del(ctx context.Context, key string) error
	// Incr atomically increments a counter, resetting TTL on each call.
	Incr(ctx context.Context, key string, ttl time.Duration) (int64, error)
	// Close releases resources held by the cache.
	Close() error
}

// wrappedCache normalises sub-package ErrNotFound values to the top-level sentinel.
type wrappedCache struct {
	inner     Cache
	notFoundE error // the sub-package's ErrNotFound
}

func (w *wrappedCache) Get(ctx context.Context, key string) ([]byte, error) {
	v, err := w.inner.Get(ctx, key)
	if errors.Is(err, w.notFoundE) {
		return nil, ErrNotFound
	}
	return v, err
}
func (w *wrappedCache) Set(ctx context.Context, key string, value []byte, ttl time.Duration) error {
	return w.inner.Set(ctx, key, value, ttl)
}
func (w *wrappedCache) Del(ctx context.Context, key string) error {
	return w.inner.Del(ctx, key)
}
func (w *wrappedCache) Incr(ctx context.Context, key string, ttl time.Duration) (int64, error) {
	return w.inner.Incr(ctx, key, ttl)
}
func (w *wrappedCache) Close() error { return w.inner.Close() }

// New returns a Redis-backed Cache when redisURL is non-empty,
// otherwise returns an in-memory Cache.
func New(redisURL string) Cache {
	if redisURL != "" {
		c, err := rediscache.New(redisURL)
		if err == nil {
			slog.Info("cache: backend selected", "backend", "redis")
			return &wrappedCache{inner: c, notFoundE: rediscache.ErrNotFound}
		}
		slog.Warn("cache: failed to connect to Redis, falling back to memory", "err", err)
	}
	slog.Info("cache: backend selected", "backend", "memory")
	return &wrappedCache{inner: memorycache.New(), notFoundE: memorycache.ErrNotFound}
}
