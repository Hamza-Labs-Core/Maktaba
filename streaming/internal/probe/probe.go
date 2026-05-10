// Package probe owns the read side of the videos→media_info join the
// Streaming service consumes (Story 8.15). The probe is *never*
// generated here — Pipeline writes media_info; we LRU-cache reads and
// fall back to Postgres on miss.
package probe

import (
	"container/list"
	"context"
	"errors"
	"sync"
	"time"

	"github.com/google/uuid"
)

// Row is the metadata Streaming consumes for a video. Subset of the
// Pipeline's media_info row; we only carry the fields that drive the
// matrix (Story 8.2) and the byte-pumping handlers (8.3–8.5).
type Row struct {
	VideoID       uuid.UUID
	LibraryID     uuid.UUID
	ContentHash   string // sha-256 of source bytes; key for evict
	Path          string // absolute path on the read-only media volume
	Container     string
	VideoCodec    string
	AudioCodec    string
	HDR           string
	Height        int
	Width         int
	DurationSec   float64
	BitrateKbps   int
	AudioChannels int
	MIMEType      string // for direct-play Content-Type
	Size          int64
	ModTime       time.Time
	Probed        bool
}

// Errors.
var (
	ErrNotProbed = errors.New("video has no media_info row — Pipeline must probe first")
	ErrNotFound  = errors.New("video not found")
)

// Backend is the read-side persistence layer. Production wires a
// pgx-backed implementation; tests use the in-memory FakeBackend.
type Backend interface {
	Lookup(ctx context.Context, videoID uuid.UUID) (*Row, error)
}

// Cache is an LRU on top of a Backend. The architecture pins the size
// at "a few thousand entries" — defaults to 4096 here.
type Cache struct {
	backend Backend
	cap     int

	mu     sync.Mutex
	items  map[uuid.UUID]*list.Element
	hashes map[string]map[uuid.UUID]struct{} // contentHash → set of VideoIDs (for evict)
	order  *list.List
}

type entry struct {
	id  uuid.UUID
	row *Row
}

// NewCache builds an LRU sitting in front of backend.
func NewCache(backend Backend, capacity int) *Cache {
	if capacity <= 0 {
		capacity = 4096
	}
	return &Cache{
		backend: backend,
		cap:     capacity,
		items:   map[uuid.UUID]*list.Element{},
		hashes:  map[string]map[uuid.UUID]struct{}{},
		order:   list.New(),
	}
}

// Lookup returns the probe row for videoID. Hits the cache first;
// falls through to the backend on miss. AC-3 forbids us from probing
// the file ourselves — the backend returns ErrNotProbed if no row.
func (c *Cache) Lookup(ctx context.Context, videoID uuid.UUID) (*Row, error) {
	c.mu.Lock()
	if el, ok := c.items[videoID]; ok {
		c.order.MoveToFront(el)
		row := el.Value.(*entry).row
		c.mu.Unlock()
		return row, nil
	}
	c.mu.Unlock()

	row, err := c.backend.Lookup(ctx, videoID)
	if err != nil {
		return nil, err
	}
	c.put(videoID, row)
	return row, nil
}

func (c *Cache) put(id uuid.UUID, row *Row) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if el, ok := c.items[id]; ok {
		el.Value = &entry{id: id, row: row}
		c.order.MoveToFront(el)
		return
	}
	el := c.order.PushFront(&entry{id: id, row: row})
	c.items[id] = el
	if row.ContentHash != "" {
		set, ok := c.hashes[row.ContentHash]
		if !ok {
			set = map[uuid.UUID]struct{}{}
			c.hashes[row.ContentHash] = set
		}
		set[id] = struct{}{}
	}
	for c.order.Len() > c.cap {
		oldest := c.order.Back()
		if oldest == nil {
			break
		}
		c.order.Remove(oldest)
		ent := oldest.Value.(*entry)
		delete(c.items, ent.id)
		if ent.row.ContentHash != "" {
			if set, ok := c.hashes[ent.row.ContentHash]; ok {
				delete(set, ent.id)
				if len(set) == 0 {
					delete(c.hashes, ent.row.ContentHash)
				}
			}
		}
	}
}

// EvictHash drops every cache entry whose content_hash matches.
// Pipeline calls streaming.EvictHashCache after re-probing a file
// that may have been modified in-place (Story 8.15 AC-4).
//
// Returns the number of entries removed.
func (c *Cache) EvictHash(hash string) int {
	c.mu.Lock()
	defer c.mu.Unlock()
	set, ok := c.hashes[hash]
	if !ok {
		return 0
	}
	n := 0
	for id := range set {
		if el, ok := c.items[id]; ok {
			c.order.Remove(el)
			delete(c.items, id)
			n++
		}
	}
	delete(c.hashes, hash)
	return n
}

// Len exposes the current cache size for metrics/tests.
func (c *Cache) Len() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.order.Len()
}

// FakeBackend is an in-memory Backend for tests and the dev server.
type FakeBackend struct {
	mu   sync.Mutex
	rows map[uuid.UUID]*Row
}

// NewFakeBackend builds an empty fake.
func NewFakeBackend() *FakeBackend { return &FakeBackend{rows: map[uuid.UUID]*Row{}} }

// Set installs a row.
func (f *FakeBackend) Set(row *Row) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.rows[row.VideoID] = row
}

// Lookup implements Backend.
func (f *FakeBackend) Lookup(_ context.Context, videoID uuid.UUID) (*Row, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	row, ok := f.rows[videoID]
	if !ok {
		return nil, ErrNotFound
	}
	if !row.Probed {
		return nil, ErrNotProbed
	}
	return row, nil
}
