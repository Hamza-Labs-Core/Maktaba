package probe

import (
	"bytes"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/google/uuid"
)

// A full node's own copy of the probe rows (PR #145): every probed video's row as
// lookupSQL reads it, and this machine's path map, kept in its state directory
// (MAKTABA_NODE_STATE_DIR) and refreshed while the home's database answers, so the node
// plays the videos it can read with that database down, even after a restart in the
// outage. Paths and media facts only: who may play is the API's call (it checks the
// video's library against the person's sign-in before it opens a session here).

// SnapshotFile is the snapshot's file in the node's state directory.
const SnapshotFile = "streaming-catalog.json.gz"

// SnapshotEvery is how often the snapshot is refreshed while the database answers.
const SnapshotEvery = 15 * time.Minute

// ErrUnavailable: the database does not answer and the snapshot has no such video.
var ErrUnavailable = errors.New("probe: the home's database does not answer and this machine has no record of the video")

const snapshotVersion = 1

type snapshotFile struct {
	Version  int               `json:"version"`
	SyncedAt time.Time         `json:"synced_at"`
	PathMap  map[string]string `json:"path_map,omitempty"`
	Rows     []Row             `json:"rows"`
}

// Snapshot is the node's copy, in memory and in its file.
type Snapshot struct {
	// Path is the file ("": memory only).
	Path   string
	Logger *slog.Logger

	mu      sync.RWMutex
	byID    map[uuid.UUID]Row
	pathMap map[string]string
	at      time.Time
	digest  [32]byte
}

// OpenSnapshot loads the snapshot's file; a missing or unreadable one is an empty
// snapshot (the latter logged), which the next refresh replaces.
func OpenSnapshot(path string, logger *slog.Logger) *Snapshot {
	s := &Snapshot{Path: path, Logger: logger, byID: map[uuid.UUID]Row{}}
	if path == "" {
		return s
	}
	f, err := readSnapshot(path)
	switch {
	case errors.Is(err, os.ErrNotExist):
	case err != nil:
		if logger != nil {
			logger.Warn("probe: this machine's playback snapshot is unreadable; the next refresh replaces it",
				"path", path, "err", err.Error(), "event", "probe_snapshot_unreadable")
		}
	default:
		s.set(f)
	}
	return s
}

func readSnapshot(path string) (snapshotFile, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return snapshotFile{}, err
	}
	zr, err := gzip.NewReader(bytes.NewReader(raw))
	if err != nil {
		return snapshotFile{}, err
	}
	var f snapshotFile
	if err := json.NewDecoder(zr).Decode(&f); err != nil {
		return snapshotFile{}, err
	}
	if f.Version != snapshotVersion {
		return snapshotFile{}, fmt.Errorf("snapshot version %d, this code reads %d", f.Version, snapshotVersion)
	}
	return f, nil
}

func (s *Snapshot) set(f snapshotFile) {
	by := make(map[uuid.UUID]Row, len(f.Rows))
	for _, r := range f.Rows {
		by[r.VideoID] = r
	}
	s.mu.Lock()
	s.byID, s.pathMap, s.at, s.digest = by, f.PathMap, f.SyncedAt, snapshotDigest(f)
	s.mu.Unlock()
}

// Lookup is a video's row from the snapshot.
func (s *Snapshot) Lookup(id uuid.UUID) (*Row, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	r, ok := s.byID[id]
	if !ok {
		return nil, false
	}
	return &r, true
}

// PathMap is this machine's path map as last read ({db prefix: local prefix}).
func (s *Snapshot) PathMap() map[string]string {
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := make(map[string]string, len(s.pathMap))
	for k, v := range s.pathMap {
		out[k] = v
	}
	return out
}

// Len is the number of rows.
func (s *Snapshot) Len() int {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return len(s.byID)
}

// Replace takes freshly read rows and path map, writing the file when they changed.
func (s *Snapshot) Replace(rows []Row, pathMap map[string]string) error {
	f := snapshotFile{Version: snapshotVersion, PathMap: pathMap, Rows: rows}
	d := snapshotDigest(f)
	f.SyncedAt = time.Now().UTC()
	s.mu.RLock()
	same := d == s.digest
	s.mu.RUnlock()
	s.set(f)
	if same || s.Path == "" {
		return nil
	}
	return writeSnapshot(s.Path, f)
}

func snapshotDigest(f snapshotFile) [32]byte {
	f.SyncedAt = time.Time{}
	b, _ := json.Marshal(f)
	return sha256.Sum256(b)
}

func writeSnapshot(path string, f snapshotFile) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(filepath.Dir(path), ".snapshot-*")
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

// SnapshotSource reads what the snapshot keeps from the home's database.
type SnapshotSource interface {
	AllRows(ctx context.Context) ([]Row, error)
	PathMap(ctx context.Context) (map[string]string, error)
}

// Refresh reads the source once; a failed read leaves the snapshot as it was.
func (s *Snapshot) Refresh(ctx context.Context, src SnapshotSource) error {
	rows, err := src.AllRows(ctx)
	if err != nil {
		return err
	}
	pm, err := src.PathMap(ctx)
	if err != nil {
		return fmt.Errorf("path map: %w", err)
	}
	return s.Replace(rows, pm)
}

// Offline is the Backend of a full node (PR #145): the primary (the home's database)
// while it answers, else the snapshot. An answer — no such video, not probed — is the
// primary's; only a failure (the database gone, a timeout) falls back.
type Offline struct {
	Primary Backend
	Snap    *Snapshot
	// Down: the database does not answer (skip it rather than wait); Failed records a
	// lookup that failed on it.
	Down    func() bool
	Failed  func(error)
	Timeout time.Duration // a primary lookup's bound (0: 3 s)
}

// Lookup implements Backend.
func (o *Offline) Lookup(ctx context.Context, id uuid.UUID) (*Row, error) {
	if o.Down == nil || !o.Down() {
		timeout := o.Timeout
		if timeout <= 0 {
			timeout = 3 * time.Second
		}
		qctx, cancel := context.WithTimeout(ctx, timeout)
		row, err := o.Primary.Lookup(qctx, id)
		cancel()
		if err == nil || errors.Is(err, ErrNotFound) || errors.Is(err, ErrNotProbed) || ctx.Err() != nil {
			return row, err
		}
		if o.Failed != nil {
			o.Failed(err)
		}
	}
	if row, ok := o.Snap.Lookup(id); ok {
		return row, nil
	}
	return nil, ErrUnavailable
}
