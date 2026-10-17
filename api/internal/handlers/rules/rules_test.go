package rules

import (
	"context"
	"database/sql"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	"github.com/go-chi/chi/v5"
	_ "modernc.org/sqlite"

	"github.com/Hamza-Labs-Core/Maktaba/api/internal/auth/principal"
)

// newTestDB opens a fresh SQLite file with the app_settings table from
// shared/db/migrations/0042_app_settings.sqlite.sql (minus the users FK).
func newTestDB(t *testing.T) *sql.DB {
	t.Helper()
	db, err := sql.Open("sqlite", filepath.Join(t.TempDir(), "rules.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	if _, err := db.Exec(`CREATE TABLE app_settings (
		key        TEXT PRIMARY KEY,
		value      TEXT NOT NULL,
		updated_at TEXT NOT NULL DEFAULT (datetime('now')),
		updated_by TEXT
	)`); err != nil {
		t.Fatal(err)
	}
	return db
}

func newServer(t *testing.T) (*sql.DB, http.Handler) {
	db := newTestDB(t)
	h := &Handler{DB: db}
	r := chi.NewRouter()
	h.Mount(r)
	return db, r
}

func do(t *testing.T, srv http.Handler, method, path, body string, p *principal.Principal) (int, map[string]any) {
	t.Helper()
	req := httptest.NewRequest(method, path, strings.NewReader(body))
	if p != nil {
		req = req.WithContext(principal.WithPrincipal(req.Context(), p))
	}
	rec := httptest.NewRecorder()
	srv.ServeHTTP(rec, req)
	var out map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
		t.Fatalf("%s %s: bad json %q", method, path, rec.Body.String())
	}
	return rec.Code, out
}

var (
	admin = &principal.Principal{UserID: "u-admin", IsAdmin: true}
	user  = &principal.Principal{UserID: "u-user"}
)

func storedRow(t *testing.T, db *sql.DB, section string) (map[string]any, bool) {
	t.Helper()
	var v string
	err := db.QueryRow(`SELECT value FROM app_settings WHERE key = $1`, KeyPrefix+section).Scan(&v)
	if err == sql.ErrNoRows {
		return nil, false
	}
	if err != nil {
		t.Fatal(err)
	}
	var m map[string]any
	if err := json.Unmarshal([]byte(v), &m); err != nil {
		t.Fatal(err)
	}
	return m, true
}

func putRow(t *testing.T, db *sql.DB, section, value string) {
	t.Helper()
	if _, err := db.Exec(`INSERT INTO app_settings (key, value) VALUES ($1, $2)`, KeyPrefix+section, value); err != nil {
		t.Fatal(err)
	}
}

// fieldOf finds a field in a GET response.
func fieldOf(t *testing.T, resp map[string]any, section, name string) map[string]any {
	t.Helper()
	for _, s := range resp["sections"].([]any) {
		sm := s.(map[string]any)
		if sm["key"] != section {
			continue
		}
		for _, f := range sm["fields"].([]any) {
			fm := f.(map[string]any)
			if fm["name"] == name {
				return fm
			}
		}
	}
	t.Fatalf("field %s.%s not found", section, name)
	return nil
}

func sectionOf(t *testing.T, resp map[string]any, key string) map[string]any {
	t.Helper()
	for _, s := range resp["sections"].([]any) {
		if sm := s.(map[string]any); sm["key"] == key {
			return sm
		}
	}
	t.Fatalf("section %s not found", key)
	return nil
}

func TestEmbeddedSchemaParses(t *testing.T) {
	s, err := ParseSchema(schemaJSON)
	if err != nil {
		t.Fatal(err)
	}
	for _, k := range []string{"planner", "groups", "detect"} {
		if s.section(k) == nil {
			t.Errorf("section %s missing", k)
		}
	}
}

func TestGet_NoRowsAllDefaults(t *testing.T) {
	_, srv := newServer(t)
	code, resp := do(t, srv, http.MethodGet, "/api/rules", "", user)
	if code != http.StatusOK {
		t.Fatalf("status %d", code)
	}
	for _, s := range resp["sections"].([]any) {
		sm := s.(map[string]any)
		if len(sm["unknown"].([]any)) != 0 {
			t.Errorf("%v: unexpected unknown", sm["key"])
		}
		for _, f := range sm["fields"].([]any) {
			fm := f.(map[string]any)
			if fm["changed"] != false {
				t.Errorf("%v changed with no rows", fm["name"])
			}
			if _, bad := fm["invalid"]; bad {
				t.Errorf("%v invalid with no rows", fm["name"])
			}
			dj, _ := json.Marshal(fm["default"])
			vj, _ := json.Marshal(fm["value"])
			if string(dj) != string(vj) {
				t.Errorf("%v: value %s != default %s", fm["name"], vj, dj)
			}
		}
	}
	if f := fieldOf(t, resp, "planner", "khutbah_day"); f["kind"] != "str" || f["choices"] == nil || f["label"] == nil {
		t.Errorf("schema keys not passed through: %v", f)
	}
}

func TestPatch_SetResetRoundTrip(t *testing.T) {
	db, srv := newServer(t)

	code, resp := do(t, srv, http.MethodPatch, "/api/rules/planner",
		`{"set":{"min_slot_minutes":45,"khutbah_day":"thu","quran_time_rules":[["03:00","05:00","warsh"]]}}`, admin)
	if code != http.StatusOK {
		t.Fatalf("patch status %d: %v", code, resp)
	}
	if resp["section"] != "planner" || strings.Join(toStrings(resp["changed"]), ",") != "khutbah_day,min_slot_minutes,quran_time_rules" {
		t.Fatalf("patch resp %v", resp)
	}
	row, ok := storedRow(t, db, "planner")
	if !ok || len(row) != 3 || row["min_slot_minutes"] != float64(45) {
		t.Fatalf("stored %v", row)
	}

	_, got := do(t, srv, http.MethodGet, "/api/rules", "", user)
	f := fieldOf(t, got, "planner", "min_slot_minutes")
	if f["value"] != float64(45) || f["changed"] != true || f["default"] != float64(30) {
		t.Errorf("GET after set: %v", f)
	}

	// A second PATCH keeps earlier overrides; reset drops one.
	code, resp = do(t, srv, http.MethodPatch, "/api/rules/planner",
		`{"set":{"dawn_quran":false},"reset":["khutbah_day","quran_time_rules"]}`, admin)
	if code != http.StatusOK {
		t.Fatalf("patch 2 status %d: %v", code, resp)
	}
	if strings.Join(toStrings(resp["changed"]), ",") != "dawn_quran,min_slot_minutes" {
		t.Fatalf("patch 2 resp %v", resp)
	}

	// Resetting everything deletes the row.
	code, resp = do(t, srv, http.MethodPatch, "/api/rules/planner",
		`{"reset":["dawn_quran","min_slot_minutes"]}`, admin)
	if code != http.StatusOK || len(toStrings(resp["changed"])) != 0 {
		t.Fatalf("reset all: %d %v", code, resp)
	}
	if _, ok := storedRow(t, db, "planner"); ok {
		t.Error("row should be deleted when no overrides remain")
	}
}

func TestPatch_DefaultValueNotStored(t *testing.T) {
	db, srv := newServer(t)
	// 30 and 10.0 are defaults (int and float, written differently);
	// a words set equal to the default in another order is too.
	code, resp := do(t, srv, http.MethodPatch, "/api/rules/planner",
		`{"set":{"min_slot_minutes":30,"theme_min_hours":10,"khutbah_start":"12:00"}}`, admin)
	if code != http.StatusOK || len(toStrings(resp["changed"])) != 0 {
		t.Fatalf("%d %v", code, resp)
	}
	if _, ok := storedRow(t, db, "planner"); ok {
		t.Error("defaults must not be stored")
	}

	// Setting a changed value back to its default removes it.
	do(t, srv, http.MethodPatch, "/api/rules/planner", `{"set":{"min_slot_minutes":60,"mix_channel":false}}`, admin)
	_, resp = do(t, srv, http.MethodPatch, "/api/rules/planner", `{"set":{"min_slot_minutes":30.0}}`, admin)
	if strings.Join(toStrings(resp["changed"]), ",") != "mix_channel" {
		t.Fatalf("resp %v", resp)
	}
	row, _ := storedRow(t, db, "planner")
	if _, has := row["min_slot_minutes"]; has || len(row) != 1 {
		t.Errorf("stored %v", row)
	}
}

func TestPatch_InvalidRejected(t *testing.T) {
	cases := []struct {
		name, section, body, field, msg string
	}{
		{"int not whole", "planner", `{"set":{"min_slot_minutes":12.5}}`, "min_slot_minutes", "whole number"},
		{"int as string", "planner", `{"set":{"min_slot_minutes":"30"}}`, "min_slot_minutes", "whole number"},
		{"below minimum", "planner", `{"set":{"min_slot_minutes":4}}`, "min_slot_minutes", "at least 5"},
		{"float below minimum", "detect", `{"set":{"core_band":0.5}}`, "core_band", "at least 1"},
		{"bad choice", "planner", `{"set":{"khutbah_day":"friday"}}`, "khutbah_day", "one of"},
		{"bad time", "planner", `{"set":{"khutbah_start":"25:00"}}`, "khutbah_start", "HH:MM"},
		{"time past 24:00", "planner", `{"set":{"khutbah_end":"24:30"}}`, "khutbah_end", "HH:MM"},
		{"time no pad", "planner", `{"set":{"khutbah_end":"9:30"}}`, "khutbah_end", "HH:MM"},
		{"bool", "planner", `{"set":{"dawn_quran":"yes"}}`, "dawn_quran", "true or false"},
		{"list of non-strings", "planner", `{"set":{"weekend_days":[1,2]}}`, "weekend_days", "list of strings"},
		{"map value", "planner", `{"set":{"mix_daypart_topics":{"morning":"raqaiq"}}}`, "mix_daypart_topics", "morning"},
		{"array row wrong length", "planner", `{"set":{"quran_time_rules":[["04:00","07:00"]]}}`, "quran_time_rules", "row 1"},
		{"array row bad time column", "planner", `{"set":{"quran_time_rules":[["04:00","7pm","warsh"]]}}`, "quran_time_rules", "row 1 until"},
		{"object row as array", "planner", `{"set":{"dayparts":[["dawn","04:00","07:00","quran","Dawn",""]]}}`, "dayparts", "must be an object"},
		{"object row unknown column", "planner", `{"set":{"dayparts":[{"key":"d","start":"04:00","end":"07:00","role":"r","label":"l","pattern":"p","x":1}]}}`, "dayparts", "unknown column"},
		{"object row missing column", "planner", `{"set":{"dayparts":[{"key":"d","start":"04:00","end":"07:00","label":"l"}]}}`, "dayparts", "missing column"},
		{"object row bad int column", "planner", `{"set":{"themes":[{"key":"q","name":"Q","name_ar":"Q","number":1.5,"topics":[],"audiences":[],"formats":[],"category":"c","min_hours":null}]}}`, "themes", "row 1 number"},
		{"nullable non-null wrong type", "planner", `{"set":{"timezone":5}}`, "timezone", "string"},
		{"unknown name", "planner", `{"set":{"nope":1}}`, "nope", "unknown setting"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			db, srv := newServer(t)
			// A valid companion value proves nothing at all is written.
			code, resp := do(t, srv, http.MethodPatch, "/api/rules/"+tc.section,
				strings.Replace(tc.body, `{"set":{`, `{"set":{"`+companion(tc.section)+`,`, 1), admin)
			if code != http.StatusBadRequest {
				t.Fatalf("status %d: %v", code, resp)
			}
			if resp["field"] != tc.field {
				t.Errorf("field = %v, want %s", resp["field"], tc.field)
			}
			msg, _ := resp["error"].(string)
			if !strings.HasPrefix(msg, tc.field+": ") || !strings.Contains(msg, tc.msg) {
				t.Errorf("error = %q, want %q prefix and %q", msg, tc.field+": ", tc.msg)
			}
			if _, ok := storedRow(t, db, tc.section); ok {
				t.Error("nothing should be written on a validation error")
			}
		})
	}
}

// companion returns a valid `"name":value` pair (without the opening
// quote) for the section, sorted after every field under test, so the
// rejection must still discard it.
func companion(section string) string {
	if section == "detect" {
		return `topic_min":7`
	}
	return `weekly_formats":["program"]`
}

func TestPatch_NullableAndValidShapes(t *testing.T) {
	db, srv := newServer(t)
	code, resp := do(t, srv, http.MethodPatch, "/api/rules/planner", `{"set":{
		"timezone":"Asia/Riyadh",
		"khutbah_end":"24:00",
		"dayparts":[{"key":"d","start":"00:00","end":"24:00","role":"r","label":"l","pattern":"p"}],
		"themes":[{"key":"q","name":"Q","name_ar":"Q","number":101,"topics":["a"],"audiences":[],"formats":[],"category":"c","min_hours":2.5}],
		"mix_daypart_topics":{"morning":["a","b"]}
	}}`, admin)
	if code != http.StatusOK || len(toStrings(resp["changed"])) != 5 {
		t.Fatalf("%d %v", code, resp)
	}
	code, resp = do(t, srv, http.MethodPatch, "/api/rules/planner", `{"set":{"timezone":null}}`, admin)
	if code != http.StatusOK {
		t.Fatalf("null for str?: %d %v", code, resp)
	}
	row, _ := storedRow(t, db, "planner")
	if _, has := row["timezone"]; has {
		t.Error("null equals the default and must be dropped")
	}
}

func TestPatch_WordsComparedAsSet(t *testing.T) {
	db, srv := newServer(t)
	s, _ := ParseSchema(schemaJSON)
	f := s.section("detect").field("generic_words")
	words := append([]any(nil), f.def.([]any)...)
	for i, j := 0, len(words)-1; i < j; i, j = i+1, j-1 {
		words[i], words[j] = words[j], words[i]
	}
	b, _ := json.Marshal(map[string]any{"set": map[string]any{"generic_words": words}})
	code, resp := do(t, srv, http.MethodPatch, "/api/rules/detect", string(b), admin)
	if code != http.StatusOK || len(toStrings(resp["changed"])) != 0 {
		t.Fatalf("%d %v", code, resp)
	}
	if _, ok := storedRow(t, db, "detect"); ok {
		t.Error("reordered default word set must not be stored")
	}
}

func TestGet_UnknownAndInvalidStored(t *testing.T) {
	db, srv := newServer(t)
	putRow(t, db, "planner", `{"retired_knob":1,"min_slot_minutes":2,"mix_number":120}`)
	putRow(t, db, "groups", `"not an object"`)
	_, resp := do(t, srv, http.MethodGet, "/api/rules", "", user)

	sec := sectionOf(t, resp, "planner")
	if u := toStrings(sec["unknown"]); len(u) != 1 || u[0] != "retired_knob" {
		t.Errorf("unknown = %v", u)
	}
	bad := fieldOf(t, resp, "planner", "min_slot_minutes")
	if bad["value"] != float64(30) || bad["changed"] != true || !strings.Contains(bad["invalid"].(string), "at least 5") {
		t.Errorf("invalid stored: %v", bad)
	}
	good := fieldOf(t, resp, "planner", "mix_number")
	if good["value"] != float64(120) || good["changed"] != true {
		t.Errorf("valid stored: %v", good)
	}
	if _, ok := sectionOf(t, resp, "groups")["invalid"]; !ok {
		t.Error("an unreadable row should flag the section")
	}

	// PATCH drops the unknown name from storage.
	code, presp := do(t, srv, http.MethodPatch, "/api/rules/planner", `{"set":{"mix_number":121}}`, admin)
	if code != http.StatusOK {
		t.Fatalf("%d %v", code, presp)
	}
	row, _ := storedRow(t, db, "planner")
	if _, has := row["retired_knob"]; has {
		t.Errorf("unknown name kept: %v", row)
	}
}

func TestPatch_AuthAndSection(t *testing.T) {
	cases := []struct {
		name string
		p    *principal.Principal
		path string
		want int
	}{
		{"non-admin forbidden", user, "/api/rules/planner", http.StatusForbidden},
		{"anonymous forbidden", nil, "/api/rules/planner", http.StatusForbidden},
		{"unknown section", admin, "/api/rules/nope", http.StatusNotFound},
		{"admin ok", admin, "/api/rules/groups", http.StatusOK},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			db, srv := newServer(t)
			code, resp := do(t, srv, http.MethodPatch, tc.path, `{"set":{"min_topic":11}}`, tc.p)
			if code != tc.want {
				t.Fatalf("status %d, want %d: %v", code, tc.want, resp)
			}
			if _, ok := storedRow(t, db, "planner"); ok {
				t.Error("planner must not be written")
			}
		})
	}
}

