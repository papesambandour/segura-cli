package credcache

import (
	"errors"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"
)

func TestFreshServedWithoutRefetch(t *testing.T) {
	var calls int32
	c := New(func() ([]string, error) {
		atomic.AddInt32(&calls, 1)
		return []string{"a"}, nil
	}, time.Minute, time.Minute, "")

	// First Get is a live fetch.
	if v, cached, _ := c.Get(); len(v) != 1 || cached {
		t.Fatalf("first Get: v=%v cached=%v", v, cached)
	}
	// Second Get within fresh window: served from cache, no new fetch.
	if v, cached, _ := c.Get(); len(v) != 1 || !cached {
		t.Fatalf("second Get: v=%v cached=%v", v, cached)
	}
	if got := atomic.LoadInt32(&calls); got != 1 {
		t.Fatalf("expected 1 fetch, got %d", got)
	}
}

func TestStaleServedThenBackgroundRefresh(t *testing.T) {
	var calls int32
	c := New(func() ([]string, error) {
		n := atomic.AddInt32(&calls, 1)
		if n == 1 {
			return []string{"v1"}, nil
		}
		return []string{"v2"}, nil
	}, 0, time.Minute, "") // fresh=0 -> immediately stale, but < stale -> SWR

	// Prime the cache.
	c.Get()
	// Now every Get is stale: returns cached immediately AND triggers bg refresh.
	v, cached, _ := c.Get()
	if !cached {
		t.Fatalf("stale Get should be served from cache")
	}
	_ = v
	// Wait for the background refresh to land.
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if v, _, _ := c.Get(); len(v) == 1 && v[0] == "v2" {
			return // refreshed
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("background refresh did not update the cache to v2")
}

func TestErrorFallsBackToStale(t *testing.T) {
	var calls int32
	c := New(func() ([]string, error) {
		n := atomic.AddInt32(&calls, 1)
		if n == 1 {
			return []string{"good"}, nil
		}
		return nil, errors.New("boom")
	}, 0, 0, "") // fresh=stale=0 -> every Get beyond first does a live fetch

	c.Get() // primes "good"
	// Next Get triggers a live fetch that errors -> must serve the stale "good".
	v, cached, err := c.Get()
	if err != nil {
		t.Fatalf("expected stale fallback, got error %v", err)
	}
	if !cached || len(v) != 1 || v[0] != "good" {
		t.Fatalf("expected stale 'good', got v=%v cached=%v", v, cached)
	}
}

func TestDiskPersistenceAcrossInstances(t *testing.T) {
	path := filepath.Join(t.TempDir(), "cache.json")
	var calls int32
	mk := func() *Cache[string] {
		return New(func() ([]string, error) {
			atomic.AddInt32(&calls, 1)
			return []string{"x", "y"}, nil
		}, time.Minute, time.Minute, path)
	}

	// First instance fetches and writes disk.
	if v, cached, _ := mk().Get(); len(v) != 2 || cached {
		t.Fatalf("first instance: v=%v cached=%v", v, cached)
	}
	// A brand-new instance (simulating a new CLI process) must load from disk,
	// serving cached without a second fetch.
	if v, cached, _ := mk().Get(); len(v) != 2 || !cached {
		t.Fatalf("second instance: v=%v cached=%v", v, cached)
	}
	if got := atomic.LoadInt32(&calls); got != 1 {
		t.Fatalf("expected 1 fetch across instances, got %d", got)
	}
}

func TestEmptyCacheReturnsFetchError(t *testing.T) {
	c := New(func() ([]string, error) {
		return nil, errors.New("down")
	}, time.Minute, time.Minute, "")
	if _, _, err := c.Get(); err == nil {
		t.Fatalf("expected error when cache empty and fetch fails")
	}
}
