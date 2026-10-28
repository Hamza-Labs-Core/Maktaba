//go:build integration

package queues

// Epic 47 (story 47.11) against a real, migrated Postgres: every endpoint's
// roles (admin 200, a signed-in non-admin 403, signed out 401 behind the
// API's RequireAuth), its audit row, and what it queues.
//
//	MAKTABA_TEST_PG_URL=postgres://u@127.0.0.1:5432/postgres?sslmode=disable \
//	    go test -tags integration ./internal/handlers/queues/ -run Offline

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/Hamza-Labs-Core/Maktaba/api/internal/auth/middleware"
	"github.com/Hamza-Labs-Core/Maktaba/api/internal/auth/principal"
	"github.com/Hamza-Labs-Core/Maktaba/api/internal/handlers/search"
	"github.com/Hamza-Labs-Core/Maktaba/api/internal/handlers/ws"
)

const (
	oAdmin  = "4700000a-0000-4000-8000-000000000001"
	oViewer = "4700000a-0000-4000-8000-000000000002"
	oLibA   = "4700000b-0000-4000-8000-00000000000a"
	oLibB   = "4700000b-0000-4000-8000-00000000000b"
	oMKV    = "4700000c-0000-4000-8000-000000000001"
	oMP4    = "4700000c-0000-4000-8000-000000000002"
	oOldMP4 = "4700000c-0000-4000-8000-000000000003"
	oDone   = "4700000d-0000-4000-8000-000000000001" // a conversion, restorable
	oPurged = "4700000d-0000-4000-8000-000000000002" // purged: not restorable
	oFetch  = "4700000d-0000-4000-8000-000000000003" // a download: no undo
	oFailed = "4700000d-0000-4000-8000-000000000004" // a failed verification
	oGroup  = "4700000f-0000-4000-8000-000000000001"
	oGroup2 = "4700000f-0000-4000-8000-000000000002"
)

type fakeResolver struct {
	got []string
	res search.ResolvedSource
	err error
}

func (f *fakeResolver) ResolveSource(_ context.Context, url string) (search.ResolvedSource, error) {
	f.got = append(f.got, url)
	return f.res, f.err
}

type offlineEnv struct {
	t   *testing.T
	db  *sql.DB
	h   http.Handler
	res *fakeResolver
}

func newOfflineEnv(t *testing.T) *offlineEnv {
	db := parityDB(t)
	res := &fakeResolver{res: search.ResolvedSource{Kind: "channel", ExternalID: "UCaaaaaaaaaaaaaaaaaaaaaa",
		URL: "https://www.youtube.com/channel/UCaaaaaaaaaaaaaaaaaaaaaa", Title: "Lessons"}}
	h := &Handler{DB: db, Hub: ws.NewHub(), NowFunc: time.Now, Sources: res}
	r := chi.NewRouter()
	h.Mount(r)
	// the API's chain: the token (here X-Test-As) sets the principal, RequireAuth
	// answers 401 without one, the package answers 403 to a non-admin
	auth := middleware.RequireAuth(r)
	front := http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		switch req.Header.Get("X-Test-As") {
		case "admin":
			req = req.WithContext(principal.WithPrincipal(req.Context(),
				&principal.Principal{UserID: oAdmin, IsAdmin: true, AccessAllLibraries: true}))
		case "viewer":
			req = req.WithContext(principal.WithPrincipal(req.Context(),
				&principal.Principal{UserID: oViewer, AccessAllLibraries: true}))
		}
		auth.ServeHTTP(w, req)
	})
	e := &offlineEnv{t: t, db: db, h: front, res: res}
	e.exec(`INSERT INTO users (id, username, pw_hash, is_admin) VALUES ($1, 'o-admin', 'x', true), ($2, 'o-viewer', 'x', false)`,
		oAdmin, oViewer)
	e.exec(`INSERT INTO libraries (id, name, roots) VALUES ($1, 'Alpha', ARRAY['/media/a']), ($2, 'Beta', ARRAY['/media/b'])`,
		oLibA, oLibB)
	e.exec(`INSERT INTO videos (id, library_id, content_hash, path, filename, size_bytes, mtime, title) VALUES
		($1, $4, repeat('1', 64), '/media/a/one.mkv', 'one.mkv', 10, now(), 'One'),
		($2, $4, repeat('2', 64), '/media/a/two.MP4', 'two.MP4', 10, now(), 'Two'),
		($3, $4, repeat('3', 64), '/media/a/three.webm', 'three.webm', 10, now(), 'Three')`, oMKV, oMP4, oOldMP4, oLibA)
	// what the worker's graph sync does once the Epic 47 stages ship
	e.exec(`UPDATE job_stages SET enabled = true, disabled_reason = NULL WHERE stage IN
		('convert_mp4','dedup_scan','dedup_apply','youtube_fetch','youtube_backfill')`)
	e.exec(`UPDATE job_stages SET subject_kinds = ARRAY['video','library'] WHERE stage IN ('convert_mp4','dedup_scan')`)
	return e
}

func (e *offlineEnv) exec(q string, args ...any) {
	e.t.Helper()
	if _, err := e.db.Exec(q, args...); err != nil {
		e.t.Fatalf("%s: %v", q, err)
	}
}

func (e *offlineEnv) count(q string, args ...any) int {
	e.t.Helper()
	var n int
	if err := e.db.QueryRow(q, args...).Scan(&n); err != nil {
		e.t.Fatalf("%s: %v", q, err)
	}
	return n
}

