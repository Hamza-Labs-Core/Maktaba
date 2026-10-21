package hdhr

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/Hamza-Labs-Core/Maktaba/streaming/internal/pgtest"
)

// hashOf is a valid content hash (hex sha256) for a fixture video.
func hashOf(n string) string { s := sha256.Sum256([]byte(n)); return hex.EncodeToString(s[:]) }

// The guide reads each block's video; a programme's image sources are its
// series' poster and first episode and its own thumbnail, and it is on air only
// as a programme of an enabled live channel in the window asked.
func TestPGRepo_GuideArt(t *testing.T) {
	_, pool := pgtest.ScratchDB(t)
	ctx := context.Background()
	exec := func(q string, args ...any) {
		t.Helper()
		if _, err := pool.Exec(ctx, q, args...); err != nil {
			t.Fatalf("%s: %v", q, err)
		}
	}
	lib, ch, off := uuid.New(), uuid.New(), uuid.New()
	exec(`INSERT INTO libraries (id, name, roots) VALUES ($1, 'L', '{/m}')`, lib)
	exec(`INSERT INTO channels (id, number, name, slug, mode) VALUES ($1, 101, 'One', 'one', 'shuffle')`, ch)
	exec(`INSERT INTO channels (id, number, name, slug, mode, enabled) VALUES ($1, 102, 'Off', 'off', 'shuffle', false)`, off)
	vid := map[string]uuid.UUID{}
	for _, n := range []string{"ep1", "ep2", "film", "filler", "later"} {
		vid[n] = uuid.New()
		exec(`INSERT INTO videos (id, library_id, path, filename, content_hash, size_bytes, mtime, state)
			VALUES ($1, $2, $3, $3, $4, 1, now(), 'ready')`, vid[n], lib, "/m/"+n+".mp4", hashOf(n))
	}
	exec(`UPDATE videos SET poster_path = '/thumbs/hash-film/poster.jpg' WHERE id = $1`, vid["film"])
	series := uuid.New()
	exec(`INSERT INTO series (id, library_id, name, poster_path) VALUES ($1, $2, 'S', '/art/s.png')`, series, lib)
	exec(`INSERT INTO series_episodes (series_id, video_id, season, episode) VALUES ($1, $2, 1, 2), ($1, $3, 1, 1)`,
		series, vid["ep2"], vid["ep1"])
	at := time.Now().UTC().Truncate(time.Second)
	seq := int64(0)
	add := func(c uuid.UUID, kind, v string, start time.Duration) {
		seq++
		exec(`INSERT INTO channel_programs (channel_id, seq, kind, video_id, start_at, end_at, source_duration)
			VALUES ($1, $2, $3, $4, $5, $6, 60000)`, c, seq, kind, vid[v], at.Add(start), at.Add(start+time.Hour))
	}
	add(ch, "program", "ep2", 0)
	add(ch, "program", "film", time.Hour)
	add(ch, "filler", "filler", 2*time.Hour)
	add(ch, "program", "later", 30*24*time.Hour)
	add(off, "program", "ep1", 0)
	exec(`INSERT INTO channel_plans (input_hash, plan) VALUES ('a', '{"config":{"dz_logo_dir":"/old"}}'),
		('b', '{"config":{"dz_logo_dir":"/logos","dz_watermark_file":"/wm/w.png"}}')`)

	r := NewPGRepo(pool)
	progs, err := r.GuidePrograms(ctx, at.Add(-time.Hour), at.Add(3*time.Hour))
	if err != nil || len(progs) != 3 || progs[0].VideoID != vid["ep2"] {
		t.Fatalf("programs %+v %v", progs, err)
	}
	start, end := at.Add(-time.Hour), at.Add(7*24*time.Hour)
	a, err := r.ProgrammeArt(ctx, ch, vid["ep2"], start, end)
	if err != nil || !a.OnAir || a.SeriesPoster != "/art/s.png" || a.SeriesCard.ID != vid["ep1"].String() ||
		a.SeriesCard.ContentHash != hashOf("ep1") || a.Video.ContentHash != hashOf("ep2") {
		t.Errorf("episode art %+v %v", a, err)
	}
	a, err = r.ProgrammeArt(ctx, ch, vid["film"], start, end)
	if err != nil || !a.OnAir || a.SeriesCard.ID != "" || a.Video.PosterPath != "/thumbs/hash-film/poster.jpg" {
		t.Errorf("film art %+v %v", a, err)
	}
	for _, c := range []struct {
		name string
		ch   uuid.UUID
		v    uuid.UUID
	}{
		{"filler block", ch, vid["filler"]},
		{"outside the window", ch, vid["later"]},
		{"disabled channel", off, vid["ep1"]},
		{"another channel's video", off, vid["film"]},
		{"unknown video", ch, uuid.New()},
	} {
		if a, err := r.ProgrammeArt(ctx, c.ch, c.v, start, end); err != nil || a.OnAir {
			t.Errorf("%s: on air %+v %v", c.name, a, err)
		}
	}
	roots, err := r.PlanArtRoots(ctx)
	if err != nil || len(roots) != 2 || roots[0] != "/logos" || roots[1] != "/wm" {
		t.Errorf("plan roots %v %v", roots, err)
	}
}
