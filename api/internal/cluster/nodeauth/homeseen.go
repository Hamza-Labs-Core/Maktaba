package nodeauth

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// The copy answers with the home away only while it was current when the home was
// last heard (CopyCurrent), so a node keeps that moment on its disk: one restarted in
// an outage measures the copy against it, not against its own start.

// HomeSeenFile is the file in the node's state directory.
const HomeSeenFile = "home-last-answered"

// keepEvery is how often a newer moment is written.
const keepEvery = 30 * time.Second

// LoadHomeSeen reads when the home last answered (before the node serves): a missing or
// unreadable file is none.
func (r *Resolver) LoadHomeSeen(path string) {
	raw, err := os.ReadFile(path)
	if err != nil {
		if !errors.Is(err, os.ErrNotExist) && r.Logger != nil {
			r.Logger.Warn("node: when the home last answered is unreadable", "path", path, "err", err,
				"event", "node_home_seen_unreadable")
		}
		return
	}
	t, err := time.Parse(time.RFC3339Nano, strings.TrimSpace(string(raw)))
	if err != nil {
		return
	}
	r.mu.Lock()
	if r.lastOK.IsZero() {
		r.lastOK = t
	}
	r.mu.Unlock()
}

// KeepHomeSeen writes when the home last answered to path whenever it moved (at most
// every 30 s, and once more when ctx ends).
func (r *Resolver) KeepHomeSeen(ctx context.Context, path string) {
	t := time.NewTicker(keepEvery)
	defer t.Stop()
	var last time.Time
	save := func() {
		r.mu.Lock()
		seen := r.lastOK
		r.mu.Unlock()
		if seen.IsZero() || seen.Equal(last) {
			return
		}
		if err := writeSeen(path, seen); err != nil {
			if r.Logger != nil {
				r.Logger.Warn("node: when the home last answered was not kept", "err", err, "event", "node_home_seen_failed")
			}
			return
		}
		last = seen
	}
	for {
		select {
		case <-ctx.Done():
			save()
			return
		case <-t.C:
			save()
		}
	}
}

func writeSeen(path string, t time.Time) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, []byte(t.UTC().Format(time.RFC3339Nano)+"\n"), 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}
