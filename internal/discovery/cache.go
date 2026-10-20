package discovery

import (
	"slices"
	"sync"
)

// Cache is a small concurrency-safe map; a nil *Cache is a cache that remembers nothing.
type Cache[V any] struct {
	mu sync.Mutex
	m  map[string]V
}

// NewCache returns an empty Cache.
func NewCache[V any]() *Cache[V] { return &Cache[V]{m: map[string]V{}} }

// Get returns the value for key and whether it was present.
func (c *Cache[V]) Get(key string) (V, bool) {
	if c == nil {
		var zero V
		return zero, false
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	v, ok := c.m[key]
	return v, ok
}

// Set stores v under key.
func (c *Cache[V]) Set(key string, v V) {
	if c == nil {
		return
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	c.m[key] = v
}

// Delete removes key, if present.
func (c *Cache[V]) Delete(key string) {
	if c == nil {
		return
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	delete(c.m, key)
}

// Keys returns the keys in sorted order.
func (c *Cache[V]) Keys() []string {
	if c == nil {
		return nil
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	keys := make([]string, 0, len(c.m))
	for k := range c.m {
		keys = append(keys, k)
	}
	slices.Sort(keys)
	return keys
}

// Len returns the number of entries.
func (c *Cache[V]) Len() int { return len(c.Keys()) }
