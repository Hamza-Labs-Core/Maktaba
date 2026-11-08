package statements

// README §15.12: a first-party plugin's write, and its read by id, is checked against the
// job, not bound to it by the plugin's own code. These tests seed one row of every kind the
// API resolves in two libraries and walk every statement of the embedded bundle: for each
// check it declares, another library's id is refused with 403 plugin_scope before any SQL
// runs, and the job's own id is allowed.

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"regexp"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/Hamza-Labs-Core/Maktaba/api/internal/cluster/clustertest"
	sharedplugins "github.com/Hamza-Labs-Core/Maktaba/shared/plugins"
)

// lib is one library's rows: an id for every kind of check.
type lib struct {
	ids   map[string]string // kind -> an id of that kind in this library
	video string            // a second video of the library (a video job's other video)
}

func seedLibrary(t *testing.T, db *sql.DB, tag string) lib {
	t.Helper()
	l := lib{ids: map[string]string{}}
	one := func(dst *string, q string, a ...any) {
		t.Helper()
		if err := db.QueryRow(q, a...).Scan(dst); err != nil {
			t.Fatalf("%s: %v", q, err)
		}
	}
	var library, video, other, series, season, track, transcript, segment string
	one(&library, `INSERT INTO libraries (name, roots) VALUES ($1, '{}') RETURNING id::text`, "L-"+tag)
	newVideo := func(dst *string, n int) {
		path := fmt.Sprintf("/m/%s-%d.mp4", tag, n)
		one(dst, `INSERT INTO videos (library_id, path, filename, size_bytes, mtime, content_hash)
			VALUES ($1, $2, $2, 1, now(), md5($2) || md5($2)) RETURNING id::text`, library, path)
	}
	newVideo(&video, 1)
	newVideo(&other, 2)
	one(&series, `INSERT INTO series (library_id, name) VALUES ($1, $2) RETURNING id::text`, library, "S-"+tag)
	one(&season, `INSERT INTO series_seasons (id, series_id, library_id, season, kind, name, membership_hash, detector_version)
		VALUES (gen_random_uuid(), $1, $2, 1, 'episodic', 'one', 'h', 1) RETURNING id::text`, series, library)
	one(&track, `INSERT INTO audio_tracks (video_id, track_index) VALUES ($1, 0) RETURNING id::text`, video)
	one(&transcript, `INSERT INTO transcripts (video_id, audio_track_id, language, backend, model)
		VALUES ($1, $2, 'en', 'b', 'm') RETURNING id::text`, video, track)
	one(&segment, `INSERT INTO transcript_segments (transcript_id, seq, start_sec, end_sec, text)
		VALUES ($1, 0, 0, 1, 'hi') RETURNING id::text`, transcript)
	l.ids["library"], l.ids["subject"], l.video = library, video, other
	l.ids["series"], l.ids["season"], l.ids["audio_track"] = series, season, track
	l.ids["transcript"], l.ids["segment"] = transcript, segment
	for kind, q := range map[string]string{
		"clip_group": `INSERT INTO clip_groups (id, library_id, kind, basis, name, title, membership_hash)
			VALUES (gen_random_uuid(), $1, 'clips', 'topic', 'n', 't', 'h') RETURNING id::text`,
		"topic_collection": `INSERT INTO topic_collections (id, library_id, playlist_id, name, membership_hash)
			VALUES (gen_random_uuid(), $1, 'p', 'n', 'h') RETURNING id::text`,
		"dup_group": `INSERT INTO duplicate_groups (library_id, tier, members_key) VALUES ($1, 'exact', 'k') RETURNING id::text`,
		"youtube_source": `INSERT INTO youtube_sources (library_id, kind, url, external_id)
			VALUES ($1, 'playlist', 'u', 'PL-` + tag + `') RETURNING id::text`,
	} {
		var id string
		one(&id, q, library)
		l.ids[kind] = id
	}
	var fa string
	one(&fa, `INSERT INTO file_actions (library_id, video_id, kind) VALUES ($1, $2, 'convert') RETURNING id::text`, library, video)
	l.ids["file_action"] = fa
	var coll, job string
	one(&coll, `INSERT INTO collections (library_id, name) VALUES ($1, 'c') RETURNING id::text`, library)
	one(&job, `INSERT INTO processing_jobs (stage, state, subject_kind, subject_id, video_id, library_id)
		VALUES ('probe', 'done', 'video', $1, $1, $2) RETURNING id::text`, video, library)
	// a job's subject: here the library's season (a video, a library or a series resolve too)
	l.ids["collection"], l.ids["job"], l.ids["job_subject"] = coll, job, season
	var ch string
	one(&ch, `INSERT INTO youtube_channels (channel_id, library_id, url) VALUES ($1, $2, 'u') RETURNING channel_id`, "UC-"+tag, library)
	var pl, yv, ac string
	one(&pl, `INSERT INTO youtube_playlists (playlist_id, channel_id, title, is_real) VALUES ($1, $2, 't', true) RETURNING playlist_id`, "PLX-"+tag, ch)
	one(&yv, `INSERT INTO youtube_videos (youtube_id, channel_id, title) VALUES ($1, $2, 't') RETURNING youtube_id`, "yt-"+tag, ch)
	one(&ac, `INSERT INTO audio_cache (content_hash, video_id, audio_track_id, path) VALUES ($1, $2, $3, '/c') RETURNING content_hash`,
		"hash-"+tag, video, track)
	l.ids["youtube_channel"], l.ids["youtube_playlist"], l.ids["youtube_video"], l.ids["audio_cache"] = ch, pl, yv, ac
	return l
}

