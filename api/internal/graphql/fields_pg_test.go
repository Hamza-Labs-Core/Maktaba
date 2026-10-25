package graphql_test

// Every root field against a real, migrated Postgres through the real
// API router (router.New + MountP6, the production middleware chain with
// an idempotency store), with three callers: nobody (unauthorized), a
// non-admin member of library A only (forbidden / row-level visibility)
// and an admin (the happy path). Runs when MAKTABA_TEST_PG_URL names a
// Postgres the user may create databases on (CI's integration job sets
// it), e.g.
//
//	MAKTABA_TEST_PG_URL=postgres://maktaba:maktaba@127.0.0.1:5432/maktaba?sslmode=disable \
//	    go test ./internal/graphql/ -run PG

import (
	"bytes"
	"context"
	"database/sql"
	"database/sql/driver"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/coder/websocket"
	"github.com/google/uuid"
	"github.com/vektah/gqlparser/v2"
	"github.com/vektah/gqlparser/v2/ast"

	"github.com/Hamza-Labs-Core/Maktaba/api/internal/auth/principal"
	"github.com/Hamza-Labs-Core/Maktaba/api/internal/cluster/clustertest"
	"github.com/Hamza-Labs-Core/Maktaba/api/internal/graphql"
	grpcstreaming "github.com/Hamza-Labs-Core/Maktaba/api/internal/grpcclients/streaming"
	"github.com/Hamza-Labs-Core/Maktaba/api/internal/handlers/ws"
	"github.com/Hamza-Labs-Core/Maktaba/api/internal/idempotency"
	"github.com/Hamza-Labs-Core/Maktaba/api/internal/router"
)

// Fixture ids.
const (
	adminID  = "00000000-0000-4000-8000-0000000000a1"
	memberID = "00000000-0000-4000-8000-0000000000b1"
	libA     = "00000000-0000-4000-8000-00000000a000"
	libB     = "00000000-0000-4000-8000-00000000b000"
	videoA1  = "00000000-0000-4000-8000-0000000a0001"
	videoA2  = "00000000-0000-4000-8000-0000000a0002"
	videoB1  = "00000000-0000-4000-8000-0000000b0001"
	colA     = "00000000-0000-4000-8000-00000000c00a"
	colB     = "00000000-0000-4000-8000-00000000c00b"
	tagID    = "00000000-0000-4000-8000-0000000000f1"
	spkKeep  = "00000000-0000-4000-8000-0000000000e1"
	spkDrop  = "00000000-0000-4000-8000-0000000000e2"
	devID    = "00000000-0000-4000-8000-0000000000d1"
	sessID   = "00000000-0000-4000-8000-000000000051"
	transA1  = "00000000-0000-4000-8000-000000000071"
)

type role string

const (
	anon   role = ""
	member role = "member"
	admin  role = "admin"
)

type pgEnv struct {
	t          *testing.T
	db         *sql.DB
	dsn        string
	h          http.Handler
	hub        *ws.Hub
	job1, job2 string
}

// fakeStreaming is the streaming service: opens a direct session.
type fakeStreaming struct{}

func (fakeStreaming) OpenSession(_ context.Context, req grpcstreaming.OpenSessionRequest) (grpcstreaming.OpenSessionResponse, error) {
	return grpcstreaming.OpenSessionResponse{SessionID: uuid.NewString(), Mode: "direct", DirectURL: "/stream/direct/" + req.VideoID,
		ExpiresAt: time.Now().Add(time.Hour)}, nil
}
func (fakeStreaming) CloseSession(context.Context, string) error   { return nil }
func (fakeStreaming) EvictHashCache(context.Context, string) error { return nil }
func (fakeStreaming) GetCapabilities(context.Context) (grpcstreaming.Capabilities, error) {
	return grpcstreaming.Capabilities{Codecs: []string{"h264"}, HWAccel: "none", MaxBitrateKbps: 8000}, nil
}
func (fakeStreaming) HealthCheck(context.Context) (grpcstreaming.Status, error) {
	return grpcstreaming.Status{}, nil
}

// setupPG is a scratch, fully migrated Postgres with the fixture, behind
// the production router. The cases share it and run in a fixed order:
// the reads first, then the writes (deleteLibrary last).
func setupPG(t *testing.T) *pgEnv {
	t.Helper()
	db, dsn := clustertest.ScratchDB(t)
	e := &pgEnv{t: t, db: db, dsn: dsn, hub: ws.NewHub()}
	e.seed()
	r := router.New(router.Deps{IdempotencyStore: idempotency.NewMemoryStore()})
	router.MountP6(r, router.P6Deps{DB: db, EventHub: e.hub, StreamingClient: fakeStreaming{}})
	// The outer security layer of main.go attaches the principal; the
	// test picks it from X-Test-As.
	e.h = http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		switch role(req.Header.Get("X-Test-As")) {
		case admin:
			req = req.WithContext(principal.WithPrincipal(req.Context(), &principal.Principal{UserID: adminID, IsAdmin: true, AccessAllLibraries: true, Source: principal.SourceJWT}))
		case member:
			req = req.WithContext(principal.WithPrincipal(req.Context(), &principal.Principal{UserID: memberID, Libraries: []string{libA}, Source: principal.SourceJWT}))
		}
		r.ServeHTTP(w, req)
	})
	return e
}

func (e *pgEnv) exec(q string, args ...any) {
	e.t.Helper()
	if _, err := e.db.Exec(q, args...); err != nil {
		e.t.Fatalf("seed %q: %v", q, err)
	}
}

