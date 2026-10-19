package jobs

import (
	"database/sql/driver"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/Hamza-Labs-Core/Maktaba/api/internal/auth/principal"
	"github.com/Hamza-Labs-Core/Maktaba/api/internal/handlers/queues/queuestest"
)

var now0 = time.Date(2026, 10, 15, 12, 0, 0, 0, time.UTC)

const (
	vid  = "11111111-1111-1111-1111-111111111111"
	vid2 = "22222222-2222-2222-2222-222222222222"
)

func newJobsFixture(rules ...*queuestest.Rule) (*queuestest.DB, chi.Router) {
	db := queuestest.New(rules...)
	h := &Handler{DB: db.DB, NowFunc: func() time.Time { return now0 }}
	r := chi.NewRouter()
	h.Mount(r)
	return db, r
}

func call(r chi.Router, method, path string, admin bool) *httptest.ResponseRecorder {
	req := httptest.NewRequest(method, path, nil)
	req = req.WithContext(principal.WithPrincipal(req.Context(), &principal.Principal{UserID: "u1", IsAdmin: admin}))
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	return rec
}

var jobCols = []string{"id", "video_id", "library_id", "stage", "state", "priority", "attempts",
	"pause_requested", "cancel_requested", "created_at", "not_before", "claimed_by", "last_heartbeat_at",
	"error", "estimated_remaining_sec", "subject_kind", "subject_id"}

func jobRow(id int64, stage, state string) []driver.Value {
	return []driver.Value{id, vid, nil, stage, state, int64(100), int64(3), false, false, now0, nil, nil, nil,
		`{"kind":"X","message":"boom"}`, 12.5, "video", vid}
}

// F12: error and estimated_remaining_sec are now selected and returned.
func TestGet_ReturnsErrorAndEstimate(t *testing.T) {
	db, r := newJobsFixture(&queuestest.Rule{Match: "from processing_jobs where id = $1", Cols: jobCols,
		Rows: [][]driver.Value{jobRow(9, "probe", "failed")}})
	// a job of no library is an admin's to see; anyone else gets 404
	if rec := call(r, "GET", "/api/jobs/9", false); rec.Code != 404 {
		t.Fatalf("non-admin: status %d %s", rec.Code, rec.Body.String())
	}
	rec := call(r, "GET", "/api/jobs/9", true)
	if rec.Code != 200 {
		t.Fatalf("status %d %s", rec.Code, rec.Body.String())
	}
	var j Job
	if err := json.Unmarshal(rec.Body.Bytes(), &j); err != nil {
		t.Fatal(err)
	}
	if j.ID != "9" || j.Error == nil || !strings.Contains(*j.Error, "boom") || j.EstimatedRemainingSec == nil ||
		*j.EstimatedRemainingSec != 12.5 || j.SubjectKind != "video" || j.SubjectID != vid {
		t.Errorf("job = %+v", j)
	}
	if q := db.Calls()[0].SQL; !strings.Contains(q, "estimated_remaining_sec") || !strings.Contains(q, "error") {
		t.Errorf("select list = %s", q)
	}
}

// F7: every control UPDATE sets updated_at (the column now exists, 0087).
func TestControlUpdates_SetUpdatedAt(t *testing.T) {
	tests := []struct {
		name, method, path string
		wantSQL            string
	}{
		{"pause", "POST", "/api/jobs/9/pause", "update processing_jobs set pause_requested=true, updated_at=$2"},
		{"force pause", "POST", "/api/jobs/9/pause?force=true", "set state='paused'"},
		{"resume", "POST", "/api/jobs/9/resume", "state='pending'"},
		{"cancel", "POST", "/api/jobs/9/cancel", "cancel_requested=true"},
		{"pause video", "POST", "/api/videos/" + vid + "/pause", "where video_id=$1"},
		{"resume video", "POST", "/api/videos/" + vid + "/resume", "where video_id=$1 and state='paused'"},
		{"cancel video", "POST", "/api/videos/" + vid + "/cancel", "cancel_requested=true"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			db, r := newJobsFixture(&queuestest.Rule{Match: "from processing_jobs where id = $1", Cols: jobCols,
				Rows: [][]driver.Value{jobRow(9, "probe", "running")}})
			rec := call(r, tc.method, tc.path, true)
			if rec.Code != 200 {
				t.Fatalf("status %d %s", rec.Code, rec.Body.String())
			}
			var upd string
			for _, c := range db.Calls() {
				if c.Exec {
					upd = c.SQL
				}
			}
			if !strings.Contains(upd, "updated_at=$2") || !strings.Contains(upd, tc.wantSQL) {
				t.Errorf("update = %q (want updated_at and %q)", upd, tc.wantSQL)
			}
		})
	}
}