type bundle struct {
	Plugins []struct {
		ID         string      `json:"id"`
		Statements []Statement `json:"statements"`
	} `json:"plugins"`
}

func embedded(t *testing.T) bundle {
	t.Helper()
	var b bundle
	if err := json.Unmarshal(sharedplugins.FirstParty, &b); err != nil {
		t.Fatal(err)
	}
	return b
}

var paramRe = regexp.MustCompile(`\$([0-9]+)`)

func maxParam(text string) int {
	n := 0
	for _, m := range paramRe.FindAllStringSubmatch(text, -1) {
		if v, _ := strconv.Atoi(m[1]); v > n {
			n = v
		}
	}
	return n
}

// argFor is a value of a kind as a plugin's code sends it (a bigint id as a number).
func argFor(kind, id string) any {
	if rowKinds[kind].key == "int" {
		n, _ := strconv.ParseInt(id, 10, 64)
		return n
	}
	return id
}

func TestEveryStatementOfTheBundleChecksEachIdItIsGiven(t *testing.T) {
	db, _ := clustertest.ScratchDB(t)
	tag := fmt.Sprint(time.Now().UnixNano())
	a, b := seedLibrary(t, db, "a"+tag), seedLibrary(t, db, "b"+tag)
	e := New(db)
	ctx := context.Background()
	checked := map[string]int{}
	for _, p := range embedded(t).Plugins {
		if err := e.Register(p.ID, p.Statements); err != nil {
			t.Fatalf("%s: %v", p.ID, err)
		}
		for i := range p.Statements {
			st := &p.Statements[i]
			for _, c := range st.Checks() {
				checked[st.Mode]++
				if c.Scope == "settings" {
					continue
				}
				job := Scope{Invocation: "inv_" + tag, PluginID: p.ID, SubjectKind: "library",
					SubjectID: a.ids["library"], LibraryID: a.ids["library"]}
				call := func(id string) *Error {
					args := make([]any, maxParam(st.Text))
					args[c.Arg-1] = argFor(c.Scope, id)
					return e.CheckScopes(ctx, job, st, args)
				}
				if err := call(b.ids[c.Scope]); err == nil || err.Status != 403 || err.Code != CodeScope {
					t.Errorf("%s %s (%s): %s $%d of another library: %v, want 403 %s", p.ID, st.Name, st.Mode, c.Scope, c.Arg, err, CodeScope)
				}
				if err := call(a.ids[c.Scope]); err != nil {
					t.Errorf("%s %s (%s): %s $%d of the job's library: %v", p.ID, st.Name, st.Mode, c.Scope, c.Arg, err)
				}
			}
		}
	}
	if checked["write"] < 100 || checked["read"] < 200 {
		t.Fatalf("only %v checks on the bundle's statements: the walk is not reading it", checked)
	}
}

