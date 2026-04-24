// Package memory provides an in-memory implementation of cache.Cache.
// It is used as a fallback when REDIS_URL is not set.
package memory

import (
	"context"
	"errors"
	"sync"
	"time"
)

// ErrNotFound is returned by Get when the key does not exist or has expired.
var ErrNotFound = errors.New("cache: key not found")

type entry struct {
	value     []byte
	expiresAt time.Time
}

type counter struct {
	mu        sync.Mutex
	val       int64
	expiresAt time.Time
}

// Cache is an in-memory cache with TTL support.
type Cache struct {
	mu       sync.RWMutex
	items    map[string]entry
	counters map[string]*counter
	stopCh   chan struct{}
}

// New creates a new in-memory cache and starts a background sweep goroutine.
func New() *Cache {
	c := &Cache{
		items:    make(map[string]entry),
		counters: make(map[string]*counter),
		stopCh:   make(chan struct{}),
	}
	go c.sweep()
	return c
}

// Get retrieves a value by key. Returns ErrNotFound on miss or expiry.
func (c *Cache) Get(_ context.Context, key string) ([]byte, error) {
	c.mu.RLock()
	e, ok := c.items[key]
	c.mu.RUnlock()
	if !ok || time.Now().After(e.expiresAt) {
		return nil, ErrNotFound
	}
	return e.value, nil
}

// Set stores a value with the given TTL.
func (c *Cache) Set(_ context.Context, key string, value []byte, ttl time.Duration) error {
	c.mu.Lock()
	c.items[key] = entry{value: value, expiresAt: time.Now().Add(ttl)}
	c.mu.Unlock()
	return nil
}

// Del removes a key.
func (c *Cache) Del(_ context.Context, key string) error {
	c.mu.Lock()
	delete(c.items, key)
	c.mu.Unlock()
	return nil
}

// Incr atomically increments a counter, resetting TTL on each call.
func (c *Cache) Incr(_ context.Context, key string, ttl time.Duration) (int64, error) {
	c.mu.Lock()
	ctr, ok := c.counters[key]
	if !ok {
		ctr = &counter{}
		c.counters[key] = ctr
	}
	c.mu.Unlock()

	ctr.mu.Lock()
	defer ctr.mu.Unlock()
	if time.Now().After(ctr.expiresAt) {
		ctr.val = 0
	}
	ctr.val++
	ctr.expiresAt = time.Now().Add(ttl)
	return ctr.val, nil
}

// Ping reports whether the cache backend is reachable. Memory caches live
// inside the process — they cannot be unreachable, so this is always nil.
// Exists to satisfy the cache.Cache interface contract; readyz handlers
// expect every cache to answer the question regardless of backend.
func (c *Cache) Ping(_ context.Context) error {
	return nil
}

// Close stops the background sweep goroutine.
func (c *Cache) Close() error {
	close(c.stopCh)
	return nil
}

// sweep removes expired entries every 60 seconds.
func (c *Cache) sweep() {
	ticker := time.NewTicker(60 * time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-ticker.C:
			now := time.Now()
			c.mu.Lock()
			for k, e := range c.items {
				if now.After(e.expiresAt) {
					delete(c.items, k)
				}
			}
			c.mu.Unlock()

			c.mu.Lock()
			for k, ctr := range c.counters {
				ctr.mu.Lock()
				expired := now.After(ctr.expiresAt)
				ctr.mu.Unlock()
				if expired {
					delete(c.counters, k)
				}
			}
			c.mu.Unlock()
		case <-c.stopCh:
			return
		}
	}
}