func TestControl_NonAdminForbidden(t *testing.T) {
	db, r := newJobsFixture()
	for _, p := range []string{"/api/jobs/9/pause", "/api/jobs/9/resume", "/api/jobs/9/cancel", "/api/jobs/9/retry"} {
		if rec := call(r, "POST", p, false); rec.Code != http.StatusForbidden {
			t.Errorf("%s -> %d", p, rec.Code)
		}
	}
	if len(db.Calls()) != 0 {
		t.Errorf("forbidden calls touched the DB")
	}
}

func TestRetry(t *testing.T) {
	tests := []struct {
		name     string
		state    string
		enqueue  []driver.Value
		wantCode int
		wantID   string
	}{
		{"failed job is re-enqueued through the SQL function", "failed", []driver.Value{int64(55), "inserted"}, 200, "55"},
		{"coalesced into a live row", "failed", []driver.Value{int64(56), "coalesced"}, 200, "56"},
		{"non-failed -> 409", "running", nil, 409, ""},
		{"stage disabled -> 409", "failed", []driver.Value{nil, "disabled"}, 409, ""},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			rules := []*queuestest.Rule{{Match: "from processing_jobs where id = $1", Cols: jobCols, Rows: [][]driver.Value{jobRow(9, "probe", tc.state)}}}
			if tc.enqueue != nil {
				rules = append(rules, &queuestest.Rule{Match: "processing_jobs_enqueue", Cols: []string{"job_id", "outcome"}, Rows: [][]driver.Value{tc.enqueue}})
			}
			db, r := newJobsFixture(rules...)
			rec := call(r, "POST", "/api/jobs/9/retry", true)
			if rec.Code != tc.wantCode {
				t.Fatalf("status %d %s", rec.Code, rec.Body.String())
			}
			enq := db.Find("processing_jobs_enqueue")
			if tc.wantCode != 200 {
				return
			}
			if len(enq) != 1 {
				t.Fatalf("enqueue calls = %d", len(enq))
			}
			a := enq[0].Args
			if a[0] != "video" || a[1] != vid || a[2] != "probe" || a[4] != true {
				t.Errorf("args = %v", a)
			}
			if !strings.Contains(a[6].(string), `"why":"retry"`) || !strings.Contains(a[6].(string), `"of_job":"9"`) {
				t.Errorf("reason = %v", a[6])
			}
			for _, c := range db.Calls() {
				if c.Exec && strings.Contains(c.SQL, "set state='pending', attempts=0") {
					t.Errorf("retry must not update the failed row in place: %s", c.SQL)
				}
			}
		})
	}
	t.Run("unknown job", func(t *testing.T) {
		_, r := newJobsFixture()
		if rec := call(r, "POST", "/api/jobs/404/retry", true); rec.Code != 404 {
			t.Fatalf("status %d", rec.Code)
		}
	})
}

