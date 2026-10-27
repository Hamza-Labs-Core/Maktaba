package pgbundle

import (
	"errors"
	"os"
	"path/filepath"
	"slices"
	"testing"
	"time"
)

func TestDueEveryFollowsTheHourAndTheDays(t *testing.T) {
	loc := time.Local
	at := func(d, h int) time.Time { return time.Date(2026, 10, d, h, 30, 0, 0, loc) }
	weekly := NightlyPolicy{EveryDays: 7, Hour: 22, Keep: 4}
	daily1am := NightlyPolicy{EveryDays: 1, Hour: 1, Keep: 7}
	for _, c := range []struct {
		name      string
		p         NightlyPolicy
		now, last time.Time
		want      bool
	}{
		{"never taken", weekly, at(10, 9), time.Time{}, true},
		{"weekly, six days after", weekly, at(16, 23), at(10, 22), false},
		{"weekly, seven days after, before the hour", weekly, at(17, 21), at(10, 22), false},
		{"weekly, seven days after, past the hour", weekly, at(17, 22), at(10, 22), true},
		{"daily at 1, taken at 1 today", daily1am, at(10, 9), at(10, 1), false},
		{"daily at 1, taken yesterday", daily1am, at(10, 9), at(9, 1), true},
		{"daily at 1, before the hour", daily1am, at(10, 0), at(9, 1), false},
	} {
		if got := dueEvery(c.now, c.last, c.p); got != c.want {
			t.Errorf("%s: due = %v", c.name, got)
		}
	}
	// the default is the behaviour from before the settings
	if !due(at(10, 4), at(9, 3)) || due(at(10, 4), at(10, 3)) {
		t.Error("the default schedule changed")
	}
}

// Moving or deleting the dumps does not make the next scheduled one due early: the
// time of the last is recorded apart.
func TestLastBackupIsRecordedApartFromTheDumps(t *testing.T) {
	dir := t.TempDir()
	b := Bundle{Backups: filepath.Join(dir, "backups"), LastNightly: filepath.Join(dir, "state", "last-nightly-backup")}
	if !b.lastBackup().IsZero() {
		t.Fatal("a time with nothing taken")
	}
	when := time.Date(2026, 10, 26, 3, 5, 0, 0, time.Local)
	b.recordNightly(when)
	if got := b.lastBackup(); !got.Equal(when) {
		t.Fatalf("recorded %v, read %v", when, got)
	}
	// a newer dump in the folder wins (taken by an older version, say)
	writeDump(t, b.Backups, "maktaba-20261027-031000.dump", 10, time.Now())
	if got := b.lastBackup(); got.Day() != 27 {
		t.Errorf("last %v", got)
	}
}

func TestPruneKeepsThePolicysCount(t *testing.T) {
	dir := t.TempDir()
	for _, d := range []string{"20261020", "20261021", "20261022", "20261023"} {
		writeDump(t, dir, "maktaba-"+d+"-030000.dump", 10, time.Now())
	}
	b := Bundle{Backups: dir, Nightly: &NightlyPolicy{EveryDays: 1, Hour: 3, Keep: 2}}
	if err := b.prune(); err != nil {
		t.Fatal(err)
	}
	if got := left(t, dir); !slices.Equal(got, []string{"maktaba-20261022-030000.dump", "maktaba-20261023-030000.dump"}) {
		t.Errorf("left %v", got)
	}
}

func TestMakeRoomWithAPolicy(t *testing.T) {
	was := diskSpace
	t.Cleanup(func() { diskSpace = was })
	dir := t.TempDir()
	writeDump(t, dir, "maktaba-20261020-030000.dump", 100, time.Now().Add(-3*time.Hour))
	writeDump(t, dir, "maktaba-20261021-030000.dump", 100, time.Now().Add(-2*time.Hour))
	writeDump(t, dir, "maktaba-20261022-030000.dump", 100, time.Now().Add(-time.Hour))
	diskSpace = func(string) (uint64, uint64, error) { return 10_000, 10_000, nil }
	// the dumps may take 2% of the disk (200 bytes): the oldest go, the newest stays
	if err := MakeRoomWith(dir, RoomPolicy{MaxShare: 0.02}); err != nil {
		t.Fatal(err)
	}
	if got := left(t, dir); !slices.Equal(got, []string{"maktaba-20261022-030000.dump"}) {
		t.Errorf("left %v", got)
	}
	// a reserve of 99%: no dump fits
	var nr *NoRoomError
	if err := MakeRoomWith(dir, RoomPolicy{ReserveShare: 0.99, MaxShare: 1}); !errors.As(err, &nr) {
		t.Errorf("err %v", err)
	}
}

func TestListDeleteAndMoveDumps(t *testing.T) {
	from, to := t.TempDir(), filepath.Join(t.TempDir(), "new")
	writeDump(t, from, "maktaba-20261020-030000.dump", 10, time.Now().Add(-time.Hour))
	writeDump(t, from, "pre-1.0-to-1.1.dump", 20, time.Now())
	if err := SetBackupsKeep(from, 4); err != nil {
		t.Fatal(err)
	}
	ds, err := ListDumps(from)
	if err != nil || len(ds) != 2 || ds[0].Kind != "update" || ds[1].Kind != "nightly" || ds[0].Bytes != 20 {
		t.Fatalf("%+v %v", ds, err)
	}
	for _, bad := range []string{"../x.dump", "backups-keep", "", "a/b.dump", "nope.dump"} {
		if err := DeleteDump(from, bad); err == nil {
			t.Errorf("deleted %q", bad)
		}
	}
	n, err := MoveDumps(from, to)
	if err != nil || n != 2 {
		t.Fatalf("moved %d %v", n, err)
	}
	if got := left(t, to); !slices.Equal(got, []string{"backups-keep", "maktaba-20261020-030000.dump", "pre-1.0-to-1.1.dump"}) {
		t.Errorf("moved %v", got)
	}
	if BackupsKeep(to, 3) != 4 {
		t.Error("the record of the pre-update dumps to keep did not move")
	}
	if err := DeleteDump(to, "pre-1.0-to-1.1.dump"); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(to, "pre-1.0-to-1.1.dump")); err == nil {
		t.Error("not deleted")
	}
}

func TestRunBackupsOffTakesNone(t *testing.T) {
	b := Bundle{Backups: t.TempDir(), Nightly: &NightlyPolicy{Off: true}}
	done := make(chan struct{})
	go func() { b.RunBackups(t.Context(), time.Millisecond, &discard{}); close(done) }()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("RunBackups ran with the scheduled backups off")
	}
}

// writeDump makes a dump of n bytes in dir, last changed at mod.
func writeDump(t *testing.T, dir, name string, n int, mod time.Time) {
	t.Helper()
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	p := filepath.Join(dir, name)
	if err := os.WriteFile(p, make([]byte, n), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Chtimes(p, mod, mod); err != nil {
		t.Fatal(err)
	}
}

type discard struct{}

func (*discard) Write(p []byte) (int, error) { return len(p), nil }
