//go:build integration

package queues

import (
	"os"
	"path/filepath"
	"testing"
)

// Slot 0175: the switch. {"on": false} is mode off; {"on": true} turns a source back on in
// its last mode on (new_and_backfill when it never was), queueing the refresh as any
// turning on does; an on source stays as it is. Only an admin may (the role table in
// TestOffline_EveryEndpointIsAdminOnly has the switch too).
func TestOffline_SourceSwitch(t *testing.T) {
	e := newOfflineEnv(t)
	base := "/api/libraries/" + oLibA + "/youtube/sources"
	var w SourceWriteResponse
	e.want(201, "POST", base, `{"url":"https://www.youtube.com/@lessons","mode":"off"}`, &w)
	sid := w.Source.ID
	if w.Source.LastOnMode != nil || w.Refresh != nil {
		t.Fatalf("added off = %+v", w)
	}
	// never on: the add form's default
	e.want(200, "PATCH", base+"/"+sid, `{"on":true}`, &w)
	if w.Source.Mode != ModeNewAndBackfill || w.Refresh == nil || w.Source.LastOnMode == nil ||
		*w.Source.LastOnMode != ModeNewAndBackfill {
		t.Fatalf("first on = %+v", w)
	}
	e.want(200, "PATCH", base+"/"+sid, `{"mode":"new_only"}`, &w)
	e.want(200, "PATCH", base+"/"+sid, `{"on":false}`, &w)
	if w.Source.Mode != ModeOff || w.Source.LastOnMode == nil || *w.Source.LastOnMode != ModeNewOnly {
		t.Fatalf("off = %+v (it keeps how it downloaded)", w)
	}
	e.exec(`DELETE FROM processing_jobs WHERE stage = 'youtube_sync'`)
	e.want(200, "PATCH", base+"/"+sid, `{"on":true}`, &w)
	if w.Source.Mode != ModeNewOnly || w.Refresh == nil {
		t.Fatalf("on again = %+v, want new_only and a refresh", w)
	}
	e.exec(`DELETE FROM processing_jobs WHERE stage = 'youtube_sync'`)
	e.want(200, "PATCH", base+"/"+sid, `{"on":true}`, &w)
	if w.Source.Mode != ModeNewOnly || w.Refresh != nil {
		t.Fatalf("on while on = %+v, want unchanged", w)
	}
	for _, body := range []string{`{"on":true,"mode":"off"}`, `{"on":"yes"}`} {
		if rec := e.do("admin", "PATCH", base+"/"+sid, body); rec.Code/100 != 4 {
			t.Errorf("patch %s = %d", body, rec.Code)
		}
	}
	// a viewer cannot, and nothing changed for them
	if rec := e.do("viewer", "PATCH", base+"/"+sid, `{"on":false}`); rec.Code != 403 {
		t.Fatalf("viewer switch = %d", rec.Code)
	}
	if n := e.count(`SELECT count(*) FROM youtube_sources WHERE id = $1 AND mode = 'new_only'`, sid); n != 1 {
		t.Fatalf("the viewer's switch changed the source")
	}
	e.audited("queue.youtube_source.patch", sid)
}