// A read by id is refused before its SQL runs: another library's season, transcript,
// collection, job or job subject, and another video of the library from a video job.
func TestAReadByIdOfAnotherScopeIsRefused(t *testing.T) {
	db, _ := clustertest.ScratchDB(t)
	tag := fmt.Sprint(time.Now().UnixNano())
	a, b := seedLibrary(t, db, "a"+tag), seedLibrary(t, db, "b"+tag)
	e := New(db)
	ctx := context.Background()
	lib := Scope{Invocation: "inv_r" + tag, PluginID: "tv.maktaba.t", SubjectKind: "library", SubjectID: a.ids["library"], LibraryID: a.ids["library"]}
	vid := Scope{Invocation: "inv_rv" + tag, PluginID: "tv.maktaba.t", SubjectKind: "video", SubjectID: a.ids["subject"], LibraryID: a.ids["library"]}
	for _, kind := range []string{"season", "transcript", "collection", "job", "job_subject", "library", "subject"} {
		st := &Statement{Name: "r." + kind, Mode: "read", Scope: kind, ScopeArg: 1}
		if err := e.CheckScopes(ctx, lib, st, []any{argFor(kind, b.ids[kind])}); err == nil || err.Code != CodeScope {
			t.Errorf("a read of another library's %s: %v", kind, err)
		}
		if err := e.CheckScopes(ctx, lib, st, []any{argFor(kind, a.ids[kind])}); err != nil {
			t.Errorf("a read of the job's library's %s: %v", kind, err)
		}
	}
	// the job's subject is a video, a library or a series too
	js := &Statement{Name: "r.js", Mode: "read", Scope: "job_subject", ScopeArg: 1}
	for _, k := range []string{"subject", "library", "series"} {
		if err := e.CheckScopes(ctx, lib, js, []any{b.ids[k]}); err == nil || err.Code != CodeScope {
			t.Errorf("a job subject that is another library's %s: %v", k, err)
		}
		if err := e.CheckScopes(ctx, lib, js, []any{a.ids[k]}); err != nil {
			t.Errorf("a job subject that is the library's %s: %v", k, err)
		}
	}
	// a video job reads its own video's rows; the same library's other video is not its own
	for _, kind := range []string{"subject", "job_subject"} {
		st := &Statement{Name: "rv." + kind, Mode: "read", Scope: kind, ScopeArg: 1}
		if err := e.CheckScopes(ctx, vid, st, []any{a.video}); err == nil || err.Code != CodeScope {
			t.Errorf("a video job reading another video as %s: %v", kind, err)
		}
		if err := e.CheckScopes(ctx, vid, st, []any{a.ids["subject"]}); err != nil {
			t.Errorf("a video job reading its own video as %s: %v", kind, err)
		}
	}
	jobSt := &Statement{Name: "rv.job", Mode: "read", Scope: "job", ScopeArg: 1}
	if err := e.CheckScopes(ctx, vid, jobSt, []any{argFor("job", a.ids["job"])}); err != nil {
		t.Errorf("a video job reading its own video's job: %v", err)
	}
	var other string
	if err := db.QueryRow(`INSERT INTO processing_jobs (stage, state, subject_kind, subject_id, video_id, library_id)
		VALUES ('probe', 'done', 'video', $1, $1, $2) RETURNING id::text`, a.video, a.ids["library"]).Scan(&other); err != nil {
		t.Fatal(err)
	}
	if err := e.CheckScopes(ctx, vid, jobSt, []any{argFor("job", other)}); err == nil || err.Code != CodeScope {
		t.Errorf("a video job reading another video's job: %v", err)
	}
}

