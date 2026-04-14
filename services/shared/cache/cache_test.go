// Package cache_test runs the same test suite against both cache implementations.
// Redis tests are skipped when TEST_REDIS_URL is unset.
package cache_test

import (
	"context"
	"errors"
	"os"
	"testing"
	"time"

	"axiaops.io/shared/cache"
	memorycache "axiaops.io/shared/cache/memory"
	rediscache "axiaops.io/shared/cache/redis"
)

// suite runs the shared test cases against any Cache implementation.
func suite(t *testing.T, c cache.Cache) {
	t.Helper()
	ctx := context.Background()

	t.Run("miss returns ErrNotFound", func(t *testing.T) {
		_, err := c.Get(ctx, "no-such-key")
		if !errors.Is(err, cache.ErrNotFound) {
			t.Fatalf("expected ErrNotFound, got %v", err)
		}
	})

	t.Run("set then get", func(t *testing.T) {
		if err := c.Set(ctx, "k1", []byte("hello"), time.Minute); err != nil {
			t.Fatal(err)
		}
		val, err := c.Get(ctx, "k1")
		if err != nil {
			t.Fatal(err)
		}
		if string(val) != "hello" {
			t.Fatalf("got %q, want %q", val, "hello")
		}
	})

	t.Run("del removes key", func(t *testing.T) {
		_ = c.Set(ctx, "k2", []byte("bye"), time.Minute)
		if err := c.Del(ctx, "k2"); err != nil {
			t.Fatal(err)
		}
		_, err := c.Get(ctx, "k2")
		if !errors.Is(err, cache.ErrNotFound) {
			t.Fatalf("expected ErrNotFound after del, got %v", err)
		}
	})

	t.Run("TTL expiry", func(t *testing.T) {
		if err := c.Set(ctx, "k3", []byte("temp"), 50*time.Millisecond); err != nil {
			t.Fatal(err)
		}
		time.Sleep(100 * time.Millisecond)
		_, err := c.Get(ctx, "k3")
		if !errors.Is(err, cache.ErrNotFound) {
			t.Fatalf("expected ErrNotFound after TTL, got %v", err)
		}
	})

	t.Run("Incr sequence", func(t *testing.T) {
		for i := int64(1); i <= 3; i++ {
			n, err := c.Incr(ctx, "counter1", time.Minute)
			if err != nil {
				t.Fatal(err)
			}
			if n != i {
				t.Fatalf("Incr %d: got %d, want %d", i, n, i)
			}
		}
	})

	t.Run("Incr resets after TTL", func(t *testing.T) {
		_, _ = c.Incr(ctx, "counter2", 50*time.Millisecond)
		time.Sleep(100 * time.Millisecond)
		n, err := c.Incr(ctx, "counter2", time.Minute)
		if err != nil {
			t.Fatal(err)
		}
		if n != 1 {
			t.Fatalf("expected counter reset to 1, got %d", n)
		}
	})
}

func TestMemoryCache(t *testing.T) {
	c := &wrappedMemory{memorycache.New()}
	defer c.Close()
	suite(t, c)
}

func TestRedisCache(t *testing.T) {
	url := os.Getenv("TEST_REDIS_URL")
	if url == "" {
		t.Skip("TEST_REDIS_URL not set — skipping Redis tests")
	}
	rc, err := rediscache.New(url)
	if err != nil {
		t.Fatalf("connect to Redis: %v", err)
	}
	c := cache.New(url) // goes through the factory wrapper
	defer c.Close()

	// Flush test keys to avoid cross-test pollution.
	ctx := context.Background()
	for _, k := range []string{"k1", "k2", "k3", "counter1", "counter2", "no-such-key"} {
		_ = rc.Del(ctx, k)
	}

	suite(t, c)
}

// wrappedMemory adapts memorycache.Cache to cache.Cache by mapping ErrNotFound.
type wrappedMemory struct{ *memorycache.Cache }

func (w *wrappedMemory) Get(ctx context.Context, key string) ([]byte, error) {
	v, err := w.Cache.Get(ctx, key)
	if errors.Is(err, memorycache.ErrNotFound) {
		return nil, cache.ErrNotFound
	}
	return v, err
}
