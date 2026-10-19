package router_test

// Access and audit rules of the REST routes GraphQL runs in process (PR
// #134's findings), against a real migrated Postgres behind the
// production router. Runs when MAKTABA_TEST_PG_URL names a Postgres the
// user may create databases on (CI's integration job sets it), e.g.
//
//	MAKTABA_TEST_PG_URL=postgres://maktaba:maktaba@127.0.0.1:5432/maktaba?sslmode=disable \
//	    go test ./internal/router/ -run PGAccess

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/Hamza-Labs-Core/Maktaba/api/internal/auth/principal"
	"github.com/Hamza-Labs-Core/Maktaba/api/internal/cluster/clustertest"
	"github.com/Hamza-Labs-Core/Maktaba/api/internal/graphql"
	grpcstreaming "github.com/Hamza-Labs-Core/Maktaba/api/internal/grpcclients/streaming"
	"github.com/Hamza-Labs-Core/Maktaba/api/internal/idempotency"
	"github.com/Hamza-Labs-Core/Maktaba/api/internal/router"
)

const (
	acAdmin  = "00000000-0000-4000-8000-0000000001a1"
	acMember = "00000000-0000-4000-8000-0000000001b1" // library A only
	acOther  = "00000000-0000-4000-8000-0000000001c1" // library A only
	acLibA   = "00000000-0000-4000-8000-00000001a000"
	acLibB   = "00000000-0000-4000-8000-00000001b000"
	acVidA1  = "00000000-0000-4000-8000-0000001a0001"
	acVidB1  = "00000000-0000-4000-8000-0000001b0001"
	acColA   = "00000000-0000-4000-8000-0000000c100a"
	acColB   = "00000000-0000-4000-8000-0000000c100b"
	acSmart  = "00000000-0000-4000-8000-0000000c100c"
	acSess   = "00000000-0000-4000-8000-000000005101" // acOther's session
)

type acStreaming struct{}

func (acStreaming) OpenSession(_ context.Context, req grpcstreaming.OpenSessionRequest) (grpcstreaming.OpenSessionResponse, error) {
	return grpcstreaming.OpenSessionResponse{SessionID: uuid.NewString(), Mode: "direct", DirectURL: "/stream/direct/" + req.VideoID,
		ExpiresAt: time.Now().Add(time.Hour)}, nil
}
func (acStreaming) CloseSession(context.Context, string) error   { return nil }
func (acStreaming) EvictHashCache(context.Context, string) error { return nil }
func (acStreaming) GetCapabilities(context.Context) (grpcstreaming.Capabilities, error) {
	return grpcstreaming.Capabilities{}, nil
}
func (acStreaming) HealthCheck(context.Context) (grpcstreaming.Status, error) {
	return grpcstreaming.Status{}, nil
}

type acEnv struct {
	t *testing.T
	h http.Handler
}

