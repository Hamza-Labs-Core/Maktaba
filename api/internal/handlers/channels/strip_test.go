package channels

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func (e artEnv) strip(t *testing.T, method, body string) (*httptest.ResponseRecorder, Channel) {
	t.Helper()
	req := httptest.NewRequest(method, "/api/channels/c1/strip", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	return e.do(t, artAdmin, req)
}

func TestStripPatchValidates(t *testing.T) {
	e := newArtEnv(t)
	insertChannel(t, e.db, "c1", nil)
	for _, bad := range []string{
		`{"position":"middle"}`, `{"height_pct":2}`, `{"height_pct":30}`, `{"bg_color":"red"}`,
		`{"bg_opacity":1.5}`, `{"font_size_pct":90}`, `{"direction":"up"}`, `{"enabled":"yes"}`,
		`{"overflow":"wrap"}`, `{"scroll_speed":5}`, `{"show_mode":"never"}`, `{"show_seconds":2.5}`,
		`{"every_minutes":0}`, `{"clock_seconds":1}`, `{"clock_format":"36h"}`, `{"digits":"roman"}`,
		`{"next_label_ar":"` + strings.Repeat("x", 41) + `"}`, `{"filler_label":"a\u0007b"}`,
		`{"clocks":[{"zone":"Mars/Olympus"}]}`, `{"clocks":[{"label_ar":"x"}]}`,
		`{"clocks":[{"zone":"UTC","city":"x"}]}`, `{"clocks":"UTC"}`,
		`{"clocks":[` + strings.Repeat(`{"zone":"UTC"},`, 8) + `{"zone":"UTC"}]}`,
		`{"font_file":"C:/Windows/Fonts/arial.ttf"}`, `{"font_file":null}`, `{"colour":"#ffffff"}`, `{"colour":null}`,
	} {
		if rec, _ := e.strip(t, "PATCH", bad); rec.Code != http.StatusUnprocessableEntity {
			t.Errorf("%s: %d %s", bad, rec.Code, rec.Body.String())
		}
	}
	if look := e.look(t, "c1"); look["strip"] != nil {
		t.Fatalf("a refused PATCH wrote %v", look["strip"])
	}
}

func TestStripPatchMergesAndResets(t *testing.T) {
	e := newArtEnv(t)
	insertChannel(t, e.db, "c1", nil)
	rec, c := e.strip(t, "PATCH", `{"enabled":true,"position":"top","bg_color":"#0B1F3A",
		"clocks":[{"zone":"Asia/Riyadh","label_ar":"مكة المكرمة","label_en":"Makkah"},
		{"zone":"America/New_York","label_ar":"نيويورك","label_en":"New York"}],"clock_seconds":8}`)
	if rec.Code != 200 || c.Art == nil || c.Art.Strip["position"] != "top" || c.Art.Strip["bg_color"] != "#0b1f3a" {
		t.Fatalf("patch: %d %s", rec.Code, rec.Body.String())
	}
	// a later PATCH of one field keeps the others
	_, c = e.strip(t, "PATCH", `{"show_next":false}`)
	s := e.look(t, "c1")["strip"].(map[string]any)
	if s["enabled"] != true || s["position"] != "top" || s["show_next"] != false || s["clock_seconds"] != 8.0 {
		t.Fatalf("merged %v", s)
	}
	if clocks := s["clocks"].([]any); len(clocks) != 2 || clocks[0].(map[string]any)["zone"] != "Asia/Riyadh" {
		t.Fatalf("clocks %v", s["clocks"])
	}
	if c.Art.Strip["show_next"] != false {
		t.Fatalf("dto %v", c.Art.Strip)
	}
	// null drops a setting: the global one applies again
	_, c = e.strip(t, "PATCH", `{"position":null,"enabled":null}`)
	if _, ok := c.Art.Strip["position"]; ok || c.Art.Strip["enabled"] != nil || c.Art.Strip["show_next"] != false {
		t.Fatalf("after null %v", c.Art.Strip)
	}
	rec, c = e.strip(t, "DELETE", ``)
	if rec.Code != 200 || c.Art.Strip != nil {
		t.Fatalf("reset: %d %v", rec.Code, c.Art.Strip)
	}
	if look := e.look(t, "c1"); look != nil && look["strip"] != nil {
		t.Fatalf("look after reset %v", look)
	}
}

func TestStripAdminOnly(t *testing.T) {
	e := newArtEnv(t)
	insertChannel(t, e.db, "c1", nil)
	req := httptest.NewRequest("PATCH", "/api/channels/c1/strip", strings.NewReader(`{"enabled":true}`))
	req.Header.Set("Content-Type", "application/json")
	if rec, _ := e.do(t, nil, req); rec.Code != http.StatusForbidden {
		t.Fatalf("anonymous: %d", rec.Code)
	}
	if rec, _ := e.do(t, artAdmin, httptest.NewRequest("DELETE", "/api/channels/nope/strip", nil)); rec.Code != http.StatusNotFound {
		t.Fatalf("missing channel: %d", rec.Code)
	}
}
