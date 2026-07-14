// Package credcache is a cache-first, stale-while-revalidate cache for slow
// lookups (the senhasegura credential list). It serves cached data immediately
// whenever possible and refreshes intelligently:
//
//   - age < fresh  -> serve cached, no refresh (fast path)
//   - age < stale  -> serve cached NOW, refresh once in the background (SWR)
//   - otherwise    -> block on a live fetch (nothing usable to serve)
//
// On a live-fetch error any previously cached value is served rather than
// failing. It is generic so it introduces no import cycle, and can persist to
// disk so short-lived processes (the CLI) benefit across invocations.
package credcache

import (
	"encoding/json"
	"os"
	"sync"
	"time"
)

// Cache holds credentials of type T and refreshes them via fetch.
type Cache[T any] struct {
	fetch    func() ([]T, error)
	fresh    time.Duration
	stale    time.Duration
	diskPath string // "" disables persistence

	mu         sync.Mutex
	items      []T
	fetchedAt  time.Time
	loaded     bool // disk load attempted
	refreshing bool // a background refresh is in flight
}

// New builds a cache. fresh is how long data is served without any refresh;
// stale is the outer bound during which stale data is served while a background
// refresh runs. Set stale == fresh to disable background revalidation (e.g. a
// short-lived CLI process where a goroutine would not outlive the call). diskPath
// enables JSON persistence; "" keeps it in-memory only.
func New[T any](fetch func() ([]T, error), fresh, stale time.Duration, diskPath string) *Cache[T] {
	if stale < fresh {
		stale = fresh
	}
	return &Cache[T]{fetch: fetch, fresh: fresh, stale: stale, diskPath: diskPath}
}

// Get returns credentials cache-first. It only blocks on a live fetch when there
// is no usable cached data; otherwise it returns immediately (kicking a
// background refresh when the data is merely stale). The bool reports whether the
// result came from cache (true) or a live fetch (false).
func (c *Cache[T]) Get() ([]T, bool, error) {
	c.mu.Lock()
	c.loadFromDiskLocked()
	have := c.items != nil
	age := time.Since(c.fetchedAt)

	if have && age < c.fresh {
		items := c.items
		c.mu.Unlock()
		return items, true, nil
	}
	if have && age < c.stale {
		items := c.items
		c.startBackgroundRefreshLocked()
		c.mu.Unlock()
		return items, true, nil
	}
	c.mu.Unlock()

	// Nothing usable — fetch live, falling back to any stale copy on error.
	items, err := c.doFetch()
	if err != nil {
		c.mu.Lock()
		fallback := c.items
		c.mu.Unlock()
		if fallback != nil {
			return fallback, true, nil
		}
		return nil, false, err
	}
	return items, false, nil
}

// Refresh forces a live fetch and updates the cache.
func (c *Cache[T]) Refresh() ([]T, error) {
	items, err := c.doFetch()
	return items, err
}

// Warm triggers a background fetch if the cache is empty or stale, so a later
// Get is instant. Safe to call at startup.
func (c *Cache[T]) Warm() {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.loadFromDiskLocked()
	if c.items == nil || time.Since(c.fetchedAt) >= c.fresh {
		c.startBackgroundRefreshLocked()
	}
}

func (c *Cache[T]) doFetch() ([]T, error) {
	items, err := c.fetch()
	if err != nil {
		return nil, err
	}
	c.mu.Lock()
	c.storeLocked(items)
	c.mu.Unlock()
	return items, nil
}

// startBackgroundRefreshLocked launches a single background refresh. Caller holds mu.
func (c *Cache[T]) startBackgroundRefreshLocked() {
	if c.refreshing {
		return
	}
	c.refreshing = true
	go func() {
		items, err := c.fetch()
		c.mu.Lock()
		c.refreshing = false
		if err == nil && items != nil {
			c.storeLocked(items)
		}
		c.mu.Unlock()
	}()
}

// storeLocked updates the in-memory value and persists it. Caller holds mu.
func (c *Cache[T]) storeLocked(items []T) {
	c.items = items
	c.fetchedAt = time.Now()
	c.saveToDiskLocked()
}

type diskEntry[T any] struct {
	Items     []T       `json:"items"`
	FetchedAt time.Time `json:"fetched_at"`
}

func (c *Cache[T]) loadFromDiskLocked() {
	if c.loaded {
		return
	}
	c.loaded = true
	if c.diskPath == "" {
		return
	}
	data, err := os.ReadFile(c.diskPath)
	if err != nil {
		return
	}
	var e diskEntry[T]
	if json.Unmarshal(data, &e) == nil && e.Items != nil {
		c.items = e.Items
		c.fetchedAt = e.FetchedAt
	}
}

func (c *Cache[T]) saveToDiskLocked() {
	if c.diskPath == "" {
		return
	}
	data, err := json.Marshal(diskEntry[T]{Items: c.items, FetchedAt: c.fetchedAt})
	if err != nil {
		return
	}
	tmp := c.diskPath + ".tmp"
	if os.WriteFile(tmp, data, 0o600) == nil {
		_ = os.Rename(tmp, c.diskPath)
	}
}
