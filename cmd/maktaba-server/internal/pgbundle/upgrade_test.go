package pgbundle

import (
	"os"
	"path/filepath"
	"sort"
	"testing"
	"time"
)

func TestUpdateDumpName(t *testing.T) {
	for _, c := range []struct{ from, to, want string }{
		{"v0.2.0", "v0.3.0", "pre-v0.2.0-to-v0.3.0.dump"},
		{"", "v0.3.0", "pre-unknown-to-v0.3.0.dump"},
		{"v0.2.0-5-gdead beef", "v0.3.0/x", "pre-v0.2.0-5-gdead_beef-to-v0.3.0_x.dump"},
	} {
		if got := UpdateDumpName(c.from, c.to); got != c.want {
			t.Errorf("UpdateDumpName(%q, %q) = %q, want %q", c.from, c.to, got, c.want)
		}
	}
}

// The newest pre-update dumps stay (by when they were written, not by name);
// the one just written always does; the nightly dumps are not counted or touched.
func TestPruneUpdateDumpsKeepsTheNewest(t *testing.T) {
	b := Bundle{Backups: t.TempDir()}
	write := func(name string, age time.Duration) string {
		p := filepath.Join(b.Backups, name)
		if err := os.WriteFile(p, []byte("d"), 0o600); err != nil {
			t.Fatal(err)
		}
		at := time.Now().Add(-age)
		if err := os.Chtimes(p, at, at); err != nil {
			t.Fatal(err)
		}
		return p
	}
	write("pre-v0.1.0-to-v0.2.0.dump", 4*time.Hour)
	write("pre-v0.9.0-to-v0.10.0.dump", 3*time.Hour) // sorts first by name, written before the others
	write("pre-v0.2.0-to-v0.3.0.dump", 2*time.Hour)
	write("pre-v0.3.0-to-v0.4.0.dump", time.Hour)
	write("maktaba-20261001-033000.dump", 10*time.Hour)
	write("pre-notes.txt", 10*time.Hour)
	// the dump just written, older on the clock than another (a clock set back)
	just := write("pre-v0.4.0-to-v0.5.0.dump", 5*time.Hour)
	if err := b.pruneUpdateDumps(just, 2); err != nil {
		t.Fatal(err)
	}
	entries, _ := os.ReadDir(b.Backups)
	var got []string
	for _, e := range entries {
		got = append(got, e.Name())
	}
	sort.Strings(got)
	want := []string{"maktaba-20261001-033000.dump", "pre-notes.txt", "pre-v0.3.0-to-v0.4.0.dump", "pre-v0.4.0-to-v0.5.0.dump"}
	if len(got) != len(want) {
		t.Fatalf("kept %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("kept %v, want %v", got, want)
		}
	}
	// keep below 1 still keeps the one just written
	if err := b.pruneUpdateDumps(just, 0); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(just); err != nil {
		t.Fatalf("the dump just written went: %v", err)
	}
	if _, err := os.Stat(filepath.Join(b.Backups, "pre-v0.3.0-to-v0.4.0.dump")); !os.IsNotExist(err) {
		t.Fatalf("keep 0 kept another: %v", err)
	}
}
