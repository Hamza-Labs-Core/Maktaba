package pgbundle

import (
	"errors"
	"os"
	"path/filepath"
	"slices"
	"testing"
	"time"
)

// The tests count in KiB (gib is a KiB here) with no floor on the reserve: the dumps
// are real files, and a sparse file of GiBs is not sparse on every disk.
const gib = 1 << 10

// roomDir is a backups folder with a file per name, oldest first (an hour apart).
func roomDir(t *testing.T, names ...string) string {
	t.Helper()
	dir := t.TempDir()
	at := time.Now().Add(-time.Duration(len(names)) * time.Hour)
	for i, n := range names {
		p := filepath.Join(dir, n)
		if err := os.WriteFile(p, []byte{1}, 0o600); err != nil {
			t.Fatal(err)
		}
		mod := at.Add(time.Duration(i) * time.Hour)
		if err := os.Chtimes(p, mod, mod); err != nil {
			t.Fatal(err)
		}
	}
	return dir
}

// fakeDisk replaces diskSpace with a disk of total bytes and free bytes, and takes
// the floor off the reserve.
func fakeDisk(t *testing.T, free, total uint64) {
	t.Helper()
	was, floor := diskSpace, reserveMin
	t.Cleanup(func() { diskSpace, reserveMin = was, floor })
	diskSpace = func(string) (uint64, uint64, error) { return free, total, nil }
	reserveMin = 0
}

func left(t *testing.T, dir string) []string {
	t.Helper()
	ents, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	var out []string
	for _, e := range ents {
		out = append(out, e.Name())
	}
	slices.Sort(out)
	return out
}

// sized makes every file in dir n bytes long, keeping its time.
func sized(t *testing.T, dir string, n int64) {
	t.Helper()
	ents, _ := os.ReadDir(dir)
	for _, e := range ents {
		p := filepath.Join(dir, e.Name())
		info, _ := os.Stat(p)
		if err := os.Truncate(p, n); err != nil {
			t.Fatal(err)
		}
		_ = os.Chtimes(p, info.ModTime(), info.ModTime())
	}
}

func TestMakeRoomLeavesDumpsAloneWhenTheyFit(t *testing.T) {
	dir := roomDir(t, "maktaba-20261024-030000.dump", "maktaba-20261025-030000.dump", "pre-a-to-b.dump")
	sized(t, dir, gib)
	fakeDisk(t, 500*gib, 1000*gib)
	if err := MakeRoom(dir); err != nil {
		t.Fatal(err)
	}
	if got := left(t, dir); len(got) != 3 {
		t.Fatalf("removed dumps with room to spare: %v", got)
	}
}

func TestMakeRoomRemovesTheOldestUntilTheDumpFits(t *testing.T) {
	// 1000 GiB disk: reserve 50 GiB; the next dump ~ 25 GiB (20 x 1.25): 75 GiB needed,
	// 40 free: two old 20 GiB dumps go (40 + 40 = 80), the oldest first
	dir := roomDir(t, "pre-a-to-b.dump", "maktaba-20261023-030000.dump", "maktaba-20261024-030000.dump",
		"pre-b-to-c.dump", "maktaba-20261025-030000.dump")
	sized(t, dir, 20*gib)
	fakeDisk(t, 40*gib, 1000*gib)
	if err := MakeRoom(dir); err != nil {
		t.Fatal(err)
	}
	want := []string{"maktaba-20261024-030000.dump", "maktaba-20261025-030000.dump", "pre-b-to-c.dump"}
	if got := left(t, dir); !slices.Equal(got, want) {
		t.Fatalf("left %v, want %v", got, want)
	}
}

func TestMakeRoomKeepsTheNewestOfEachKindAndRefusesWhenItCannotFit(t *testing.T) {
	dir := roomDir(t, "maktaba-20261024-030000.dump", "pre-a-to-b.dump", "failed-v1-20261025T000000.dump",
		"maktaba-20261025-030000.dump", "pre-b-to-c.dump")
	sized(t, dir, 20*gib)
	fakeDisk(t, 5*gib, 1000*gib)
	err := MakeRoom(dir)
	var nr *NoRoomError
	if !errors.As(err, &nr) {
		t.Fatalf("want a NoRoomError, got %v", err)
	}
	if nr.Free != 45*gib || nr.Need != 75*gib {
		t.Fatalf("free %d need %d (GiB)", nr.Free/gib, nr.Need/gib)
	}
	want := []string{"failed-v1-20261025T000000.dump", "maktaba-20261025-030000.dump", "pre-b-to-c.dump"}
	if got := left(t, dir); !slices.Equal(got, want) {
		t.Fatalf("left %v, want %v", got, want)
	}
}

func TestMakeRoomKeepsTheDumpsWithinTheirShareOfTheDisk(t *testing.T) {
	// plenty free, but 6 x 40 GiB of dumps on a 1000 GiB disk is over its fifth (200)
	dir := roomDir(t, "maktaba-20261020-030000.dump", "maktaba-20261021-030000.dump", "maktaba-20261022-030000.dump",
		"maktaba-20261023-030000.dump", "maktaba-20261024-030000.dump", "maktaba-20261025-030000.dump")
	sized(t, dir, 40*gib)
	fakeDisk(t, 600*gib, 1000*gib)
	if err := MakeRoom(dir); err != nil {
		t.Fatal(err)
	}
	// 3 left (120) + the next (50) = 170 <= 200; 4 left would be 210
	want := []string{"maktaba-20261023-030000.dump", "maktaba-20261024-030000.dump", "maktaba-20261025-030000.dump"}
	if got := left(t, dir); !slices.Equal(got, want) {
		t.Fatalf("left %v, want %v", got, want)
	}
}

func TestMakeRoomLeavesWhatIsNotADumpAlone(t *testing.T) {
	dir := roomDir(t, "maktaba-20261024-030000.dump", "maktaba-20261025-030000.dump", "x.dump.partial", "backups-keep")
	sized(t, dir, 20*gib)
	fakeDisk(t, 1*gib, 1000*gib)
	_ = MakeRoom(dir)
	want := []string{"backups-keep", "maktaba-20261025-030000.dump", "x.dump.partial"}
	if got := left(t, dir); !slices.Equal(got, want) {
		t.Fatalf("left %v, want %v", got, want)
	}
}

func TestReserveIsAShareOfTheDiskWithAFloor(t *testing.T) {
	if got := reserveOf(1000 << 30); got != 50<<30 {
		t.Fatalf("1000 GiB disk: reserve %d GiB", got>>30)
	}
	if got := reserveOf(16 << 30); got != reserveMin || reserveMin != 2<<30 {
		t.Fatalf("16 GiB disk: reserve %d", got)
	}
}