func setupAccess(t *testing.T) (*acEnv, func(q string, args ...any) int) {
	t.Helper()
	db, _ := clustertest.ScratchDB(t)
	exec := func(q string, args ...any) {
		t.Helper()
		if _, err := db.Exec(q, args...); err != nil {
			t.Fatalf("seed %q: %v", q, err)
		}
	}
	count := func(q string, args ...any) int {
		t.Helper()
		var n int
		if err := db.QueryRow(q, args...).Scan(&n); err != nil {
			t.Fatalf("count %q: %v", q, err)
		}
		return n
	}
	exec(`INSERT INTO users (id, username, pw_hash, is_admin) VALUES ($1,'ac-admin','x',true), ($2,'ac-member','x',false), ($3,'ac-other','x',false)`,
		acAdmin, acMember, acOther)
	exec(`INSERT INTO libraries (id, name, roots) VALUES ($1,'AcAlpha','{/media/ac-alpha}'), ($2,'AcBeta','{/media/ac-beta}')`, acLibA, acLibB)
	now := time.Now().UTC()
	for _, v := range []struct{ id, lib string }{{acVidA1, acLibA}, {acVidB1, acLibB}} {
		exec(`INSERT INTO videos (id, library_id, path, filename, content_hash, size_bytes, mtime, state, title, duration_sec, updated_at, created_at)
			VALUES ($1,$2,$3,$4,encode(sha256(convert_to($6::text,'UTF8')),'hex'),1000,$5,'ready','t',100,$5,$5)`,
			v.id, v.lib, "/media/ac/"+v.id+".mp4", v.id+".mp4", now, v.id)
	}
	exec(`INSERT INTO collections (id, library_id, name) VALUES ($1,$2,'A picks'), ($3,$4,'B picks')`, acColA, acLibA, acColB, acLibB)
	// colA holds a video of library B too: hidden from a library-A caller
	exec(`INSERT INTO collection_items (collection_id, video_id, position) VALUES ($1,$2,0), ($1,$3,1), ($4,$3,0)`, acColA, acVidA1, acVidB1, acColB)
	// a smart collection of library A whose query names library B
	exec(`INSERT INTO collections (id, library_id, name, is_smart, smart_query) VALUES ($1,$2,'A smart',true,$3)`,
		acSmart, acLibA, `{"library_id":"`+acLibB+`"}`)
	exec(`INSERT INTO streaming_sessions (id, user_id, video_id, mode, opened_at, last_seen_at) VALUES ($1,$2,$3,'direct',$4,$4)`,
		acSess, acOther, acVidA1, now)

	r := router.New(router.Deps{IdempotencyStore: idempotency.NewMemoryStore()})
	router.MountP6(r, router.P6Deps{DB: db, StreamingClient: acStreaming{}})
	h := http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		var p *principal.Principal
		switch req.Header.Get("X-Test-As") {
		case "admin":
			p = &principal.Principal{UserID: acAdmin, IsAdmin: true, AccessAllLibraries: true, Source: principal.SourceJWT}
		case "member":
			p = &principal.Principal{UserID: acMember, Libraries: []string{acLibA}, Source: principal.SourceJWT}
		case "other":
			p = &principal.Principal{UserID: acOther, Libraries: []string{acLibA}, Source: principal.SourceJWT}
		}
		if p != nil {
			req = req.WithContext(principal.WithPrincipal(req.Context(), p))
		}
		r.ServeHTTP(w, req)
	})
	return &acEnv{t: t, h: h}, count
}

// do runs one request and decodes a JSON body (nil when empty).
func (e *acEnv) do(who, method, path string, body any) (int, map[string]any) {
	e.t.Helper()
	var rd *bytes.Reader
	if body != nil {
		b, _ := json.Marshal(body)
		rd = bytes.NewReader(b)
	} else {
		rd = bytes.NewReader(nil)
	}
	req := httptest.NewRequest(method, path, rd)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Test-As", who)
	rec := httptest.NewRecorder()
	e.h.ServeHTTP(rec, req)
	var out map[string]any
	_ = json.Unmarshal(rec.Body.Bytes(), &out)
	return rec.Code, out
}

func (e *acEnv) want(status int, who, method, path string, body any) map[string]any {
	e.t.Helper()
	got, out := e.do(who, method, path, body)
	if got != status {
		e.t.Fatalf("%s %s %s = %d (%v), want %d", who, method, path, got, out, status)
	}
	return out
}

func itemIDs(out map[string]any, key string) []string {
	var ids []string
	items, _ := out["items"].([]any)
	for _, it := range items {
		if m, ok := it.(map[string]any); ok {
			if s, ok := m[key].(string); ok {
				ids = append(ids, s)
			}
		}
	}
	return ids
}

func contains(xs []string, x string) bool {
	for _, y := range xs {
		if y == x {
			return true
		}
	}
	return false
}

