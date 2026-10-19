package videos

import (
	"encoding/json"
	"net/http/httptest"
	"testing"

	"github.com/Hamza-Labs-Core/Maktaba/api/internal/auth/principal"
)

// Story 9.1: the library's default_subtitle_lang marks its subtitle track the
// default; a library without one has no default track (as before).
func TestSubtitlesDefaultTrackFollowsTheLibrarySetting(t *testing.T) {
	db, srv, _ := newDisplayEnv(t)
	for _, q := range []string{
		`CREATE TABLE libraries (id TEXT PRIMARY KEY, settings TEXT)`,
		`CREATE TABLE subtitle_files (id INTEGER PRIMARY KEY, video_id TEXT, language TEXT, format TEXT,
			source TEXT, deleted_at DATETIME)`,
		`INSERT INTO libraries (id, settings) VALUES ('lib1', '{}')`,
		`INSERT INTO subtitle_files (video_id, language, format, source) VALUES
			('` + vid + `', 'ar', 'vtt', 'generated'), ('` + vid + `', 'en', 'vtt', 'external'),
			('` + vid + `', 'ar', 'srt', 'generated')`,
	} {
		if _, err := db.Exec(q); err != nil {
			t.Fatal(err)
		}
	}
	get := func() map[string]bool {
		t.Helper()
		req := httptest.NewRequest("GET", "/api/videos/"+vid+"/subtitles", nil)
		p := &principal.Principal{UserID: "a", AccessAllLibraries: true}
		req = req.WithContext(principal.WithPrincipal(req.Context(), p))
		rec := httptest.NewRecorder()
		srv.ServeHTTP(rec, req)
		if rec.Code != 200 {
			t.Fatalf("status %d: %s", rec.Code, rec.Body.String())
		}
		var body struct{ Items []Subtitle }
		if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil || len(body.Items) != 3 {
			t.Fatalf("body %s", rec.Body.String())
		}
		out := map[string]bool{}
		for _, s := range body.Items {
			out[s.Language+"."+s.Format] = s.IsDefault
		}
		return out
	}
	for k, d := range get() {
		if d {
			t.Fatalf("%s is the default with no setting", k)
		}
	}
	if _, err := db.Exec(`UPDATE libraries SET settings = '{"default_subtitle_lang":"ar"}'`); err != nil {
		t.Fatal(err)
	}
	got := get()
	if !got["ar.vtt"] || !got["ar.srt"] || got["en.vtt"] {
		t.Fatalf("ar default: %v", got)
	}
}
