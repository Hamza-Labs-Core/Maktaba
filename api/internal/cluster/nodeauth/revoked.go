package nodeauth

import (
	"encoding/json"
	"errors"
	"maps"
	"os"
	"time"
)

// The revocations the home pushes are honoured on the copy's answers until the copy has
// them (Evict, revokedLately). A node keeps them in its state directory for as long as
// they are honoured: one restarted in an outage, when the home cannot push them again,
// still honours them. The file names a session by the SHA-256 of its id and a person by
// their id ("u:"+id), never a credential, with when the revocation reached this machine.

// RevokedFile is the file in the node's state directory.
const RevokedFile = "revoked.json"

// KeepRevoked keeps the revocations the home pushes at path, and takes the ones kept
// there (before the node serves: a missing or unreadable file is none; those past the
// time they are honoured for are let go at the next one).
func (r *Resolver) KeepRevoked(path string) {
	var kept map[string]time.Time
	raw, err := os.ReadFile(path)
	if err == nil {
		err = json.Unmarshal(raw, &kept)
	}
	if err != nil && !errors.Is(err, os.ErrNotExist) && r.Logger != nil {
		r.Logger.Warn("node: the revocations the home pushed are unreadable; starting without them", "path", path,
			"err", err, "event", "node_revoked_unreadable")
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	r.revokedPath = path
	if r.revoked == nil {
		r.revoked = map[string]time.Time{}
	}
	for k, t := range kept {
		if was, ok := r.revoked[k]; !ok || t.After(was) {
			r.revoked[k] = t
		}
	}
}

// saveRevoked writes the revocations to their file (written beside it, flushed, then
// renamed over it: writeFile). The snapshot is taken under rvmu, so the last write has
// the latest.
func (r *Resolver) saveRevoked() {
	r.rvmu.Lock()
	defer r.rvmu.Unlock()
	r.mu.Lock()
	path, snap := r.revokedPath, maps.Clone(r.revoked)
	r.mu.Unlock()
	if path == "" {
		return
	}
	b, err := json.Marshal(snap)
	if err == nil {
		err = writeFile(path, b)
	}
	if err != nil && r.Logger != nil {
		r.Logger.Warn("node: a revocation the home pushed was not kept on the disk", "err", err,
			"event", "node_revoked_not_kept")
	}
}