func TestPGAccessStreamSessions(t *testing.T) {
	e, count := setupAccess(t)
	path := "/api/stream/sessions/" + acSess

	// another user (same library) cannot see, report progress on, or close it
	e.want(http.StatusNotFound, "member", http.MethodGet, path, nil)
	e.want(http.StatusNotFound, "member", http.MethodPost, path+"/progress", map[string]any{"position_sec": 10})
	e.want(http.StatusNotFound, "member", http.MethodDelete, path, nil)
	if n := count(`SELECT count(*) FROM streaming_sessions WHERE id=$1 AND closed_at IS NULL`, acSess); n != 1 {
		t.Fatal("another user closed the session")
	}
	if n := count(`SELECT count(*) FROM playback_state WHERE video_id=$1`, acVidA1); n != 0 {
		t.Fatal("another user's progress was written")
	}
	// the same 404 as an unknown session
	e.want(http.StatusNotFound, "member", http.MethodGet, "/api/stream/sessions/"+uuid.NewString(), nil)

	// the owner may
	if s := e.want(http.StatusOK, "other", http.MethodGet, path, nil); s["user_id"] != acOther {
		t.Fatalf("owner read %v", s)
	}
	e.want(http.StatusOK, "other", http.MethodPost, path+"/progress", map[string]any{"position_sec": 10})
	if n := count(`SELECT count(*) FROM playback_state WHERE user_id=$1 AND video_id=$2`, acOther, acVidA1); n != 1 {
		t.Fatal("owner's progress not written")
	}

	// an admin may read and close it
	e.want(http.StatusOK, "admin", http.MethodGet, path, nil)
	e.want(http.StatusNoContent, "admin", http.MethodDelete, path, nil)
	if n := count(`SELECT count(*) FROM streaming_sessions WHERE id=$1 AND closed_at IS NOT NULL`, acSess); n != 1 {
		t.Fatal("admin did not close the session")
	}

	// a session the member opens is theirs
	s := e.want(http.StatusCreated, "member", http.MethodPost, "/api/stream/sessions", map[string]any{"video_id": acVidA1})
	sid, _ := s["session_id"].(string)
	e.want(http.StatusOK, "member", http.MethodGet, "/api/stream/sessions/"+sid, nil)
	e.want(http.StatusNotFound, "other", http.MethodGet, "/api/stream/sessions/"+sid, nil)
}

func TestPGAccessCollections(t *testing.T) {
	e, count := setupAccess(t)

	got := itemIDs(e.want(http.StatusOK, "member", http.MethodGet, "/api/collections", nil), "id")
	if !contains(got, acColA) || contains(got, acColB) {
		t.Fatalf("member lists %v; want A's collections only", got)
	}
	if got := itemIDs(e.want(http.StatusOK, "admin", http.MethodGet, "/api/collections", nil), "id"); !contains(got, acColB) {
		t.Fatalf("admin lists %v", got)
	}

	// another library's collection is 404, like an unknown one
	e.want(http.StatusNotFound, "member", http.MethodGet, "/api/collections/"+acColB, nil)
	e.want(http.StatusNotFound, "member", http.MethodGet, "/api/collections/"+acColB+"/items", nil)
	e.want(http.StatusNotFound, "member", http.MethodGet, "/api/collections/"+uuid.NewString(), nil)
	e.want(http.StatusOK, "admin", http.MethodGet, "/api/collections/"+acColB, nil)

	// items of a hidden library are filtered out
	items := itemIDs(e.want(http.StatusOK, "member", http.MethodGet, "/api/collections/"+acColA+"/items", nil), "video_id")
	if len(items) != 1 || items[0] != acVidA1 {
		t.Fatalf("member items %v; want only A1", items)
	}
	if items := itemIDs(e.want(http.StatusOK, "admin", http.MethodGet, "/api/collections/"+acColA+"/items", nil), "video_id"); len(items) != 2 {
		t.Fatalf("admin items %v", items)
	}
	// a smart query naming library B returns nothing to a library-A caller
	if items := itemIDs(e.want(http.StatusOK, "member", http.MethodGet, "/api/collections/"+acSmart+"/items", nil), "video_id"); len(items) != 0 {
		t.Fatalf("member smart items %v", items)
	}
	if items := itemIDs(e.want(http.StatusOK, "admin", http.MethodGet, "/api/collections/"+acSmart+"/items", nil), "video_id"); !contains(items, acVidB1) {
		t.Fatalf("admin smart items %v", items)
	}

	// writes: role check first, then the same visibility
	e.want(http.StatusForbidden, "member", http.MethodPatch, "/api/collections/"+acColA, map[string]any{"name": "x"})
	e.want(http.StatusForbidden, "member", http.MethodDelete, "/api/collections/"+acColB, nil)
	e.want(http.StatusUnprocessableEntity, "admin", http.MethodPost, "/api/collections/"+acColA+"/items",
		map[string]any{"video_id": uuid.NewString(), "position": 3})

	// and every admin write is audited
	created := e.want(http.StatusCreated, "admin", http.MethodPost, "/api/collections", map[string]any{"library_id": acLibA, "name": "New"})
	cid, _ := created["id"].(string)
	e.want(http.StatusOK, "admin", http.MethodPatch, "/api/collections/"+cid, map[string]any{"name": "Renamed"})
	e.want(http.StatusOK, "admin", http.MethodPost, "/api/collections/"+cid+"/items", map[string]any{"video_id": acVidA1, "position": 1})
	e.want(http.StatusNoContent, "admin", http.MethodDelete, "/api/collections/"+cid+"/items/"+acVidA1, nil)
	e.want(http.StatusNoContent, "admin", http.MethodDelete, "/api/collections/"+cid, nil)
	for _, action := range []string{"collection-created", "collection-patched", "collection-item-added", "collection-item-removed", "collection-deleted"} {
		if n := count(`SELECT count(*) FROM audit_log WHERE category='library' AND action=$1 AND actor_user_id=$2 AND target_id=$3`,
			action, acAdmin, acLibA); n != 1 {
			t.Errorf("audit %s: %d rows, want 1", action, n)
		}
	}
}

