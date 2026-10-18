package transcription

import (
	"database/sql/driver"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/Hamza-Labs-Core/Maktaba/api/internal/auth/principal"
	"github.com/Hamza-Labs-Core/Maktaba/api/internal/handlers/queues/queuestest"
)

var t0 = time.Date(2026, 10, 17, 12, 0, 0, 0, time.UTC)

const (
	libA = "11111111-1111-1111-1111-111111111111"
	libB = "22222222-2222-2222-2222-222222222222"
	vid1 = "33333333-3333-3333-3333-333333333333"
)

type fixture struct {
	h   *Handler
	db  *queuestest.DB
	r   chi.Router
	now time.Time
}

func newFixture(rules ...*queuestest.Rule) *fixture {
	db := queuestest.New(rules...)
	f := &fixture{db: db, r: chi.NewRouter(), now: t0}
	f.h = &Handler{DB: db.DB, NowFunc: func() time.Time { return f.now }}
	f.h.Mount(f.r)
	return f
}

func viewer() *principal.Principal { return &principal.Principal{UserID: "u2"} }

func (f *fixture) get(p *principal.Principal) *httptest.ResponseRecorder {
	req := httptest.NewRequest(http.MethodGet, "/api/transcription/summary", nil)
	if p != nil {
		req = req.WithContext(principal.WithPrincipal(req.Context(), p))
	}
	rec := httptest.NewRecorder()
	f.r.ServeHTTP(rec, req)
	return rec
}

func decode(t *testing.T, rec *httptest.ResponseRecorder) Summary {
	t.Helper()
	if rec.Code != http.StatusOK {
		t.Fatalf("status %d: %s", rec.Code, rec.Body.String())
	}
	var s Summary
	if err := json.Unmarshal(rec.Body.Bytes(), &s); err != nil {
		t.Fatalf("decode %q: %v", rec.Body.String(), err)
	}
	return s
}

// Rules keyed on fragments unique to each statement.
func librariesRule(rows ...[]driver.Value) *queuestest.Rule {
	return &queuestest.Rule{Match: "from videos v", Cols: []string{"id", "name", "n", "done", "secs", "done_secs"}, Rows: rows}
}

func stagesRule(rows ...[]driver.Value) *queuestest.Rule {
	return &queuestest.Rule{Match: "group by stage, subject_id", Cols: []string{"stage", "p", "r", "d", "f"}, Rows: rows}
}

func segmentsRule(n int64) *queuestest.Rule {
	return &queuestest.Rule{Match: "output_summary->'segments'", Cols: []string{"n"}, Rows: [][]driver.Value{{n}}}
}

func liveRule(sec, lost float64, workers int64) *queuestest.Rule {
	return &queuestest.Rule{Match: "stt.audio_sec:", Cols: []string{"sec", "lost", "workers"},
		Rows: [][]driver.Value{{sec, lost, workers}}}
}

func rateRule(jobs10 int64, sec10 float64, rtf10 any, jobs60 int64, sec60 float64, rtf60 any) *queuestest.Rule {
	return &queuestest.Rule{Match: "with w as", Cols: []string{"j10", "s10", "r10", "j60", "s60", "r60"},
		Rows: [][]driver.Value{{jobs10, sec10, rtf10, jobs60, sec60, rtf60}}}
}

func failuresRule(rows ...[]driver.Value) *queuestest.Rule {
	return &queuestest.Rule{Match: "j.state = 'failed'", Cols: []string{"id", "vid", "title", "lib", "stage", "cause", "err", "fin", "attempts"}, Rows: rows}
}

func fullRules() []*queuestest.Rule {
	return []*queuestest.Rule{
		librariesRule(
			[]driver.Value{libA, "Lectures", int64(100), int64(40), 360000.0, 144000.0}, // 100 h, 40 h done
			[]driver.Value{libB, "Series", int64(50), int64(0), 180000.0, 0.0},          // 50 h
		),
		stagesRule(
			[]driver.Value{"transcribe", int64(75), int64(3), int64(40), int64(3)},
			[]driver.Value{"subtitle_gen", int64(0), int64(0), int64(38), int64(0)},
			[]driver.Value{"index", int64(0), int64(4), int64(0), int64(0)},
			[]driver.Value{"other", int64(1), int64(1), int64(99), int64(1)},
		),
		segmentsRule(12345),
		// 10 min: 5 h of audio -> 30x realtime; 1 h: 20 h of audio -> 20x.
		rateRule(6, 18000.0, 31.5, 30, 72000.0, 25.0),
		liveRule(0, 0, 0),
		failuresRule(
			[]driver.Value{int64(901), vid1, "Lesson 1", "Lectures", "transcribe", "gpu_oom",
				`{"kind":"gpu_oom","message":"CUDA out of memory"}`, t0.Add(-time.Minute), int64(3)},
			[]driver.Value{int64(902), vid1, "Lesson 1", nil, "index", nil, nil, nil, int64(1)},
		),
	}
}

