package logbench

import (
	"encoding/json"
	"fmt"
	"math/rand"
	"time"
)

// Corpus makes n lines of a realistic mix (seeded: the same seed, the same lines): the
// services' share, 80 % info, 15 % warn, 4 % error, 1 % debug, request ids shared by a few
// lines each, Arabic library and video names, over the span ending at end.
func Corpus(seed int64, n int, end time.Time, span time.Duration) [][]byte {
	r := rand.New(rand.NewSource(seed))
	services := []string{"api", "api", "api", "streaming", "pipeline", "pipeline", "supervisor"}
	msgs := []string{"http_request", "video_loaded", "transcode_started", "segment_written", "job_claimed",
		"job_done", "plex_fetch_failed", "library_scanned", "session_opened", "thumbnail_made"}
	names := []string{"مكتبة الأفلام", "دروس التفسير", "Lectures 2026", "الحلقة الأولى", "Kids", "محاضرات"}
	out := make([][]byte, n)
	var rid string
	for i := 0; i < n; i++ {
		lvl := "info"
		switch p := r.Intn(100); {
		case p < 1:
			lvl = "debug"
		case p < 5:
			lvl = "error"
		case p < 20:
			lvl = "warn"
		}
		if i%4 == 0 {
			rid = fmt.Sprintf("0193%04x-%04x-7%03x-8%03x-%012x", r.Intn(1<<16), r.Intn(1<<16), r.Intn(1<<12),
				r.Intn(1<<12), r.Int63n(1<<48))
		}
		at := end.Add(-span + time.Duration(float64(span)*float64(i)/float64(n)))
		m := map[string]any{"ts": at.UTC().Format(time.RFC3339Nano), "level": lvl,
			"service": services[r.Intn(len(services))], "msg": msgs[r.Intn(len(msgs))], "request_id": rid,
			"attrs": map[string]any{"library": names[r.Intn(len(names))], "n": r.Intn(10000)}}
		if lvl == "error" {
			m["err"] = map[string]any{"code": "db.timeout", "msg": fmt.Sprintf("query took %dms", 5000+r.Intn(900))}
		}
		b, _ := json.Marshal(m)
		out[i] = b
	}
	return out
}
