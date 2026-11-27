package pgbundle

import (
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"testing"
	"time"

	"github.com/klauspost/compress/zstd"
)

const archiveTestCopy = "PGCOPY\n\xff\r\n\x00\x00\x00\x00\x00\x00\x00\x00\x00\xff\xff"

func TestScheduledRetentionTiers(t *testing.T) {
	stamps := []string{"20250102", "20250101", "20241231", "20241230", "20240229", "20240228", "20231231", "20221231"}
	dumps := make([]scheduledDump, 0, len(stamps))
	for _, stamp := range stamps {
		name := "maktaba-" + stamp + "-030000.dump"
		dumps = append(dumps, scheduledDump{name: name, at: scheduledAt(name, time.Now())})
	}
	for _, tc := range []struct {
		monthly, yearly int
		keep            []int
	}{
		{0, 0, []int{0, 1}}, {3, 0, []int{0, 1, 2, 4}}, {0, 3, []int{0, 1, 2, 6}}, {3, 3, []int{0, 1, 2, 4, 6}},
	} {
		want := map[string]bool{}
		for _, i := range tc.keep {
			want[dumps[i].name] = true
		}
		if got := selectScheduledToKeep(dumps, NightlyPolicy{Keep: 2, KeepMonthly: tc.monthly, KeepYearly: tc.yearly}); !reflect.DeepEqual(got, want) {
			t.Fatalf("monthly %d yearly %d: kept %v, want %v", tc.monthly, tc.yearly, got, want)
		}
	}
}

func TestArchiveMaintenanceResumesAndVerifies(t *testing.T) {
	root := t.TempDir()
	now := time.Date(2026, 11, 26, 3, 0, 0, 0, time.Local)
	dir := filepath.Join(root, "maktaba-20260228-030000.dump")
	recent := filepath.Join(root, "maktaba-20261125-030000.dump")
	for _, p := range []string{dir, recent} {
		if err := os.Mkdir(p, 0o700); err != nil {
			t.Fatal(err)
		}
	}
	write := func(path string, data []byte) {
		t.Helper()
		if err := os.WriteFile(path, data, 0o600); err != nil {
			t.Fatal(err)
		}
	}
	encoder, err := zstd.NewWriter(nil, zstd.WithEncoderLevel(zstd.SpeedFastest))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = encoder.Close() }()
	compressed := encoder.EncodeAll([]byte(archiveTestCopy), nil)
	first := filepath.Join(dir, "chunk.public.books.0.bin")
	second := filepath.Join(dir, "chunk.public.books.1.bin")
	write(first+".zst", compressed)
	write(second, []byte(archiveTestCopy))
	write(second+".zst.partial", []byte("stale"))
	write(filepath.Join(recent, filepath.Base(second)), []byte(archiveTestCopy))
	dat := filepath.Join(dir, "12.dat.zst")
	payload := bytes.Repeat([]byte("archival data repeated for compression\n"), 16384)
	originalDat := encoder.EncodeAll(payload, nil)
	write(dat, originalDat)
	for _, p := range []string{dir, recent} {
		if err := os.Chtimes(p, now, now); err != nil {
			t.Fatal(err)
		}
	}
	b := Bundle{Backups: root}
	if err := HoldBackups(root); err != nil {
		t.Fatal(err)
	}
	if err := b.TidyContext(context.Background(), now); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(second); err != nil {
		t.Fatal(err)
	}
	if err := ReleaseBackups(root); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := b.TidyContext(ctx, now); !errors.Is(err, context.Canceled) {
		t.Fatalf("canceled maintenance: %v", err)
	}
	if err := b.TidyContext(context.Background(), now); err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(dir)
	if err != nil || !info.ModTime().Equal(now) {
		t.Fatalf("archive mtime: %v, %v", info, err)
	}
	if _, err := os.Stat(filepath.Join(dir, archiveMarker)); err != nil {
		t.Fatal(err)
	}
	for _, p := range []string{second, second + ".zst.partial"} {
		if _, err := os.Stat(p); !os.IsNotExist(err) {
			t.Fatalf("%s: %v", p, err)
		}
	}
	want := []string{first + ".zst", second + ".zst"}
	if got := Chunks(dir)["public.books"]; !reflect.DeepEqual(got, want) {
		t.Fatalf("chunks %v", got)
	}
	for _, path := range want {
		if rows, err := countChunkRows(path); err != nil || rows != 0 {
			t.Fatalf("verify %s: %d, %v", path, rows, err)
		}
	}
	dumps, err := ListDumps(root)
	if err != nil || len(dumps) != 2 || dumps[0].Compressed || !dumps[1].Compressed {
		t.Fatalf("listing: %+v, %v", dumps, err)
	}
	archivedDat, err := os.ReadFile(dat)
	if err != nil || len(archivedDat) >= len(originalDat) {
		t.Fatalf("data recompression: %d to %d bytes, %v", len(originalDat), len(archivedDat), err)
	}
	var restored bytes.Buffer
	if err := ExpandChunk(context.Background(), &restored, dat); err != nil || !bytes.Equal(restored.Bytes(), payload) {
		t.Fatalf("archived data changed: %v", err)
	}
	write(second, []byte(archiveTestCopy))
	if got := Chunks(dir)["public.books"]; !reflect.DeepEqual(got, []string{first + ".zst", second}) {
		t.Fatalf("duplicate chunk: %v", got)
	}
	for _, bad := range [][]byte{compressed[:len(compressed)-1], []byte("invalid zstd")} {
		write(second+".zst", bad)
		if _, err := countChunkRows(second + ".zst"); err == nil {
			t.Fatal("accepted damaged zstd")
		}
	}
}

func TestLoadCompressedChunk(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("fake psql uses a shell script")
	}
	dir := t.TempDir()
	raw := filepath.Join(dir, "chunk.public.books.0.bin")
	if err := os.WriteFile(raw, []byte(archiveTestCopy), 0o600); err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	if err := archiveFile(ctx, raw, raw+".zst", false); err != nil {
		t.Fatal(err)
	}
	psql := filepath.Join(dir, "psql")
	if err := os.WriteFile(psql, []byte("#!/bin/sh\ncat > \"$1\"\n"), 0o700); err != nil {
		t.Fatal(err)
	}
	out := filepath.Join(dir, "loaded")
	if err := LoadChunks(ctx, psql, []string{out}, os.Environ(), Chunks(dir), 1); err != nil {
		t.Fatal(err)
	}
	got, err := os.ReadFile(out)
	if err != nil || string(got) != archiveTestCopy {
		t.Fatalf("COPY received %q, %v", got, err)
	}
}
