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
	// kept for that release already (the database step kept a failed update's schema): not
	// written again, which would end the failed release's halt during its rollback
	before, _ := os.ReadFile(path)
	if ok, err := MarkRecordKept(path, "1.5.0", "1.4.0", "logs"); err != nil || !ok {
		t.Fatalf("mark again: %v %v", ok, err)
	}
	if after, _ := os.ReadFile(path); string(after) != string(before) {
		t.Fatalf("a record kept for the same release was written again:\n%s\n%s", before, after)
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

// A failed update stops and asks (no start puts the dump back by itself): the record is
// held, its version stays the failed release's so every older release refuses it, and a
// switch back that fails leaves it held (its Down scripts may have run in part).
func TestRecordHeldMarkAndUnmark(t *testing.T) {
	path := filepath.Join(t.TempDir(), "database.json")
	_ = os.WriteFile(path, []byte(`{"version":"1.5.0","state":"migrating","from":"1.4.0","dump":"/b/pre-1.4.0-to-1.5.0.dump","schema_before":230}`), 0o644)
	if ok, err := MarkRecordHeld(path, "1.4.0", "x"); err != nil || ok {
		t.Fatalf("another release's record marked: %v %v", ok, err)
	}
	if ok, err := MarkRecordHeld(path, "1.5.0", "launcher_pending_timeout"); err != nil || !ok {
		t.Fatalf("mark: %v %v", ok, err)
	}
	rec := readRecord(t, path)
	if rec["state"] != "held" || rec["failed"] != "1.5.0" || rec["version"] != "1.5.0" ||
		rec["error"] != "launcher_pending_timeout" || rec["schema_before"] != float64(230) {
		t.Fatalf("held record %v", rec)
	}
	// marked already: a restore mark over it is not written (nothing puts it back by itself)
	if ok, err := MarkRecordRestoring(path, "1.5.0", "watch"); err != nil || !ok {
		t.Fatalf("mark again: %v %v", ok, err)
	}
	if rec := readRecord(t, path); rec["state"] != "held" {
		t.Fatalf("a held record was marked restoring: %v", rec)
	}
	if ok, err := UnmarkRecordRestoring(path, "1.5.0"); err != nil || ok {
		t.Fatalf("a held record unmarked: %v %v", ok, err)
	}
	if rec := readRecord(t, path); rec["state"] != "held" || rec["failed"] != "1.5.0" {
		t.Fatalf("after a switch back that failed: %v", rec)
	}
	// the other way round: an older launcher marked it restoring first, with no person
	// asking; the new release's watch holds it. A person's restore is left as it is
	for by, want := range map[string]string{"": "held", "admin": "restoring"} {
		_ = os.WriteFile(path, []byte(`{"version":"1.5.0","state":"restoring","from":"1.4.0","failed":"1.5.0","by":"`+by+
			`","dump":"/b/pre-1.4.0-to-1.5.0.dump"}`), 0o644)
		if ok, err := MarkRecordHeld(path, "1.5.0", "watch_timeout"); err != nil || !ok {
			t.Fatalf("held over restoring (by %q): %v %v", by, ok, err)
		}
		if rec := readRecord(t, path); rec["state"] != want || rec["dump"] != "/b/pre-1.4.0-to-1.5.0.dump" {
			t.Fatalf("held over restoring (by %q): %v, want %s", by, rec, want)
		}
	}
	// no dump: nothing to hold for (the update added no migration)
	_ = os.WriteFile(path, []byte(`{"version":"1.5.0","state":"migrated"}`), 0o644)
	if ok, err := MarkRecordHeld(path, "1.5.0", "x"); err != nil || ok {
		t.Fatalf("held without a dump: %v %v", ok, err)
	}
}

// An update's record not confirmed yet withholds its backup from older code: a guard of any
// release before 2026-11-24 leaves it alone (no dump), a release since holds it, with the
// backup named where every release reads it; its confirmation names it too.
func TestUnconfirmedRecordIsLeftAloneByOlderCode(t *testing.T) {
	path := filepath.Join(t.TempDir(), "database.json")
	const unconfirmed = `{"version":"1.5.0","state":"migrated","from":"1.4.0","dump_unconfirmed":"/b/d.dump","schema_before":230}`
	write := func(s string) { _ = os.WriteFile(path, []byte(s), 0o644) }
	write(unconfirmed)
	if ok, err := MarkRecordRestoring(path, "1.5.0", "launcher_boot_loop"); err != nil || ok {
		t.Fatalf("older code marked it restoring: %v %v", ok, err)
	}
	if b, _ := os.ReadFile(path); string(b) != unconfirmed {
		t.Fatalf("older code changed it: %s", b)
	}
	if ok, err := MarkRecordHeld(path, "1.5.0", "watch_timeout"); err != nil || !ok {
		t.Fatalf("held: %v %v", ok, err)
	}
	if rec := readRecord(t, path); rec["state"] != "held" || rec["dump"] != "/b/d.dump" || rec["dump_unconfirmed"] != nil {
		t.Fatalf("held %v", rec)
	}
	if ok, err := ConfirmRecord(path, "1.5.0"); err != nil || ok {
		t.Fatalf("a held record confirmed: %v %v", ok, err)
	}
	write(unconfirmed)
	if ok, err := ConfirmRecord(path, "1.4.0"); err != nil || ok {
		t.Fatalf("another release's record confirmed: %v %v", ok, err)
	}
	if ok, err := ConfirmRecord(path, "v1.5.0"); err != nil || !ok {
		t.Fatalf("confirm: %v %v", ok, err)
	}
	if rec := readRecord(t, path); rec["dump"] != "/b/d.dump" || rec["confirmed"] != true || rec["dump_unconfirmed"] != nil ||
		rec["schema_before"] != float64(230) {
		t.Fatalf("confirmed %v", rec)
	}
	if ok, _ := ConfirmRecord(path, "1.5.0"); ok {
		t.Fatal("confirmed twice")
	}
	if ok, err := ConfirmRecord(filepath.Join(t.TempDir(), "none.json"), "1.5.0"); ok || err != nil {
		t.Fatalf("no record: %v %v", ok, err)
	}
}

// A rollback whose Down scripts undid the release's migrations leaves the record the
// release before's own, migrated, with nothing to put back and nothing kept; a record that
// is not the failed release's, migrated, stays as it is.
func TestMarkRecordDown(t *testing.T) {
	path := filepath.Join(t.TempDir(), "database.json")
	write := func(s string) {
		if err := os.WriteFile(path, []byte(s), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	write(`{"version":"v0.3.90","state":"migrated","from":"v0.3.82","release":"v0.3.90",` +
		`"dump":"/b/pre-v0.3.82-to-v0.3.90.dump","kept_for":"v0.3.80","at":"2026-11-07T03:51:00Z"}`)
	ok, err := MarkRecordDown(path, "0.3.90", "0.3.82", "watch_timeout")
	if err != nil || !ok {
		t.Fatalf("ok %v, err %v", ok, err)
	}
	b, _ := os.ReadFile(path)
	var rec map[string]any
	if err := json.Unmarshal(b, &rec); err != nil {
		t.Fatal(err)
	}
	if rec["version"] != "v0.3.82" || rec["state"] != "migrated" || rec["down_from"] != "v0.3.90" ||
		rec["down_reason"] != "watch_timeout" {
		t.Fatalf("record: %s", b)
	}
	for _, k := range []string{"release", "kept_for"} {
		if _, there := rec[k]; there {
			t.Fatalf("%s left in the record: %s", k, b)
		}
	}
	if !RecordMigrated(path, "0.3.82") {
		t.Fatal("the release before's record is not migrated")
	}
	// a newer schema kept for the release before, before the update: kept for it again
	write(`{"version":"v0.3.90","state":"migrated","from":"v0.3.82","dump_kept_for":"0.3.82","schema_before":183}`)
	if slot, kept, err := RecordSchemaBefore(path, "0.3.90"); err != nil || slot != 183 || !kept {
		t.Fatalf("before 0.3.90: %d %v %v", slot, kept, err)
	}
	if slot, kept, err := RecordSchemaBefore(path, "0.3.89"); err != nil || slot != 0 || kept {
		t.Fatalf("another release's record: %d %v %v", slot, kept, err)
	}
	if ok, err := MarkRecordDown(path, "0.3.90", "0.3.82", "watch_timeout"); !ok || err != nil {
		t.Fatalf("ok %v, err %v", ok, err)
	}
	b, _ = os.ReadFile(path)
	rec = nil
	if err := json.Unmarshal(b, &rec); err != nil {
		t.Fatal(err)
	}
	if rec["kept_for"] != "0.3.82" || rec["kept_reason"] == nil || rec["dump_kept_for"] != nil || rec["schema_before"] != nil {
		t.Fatalf("record: %s", b)
	}
	if _, _, err := RecordSchemaBefore(filepath.Join(t.TempDir(), "none.json"), "0.3.90"); err != nil {
		t.Fatalf("no record: %v", err)
	}
	write(`{`)
	if _, _, err := RecordSchemaBefore(path, "0.3.90"); err == nil {
		t.Fatal("a record that cannot be read says nothing was kept")
	}
	write(`{"version":"v0.3.90","state":"migrated","kept_for":"0.3.82"}`)
	if slot, kept, _ := RecordSchemaBefore(path, "0.3.90"); slot != 0 || !kept {
		t.Fatalf("a kept schema 0.3.90 ran no migration on: %d %v", slot, kept)
	}
	write(`{"version":"v0.3.82","state":"migrated"}`)
	if ok, _ := MarkRecordDown(path, "0.3.90", "0.3.82", "again"); ok {
		t.Fatal("a record that is no longer the failed release's changed")
	}
	write(`{"version":"v0.3.90","state":"restoring"}`)
	if ok, _ := MarkRecordDown(path, "0.3.90", "0.3.82", "x"); ok {
		t.Fatal("a restoring record changed")
	}
}

// Research P5: the release being judged may push a guard's deadline, but never past the
// latest the release that switched set, nor past MaxGuardSpan after the switch.
func TestGuardDeadlineIsCappedAtItsLatest(t *testing.T) {
	root := t.TempDir()
	now := time.Now().UTC()
	latest := now.Add(3 * time.Hour)
	if err := WriteGuard(root, Guard{Version: "2.0.0", RunID: "run-1", Since: now, Deadline: now.Add(time.Hour),
		Latest: latest}); err != nil {
		t.Fatal(err)
	}
	if ok, err := ExtendGuard(root, "run-1", now.Add(2*time.Hour)); !ok || err != nil {
		t.Fatalf("within the latest: %v %v", ok, err)
	}
	if ok, err := ExtendGuard(root, "run-1", now.Add(50*time.Hour)); !ok || err != nil {
		t.Fatalf("past the latest: %v %v", ok, err)
	}
	if g, _ := ReadGuard(root); !g.Deadline.Equal(latest) {
		t.Fatalf("deadline %v, want the latest %v", g.Deadline, latest)
	}
	if ok, _ := ExtendGuard(root, "run-1", now.Add(60*time.Hour)); ok {
		t.Fatal("pushed past the latest again")
	}
	if ok, _ := ExtendGuard(root, "run-2", now.Add(2*time.Hour)); ok {
		t.Fatal("another run pushed it")
	}

	// the pending marker of a layout install: the same cap, and a day without a latest
	l := New(t.TempDir())
	mkRelease(t, l, "2.0.0")
	if err := l.Switch("2.0.0", "run-1", now); err != nil {
		t.Fatal(err)
	}
	if err := l.GuardPending("2.0.0", now.Add(time.Hour), time.Time{}, ""); err != nil {
		t.Fatal(err)
	}
	if ok, err := l.ExtendPending("run-1", now.Add(30*24*time.Hour)); !ok || err != nil {
		t.Fatalf("push: %v %v", ok, err)
	}
	p, _ := l.ReadPending()
	if want := GuardLatest(p.Since, time.Time{}); !p.Deadline.Equal(want) || want.Sub(p.Since) != MaxGuardSpan {
		t.Fatalf("deadline %v, want %v (a day after the switch)", p.Deadline, want)
	}
}