func TestPatch_ConcurrentSameSection(t *testing.T) {
	db, srv := newServer(t)
	names := []string{"mix_channel", "dawn_quran", "friday_khutbah", "extra_airings", "loose_programs", "ramadan_grid"}
	done := make(chan int, len(names))
	for _, n := range names {
		go func(n string) {
			req := httptest.NewRequest(http.MethodPatch, "/api/rules/planner", strings.NewReader(`{"set":{"`+n+`":false}}`))
			req = req.WithContext(principal.WithPrincipal(context.Background(), admin))
			rec := httptest.NewRecorder()
			srv.ServeHTTP(rec, req)
			done <- rec.Code
		}(n)
	}
	for range names {
		if c := <-done; c != http.StatusOK {
			t.Errorf("status %d", c)
		}
	}
	row, _ := storedRow(t, db, "planner")
	if len(row) != len(names) {
		t.Errorf("lost update: stored %v", row)
	}
}

func toStrings(v any) []string {
	arr, _ := v.([]any)
	out := make([]string, 0, len(arr))
	for _, x := range arr {
		out = append(out, x.(string))
	}
	return out
}

// A row saved before a column existed (dayparts gained topics / exclude) is
// still valid: the column has a default. A column that is sent must not be null.
func TestPatch_RowsMayLeaveOutDefaultedColumnsButNotNullThem(t *testing.T) {
	_, srv := newServer(t)
	code, resp := do(t, srv, http.MethodPatch, "/api/rules/planner", `{"set":{
		"dayparts":[{"key":"d","start":"00:00","end":"24:00","role":"r"}]
	}}`, admin)
	if code != http.StatusOK {
		t.Fatalf("an old row without the new columns: %d %v", code, resp)
	}
	code, resp = do(t, srv, http.MethodPatch, "/api/rules/planner", `{"set":{
		"dayparts":[{"key":"d","start":"00:00","end":"24:00","role":"r","topics":null}]
	}}`, admin)
	if code != http.StatusBadRequest {
		t.Fatalf("a null column: want 400, got %d %v", code, resp)
	}
	code, resp = do(t, srv, http.MethodPatch, "/api/rules/planner", `{"set":{
		"dayparts":[{"start":"00:00","end":"24:00","role":"r"}]
	}}`, admin)
	if code != http.StatusBadRequest {
		t.Fatalf("a required column left out: want 400, got %d %v", code, resp)
	}
}
