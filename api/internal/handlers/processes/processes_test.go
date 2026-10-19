package processes

import (
	"context"
	"database/sql"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"
	_ "modernc.org/sqlite"

	"github.com/Hamza-Labs-Core/Maktaba/api/internal/auth/principal"
)

const snapshotJSON = `{
  "host": "tv-box",
  "processes": [
    {"role": "api", "state": "running", "pid": 101, "restarts": 0},
    {"role": "streaming", "state": "backoff", "restarts": 2,
     "last_exit": {"code": 2, "reason": "crash", "uptime_sec": 12.5}}
  ],
  "events": [
    {"seq": 1, "at": "2026-10-18T10:00:00Z", "role": "streaming", "kind": "started", "pid": 200},
    {"seq": 2, "at": "2026-10-18T10:00:12Z", "role": "streaming", "kind": "exited", "reason": "crash", "code": 2},
    {"seq": 3, "at": "2026-10-18T10:00:13Z", "role": "streaming", "kind": "restarting"},
    {"seq": 4, "at": "2026-10-18T10:05:00Z", "role": "pipeline", "kind": "exited", "reason": "hang"},
    {"seq": 5, "at": "2026-10-18T10:06:00Z", "role": "api", "kind": "exited", "reason": "shutdown"}
  ]
}`

func fakeSupervisor(t *testing.T, body string) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/processes" {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(body))
	}))
	t.Cleanup(srv.Close)
	return srv
}

func serve(t *testing.T, h *Handler, admin bool) *httptest.ResponseRecorder {
	t.Helper()
	r := chi.NewRouter()
	h.Mount(r)
	req := httptest.NewRequest(http.MethodGet, "/api/system/processes", nil)
	req = req.WithContext(principal.WithPrincipal(req.Context(),
		&principal.Principal{UserID: "u", IsAdmin: admin}))
	rr := httptest.NewRecorder()
	r.ServeHTTP(rr, req)
	return rr
}

func TestProcessesProxiesSupervisorForAdmins(t *testing.T) {
	sup := fakeSupervisor(t, snapshotJSON)
	rr := serve(t, &Handler{SupervisorURL: sup.URL}, true)
	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d: %s", rr.Code, rr.Body.String())
	}
	var doc struct {
		Available bool `json:"available"`
		Processes []struct {
			Role     string `json:"role"`
			State    string `json:"state"`
			Restarts int    `json:"restarts"`
			LastExit *struct {
				Reason string `json:"reason"`
			} `json:"last_exit"`
		} `json:"processes"`
		Events []Event `json:"events"`
	}
	if err := json.Unmarshal(rr.Body.Bytes(), &doc); err != nil {
		t.Fatal(err)
	}
	if !doc.Available || len(doc.Processes) != 2 || len(doc.Events) != 5 {
		t.Fatalf("doc = %+v", doc)
	}
	st := doc.Processes[1]
	if st.Role != "streaming" || st.State != "backoff" || st.Restarts != 2 || st.LastExit == nil || st.LastExit.Reason != "crash" {
		t.Errorf("streaming = %+v", st)
	}
}

func TestProcessesIsAdminOnly(t *testing.T) {
	sup := fakeSupervisor(t, snapshotJSON)
	if rr := serve(t, &Handler{SupervisorURL: sup.URL}, false); rr.Code != http.StatusForbidden {
		t.Fatalf("non-admin status = %d, want 403", rr.Code)
	}
}

func TestProcessesWithoutSupervisor(t *testing.T) {
	for name, url := range map[string]string{
		"unset":       "",
		"unreachable": "http://127.0.0.1:1",
	} {
		rr := serve(t, &Handler{SupervisorURL: url, Client: &http.Client{Timeout: time.Second}}, true)
		if rr.Code != http.StatusOK {
			t.Fatalf("%s: status %d", name, rr.Code)
		}
		var doc map[string]any
		_ = json.Unmarshal(rr.Body.Bytes(), &doc)
		if doc["available"] != false || doc["reason"] == "" {
			t.Errorf("%s: doc = %v", name, doc)
		}
		if ps, ok := doc["processes"].([]any); !ok || len(ps) != 0 {
			t.Errorf("%s: processes = %v", name, doc["processes"])
		}
	}
}

func TestMetric(t *testing.T) {
	cases := map[Event]string{
		{Role: "streaming", Kind: "exited", Reason: "crash"}:        "proc.streaming.crash",
		{Role: "streaming", Kind: "exited", Reason: "start_failed"}: "proc.streaming.crash",
		{Role: "pipeline", Kind: "exited", Reason: "hang"}:          "proc.pipeline.hang",
		{Role: "api", Kind: "exited", Reason: "clean"}:              "",
		{Role: "api", Kind: "exited", Reason: "shutdown"}:           "",
		{Role: "api", Kind: "hung", Reason: "hang"}:                 "",
	}
	for e, want := range cases {
		if got := Metric(e); got != want {
			t.Errorf("Metric(%+v) = %q, want %q", e, got, want)
		}
	}
}

func openHealthDB(t *testing.T) *sql.DB {
	t.Helper()
	db, err := sql.Open("sqlite", filepath.Join(t.TempDir(), "h.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	// The SQLite shape of slot 0089's health_samples.
	if _, err := db.Exec(`CREATE TABLE health_samples (
		at TEXT NOT NULL, host TEXT NOT NULL, metric TEXT NOT NULL, value REAL NOT NULL,
		PRIMARY KEY (metric, host, at))`); err != nil {
		t.Fatal(err)
	}
	return db
}

func TestRecorderWritesCrashSamplesOnce(t *testing.T) {
	sup := fakeSupervisor(t, snapshotJSON)
	db := openHealthDB(t)
	rc := &Recorder{SupervisorURL: sup.URL, DB: db, Host: "tv-box"}

	n, err := rc.Once(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if n != 2 {
		t.Fatalf("wrote %d samples, want 2 (one crash, one hang)", n)
	}
	// A restarted API (fresh recorder) sees the same events again: the
	// sample key makes the write idempotent.
	rc2 := &Recorder{SupervisorURL: sup.URL, DB: db, Host: "tv-box"}
	if n, err := rc2.Once(context.Background()); err != nil || n != 0 {
		t.Fatalf("second recorder wrote %d (%v), want 0", n, err)
	}
	rows, err := db.Query(`SELECT at, metric, value FROM health_samples ORDER BY at`)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = rows.Close() }()
	var got []string
	for rows.Next() {
		var at, metric string
		var v float64
		if err := rows.Scan(&at, &metric, &v); err != nil {
			t.Fatal(err)
		}
		got = append(got, at+" "+metric)
		if v != 1 {
			t.Errorf("value = %v", v)
		}
	}
	want := []string{"2026-10-18T10:00:12.000Z proc.streaming.crash", "2026-10-18T10:05:00.000Z proc.pipeline.hang"}
	if len(got) != 2 || got[0] != want[0] || got[1] != want[1] {
		t.Fatalf("samples = %v, want %v", got, want)
	}
}

func TestRecorderWithoutSupervisor(t *testing.T) {
	rc := &Recorder{DB: openHealthDB(t)}
	if _, err := rc.Once(context.Background()); err == nil {
		t.Fatal("want an error without a supervisor URL")
	}
}
