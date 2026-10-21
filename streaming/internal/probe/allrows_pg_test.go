package probe

import (
	"context"
	"os"
	"testing"

	"github.com/google/uuid"

	"github.com/Hamza-Labs-Core/Maktaba/streaming/internal/pgtest"
)

// AllRows reads every probed video as Lookup reads one (a full node's snapshot).
func TestPGBackend_AllRowsMatchesLookup(t *testing.T) {
	if os.Getenv("MAKTABA_TEST_PG_URL") == "" || testing.Short() {
		t.Skip("MAKTABA_TEST_PG_URL unset (or -short)")
	}
	_, pool := pgtest.ScratchDB(t)
	ctx := context.Background()
	lib, probed, unprobed := uuid.New(), uuid.New(), uuid.New()
	for _, q := range []struct {
		sql  string
		args []any
	}{
		{`INSERT INTO libraries (id, name, roots) VALUES ($1, 'lib', '{}')`, []any{lib}},
		{`INSERT INTO videos (id, library_id, content_hash, path, filename, size_bytes, mtime, duration_sec)
		  VALUES ($1, $3, repeat('a', 64), '/Volumes/Originals/a.mp4', 'a.mp4', 1000, now(), 14),
		         ($2, $3, repeat('b', 64), '/Volumes/Originals/b.mp4', 'b.mp4', 1000, now(), 20)`, []any{probed, unprobed, lib}},
		{`INSERT INTO media_info (video_id, container, video_codec, width, height, bitrate_kbps)
		  VALUES ($1, 'mp4', 'h264', 1920, 1080, 4000)`, []any{probed}},
		{`INSERT INTO audio_tracks (video_id, track_index, codec, channels, is_default)
		  VALUES ($1, 0, 'ac3', 6, false), ($1, 1, 'aac', 2, true)`, []any{probed}},
	} {
		if _, err := pool.Exec(ctx, q.sql, q.args...); err != nil {
			t.Fatal(err)
		}
	}
	b := NewPGBackend(pool)
	rows, err := b.AllRows(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 1 || rows[0].VideoID != probed {
		t.Fatalf("all rows: %+v, want the probed video only", rows)
	}
	one, err := b.Lookup(ctx, probed)
	if err != nil {
		t.Fatal(err)
	}
	got := rows[0]
	if got.LibraryID != one.LibraryID || got.Path != one.Path || got.AudioCodec != "aac" || one.AudioCodec != "aac" ||
		got.AudioChannels != one.AudioChannels || got.Height != one.Height || got.BitrateKbps != one.BitrateKbps ||
		got.DurationSec != one.DurationSec || got.Size != one.Size || !got.ModTime.Equal(one.ModTime) || !got.Probed {
		t.Fatalf("all rows %+v differs from lookup %+v", got, *one)
	}
}
