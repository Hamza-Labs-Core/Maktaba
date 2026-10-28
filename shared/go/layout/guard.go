package layout

// The launcher's boot-loop guard for the installs that have no release pointers (Epic
// 35, install-kinds.md): the desktop app's bundle and the Linux runtime copy. The update
// orchestrator writes <launcher root>/updates/pending.json before it restarts onto a new
// release; the launcher falls back to the previous release (a kept copy of the app's
// payload, or the runtime directory that ran before) when the new one cannot stay up,
// and marks the database record so the previous release puts back the dump taken
// before the new one's migrations (shared with the layout's pending, Pending.Record).

import (
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"time"
)

// GuardFile is the guard's file in the launcher root's updates directory.
const GuardFile = "pending.json"

// OverrideFile names, in the updates directory, the kept app payload the launcher runs
// in place of the bundle's (a rolled-back app update).
const OverrideFile = "payload-override"

// Guard is a switched-to release not yet confirmed (app bundle, runtime copy).
type Guard struct {
	Version string    `json:"version"`
	RunID   string    `json:"run_id,omitempty"`
	Since   time.Time `json:"since"`
	// Deadline: not confirmed by then, the launcher falls back (zero: its own
	// pending timeout).
	Deadline time.Time `json:"deadline,omitzero"`
	// PreviousPayload is the kept copy of the app payload that ran before (app
	// bundle); PreviousRuntime the runtime directory that did (runtime copy).
	PreviousPayload string `json:"previous_payload,omitempty"`
	PreviousRuntime string `json:"previous_runtime,omitempty"`
	// Record is the database's record (state/database.json) the fallback marks for
	// the dump to be put back (a home's; "" on a node).
	Record string `json:"record,omitempty"`
}

// GuardPath is the guard's file under a launcher root.
func GuardPath(root string) string { return filepath.Join(root, "updates", GuardFile) }

// OverridePath is the payload override's file under a launcher root.
func OverridePath(root string) string { return filepath.Join(root, "updates", OverrideFile) }

// ReadGuard reads the guard (nil when there is none).
func ReadGuard(root string) (*Guard, error) {
	b, err := os.ReadFile(GuardPath(root))
	if errors.Is(err, fs.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var g Guard
	if err := json.Unmarshal(b, &g); err != nil {
		return nil, fmt.Errorf("%s: %w", GuardPath(root), err)
	}
	return &g, nil
}

// WriteGuard writes the guard atomically.
func WriteGuard(root string, g Guard) error {
	b, err := json.MarshalIndent(g, "", "  ")
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(GuardPath(root)), 0o700); err != nil {
		return err
	}
	return WriteFileAtomic(GuardPath(root), append(b, '\n'), 0o600)
}

// ClearGuard removes the guard (the release is confirmed, or it fell back).
func ClearGuard(root string) error {
	err := os.Remove(GuardPath(root))
	if err != nil && !errors.Is(err, fs.ErrNotExist) {
		return err
	}
	return nil
}

// ReadOverride is the kept payload the launcher runs in place of the bundle's ("" none).
func ReadOverride(root string) string {
	b, err := os.ReadFile(OverridePath(root))
	if err != nil {
		return ""
	}
	return string(trimSpace(b))
}

// WriteOverride makes the launcher run payload in place of the bundle's ("" removes it).
func WriteOverride(root, payload string) error {
	if payload == "" {
		err := os.Remove(OverridePath(root))
		if err != nil && !errors.Is(err, fs.ErrNotExist) {
			return err
		}
		return nil
	}
	if err := os.MkdirAll(filepath.Dir(OverridePath(root)), 0o700); err != nil {
		return err
	}
	return WriteFileAtomic(OverridePath(root), []byte(payload+"\n"), 0o600)
}

func trimSpace(b []byte) []byte {
	for len(b) > 0 && (b[len(b)-1] == '\n' || b[len(b)-1] == '\r' || b[len(b)-1] == ' ') {
		b = b[:len(b)-1]
	}
	return b
}