// F11: stage names come from job_stages, workers are distinct claimed_by,
// eta_sec is the sum of ready/rate.
func TestStats(t *testing.T) {
	rules := []*queuestest.Rule{
		{Match: "from job_stages", Cols: []string{"stage", "ext", "enabled"}, Rows: [][]driver.Value{
			{"probe", nil, true}, {"transcribe", nil, true}, {"plex_collection", "plex", false}, {"thumb_hash", nil, true}}},
		{Match: "group by stage, state", Cols: []string{"stage", "state", "n"}, Rows: [][]driver.Value{
			{"probe", "pending", int64(30)}, {"probe", "running", int64(2)}, {"probe", "claimed", int64(1)},
			{"probe", "failed", int64(4)}, {"transcribe", "paused", int64(1)}, {"transcribe", "pending", int64(5)},
			{"legacy_only", "pending", int64(1)}}},
		{Match: "where state = 'done' and finished_at > $1 group by stage", Cols: []string{"stage", "n"}, Rows: [][]driver.Value{{"probe", int64(200)}}},
		{Match: "filter (where state = 'pending' and (not_before", Cols: []string{"stage", "rate", "ready"}, Rows: [][]driver.Value{
			{"probe", int64(20), int64(20)},      // 2/min -> 20 ready = 600 s
			{"transcribe", int64(0), int64(5)}}}, // no rate -> no estimate
		{Match: "select min(created_at)", Cols: []string{"min"}, Rows: [][]driver.Value{{now0.Add(-90 * time.Second)}}},
		{Match: "group by claimed_by", Cols: []string{"claimed_by", "hb", "job"}, Rows: [][]driver.Value{
			{"nas-1/4242/abc", now0, "7"}, {"mac-1/99/def", now0.Add(-time.Second), "8"}}},
		{Match: "from workers w left join cluster_nodes", Cols: []string{"id", "host", "stages", "pools", "started_at", "last_seen_at", "version", "node_id", "node_name", "role"},
			Rows: [][]driver.Value{{"w1", "nas-1", "{probe}", nil, now0.Add(-time.Hour), now0.Add(-time.Second), nil, nil, nil, nil}}},
	}
	_, r := newJobsFixture(rules...)
	rec := call(r, "GET", "/api/queue/stats", false)
	if rec.Code != 200 {
		t.Fatalf("status %d %s", rec.Code, rec.Body.String())
	}
	var s StatsResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &s); err != nil {
		t.Fatal(err)
	}
	for _, bogus := range []string{"audio_extract", "subtitle", "embed", "rehash"} {
		if _, ok := s.ByStage[bogus]; ok {
			t.Errorf("stage %q does not exist and must not be listed", bogus)
		}
	}
	for _, real := range []string{"probe", "transcribe", "plex_collection", "thumb_hash", "legacy_only"} {
		if _, ok := s.ByStage[real]; !ok {
			t.Errorf("stage %q missing", real)
		}
	}
	probe := s.ByStage["probe"]
	if probe.Pending != 30 || probe.Running != 3 || probe.Failed != 4 || probe.Done24h != 200 {
		t.Errorf("probe = %+v", probe)
	}
	if s.TotalInFlight != 3 {
		t.Errorf("in flight = %d", s.TotalInFlight)
	}
	if s.EtaSec != 600 {
		t.Errorf("eta_sec = %v, want 600 (20 ready at 2/min)", s.EtaSec)
	}
	if s.OldestPendingAgeSec != 90 {
		t.Errorf("oldest = %v", s.OldestPendingAgeSec)
	}
	if len(s.Workers) != 2 || s.Workers[0].Host != "nas-1" || s.Workers[0].ID != "nas-1/4242/abc" ||
		s.Workers[1].Host != "mac-1" || *s.Workers[0].CurrentJobID != "7" {
		t.Errorf("workers = %+v", s.Workers)
	}
	if probe.NoWorker || probe.ServedWorkers != 1 || probe.ServedHosts[0] != "nas-1" {
		t.Errorf("probe coverage = %+v", probe)
	}
	if tr := s.ByStage["transcribe"]; !tr.NoWorker || tr.ServedWorkers != 0 {
		t.Errorf("transcribe must be flagged no_worker: %+v", tr)
	}
	if px := s.ByStage["plex_collection"]; px.NoWorker || px.Extension == nil || *px.Extension != "plex" {
		t.Errorf("disabled extension stage = %+v", px)
	}
}

func TestStats_WorkersTableMissingIsTolerated(t *testing.T) {
	_, r := newJobsFixture(
		&queuestest.Rule{Match: "from job_stages", Cols: []string{"stage", "ext", "enabled"}, Rows: [][]driver.Value{{"probe", nil, true}}},
		&queuestest.Rule{Match: "from workers", Err: errNoTable})
	rec := call(r, "GET", "/api/queue/stats", false)
	var s StatsResponse
	_ = json.Unmarshal(rec.Body.Bytes(), &s)
	if rec.Code != 200 || s.ByStage["probe"].NoWorker {
		t.Fatalf("%d, no_worker must stay false when the registry is unavailable: %+v", rec.Code, s.ByStage["probe"])
	}
}

type tableErr struct{}

func (tableErr) Error() string { return `relation "workers" does not exist` }

var errNoTable = tableErr{}

func TestEtaSeconds(t *testing.T) {
	tests := []struct {
		name   string
		stages map[string]StageStats
		want   float64
	}{
		{"empty", map[string]StageStats{}, 0},
		{"no rate -> no estimate", map[string]StageStats{"a": {ready: 100}}, 0},
		{"sum over stages", map[string]StageStats{"a": {ready: 10, rate: 10}, "b": {ready: 30, rate: 30}}, 600 + 600},
		{"nothing ready", map[string]StageStats{"a": {rate: 10}}, 0},
	}
	for _, tc := range tests {
		if got := etaSeconds(tc.stages); got != tc.want {
			t.Errorf("%s: %v want %v", tc.name, got, tc.want)
		}
	}
}

func TestWorkerHost(t *testing.T) {
	for in, want := range map[string]string{"nas/12/uuid": "nas", "solo": "solo", "/x": "/x", "": ""} {
		if got := workerHost(in); got != want {
			t.Errorf("workerHost(%q)=%q want %q", in, got, want)
		}
	}
}
