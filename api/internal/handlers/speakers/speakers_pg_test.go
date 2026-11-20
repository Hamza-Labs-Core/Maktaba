package speakers

import (
	"context"
	"net/http"
	"net/http/httptest"
	"sort"
	"strings"
	"testing"

	"github.com/go-chi/chi/v5"

	"github.com/Hamza-Labs-Core/Maktaba/api/internal/auth/principal"
	"github.com/Hamza-Labs-Core/Maktaba/api/internal/cluster/clustertest"
)

// Epic 49 story 49.6: renaming or merging a speaker queues the keyword_index stage of the
// videos whose active transcript has the speaker's lines, and of no other.
func TestRenameAndMergeQueueKeywordIndex(t *testing.T) {
	db, _ := clustertest.ScratchDB(t)
	const lib = "00000000-0000-4000-8000-000000490101"
	vids := []string{"00000000-0000-4000-8000-000000490111", "00000000-0000-4000-8000-000000490112",
		"00000000-0000-4000-8000-000000490113"}
	tids := []string{"00000000-0000-4000-8000-000000490121", "00000000-0000-4000-8000-000000490122",
		"00000000-0000-4000-8000-000000490123"}
	must := func(q string, args ...any) {
		t.Helper()
		if _, err := db.Exec(q, args...); err != nil {
			t.Fatalf("%s: %v", q, err)
		}
	}
	must(`INSERT INTO libraries (id, name, roots) VALUES ($1, 'l', ARRAY['/l'])`, lib)
	for i, v := range vids {
		must(`INSERT INTO videos (id, library_id, content_hash, path, filename, size_bytes, mtime)
			VALUES ($1, $2, repeat($3, 64), '/l/' || $3, $3 || '.mp4', 1, now())`, v, lib, string(rune('a'+i)))
		// the third video's transcript is not the active one
		must(`INSERT INTO audio_tracks (video_id, track_index, codec, channels, language, is_default)
			VALUES ($1, 0, 'aac', 2, 'ara', true)`, v)
		must(`INSERT INTO transcripts (id, video_id, audio_track_id, language, backend, model, is_active)
			SELECT $1, $2, a.id, 'ar', 'test', 'test', $3 FROM audio_tracks a WHERE a.video_id = $2`, tids[i], v, i < 2)
		must(`INSERT INTO transcript_segments (transcript_id, seq, start_sec, end_sec, text) VALUES ($1, 0, 0, 1, 'x')`, tids[i])
	}
	const a, b = "00000000-0000-4000-8000-000000490131", "00000000-0000-4000-8000-000000490132"
	must(`INSERT INTO speakers (id, library_id, name) VALUES ($1, $3, 'الشيخ'), ($2, $3, 'الشيخ أحمد')`, a, b, lib)
	// a speaks in videos 1 and 3 (3's transcript inactive), b in video 2
	must(`INSERT INTO segment_speakers (segment_id, speaker_id)
		SELECT g.id, $1 FROM transcript_segments g WHERE g.transcript_id IN ($2, $3)`, a, tids[0], tids[2])
	must(`INSERT INTO segment_speakers (segment_id, speaker_id)
		SELECT g.id, $1 FROM transcript_segments g WHERE g.transcript_id = $2`, b, tids[1])

	var queued []string
	h := &Handler{DB: db, KeywordIndexRefresh: func(_ context.Context, ids []string) error {
		queued = append(queued, ids...)
		return nil
	}}
	r := chi.NewRouter()
	h.Mount(r)
	do := func(method, path, body string) {
		t.Helper()
		req := httptest.NewRequest(method, path, strings.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
		req = req.WithContext(principal.WithPrincipal(req.Context(), &principal.Principal{UserID: "a", IsAdmin: true}))
		rec := httptest.NewRecorder()
		r.ServeHTTP(rec, req)
		if rec.Code != http.StatusOK {
			t.Fatalf("%s %s: %d %s", method, path, rec.Code, rec.Body.String())
		}
	}
	do("PATCH", "/api/speakers/"+a, `{"name":"الشيخ محمد"}`)
	if len(queued) != 1 || queued[0] != vids[0] {
		t.Fatalf("rename queued %v, want only %s (the active transcript's video)", queued, vids[0])
	}
	queued = nil
	do("POST", "/api/speakers/merge", `{"keep":"`+b+`","drop":"`+a+`"}`)
	sort.Strings(queued)
	if len(queued) != 2 || queued[0] != vids[0] || queued[1] != vids[1] {
		t.Fatalf("merge queued %v, want %s and %s", queued, vids[0], vids[1])
	}
}
