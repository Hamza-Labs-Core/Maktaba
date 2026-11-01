package probe

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/google/uuid"

	"github.com/Hamza-Labs-Core/Maktaba/streaming/internal/pgtest"
)

// lookupSQL against a real migrated Postgres: a track row with no codec (transcribe
// recorded the stream it decoded before the probe filled it in, the silent streams of
// 2026-10-31) still has audio; so has a video with no track row whose ffprobe output
// lists an audio stream; only a video with neither has none. Slot 0171's backfill then
// fills the codec-less row from the probe's output, by audio rank.
func TestPGLookup_HasAudio(t *testing.T) {
	_, pool := pgtest.ScratchDB(t)
	ctx := context.Background()
	exec := func(q string, args ...any) {
		t.Helper()
		if _, err := pool.Exec(ctx, q, args...); err != nil {
			t.Fatalf("%s: %v", q, err)
		}
	}
	const withAudio = `{"format": {"format_name": "matroska,webm"}, "streams": [
		{"codec_type": "video", "codec_name": "h264"},
		{"codec_type": "audio", "codec_name": "opus", "channels": 2, "sample_rate": "48000",
		 "tags": {"language": "ara"}, "disposition": {"default": 1}}]}`
	const silent = `{"format": {"format_name": "mov,mp4"}, "streams": [{"codec_type": "video", "codec_name": "h264"}]}`

	lib := uuid.New()
	exec(`INSERT INTO libraries (id, name, roots) VALUES ($1, 'L', '{/m}')`, lib)
	vid := map[string]uuid.UUID{}
	for _, n := range []string{"bare", "untracked", "silent", "known"} {
		vid[n] = uuid.New()
		exec(`INSERT INTO videos (id, library_id, path, filename, content_hash, size_bytes, mtime, state)
			VALUES ($1, $2, $3, $3, encode(sha256(convert_to($3, 'UTF8')), 'hex'), 1, now(), 'ready')`, vid[n], lib, "/m/"+n+".mkv")
	}
	probed := func(n, raw string) {
		exec(`INSERT INTO media_info (video_id, container, video_codec, raw_ffprobe) VALUES ($1, 'matroska,webm', 'h264', $2::text::jsonb)`, vid[n], raw)
	}
	probed("bare", withAudio)
	exec(`INSERT INTO audio_tracks (video_id, track_index, is_default) VALUES ($1, 0, true)`, vid["bare"])
	probed("untracked", withAudio)
	probed("silent", silent)
	probed("known", withAudio)
	exec(`INSERT INTO audio_tracks (video_id, track_index, codec, channels, is_default) VALUES ($1, 0, 'aac', 6, true)`, vid["known"])

	look := func(n string) *Row {
		t.Helper()
		row, err := lookupOn(ctx, pool, vid[n])
		if err != nil {
			t.Fatalf("lookup %s: %v", n, err)
		}
		return row
	}
	for n, want := range map[string]bool{"bare": false, "untracked": false, "silent": true, "known": false} {
		if got := look(n).NoAudio(); got != want {
			t.Errorf("%s: NoAudio()=%v want %v", n, got, want)
		}
	}
	if r := look("bare"); !r.HasAudio || r.AudioCodec != "" {
		t.Errorf("bare: HasAudio=%v codec=%q, want audio with its codec unknown", r.HasAudio, r.AudioCodec)
	}
	if r := look("known"); r.AudioCodec != "aac" || r.AudioChannels != 6 {
		t.Errorf("known: %q/%d", r.AudioCodec, r.AudioChannels)
	}

	// slot 0171: the bare row takes its stream's facts; the row with a codec keeps it
	up, err := os.ReadFile(filepath.Join(pgtest.RepoRoot(t), "shared", "db", "migrations", "0171_audio_tracks_backfill_codec.sql"))
	if err != nil {
		t.Fatal(err)
	}
	backfill := strings.Split(string(up), "-- +goose Down")[0]
	for range 2 { // idempotent
		exec(backfill)
	}
	if r := look("bare"); r.AudioCodec != "opus" || r.AudioChannels != 2 || !r.HasAudio {
		t.Errorf("bare after the backfill: %q/%d has_audio=%v, want opus/2", r.AudioCodec, r.AudioChannels, r.HasAudio)
	}
	if r := look("known"); r.AudioCodec != "aac" || r.AudioChannels != 6 {
		t.Errorf("known after the backfill: %q/%d, want aac/6 untouched", r.AudioCodec, r.AudioChannels)
	}
}