// The file-name fallback: ids in yt-dlp's names resolve through youtube_videos; a sidecar
// names its channel itself; the answer is kept; nothing is added or turned on.
func TestOffline_GuessSource(t *testing.T) {
	e := newOfflineEnv(t)
	dir := t.TempDir()
	e.exec(`INSERT INTO libraries (id, name, roots) VALUES ('4700000a-0000-4000-8000-0000000000c1', 'Gamma', ARRAY[$1])`, dir)
	gamma := "4700000a-0000-4000-8000-0000000000c1"
	for i, name := range []string{"talk one #aaaaaaaaaa1.mp4", "talk two [aaaaaaaaaa2].mkv", "plain.mp4"} {
		e.exec(`INSERT INTO videos (library_id, content_hash, path, filename, size_bytes, mtime, title)
			VALUES ($1, repeat($2, 64), $3, $4, 1, now(), $4)`, gamma, string(rune('a'+i)), filepath.Join(dir, name), name)
	}
	e.exec(`INSERT INTO youtube_videos (youtube_id, channel_id, title) VALUES
		('aaaaaaaaaa1', 'UCgggggggggggggggggggggg', 'one'), ('aaaaaaaaaa2', 'UCgggggggggggggggggggggg', 'two')`)
	e.exec(`INSERT INTO youtube_channels (channel_id, library_id, url, title) VALUES
		('UCgggggggggggggggggggggg', $1, 'https://www.youtube.com/channel/UCgggggggggggggggggggggg', 'Gamma talks')`, oLibB)
	e.exec(`INSERT INTO youtube_sources (library_id, kind, url, external_id, mode) VALUES
		($1, 'channel', 'https://www.youtube.com/channel/UCgggggggggggggggggggggg', 'UCgggggggggggggggggggggg', 'off')`, oLibB)
	var g SourceGuess
	e.want(200, "POST", "/api/libraries/"+gamma+"/youtube/guess", "", &g)
	if g.ExternalID == nil || *g.ExternalID != "UCgggggggggggggggggggggg" || g.IDsFound != 2 || g.IDsMatched != 2 ||
		g.Title == nil || *g.Title != "Gamma talks" || *g.Evidence != "filename" ||
		len(g.FollowedBy) != 1 || g.FollowedBy[0].Name != "Beta" {
		t.Fatalf("guess = %+v", g)
	}
	if n := e.count(`SELECT count(*) FROM youtube_sources WHERE library_id = $1`, gamma); n != 0 {
		t.Fatalf("a guess added %d sources", n)
	}
	// a sidecar beats the names
	if err := os.WriteFile(filepath.Join(dir, "plain.info.json"),
		[]byte(`{"id":"zzzzzzzzzz9","channel_id":"UCssssssssssssssssssssss","channel":"Side"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	e.want(200, "POST", "/api/libraries/"+gamma+"/youtube/guess", "", &g)
	if g.ExternalID == nil || *g.ExternalID != "UCssssssssssssssssssssss" || *g.Evidence != "info_json" || g.IDsFound != 3 {
		t.Fatalf("sidecar guess = %+v", g)
	}
	if n := e.count(`SELECT count(*) FROM youtube_source_guesses WHERE library_id = $1 AND external_id = 'UCssssssssssssssssssssss'`, gamma); n != 1 {
		t.Fatal("the guess was not kept")
	}
	// nothing found is an answer too
	var none SourceGuess
	e.want(200, "POST", "/api/libraries/"+oLibB+"/youtube/guess", "", &none)
	if none.ExternalID != nil || none.IDsFound != 0 {
		t.Fatalf("no ids = %+v", none)
	}
	e.audited("queue.youtube_source.guess", gamma)
}

// The Tartube import is one catalog job; its report and the path to offer come back.
func TestOffline_TartubeImport(t *testing.T) {
	e := newOfflineEnv(t)
	var empty TartubeImport
	e.want(200, "GET", "/api/queues/youtube-tartube", "", &empty)
	if empty.JobID != nil {
		t.Fatalf("no import yet = %+v", empty)
	}
	for _, body := range []string{`{"path":"relative.db"}`, `{"path":"/x/tartube.txt"}`, `{}`} {
		if rec := e.do("admin", "POST", "/api/queues/youtube-tartube", body); rec.Code != 422 {
			t.Errorf("post %s = %d", body, rec.Code)
		}
	}
	e.want(202, "POST", "/api/queues/youtube-tartube", `{"path":"/Volumes/O/Youtube/tartube.db"}`, nil)
	j := e.job(StageYouTubeTartube, "catalog", catalogSubject)
	tt, _ := j.payload["tartube"].(map[string]any)
	if j.state != "pending" || !j.force || tt["path"] != "/Volumes/O/Youtube/tartube.db" {
		t.Fatalf("job = %+v", j)
	}
	e.exec(`UPDATE processing_jobs SET state = 'done', finished_at = now(),
		output_summary = '{"counts":{"created":2}}'::jsonb WHERE id = $1`, j.id)
	var got TartubeImport
	e.want(200, "GET", "/api/queues/youtube-tartube", "", &got)
	if got.State == nil || *got.State != "done" || got.Path == nil || string(got.Report) != `{"counts":{"created":2}}` {
		t.Fatalf("last import = %+v %s", got, got.Report)
	}
	// a tartube.db beside the library folders is offered
	dir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dir, "Chan"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "tartube.db"), []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	e.exec(`UPDATE libraries SET roots = ARRAY[$1] WHERE id = $2`, filepath.Join(dir, "Chan"), oLibB)
	e.want(200, "GET", "/api/queues/youtube-tartube", "", &got)
	if got.SuggestedPath == nil || *got.SuggestedPath != dir+"/tartube.db" {
		t.Fatalf("suggested = %v", got.SuggestedPath)
	}
	e.audited("queue.youtube_tartube.import", "")
}
