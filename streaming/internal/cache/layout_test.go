package cache

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestLayout_ShardedPath(t *testing.T) {
	l := New("/var/cache")
	got := l.ShardedPath(TierRemux, "abcdef1234", ".mp4")
	want := filepath.Join("/var/cache", "remux", "ab", "abcdef1234.mp4")
	if got != want {
		t.Fatalf("got %s want %s", got, want)
	}
}

func TestLayout_ShortHashHandled(t *testing.T) {
	l := New("/c")
	// hash too short — should still produce something deterministic.
	got := l.ShardedPath(TierPosters, "x", ".jpg")
	if !strings.Contains(got, "00x.jpg") {
		t.Fatalf("got %s", got)
	}
}

func TestLayout_EnsureTiers(t *testing.T) {
	dir := t.TempDir()
	l := New(dir)
	if err := l.EnsureTiers(); err != nil {
		t.Fatal(err)
	}
	for _, tier := range []string{TierRemux, TierHLS, TierPosters, TierSprites, TierThumbs, TierSubs, TierProbe} {
		if st, err := os.Stat(filepath.Join(dir, tier)); err != nil || !st.IsDir() {
			t.Fatalf("tier %s missing", tier)
		}
	}
}

func TestLayout_PurgeSession(t *testing.T) {
	dir := t.TempDir()
	l := New(dir)
	_ = l.EnsureTiers()
	sess := "sess-1"
	dirPath := l.HLSDir(sess)
	_ = os.MkdirAll(dirPath, 0o755)
	if err := os.WriteFile(filepath.Join(dirPath, "x.ts"), []byte("hi"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := l.PurgeSession(sess); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(dirPath); err == nil {
		t.Fatal("session dir not removed")
	}
}

// fillTier writes n files of size bytes each into tier under root.
func fillTier(t *testing.T, l *Layout, tier string, n, size int) []string {
	t.Helper()
	dir := filepath.Join(l.Root, tier)
	_ = os.MkdirAll(dir, 0o755)
	paths := []string{}
	for i := 0; i < n; i++ {
		shard := filepath.Join(dir, "ab")
		_ = os.MkdirAll(shard, 0o755)
		p := filepath.Join(shard, "f"+itoa(i)+".bin")
		body := make([]byte, size)
		if err := os.WriteFile(p, body, 0o644); err != nil {
			t.Fatal(err)
		}
		paths = append(paths, p)
	}
	return paths
}

func itoa(n int) string { return strings.TrimLeft("0123456789"[n%10:n%10+1], "") }

func TestGC_NoSweepBelowHighWater(t *testing.T) {
	dir := t.TempDir()
	l := New(dir)
	_ = l.EnsureTiers()
	fillTier(t, l, TierRemux, 3, 1024)
	gc := NewGC(l, GCConfig{MaxBytes: 1 << 20, HighWater: 1 << 20, LowWater: 512 * 1024})
	bytes, evicted, err := gc.Sweep()
	if err != nil {
		t.Fatal(err)
	}
	if evicted != 0 {
		t.Fatalf("evicted=%d, expected 0", evicted)
	}
	if bytes <= 0 {
		t.Fatal("bytes was 0")
	}
}

func TestGC_EvictsAboveHighWater(t *testing.T) {
	dir := t.TempDir()
	l := New(dir)
	_ = l.EnsureTiers()
	// Fill 8 KB of remux files.
	paths := fillTier(t, l, TierRemux, 8, 1024)
	// Set mtimes apart to give the LRU a clear order.
	now := time.Now()
	for i, p := range paths {
		if err := os.Chtimes(p, now.Add(time.Duration(i)*time.Second), now.Add(time.Duration(i)*time.Second)); err != nil {
			t.Fatal(err)
		}
	}

	gc := NewGC(l, GCConfig{MaxBytes: 5 * 1024, HighWater: 5 * 1024, LowWater: 3 * 1024})
	_, evicted, err := gc.Sweep()
	if err != nil {
		t.Fatal(err)
	}
	if evicted < 5 {
		t.Fatalf("evicted=%d", evicted)
	}
	bytesNow, err := gc.Stat()
	if err != nil {
		t.Fatal(err)
	}
	if bytesNow > 3*1024 {
		t.Fatalf("bytes=%d should be ≤ 3072", bytesNow)
	}
}

func TestGC_PostersFloorRespected(t *testing.T) {
	dir := t.TempDir()
	l := New(dir)
	_ = l.EnsureTiers()
	// posters: 4 files * 1024 = 4096
	posterPaths := fillTier(t, l, TierPosters, 4, 1024)
	// remux: 4 files * 1024 = 4096
	_ = fillTier(t, l, TierRemux, 4, 1024)

	now := time.Now()
	for i, p := range posterPaths {
		// Posters are oldest in atime — without floor they'd be evicted first.
		_ = os.Chtimes(p, now.Add(-time.Duration(10-i)*time.Hour), now.Add(-time.Duration(10-i)*time.Hour))
	}

	gc := NewGC(l, GCConfig{
		MaxBytes:          5 * 1024,
		HighWater:         5 * 1024,
		LowWater:          3 * 1024,
		PostersFloorBytes: 4096,
	})
	_, evicted, err := gc.Sweep()
	if err != nil {
		t.Fatal(err)
	}
	if evicted == 0 {
		t.Fatal("expected some eviction")
	}
	// Posters should all still exist because of the floor.
	for _, p := range posterPaths {
		if _, err := os.Stat(p); err != nil {
			t.Fatalf("poster %s evicted despite floor: %v", p, err)
		}
	}
}
