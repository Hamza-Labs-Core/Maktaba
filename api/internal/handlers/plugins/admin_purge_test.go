package plugins

import (
	"context"
	"database/sql"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/go-chi/chi/v5"
	_ "modernc.org/sqlite"

	"github.com/Hamza-Labs-Core/Maktaba/api/internal/auth/principal"
	svc "github.com/Hamza-Labs-Core/Maktaba/api/internal/components"
	pl "github.com/Hamza-Labs-Core/Maktaba/api/internal/plugins"
)

func sqliteComponentsDB(t *testing.T) *sql.DB {
	t.Helper()
	db, err := sql.Open("sqlite", filepath.Join(t.TempDir(), "components.db"))
	if err != nil {
		t.Fatal(err)
	}
	db.SetMaxOpenConns(1)
	t.Cleanup(func() { _ = db.Close() })
	base := `
CREATE TABLE cluster_nodes (id TEXT PRIMARY KEY, name TEXT NOT NULL DEFAULT '');
CREATE TABLE audit_log (id INTEGER PRIMARY KEY AUTOINCREMENT, category TEXT NOT NULL, action TEXT NOT NULL,
	actor_user_id TEXT, target_id TEXT, payload TEXT NOT NULL DEFAULT '{}',
	ts TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ', 'now')), actor_source TEXT, target_kind TEXT);
CREATE TABLE job_stages (stage TEXT PRIMARY KEY, label TEXT NOT NULL, subject_kinds TEXT NOT NULL,
	pool TEXT NOT NULL, lane TEXT NOT NULL, lane_order INTEGER NOT NULL DEFAULT 0, version INTEGER NOT NULL DEFAULT 1,
	debounce_sec INTEGER NOT NULL DEFAULT 0, debounce_max_sec INTEGER NOT NULL DEFAULT 0,
	max_attempts INTEGER NOT NULL DEFAULT 3, edges TEXT NOT NULL DEFAULT '[]', may_request TEXT NOT NULL DEFAULT '[]',
	entry_for TEXT NOT NULL DEFAULT '[]', legacy INTEGER NOT NULL DEFAULT 0, hidden INTEGER NOT NULL DEFAULT 0,
	external_write INTEGER NOT NULL DEFAULT 0, write_mode TEXT NOT NULL DEFAULT 'auto',
	placement TEXT NOT NULL DEFAULT 'any', enabled INTEGER NOT NULL DEFAULT 1);
CREATE TABLE processing_jobs (id INTEGER PRIMARY KEY, stage TEXT NOT NULL, state TEXT NOT NULL DEFAULT 'pending');
`
	for _, stmt := range strings.Split(base, ";\n") {
		if strings.TrimSpace(stmt) == "" {
			continue
		}
		if _, err := db.Exec(stmt); err != nil {
			t.Fatalf("base DDL: %v\n%s", err, stmt)
		}
	}
	apply := func(name string, stripIfNotExists bool) {
		raw, err := os.ReadFile(filepath.Join("..", "..", "..", "..", "shared", "db", "migrations", name))
		if err != nil {
			t.Fatal(err)
		}
		up := string(raw)
		if i := strings.Index(up, "-- +goose Down"); i >= 0 {
			up = up[:i]
		}
		for _, block := range strings.Split(up, "-- +goose StatementBegin") {
			if i := strings.Index(block, "-- +goose StatementEnd"); i >= 0 {
				s := strings.TrimSpace(block[:i])
				if s == "" {
					continue
				}
				if stripIfNotExists {
					s = strings.Replace(s, "ADD COLUMN IF NOT EXISTS", "ADD COLUMN", 1)
				}
				if _, err := db.Exec(s); err != nil {
					t.Fatalf("%s: %v\n%s", name, err, s)
				}
			}
		}
	}
	apply("0178_components.sqlite.sql", false)
	apply("0181_component_licence.sqlite.sql", true)
	return db
}

// #1463 item 3: DELETE /api/plugins/{id}?purge=true on a Maktaba plugin checks whether
// another enabled component requires it before returning plugin_data_kept.
func TestRemovePublishedPurgeReportsRequiredByFirst(t *testing.T) {
	db := sqliteComponentsDB(t)
	s := svc.NewService(db)
	l := &pl.Lifecycle{DB: db, Components: s}
	r := chi.NewRouter()
	(&Admin{L: l}).Mount(r)
	admin := &principal.Principal{UserID: "00000000-0000-0000-0000-0000000000a1", IsAdmin: true, AccessAllLibraries: true}

	call := func(method, path string) (int, map[string]any) {
		t.Helper()
		req := httptest.NewRequest(method, path, nil)
		req = req.WithContext(principal.WithPrincipal(req.Context(), admin))
		rec := httptest.NewRecorder()
		r.ServeHTTP(rec, req)
		var out map[string]any
		_ = json.Unmarshal(rec.Body.Bytes(), &out)
		return rec.Code, out
	}

	ctx := context.Background()
	actor := svc.Actor{By: "owner", Source: "web"}
	for _, cid := range []string{"transcription", "subtitles"} {
		if _, err := s.Apply(ctx, svc.Request{Changes: []svc.Change{{ID: cid, Want: "enabled"}}}, actor); err != nil {
			t.Fatalf("enable %s: %v", cid, err)
		}
	}

	// Required plugin with ?purge=true must report component_required_by, not plugin_data_kept.
	code, got := call(http.MethodDelete, "/api/plugins/tv.maktaba.transcription?purge=true")
	if code != http.StatusConflict || got["code"] != svc.CodeRequiredBy {
		t.Fatalf("purge required plugin: %d %v, want 409 %s", code, got, svc.CodeRequiredBy)
	}
	reqBy, _ := got["required_by"].([]any)
	names := make([]string, 0, len(reqBy))
	for _, x := range reqBy {
		names = append(names, x.(string))
	}
	if !slices.Contains(names, "subtitles") {
		t.Fatalf("required_by = %v, want subtitles", names)
	}
	if d, err := s.Show(ctx, "transcription"); err != nil || d.Want != "enabled" {
		t.Fatalf("refused purge changed transcription: %+v %v", d.Want, err)
	}

	// Once subtitles is disabled, ?purge=true reports plugin_data_kept and leaves transcription enabled.
	if _, err := s.Apply(ctx, svc.Request{Changes: []svc.Change{{ID: "subtitles", Want: "installed"}}}, actor); err != nil {
		t.Fatal(err)
	}
	code, got = call(http.MethodDelete, "/api/plugins/tv.maktaba.transcription?purge=true")
	if code != http.StatusConflict || got["code"] != pl.CodeDataKept {
		t.Fatalf("purge unrequired plugin: %d %v, want 409 %s", code, got, pl.CodeDataKept)
	}
	if d, err := s.Show(ctx, "transcription"); err != nil || d.Want != "enabled" {
		t.Fatalf("refused purge changed transcription: %+v %v", d.Want, err)
	}

	// A non-required plugin (thumbnails) with ?purge=true reports plugin_data_kept and stays enabled.
	code, got = call(http.MethodDelete, "/api/plugins/tv.maktaba.thumbnails?purge=true")
	if code != http.StatusConflict || got["code"] != pl.CodeDataKept {
		t.Fatalf("purge thumbnails: %d %v, want 409 %s", code, got, pl.CodeDataKept)
	}
	if d, err := s.Show(ctx, "media.thumbnails"); err != nil || d.Want != "enabled" {
		t.Fatalf("refused purge changed media.thumbnails: %+v %v", d.Want, err)
	}
}