func TestPGAccessAuditLibrariesVideos(t *testing.T) {
	e, count := setupAccess(t)
	audited := func(action, target string) {
		t.Helper()
		if n := count(`SELECT count(*) FROM audit_log WHERE category='library' AND action=$1 AND actor_user_id=$2 AND target_id=$3`,
			action, acAdmin, target); n != 1 {
			t.Errorf("audit %s on %s: %d rows, want 1", action, target, n)
		}
	}

	// library create / patch / scan / delete
	root := t.TempDir()
	lib := e.want(http.StatusCreated, "admin", http.MethodPost, "/api/libraries", map[string]any{"name": "Gamma", "roots": []string{root}})
	lid, _ := lib["id"].(string)
	audited("library-created", lid)
	e.want(http.StatusOK, "admin", http.MethodPatch, "/api/libraries/"+lid, map[string]any{"name": "Gamma 2"})
	audited("settings-changed", lid)
	e.want(http.StatusForbidden, "member", http.MethodPost, "/api/libraries/"+lid+"/scan", nil)
	e.want(http.StatusAccepted, "admin", http.MethodPost, "/api/libraries/"+lid+"/scan", nil)
	audited("scan-triggered", lid)
	// a refused delete (not admin) writes nothing; the admin's does
	e.want(http.StatusForbidden, "member", http.MethodDelete, "/api/libraries/"+lid, nil)
	e.want(http.StatusNoContent, "admin", http.MethodDelete, "/api/libraries/"+lid, nil)
	audited("library-deleted", lid)

	// video patch / process / reprocess / delete
	e.want(http.StatusOK, "admin", http.MethodPatch, "/api/videos/"+acVidA1, map[string]any{"title": "New title"})
	audited("video-patched", acVidA1)
	e.want(http.StatusOK, "admin", http.MethodPost, "/api/videos/"+acVidA1+"/process", map[string]any{"stage": "transcribe"})
	audited("video-process", acVidA1)
	e.want(http.StatusOK, "admin", http.MethodPost, "/api/videos/"+acVidA1+"/reprocess", map[string]any{"from_stage": "transcribe"})
	audited("video-reprocess", acVidA1)
	e.want(http.StatusForbidden, "member", http.MethodDelete, "/api/videos/"+acVidA1, nil)
	e.want(http.StatusNoContent, "admin", http.MethodDelete, "/api/videos/"+acVidA1, nil)
	audited("video-deleted", acVidA1)
	if n := count(`SELECT count(*) FROM audit_log WHERE actor_user_id=$1`, acMember); n != 0 {
		t.Errorf("refused calls wrote %d audit rows", n)
	}
}

// gqlPost sends a raw GraphQL-over-HTTP body.
func (e *acEnv) gqlPost(who string, body map[string]any) (int, map[string]any) {
	e.t.Helper()
	return e.do(who, http.MethodPost, "/graphql", body)
}

func errCode(out map[string]any) string {
	errs, _ := out["errors"].([]any)
	if len(errs) == 0 {
		return ""
	}
	m, _ := errs[0].(map[string]any)
	ext, _ := m["extensions"].(map[string]any)
	c, _ := ext["code"].(string)
	return c
}

