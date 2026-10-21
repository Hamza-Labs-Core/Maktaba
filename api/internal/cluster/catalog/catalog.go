// Package catalog is a full node's own copy of what playback needs from the home's
// database (PR #145): each video's library, file path and length, and this machine's
// share map (canonical share → local mount). The node keeps it in its state directory
// (MAKTABA_NODE_STATE_DIR), refreshes it from the home's database while that answers
// (Run), and reads it when it does not, so a node with the home's database down, even
// one rebooted in the outage, still plays the videos it can read. Paths and numbers
// only (no pictures, no people): who may play stays the home's last answer for the
// person, checked against the video's library as on the home.
package catalog

import (
	"bytes"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"sync"
	"time"
)

// FileName is the catalog's file in the node's state directory.
const FileName = "api-catalog.json.gz"

// SyncEvery is how often the catalog is refreshed while the home's database answers.
const SyncEvery = 10 * time.Minute

// version of the file's shape; a file of another version is not read.
const version = 1

// Video is what playback needs of one video.
type Video struct {
	LibraryID   string  `json:"l"`
	Path        string  `json:"p"`
	DurationSec float64 `json:"d,omitempty"`
}

type file struct {
	Version  int               `json:"version"`
	SyncedAt time.Time         `json:"synced_at"`
	ShareMap map[string]string `json:"share_map,omitempty"`
	Videos   map[string]Video  `json:"videos"`
}

// Catalog is the node's copy, in memory and in its file.
type Catalog struct {
	// Path is the file ("": memory only).
	Path   string
	Logger *slog.Logger

	mu     sync.RWMutex
	f      file
	digest [32]byte
}

// Open loads the catalog's file. A missing file is an empty catalog; an unreadable one
// too (logged), which the next sync replaces.
func Open(path string, logger *slog.Logger) *Catalog {
	c := &Catalog{Path: path, Logger: logger, f: file{Version: version, Videos: map[string]Video{}}}
	if path == "" {
		return c
	}
	f, err := read(path)
	switch {
	case errors.Is(err, os.ErrNotExist):
	case err != nil:
		c.warn("node: this machine's playback catalog is unreadable; the next refresh replaces it",
			"path", path, "err", err, "event", "node_catalog_unreadable")
	default:
		c.f = f
		c.digest = digest(f)
	}
	return c
}

func read(path string) (file, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return file{}, err
	}
	zr, err := gzip.NewReader(bytes.NewReader(raw))
	if err != nil {
		return file{}, err
	}
	var f file
	if err := json.NewDecoder(zr).Decode(&f); err != nil {
		return file{}, err
	}
	if f.Version != version {
		return file{}, fmt.Errorf("catalog version %d, this code reads %d", f.Version, version)
	}
	if f.Videos == nil {
		f.Videos = map[string]Video{}
	}
	return f, nil
}

// Video is one video's entry.
func (c *Catalog) Video(id string) (Video, bool) {
	c.mu.RLock()
	defer c.mu.RUnlock()
	v, ok := c.f.Videos[id]
	return v, ok
}

// ShareMap is this machine's share map as last read ({canonical: local}).
func (c *Catalog) ShareMap() map[string]string {
	c.mu.RLock()
	defer c.mu.RUnlock()
	out := make(map[string]string, len(c.f.ShareMap))
	for k, v := range c.f.ShareMap {
		out[k] = v
	}
	return out
}

// SyncedAt is when the catalog was last read from the home's database (zero: never).
func (c *Catalog) SyncedAt() time.Time {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.f.SyncedAt
}

// Len is the number of videos in the catalog.
func (c *Catalog) Len() int {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return len(c.f.Videos)
}

const videosSQL = `SELECT id, library_id, path, duration_sec FROM videos`

// shareMapSQL is the node's own share map (cluster_node_beat answers the node's row,
// slot 0103), as sharepath.LoadNode reads it.
const shareMapSQL = `SELECT COALESCE(path_map, '{}'::jsonb)::text FROM cluster_node_beat($1)`

// Sync reads the catalog from the home's database and writes the file when it changed.
// A failed read leaves the catalog and its file as they were. nodeID "" skips the
// share map.
func (c *Catalog) Sync(ctx context.Context, db *sql.DB, nodeID string) error {
	rows, err := db.QueryContext(ctx, videosSQL)
	if err != nil {
		return err
	}
	defer rows.Close()
	videos := map[string]Video{}
	for rows.Next() {
		var id string
		var lib, path sql.NullString
		var dur sql.NullFloat64
		if err := rows.Scan(&id, &lib, &path, &dur); err != nil {
			return err
		}
		videos[id] = Video{LibraryID: lib.String, Path: path.String, DurationSec: dur.Float64}
	}
	if err := rows.Err(); err != nil {
		return err
	}
	f := file{Version: version, Videos: videos}
	if nodeID != "" {
		var raw string
		if err := db.QueryRowContext(ctx, shareMapSQL, nodeID).Scan(&raw); err != nil {
			return fmt.Errorf("share map: %w", err)
		}
		if err := json.Unmarshal([]byte(raw), &f.ShareMap); err != nil {
			return fmt.Errorf("share map: %w", err)
		}
	}
	d := digest(f)
	f.SyncedAt = time.Now().UTC()
	c.mu.Lock()
	changed := d != c.digest
	c.f, c.digest = f, d
	c.mu.Unlock()
	if !changed || c.Path == "" {
		return nil
	}
	return write(c.Path, f)
}

// digest is the content's hash (its read time aside): an unchanged catalog is not
// written again.
func digest(f file) [32]byte {
	f.SyncedAt = time.Time{}
	b, _ := json.Marshal(f)
	return sha256.Sum256(b)
}

func write(path string, f file) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(filepath.Dir(path), ".catalog-*")
	if err != nil {
		return err
	}
	defer func() { _ = os.Remove(tmp.Name()) }()
	zw := gzip.NewWriter(tmp)
	if err := json.NewEncoder(zw).Encode(f); err != nil {
		_ = tmp.Close()
		return err
	}
	if err := zw.Close(); err != nil {
		_ = tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return os.Rename(tmp.Name(), path)
}

// Run refreshes the catalog now and every SyncEvery while the home's database answers
// (watch), and as soon as it answers again after an outage, until ctx ends.
func (c *Catalog) Run(ctx context.Context, db *sql.DB, nodeID string, watch *Watch) {
	refresh := func() {
		if watch != nil && watch.Down() {
			return
		}
		sctx, cancel := context.WithTimeout(ctx, 2*time.Minute)
		defer cancel()
		start := time.Now()
		if err := c.Sync(sctx, db, nodeID); err != nil {
			if ctx.Err() == nil {
				c.warn("node: this machine's playback catalog was not refreshed", "err", err,
					"event", "node_catalog_sync_failed")
			}
			return
		}
		if c.Logger != nil {
			c.Logger.Debug("node: playback catalog refreshed", "videos", c.Len(),
				"took_ms", time.Since(start).Milliseconds(), "event", "node_catalog_synced")
		}
	}
	refresh()
	t := time.NewTicker(SyncEvery)
	defer t.Stop()
	for {
		var back <-chan struct{} // the database answering again after an outage
		if watch != nil {
			back = watch.backCh()
		}
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		case <-back:
		}
		refresh()
	}
}

func (c *Catalog) warn(msg string, args ...any) {
	if c.Logger != nil {
		c.Logger.Warn(msg, args...)
	}
}
