package marketdata

import (
	"context"
	"sync"
	"time"
)

// Cache stores one endpoint result and coalesces concurrent refreshes.
type Cache[T any] struct {
	mu        sync.Mutex
	value     T
	fetchedAt time.Time
	valid     bool
	loading   bool
	done      chan struct{}
}

type KeyedCache[K comparable, V any] struct {
	mu     sync.Mutex
	values map[K]*Cache[V]
}

func (c *KeyedCache[K, V]) Get(ctx context.Context, key K, ttl time.Duration, fetch func(context.Context) (V, error)) (V, error) {
	c.mu.Lock()
	if c.values == nil {
		c.values = make(map[K]*Cache[V])
	}
	item := c.values[key]
	if item == nil {
		item = &Cache[V]{}
		c.values[key] = item
	}
	c.mu.Unlock()
	return item.Get(ctx, ttl, fetch)
}

func (c *Cache[T]) Get(ctx context.Context, ttl time.Duration, fetch func(context.Context) (T, error)) (T, error) {
	for {
		c.mu.Lock()
		if c.valid && ttl > 0 && time.Since(c.fetchedAt) < ttl {
			value := c.value
			c.mu.Unlock()
			return value, nil
		}
		if !c.loading {
			c.loading = true
			c.done = make(chan struct{})
			done := c.done
			c.mu.Unlock()

			value, err := fetch(ctx)

			c.mu.Lock()
			if err == nil {
				c.value = value
				c.fetchedAt = time.Now()
				c.valid = true
			}
			c.loading = false
			close(done)
			c.mu.Unlock()
			return value, err
		}
		done := c.done
		c.mu.Unlock()

		select {
		case <-ctx.Done():
			var zero T
			return zero, ctx.Err()
		case <-done:
		}
	}
}

func (c *Cache[T]) Clear() {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.valid = false
}
