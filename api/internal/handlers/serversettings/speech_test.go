package serversettings

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"

	"github.com/go-chi/chi/v5"

	"github.com/Hamza-Labs-Core/Maktaba/api/internal/auth/principal"
)

const speechDocFixture = `{"speech":{"on":true,"model":"small","repo":"Systran/faster-whisper-small","offered":["tiny","base","small","medium"]},
"measure":{"model":"Systran/faster-whisper-small","rtf":0.5},"quiet_hours":"",
"models":[{"name":"tiny","cost":0.5},{"name":"base","cost":1},{"name":"small","cost":3},{"name":"medium","cost":8}]}`

// speechFixture answers /config/speech as maktaba-server does; its database has 900 hours
// of audio waiting for transcription (two files).
func speechFixture(t *testing.T) (http.Handler, *atomic.Int32, *[]string, *Handler) {
	t.Helper()
	calls := &atomic.Int32{}
	bodies := &[]string{}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer tok" {
			http.Error(w, "forbidden", http.StatusForbidden)
			return
		}
		calls.Add(1)
		b, _ := io.ReadAll(r.Body)
		*bodies = append(*bodies, string(b))
		w.Header().Set("Content-Type", "application/json")
		switch r.Method + " " + r.URL.Path {
		case "GET /config/speech":
			_, _ = io.WriteString(w, speechDocFixture)
		case "PUT /config/speech":
			var in struct{ Model *string }
			_ = json.Unmarshal(b, &in)
			if in.Model != nil && *in.Model == "huge" {
				w.WriteHeader(http.StatusUnprocessableEntity)
				_, _ = io.WriteString(w, `{"errors":{"model":"auto or one of tiny, base"}}`)
				return
			}
			_, _ = io.WriteString(w, speechDocFixture)
		case "POST /config/speech/measure":
			w.WriteHeader(http.StatusAccepted)
			_, _ = io.WriteString(w, speechDocFixture)
		default:
			t.Errorf("unexpected %s %s", r.Method, r.URL.Path)
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(srv.Close)
	tf := filepath.Join(t.TempDir(), "token")
	if err := os.WriteFile(tf, []byte("tok\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	db := testDB(t)
	for _, q := range []string{
		`CREATE TABLE videos (id TEXT PRIMARY KEY, duration_sec REAL)`,
		`CREATE TABLE processing_jobs (id INTEGER PRIMARY KEY, video_id TEXT, stage TEXT, state TEXT)`,
		`INSERT INTO videos VALUES ('a', 1800000), ('b', 1440000), ('c', 3600)`,
		`INSERT INTO processing_jobs (video_id, stage, state) VALUES ('a', 'transcribe', 'pending'),
			('b', 'transcribe', 'paused'), ('c', 'transcribe', 'done'), ('c', 'thumbnail', 'pending')`,
	} {
		if _, err := db.Exec(q); err != nil {
			t.Fatal(err)
		}
	}
	h := &Handler{Supervisor: &Supervisor{URL: srv.URL, TokenFile: tf}, DB: db}
	r := chi.NewRouter()
	h.Mount(r)
	return r, calls, bodies, h
}

// Epic 59 story 59.06: the API enforces the role on every speech route, before the
// supervisor is called.
func TestSpeechRefusesNonAdmins(t *testing.T) {
	r, calls, _, _ := speechFixture(t)
	viewer := &principal.Principal{UserID: "u2", Libraries: []string{"lib"}}
	for _, c := range []struct{ method, path, body string }{
		{"GET", "/api/server/speech", ""}, {"PUT", "/api/server/speech", `{"speech":"on"}`}, {"POST", "/api/server/speech/measure", ""},
	} {
		for _, p := range []*principal.Principal{nil, viewer} {
			if rec := call(r, p, c.method, c.path, c.body); rec.Code != http.StatusForbidden {
				t.Errorf("%s %s as %v: %d, want 403", c.method, c.path, p, rec.Code)
			}
		}
	}
	if n := calls.Load(); n != 0 {
		t.Errorf("the supervisor was called %d times", n)
	}
}

// T03: at 0.5× real time a 900-hour backlog takes 75 days, over 30: the next smaller model
// (base, three times cheaper than small) is suggested with its own estimate.
func TestSpeechForAnAdmin(t *testing.T) {
	r, _, bodies, h := speechFixture(t)
	rec := call(r, admin, "GET", "/api/server/speech", "")
	var doc struct {
		Available bool         `json:"available"`
		Advice    SpeechAdvice `json:"advice"`
	}
	if rec.Code != 200 || json.Unmarshal(rec.Body.Bytes(), &doc) != nil || !doc.Available {
		t.Fatalf("GET %d %s", rec.Code, rec.Body)
	}
	a := doc.Advice
	if a.Backlog == nil || a.Backlog.Files != 2 || a.Backlog.Hours != 900 {
		t.Fatalf("backlog: %+v", a.Backlog)
	}
	if a.Estimate == nil || a.Estimate.Days != 75 || !a.Estimate.Measured || a.Suggest == nil ||
		a.Suggest.Model != "base" || a.Suggest.RTF != 1.5 || a.Suggest.Days != 25 || a.Off {
		t.Fatalf("advice: %+v %+v", a.Estimate, a.Suggest)
	}
	if rec := call(r, admin, "PUT", "/api/server/speech", `{"model":"huge"}`); rec.Code != http.StatusUnprocessableEntity {
		t.Errorf("a non-size: %d %s", rec.Code, rec.Body)
	}
	if rec := call(r, admin, "PUT", "/api/server/speech", `{"speech":"on","model":"base","extra":1}`); rec.Code != 200 {
		t.Fatalf("PUT %d %s", rec.Code, rec.Body)
	}
	if last := (*bodies)[len(*bodies)-1]; last != `{"speech":"on","model":"base"}` {
		t.Errorf("the supervisor got %s", last)
	}
	if rec := call(r, admin, "POST", "/api/server/speech/measure", ""); rec.Code != http.StatusAccepted {
		t.Errorf("measure %d", rec.Code)
	}
	for action, want := range map[string]int{ActionSpeechChanged: 1, ActionSpeechMeasured: 1} {
		var n int
		if err := h.DB.QueryRow(`SELECT count(*) FROM audit_log WHERE action = ?`, action).Scan(&n); err != nil || n != want {
			t.Errorf("%s: %d audit rows %v", action, n, err)
		}
	}
	if rec := call(r, admin, "PUT", "/api/server/speech", `not json`); rec.Code != http.StatusBadRequest {
		t.Errorf("not json %d", rec.Code)
	}
}

func TestSpeechAdvice(t *testing.T) {
	models := []SpeechModelCost{{"tiny", 0.5}, {"base", 1}, {"small", 3}}
	// quiet hours: five working hours a day
	a := Advise(SpeechBacklog{Hours: 100}, "small", 2, "01:00-06:00", models)
	if a.Estimate.HoursPerDay != 5 || a.Estimate.Days != 10 || a.Suggest != nil {
		t.Fatalf("quiet: %+v", a.Estimate)
	}
	// the smallest offered and still over 30 days: speech off is suggested
	a = Advise(SpeechBacklog{Hours: 10000}, "tiny", 1, "", models)
	if !a.Off || a.Suggest != nil {
		t.Fatalf("tiny: %+v", a)
	}
	if a := Advise(SpeechBacklog{Hours: 10}, "base", 0, "", models); a.Estimate != nil || a.Backlog == nil {
		t.Fatalf("not measured: %+v", a)
	}
	for w, want := range map[string]float64{"01:00-06:00": 5, "22:00-06:00": 8, "": 24, "x": 24} {
		if got := QuietHoursLength(w); got != want {
			t.Errorf("%q: %v", w, got)
		}
	}
}

func TestSpeechWithoutASupervisor(t *testing.T) {
	r := chi.NewRouter()
	(&Handler{}).Mount(r)
	rec := call(r, admin, "GET", "/api/server/speech", "")
	var doc map[string]any
	if rec.Code != 200 || json.Unmarshal(rec.Body.Bytes(), &doc) != nil || doc["available"] != false {
		t.Fatalf("%d %s", rec.Code, rec.Body)
	}
	for _, c := range [][2]string{{"PUT", "/api/server/speech"}, {"POST", "/api/server/speech/measure"}} {
		if rec := call(r, admin, c[0], c[1], `{}`); rec.Code != http.StatusConflict {
			t.Errorf("%s %d", c[1], rec.Code)
		}
	}
}