func apq(hash string) map[string]any {
	return map[string]any{"persistedQuery": map[string]any{"version": 1, "sha256Hash": hash}}
}

func TestPGAccessPersistedQueries(t *testing.T) {
	e, count := setupAccess(t)
	q := `{ libraries { id } }`
	hash := graphql.QueryHash(q)

	// miss: Apollo's PersistedQueryNotFound, HTTP 200
	status, out := e.gqlPost("member", map[string]any{"extensions": apq(hash)})
	if status != http.StatusOK || errCode(out) != graphql.CodePersistedNotFound {
		t.Fatalf("miss = %d %v", status, out)
	}
	if msg := out["errors"].([]any)[0].(map[string]any)["message"]; msg != graphql.PersistedQueryNotFound {
		t.Fatalf("miss message %v", msg)
	}
	// a hash that is not the text's sha256
	status, out = e.gqlPost("member", map[string]any{"query": q, "extensions": apq(strings.Repeat("0", 64))})
	if status != http.StatusBadRequest || errCode(out) != graphql.CodePersistedHashMismatch {
		t.Fatalf("mismatch = %d %v", status, out)
	}
	// the retry with the text runs it and registers it
	status, out = e.gqlPost("member", map[string]any{"query": q, "extensions": apq(hash)})
	if status != http.StatusOK || out["errors"] != nil {
		t.Fatalf("register = %d %v", status, out)
	}
	if n := count(`SELECT count(*) FROM graphql_persisted_queries WHERE sha256=$1`, hash); n != 1 {
		t.Fatalf("registered rows %d", n)
	}
	// hit, by Apollo extension and by persistedQueryId
	for _, body := range []map[string]any{{"extensions": apq(hash)}, {"persistedQueryId": hash}} {
		status, out = e.gqlPost("member", body)
		if status != http.StatusOK || out["errors"] != nil || out["data"] == nil {
			t.Fatalf("hit %v = %d %v", body, status, out)
		}
	}

	// allowlist mode (the runtime setting, default off)
	other := `{ collections { id } }`
	if status, out := e.gqlPost("member", map[string]any{"query": other}); status != http.StatusOK || out["errors"] != nil {
		t.Fatalf("allowlist off: plain query = %d %v", status, out)
	}
	e.want(http.StatusOK, "admin", http.MethodPatch, "/api/settings", map[string]any{"graphql.persisted_only": true})
	status, out = e.gqlPost("member", map[string]any{"query": other})
	if status != http.StatusForbidden || errCode(out) != graphql.CodePersistedNotAllowed {
		t.Fatalf("allowlist: unknown text = %d %v", status, out)
	}
	// a non-admin cannot add to the allowlist
	status, out = e.gqlPost("member", map[string]any{"query": other, "extensions": apq(graphql.QueryHash(other))})
	if status != http.StatusForbidden || errCode(out) != graphql.CodePersistedNotAllowed {
		t.Fatalf("allowlist: member register = %d %v", status, out)
	}
	if n := count(`SELECT count(*) FROM graphql_persisted_queries WHERE sha256=$1`, graphql.QueryHash(other)); n != 0 {
		t.Fatal("a member registered a query in allowlist mode")
	}
	// stored operations still run, by hash or by text
	if status, out := e.gqlPost("member", map[string]any{"persistedQueryId": hash}); status != http.StatusOK || out["errors"] != nil {
		t.Fatalf("allowlist: stored hash = %d %v", status, out)
	}
	if status, out := e.gqlPost("member", map[string]any{"query": q}); status != http.StatusOK || out["errors"] != nil {
		t.Fatalf("allowlist: stored text = %d %v", status, out)
	}
	// an admin runs and registers new operations
	status, out = e.gqlPost("admin", map[string]any{"query": other, "extensions": apq(graphql.QueryHash(other))})
	if status != http.StatusOK || out["errors"] != nil {
		t.Fatalf("allowlist: admin = %d %v", status, out)
	}
	if status, out := e.gqlPost("member", map[string]any{"query": other}); status != http.StatusOK || out["errors"] != nil {
		t.Fatalf("allowlist: admin-registered text for member = %d %v", status, out)
	}
}
