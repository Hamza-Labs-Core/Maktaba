// Package cache implements Story 8.14 — disk layout, sharding, and
// the LRU GC sweeper. The cache root holds four tiers:
//
//	{root}/remux/{hash[:2]}/{hash}.mp4
//	{root}/hls/{session_id}/...
//	{root}/posters/{hash[:2]}/{hash}.jpg
//	{root}/sprites/{hash[:2]}/{hash}.{webp,vtt}
//	{root}/thumbs/{video_id}/...
//	{root}/subs/{hash[:2]}/{hash}.vtt
//	{root}/probe/{hash[:2]}/{hash}.json    (Story 8.15 fallback)
//
// HLS sessions are excluded from the byte cap because they're purged
// on session close (Story 8.9). The other tiers share one cap
// (default 50 GiB) with soft floors so posters/sprites aren't
// preferentially evicted (their regeneration requires Pipeline).
package cache

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"sync"
	"sync/atomic"
	"time"
)

// Tier names — also used as subdirectories under root.
const (
	TierRemux   = "remux"
	TierHLS     = "hls"
	TierPosters = "posters"
	TierSprites = "sprites"
	TierThumbs  = "thumbs"
	TierSubs    = "subs"
	TierProbe   = "probe"
)

// Layout owns the on-disk shape and provides shard-aware path helpers.
type Layout struct {
	Root string
}

// New returns a Layout. The directory is created on first write.
func New(root string) *Layout { return &Layout{Root: root} }

// EnsureTiers materializes the per-tier subfolders.
func (l *Layout) EnsureTiers() error {
	tiers := []string{TierRemux, TierHLS, TierPosters, TierSprites, TierThumbs, TierSubs, TierProbe}
	for _, t := range tiers {
		if err := os.MkdirAll(filepath.Join(l.Root, t), 0o755); err != nil {
			return err
		}
	}
	return nil
}

// ShardedPath returns the two-char-hash-shard path under tier.
// hash must be at least 2 chars.
func (l *Layout) ShardedPath(tier, hash, ext string) string {
	if len(hash) < 2 {
		hash = "00" + hash
	}
	return filepath.Join(l.Root, tier, hash[:2], hash+ext)
}

// HLSDir returns the per-session HLS folder.
func (l *Layout) HLSDir(sessionID string) string {
	return filepath.Join(l.Root, TierHLS, sessionID)
}

// ThumbsDir returns the per-video chapter-thumb folder.
func (l *Layout) ThumbsDir(videoID string) string {
	return filepath.Join(l.Root, TierThumbs, videoID)
}

// GCConfig governs the sweeper.
type GCConfig struct {
	// MaxBytes is the combined cap across remux + posters + sprites
	// + thumbs + subs + probe. HLS is excluded.
	MaxBytes int64

	// HighWater triggers a sweep — usually MaxBytes.
	HighWater int64

	// LowWater is the target after a sweep — typically MaxBytes * 0.9.
	LowWater int64

	// Interval is the sweeper period (default 5 min per AC-2).
	Interval time.Duration

	// PostersFloorBytes is the soft minimum for posters+sprites
	// before the GC starts touching them (Story 8.14 AC-3 default 1
	// GiB).
	PostersFloorBytes int64
}

// GC is the LRU sweeper.
type GC struct {
	layout *Layout
	cfg    GCConfig
	now    func() time.Time

	totalBytes  atomic.Int64
	evicted     atomic.Uint64
	lastSweepAt atomic.Int64
}

// NewGC returns a configured sweeper. Run() starts the loop.
func NewGC(layout *Layout, cfg GCConfig) *GC {
	if cfg.MaxBytes <= 0 {
		cfg.MaxBytes = 50 * 1024 * 1024 * 1024
	}
	if cfg.HighWater <= 0 {
		cfg.HighWater = cfg.MaxBytes
	}
	if cfg.LowWater <= 0 {
		cfg.LowWater = cfg.MaxBytes * 9 / 10
	}
	if cfg.Interval <= 0 {
		cfg.Interval = 5 * time.Minute
	}
	if cfg.PostersFloorBytes <= 0 {
		cfg.PostersFloorBytes = 1024 * 1024 * 1024
	}
	return &GC{layout: layout, cfg: cfg, now: time.Now}
}

