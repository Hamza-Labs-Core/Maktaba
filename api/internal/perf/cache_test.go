package perf

import (
	"testing"
	"time"
)

func TestCachePutGet(t *testing.T) {
	c := NewCache[string]("manifest", 10, time.Minute)
	c.Put("a", "x")
	v, ok := c.Get("a")
	if !ok || v != "x" {
		t.Fatalf("Get: %v %v", v, ok)
	}
}

func TestCacheMissReturnsZero(t *testing.T) {
	c := NewCache[int]("seg", 4, time.Minute)
	v, ok := c.Get("missing")
	if ok || v != 0 {
		t.Fatalf("got %v %v", v, ok)
	}
}

func TestCacheExpiry(t *testing.T) {
	c := NewCache[string]("manifest", 4, 10*time.Millisecond)
	now := time.Now()
	c.now = func() time.Time { return now }
	c.Put("a", "x")
	now = now.Add(time.Second)
	if _, ok := c.Get("a"); ok {
		t.Fatal("entry should have expired")
	}
}

func TestCacheLRUEviction(t *testing.T) {
	c := NewCache[string]("seg", 2, time.Minute)
	c.Put("a", "1")
	time.Sleep(2 * time.Millisecond)
	c.Put("b", "2")
	time.Sleep(2 * time.Millisecond)
	_, _ = c.Get("a") // touch a so b is the LRU
	time.Sleep(2 * time.Millisecond)
	c.Put("c", "3")
	if _, ok := c.Get("b"); ok {
		t.Fatal("b should have been evicted")
	}
}

func TestCacheStatsTrackHitRate(t *testing.T) {
	c := NewCache[int]("k", 4, time.Minute)
	c.Put("a", 1)
	_, _ = c.Get("a")
	_, _ = c.Get("a")
	_, _ = c.Get("nope")
	s := c.Stats()
	if s.Hits != 2 || s.Misses != 1 {
		t.Fatalf("stats: %+v", s)
	}
	if s.HitRate < 0.66 || s.HitRate > 0.67 {
		t.Fatalf("hit rate: %v", s.HitRate)
	}
}

func TestRegistryFlush(t *testing.T) {
	c := NewCache[int]("k", 4, time.Minute)
	r := NewRegistry()
	r.Register(c)
	c.Put("a", 1)
	got, ok := r.Lookup("k")
	if !ok {
		t.Fatal("not registered")
	}
	got.Flush()
	if _, present := c.Get("a"); present {
		t.Fatal("flush did not clear")
	}
	if names := r.Names(); len(names) != 1 || names[0] != "k" {
		t.Fatalf("names: %+v", names)
	}
}