func TestSummary_NeedsAuth(t *testing.T) {
	f := newFixture(fullRules()...)
	if rec := f.get(nil); rec.Code != http.StatusForbidden {
		t.Fatalf("status %d", rec.Code)
	}
	if n := len(f.db.Calls()); n != 0 {
		t.Fatalf("anonymous call ran %d statements", n)
	}
}

func TestSummary_Full(t *testing.T) {
	f := newFixture(fullRules()...)
	s := decode(t, f.get(viewer()))

	wantTotals := Totals{Videos: 150, VideosTranscribed: 40, AudioHours: 150, HoursTranscribed: 40, HoursRemaining: 110, Segments: 12345}
	if s.Totals != wantTotals {
		t.Errorf("totals = %+v, want %+v", s.Totals, wantTotals)
	}
	wantStages := []StageCounts{
		{Stage: "transcribe", Pending: 75, Running: 3, Done: 40, Failed: 3},
		{Stage: "subtitle_gen", Done: 38},
		{Stage: "index", Running: 4},
	}
	if len(s.Stages) != 3 {
		t.Fatalf("stages = %+v", s.Stages)
	}
	for i, w := range wantStages {
		if s.Stages[i] != w {
			t.Errorf("stage[%d] = %+v, want %+v", i, s.Stages[i], w)
		}
	}

	r10, r60 := s.Rate.Last10m, s.Rate.Last1h
	if r10.WindowSec != 600 || r10.Jobs != 6 || r10.AudioHours != 5 || r10.RealtimeFactor != 30 ||
		r10.AudioHoursPerHour != 30 || r10.AvgJobRTF == nil || *r10.AvgJobRTF != 31.5 {
		t.Errorf("last_10m = %+v", r10)
	}
	if r60.WindowSec != 3600 || r60.Jobs != 30 || r60.AudioHours != 20 || r60.RealtimeFactor != 20 {
		t.Errorf("last_1h = %+v", r60)
	}

	// 110 h remaining at 30x -> 3 h 40 min, on the 10-minute basis.
	if s.ETA.Seconds == nil || *s.ETA.Seconds != 13200 || s.ETA.Basis == nil || *s.ETA.Basis != "10m" ||
		s.ETA.At == nil || !s.ETA.At.Equal(t0.Add(13200*time.Second)) {
		t.Errorf("eta = %+v", s.ETA)
	}

	if len(s.Libraries) != 2 || s.Libraries[0] != (LibraryRow{LibraryID: libA, Name: "Lectures", Videos: 100, VideosDone: 40, Hours: 100, HoursDone: 40}) {
		t.Errorf("libraries = %+v", s.Libraries)
	}

	if len(s.Failures) != 2 {
		t.Fatalf("failures = %+v", s.Failures)
	}
	f0 := s.Failures[0]
	if f0.JobID != 901 || f0.VideoID != vid1 || f0.Title != "Lesson 1" || *f0.LibraryName != "Lectures" ||
		*f0.ErrorCause != "gpu_oom" || *f0.Error != "CUDA out of memory" || f0.Attempts != 3 || !f0.FinishedAt.Equal(t0.Add(-time.Minute)) {
		t.Errorf("failure[0] = %+v", f0)
	}
	if f1 := s.Failures[1]; f1.LibraryName != nil || f1.ErrorCause != nil || f1.Error != nil || f1.FinishedAt != nil {
		t.Errorf("failure[1] nulls = %+v", f1)
	}
}

func TestSummary_QueryArguments(t *testing.T) {
	f := newFixture(fullRules()...)
	decode(t, f.get(viewer()))

	stage := f.db.Find("group by stage, subject_id")
	if len(stage) != 1 || stage[0].Args[0] != "{\"transcribe\",\"subtitle_gen\",\"index\"}" {
		t.Errorf("stage counts call = %+v", stage)
	}
	rate := f.db.Find("with w as")
	if len(rate) != 1 || !rate[0].Args[0].(time.Time).Equal(t0.Add(-time.Hour)) ||
		!rate[0].Args[1].(time.Time).Equal(t0.Add(-10*time.Minute)) {
		t.Errorf("rate call = %+v", rate)
	}
	fail := f.db.Find("j.state = 'failed'")
	if len(fail) != 1 || fail[0].Args[1] != int64(FailureLimit) {
		t.Errorf("failures call = %+v", fail)
	}
	// Only open failures: a later job for the same video+stage hides one.
	if len(f.db.Find("not exists (select 1 from processing_jobs n")) != 1 {
		t.Error("failures query does not exclude superseded failures")
	}
}

