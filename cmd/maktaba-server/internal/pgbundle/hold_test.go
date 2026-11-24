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