func (e *pgEnv) seed() {
	e.exec(`INSERT INTO users (id, username, pw_hash, is_admin) VALUES ($1,'gql-admin','x',true), ($2,'gql-member','x',false)`, adminID, memberID)
	e.exec(`INSERT INTO libraries (id, name, roots) VALUES ($1,'Alpha','{/media/alpha}'), ($2,'Beta','{/media/beta}')`, libA, libB)
	now := time.Now().UTC()
	for i, v := range []struct{ id, lib, title string }{{videoA1, libA, "First lesson"}, {videoA2, libA, "Second lesson"}, {videoB1, libB, "Hidden lesson"}} {
		e.exec(`INSERT INTO videos (id, library_id, path, filename, content_hash, size_bytes, mtime, state, title, duration_sec, detected_language, updated_at, created_at)
			VALUES ($1,$2,$3,$4,encode(sha256(convert_to($5,'UTF8')),'hex'),3000000000,$6,'ready',$7,100,'ar',$8,$8)`,
			v.id, v.lib, "/media/x/"+v.id+".mp4", v.id+".mp4", "hash-"+v.id, now, v.title, now.Add(-time.Duration(i)*time.Minute))
	}
	e.exec(`INSERT INTO tags (id, name, name_norm) VALUES ($1,'Arabic','arabic')`, tagID)
	e.exec(`INSERT INTO video_tags (video_id, tag_id) VALUES ($1,$2)`, videoA1, tagID)
	e.exec(`INSERT INTO media_info (video_id, container, video_codec, width, height, fps, bitrate_kbps) VALUES ($1,'mp4','h264',1920,1080,25,4000)`, videoA1)
	var trackID int64
	if err := e.db.QueryRow(`INSERT INTO audio_tracks (video_id, track_index, codec, channels, language, is_default) VALUES ($1,0,'aac',2,'ara',true) RETURNING id`, videoA1).Scan(&trackID); err != nil {
		e.t.Fatal(err)
	}
	e.exec(`INSERT INTO transcripts (id, video_id, audio_track_id, language, backend, model, is_active) VALUES ($1,$2,$3,'ar','test','m',true)`, transA1, videoA1, trackID)
	var segID int64
	if err := e.db.QueryRow(`INSERT INTO transcript_segments (transcript_id, seq, start_sec, end_sec, text) VALUES ($1,0,0,5,'hello patience') RETURNING id`, transA1).Scan(&segID); err != nil {
		e.t.Fatal(err)
	}
	e.exec(`INSERT INTO transcript_segments (transcript_id, seq, start_sec, end_sec, text) VALUES ($1,1,5,10,'second line')`, transA1)
	e.exec(`INSERT INTO transcript_words (segment_id, seq, start_sec, end_sec, text, confidence) VALUES ($1,0,0,1,'hello',0.9)`, segID)
	e.exec(`INSERT INTO subtitle_files (video_id, language, format, source, path) VALUES ($1,'en','vtt','embedded','/subs/a1.vtt')`, videoA1)
	e.exec(`INSERT INTO chapters (video_id, seq, start_sec, end_sec, title, source) VALUES ($1,0,0,50,'Intro','manual')`, videoA1)
	e.exec(`INSERT INTO collections (id, library_id, name) VALUES ($1,$2,'Alpha picks'), ($3,$4,'Beta picks')`, colA, libA, colB, libB)
	e.exec(`INSERT INTO collection_items (collection_id, video_id, position) VALUES ($1,$2,0), ($1,$3,1)`, colA, videoA1, videoB1)
	if err := e.db.QueryRow(`INSERT INTO processing_jobs (video_id, library_id, stage, state, created_at) VALUES ($1,$2,'transcribe','pending',$3) RETURNING id::text`, videoA1, libA, now).Scan(&e.job1); err != nil {
		e.t.Fatal(err)
	}
	if err := e.db.QueryRow(`INSERT INTO processing_jobs (video_id, library_id, stage, state, created_at) VALUES ($1,$2,'subtitle_gen','failed',$3) RETURNING id::text`, videoA1, libA, now.Add(-time.Hour)).Scan(&e.job2); err != nil {
		e.t.Fatal(err)
	}
	e.exec(`INSERT INTO devices (id, user_id, platform, push_token, bundle_id, app_version, locale) VALUES ($1,$2,'ios','tok','dev.maktaba.tv','1.0','ar')`, devID, memberID)
	e.exec(`INSERT INTO speakers (id, video_id, name, cluster_label) VALUES ($1,$2,'Shaykh','c1'), ($3,$2,'Unknown 2','c2')`, spkKeep, videoA1, spkDrop)
	e.exec(`INSERT INTO playback_state (user_id, video_id, position_sec, completed, updated_at) VALUES ($1,$2,30,false,$3)`, memberID, videoA1, now)
	e.exec(`INSERT INTO streaming_sessions (id, user_id, video_id, mode, opened_at, last_seen_at) VALUES ($1,$2,$3,'direct',$4,$4)`, sessID, memberID, videoA1, now)
	// the member's own search history (suggestions are per user)
	e.exec(`INSERT INTO search_history (user_id, query, query_norm) VALUES ($1,'patience','patience')`, memberID)
}

// gql posts one operation as who and returns the HTTP status and body.
func (e *pgEnv) gql(who role, query string, vars map[string]any, hdr ...string) (int, map[string]any) {
	e.t.Helper()
	b, _ := json.Marshal(map[string]any{"query": query, "variables": vars})
	req := httptest.NewRequest(http.MethodPost, "/graphql", bytes.NewReader(b))
	req.Header.Set("Content-Type", "application/json")
	if who != anon {
		req.Header.Set("X-Test-As", string(who))
	}
	for i := 0; i+1 < len(hdr); i += 2 {
		req.Header.Set(hdr[i], hdr[i+1])
	}
	rec := httptest.NewRecorder()
	e.h.ServeHTTP(rec, req)
	var env map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &env); err != nil {
		e.t.Fatalf("status %d, body not JSON: %s", rec.Code, rec.Body.String())
	}
	return rec.Code, env
}

func code(env map[string]any) string {
	errs, _ := env["errors"].([]any)
	if len(errs) == 0 {
		return ""
	}
	ext, _ := errs[0].(map[string]any)["extensions"].(map[string]any)
	c, _ := ext["code"].(string)
	return c
}

// data returns data[field], failing on any error.
func (e *pgEnv) ok(who role, field, query string, vars map[string]any) any {
	e.t.Helper()
	status, env := e.gql(who, query, vars)
	if status != http.StatusOK || env["errors"] != nil {
		e.t.Fatalf("%s as %q: status %d errors %v", field, who, status, env["errors"])
	}
	return env["data"].(map[string]any)[field]
}

// fails asserts the operation fails with code.
func (e *pgEnv) fails(who role, want, query string, vars map[string]any) {
	e.t.Helper()
	_, env := e.gql(who, query, vars)
	if got := code(env); got != want {
		e.t.Fatalf("as %q: code %q, want %q (%v)", who, got, want, env)
	}
}

func ids(v any, key string) []string {
	var out []string
	list, _ := v.([]any)
	for _, it := range list {
		m, _ := it.(map[string]any)
		out = append(out, fmt.Sprint(m[key]))
	}
	return out
}

