package pgbundle

import (
	"os"
	"path/filepath"
	"slices"
	"testing"
	"time"
)

// A dump of a failed update or of a replaced database goes once a month old, even the
// only one of its kind (a lone 33 GB failed dump stayed for weeks); the oldest pre-update
// dump never expires by age, only by count.
func TestPruneBackupsExpiresOldFailedAndReplacedDumps(t *testing.T) {
	dir := t.TempDir()
	month := expireAfter + time.Hour
	writeDump(t, dir, "failed-v0.3.36-20261027T214413.dump", 10, time.Now().Add(-month))
	writeDump(t, dir, "failed-v0.3.90-20261107T150000.dump", 10, time.Now().Add(-time.Hour))
	writeDump(t, dir, "replaced-v0.3.10-20261001T000000.dump", 10, time.Now().Add(-month))
	writeDump(t, dir, "pre-0.3.10-to-0.3.11.dump", 10, time.Now().Add(-3*month))
	if err := PruneBackups(dir, "", 3); err != nil {
		t.Fatal(err)
	}
	want := []string{"failed-v0.3.90-20261107T150000.dump", "pre-0.3.10-to-0.3.11.dump"}
	if got := left(t, dir); !slices.Equal(got, want) {
		t.Errorf("left %v, want %v", got, want)
	}
}

// The dump an update names (keepPath) stays whatever its age.
func TestPruneBackupsNeverExpiresTheKeptPath(t *testing.T) {
	dir := t.TempDir()
	writeDump(t, dir, "failed-v0.3.36-20261027T214413.dump", 10, time.Now().Add(-expireAfter-time.Hour))
	if err := PruneBackups(dir, filepath.Join(dir, "failed-v0.3.36-20261027T214413.dump"), 3); err != nil {
		t.Fatal(err)
	}
	if got := left(t, dir); len(got) != 1 {
		t.Errorf("left %v", got)
	}
}

// Tidy holds every kind to its rule at once: nightly dumps to [backups] keep, pre-update
// dumps to UpdateKeep (or what the last update recorded), and leaves what no rule names.
func TestTidyKeepsEveryKindToItsRule(t *testing.T) {
	dir := t.TempDir()
	for i, d := range []string{"20261101", "20261102", "20261103", "20261104"} {
		writeDump(t, dir, "maktaba-"+d+"-030000.dump", 10, time.Now().Add(time.Duration(i-4)*time.Hour))
	}
	for i, v := range []string{"0.3.73-to-0.3.78", "0.3.78-to-0.3.81", "0.3.82-to-0.3.90"} {
		writeDump(t, dir, "pre-"+v+".dump", 10, time.Now().Add(time.Duration(i-3)*time.Hour))
	}
	writeDump(t, dir, "from-dev-20261023-002241.dump", 10, time.Now().Add(-48*time.Hour))
	b := Bundle{Backups: dir, Nightly: &NightlyPolicy{EveryDays: 1, Hour: 3, Keep: 2}, UpdateKeep: 1}
	if err := b.Tidy(); err != nil {
		t.Fatal(err)
	}
	want := []string{"from-dev-20261023-002241.dump", "maktaba-20261103-030000.dump",
		"maktaba-20261104-030000.dump", "pre-0.3.82-to-0.3.90.dump"}
	if got := left(t, dir); !slices.Equal(got, want) {
		t.Errorf("left %v, want %v", got, want)
	}
	// what the last update recorded wins over the server's own count
	writeDump(t, dir, "pre-0.3.90-to-0.3.91.dump", 10, time.Now())
	if err := SetBackupsKeep(dir, 2); err != nil {
		t.Fatal(err)
	}
	if err := b.Tidy(); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(dir, "pre-0.3.82-to-0.3.90.dump")); err != nil {
		t.Errorf("the recorded keep of 2 was not followed: %v", err)
	}
}

// A backups folder not made yet is nothing to prune.
func TestTidyWithoutAFolder(t *testing.T) {
	b := Bundle{Backups: filepath.Join(t.TempDir(), "none")}
	if err := b.Tidy(); err != nil {
		t.Fatal(err)
	}
}
