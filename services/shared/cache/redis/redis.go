// Package redis provides a Redis-backed implementation of cache.Cache.
package redis

import (
	"context"
	"errors"
	"time"

	"github.com/redis/go-redis/v9"
	"axiaops.io/shared/observability"
)

// ErrNotFound is returned by Get when the key does not exist or has expired.
var ErrNotFound = errors.New("cache: key not found")

// Client wraps a Redis client and implements cache.Cache.
type Client struct {
	rdb *redis.Client
}

// New creates a Redis cache client from the given URL.
func New(redisURL string) (*Client, error) {
	opts, err := redis.ParseURL(redisURL)
	if err != nil {
		return nil, err
	}
	rdb := redis.NewClient(opts)
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	if err := rdb.Ping(ctx).Err(); err != nil {
		return nil, err
	}
	return &Client{rdb: rdb}, nil
}

// Get retrieves a value by key. Returns ErrNotFound on miss.
func (c *Client) Get(ctx context.Context, key string) ([]byte, error) {
	obs := observability.NewDatabaseObserver("REDIS_GET")
	val, err := c.rdb.Get(ctx, key).Bytes()
	if errors.Is(err, redis.Nil) {
		obs.Observe()
		return nil, ErrNotFound
	}
	if err != nil {
		obs.ObserveError()
		return nil, err
	}
	obs.Observe()
	return val, nil
}

// Set stores a value with the given TTL.
func (c *Client) Set(ctx context.Context, key string, value []byte, ttl time.Duration) error {
	obs := observability.NewDatabaseObserver("REDIS_SET")
	err := c.rdb.Set(ctx, key, value, ttl).Err()
	if err != nil {
		obs.ObserveError()
		return err
	}
	obs.Observe()
	return nil
}

// Del removes a key.
func (c *Client) Del(ctx context.Context, key string) error {
	obs := observability.NewDatabaseObserver("REDIS_DEL")
	err := c.rdb.Del(ctx, key).Err()
	if err != nil {
		obs.ObserveError()
		return err
	}
	obs.Observe()
	return nil
}

// Incr atomically increments a counter. TTL is applied on every call
// (Redis EXPIRE resets the TTL, which is correct for sliding-window counters).
func (c *Client) Incr(ctx context.Context, key string, ttl time.Duration) (int64, error) {
	obs := observability.NewDatabaseObserver("REDIS_INCR")
	pipe := c.rdb.Pipeline()
	incrCmd := pipe.Incr(ctx, key)
	pipe.Expire(ctx, key, ttl)
	_, err := pipe.Exec(ctx)
	if err != nil {
		obs.ObserveError()
		return 0, err
	}
	obs.Observe()
	return incrCmd.Val(), nil
}

// Close closes the underlying Redis connection.
func (c *Client) Close() error {
	return c.rdb.Close()
}