func has(list []string, s string) bool {
	for _, x := range list {
		if x == s {
			return true
		}
	}
	return false
}

// fieldCase is one root field's test: run(e) exercises unauthorized,
// forbidden (or the row-level rule) and the happy path.
type fieldCase struct {
	field string
	run   func(t *testing.T, e *pgEnv)
}

// unauthorized: no principal → 401 unauthenticated before any REST call.
func unauthorized(t *testing.T, e *pgEnv, q string, vars map[string]any) {
	t.Helper()
	status, env := e.gql(anon, q, vars)
	if status != http.StatusUnauthorized || code(env) != "unauthenticated" {
		t.Fatalf("unauthorized: status %d code %q", status, code(env))
	}
}

func cases() []fieldCase {
	return []fieldCase{
		{"libraries", func(t *testing.T, e *pgEnv) {
			q := `{ libraries { id name roots } }`
			unauthorized(t, e, q, nil)
			got := ids(e.ok(member, "libraries", q, nil), "id")
			if !has(got, libA) || has(got, libB) {
				t.Fatalf("member sees %v; want only library A", got)
			}
			if got := ids(e.ok(admin, "libraries", q, nil), "id"); !has(got, libA) || !has(got, libB) {
				t.Fatalf("admin sees %v", got)
			}
		}},
		{"library", func(t *testing.T, e *pgEnv) {
			q := `query($id: ID!) { library(id: $id) { id name createdAt } }`
			unauthorized(t, e, q, map[string]any{"id": libA})
			// a library the member may not see answers as one that does not exist (the REST
			// read's 404 since #313: a 403 told which ids exist): null, never its fields
			if v := e.ok(member, "library", q, map[string]any{"id": libB}); v != nil {
				t.Fatalf("a library the member may not see = %v, want null", v)
			}
			lib := e.ok(member, "library", q, map[string]any{"id": libA}).(map[string]any)
			if lib["name"] != "Alpha" {
				t.Fatalf("library = %v", lib)
			}
			if v := e.ok(admin, "library", q, map[string]any{"id": uuid.NewString()}); v != nil {
				t.Fatalf("missing library = %v, want null", v)
			}
		}},
		{"videos", func(t *testing.T, e *pgEnv) {
			q := `query($after: String) { videos(limit: 1, after: $after) { items { id sizeBytes tags { name } audioTracks { codec } mediaInfo { width } playbackState { positionSec } } nextCursor } }`
			unauthorized(t, e, q, nil)
			// member: library A only, paged through the shared cursor.
			var seen []string
			var after any
			for i := 0; i < 5; i++ {
				conn := e.ok(member, "videos", q, map[string]any{"after": after}).(map[string]any)
				seen = append(seen, ids(conn["items"], "id")...)
				if conn["nextCursor"] == nil {
					break
				}
				after = conn["nextCursor"]
			}
			if len(seen) != 2 || has(seen, videoB1) || !has(seen, videoA1) || !has(seen, videoA2) {
				t.Fatalf("member pages = %v, want A1 and A2 once each", seen)
			}
			conn := e.ok(admin, "videos", `{ videos(limit: 50) { items { id } } }`, nil).(map[string]any)
			if got := ids(conn["items"], "id"); len(got) != 3 {
				t.Fatalf("admin videos = %v", got)
			}
		}},
		{"video", func(t *testing.T, e *pgEnv) {
			q := `query($id: ID!) { video(id: $id) { id title sizeBytes tags { id name } mediaInfo { container width height fps bitrateKbps durationSec }
				audioTracks { id language codec channels default } subtitles { language format url } chapters { seq title }
				latestTranscript { id language segments(first: 10) { seq text words { text } } } playbackState { positionSec completed } } }`
			unauthorized(t, e, q, map[string]any{"id": videoA1})
			e.fails(member, "forbidden", q, map[string]any{"id": videoB1})
			v := e.ok(member, "video", q, map[string]any{"id": videoA1}).(map[string]any)
			if v["sizeBytes"].(float64) != 3e9 {
				t.Fatalf("sizeBytes = %v", v["sizeBytes"])
			}
			if v["mediaInfo"].(map[string]any)["width"].(float64) != 1920 {
				t.Fatalf("mediaInfo = %v", v["mediaInfo"])
			}
			at := v["audioTracks"].([]any)[0].(map[string]any)
			if at["codec"] != "aac" || at["language"] != "ara" || at["default"] != true {
				t.Fatalf("audioTracks = %v", v["audioTracks"])
			}
			tr := v["latestTranscript"].(map[string]any)
			segs := tr["segments"].([]any)
			if tr["id"] != transA1 || len(segs) != 2 || len(segs[0].(map[string]any)["words"].([]any)) != 1 {
				t.Fatalf("transcript = %v", tr)
			}
			if v["playbackState"].(map[string]any)["positionSec"].(float64) != 30 {
				t.Fatalf("playbackState = %v", v["playbackState"])
			}
			if len(v["subtitles"].([]any)) != 1 || len(v["chapters"].([]any)) != 1 || len(v["tags"].([]any)) != 1 {
				t.Fatalf("joins = %v", v)
			}
			// Not watched by the admin: null, not an error.
			if va := e.ok(admin, "video", q, map[string]any{"id": videoA1}).(map[string]any); va["playbackState"] != nil {
				t.Fatalf("admin playbackState = %v", va["playbackState"])
			}
		}},
		{"search", func(t *testing.T, e *pgEnv) {
			q := `query($q: String!) { search(input: {q: $q, mode: "fts"}) { total hits { videoId bestMatch { snippet score } matches { segmentId } } tookMs } }`
			unauthorized(t, e, q, map[string]any{"q": "patience"})
			res := e.ok(member, "search", q, map[string]any{"q": "patience"}).(map[string]any)
			if got := ids(res["hits"], "videoId"); !has(got, videoA1) {
				t.Fatalf("search hits = %v", res)
			}
			// Row-level: the member gets nothing from library B.
			e.exec(`INSERT INTO transcripts (id, video_id, audio_track_id, language, backend, model, is_active)
				SELECT gen_random_uuid(), $1, id, 'ar','t','m',true FROM audio_tracks WHERE video_id = $2`, videoB1, videoA1)
			e.exec(`INSERT INTO transcript_segments (transcript_id, seq, start_sec, end_sec, text) SELECT id, 0, 0, 5, 'zebra hidden' FROM transcripts WHERE video_id = $1`, videoB1)
			if res := e.ok(member, "search", q, map[string]any{"q": "zebra"}).(map[string]any); len(res["hits"].([]any)) != 0 {
				t.Fatalf("member found library B: %v", res)
			}
			if res := e.ok(admin, "search", q, map[string]any{"q": "zebra"}).(map[string]any); len(res["hits"].([]any)) == 0 {
				t.Fatalf("admin found nothing: %v", res)
			}
		}},
		{"searchSuggest", func(t *testing.T, e *pgEnv) {
			q := `{ searchSuggest(q: "pat") }`
			unauthorized(t, e, q, nil)
			// REST serves each signed-in user their own suggestions.
			if got := e.ok(admin, "searchSuggest", q, nil).([]any); len(got) != 0 {
				t.Fatalf("admin sees the member's history: %v", got)
			}
			if got := e.ok(member, "searchSuggest", q, nil).([]any); len(got) != 1 || got[0] != "patience" {
				t.Fatalf("suggest = %v", got)
			}
		}},
		{"job", func(t *testing.T, e *pgEnv) {
			q := `query($id: ID!) { job(id: $id) { id stage state videoId createdAt } }`
			unauthorized(t, e, q, map[string]any{"id": e.job1})
			// REST reads a job for any signed-in user.
			j := e.ok(member, "job", q, map[string]any{"id": e.job1}).(map[string]any)
			if j["stage"] != "transcribe" || j["videoId"] != videoA1 {
				t.Fatalf("job = %v", j)
			}
			if v := e.ok(admin, "job", q, map[string]any{"id": "999999"}); v != nil {
				t.Fatalf("missing job = %v", v)
			}
		}},
		{"jobs", func(t *testing.T, e *pgEnv) {
			q := `query($after: String) { jobs(limit: 1, after: $after) { items { id } nextCursor } }`
			unauthorized(t, e, q, nil)
			first := e.ok(admin, "jobs", q, nil).(map[string]any)
			second := e.ok(admin, "jobs", q, map[string]any{"after": first["nextCursor"]}).(map[string]any)
			got := append(ids(first["items"], "id"), ids(second["items"], "id")...)
			if len(got) != 2 || got[0] != e.job1 || got[1] != e.job2 || second["nextCursor"] != nil {
				t.Fatalf("pages = %v %v", first, second)
			}
			filtered := e.ok(member, "jobs", `{ jobs(state: "failed") { items { id state } } }`, nil).(map[string]any)
			if got := ids(filtered["items"], "id"); len(got) != 1 || got[0] != e.job2 {
				t.Fatalf("filtered = %v", got)
			}
		}},
		{"queueStats", func(t *testing.T, e *pgEnv) {
			q := `{ queueStats }`
			unauthorized(t, e, q, nil)
			e.fails(member, "forbidden", q, nil) // admin-only since #313 (GET /api/queue/stats)
			st := e.ok(admin, "queueStats", q, nil).(map[string]any)
			if _, ok := st["by_stage"]; !ok {
				t.Fatalf("queueStats = %v", st)
			}
		}},
		{"collections", func(t *testing.T, e *pgEnv) {
			q := `{ collections { id name items { id } } }`
			unauthorized(t, e, q, nil)
			got := e.ok(member, "collections", q, nil).([]any)
			if cs := ids(got, "id"); len(cs) != 1 || cs[0] != colA {
				t.Fatalf("member collections = %v", cs)
			}
			// Items: library B's video is dropped for the member.
			if items := ids(got[0].(map[string]any)["items"], "id"); len(items) != 1 || items[0] != videoA1 {
				t.Fatalf("member items = %v", items)
			}
			if cs := ids(e.ok(admin, "collections", q, nil), "id"); len(cs) != 2 {
				t.Fatalf("admin collections = %v", cs)
			}
		}},
		{"collection", func(t *testing.T, e *pgEnv) {
			q := `query($id: ID!) { collection(id: $id) { id name isSmart items { id } } }`
			unauthorized(t, e, q, map[string]any{"id": colA})
			c := e.ok(admin, "collection", q, map[string]any{"id": colA}).(map[string]any)
			if items := ids(c["items"], "id"); len(items) != 2 || items[0] != videoA1 {
				t.Fatalf("admin items = %v", items)
			}
			// The REST collection read has no library check of its own; the
			// videos in it follow the video rule.
			c = e.ok(member, "collection", q, map[string]any{"id": colA}).(map[string]any)
			if items := ids(c["items"], "id"); len(items) != 1 {
				t.Fatalf("member items = %v", items)
			}
			if v := e.ok(admin, "collection", q, map[string]any{"id": uuid.NewString()}); v != nil {
				t.Fatalf("missing = %v", v)
			}
		}},
		{"tags", func(t *testing.T, e *pgEnv) {
			q := `{ tags { id name } }`
			unauthorized(t, e, q, nil)
			if got := ids(e.ok(member, "tags", q, nil), "name"); len(got) != 1 || got[0] != "Arabic" {
				t.Fatalf("tags = %v", got)
			}
		}},
		{"speakers", func(t *testing.T, e *pgEnv) {
			q := `query($v: ID) { speakers(videoId: $v) { id name clusterLabel } }`
			unauthorized(t, e, q, nil)
			if got := ids(e.ok(member, "speakers", q, map[string]any{"v": videoA1}), "id"); len(got) != 2 {
				t.Fatalf("speakers = %v", got)
			}
			if got := ids(e.ok(admin, "speakers", q, map[string]any{"v": videoB1}), "id"); len(got) != 0 {
				t.Fatalf("speakers of B1 = %v", got)
			}
		}},
		{"settings", func(t *testing.T, e *pgEnv) {
			q := `{ settings }`
			unauthorized(t, e, q, nil)
			e.fails(member, "forbidden", q, nil) // admin-only since #313 (GET /api/settings)
			if _, ok := e.ok(admin, "settings", q, nil).(map[string]any); !ok {
				t.Fatal("settings is not an object")
			}
		}},
		{"recommendations", func(t *testing.T, e *pgEnv) {
			q := `{ recommendations(limit: 12) { id title reason positionSec } }`
			unauthorized(t, e, q, nil)
			cards := e.ok(member, "recommendations", q, nil).([]any)
			var found bool
			for _, c := range cards {
				m := c.(map[string]any)
				if m["id"] == videoA1 && m["reason"] == "continue_watching" {
					found = m["title"] == "First lesson"
				}
				if m["id"] == videoB1 {
					t.Fatalf("member recommended a library B video: %v", m)
				}
			}
			if !found {
				t.Fatalf("no titled continue card: %v", cards)
			}
		}},
		{"continueWatching", func(t *testing.T, e *pgEnv) {
			q := `{ continueWatching(limit: 12) { id title positionSec durationSec remainingSec } }`
			unauthorized(t, e, q, nil)
			cards := e.ok(member, "continueWatching", q, nil).([]any)
			if len(cards) != 1 || cards[0].(map[string]any)["positionSec"].(float64) != 30 {
				t.Fatalf("member cards = %v", cards)
			}
			// Another user's progress is not visible.
			if cards := e.ok(admin, "continueWatching", q, nil).([]any); len(cards) != 0 {
				t.Fatalf("admin cards = %v", cards)
			}
		}},
		{"streamCapabilities", func(t *testing.T, e *pgEnv) {
			q := `{ streamCapabilities }`
			unauthorized(t, e, q, nil)
			caps := e.ok(member, "streamCapabilities", q, nil).(map[string]any)
			if fmt.Sprint(caps["codecs"]) != "[h264]" {
				t.Fatalf("caps = %v", caps)
			}
		}},
		{"streamSession", func(t *testing.T, e *pgEnv) {
			q := `query($id: ID!) { streamSession(id: $id) { id videoId mode openedAt closedAt } }`
			unauthorized(t, e, q, map[string]any{"id": sessID})
			s := e.ok(member, "streamSession", q, map[string]any{"id": sessID}).(map[string]any)
			if s["videoId"] != videoA1 || s["mode"] != "direct" || s["closedAt"] != nil {
				t.Fatalf("session = %v", s)
			}
			if v := e.ok(member, "streamSession", q, map[string]any{"id": uuid.NewString()}); v != nil {
				t.Fatalf("missing = %v", v)
			}
		}},
		{"devices", func(t *testing.T, e *pgEnv) {
			q := `{ devices { id platform bundleId appVersion locale } }`
			unauthorized(t, e, q, nil)
			if got := ids(e.ok(member, "devices", q, nil), "id"); len(got) != 1 || got[0] != devID {
				t.Fatalf("member devices = %v", got)
			}
			if got := ids(e.ok(admin, "devices", q, nil), "id"); len(got) != 0 {
				t.Fatalf("admin sees the member's devices: %v", got)
			}
		}},

		// ---------- mutations ----------
		{"createLibrary", func(t *testing.T, e *pgEnv) {
			q := `mutation($n: String!) { createLibrary(input: {name: $n, roots: ["` + jsonPath(t.TempDir()) + `"]}) { id name } }`
			unauthorized(t, e, q, map[string]any{"n": "Gamma"})
			e.fails(member, "forbidden", q, map[string]any{"n": "Gamma"})
			lib := e.ok(admin, "createLibrary", q, map[string]any{"n": "Gamma"}).(map[string]any)
			if lib["name"] != "Gamma" {
				t.Fatalf("created = %v", lib)
			}
		}},
		{"patchLibrary", func(t *testing.T, e *pgEnv) {
			q := `mutation($id: ID!) { patchLibrary(id: $id, input: {name: "Alpha 2"}) { id name } }`
			unauthorized(t, e, q, map[string]any{"id": libA})
			e.fails(member, "forbidden", q, map[string]any{"id": libA})
			if lib := e.ok(admin, "patchLibrary", q, map[string]any{"id": libA}).(map[string]any); lib["name"] != "Alpha 2" {
				t.Fatalf("patched = %v", lib)
			}
		}},
		{"scanLibrary", func(t *testing.T, e *pgEnv) {
			q := `mutation($id: ID!) { scanLibrary(id: $id) }`
			unauthorized(t, e, q, map[string]any{"id": libA})
			e.fails(admin, "not-found", q, map[string]any{"id": uuid.NewString()})
			if id := e.ok(admin, "scanLibrary", q, map[string]any{"id": libA}); id == "" {
				t.Fatalf("scan job id = %q", id)
			}
		}},
		{"patchVideo", func(t *testing.T, e *pgEnv) {
			q := `mutation($id: ID!) { patchVideo(id: $id, input: {title: "Renamed", tags: ["a", "b"]}) { id title tags { name } } }`
			unauthorized(t, e, q, map[string]any{"id": videoA2})
			e.fails(member, "forbidden", q, map[string]any{"id": videoA2})
			v := e.ok(admin, "patchVideo", q, map[string]any{"id": videoA2}).(map[string]any)
			if v["title"] != "Renamed" || len(v["tags"].([]any)) != 2 {
				t.Fatalf("patched = %v", v)
			}
		}},
		{"deleteVideo", func(t *testing.T, e *pgEnv) {
			q := `mutation($id: ID!) { deleteVideo(id: $id) { deleted warning } }`
			unauthorized(t, e, q, map[string]any{"id": videoA2})
			e.fails(member, "forbidden", q, map[string]any{"id": videoA2})
			if r := e.ok(admin, "deleteVideo", q, map[string]any{"id": videoA2}).(map[string]any); r["deleted"] != true {
				t.Fatalf("delete = %v", r)
			}
			if v := e.ok(admin, "video", `query($id: ID!) { video(id: $id) { id } }`, map[string]any{"id": videoA2}); v != nil {
				t.Fatalf("video still there: %v", v)
			}
		}},
		{"processVideo", func(t *testing.T, e *pgEnv) {
			q := `mutation($id: ID!) { processVideo(id: $id, stage: "transcribe", priority: 10) }`
			unauthorized(t, e, q, map[string]any{"id": videoA1})
			e.fails(member, "forbidden", q, map[string]any{"id": videoA1})
			if _, ok := e.ok(admin, "processVideo", q, map[string]any{"id": videoA1}).(string); !ok {
				t.Fatal("processVideo did not return a job id string")
			}
		}},
		{"reprocessVideo", func(t *testing.T, e *pgEnv) {
			q := `mutation($id: ID!, $s: String!) { reprocessVideo(id: $id, fromStage: $s) }`
			unauthorized(t, e, q, map[string]any{"id": videoA1, "s": "subtitle_gen"})
			e.fails(member, "forbidden", q, map[string]any{"id": videoA1, "s": "subtitle_gen"})
			e.fails(admin, "bad-request", q, map[string]any{"id": videoA1, "s": "scan"})
			if _, ok := e.ok(admin, "reprocessVideo", q, map[string]any{"id": videoA1, "s": "subtitle_gen"}).([]any); !ok {
				t.Fatal("reprocessVideo did not return a list")
			}
		}},
		{"saveSearch", func(t *testing.T, e *pgEnv) {
			q := `mutation { saveSearch(name: "mine", query: {q: "patience"}) }`
			unauthorized(t, e, q, nil)
			id := e.ok(member, "saveSearch", q, nil)
			var owner string
			if err := e.db.QueryRow(`SELECT user_id::text FROM saved_searches WHERE id::text = $1`, id).Scan(&owner); err != nil || owner != memberID {
				t.Fatalf("saved search %v owner %q: %v", id, owner, err)
			}
		}},
		{"openStreamSession", func(t *testing.T, e *pgEnv) {
			q := `mutation($v: ID!) { openStreamSession(videoId: $v, profile: "tv") { id videoId mode directUrl expiresAt openedAt } }`
			unauthorized(t, e, q, map[string]any{"v": videoA1})
			e.fails(member, "forbidden", q, map[string]any{"v": videoB1})
			s := e.ok(member, "openStreamSession", q, map[string]any{"v": videoA1}).(map[string]any)
			if s["mode"] != "direct" || s["videoId"] != videoA1 || s["openedAt"] == nil || s["expiresAt"] == nil {
				t.Fatalf("session = %v", s)
			}
		}},
		{"closeStreamSession", func(t *testing.T, e *pgEnv) {
			q := `mutation($id: ID!) { closeStreamSession(id: $id) }`
			unauthorized(t, e, q, map[string]any{"id": sessID})
			e.fails(member, "bad-request", q, map[string]any{"id": "not-a-uuid"})
			if v := e.ok(member, "closeStreamSession", q, map[string]any{"id": sessID}); v != true {
				t.Fatalf("close = %v", v)
			}
			s := e.ok(member, "streamSession", `query($id: ID!) { streamSession(id: $id) { closedAt } }`, map[string]any{"id": sessID}).(map[string]any)
			if s["closedAt"] == nil {
				t.Fatal("session not closed")
			}
		}},
		{"postProgress", func(t *testing.T, e *pgEnv) {
			q := `mutation($id: ID!) { postProgress(sessionId: $id, positionSec: 42) }`
			unauthorized(t, e, q, map[string]any{"id": sessID})
			e.fails(member, "not-found", q, map[string]any{"id": uuid.NewString()})
			if v := e.ok(member, "postProgress", q, map[string]any{"id": sessID}); v != true {
				t.Fatalf("progress = %v", v)
			}
			var pos float64
			_ = e.db.QueryRow(`SELECT position_sec FROM playback_state WHERE user_id=$1 AND video_id=$2`, memberID, videoA1).Scan(&pos)
			if pos != 42 {
				t.Fatalf("position = %v", pos)
			}
		}},
		{"pauseJob", jobMutation("pauseJob", "pause_requested")},
		{"resumeJob", jobMutation("resumeJob", "")},
		{"cancelJob", jobMutation("cancelJob", "cancel")},
		{"retryJob", func(t *testing.T, e *pgEnv) {
			q := `mutation($id: ID!) { retryJob(id: $id) { id state } }`
			unauthorized(t, e, q, map[string]any{"id": e.job2})
			e.fails(member, "forbidden", q, map[string]any{"id": e.job2})
			if j := e.ok(admin, "retryJob", q, map[string]any{"id": e.job2}).(map[string]any); j["id"] == nil {
				t.Fatalf("retry = %v", j)
			}
		}},
		{"patchSettings", func(t *testing.T, e *pgEnv) {
			q := `mutation($in: JSON!) { patchSettings(input: $in) }`
			vars := map[string]any{"in": map[string]any{"search.fts_weight": 0.7}}
			unauthorized(t, e, q, vars)
			e.fails(member, "forbidden", q, vars)
			e.fails(admin, "forbidden", q, map[string]any{"in": map[string]any{"database.url": "x"}}) // not a runtime key
			out := e.ok(admin, "patchSettings", q, vars)
			if !strings.Contains(fmt.Sprint(out), "0.7") {
				t.Fatalf("settings after patch = %v", out)
			}
		}},
		{"registerDevice", func(t *testing.T, e *pgEnv) {
			q := `mutation { registerDevice(input: {platform: "android", pushToken: "fcm_token_for_the_graphql_test_000000000002", bundleId: "dev.maktaba.tv", locale: "en"}) }`
			unauthorized(t, e, q, nil)
			e.fails(member, "validation-failed", `mutation { registerDevice(input: {platform: "bogus", pushToken: "t", bundleId: "b"}) }`, nil)
			id := e.ok(member, "registerDevice", q, nil)
			if got := ids(e.ok(member, "devices", `{ devices { id } }`, nil), "id"); !has(got, fmt.Sprint(id)) {
				t.Fatalf("registered %v not listed in %v", id, got)
			}
		}},
		{"unregisterDevice", func(t *testing.T, e *pgEnv) {
			q := `mutation($id: ID!) { unregisterDevice(id: $id) }`
			unauthorized(t, e, q, map[string]any{"id": devID})
			// Another user's device is not found for the admin (REST scopes by owner).
			e.fails(admin, "not-found", q, map[string]any{"id": devID})
			if v := e.ok(member, "unregisterDevice", q, map[string]any{"id": devID}); v != true {
				t.Fatalf("unregister = %v", v)
			}
		}},
		{"mergeSpeakers", func(t *testing.T, e *pgEnv) {
			q := `mutation($k: ID!, $d: ID!) { mergeSpeakers(keep: $k, drop: $d) }`
			vars := map[string]any{"k": spkKeep, "d": spkDrop}
			unauthorized(t, e, q, vars)
			e.fails(member, "forbidden", q, vars)
			if _, ok := e.ok(admin, "mergeSpeakers", q, vars).(float64); !ok {
				t.Fatal("merge did not return a count")
			}
			if got := ids(e.ok(admin, "speakers", `{ speakers { id } }`, nil), "id"); has(got, spkDrop) {
				t.Fatalf("dropped speaker still listed: %v", got)
			}
		}},

		{"deleteLibrary", func(t *testing.T, e *pgEnv) {
			q := `mutation($id: ID!, $purge: Boolean, $c: String) { deleteLibrary(id: $id, purge: $purge, confirm: $c) { deleted failedPaths } }`
			unauthorized(t, e, q, map[string]any{"id": libB})
			e.fails(member, "forbidden", q, map[string]any{"id": libB})
			e.fails(admin, "confirmation-required", q, map[string]any{"id": libB, "purge": true, "c": "wrong"})
			if r := e.ok(admin, "deleteLibrary", q, map[string]any{"id": libB}).(map[string]any); r["deleted"] != true {
				t.Fatalf("delete = %v", r)
			}
			if v := e.ok(admin, "library", `query($id: ID!) { library(id: $id) { id } }`, map[string]any{"id": libB}); v != nil {
				t.Fatalf("library B still there: %v", v)
			}
		}},

		// ---------- subscriptions ----------
		{"jobUpdates", func(t *testing.T, e *pgEnv) {
			subscriptionCase(t, e, `subscription { jobUpdates { id stage } }`, func() {
				e.hub.Publish("jobs", ws.Event{Type: "job.updated", Payload: map[string]any{"job_id": e.job1}})
			}, func(data map[string]any) bool {
				j, _ := data["jobUpdates"].(map[string]any)
				return j != nil && j["id"] == e.job1 && j["stage"] == "transcribe"
			})
		}},
		{"libraryEvents", func(t *testing.T, e *pgEnv) {
			subscriptionCase(t, e, `subscription { libraryEvents(libraryId: "`+libA+`") }`, func() {
				e.hub.Publish("library:"+libA, ws.Event{Type: "library.scanned", Payload: map[string]any{"new": 1}})
			}, func(data map[string]any) bool {
				ev, _ := data["libraryEvents"].(map[string]any)
				return ev != nil && ev["type"] == "library.scanned"
			})
		}},
		{"playbackUpdates", func(t *testing.T, e *pgEnv) {
			subscriptionCase(t, e, `subscription { playbackUpdates(videoId: "`+videoA1+`") }`, func() {
				e.hub.Publish("playback:"+videoA1, ws.Event{Type: "playback.changed", Payload: map[string]any{"position_sec": 12}})
			}, func(data map[string]any) bool {
				ev, _ := data["playbackUpdates"].(map[string]any)
				return ev != nil && ev["type"] == "playback.changed"
			})
		}},
	}
}

