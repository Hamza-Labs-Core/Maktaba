package probe

import (
	"context"
	"sync/atomic"
	"testing"

	"github.com/google/uuid"
)

func mkRow(hash string) *Row {
	return &Row{
		VideoID:     uuid.New(),
		LibraryID:   uuid.New(),
		ContentHash: hash,
		Path:        "/var/media/video.mkv",
		Container:   "mkv",
		VideoCodec:  "h264",
		AudioCodec:  "aac",
		Height:      1080,
		Width:       1920,
		Probed:      true,
	}
}

type countingBackend struct {
	*FakeBackend
	calls atomic.Int64
}

func (c *countingBackend) Lookup(ctx context.Context, id uuid.UUID) (*Row, error) {
	c.calls.Add(1)
	return c.FakeBackend.Lookup(ctx, id)
}

func TestCache_HitAvoidsBackend(t *testing.T) {
	cb := &countingBackend{FakeBackend: NewFakeBackend()}
	c := NewCache(cb, 16)
	row := mkRow("h1")
	cb.Set(row)

	if _, err := c.Lookup(context.Background(), row.VideoID); err != nil {
		t.Fatalf("first: %v", err)
	}
	if _, err := c.Lookup(context.Background(), row.VideoID); err != nil {
		t.Fatalf("second: %v", err)
	}
	if cb.calls.Load() != 1 {
		t.Fatalf("calls=%d, expected 1 (cache should hit)", cb.calls.Load())
	}
}

func TestCache_MissCallsBackend(t *testing.T) {
	cb := &countingBackend{FakeBackend: NewFakeBackend()}
	c := NewCache(cb, 16)
	id := uuid.New()
	if _, err := c.Lookup(context.Background(), id); err == nil {
		t.Fatal("expected ErrNotFound")
	}
	if cb.calls.Load() != 1 {
		t.Fatalf("calls=%d", cb.calls.Load())
	}
}

func TestCache_NotProbedReturnsErrNotProbed(t *testing.T) {
	fb := NewFakeBackend()
	c := NewCache(fb, 16)
	r := mkRow("h1")
	r.Probed = false
	fb.Set(r)
	_, err := c.Lookup(context.Background(), r.VideoID)
	if err != ErrNotProbed {
		t.Fatalf("err=%v want ErrNotProbed", err)
	}
}

func TestCache_EvictHashByContentHash(t *testing.T) {
	fb := NewFakeBackend()
	c := NewCache(fb, 16)
	a := mkRow("hash-A")
	b := mkRow("hash-A")
	d := mkRow("hash-B")
	fb.Set(a)
	fb.Set(b)
	fb.Set(d)
	_, _ = c.Lookup(context.Background(), a.VideoID)
	_, _ = c.Lookup(context.Background(), b.VideoID)
	_, _ = c.Lookup(context.Background(), d.VideoID)
	if c.Len() != 3 {
		t.Fatalf("len=%d", c.Len())
	}

	if n := c.EvictHash("hash-A"); n != 2 {
		t.Fatalf("evicted=%d want 2", n)
	}
	if c.Len() != 1 {
		t.Fatalf("len after evict=%d want 1", c.Len())
	}
}

func TestCache_LRUEvictionByCapacity(t *testing.T) {
	fb := NewFakeBackend()
	c := NewCache(fb, 2)
	rows := []*Row{mkRow("x"), mkRow("y"), mkRow("z")}
	for _, r := range rows {
		fb.Set(r)
		_, _ = c.Lookup(context.Background(), r.VideoID)
	}
	if c.Len() != 2 {
		t.Fatalf("len=%d", c.Len())
	}
}