func TestARowOfAVideoKindIsTheVideoJobsOwnOnly(t *testing.T) {
	db, _ := clustertest.ScratchDB(t)
	tag := fmt.Sprint(time.Now().UnixNano())
	a := seedLibrary(t, db, "a"+tag)
	e := New(db)
	ctx := context.Background()
	// the same library's second video: its transcript and track are not a video job's own
	var track, transcript string
	if err := db.QueryRow(`INSERT INTO audio_tracks (video_id, track_index) VALUES ($1, 0) RETURNING id::text`, a.video).Scan(&track); err != nil {
		t.Fatal(err)
	}
	if err := db.QueryRow(`INSERT INTO transcripts (video_id, audio_track_id, language, backend, model) VALUES ($1, $2, 'en', 'b', 'm') RETURNING id::text`,
		a.video, track).Scan(&transcript); err != nil {
		t.Fatal(err)
	}
	job := Scope{Invocation: "inv_v" + tag, PluginID: "tv.maktaba.t", SubjectKind: "video", SubjectID: a.ids["subject"], LibraryID: a.ids["library"]}
	st := &Statement{Name: "t.w", Mode: "write", Scope: "transcript", ScopeArg: 1, Also: []Check{{"audio_track", 2}, {"season", 3}}}
	if err := e.CheckScopes(ctx, job, st, []any{a.ids["transcript"], argFor("audio_track", a.ids["audio_track"]), a.ids["season"]}); err != nil {
		t.Fatalf("the job's own transcript, track and its library's season: %v", err)
	}
	if err := e.CheckScopes(ctx, job, st, []any{transcript, nil, nil}); err == nil || err.Code != CodeScope {
		t.Fatalf("another video's transcript from a video job: %v", err)
	}
	if err := e.CheckScopes(ctx, job, st, []any{nil, argFor("audio_track", track), nil}); err == nil || err.Code != CodeScope {
		t.Fatalf("another video's track from a video job: %v", err)
	}
	// an id in another form Postgres reads as the same uuid is the same row
	for _, form := range []string{strings.ToUpper(transcript), strings.ReplaceAll(transcript, "-", ""), "{" + transcript + "}"} {
		if err := e.CheckScopes(ctx, job, st, []any{form, nil, nil}); err == nil || err.Code != CodeScope {
			t.Fatalf("another video's transcript written as %q: %v", form, err)
		}
	}
	// a new id (an insert's) matches no row: allowed
	if err := e.CheckScopes(ctx, job, st, []any{"0b8f8a52-61a4-4d0a-9d8a-9a3f4bd2d001", nil, nil}); err != nil {
		t.Fatalf("a new transcript id: %v", err)
	}
	// a value that is no id of its kind
	if err := e.CheckScopes(ctx, job, st, []any{"nope", nil, nil}); err == nil || err.Code != CodeScope {
		t.Fatalf("a transcript id that is no uuid: %v", err)
	}
}

func TestAnArrayArgumentIsCheckedElementByElement(t *testing.T) {
	db, _ := clustertest.ScratchDB(t)
	tag := fmt.Sprint(time.Now().UnixNano())
	a, b := seedLibrary(t, db, "a"+tag), seedLibrary(t, db, "b"+tag)
	e := New(db)
	ctx := context.Background()
	job := Scope{Invocation: "inv_arr" + tag, PluginID: "tv.maktaba.t", SubjectKind: "library", SubjectID: a.ids["library"], LibraryID: a.ids["library"]}
	videos := &Statement{Name: "t.del", Mode: "write", Scope: "subject", ScopeArg: 1}
	segs := &Statement{Name: "t.segs", Mode: "write", Scope: "segment", ScopeArg: 1}
	for _, c := range []struct {
		st   *Statement
		raw  any
		want bool
	}{
		{videos, []any{a.ids["subject"], a.video}, true},
		{videos, []any{a.ids["subject"], b.ids["subject"]}, false},
		{segs, []any{argFor("segment", a.ids["segment"])}, true},
		{segs, []any{argFor("segment", a.ids["segment"]), argFor("segment", b.ids["segment"])}, false},
	} {
		js, _ := json.Marshal(c.raw)
		arg, err := decodeArg(js)
		if err != nil {
			t.Fatal(err)
		}
		// as decodeArg sends it ({…}), and as a JSON array's text (json_each($N))
		for _, v := range []any{arg, string(js)} {
			got := e.CheckScopes(ctx, job, c.st, []any{v})
			if (got == nil) != c.want || (got != nil && got.Code != CodeScope) {
				t.Errorf("%s %v: %v, want allowed=%v", c.st.Name, v, got, c.want)
			}
		}
	}
}