func (e *offlineEnv) do(who, method, path, body string) *httptest.ResponseRecorder {
	e.t.Helper()
	var req *http.Request
	if body == "" {
		req = httptest.NewRequest(method, path, nil)
	} else {
		req = httptest.NewRequest(method, path, strings.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
	}
	req.Header.Set("X-Test-As", who)
	rec := httptest.NewRecorder()
	e.h.ServeHTTP(rec, req)
	return rec
}

func (e *offlineEnv) want(status int, method, path, body string, out any) {
	e.t.Helper()
	rec := e.do("admin", method, path, body)
	if rec.Code != status {
		e.t.Fatalf("%s %s = %d %s, want %d", method, path, rec.Code, rec.Body.String(), status)
	}
	if out != nil {
		decode(e.t, rec, out)
	}
}

func (e *offlineEnv) audited(action, target string) {
	e.t.Helper()
	if n := e.count(`SELECT count(*) FROM audit_log WHERE category = 'admin' AND action = $1
		AND actor_user_id = $2 AND ($3 = '' OR target_id = $3)`, action, oAdmin, target); n == 0 {
		e.t.Errorf("no audit row %s on %q", action, target)
	}
}

type jobRow struct {
	id       int64
	state    string
	priority int
	force    bool
	payload  map[string]any
}

func (e *offlineEnv) job(stage, kind, subject string) jobRow {
	e.t.Helper()
	var j jobRow
	var payload []byte
	err := e.db.QueryRow(`SELECT id, state, priority, force, payload FROM processing_jobs
		WHERE stage = $1 AND subject_kind = $2 AND subject_id = $3 ORDER BY id DESC LIMIT 1`,
		stage, kind, subject).Scan(&j.id, &j.state, &j.priority, &j.force, &payload)
	if errors.Is(err, sql.ErrNoRows) {
		e.t.Fatalf("no %s job for %s %s", stage, kind, subject)
	}
	if err != nil {
		e.t.Fatal(err)
	}
	_ = json.Unmarshal(payload, &j.payload)
	return j
}

func TestOffline_EveryEndpointIsAdminOnly(t *testing.T) {
	e := newOfflineEnv(t)
	e.exec(`INSERT INTO youtube_sources (id, library_id, kind, url, external_id, mode, added_at)
		VALUES ('4700000e-0000-4000-8000-000000000001', $1, 'channel', 'https://www.youtube.com/channel/UCbbbbbbbbbbbbbbbbbbbbbb',
		        'UCbbbbbbbbbbbbbbbbbbbbbb', 'off', now())`, oLibA)
	e.exec(`INSERT INTO file_actions (id, library_id, video_id, kind, state, old_path, new_path, trash_path, old_size,
		purge_after) VALUES ($1, $2, $3, 'convert', 'done', '/media/a/x.mkv', '/media/a/one.mkv',
		'/media/a/.maktaba/trash/convert/2026-10-28/x.mkv', 5, now() + interval '30 days')`, oDone, oLibA, oMKV)
	e.exec(`INSERT INTO duplicate_groups (id, library_id, tier, status, members_key) VALUES ($1, $2, 'same_video', 'open', 'k')`,
		oGroup, oLibA)
	e.exec(`INSERT INTO duplicate_members (group_id, video_id, path) VALUES ($1, $2, '/media/a/one.mkv'), ($1, $3, '/media/a/two.MP4')`,
		oGroup, oMKV, oMP4)
	src := "/api/libraries/" + oLibA + "/youtube/sources/4700000e-0000-4000-8000-000000000001"
	for _, c := range []struct {
		method, path, body string
		ok                 int
	}{
		{"POST", "/api/libraries/" + oLibA + "/convert", `{"dry_run":true}`, 200},
		{"POST", "/api/libraries/" + oLibA + "/dedup-scan", `{}`, 200},
		{"POST", "/api/libraries/" + oLibA + "/youtube/refresh", "", 200},
		{"POST", "/api/libraries/" + oLibA + "/youtube/backfill", "", 200},
		{"POST", "/api/libraries/" + oLibA + "/youtube/retry", `{}`, 200},
		{"POST", "/api/libraries/" + oLibA + "/youtube/tartube", `{"path":"/Volumes/O/tartube_db_export.json"}`, 200},
		{"GET", "/api/libraries/" + oLibA + "/youtube/sources", "", 200},
		{"POST", "/api/libraries/" + oLibA + "/youtube/sources", `{"url":"https://www.youtube.com/@lessons","mode":"off"}`, 201},
		{"PATCH", src, `{"options":{"max_height":720}}`, 200},
		{"GET", "/api/libraries/" + oLibA + "/offline-summary", "", 200},
		{"GET", "/api/queues/offline-summary", "", 200},
		{"GET", "/api/queues/offline-items", "", 200},
		{"GET", "/api/queues/youtube-downloads", "", 200},
		{"GET", "/api/queues/file-actions", "", 200},
		{"POST", "/api/queues/file-actions/" + oDone + "/restore", "", 200},
		{"DELETE", src, "", 204},
		{"GET", "/api/queues/duplicates", "", 200},
		{"POST", "/api/queues/duplicates/" + oGroup + "/apply", `{"keeper_video_id":"` + oMP4 + `"}`, 200},
		{"POST", "/api/queues/duplicates/" + oGroup + "/dismiss", "", 200},
	} {
		if rec := e.do("", c.method, c.path, c.body); rec.Code != http.StatusUnauthorized {
			t.Errorf("signed out %s %s = %d", c.method, c.path, rec.Code)
		}
		if rec := e.do("viewer", c.method, c.path, c.body); rec.Code != http.StatusForbidden {
			t.Errorf("viewer %s %s = %d", c.method, c.path, rec.Code)
		}
		if rec := e.do("admin", c.method, c.path, c.body); rec.Code != c.ok {
			t.Errorf("admin %s %s = %d %s", c.method, c.path, rec.Code, rec.Body.String())
		}
	}
	// nothing the refused callers asked for was queued twice or audited for them
	if n := e.count(`SELECT count(*) FROM audit_log WHERE actor_user_id = $1`, oViewer); n != 0 {
		t.Errorf("%d audit rows for the viewer", n)
	}
	for _, a := range []string{"queue.library.convert", "queue.library.dedup_scan", "queue.library.youtube_refresh",
		"queue.library.youtube_backfill", "queue.library.youtube_retry", "queue.library.youtube_tartube",
		"queue.youtube_source.add", "queue.youtube_source.patch", "queue.youtube_source.delete",
		"queue.file_action.restore", "queue.duplicates.apply", "queue.duplicates.dismiss"} {
		e.audited(a, "")
	}
}

func TestOffline_LibraryActionsQueueThroughTheEnqueue(t *testing.T) {
	e := newOfflineEnv(t)
	var out LibraryActionResponse
	// a dry run first, then the real run: the job it coalesced into takes the last ask
	e.want(200, "POST", "/api/libraries/"+oLibA+"/convert", `{"dry_run":true}`, &out)
	if out.Stage != StageConvertMP4 || out.Enqueued != 1 || out.Results[0].Outcome != OutcomeInserted {
		t.Fatalf("convert = %+v", out)
	}
	j := e.job(StageConvertMP4, "library", oLibA)
	if j.priority != 150 || j.payload["dry_run"] != true {
		t.Fatalf("convert job = %+v", j)
	}
	e.want(200, "POST", "/api/libraries/"+oLibA+"/convert", "", &out)
	if out.Coalesced != 1 || e.job(StageConvertMP4, "library", oLibA).payload["dry_run"] != false {
		t.Fatalf("the real run did not replace the dry run: %+v %+v", out, e.job(StageConvertMP4, "library", oLibA))
	}
	e.audited("queue.library.convert", oLibA)

	// all: every library that is not deleted
	e.exec(`INSERT INTO libraries (name, roots, deleted_at) VALUES ('Gone', ARRAY['/x'], now())`)
	e.want(200, "POST", "/api/libraries/all/dedup-scan", `{"all":true,"dry_run":true}`, &out)
	if len(out.Results) != 2 || out.Enqueued != 2 {
		t.Fatalf("dedup all = %+v", out)
	}
	for _, lib := range []string{oLibA, oLibB} {
		if j := e.job(StageDedupScan, "library", lib); j.payload["scope"] != "all" || j.payload["dry_run"] != true || j.priority != 150 {
			t.Fatalf("dedup job of %s = %+v", lib, j)
		}
	}

	// a refresh is forced, at the reconciler's 110, with its options
	e.want(200, "POST", "/api/libraries/"+oLibB+"/youtube/refresh", `{"full_listing":true}`, &out)
	if j := e.job(StageYouTubeSync, "library", oLibB); !j.force || j.priority != 110 || j.payload["full_listing"] != true {
		t.Fatalf("refresh job = %+v", j)
	}
	// Tartube rides the same job (a pending one takes it)
	e.want(200, "POST", "/api/libraries/"+oLibB+"/youtube/tartube",
		`{"path":"/Volumes/Originals/Backup/Youtube/tartube_db_export.json","channels":["Lessons"]}`, &out)
	tj := e.job(StageYouTubeSync, "library", oLibB)
	tt, _ := tj.payload["tartube"].(map[string]any)
	if out.Coalesced != 1 || tt["path"] != "/Volumes/Originals/Backup/Youtube/tartube_db_export.json" || tj.payload["full_listing"] != true {
		t.Fatalf("tartube = %+v %+v", out, tj)
	}
	for _, body := range []string{`{"path":"relative.json"}`, `{"path":"/x/tartube.txt"}`, `{}`} {
		if rec := e.do("admin", "POST", "/api/libraries/"+oLibB+"/youtube/tartube", body); rec.Code != 422 {
			t.Errorf("tartube %s = %d", body, rec.Code)
		}
	}

	// the back catalogue resumes at 300
	e.want(200, "POST", "/api/libraries/"+oLibA+"/youtube/backfill", "", &out)
	if j := e.job(StageYouTubeBackfill, "library", oLibA); j.priority != 300 {
		t.Fatalf("backfill = %+v", j)
	}

	// unknown library, bad id
	if rec := e.do("admin", "POST", "/api/libraries/"+oViewer+"/convert", ""); rec.Code != 404 {
		t.Errorf("unknown library = %d", rec.Code)
	}
	if rec := e.do("admin", "POST", "/api/libraries/nope/convert", ""); rec.Code != 400 {
		t.Errorf("bad id = %d", rec.Code)
	}
	// a stage the worker has not turned on yet is reported, not queued
	e.exec(`UPDATE job_stages SET enabled = false, disabled_reason = 'not_configured' WHERE stage = 'convert_mp4'`)
	e.want(200, "POST", "/api/libraries/"+oLibB+"/convert", "", &out)
	if out.Skipped != 1 || out.Results[0].Outcome != OutcomeDisabled {
		t.Fatalf("disabled stage = %+v", out)
	}
}

func TestOffline_RetryDownloads(t *testing.T) {
	e := newOfflineEnv(t)
	e.exec(`INSERT INTO youtube_downloads (library_id, youtube_id, state, backfill, reason, failure_class, error, attempts) VALUES
		($1, 'aaaaaaaaaa1', 'gone', false, 'removed', 'gone', 'Video unavailable', 1),
		($1, 'aaaaaaaaaa2', 'skipped', true, 'members_only', 'skipped', NULL, 1),
		($1, 'aaaaaaaaaa3', 'failed', true, 'verify', 'convert_verify', 'x', 2),
		($1, 'aaaaaaaaaa4', 'done', false, NULL, NULL, NULL, 1),
		($2, 'bbbbbbbbbb1', 'gone', false, NULL, NULL, NULL, 1)`, oLibA, oLibB)
	var out RetryDownloadsResponse
	e.want(200, "POST", "/api/libraries/"+oLibA+"/youtube/retry", `{"states":["gone","failed"]}`, &out)
	if out.Wanted != 2 || out.New != 1 || out.Backlog != 1 || len(out.Results) != 2 {
		t.Fatalf("retry = %+v", out)
	}
	if n := e.count(`SELECT count(*) FROM youtube_downloads WHERE state = 'wanted' AND reason IS NULL AND attempts = 0
		AND failure_class IS NULL AND error IS NULL`); n != 2 {
		t.Fatalf("%d rows wanted again", n)
	}
	if e.job(StageYouTubeFetch, "library", oLibA).priority != 120 || e.job(StageYouTubeBackfill, "library", oLibA).priority != 300 {
		t.Fatal("fetch/backfill priorities")
	}
	if n := e.count(`SELECT count(*) FROM youtube_downloads WHERE state = 'skipped'`); n != 1 {
		t.Fatal("a state not asked for was retried")
	}
	if rec := e.do("admin", "POST", "/api/libraries/"+oLibA+"/youtube/retry", `{"states":["done"]}`); rec.Code != 400 {
		t.Errorf("retry done = %d", rec.Code)
	}
	var list struct {
		Items []YouTubeDownload `json:"items"`
		Next  *string           `json:"next"`
	}
	e.want(200, "GET", "/api/queues/youtube-downloads?library_id="+oLibA+"&state=skipped,wanted&limit=2", "", &list)
	if len(list.Items) != 2 || list.Next == nil || list.Items[0].LibraryName != "Alpha" {
		t.Fatalf("downloads page 1 = %+v", list)
	}
	first := append([]YouTubeDownload(nil), list.Items...)
	e.want(200, "GET", "/api/queues/youtube-downloads?library_id="+oLibA+"&state=skipped,wanted&limit=2&cursor="+*list.Next, "", &list)
	if len(list.Items) != 1 || list.Next != nil || list.Items[0].YouTubeID == first[0].YouTubeID || list.Items[0].YouTubeID == first[1].YouTubeID {
		t.Fatalf("downloads page 2 = %+v", list)
	}
}

func TestOffline_Sources(t *testing.T) {
	e := newOfflineEnv(t)
	base := "/api/libraries/" + oLibA + "/youtube/sources"
	var w SourceWriteResponse
	e.want(201, "POST", base, `{"url":"https://www.youtube.com/@lessons","options":{"max_height":720,"sub_langs":["ar","en"]}}`, &w)
	if w.Source.Kind != "channel" || w.Source.ExternalID != "UCaaaaaaaaaaaaaaaaaaaaaa" || w.Source.Mode != ModeNewAndBackfill ||
		w.Refresh == nil || w.Refresh.Outcome != OutcomeInserted {
		t.Fatalf("add = %+v", w)
	}
	var opts map[string]any
	_ = json.Unmarshal(w.Source.Options, &opts)
	if opts["max_height"] != 720.0 || opts["sub_langs"] != "ar,en" {
		t.Fatalf("options = %v", opts)
	}
	if e.res.got[0] != "https://www.youtube.com/@lessons" {
		t.Fatalf("resolver got %v", e.res.got)
	}
	if j := e.job(StageYouTubeSync, "library", oLibA); j.priority != 110 || !j.force {
		t.Fatalf("refresh = %+v", j)
	}
	// the same channel again
	if rec := e.do("admin", "POST", base, `{"url":"https://www.youtube.com/channel/UCaaaaaaaaaaaaaaaaaaaaaa"}`); rec.Code != 409 {
		t.Fatalf("duplicate = %d", rec.Code)
	}
	// a playlist, off: no refresh
	e.res.res = search.ResolvedSource{Kind: "playlist", ExternalID: "PLxxxxxxxxxx", URL: "https://www.youtube.com/playlist?list=PLxxxxxxxxxx"}
	e.exec(`DELETE FROM processing_jobs WHERE stage = 'youtube_sync'`)
	e.want(201, "POST", base, `{"url":"https://www.youtube.com/playlist?list=PLxxxxxxxxxx","mode":"off","folder":"Playlists/Seerah"}`, &w)
	if w.Refresh != nil || *w.Source.Folder != "Playlists/Seerah" {
		t.Fatalf("add off = %+v", w)
	}
	pid := w.Source.ID
	// options checked as the pipeline reads them; the URL never changes
	for _, body := range []string{`{"options":{"max_height":100}}`, `{"options":{"cookies_from_browser":"fire fox;rm"}}`,
		`{"options":{"bogus":1}}`, `{"mode":"sometimes"}`, `{"folder":"../etc"}`, `{"url":"https://x"}`} {
		if rec := e.do("admin", "PATCH", base+"/"+pid, body); rec.Code != 422 {
			t.Errorf("patch %s = %d", body, rec.Code)
		}
	}
	// turned on: a refresh
	e.want(200, "PATCH", base+"/"+pid, `{"mode":"new_only","options":{"audio_only":true,"cookies_from_browser":"firefox"}}`, &w)
	if w.Source.Mode != ModeNewOnly || w.Refresh == nil {
		t.Fatalf("turn on = %+v", w)
	}
	// downloads per source
	e.exec(`INSERT INTO youtube_downloads (library_id, youtube_id, source_id, state, backfill) VALUES
		($1, 'p1', $2, 'wanted', true), ($1, 'p2', $2, 'done', false), ($1, 'p3', $2, 'failed', false)`, oLibA, pid)
	var list struct {
		Items []YouTubeSource `json:"items"`
	}
	e.want(200, "GET", base, "", &list)
	if len(list.Items) != 2 {
		t.Fatalf("sources = %+v", list)
	}
	for _, s := range list.Items {
		if s.ID == pid && (s.Downloads.Backfill != 1 || s.Downloads.Done != 1 || s.Downloads.Failed != 1) {
			t.Fatalf("playlist downloads = %+v", s.Downloads)
		}
	}
	// a URL that is not a source; yt-dlp missing
	e.res.err = search.ErrNotASource
	if rec := e.do("admin", "POST", base, `{"url":"https://www.youtube.com/watch?v=x"}`); rec.Code != 422 {
		t.Errorf("not a source = %d", rec.Code)
	}
	e.res.err = search.ErrYouTubeDisabled
	if rec := e.do("admin", "POST", base, `{"url":"https://www.youtube.com/@y"}`); rec.Code != 503 {
		t.Errorf("no yt-dlp = %d", rec.Code)
	}
	// delete: its downloads stay, without a source
	e.want(204, "DELETE", base+"/"+pid, "", nil)
	if n := e.count(`SELECT count(*) FROM youtube_downloads WHERE youtube_id LIKE 'p_' AND source_id IS NULL`); n != 3 {
		t.Fatalf("%d downloads kept", n)
	}
	if rec := e.do("admin", "DELETE", base+"/"+pid, ""); rec.Code != 404 {
		t.Errorf("delete again = %d", rec.Code)
	}
	// another library's source is not this library's
	if rec := e.do("admin", "PATCH", "/api/libraries/"+oLibB+"/youtube/sources/"+list.Items[0].ID, `{"mode":"off"}`); rec.Code != 404 {
		t.Errorf("cross-library patch = %d", rec.Code)
	}
}

func TestOffline_SummaryAndItems(t *testing.T) {
	e := newOfflineEnv(t)
	e.exec(`INSERT INTO youtube_sources (library_id, kind, url, external_id, mode, added_at, status, error, last_checked_at) VALUES
		($1, 'channel', 'u1', 'UC1', 'new_and_backfill', now(), 'ok', NULL, '2026-10-27T10:00:00Z'),
		($1, 'playlist', 'u2', 'PL2', 'new_only', now(), 'bot_check', 'Sign in to confirm', '2026-10-28T10:00:00Z'),
		($1, 'channel', 'u3', 'UC3', 'off', now(), NULL, NULL, NULL)`, oLibA)
	e.exec(`INSERT INTO youtube_downloads (library_id, youtube_id, state, backfill) VALUES
		($1, 'n1', 'wanted', false), ($1, 'b1', 'wanted', true), ($1, 'b2', 'wanted', true),
		($1, 'd1', 'done', false), ($1, 'f1', 'failed', false), ($1, 'g1', 'gone', true), ($1, 'w1', 'downloading', false)`, oLibA)
	e.exec(`INSERT INTO duplicate_groups (library_id, tier, status, members_key) VALUES
		($1, 'exact', 'open', 'k1'), ($1, 'same_video', 'open', 'k2'), ($1, 'exact', 'dismissed', 'k3'),
		(NULL, 'same_video', 'open', 'k4')`, oLibA)
	e.exec(`INSERT INTO file_actions (library_id, kind, state, old_path, trash_path, old_size, purge_after) VALUES
		($1, 'convert', 'done', '/a', '/t/a', 100, '2026-11-20T00:00:00Z'),
		($1, 'dedup', 'done', '/b', '/t/b', 50, '2026-11-10T00:00:00Z'),
		($1, 'convert', 'failed', '/c', NULL, 70, NULL)`, oLibA)
	var s OfflineSummary
	e.want(200, "GET", "/api/libraries/"+oLibA+"/offline-summary", "", &s)
	y := s.YouTube
	if y.Sources != 3 || y.SourcesOn != 2 || y.Wanted != 1 || y.Backfill != 2 || y.Downloaded != 1 || y.Failed != 1 ||
		y.Gone != 1 || y.Downloading != 1 || y.LastRefresh == nil || !y.LastRefresh.Equal(time.Date(2026, 10, 28, 10, 0, 0, 0, time.UTC)) ||
		len(y.Problems) != 1 || y.Problems[0].Status != "bot_check" {
		t.Fatalf("youtube = %+v", y)
	}
	// one.mkv and three.webm; two.MP4 is an MP4 whatever its case
	if s.NotMP4 != 2 || s.Duplicates != 2 || s.Trash.Entries != 2 || s.Trash.Bytes != 150 ||
		!s.Trash.NextPurgeAt.Equal(time.Date(2026, 11, 10, 0, 0, 0, 0, time.UTC)) {
		t.Fatalf("summary = %+v", s)
	}
	var all struct {
		Items []OfflineSummary `json:"items"`
		Cross int              `json:"cross_library_duplicates"`
	}
	e.want(200, "GET", "/api/queues/offline-summary", "", &all)
	if len(all.Items) != 2 || all.Cross != 1 || all.Items[1].NotMP4 != 0 {
		t.Fatalf("all = %+v", all)
	}
	if rec := e.do("admin", "GET", "/api/libraries/"+oViewer+"/offline-summary", ""); rec.Code != 404 {
		t.Errorf("unknown library summary = %d", rec.Code)
	}

	// per item: a conversion queued, a check that waits for its conversion, a failed check
	enq := func(kind, id, stage string) int64 {
		var job int64
		if err := e.db.QueryRow(`SELECT job_id FROM processing_jobs_enqueue($1, $2::uuid, $3, NULL, false, false,
			'{}'::jsonb, NULL, NULL, NULL)`, kind, id, stage).Scan(&job); err != nil {
			t.Fatalf("enqueue %s: %v", stage, err)
		}
		return job
	}
	conv := enq("video", oMKV, StageConvertMP4)
	wait := enq("video", oMP4, StageDedupScan)
	e.exec(`UPDATE processing_jobs SET state = 'done', outcome = 'unchanged', finished_at = now(),
		output_summary = '{"skipped":"not_converted"}' WHERE id = $1`, wait)
	failed := enq("video", oOldMP4, StageDedupScan)
	e.exec(`UPDATE processing_jobs SET state = 'failed', finished_at = now(),
		error = '{"kind":"keeper_unreadable","message":"ffprobe could not read the keeper"}' WHERE id = $1`, failed)
	done := enq("library", oLibA, StageDedupScan)
	e.exec(`UPDATE processing_jobs SET state = 'done', finished_at = now() WHERE id = $1`, done)
	var items struct {
		Items []OfflineItem `json:"items"`
	}
	e.want(200, "GET", "/api/queues/offline-items?library_id="+oLibA, "", &items)
	got := map[int64]OfflineItem{}
	for _, it := range items.Items {
		got[it.ID] = it
	}
	if len(got) != 3 || got[conv].Step != StepConvert || got[conv].SubjectLabel != "One" || !got[wait].Waiting ||
		got[failed].Error == nil || *got[failed].Error != "ffprobe could not read the keeper" {
		t.Fatalf("items = %+v", items.Items)
	}
	e.want(200, "GET", "/api/queues/offline-items?step=convert", "", &items)
	if len(items.Items) != 1 || items.Items[0].ID != conv {
		t.Fatalf("convert items = %+v", items.Items)
	}
	if rec := e.do("admin", "GET", "/api/queues/offline-items?step=nope", ""); rec.Code != 400 {
		t.Errorf("bad step = %d", rec.Code)
	}
}

func TestOffline_FileActionsAndRestore(t *testing.T) {
	e := newOfflineEnv(t)
	e.exec(`INSERT INTO file_actions (id, library_id, video_id, kind, state, old_path, new_path, trash_path, old_size,
		purge_after, purged_at, error, verify, created_at) VALUES
		($1, $5, $6, 'convert', 'done', '/media/a/one.mkv', '/media/a/one.mp4', '/media/a/.maktaba/trash/convert/d/one.mkv', 10,
		 now() + interval '30 days', NULL, NULL, NULL, now() - interval '1 minute'),
		($2, $5, $6, 'convert', 'done', '/media/a/old.mkv', '/media/a/old.mp4', '/media/a/.maktaba/trash/convert/d/old.mkv', 10,
		 now() - interval '1 day', now(), NULL, NULL, now() - interval '2 minutes'),
		($3, $5, NULL, 'youtube_fetch', 'done', NULL, '/media/a/new.mp4', NULL, NULL, NULL, NULL, NULL, NULL, now() - interval '3 minutes'),
		($4, $5, $6, 'convert', 'failed', '/media/a/one.mkv', NULL, NULL, 10, NULL, NULL, 'frames differ',
		 '{"ok":false,"reasons":["frames"]}', now() - interval '4 minutes')`,
		oDone, oPurged, oFetch, oFailed, oLibA, oMKV)
	type page struct {
		Items []FileAction   `json:"items"`
		Next  *string        `json:"next"`
		Trash []LibraryTrash `json:"trash"`
	}
	var p page
	e.want(200, "GET", "/api/queues/file-actions?limit=3", "", &p)
	if len(p.Items) != 3 || p.Next == nil || p.Items[0].ID != oDone || !p.Items[0].Restorable || p.Items[1].Restorable ||
		p.Items[2].Restorable || *p.Items[0].VideoTitle != "One" || p.Items[0].LibraryName != "Alpha" {
		t.Fatalf("page 1 = %+v", p.Items)
	}
	if len(p.Trash) != 1 || p.Trash[0].Entries != 1 || p.Trash[0].Bytes != 10 || p.Trash[0].NextPurgeAt == nil {
		t.Fatalf("trash = %+v", p.Trash)
	}
	e.want(200, "GET", "/api/queues/file-actions?limit=3&cursor="+*p.Next, "", &p)
	if len(p.Items) != 1 || p.Items[0].ID != oFailed || p.Next != nil || string(p.Items[0].Verify) == "null" {
		t.Fatalf("page 2 = %+v", p.Items)
	}
	e.want(200, "GET", "/api/queues/file-actions?kind=convert&state=failed,done&restorable=true", "", &p)
	if len(p.Items) != 1 || p.Items[0].ID != oDone {
		t.Fatalf("filtered = %+v", p.Items)
	}
	for _, q := range []string{"kind=nope", "state=nope", "library_id=x", "cursor=abc", "limit=0"} {
		if rec := e.do("admin", "GET", "/api/queues/file-actions?"+q, ""); rec.Code != 400 {
			t.Errorf("file-actions?%s = %d", q, rec.Code)
		}
	}

	// Restore queues file_restore for the library; a second one joins it
	var r RestoreResponse
	e.want(200, "POST", "/api/queues/file-actions/"+oDone+"/restore", "", &r)
	if r.Outcome != OutcomeInserted || r.JobID == nil {
		t.Fatalf("restore = %+v", r)
	}
	j := e.job(StageFileRestore, "library", oLibA)
	if j.priority != 50 || !j.force {
		t.Fatalf("restore job = %+v", j)
	}
	e.audited("queue.file_action.restore", oDone)
	// a row whose restore is queued says so
	e.want(200, "GET", "/api/queues/file-actions?restorable=true", "", &p)
	if !p.Items[0].RestoreQueued {
		t.Fatalf("restore not shown as queued: %+v", p.Items[0])
	}
	// a second restorable row of the library joins the queued job
	second := "4700000d-0000-4000-8000-000000000005"
	e.exec(`INSERT INTO file_actions (id, library_id, kind, state, old_path, trash_path, old_size, purge_after)
		VALUES ($1, $2, 'dedup', 'done', '/media/a/dup.mkv', '/media/a/.maktaba/trash/dedup/d/dup.mkv', 3, now() + interval '1 day')`,
		second, oLibA)
	e.want(200, "POST", "/api/queues/file-actions/"+second+"/restore", "", &r)
	j = e.job(StageFileRestore, "library", oLibA)
	ids, _ := j.payload["action_ids"].([]any)
	if r.Outcome != OutcomeCoalesced || len(ids) != 2 {
		t.Fatalf("second restore = %+v, payload %v", r, j.payload)
	}
	// the same row twice stays once
	e.want(200, "POST", "/api/queues/file-actions/"+oDone+"/restore", "", &r)
	if ids, _ := e.job(StageFileRestore, "library", oLibA).payload["action_ids"].([]any); len(ids) != 2 {
		t.Fatalf("ids = %v", ids)
	}
	// purged, nothing in the trash, failed, unknown
	for id, want := range map[string]int{oPurged: 409, oFetch: 409, oFailed: 409, oViewer: 404} {
		if rec := e.do("admin", "POST", "/api/queues/file-actions/"+id+"/restore", ""); rec.Code != want {
			t.Errorf("restore %s = %d, want %d", id, rec.Code, want)
		}
	}
}

// The failures page words the new kinds and links the ledger row.
func TestOffline_FailuresShowTheNewKinds(t *testing.T) {
	e := newOfflineEnv(t)
	var job int64
	if err := e.db.QueryRow(`SELECT job_id FROM processing_jobs_enqueue('video', $1::uuid, 'convert_mp4', 150, false, false,
		'{}'::jsonb, NULL, NULL, NULL)`, oMKV).Scan(&job); err != nil {
		t.Fatal(err)
	}
	e.exec(`UPDATE processing_jobs SET state = 'failed', attempts = 2, finished_at = now(), failure_class = 'unknown',
		error = '{"kind":"convert_verify","message":"frames differ"}', error_cause = 'convert_verify:frames differ' WHERE id = $1`, job)
	e.exec(`INSERT INTO file_actions (id, library_id, video_id, job_id, kind, state, old_path, error, verify)
		VALUES ($1, $2, $3, $4, 'convert', 'failed', '/media/a/one.mkv', 'frames differ', '{"ok":false}')`, oFailed, oLibA, oMKV, job)
	var list struct {
		Items []FailureItem `json:"items"`
	}
	e.want(200, "GET", "/api/queues/failures", "", &list)
	if len(list.Items) != 1 || list.Items[0].Reason != "convert_verify" || list.Items[0].FileAction == nil ||
		list.Items[0].FileAction.ID != oFailed || list.Items[0].FileAction.Restorable {
		t.Fatalf("failures = %+v", list.Items)
	}
}

func TestOffline_Duplicates(t *testing.T) {
	e := newOfflineEnv(t)
	e.exec(`INSERT INTO duplicate_groups (id, library_id, tier, status, members_key, created_at) VALUES
		($1, $3, 'same_video', 'open', 'k1', now() - interval '1 hour'),
		($2, NULL, 'exact', 'open', 'k2', now())`, oGroup, oGroup2, oLibA)
	e.exec(`INSERT INTO duplicate_members (group_id, video_id, path, is_copy, score) VALUES
		($1, $3, '/media/a/one.mkv', false, '{"pixels":2073600}'), ($1, $4, '/media/a/two.MP4', false, NULL),
		($2, $3, '/media/a/one.mkv', false, NULL), ($2, $3, '/media/a/copy/one.mkv', true, NULL)`, oGroup, oGroup2, oMKV, oMP4)
	e.exec(`INSERT INTO media_info (video_id, container, video_codec, width, height, bitrate_kbps) VALUES ($1, 'matroska', 'h264', 1920, 1080, 4000)`, oMKV)
	e.exec(`INSERT INTO playback_state (user_id, video_id, position_sec, completed) VALUES ($1, $2, 30, true)`, oAdmin, oMKV)
	type page struct {
		Items []DuplicateGroup `json:"items"`
		Next  *string          `json:"next"`
	}
	var p page
	e.want(200, "GET", "/api/queues/duplicates?limit=1", "", &p)
	if len(p.Items) != 1 || p.Items[0].ID != oGroup2 || p.Next == nil || p.Items[0].LibraryID != nil ||
		len(p.Items[0].Members) != 2 || !p.Items[0].Members[1].IsCopy {
		t.Fatalf("page 1 = %+v", p)
	}
	var next page
	e.want(200, "GET", "/api/queues/duplicates?limit=1&cursor="+*p.Next, "", &next)
	if len(next.Items) != 1 || next.Items[0].ID != oGroup || next.Next != nil {
		t.Fatalf("page 2 = %+v", next)
	}
	var p2 page
	e.want(200, "GET", "/api/queues/duplicates?library_id="+oLibA, "", &p2)
	g := p2.Items[0]
	if g.ID != oGroup || *g.LibraryName != "Alpha" || len(g.Members) != 2 {
		t.Fatalf("library page = %+v", p2)
	}
	m := g.Members[0]
	if m.VideoID != oMKV || *m.Width != 1920 || *m.VideoCodec != "h264" || m.Viewers != 1 || m.Completed != 1 ||
		!strings.Contains(string(m.Score), "2073600") {
		t.Fatalf("member = %+v %s", m, m.Score)
	}
	var cross page
	e.want(200, "GET", "/api/queues/duplicates?library_id=cross", "", &cross)
	if len(cross.Items) != 1 || cross.Items[0].ID != oGroup2 {
		t.Fatalf("cross = %+v", cross.Items)
	}
	for _, q := range []string{"status=x", "tier=x", "library_id=x", "cursor=abc"} {
		if rec := e.do("admin", "GET", "/api/queues/duplicates?"+q, ""); rec.Code != 400 {
			t.Errorf("duplicates?%s = %d", q, rec.Code)
		}
	}

	// Apply with a keeper: the group's keeper, and an approved dedup_apply at 150
	var out map[string]any
	if rec := e.do("admin", "POST", "/api/queues/duplicates/"+oGroup+"/apply", `{"keeper_video_id":"`+oOldMP4+`"}`); rec.Code != 422 {
		t.Fatalf("a keeper that is no member = %d", rec.Code)
	}
	e.want(200, "POST", "/api/queues/duplicates/"+oGroup+"/apply", `{"keeper_video_id":"`+oMP4+`"}`, &out)
	j := e.job(StageDedupApply, "dup_group", oGroup)
	if j.priority != 150 || !j.force || j.payload["apply"] != true || j.payload["group_id"] != oGroup ||
		j.payload["keeper_video_id"] != oMP4 || j.payload["decided_by"] != "user:"+oAdmin {
		t.Fatalf("apply job = %+v", j)
	}
	if n := e.count(`SELECT count(*) FROM duplicate_groups WHERE id = $1 AND keeper_video_id = $2`, oGroup, oMP4); n != 1 {
		t.Fatal("keeper not recorded")
	}
	// applied again without a keeper: one approval, the keeper the group now records
	out = nil
	e.want(200, "POST", "/api/queues/duplicates/"+oGroup+"/apply", "", &out)
	if j := e.job(StageDedupApply, "dup_group", oGroup); out["outcome"] != OutcomeCoalesced || j.payload["keeper_video_id"] != nil {
		t.Fatalf("second apply = %v %+v", out, j)
	}
	p2 = page{}
	e.want(200, "GET", "/api/queues/duplicates?library_id="+oLibA, "", &p2)
	if p2.Items[0].Job == nil || p2.Items[0].Job.State != "pending" || p2.Items[0].Job.SubjectLabel != "Two" ||
		*p2.Items[0].KeeperVideoID != oMP4 {
		t.Fatalf("group job = %+v", p2.Items[0])
	}

	// Dismiss: remembered, and no longer open
	e.want(200, "POST", "/api/queues/duplicates/"+oGroup2+"/dismiss", "", &out)
	if n := e.count(`SELECT count(*) FROM duplicate_groups WHERE id = $1 AND status = 'dismissed' AND decided_by = $2
		AND decided_at IS NOT NULL`, oGroup2, "user:"+oAdmin); n != 1 {
		t.Fatal("not dismissed")
	}
	for path, want := range map[string]int{"/" + oGroup2 + "/dismiss": 409, "/" + oGroup2 + "/apply": 409,
		"/" + oViewer + "/apply": 404, "/nope/apply": 400} {
		if rec := e.do("admin", "POST", "/api/queues/duplicates"+path, ""); rec.Code != want {
			t.Errorf("%s = %d, want %d", path, rec.Code, want)
		}
	}
	e.audited("queue.duplicates.apply", oGroup)
	e.audited("queue.duplicates.dismiss", oGroup2)
}
