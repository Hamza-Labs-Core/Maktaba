package pgbundle

import (
	"errors"
	"os"
	"path/filepath"
	"slices"
	"testing"
	"time"
)

// A dump's chunks are found by table, in order, and only its own files count as chunks; the
// in-place restore's script loads each with \copy, quoting the path.
func TestChunksOfADump(t *testing.T) {
	dir := t.TempDir()
	d := dirDump(t, dir, "pre-a-to-b.dump", 5, time.Now())
	for _, n := range []string{"chunk.public.segment_embeddings.001.bin", "chunk.public.segment_embeddings.000.bin",
		"chunk.public.other.000.bin", "chunk.bad.bin", "chunk.public.x.y.bin", "3457.dat.gz"} {
		if err := os.WriteFile(filepath.Join(d, n), []byte("x"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	c := Chunks(d)
	if len(c) != 2 || len(c["public.segment_embeddings"]) != 2 || filepath.Base(c["public.segment_embeddings"][0]) !=
		"chunk.public.segment_embeddings.000.bin" || len(c["public.other"]) != 1 {
		t.Fatalf("chunks %v", c)
	}
	if Chunks(filepath.Join(dir, "none.dump")) != nil {
		t.Fatal("chunks of no dump")
	}
	if s := CopyScript(map[string][]string{"public.t": {"/b/it's.bin"}}); s != `\copy public.t FROM '/b/it''s.bin' WITH (FORMAT binary)`+"\n" {
		t.Fatalf("script %q", s)
	}
	if PartialBytes(filepath.Join(dir, "none.dump")) != 0 {
		t.Fatal("PartialBytes of nothing")
	}
}

// dirDump makes a directory-format dump named name in dir: its table of contents and a
// table's file of n bytes, at mod.
func dirDump(t *testing.T, dir, name string, n int, mod time.Time) string {
	t.Helper()
	p := filepath.Join(dir, name)
	if err := os.MkdirAll(p, 0o700); err != nil {
		t.Fatal(err)
	}
	for f, size := range map[string]int{"toc.dat": 1, "3456.dat.gz": n - 1} {
		if err := os.WriteFile(filepath.Join(p, f), make([]byte, size), 0o600); err != nil {
			t.Fatal(err)
		}
		_ = os.Chtimes(filepath.Join(p, f), mod, mod)
	}
	_ = os.Chtimes(p, mod, mod)
	return p
}

// A directory-format dump is a dump of the folder like a file of before: listed with its
// files' size, deleted whole, moved to another disk file by file, counted and removed to
// make room, and pruned with its kind.
func TestDirectoryDumpsFollowTheFolderRules(t *testing.T) {
	from, to := t.TempDir(), filepath.Join(t.TempDir(), "new")
	writeDump(t, from, "maktaba-20261020-030000.dump", 10, time.Now().Add(-2*time.Hour))
	dirDump(t, from, "pre-1.0-to-1.1.dump", 30, time.Now().Add(-time.Hour))
	ds, err := ListDumps(from)
	if err != nil || len(ds) != 2 || ds[0].Name != "pre-1.0-to-1.1.dump" || ds[0].Kind != "update" || ds[0].Bytes != 30 {
		t.Fatalf("%+v %v", ds, err)
	}
	if DumpBytes(filepath.Join(from, "pre-1.0-to-1.1.dump")) != 30 || DumpBytes(filepath.Join(from, "none.dump")) != 0 {
		t.Fatal("DumpBytes")
	}
	// another disk: the folder is copied file by file (no .partial left), then removed
	src := filepath.Join(from, "pre-1.0-to-1.1.dump")
	_ = os.MkdirAll(to+"-copy", 0o700)
	if err := copyDump(src, filepath.Join(to+"-copy", "pre-1.0-to-1.1.dump")); err != nil {
		t.Fatal(err)
	}
	if DumpBytes(filepath.Join(to+"-copy", "pre-1.0-to-1.1.dump")) != 30 || len(left(t, to+"-copy")) != 1 {
		t.Fatalf("the copy: %v", left(t, to+"-copy"))
	}
	if n, err := MoveDumps(from, to); err != nil || n != 2 {
		t.Fatalf("moved %d %v", n, err)
	}
	if err := DeleteDump(to, "pre-1.0-to-1.1.dump"); err != nil {
		t.Fatal(err)
	}
	if got := left(t, to); !slices.Equal(got, []string{"maktaba-20261020-030000.dump"}) {
		t.Fatalf("left %v", got)
	}
}

// MakeRoom counts a directory-format dump by its files, and removes it whole.
func TestMakeRoomWithDirectoryDumps(t *testing.T) {
	dir := t.TempDir()
	now := time.Now()
	dirDump(t, dir, "pre-a-to-b.dump", 20*gib, now.Add(-3*time.Hour))
	dirDump(t, dir, "pre-b-to-c.dump", 20*gib, now.Add(-2*time.Hour))
	dirDump(t, dir, "maktaba-20261025-030000.dump", 20*gib, now.Add(-time.Hour))
	// 1000: reserve 50, the next 25: 75 needed, 60 free: one old dump goes (the newest of
	// each kind stays)
	fakeDisk(t, 60*gib, 1000*gib)
	if err := MakeRoom(dir); err != nil {
		t.Fatal(err)
	}
	if got := left(t, dir); !slices.Equal(got, []string{"maktaba-20261025-030000.dump", "pre-b-to-c.dump"}) {
		t.Fatalf("left %v", got)
	}
	fakeDisk(t, 1*gib, 1000*gib)
	var nr *NoRoomError
	if err := MakeRoom(dir); !errors.As(err, &nr) || nr.Need != 75*gib {
		t.Fatalf("want a NoRoomError needing 75: %v", err)
	}
}

// Pruning keeps the newest of each kind (directory-format dumps too), and removes a dump a
// crash cut short once nothing was written to it for an hour; one being written stays.
func TestPruneBackupsDirectoryDumpsAndPartials(t *testing.T) {
	dir := t.TempDir()
	now := time.Now()
	dirDump(t, dir, "pre-a-to-b.dump", 5, now.Add(-3*time.Hour))
	dirDump(t, dir, "pre-b-to-c.dump", 5, now.Add(-2*time.Hour))
	dirDump(t, dir, "pre-c-to-d.dump.partial", 5, now.Add(-2*time.Hour))
	writing := dirDump(t, dir, "maktaba-20261025-030000.dump.partial", 5, now.Add(-2*time.Hour))
	_ = os.Chtimes(filepath.Join(writing, "3456.dat.gz"), now, now) // a table being written
	if err := PruneBackups(dir, "", 1); err != nil {
		t.Fatal(err)
	}
	if got := left(t, dir); !slices.Equal(got, []string{"maktaba-20261025-030000.dump.partial", "pre-b-to-c.dump"}) {
		t.Fatalf("left %v", got)
	}
}