func TestYouTubeRowsAreSharedByTheLibrariesThatFollowThem(t *testing.T) {
	db, _ := clustertest.ScratchDB(t)
	tag := fmt.Sprint(time.Now().UnixNano())
	a, b := seedLibrary(t, db, "a"+tag), seedLibrary(t, db, "b"+tag)
	e := New(db)
	ctx := context.Background()
	job := Scope{Invocation: "inv_yt" + tag, PluginID: "tv.maktaba.youtube", SubjectKind: "library", SubjectID: a.ids["library"], LibraryID: a.ids["library"]}
	check := func(kind, id string) *Error {
		return e.CheckScopes(ctx, job, &Statement{Name: "y." + kind, Mode: "write", Scope: kind, ScopeArg: 1}, []any{id})
	}
	for _, k := range []string{"youtube_channel", "youtube_playlist", "youtube_video"} {
		if err := check(k, b.ids[k]); err == nil || err.Code != CodeScope {
			t.Fatalf("%s of a channel only B follows: %v", k, err)
		}
	}
	// A follows B's channel as a source: its channel, playlists and videos are A's too
	if _, err := db.Exec(`INSERT INTO youtube_sources (library_id, kind, url, external_id) VALUES ($1, 'channel', 'u', $2)`,
		a.ids["library"], b.ids["youtube_channel"]); err != nil {
		t.Fatal(err)
	}
	for _, k := range []string{"youtube_channel", "youtube_playlist", "youtube_video"} {
		if err := check(k, b.ids[k]); err != nil {
			t.Fatalf("%s of a channel A follows: %v", k, err)
		}
	}
	// a video A downloads, on a channel A does not follow
	var vid string
	if err := db.QueryRow(`INSERT INTO youtube_channels (channel_id, library_id, url) VALUES ($1, $2, 'u') RETURNING channel_id`,
		"UC-c"+tag, b.ids["library"]).Scan(&vid); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`INSERT INTO youtube_videos (youtube_id, channel_id, title) VALUES ($1, $2, 't')`, "yt-c"+tag, "UC-c"+tag); err != nil {
		t.Fatal(err)
	}
	if err := check("youtube_video", "yt-c"+tag); err == nil || err.Code != CodeScope {
		t.Fatalf("a video of B's channel that A does not download: %v", err)
	}
	if _, err := db.Exec(`INSERT INTO youtube_downloads (library_id, youtube_id) VALUES ($1, $2)`, a.ids["library"], "yt-c"+tag); err != nil {
		t.Fatal(err)
	}
	if err := check("youtube_video", "yt-c"+tag); err != nil {
		t.Fatalf("a video A downloads: %v", err)
	}
	// a channel no library has claimed, and its videos, are every library's
	if _, err := db.Exec(`INSERT INTO youtube_channels (channel_id, library_id, url) VALUES ($1, NULL, 'u')`, "UC-n"+tag); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`INSERT INTO youtube_videos (youtube_id, channel_id, title) VALUES ($1, $2, 't')`, "yt-n"+tag, "UC-n"+tag); err != nil {
		t.Fatal(err)
	}
	if err := check("youtube_channel", "UC-n"+tag); err != nil {
		t.Fatalf("an unclaimed channel: %v", err)
	}
	if err := check("youtube_video", "yt-n"+tag); err != nil {
		t.Fatalf("a video of an unclaimed channel: %v", err)
	}
	// a new channel id (upsert_discovered_channel's insert) matches no row
	if err := check("youtube_channel", "UC-new"+tag); err != nil {
		t.Fatalf("a new channel: %v", err)
	}
}

func TestACatalogJobSpansEveryLibrary(t *testing.T) {
	db, _ := clustertest.ScratchDB(t)
	tag := fmt.Sprint(time.Now().UnixNano())
	b := seedLibrary(t, db, "b"+tag)
	e := New(db)
	job := Scope{Invocation: "inv_c" + tag, PluginID: "tv.maktaba.livetv", SubjectKind: "catalog"}
	for kind, id := range b.ids {
		st := &Statement{Name: "c." + kind, Mode: "write", Scope: kind, ScopeArg: 1}
		if err := e.CheckScopes(context.Background(), job, st, []any{argFor(kind, id)}); err != nil {
			t.Errorf("%s from a catalog job: %v", kind, err)
		}
	}
}

func TestEveryRowKindIsSeededAndResolved(t *testing.T) {
	db, _ := clustertest.ScratchDB(t)
	a := seedLibrary(t, db, "k"+fmt.Sprint(time.Now().UnixNano()))
	for _, k := range RowScopes() {
		if a.ids[k] == "" {
			t.Errorf("kind %s has no seeded row: the bundle walk cannot test its writes", k)
		}
	}
}