func TestSummary_Empty(t *testing.T) {
	f := newFixture(segmentsRule(0), rateRule(0, 0.0, nil, 0, 0.0, nil), liveRule(0, 0, 0))
	s := decode(t, f.get(viewer()))
	if s.Totals != (Totals{}) || len(s.Libraries) != 0 || len(s.Failures) != 0 || len(s.Stages) != 3 {
		t.Errorf("empty summary = %+v", s)
	}
	if s.Rate.Last10m.AvgJobRTF != nil {
		t.Errorf("avg rtf should be null: %+v", s.Rate.Last10m)
	}
	// Nothing remaining -> done now.
	if s.ETA.Seconds == nil || *s.ETA.Seconds != 0 || s.ETA.Basis != nil {
		t.Errorf("eta = %+v", s.ETA)
	}
	// JSON arrays, never null, so the page can map over them.
	var raw map[string]any
	_ = json.Unmarshal(f.get(viewer()).Body.Bytes(), &raw)
	for _, k := range []string{"libraries", "failures", "stages"} {
		if _, ok := raw[k].([]any); !ok {
			t.Errorf("%s = %v, want an array", k, raw[k])
		}
	}
}

func TestSummary_LiveSpeed(t *testing.T) {
	// two workers decoded 26 h of audio in 10 min and a restart threw 1 h of it
	// away: 150x; finished jobs say 30x
	rules := append([]*queuestest.Rule{liveRule(93600, 3600, 2)}, fullRules()...)
	s := decode(t, newFixture(rules...).get(viewer()))
	if s.Rate.Live != (Live{WindowSec: 600, Workers: 2, AudioHours: 25, RealtimeFactor: 150}) {
		t.Errorf("live = %+v", s.Rate.Live)
	}
	// 110 h remaining at 150x -> 44 min
	if s.ETA.Seconds == nil || *s.ETA.Seconds != 2640 || *s.ETA.Basis != "live" {
		t.Errorf("eta = %+v", s.ETA)
	}
}

func TestLive_LostAudioNeverMakesItNegative(t *testing.T) {
	if l := live(600, 1, 100, 400); l.RealtimeFactor != 0 || l.AudioHours != 0 {
		t.Errorf("live = %+v", l)
	}
}

func TestETA(t *testing.T) {
	ten := func(jobs int, rt float64) Window { return Window{Jobs: jobs, RealtimeFactor: rt} }
	tests := []struct {
		name      string
		remaining float64
		r         Rates
		wantSec   *float64
		wantBasis string
	}{
		{"live speed first", 10, Rates{Live: Live{RealtimeFactor: 100}, Last10m: ten(2, 20)}, ptr(360), "live"},
		{"10m basis", 10, Rates{Last10m: ten(2, 20), Last1h: ten(9, 5)}, ptr(1800), "10m"},
		{"falls back to the hour", 10, Rates{Last10m: ten(0, 0), Last1h: ten(9, 5)}, ptr(7200), "1h"},
		{"no recent work -> unknown", 10, Rates{}, nil, ""},
		{"nothing left", 0, Rates{}, ptr(0), ""},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			e := eta(tc.remaining, tc.r, t0)
			if (e.Seconds == nil) != (tc.wantSec == nil) || (e.Seconds != nil && *e.Seconds != *tc.wantSec) {
				t.Fatalf("seconds = %v, want %v", e.Seconds, tc.wantSec)
			}
			if got := ""; e.Basis != nil {
				got = *e.Basis
				if got != tc.wantBasis {
					t.Fatalf("basis = %q, want %q", got, tc.wantBasis)
				}
			} else if tc.wantBasis != "" {
				t.Fatalf("basis nil, want %q", tc.wantBasis)
			}
		})
	}
}

func ptr(v float64) *float64 { return &v }

func TestSummary_Cached(t *testing.T) {
	f := newFixture(fullRules()...)
	decode(t, f.get(viewer()))
	n := len(f.db.Calls())
	f.now = t0.Add(CacheTTL / 2)
	decode(t, f.get(viewer()))
	if got := len(f.db.Calls()); got != n {
		t.Fatalf("cached call ran %d more statements", got-n)
	}
	f.now = t0.Add(CacheTTL)
	decode(t, f.get(viewer()))
	if got := len(f.db.Calls()); got != 2*n {
		t.Fatalf("expired cache ran %d statements, want %d", got-n, n)
	}
}

func TestSummary_QueryError(t *testing.T) {
	f := newFixture(&queuestest.Rule{Match: "from videos v", Err: errors.New("boom")})
	rec := f.get(viewer())
	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("status %d", rec.Code)
	}
}

func TestErrorText(t *testing.T) {
	tests := map[string]string{
		`{"kind":"gpu_oom","message":"out of memory"}`: "out of memory",
		`{"kind":"gpu_oom"}`:                           "gpu_oom",
		"  plain text  ":                               "plain text",
		"{not json":                                    "{not json",
	}
	for in, want := range tests {
		if got := errorText(in); got != want {
			t.Errorf("errorText(%q) = %q, want %q", in, got, want)
		}
	}
}
