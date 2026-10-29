package layout

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"
)

// A release that added no migration leaves Version as it was (release says it runs): no
// fallback marks a dump to put back, and it does not count as migrated.
func TestRecordOfAReleaseWithoutMigrations(t *testing.T) {
	path := filepath.Join(t.TempDir(), "database.json")
	_ = os.WriteFile(path, []byte(`{"version":"1.4.0","release":"1.5.0","state":"migrated","from":"1.4.0"}`), 0o644)
	if ok, err := MarkRecordRestoring(path, "1.5.0", "launcher_pending_timeout"); err != nil || ok {
		t.Fatalf("marked: %v %v", ok, err)
	}
	if RecordMigrated(path, "1.5.0") {
		t.Fatal("1.5.0 counted as having migrated the database")
	}
}

// The backup step notes the dump it took for the next release, keeping what the record
// holds (or making one as the start would read the install).
func TestNoteRecordNext(t *testing.T) {
	path := filepath.Join(t.TempDir(), "state", "database.json")
	at := time.Date(2026, 10, 27, 21, 0, 0, 0, time.UTC)
	if err := NoteRecordNext(path, "1.4.0", "1.5.0", "/b/pre-1.4.0-to-1.5.0.dump", at); err != nil {
		t.Fatal(err)
	}
	rec := readRecord(t, path)
	if rec["version"] != "1.4.0" || rec["state"] != "migrated" || rec["next"] != "1.5.0" ||
		rec["next_dump"] != "/b/pre-1.4.0-to-1.5.0.dump" || rec["next_at"] != "2026-10-27T21:00:00Z" {
		t.Fatalf("made record %v", rec)
	}
	_ = os.WriteFile(path, []byte(`{"version":"1.3.0","release":"1.4.0","state":"migrated","other":"kept"}`), 0o644)
	if err := NoteRecordNext(path, "1.4.0", "1.5.0", "/b/d.dump", at); err != nil {
		t.Fatal(err)
	}
	rec = readRecord(t, path)
	if rec["version"] != "1.3.0" || rec["release"] != "1.4.0" || rec["other"] != "kept" || rec["next_dump"] != "/b/d.dump" {
		t.Fatalf("noted record %v", rec)
	}
}

func readRecord(t *testing.T, path string) map[string]any {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var rec map[string]any
	if err := json.Unmarshal(b, &rec); err != nil {
		t.Fatal(err)
	}
	return rec
}

// A rollback after the migrations marks the record for the dump to be put back; a
// switch back that failed takes the mark back (the new release keeps its database).
// A rollback whose migrations are additive keeps the schema: the record keeps the failed
// release's version (the schema the database holds) and names the older release that may
// start on it; never over migrations cut short. Unmarked when the switch back failed.
func TestRecordKeptMarkAndUnmark(t *testing.T) {
	path := filepath.Join(t.TempDir(), "database.json")
	_ = os.WriteFile(path, []byte(`{"version":"1.5.0","state":"migrating","dump":"/b/d.dump"}`), 0o644)
	if ok, err := MarkRecordKept(path, "1.5.0", "1.4.0", "watch_timeout"); err != nil || ok {
		t.Fatalf("migrations cut short kept: %v %v", ok, err)
	}
	_ = os.WriteFile(path, []byte(`{"version":"1.5.0","state":"migrated","dump":"/b/d.dump","from":"1.4.0"}`), 0o644)
	if ok, err := MarkRecordKept(path, "1.4.0", "1.3.0", "x"); err != nil || ok {
		t.Fatalf("another release's record marked: %v %v", ok, err)
	}
	if ok, err := MarkRecordKept(path, "v1.5.0", "v1.4.0", "watch_timeout"); err != nil || !ok {
		t.Fatalf("mark: %v %v", ok, err)
	}
	rec := readRecord(t, path)
	if rec["version"] != "1.5.0" || rec["state"] != "migrated" || rec["kept_for"] != "1.4.0" ||
		rec["kept_reason"] != "watch_timeout" || rec["dump"] != "/b/d.dump" {
		t.Fatalf("kept record %v", rec)
	}
	if !RecordMigrated(path, "1.5.0") {
		t.Fatal("the kept record no longer says 1.5.0's migrations ran")
	}
	if ok, err := UnmarkRecordKept(path, "1.5.0"); err != nil || !ok {
		t.Fatalf("unmark: %v %v", ok, err)
	}
	if rec := readRecord(t, path); rec["kept_for"] != nil || rec["kept_reason"] != nil || rec["state"] != "migrated" {
		t.Fatalf("unmarked record %v", rec)
	}
}

func TestRecordRestoringMarkAndUnmark(t *testing.T) {
	path := filepath.Join(t.TempDir(), "database.json")
	_ = os.WriteFile(path, []byte(`{"version":"1.5.0","state":"migrated","dump":"/b/pre-1.4.0-to-1.5.0.dump"}`), 0o644)
	if ok, err := MarkRecordRestoring(path, "1.4.0", "x"); err != nil || ok {
		t.Fatalf("another release's record marked: %v %v", ok, err)
	}
	if ok, err := MarkRecordRestoring(path, "1.5.0", "watch"); err != nil || !ok {
		t.Fatalf("mark: %v %v", ok, err)
	}
	if rec := readRecord(t, path); rec["state"] != "restoring" || rec["failed"] != "1.5.0" {
		t.Fatalf("marked record %v", rec)
	}
	if ok, err := UnmarkRecordRestoring(path, "1.4.0"); err != nil || ok {
		t.Fatalf("unmarked for another release: %v %v", ok, err)
	}
	if ok, err := UnmarkRecordRestoring(path, "1.5.0"); err != nil || !ok {
		t.Fatalf("unmark: %v %v", ok, err)
	}
	rec := readRecord(t, path)
	if rec["state"] != "migrated" || rec["failed"] != nil || rec["error"] != nil || rec["dump"] != "/b/pre-1.4.0-to-1.5.0.dump" {
		t.Fatalf("unmarked record %v", rec)
	}
	if ok, err := UnmarkRecordRestoring(filepath.Join(t.TempDir(), "none.json"), "1.5.0"); err != nil || ok {
		t.Fatalf("no record: %v %v", ok, err)
	}
}