// MarkRecordRestoring marks the database's record (state/database.json, written by the
// server's database step at its start) so that the next start puts back the dump taken
// before failed's migrations: when the record says failed's migrations ran (or were
// running) over a database it dumped first, its state becomes "restoring" with failed
// and reason, and the release that starts next — any release with the database step —
// restores the dump before any role runs. It reports whether it marked the record; one
// that names another release, or no dump, is left alone. Fields it does not know are
// kept.
func MarkRecordRestoring(path, failed, reason string) (bool, error) {
	if path == "" {
		return false, nil
	}
	b, err := os.ReadFile(path)
	if errors.Is(err, fs.ErrNotExist) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	var rec map[string]any
	if err := json.Unmarshal(b, &rec); err != nil {
		return false, fmt.Errorf("%s: %w", path, err)
	}
	str := func(k string) string { s, _ := rec[k].(string); return s }
	if NormalizeVersion(str("version")) != NormalizeVersion(failed) || str("dump") == "" {
		return false, nil
	}
	switch str("state") {
	case "migrated", "migrating":
	case "restoring":
		return true, nil // marked already
	default:
		return false, nil
	}
	rec["state"], rec["failed"], rec["error"] = "restoring", str("version"), reason
	rec["at"] = time.Now().UTC().Format(time.RFC3339Nano)
	out, err := json.MarshalIndent(rec, "", "  ")
	if err != nil {
		return false, err
	}
	return true, WriteFileAtomic(path, append(out, '\n'), 0o644)
}

// ExtendGuard pushes the guard's deadline to until when the guard is runID's and its
// deadline is earlier (the new release is busy starting); it reports whether it did.
func ExtendGuard(root, runID string, until time.Time) (bool, error) {
	g, err := ReadGuard(root)
	if err != nil || g == nil || runID == "" || g.RunID != runID || !g.Deadline.Before(until) {
		return false, err
	}
	g.Deadline = until.UTC()
	return true, WriteGuard(root, *g)
}

// UnmarkRecordRestoring takes back MarkRecordRestoring when the switch back to the
// release before failed: failed (still running) keeps the database its migrations made,
// so the record says migrated again and no start puts the dump back. It reports whether
// it changed the record.
func UnmarkRecordRestoring(path, failed string) (bool, error) {
	if path == "" {
		return false, nil
	}
	b, err := os.ReadFile(path)
	if errors.Is(err, fs.ErrNotExist) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	var rec map[string]any
	if err := json.Unmarshal(b, &rec); err != nil {
		return false, fmt.Errorf("%s: %w", path, err)
	}
	str := func(k string) string { s, _ := rec[k].(string); return s }
	if str("state") != "restoring" || NormalizeVersion(str("failed")) != NormalizeVersion(failed) {
		return false, nil
	}
	rec["state"] = "migrated"
	delete(rec, "failed")
	delete(rec, "error")
	rec["at"] = time.Now().UTC().Format(time.RFC3339Nano)
	out, err := json.MarshalIndent(rec, "", "  ")
	if err != nil {
		return false, err
	}
	return true, WriteFileAtomic(path, append(out, '\n'), 0o644)
}

// NoteRecordNext records in the database's record at path that the running release took
// dump, the pre-update dump for next, while it served (an update's backup step, right
// before its switch): next's start puts it in the record as its own pre-update dump
// instead of taking one. A record not there yet is made as the start would read the
// install (running's migrations, migrated). Fields it does not know are kept.
func NoteRecordNext(path, running, next, dump string, at time.Time) error {
	rec := map[string]any{}
	b, err := os.ReadFile(path)
	switch {
	case errors.Is(err, fs.ErrNotExist):
		rec["version"], rec["state"] = running, "migrated"
	case err != nil:
		return err
	default:
		if err := json.Unmarshal(b, &rec); err != nil {
			return fmt.Errorf("%s: %w", path, err)
		}
	}
	rec["next"], rec["next_dump"], rec["next_at"] = next, dump, at.UTC().Format(time.RFC3339Nano)
	out, err := json.MarshalIndent(rec, "", "  ")
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	return WriteFileAtomic(path, append(out, '\n'), 0o644)
}

// RecordMigrated reports whether the record says version's migrations ran (or were
// running: a database that may hold some of them).
func RecordMigrated(path, version string) bool {
	b, err := os.ReadFile(path)
	if err != nil {
		return false
	}
	var rec struct {
		Version string `json:"version"`
		State   string `json:"state"`
	}
	if json.Unmarshal(b, &rec) != nil {
		return false
	}
	return NormalizeVersion(rec.Version) == NormalizeVersion(version) && (rec.State == "migrated" || rec.State == "migrating")
}
