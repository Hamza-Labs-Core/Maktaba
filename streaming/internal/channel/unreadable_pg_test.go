package channel

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/Hamza-Labs-Core/Maktaba/streaming/internal/pgtest"
)

// The stand-ins come from the generation a repo airs (one per video, nearest
// first, never a deleted video), and a report is a forced probe of the video.
func TestPGRepo_FillersAroundAndReport(t *testing.T) {
	_, pool := pgtest.ScratchDB(t)
	ctx := context.Background()
	exec := func(q string, args ...any) {
		t.Helper()
		if _, err := pool.Exec(ctx, q, args...); err != nil {
			t.Fatalf("%s: %v", q, err)
		}
	}
	lib, ch := uuid.New(), uuid.New()
	exec(`INSERT INTO libraries (id, name, roots) VALUES ($1, 'L', '{/m}')`, lib)
	exec(`INSERT INTO channels (id, number, name, slug, mode) VALUES ($1, 101, 'One', 'one', 'shuffle')`, ch)
	vid := map[string]uuid.UUID{}
	for _, n := range []string{"near", "far", "deleted", "prog"} {
		vid[n] = uuid.New()
		exec(`INSERT INTO videos (id, library_id, path, filename, content_hash, size_bytes, mtime, state)
			VALUES ($1, $2, $3, $3, md5($3), 1, now(), 'ready')`, vid[n], lib, "/m/"+n+".mp4")
	}
	exec(`UPDATE videos SET deleted_at = now() WHERE id = $1`, vid["deleted"])
	at := time.Now().UTC().Truncate(time.Second)
	add := func(seq int64, gen int, kind, v string, start time.Duration) {
		exec(`INSERT INTO channel_programs (channel_id, seq, kind, video_id, start_at, end_at, source_duration, sched_gen)
			VALUES ($1, $2, $3, $4, $5, $6, 60000, $7)`, ch, seq, kind, vid[v], at.Add(start), at.Add(start+time.Minute), gen)
	}
	add(1, 0, KindFiller, "far", -3*time.Hour)
	add(2, 0, KindFiller, "near", -10*time.Minute)
	add(3, 0, KindProgram, "prog", 0)
	add(4, 0, KindBumper, "near", 5*time.Minute) // nearer, same video
	add(5, 0, KindFiller, "deleted", time.Minute)
	add(6, 3, KindFiller, "far", 0) // generation 3 only

	live := NewPGRepo(pool, "home")
	got, err := live.FillersAround(ctx, ch, at, 5)
	if err != nil || len(got) != 2 || got[0].Seq != 4 || got[1].Seq != 1 || got[0].Path != "/m/near.mp4" {
		t.Fatalf("live fillers %+v %v", got, err)
	}
	var rel uuid.UUID
	if err := pool.QueryRow(ctx, `INSERT INTO tv_releases (digest, sched_gen) VALUES ('d', 3) RETURNING id`).Scan(&rel); err != nil {
		t.Fatal(err)
	}
	rr, err := NewReleaseRepo(ctx, pool, "home", rel)
	if err != nil {
		t.Fatal(err)
	}
	got, err = rr.FillersAround(ctx, ch, at, 5)
	if err != nil || len(got) != 1 || got[0].Seq != 6 {
		t.Fatalf("release fillers %+v %v", got, err)
	}

	if err := live.ReportUnreadable(ctx, vid["prog"], "stat /m/prog.mp4: no such file"); err != nil {
		t.Fatal(err)
	}
	var stage, state string
	if err := pool.QueryRow(ctx, `SELECT stage, state FROM processing_jobs WHERE video_id = $1`, vid["prog"]).
		Scan(&stage, &state); err != nil || stage != "probe" || state != "pending" {
		t.Fatalf("probe job %q %q %v", stage, state, err)
	}
	if err := live.ReportUnreadable(ctx, vid["prog"], "again"); err != nil {
		t.Fatalf("a second report coalesces: %v", err)
	}
}
