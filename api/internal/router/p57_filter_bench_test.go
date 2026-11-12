package router_test

// Epic 57 story 57.03, test case T12: what the parental filter costs, on a library of
// 150,000 videos (MAKTABA_P57_BENCH_VIDEOS changes the number), behind the production router
// on a migrated Postgres (MAKTABA_TEST_PG_URL; skipped without it).
//
//	go test ./internal/router/ -run '^$' -bench BenchmarkP57Filter -benchtime 200x
//
// Each case reports its p95 in milliseconds beside ns/op. The story's bounds (an account with
// no limit within 2% of before; a restricted account's p95 at most 60 ms) are read from the
// output, not asserted: on a shared runner or a loaded Mac the tail measures the machine. What
// can be held without a clock is held by tests: an account with no limit adds nothing to a
// query (parental.SQL answers "", TestSQLMatchesPermits and the filter's unit tests).

import (
	"io"
	"log"
	"log/slog"
	"net/http"
	"os"
	"sort"
	"strconv"
	"testing"
	"time"

	"github.com/Hamza-Labs-Core/Maktaba/api/internal/parental"
)

func BenchmarkP57Filter(b *testing.B) {
	n := 150000
	if v, err := strconv.Atoi(os.Getenv("MAKTABA_P57_BENCH_VIDEOS")); err == nil && v > 0 {
		n = v
	}
	e := setupFilter(b)
	// the request log is not what is measured
	prevLog, prevOut := slog.Default(), log.Writer()
	slog.SetDefault(slog.New(slog.NewTextHandler(io.Discard, nil)))
	log.SetOutput(io.Discard)
	b.Cleanup(func() { slog.SetDefault(prevLog); log.SetOutput(prevOut) })

	// every tier and the unrated in equal parts, in two libraries, a tenth of them in series
	e.exec(`INSERT INTO videos (id, library_id, path, filename, content_hash, size_bytes, mtime, state, title, duration_sec, updated_at, created_at, age_tier)
		SELECT gen_random_uuid(), CASE WHEN g % 4 = 0 THEN $2::uuid ELSE $1::uuid END, '/media/bench/' || g || '.mp4', g || '.mp4',
		       encode(sha256(convert_to('bench-' || g, 'UTF8')), 'hex'), 1000, now(), 'ready', 'Bench ' || g, 600,
		       now() - (g || ' seconds')::interval, now() - (g || ' seconds')::interval,
		       (ARRAY[0, 9, 13, 16, 18, NULL]::smallint[])[1 + g % 6]
		FROM generate_series(1, $3::int) g`, pfLibA, pfLibB, n)
	e.exec(`INSERT INTO series_episodes (series_id, video_id)
		SELECT $1, id FROM videos WHERE title LIKE 'Bench %0' ON CONFLICT DO NOTHING`, pfSeries16)
	e.exec(`ANALYZE videos`)
	e.exec(`ANALYZE series_episodes`)

	limits := []struct {
		name string
		who  string
		set  func()
	}{
		{"unrestricted", "viewer", func() {}},
		{"restricted", "child", func() { e.clearRules() }},
		// a block on a series and an allow on a collection: the clause's sub-selects
		{"restricted_with_rules", "child", func() {
			e.clearRules()
			e.rule(parental.EffectBlock, parental.SubjectSeries, pfSeries16)
			e.rule(parental.EffectAllow, parental.SubjectCollection, pfCol16)
		}},
	}
	routes := []struct{ name, path string }{
		{"videos_page", "/api/videos?library_id=" + pfLibA + "&limit=50"},
		{"home", "/api/home"},
	}
	for _, route := range routes {
		for _, lim := range limits {
			b.Run(route.name+"/"+lim.name, func(b *testing.B) {
				lim.set()
				if code, body := e.get(lim.who, route.path); code != http.StatusOK { // and a warm start
					b.Fatalf("%s GET %s: %d %s", lim.who, route.path, code, body)
				}
				took := make([]time.Duration, 0, b.N)
				b.ResetTimer()
				for i := 0; i < b.N; i++ {
					t0 := time.Now()
					if code, _ := e.get(lim.who, route.path); code != http.StatusOK {
						b.Fatalf("%s GET %s: %d", lim.who, route.path, code)
					}
					took = append(took, time.Since(t0))
				}
				b.StopTimer()
				sort.Slice(took, func(i, j int) bool { return took[i] < took[j] })
				p95 := took[(len(took)*95+99)/100-1]
				b.ReportMetric(float64(p95.Microseconds())/1000, "p95-ms")
			})
		}
	}
}
