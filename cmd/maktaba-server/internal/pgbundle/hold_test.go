package pgbundle

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// While the backups are held nothing removes one by itself, whichever rule says so, and no
// scheduled dump is taken; released, the rules apply again. What a crash left goes all the same.
func TestHeldBackupsStay(t *testing.T) {
	dir := t.TempDir()
	old := time.Now().Add(-60 * 24 * time.Hour)
	names := []string{"pre-v1-to-v2.dump", "pre-v2-to-v3.dump", "failed-v2-x.dump", "replaced-v1-x.dump", "replaced-v2-x.dump",
		"maktaba-20260101-030000.dump", "maktaba-20260102-030000.dump"}
	for i, n := range append(names, ".rehearse-x.dump.partial") {
		p := filepath.Join(dir, n)
		if err := os.WriteFile(p, []byte("x"), 0o600); err != nil {
			t.Fatal(err)
		}
		if err := os.Chtimes(p, old.Add(time.Duration(i)*time.Minute), old.Add(time.Duration(i)*time.Minute)); err != nil {
			t.Fatal(err)
		}
	}
	b := Bundle{Backups: dir, UpdateKeep: 1, Nightly: &NightlyPolicy{EveryDays: 1, Hour: 3, Keep: 1}}
	left := func() int {
		n := 0
		ents, _ := os.ReadDir(dir)
		for _, e := range ents {
			if isDump(e) {
				n++
			}
		}
		return n
	}
	prune := func() {
		t.Helper()
		if err := b.Tidy(); err != nil {
			t.Fatal(err)
		}
		prev := diskSpace
		diskSpace = func(string) (uint64, uint64, error) { return 1, 1 << 30, nil }
		defer func() { diskSpace = prev }()
		var no *NoRoomError
		if err := MakeRoomWith(dir, RoomPolicy{MaxShare: 0.0000001, ReserveMin: 1 << 20}); !errors.As(err, &no) {
			t.Fatalf("room made on a full disk: %v", err)
		}
	}
	for range 2 { // holding again is no change
		if err := HoldBackups(dir); err != nil || !BackupsHeld(dir) {
			t.Fatal(err)
		}
	}
	prune()
	if n := left(); n != len(names) {
		t.Fatalf("%d of %d dumps left while held", n, len(names))
	}
	if _, err := os.Stat(filepath.Join(dir, ".rehearse-x.dump.partial")); !os.IsNotExist(err) {
		t.Fatal("a dump cut short stays")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	var log strings.Builder
	b.RunBackups(ctx, 5*time.Millisecond, &log) // due for two months: none is taken
	if log.Len() != 0 || left() != len(names) {
		t.Fatalf("a scheduled dump while held:\n%s", log.String())
	}
	for range 2 {
		if err := ReleaseBackups(dir); err != nil || BackupsHeld(dir) {
			t.Fatal(err)
		}
	}
	prune()
	if n := left(); n >= len(names) {
		t.Fatalf("%d dumps left once released", n)
	}
}

// The room for the copy of the database a restore replaces: made from the older dumps, held or
// not, never the newest of each kind nor those named; a look removes nothing.
func TestRoomKeeping(t *testing.T) {
	dir := t.TempDir()
	old := time.Now().Add(-24 * time.Hour)
	for i, n := range []string{"maktaba-20260101-030000.dump", "maktaba-20260102-030000.dump", "maktaba-20260103-030000.dump",
		"pre-v1-to-v2.dump", "pre-v2-to-v3.dump", "replaced-v1-x.dump"} {
		p := filepath.Join(dir, n)
		if err := os.WriteFile(p, make([]byte, 1000), 0o600); err != nil {
			t.Fatal(err)
		}
		if err := os.Chtimes(p, old.Add(time.Duration(i)*time.Minute), old.Add(time.Duration(i)*time.Minute)); err != nil {
			t.Fatal(err)
		}
	}
	if err := HoldBackups(dir); err != nil {
		t.Fatal(err)
	}
	prev := diskSpace
	t.Cleanup(func() { diskSpace = prev })
	left := func() string {
		var names []string
		ents, _ := os.ReadDir(dir)
		for _, e := range ents {
			if isDump(e) {
				names = append(names, e.Name())
			}
		}
		return strings.Join(names, " ")
	}
	all := left()
	// 500 free, 1250 needed for the copy (the newest dump and a quarter)
	diskSpace = func(string) (uint64, uint64, error) { return 500, 1 << 30, nil }
	p := RoomPolicy{MaxShare: 1}
	if err := RoomKeeping(dir, p, "pre-v1-to-v2.dump"); err != nil || left() != all {
		t.Fatalf("%v, left %s", err, left())
	}
	var no *NoRoomError
	if err := RoomKeeping(dir, p, "pre-v1-to-v2.dump", "maktaba-20260101-030000.dump", "maktaba-20260102-030000.dump"); !errors.As(err, &no) ||
		no.Free != 500 || no.Need != 1250 || left() != all {
		t.Fatalf("%v, left %s", err, left())
	}
	if err := MakeRoomWith(dir, p); !errors.As(err, &no) || left() != all {
		t.Fatalf("held: %v, left %s", err, left())
	}
	// as it is, with nothing removed
	if err := RoomNow(dir, p); !errors.As(err, &no) || no.Free != 500 || no.Need != 1250 || left() != all {
		t.Fatalf("%v, left %s", err, left())
	}
	diskSpace = func(string) (uint64, uint64, error) { return 1250, 1 << 30, nil }
	if err := RoomNow(dir, RoomPolicy{MaxShare: 1e-15}); err != nil || left() != all { // their share is no matter
		t.Fatalf("%v, left %s", err, left())
	}
	diskSpace = func(string) (uint64, uint64, error) { return 500, 1 << 30, nil }
	// a name in another case is the same dump on this disk
	if err := MakeRoomKeeping(dir, p, "Maktaba-20260101-030000.DUMP"); err != nil {
		t.Fatal(err)
	}
	if got, want := left(), strings.Replace(all, "maktaba-20260102-030000.dump ", "", 1); got != want {
		t.Fatalf("left %s\nwant %s", got, want)
	}
}

// KeepForRestore's dumps stay whatever a release that knows nothing of the hold removes: the
// room for a dump, its retention, its nightly pruning (the same code as every release since
// v0.3.82 has, but for the hold); then they are back, with their dates.
func TestKeptForARestore(t *testing.T) {
	dir := t.TempDir()
	at := time.Now().Add(-40 * 24 * time.Hour).Truncate(time.Second)
	names := []string{"maktaba-20260101-030000.dump", "pre-v1-to-v2.dump", "replaced-v1-x.dump", "failed-v1-x.dump"}
	for _, n := range names {
		p := filepath.Join(dir, n)
		if err := os.MkdirAll(filepath.Join(p, "chunks"), 0o700); err != nil {
			t.Fatal(err)
		}
		for _, f := range []string{"toc.dat", "chunks/1"} {
			if err := os.WriteFile(filepath.Join(p, f), []byte(n+f), 0o600); err != nil {
				t.Fatal(err)
			}
		}
		if err := os.Chtimes(p, at, at); err != nil {
			t.Fatal(err)
		}
		// a newer one of each kind
		if err := os.WriteFile(filepath.Join(dir, strings.Replace(strings.Replace(n, "v1", "v9", 1), "20260101", "20260909", 1)), []byte("x"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	file := "pre-v0-to-v1.dump" // one of a single file, as before the directory format
	if err := os.WriteFile(filepath.Join(dir, file), []byte(file), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Chtimes(filepath.Join(dir, file), at, at); err != nil {
		t.Fatal(err)
	}
	for range 2 { // again is no change
		if err := KeepForRestore(dir, append(names, file)...); err != nil {
			t.Fatal(err)
		}
	}
	// what an older release does (nothing is held)
	prev := diskSpace
	diskSpace = func(string) (uint64, uint64, error) { return 1, 1 << 30, nil }
	_ = MakeRoomWith(dir, RoomPolicy{MaxShare: 0.0000001, ReserveMin: 1 << 20})
	diskSpace = prev
	if err := PruneBackups(dir, "", 1); err != nil {
		t.Fatal(err)
	}
	if err := (Bundle{Backups: dir, Nightly: &NightlyPolicy{EveryDays: 1, Hour: 3, Keep: 1}}).prune(); err != nil {
		t.Fatal(err)
	}
	for _, n := range append(names, file) {
		if _, err := os.Stat(filepath.Join(dir, n)); !os.IsNotExist(err) {
			t.Errorf("%s was not removed: this shows nothing (%v)", n, err)
		}
		if _, err := os.Stat(KeptPath(dir, n)); err != nil {
			t.Errorf("%s: %v", n, err)
		}
		if OriginalPath(KeptPath(dir, n)) != filepath.Join(dir, n) || OriginalPath(filepath.Join(dir, n)) != filepath.Join(dir, n) {
			t.Error(OriginalPath(KeptPath(dir, n)))
		}
	}
	if err := os.Mkdir(KeptPath(dir, "half.dump.linking"), 0o700); err != nil { // a link cut short
		t.Fatal(err)
	}
	if err := HoldBackups(dir); err != nil {
		t.Fatal(err)
	}
	if err := ReleaseBackups(dir); err != nil || BackupsHeld(dir) {
		t.Fatal(err)
	}
	for _, n := range append(names, file) {
		info, err := os.Stat(filepath.Join(dir, n))
		if err != nil || !info.ModTime().Equal(at) {
			t.Errorf("%s is not back as it was: %v", n, err)
			continue
		}
		if b, err := os.ReadFile(filepath.Join(dir, n, "chunks", "1")); info.IsDir() && (err != nil || string(b) != n+"chunks/1") {
			t.Errorf("%s: %q %v", n, b, err)
		}
	}
	if left, _ := filepath.Glob(filepath.Join(dir, keptDir, "*")); len(left) != 0 {
		t.Errorf("left %v", left)
	}
	if ents, _ := filepath.Glob(filepath.Join(dir, "*.linking")); len(ents) != 0 {
		t.Errorf("left %v", ents)
	}
}

func TestNewestOfKind(t *testing.T) {
	dir := t.TempDir()
	for i, n := range []string{"pre-a-to-b.dump", "pre-b-to-c.dump", "failed-x.dump"} {
		p := filepath.Join(dir, n)
		at := time.Now().Add(time.Duration(i-9) * time.Hour)
		if err := os.WriteFile(p, nil, 0o600); err != nil {
			t.Fatal(err)
		}
		if err := os.Chtimes(p, at, at); err != nil {
			t.Fatal(err)
		}
	}
	for n, want := range map[string]bool{"pre-a-to-b.dump": false, "pre-b-to-c.dump": true, "failed-x.dump": true, "gone.dump": false} {
		if NewestOfKind(dir, n) != want {
			t.Errorf("%s: want %v", n, want)
		}
	}
	for n, want := range map[string]bool{"pre-a-to-b.dump": true, "maktaba-20260101-030000.dump": false, "replaced-x.dump": false, "failed-x.dump": false} {
		if IsUpdateDump(n) != want {
			t.Errorf("%s: want %v", n, want)
		}
	}
}

// A release stopped in the middle of removing a backup leaves a part of it: the rest comes back,
// with its date, and only then is what is kept removed. One of another content under its name is
// another backup, and stays as it is; a kept one that is only a part takes nothing away.
func TestAHalfRemovedBackupComesBackWhole(t *testing.T) {
	dir := t.TempDir()
	at := time.Now().Add(-time.Hour).Truncate(time.Second)
	const half, other, whole = "pre-v1-to-v2.dump", "pre-v2-to-v3.dump", "pre-v3-to-v4.dump"
	files := []string{"toc.dat", "1.dat", "2.dat"}
	fill := func(n, text string, files ...string) {
		t.Helper()
		if err := os.MkdirAll(filepath.Join(dir, n), 0o700); err != nil {
			t.Fatal(err)
		}
		for _, f := range files {
			if err := os.WriteFile(filepath.Join(dir, n, f), []byte(text+f), 0o600); err != nil {
				t.Fatal(err)
			}
		}
		if err := os.Chtimes(filepath.Join(dir, n), at, at); err != nil {
			t.Fatal(err)
		}
	}
	for _, n := range []string{half, other, whole} {
		fill(n, n, files...)
	}
	if err := KeepForRestore(dir, half, other, whole); err != nil {
		t.Fatal(err)
	}
	for _, p := range []string{filepath.Join(dir, half, "1.dat"), KeptPath(dir, whole) + "/2.dat"} {
		if err := os.Remove(p); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.RemoveAll(filepath.Join(dir, other)); err != nil { // and made again
		t.Fatal(err)
	}
	fill(other, "new", "toc.dat", "1.dat")
	// keeping again takes nothing from what is kept
	if err := KeepForRestore(dir, half, other); err != nil {
		t.Fatal(err)
	}
	if err := ReleaseBackups(dir); err != nil {
		t.Fatal(err)
	}
	for n, want := range map[string][]string{half: files, whole: files, other: {"toc.dat", "1.dat"}} {
		text := n
		if n == other {
			text = "new"
		}
		ents, _ := os.ReadDir(filepath.Join(dir, n))
		if len(ents) != len(want) {
			t.Errorf("%s holds %v", n, ents)
		}
		for _, f := range want {
			if b, err := os.ReadFile(filepath.Join(dir, n, f)); err != nil || string(b) != text+f {
				t.Errorf("%s/%s: %q %v", n, f, b, err)
			}
		}
		if info, err := os.Stat(filepath.Join(dir, n)); err != nil || !info.ModTime().Equal(at) {
			t.Errorf("%s is of %v, not %v", n, info.ModTime(), at)
		}
	}
	if left, _ := filepath.Glob(filepath.Join(dir, keptDir)); len(left) != 0 {
		t.Errorf("left %v", left)
	}
}

// A backup a person deletes by name goes whole: a link of it would hold its room, and bring it
// back.
func TestDeletedWhileKept(t *testing.T) {
	dir := t.TempDir()
	const n = "pre-v1-to-v2.dump"
	if err := os.WriteFile(filepath.Join(dir, n), []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := KeepForRestore(dir, n); err != nil {
		t.Fatal(err)
	}
	if err := DeleteDump(dir, n); err != nil {
		t.Fatal(err)
	}
	if err := ReturnKept(dir); err != nil {
		t.Fatal(err)
	}
	for _, p := range []string{filepath.Join(dir, n), KeptPath(dir, n)} {
		if _, err := os.Lstat(p); !os.IsNotExist(err) {
			t.Errorf("%s is still there (%v)", p, err)
		}
	}
}