func jsonPath(p string) string { return strings.ReplaceAll(p, `\`, `\\`) }

func jobMutation(field, _ string) func(t *testing.T, e *pgEnv) {
	return func(t *testing.T, e *pgEnv) {
		q := `mutation($id: ID!) { ` + field + `(id: $id) { id state pauseRequested cancelRequested } }`
		unauthorized(t, e, q, map[string]any{"id": e.job1})
		e.fails(member, "forbidden", q, map[string]any{"id": e.job1})
		e.fails(admin, "not-found", q, map[string]any{"id": "999999"})
		if j := e.ok(admin, field, q, map[string]any{"id": e.job1}).(map[string]any); j["id"] != e.job1 {
			t.Fatalf("%s = %v", field, j)
		}
	}
}

// subscriptionCase runs a graphql-transport-ws subscription against a
// loopback server: no principal is refused at the upgrade (401); a
// member receives the event published after subscribing.
func subscriptionCase(t *testing.T, e *pgEnv, query string, publish func(), match func(map[string]any) bool) {
	t.Helper()
	var as role
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if as != anon {
			r.Header.Set("X-Test-As", string(as))
		}
		e.h.ServeHTTP(w, r)
	}))
	defer srv.Close()
	url := "ws" + strings.TrimPrefix(srv.URL, "http") + "/graphql"
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	// Unauthorized.
	if _, resp, err := websocket.Dial(ctx, url, &websocket.DialOptions{Subprotocols: []string{"graphql-transport-ws"}}); err == nil || resp == nil || resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("anonymous upgrade: err %v resp %v", err, resp)
	}

	as = member
	c, _, err := websocket.Dial(ctx, url, &websocket.DialOptions{Subprotocols: []string{"graphql-transport-ws"}})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = c.CloseNow() }()
	send := func(m map[string]any) {
		b, _ := json.Marshal(m)
		if err := c.Write(ctx, websocket.MessageText, b); err != nil {
			t.Fatal(err)
		}
	}
	read := func() map[string]any {
		_, b, err := c.Read(ctx)
		if err != nil {
			t.Fatal(err)
		}
		var m map[string]any
		_ = json.Unmarshal(b, &m)
		return m
	}
	send(map[string]any{"type": "connection_init"})
	if m := read(); m["type"] != "connection_ack" {
		t.Fatalf("init answer %v", m)
	}
	send(map[string]any{"id": "1", "type": "subscribe", "payload": map[string]any{"query": query}})
	// The hub subscription is opened asynchronously; publish until seen.
	go func() {
		for i := 0; i < 50 && ctx.Err() == nil; i++ {
			publish()
			time.Sleep(50 * time.Millisecond)
		}
	}()
	for {
		m := read()
		if m["type"] == "error" {
			t.Fatalf("subscription error %v", m)
		}
		if m["type"] != "next" {
			continue
		}
		payload, _ := m["payload"].(map[string]any)
		if payload["errors"] != nil {
			t.Fatalf("next errors %v", payload["errors"])
		}
		if data, _ := payload["data"].(map[string]any); data != nil && match(data) {
			break
		}
	}
	send(map[string]any{"id": "1", "type": "complete"})
}

// TestPG_Fields runs every root field's case; TestPG_EveryRootFieldHasACase
// fails when a schema root field has none.
func TestPG_Fields(t *testing.T) {
	e := setupPG(t)
	run := func(name string, fn func(*testing.T, *pgEnv)) {
		t.Run(name, func(t *testing.T) {
			e.t = t
			fn(t, e)
		})
	}
	var writes []fieldCase
	for _, c := range cases() {
		if isMutation(c.field) {
			writes = append(writes, c)
			continue
		}
		run(c.field, c.run)
	}
	// After the reads (they pin the fixture's counts), before the writes.
	run("batching", nestedFieldsAreBatched)
	run("idempotency", idempotencyReplays)
	run("purge", purgeNeedsConfirm)
	for _, c := range writes {
		run(c.field, c.run)
	}
}

func isMutation(field string) bool {
	return parsedSchema.Mutation.Fields.ForName(field) != nil
}

var parsedSchema = gqlparser.MustLoadSchema(&ast.Source{Input: graphql.Schema})

func TestPG_EveryRootFieldHasACase(t *testing.T) {
	s := parsedSchema
	have := map[string]bool{}
	for _, c := range cases() {
		have[c.field] = true
	}
	for _, def := range []*ast.Definition{s.Query, s.Mutation, s.Subscription} {
		for _, f := range def.Fields {
			if !strings.HasPrefix(f.Name, "__") && !have[f.Name] {
				t.Errorf("root field %s.%s has no real-database test case", def.Name, f.Name)
			}
		}
	}
}

// idempotencyReplays: a retried mutation with the same Idempotency-Key replays the first
// GraphQL response instead of running the write again.
func idempotencyReplays(t *testing.T, e *pgEnv) {
	q := `mutation { registerDevice(input: {platform: "android", pushToken: "fcm_token_for_the_graphql_test_00000000idem", bundleId: "dev.maktaba.tv"}) }`
	_, first := e.gql(member, q, nil, "Idempotency-Key", "idem-1")
	_, second := e.gql(member, q, nil, "Idempotency-Key", "idem-1")
	if fmt.Sprint(first) != fmt.Sprint(second) || first["errors"] != nil {
		t.Fatalf("first %v second %v", first, second)
	}
	var n int
	_ = e.db.QueryRow(`SELECT count(*) FROM devices WHERE push_token = 'fcm_token_for_the_graphql_test_00000000idem'`).Scan(&n)
	if n != 1 {
		t.Fatalf("devices registered = %d", n)
	}
	// Same key, different body: the REST conflict.
	status, env := e.gql(member, `mutation { registerDevice(input: {platform: "android", pushToken: "fcm_token_for_the_graphql_test_0000000other", bundleId: "b"}) }`, nil, "Idempotency-Key", "idem-1")
	if status != http.StatusConflict || !strings.HasSuffix(fmt.Sprint(env["type"]), "idempotency-key-conflict") {
		t.Fatalf("reused key with another body: %d %v", status, env)
	}
}

// purgeNeedsConfirm: a purge needs the REST confirmation; the REST warning header comes back.
func purgeNeedsConfirm(t *testing.T, e *pgEnv) {
	const videoP = "00000000-0000-4000-8000-0000000a00ff"
	e.exec(`INSERT INTO videos (id, library_id, path, filename, content_hash, size_bytes, mtime, state)
		VALUES ($1, $2, '/nonexistent/purge.mp4', 'purge.mp4', encode(sha256(convert_to($3::text,'UTF8')),'hex'), 1, now(), 'ready')`, videoP, libA, videoP)
	q := `mutation($id: ID!, $c: String) { deleteVideo(id: $id, purge: true, confirm: $c) { deleted warning } }`
	e.fails(admin, "confirmation-required", q, map[string]any{"id": videoP})
	r := e.ok(admin, "deleteVideo", q, map[string]any{"id": videoP, "c": videoP}).(map[string]any)
	if r["deleted"] != true || r["warning"] != "file-not-found" {
		t.Fatalf("purge = %v", r)
	}
}

// nestedFieldsAreBatched (DataLoader): a page of videos with seven nested joins costs one batch
// query per join, not one per video.
func nestedFieldsAreBatched(t *testing.T, e *pgEnv) {
	for i := 0; i < 40; i++ {
		e.exec(`INSERT INTO videos (library_id, path, filename, content_hash, size_bytes, mtime, state) VALUES ($1,$2,$2,encode(sha256(convert_to($2,'UTF8')),'hex'),1,now(),'ready')`, libA, fmt.Sprintf("/m/%d.mp4", i))
	}
	counted := &countingConnector{base: e.db.Driver(), dsn: e.dsn}
	cdb := sql.OpenDB(counted)
	defer cdb.Close()
	r := router.New(router.Deps{})
	router.MountP6(r, router.P6Deps{DB: cdb, EventHub: e.hub})
	h := http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		req = req.WithContext(principal.WithPrincipal(req.Context(), &principal.Principal{UserID: adminID, IsAdmin: true, AccessAllLibraries: true}))
		r.ServeHTTP(w, req)
	})
	q := `{ videos(limit: 50) { items { id tags { name } audioTracks { codec } mediaInfo { width } chapters { title } subtitles { url } playbackState { positionSec } latestTranscript { id } } } }`
	b, _ := json.Marshal(map[string]any{"query": q})
	counted.n.Store(0)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/graphql", bytes.NewReader(b)))
	var env struct {
		Data struct {
			Videos struct{ Items []any } `json:"videos"`
		} `json:"data"`
		Errors any `json:"errors"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &env); err != nil || env.Errors != nil {
		t.Fatalf("%s", rec.Body.String())
	}
	if n := len(env.Data.Videos.Items); n != 43 {
		t.Fatalf("items = %d", n)
	}
	// 1 list query + 7 batch queries.
	if n := counted.n.Load(); n > 8 {
		t.Fatalf("%d SQL statements for 43 videos with 7 joins: N+1", n)
	}
}

// countingConnector counts the statements run through its connections.
type countingConnector struct {
	base driver.Driver
	dsn  string
	n    atomic.Int64
}

func (c *countingConnector) Connect(context.Context) (driver.Conn, error) {
	conn, err := c.base.Open(c.dsn)
	if err != nil {
		return nil, err
	}
	return &countingConn{Conn: conn, n: &c.n}, nil
}

func (c *countingConnector) Driver() driver.Driver { return c.base }

type countingConn struct {
	driver.Conn
	n *atomic.Int64
}

func (c *countingConn) QueryContext(ctx context.Context, q string, args []driver.NamedValue) (driver.Rows, error) {
	c.n.Add(1)
	return c.Conn.(driver.QueryerContext).QueryContext(ctx, q, args)
}

func (c *countingConn) ExecContext(ctx context.Context, q string, args []driver.NamedValue) (driver.Result, error) {
	c.n.Add(1)
	return c.Conn.(driver.ExecerContext).ExecContext(ctx, q, args)
}