// SetClock replaces the wall clock — for tests.
func (g *GC) SetClock(now func() time.Time) { g.now = now }

// Sweep walks the cache tiers (excluding HLS), measures total bytes,
// and if over HighWater evicts least-recently-accessed files until
// usage is below LowWater. Posters/sprites have a soft floor.
//
// Returns (bytesAfter, evictedCount).
func (g *GC) Sweep() (int64, int, error) {
	files, total, err := g.scan()
	if err != nil {
		return 0, 0, err
	}
	g.totalBytes.Store(total)

	if total <= g.cfg.HighWater {
		return total, 0, nil
	}

	// LRU: oldest atime first.
	sort.Slice(files, func(i, j int) bool { return files[i].atime.Before(files[j].atime) })

	var evicted int
	postersBytes := tierBytes(files, TierPosters) + tierBytes(files, TierSprites)
	for _, f := range files {
		if total <= g.cfg.LowWater {
			break
		}
		// Respect posters/sprites floor (Story 8.14 AC-3).
		if (f.tier == TierPosters || f.tier == TierSprites) && postersBytes <= g.cfg.PostersFloorBytes {
			continue
		}
		if err := os.Remove(f.path); err != nil {
			continue
		}
		total -= f.size
		if f.tier == TierPosters || f.tier == TierSprites {
			postersBytes -= f.size
		}
		evicted++
	}
	g.totalBytes.Store(total)
	g.evicted.Add(uint64(evicted))
	g.lastSweepAt.Store(g.now().UnixNano())
	return total, evicted, nil
}

type fileEntry struct {
	path  string
	tier  string
	size  int64
	atime time.Time
}

func (g *GC) scan() ([]fileEntry, int64, error) {
	files := []fileEntry{}
	var total int64
	tiers := []string{TierRemux, TierPosters, TierSprites, TierThumbs, TierSubs, TierProbe}
	for _, tier := range tiers {
		dir := filepath.Join(g.layout.Root, tier)
		err := filepath.WalkDir(dir, func(path string, d fs.DirEntry, err error) error {
			if err != nil {
				if errors.Is(err, fs.ErrNotExist) {
					return nil
				}
				return err
			}
			if d.IsDir() {
				return nil
			}
			info, err := d.Info()
			if err != nil {
				return nil
			}
			files = append(files, fileEntry{
				path:  path,
				tier:  tier,
				size:  info.Size(),
				atime: info.ModTime(), // atime varies by FS — fall back to mtime
			})
			total += info.Size()
			return nil
		})
		if err != nil {
			return nil, 0, err
		}
	}
	return files, total, nil
}

func tierBytes(files []fileEntry, tier string) int64 {
	var n int64
	for _, f := range files {
		if f.tier == tier {
			n += f.size
		}
	}
	return n
}

// TotalBytes is the most recent total observed in a sweep.
func (g *GC) TotalBytes() int64 { return g.totalBytes.Load() }

// EvictedCount is the lifetime evicted file count.
func (g *GC) EvictedCount() uint64 { return g.evicted.Load() }

// LastSweep returns the wall time of the most recent successful sweep.
func (g *GC) LastSweep() time.Time {
	v := g.lastSweepAt.Load()
	if v == 0 {
		return time.Time{}
	}
	return time.Unix(0, v)
}

// PurgeSession removes the entire HLS folder for a session — called
// from session.Close so the cache cap excludes per-session HLS bytes.
func (l *Layout) PurgeSession(sessionID string) error {
	return os.RemoveAll(l.HLSDir(sessionID))
}

// Stat returns total bytes across all non-HLS tiers without sweeping.
func (g *GC) Stat() (int64, error) {
	_, total, err := g.scan()
	return total, err
}

var _ = sync.RWMutex{} // future-proof: planned for live-readers metric
var _ = fmt.Sprintf    // keep formatting helper available for future logs
