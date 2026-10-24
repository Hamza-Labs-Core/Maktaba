package logs

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/go-chi/chi/v5"
)

// A machine's log files over a window: the home's from its supervisor, a machine's over
// the channel, the query checked before it goes anywhere, a machine that is away or does
// not answer said so with a code; the admin bundle carries the files.
func TestFilesAndTheBundle(t *testing.T) {
	var asked []string
	h := &Handler{Ring: seedRing(t), FileSource: func(_ context.Context, node string, q url.Values) (json.RawMessage, error) {
		asked = append(asked, node+"?"+q.Encode())
		switch node {
		case "":
			return json.RawMessage(`{"entries":[{"ts":"2026-10-24T05:30:00Z","file":"app.log","service":"app","msg":"update failed","raw":"2026-10-24T05:30:00Z update failed"},
				{"ts":"2026-10-24T05:30:01Z","file":"server.log","service":"server","msg":"dump","raw":"[serve] dump"}],
				"files":[{"name":"server.log"},{"name":"app.log"},{"name":"launcher.log","missing":true}]}`), nil
		case "away":
			return nil, ErrNodeOffline
		case "mute":
			return nil, ErrNodeNoAnswer
		}
		return json.RawMessage(`{"entries":[{"file":"server.log","raw":"node line"}],"files":[{"name":"server.log"}]}`), nil
	}}
	r := chi.NewRouter()
	h.Mount(r)
	get := func(path string) *httptest.ResponseRecorder {
		rec := httptest.NewRecorder()
		r.ServeHTTP(rec, adminCtx(httptest.NewRequest(http.MethodGet, path, nil)))
		return rec
	}
	rec := get("/api/admin/logs/files?files=app,server&since=2026-10-24T05:29:00Z&until=2026-10-24T05:40:00Z&limit=99999&q=dropped")
	var doc struct {
		Node    string `json:"node"`
		Entries []struct {
			Msg string `json:"msg"`
		} `json:"entries"`
	}
	if rec.Code != http.StatusOK || json.Unmarshal(rec.Body.Bytes(), &doc) != nil || len(doc.Entries) != 2 || doc.Node != "" {
		t.Fatalf("the home's files: %d %s", rec.Code, rec.Body.String())
	}
	if len(asked) != 1 || asked[0] != "?files=app%2Cserver&limit=10000&since=2026-10-24T05%3A29%3A00Z&until=2026-10-24T05%3A40%3A00Z" {
		t.Fatalf("what the supervisor was asked: %v", asked)
	}
	for path, want := range map[string]int{
		"/api/admin/logs/files?files=../../etc":       http.StatusBadRequest,
		"/api/admin/logs/files?since=yesterday":       http.StatusBadRequest,
		"/api/admin/logs/files?node=a/b":              http.StatusBadRequest,
		"/api/admin/logs/files?node=away":             http.StatusServiceUnavailable,
		"/api/admin/logs/files?node=mute":             http.StatusBadGateway,
		"/api/admin/logs/files?node=7a2d3b4c-5e6f-4a": http.StatusOK,
	} {
		if rec := get(path); rec.Code != want {
			t.Errorf("%s: %d, want %d (%s)", path, rec.Code, want, rec.Body.String())
		}
	}
	if rec := get("/api/admin/logs/files?node=away"); !strings.Contains(rec.Body.String(), `"code":"node_offline"`) {
		t.Errorf("the code: %s", rec.Body.String())
	}
	// not an admin: no files
	plain := httptest.NewRecorder()
	r.ServeHTTP(plain, httptest.NewRequest(http.MethodGet, "/api/admin/logs/files", nil))
	if plain.Code == http.StatusOK {
		t.Fatal("the files without an admin")
	}

	rec = get("/api/admin/logs/export?format=json&since=2026-10-24T05:29:00Z&node=7a2d3b4c")
	var bundle map[string]json.RawMessage
	if rec.Code != http.StatusOK || json.Unmarshal(rec.Body.Bytes(), &bundle) != nil {
		t.Fatalf("the bundle: %d %s", rec.Code, rec.Body.String())
	}
	str := func(k string) string {
		var s string
		_ = json.Unmarshal(bundle[k], &s)
		return s
	}
	if str("files/app.log") != "2026-10-24T05:30:00Z update failed\n" || str("files/server.log") != "[serve] dump\n" ||
		str("node-7a2d3b4c/files/server.log") != "node line\n" {
		t.Fatalf("the bundle's files: %v", bundleKeys(bundle))
	}
	if _, ok := bundle["files/launcher.log"]; ok {
		t.Error("a missing file in the bundle")
	}
	// a machine that is away: a note, not a failed bundle
	rec = get("/api/admin/logs/export?format=json&node=away")
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), "node-away/files-unavailable.txt") {
		t.Fatalf("a machine away: %d %s", rec.Code, rec.Body.String())
	}
	// the user's own export has no files
	rec = httptest.NewRecorder()
	r.ServeHTTP(rec, adminCtx(httptest.NewRequest(http.MethodGet, "/api/diagnostics/export?format=json", nil)))
	if rec.Code != http.StatusOK || strings.Contains(rec.Body.String(), "files/") {
		t.Fatalf("the user's export: %d %s", rec.Code, rec.Body.String())
	}
}

// Without a supervisor (a bare api), no file is there; a machine still needs its agent.
func TestFilesWithoutASupervisor(t *testing.T) {
	h := &Handler{FileSource: func(context.Context, string, url.Values) (json.RawMessage, error) { return nil, ErrNoSupervisor }}
	raw, err := h.files(context.Background(), "", url.Values{"files": {"app"}})
	if err != nil || !strings.Contains(string(raw), `"name":"app.log","truncated":false`) && !strings.Contains(string(raw), `"missing":true`) {
		t.Fatalf("%s %v", raw, err)
	}
	if _, err := h.files(context.Background(), "n1", url.Values{}); !errors.Is(err, ErrNodeOffline) {
		t.Fatalf("a machine: %v", err)
	}
}

func bundleKeys(m map[string]json.RawMessage) []string {
	var out []string
	for k := range m {
		out = append(out, k)
	}
	return out
}
