package queues

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
	"github.com/Hamza-Labs-Core/Maktaba/api/internal/handlers/ws"
)

var t0 = time.Date(2026, 10, 15, 12, 0, 0, 0, time.UTC)

const (
	uidA = "11111111-1111-1111-1111-111111111111"
	uidB = "22222222-2222-2222-2222-222222222222"
)

type fixture struct {
	h  *Handler
	db *queuestest.DB
	r  chi.Router
}

func newFixture(rules ...*queuestest.Rule) *fixture {
	db := queuestest.New(rules...)
	h := &Handler{DB: db.DB, Hub: ws.NewHub(), NowFunc: func() time.Time { return t0 }, CPUCount: func() int { return 5 }}
	r := chi.NewRouter()
	h.Mount(r)
	return &fixture{h: h, db: db, r: r}
}

func admin() *principal.Principal {
	return &principal.Principal{UserID: "u1", IsAdmin: true, AccessAllLibraries: true}
}
func viewer() *principal.Principal { return &principal.Principal{UserID: "u2"} }

func (f *fixture) do(method, target, body string, p *principal.Principal) *httptest.ResponseRecorder {
	var req *http.Request
	if body == "" {
		req = httptest.NewRequest(method, target, nil)
	} else {
		req = httptest.NewRequest(method, target, strings.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
	}
	if p != nil {
		req = req.WithContext(principal.WithPrincipal(req.Context(), p))
	}
	rec := httptest.NewRecorder()
	f.r.ServeHTTP(rec, req)
	return rec
}

func decode(t *testing.T, rec *httptest.ResponseRecorder, v any) {
	t.Helper()
	if err := json.Unmarshal(rec.Body.Bytes(), v); err != nil {
		t.Fatalf("decode %q: %v", rec.Body.String(), err)
	}
}

var stageCols = []string{"stage", "label", "lane", "lane_order", "subject_kinds", "pool", "version",
	"legacy", "hidden", "external_write", "write_mode", "enabled", "disabled_reason", "paused",
	"paused_at", "paused_by", "debounce_sec", "edges", "may_request", "entry_for", "graph_synced_at", "extension"}

type stageOpt func([]driver.Value)

func stageRow(stage, lane string, order int64, pool string, opts ...stageOpt) []driver.Value {
	row := []driver.Value{stage, strings.ToUpper(stage[:1]) + stage[1:], lane, order, "{video}", pool, int64(1),
		false, false, false, "auto", true, nil, false, nil, nil, int64(0), "[]", "{}", "{}", t0, nil}
	for _, o := range opts {
		o(row)
	}
	return row
}

func withKinds(k string) stageOpt { return func(r []driver.Value) { r[4] = k } }
func withEdges(j string) stageOpt { return func(r []driver.Value) { r[17] = j } }
func withEntry(e string) stageOpt { return func(r []driver.Value) { r[19] = e } }
func withExternal() stageOpt      { return func(r []driver.Value) { r[9] = true } }
func withDisabled(reason string) stageOpt {
	return func(r []driver.Value) { r[11] = false; r[12] = reason }
}
func withSynced(t time.Time) stageOpt { return func(r []driver.Value) { r[20] = t } }
func withExtension(e string) stageOpt { return func(r []driver.Value) { r[21] = e } }

func stagesRule(rows ...[]driver.Value) *queuestest.Rule {
	return &queuestest.Rule{Match: "order by lane_order, stage", Cols: stageCols, Rows: rows}
}

func singleStageRule(row []driver.Value) *queuestest.Rule {
	return &queuestest.Rule{Match: "from job_stages where stage = $1", Cols: stageCols, Rows: [][]driver.Value{row}}
}

func jobRefCols() []string {
	return []string{"id", "stage", "state", "subject_kind", "subject_id", "label", "library_id", "priority",
		"attempts", "max_attempts", "outcome", "error_cause", "created_at", "started_at", "finished_at", "progress"}
}

func jobRefRow(id int64, stage, state string) []driver.Value {
	return []driver.Value{id, stage, state, "video", uidA, "Episode 1", uidB, int64(100), int64(1), int64(3),
		nil, nil, t0.Add(-time.Hour), t0.Add(-time.Minute), nil,
		`{"step":"extract","steps":["probe","extract"],"done_units":3,"total_units":10}`}
}

// ---------- pure functions ----------

func TestWaitingReason_Precedence(t *testing.T) {
	ptr := func(s string) *string { return &s }
	base := func() stageInputs {
		return stageInputs{Info: StageInfo{Stage: "x", Enabled: true}, PoolSize: 2, Now: t0, Served: Coverage{Workers: 1}}
	}
	tests := []struct {
		name string
		mut  func(*stageInputs)
		want WaitingReason
		none bool
	}{
		{"not configured beats everything", func(i *stageInputs) {
			i.Info.Enabled, i.Info.DisabledReason, i.PausedAll = false, ptr("not_configured"), true
		}, WaitNotConfigured, false},
		{"operator disabled", func(i *stageInputs) { i.Info.Enabled, i.Info.DisabledReason = false, ptr("operator") }, WaitDisabled, false},
		{"paused all beats paused", func(i *stageInputs) { i.PausedAll, i.Info.Paused = true, true }, WaitPausedAll, false},
		{"paused", func(i *stageInputs) { i.Info.Paused = true; i.Live.Pending, i.Live.Ready = 3, 3 }, WaitPaused, false},
		{"awaiting review", func(i *stageInputs) { i.Awaiting = 2 }, WaitAwaitingReview, false},
		{"awaiting review yields to pending work", func(i *stageInputs) {
			i.Awaiting, i.Live.Pending = 2, 1
			i.Live.Ready = 0
		}, WaitDebouncing, false},
		{"no worker serves a stage with pending work", func(i *stageInputs) { i.Live.Pending, i.Live.Ready, i.Served = 2, 2, Coverage{} }, WaitNoWorker, false},
		{"pool full", func(i *stageInputs) { i.Live.Pending, i.Live.Ready, i.PoolBusy = 4, 4, 2 }, WaitPoolFull, false},
		{"debouncing", func(i *stageInputs) { i.Live.Pending = 2 }, WaitDebouncing, false},
		{"backoff", func(i *stageInputs) { i.Live.Pending, i.Live.Backoff = 2, 1 }, WaitBackoff, false},
		{"backoff on an unreachable service", func(i *stageInputs) {
			i.Live.Pending, i.Live.Backoff, i.Live.BackoffCause = 2, 1, ptr("plex_unreachable")
		}, WaitServiceRetrying, false},
		{"backoff on another cause stays backoff", func(i *stageInputs) {
			i.Live.Pending, i.Live.Backoff, i.Live.BackoffCause = 2, 1, ptr("nas_timeout")
		}, WaitBackoff, false},
		{"upstream", func(i *stageInputs) { i.UpstreamLive = true }, WaitUpstream, false},
		{"idle", func(*stageInputs) {}, WaitIdle, false},
		{"ready and free slot -> about to claim", func(i *stageInputs) { i.Live.Pending, i.Live.Ready = 1, 1 }, "", true},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			in := base()
			tc.mut(&in)
			got := waitingReason(in)
			if tc.none {
				if got != nil {
					t.Fatalf("want nil, got %v", *got)
				}
				return
			}
			if got == nil || *got != tc.want {
				t.Fatalf("got %v want %v", got, tc.want)
			}
		})
	}
}

func TestSummarizeStage(t *testing.T) {
	since := t0.Add(-2 * time.Hour)
	oldest := t0.Add(-90 * time.Second)
	tests := []struct {
		name       string
		in         stageInputs
		wantTotal  int
		wantDone   int
		wantRate   float64
		wantETA    *float64
		wantReason bool
		wantNoWork bool
		wantOldest *float64
	}{
		{
			name: "active wave with rate and eta",
			in: stageInputs{Info: StageInfo{Stage: "probe", Enabled: true},
				Live:     liveCounts{Pending: 40, Ready: 30, Running: 2, Since: &since, Oldest: &oldest},
				Done:     doneCounts{Done: 50, Skipped: 5, Recent: 20},
				WaveDone: 60, FailedOpen: 3, Now: t0, Served: Coverage{Workers: 1, Hosts: []string{"nas"}}},
			wantTotal: 60 + 42 + 3, wantDone: 60, wantRate: 2, wantETA: fp(900), wantOldest: fp(90),
		},
		{
			name: "no live rows -> 24h window, skipped counts as done",
			in: stageInputs{Info: StageInfo{Stage: "probe", Enabled: true},
				Done: doneCounts{Done: 7, Skipped: 3}, Now: t0, Served: Coverage{Workers: 1}},
			wantTotal: 10, wantDone: 10, wantReason: true,
		},
		{
			name:       "no worker serves an enabled stage",
			in:         stageInputs{Info: StageInfo{Stage: "transcribe", Enabled: true}, Live: liveCounts{Pending: 2, Ready: 2}, Now: t0},
			wantReason: true, wantNoWork: true, wantTotal: 2,
		},
		{
			name:       "idle stage with no worker is not flagged (nothing is waiting)",
			in:         stageInputs{Info: StageInfo{Stage: "transcribe", Enabled: true}, Now: t0},
			wantReason: true,
		},
		{
			name:       "disabled stage is not flagged no_worker",
			in:         stageInputs{Info: StageInfo{Stage: "plex_collection", Enabled: false}, Now: t0},
			wantReason: true,
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			s := summarizeStage(tc.in)
			if s.Wave.Total != tc.wantTotal || s.Wave.Done != tc.wantDone {
				t.Errorf("wave = %+v want total %d done %d", s.Wave, tc.wantTotal, tc.wantDone)
			}
			if s.RatePerMin != tc.wantRate {
				t.Errorf("rate %v want %v", s.RatePerMin, tc.wantRate)
			}
			if (s.EtaSec == nil) != (tc.wantETA == nil) || (s.EtaSec != nil && *s.EtaSec != *tc.wantETA) {
				t.Errorf("eta %v want %v", s.EtaSec, tc.wantETA)
			}
			if (s.OldestWaitingSec == nil) != (tc.wantOldest == nil) || (s.OldestWaitingSec != nil && *s.OldestWaitingSec != *tc.wantOldest) {
				t.Errorf("oldest %v want %v", s.OldestWaitingSec, tc.wantOldest)
			}
			if tc.wantReason && s.WaitingReason == nil {
				t.Errorf("want a waiting reason")
			}
			if !tc.wantReason && tc.in.Live.Running == 0 && s.WaitingReason == nil && false {
				t.Errorf("unexpected")
			}
			if s.NoWorker != tc.wantNoWork {
				t.Errorf("no_worker %v want %v", s.NoWorker, tc.wantNoWork)
			}
			if s.RunningJobs == nil || s.ServedBy == nil {
				t.Errorf("arrays must be non-nil for JSON")
			}
		})
	}
}

func fp(v float64) *float64 { return &v }

func TestSummarizeStage_RunningHasNoWaitingReason(t *testing.T) {
	s := summarizeStage(stageInputs{Info: StageInfo{Stage: "probe", Enabled: true},
		Live: liveCounts{Running: 1, Pending: 1}, Now: t0})
	if s.WaitingReason != nil {
		t.Fatalf("waiting_reason must be unset while running, got %v", *s.WaitingReason)
	}
}

func TestEffectiveSize(t *testing.T) {
	two, zero := 2, 0
	tests := []struct {
		name      string
		pool      string
		size, def *int
		cpus      int
		want      int
	}{
		{"explicit size", "nas_read", &two, nil, 8, 2},
		{"explicit zero blocks the pool", "nas_read", &zero, &two, 8, 0},
		{"falls back to default", "nas_read", nil, &two, 8, 2},
		{"cpu auto = cores-1", "cpu", nil, nil, 8, 7},
		{"cpu auto minimum 1", "cpu", nil, nil, 1, 1},
		{"unknown pool -> 1", "weird", nil, nil, 8, 1},
	}
	for _, tc := range tests {
		if got := effectiveSize(tc.pool, tc.size, tc.def, tc.cpus); got != tc.want {
			t.Errorf("%s: got %d want %d", tc.name, got, tc.want)
		}
	}
}

func TestFillFedBy(t *testing.T) {
	stages := []StageInfo{
		{Stage: "probe", Edges: []Edge{{To: "detect"}, {To: "thumb_hash"}}},
		{Stage: "thumb_hash", Edges: []Edge{{To: "detect"}}},
		{Stage: "detect", MayRequest: []string{"fingerprint"}},
		{Stage: "fingerprint", Edges: []Edge{{To: "detect"}, {To: "fingerprint"}}},
	}
	fillFedBy(stages)
	got := map[string]string{}
	for _, s := range stages {
		got[s.Stage] = strings.Join(s.FedBy, ",")
	}
	want := map[string]string{"probe": "", "thumb_hash": "probe", "detect": "fingerprint,probe,thumb_hash", "fingerprint": "detect"}
	for k, v := range want {
		if got[k] != v {
			t.Errorf("fed_by[%s] = %q want %q", k, got[k], v)
		}
	}
}

func TestServedBy_OnlyOnlineWorkers(t *testing.T) {
	ws := []Worker{
		{ID: "a", Host: "mac", Stages: []string{"transcribe"}, Online: true},
		{ID: "b", Host: "mac", Stages: []string{"transcribe", "probe"}, Online: true},
		{ID: "c", Host: "nas", Stages: []string{"probe"}, Online: false},
	}
	got := servedBy(ws)
	if got["transcribe"].Workers != 2 || len(got["transcribe"].Hosts) != 1 {
		t.Errorf("transcribe = %+v", got["transcribe"])
	}
	if got["probe"].Workers != 1 || got["probe"].Hosts[0] != "mac" {
		t.Errorf("probe = %+v (offline worker must not count)", got["probe"])
	}
}

func TestErrorTextAndLegacyCause(t *testing.T) {
	tests := []struct{ raw, msg, cause string }{
		{`{"kind":"TimeoutError","message":"read timed out","retryable":true}`, "read timed out", "TimeoutError"},
		{`{"kind":"OSError"}`, "OSError", "OSError"},
		{"plain failure", "plain failure", "unknown"},
		{"{broken", "{broken", "unknown"},
		{"", "", "unknown"},
	}
	for _, tc := range tests {
		if got := errorText(tc.raw); got != tc.msg {
			t.Errorf("errorText(%q)=%q want %q", tc.raw, got, tc.msg)
		}
		if got := legacyCause(tc.raw); got != tc.cause {
			t.Errorf("legacyCause(%q)=%q want %q", tc.raw, got, tc.cause)
		}
	}
}

func TestDescribeCause(t *testing.T) {
	if c := describeCause("plex_auth"); c.Action != ActionOpenSettings {
		t.Errorf("plex_auth action = %s", c.Action)
	}
	if c := describeCause(CauseAwaitingReview); c.Action != ActionApply {
		t.Errorf("awaiting_review action = %s", c.Action)
	}
	if c := describeCause("OSError:something <path>"); c.Label != "OSError:something <path>" || c.Action != ActionRetry {
		t.Errorf("fallback = %+v", c)
	}
}

func TestParseCursor(t *testing.T) {
	for in, want := range map[string]struct {
		n  int64
		ok bool
	}{"": {0, true}, "42": {42, true}, "-1": {0, false}, "x": {0, false}} {
		n, ok := parseCursor(in)
		if n != want.n || ok != want.ok {
			t.Errorf("parseCursor(%q) = %d,%v", in, n, ok)
		}
	}
}

// ---------- snapshot ----------

func snapshotRules() []*queuestest.Rule {
	return []*queuestest.Rule{
		stagesRule(
			stageRow("probe", "episodes", 1, "nas_read", withEdges(`[{"to":"detect","map":"library_of"}]`)),
			stageRow("detect", "seasons", 1, "cpu", withKinds("{library}")),
			stageRow("transcribe", "media", 2, "gpu", withExtension("whisper")),
			stageRow("plex_collection", "seasons", 4, "network_plex", withExternal(), withDisabled("not_configured"), withExtension("plex")),
		),
		{Match: "from worker_pools", Cols: []string{"pool", "label", "scope", "size", "default_size", "updated_at", "updated_by"},
			Rows: [][]driver.Value{
				{"cpu", "CPU", "per_worker", nil, nil, t0, nil},
				{"gpu", "GPU", "per_worker", int64(1), int64(1), t0, "user:u1"},
				{"nas_read", "NAS reads", "global", int64(2), int64(2), t0, nil},
				{"network_plex", "Plex", "global", int64(2), int64(2), t0, nil},
			}},
		{Match: "group by s.pool", Cols: []string{"pool", "n"}, Rows: [][]driver.Value{{"nas_read", int64(2)}}},
		{Match: "from queue_control", Cols: []string{"paused"}, Rows: [][]driver.Value{{false}}},
		{Match: "min(created_at) filter", Cols: []string{"stage", "pending", "ready", "backoff", "running", "paused", "since", "oldest", "backoff_cause"},
			Rows: [][]driver.Value{
				{"probe", int64(10), int64(8), int64(0), int64(2), int64(0), t0.Add(-time.Hour), t0.Add(-2 * time.Minute), nil},
				{"transcribe", int64(3), int64(3), int64(0), int64(0), int64(0), t0.Add(-time.Hour), t0.Add(-time.Minute), nil},
			}},
		{Match: "and finished_at > $1 group by stage", Cols: []string{"stage", "done", "skipped", "recent"},
			Rows: [][]driver.Value{{"probe", int64(100), int64(20), int64(30)}}},
		{Match: "select w.stage", Cols: []string{"stage", "n"}, Rows: [][]driver.Value{{"probe", int64(50)}}},
		{Match: "order by j.started_at", Cols: jobRefCols(), Rows: [][]driver.Value{jobRefRow(11, "probe", "running")}},
		{Match: "group by stage, error_cause",
			Cols: []string{"stage", "cause", "n", "first", "last", "msg", "samples"},
			Rows: [][]driver.Value{{"probe", "nas_timeout", int64(4), t0.Add(-time.Hour), t0, `{"kind":"X","message":"timed out"}`, "{video:" + uidA + "}"}}},
		{Match: "limit 5000", Cols: []string{"id", "stage", "kind", "sid", "error", "at"}},
		{Match: "s.external_write", Cols: []string{"stage", "n", "first", "last", "samples"}},
		{Match: "stage = any($2::text[])", Cols: []string{"stage", "n"}, Rows: [][]driver.Value{{"transcribe", int64(3)}}},
		{Match: "from workers order by host",
			Cols: []string{"id", "host", "stages", "pools", "started_at", "last_seen_at", "version"},
			Rows: [][]driver.Value{
				{"w1", "nas", "{probe,detect}", `{"cpu":2}`, t0.Add(-time.Hour), t0.Add(-5 * time.Second), "1.0"},
				{"w2", "mac", "{transcribe}", nil, t0.Add(-time.Hour), t0.Add(-10 * time.Minute), nil},
			}},
		{Match: "triggered_by_stage is not null", Cols: []string{"from", "to", "n"},
			Rows: [][]driver.Value{{"probe", "detect", int64(20)}}},
		{Match: "select distinct on (metric)", Cols: []string{"metric", "value"}, Rows: [][]driver.Value{
			{"nas.open_ms_p50", 12.5}, {"nas.open_ms_p95", 80.0}, {"cpu.pct", 33.0}, {"net.plex_ms", -1.0}}},
		{Match: "metric = 'nas.timeouts' and host", Cols: []string{"sum"}, Rows: [][]driver.Value{{3.0}}},
		{Match: "date_trunc('minute'", Cols: []string{"metric", "t", "avg", "sum"}, Rows: [][]driver.Value{
			{"nas.open_ms_p95", t0.Add(-2 * time.Minute), 70.0, 70.0}, {"nas.open_ms_p95", t0.Add(-time.Minute), 80.0, 80.0},
			{"nas.timeouts", t0.Add(-time.Minute), 1.0, 3.0}}},
	}
}

func TestSnapshot(t *testing.T) {
	f := newFixture(snapshotRules()...)
	rec := f.do(http.MethodGet, "/api/queues", "", viewer())
	if rec.Code != 200 {
		t.Fatalf("status %d: %s", rec.Code, rec.Body.String())
	}
	var snap Snapshot
	decode(t, rec, &snap)

	if snap.PausedAll || len(snap.Stages) != 4 || len(snap.Queues) != 4 {
		t.Fatalf("snapshot shape: %+v", snap)
	}
	q := map[string]QueueSummary{}
	for _, s := range snap.Queues {
		q[s.Stage] = s
	}
	probe := q["probe"]
	if probe.Counts.Pending != 10 || probe.Counts.Ready != 8 || probe.Counts.Running != 2 ||
		probe.Counts.Done24h != 100 || probe.Counts.Skipped24h != 20 || probe.Counts.FailedOpen != 4 {
		t.Errorf("probe counts = %+v", probe.Counts)
	}
	// wave: 50 done since oldest live row + 12 live + 4 failed.
	if probe.Wave.Done != 50 || probe.Wave.Total != 66 {
		t.Errorf("probe wave = %+v", probe.Wave)
	}
	if probe.RatePerMin != 3 || probe.EtaSec == nil || *probe.EtaSec != 160 {
		t.Errorf("probe rate/eta = %v / %v", probe.RatePerMin, probe.EtaSec)
	}
	if probe.WaitingReason != nil {
		t.Errorf("running queue must not carry a waiting reason")
	}
	if len(probe.RunningJobs) != 1 || probe.RunningJobs[0].Progress == nil || probe.RunningJobs[0].Progress.TotalUnits != 10 {
		t.Errorf("running jobs = %+v", probe.RunningJobs)
	}
	if len(probe.ServedBy) != 1 || probe.ServedBy[0] != "nas" || probe.ServedWorkers != 1 || probe.NoWorker {
		t.Errorf("probe served_by = %+v no_worker=%v", probe.ServedBy, probe.NoWorker)
	}

	tr := q["transcribe"]
	if !tr.NoWorker || len(tr.ServedBy) != 0 || tr.WaitingReason == nil || *tr.WaitingReason != WaitNoWorker {
		t.Errorf("transcribe: the Mac worker is offline, want no_worker; got %+v", tr)
	}
	if tr.Extension == nil || *tr.Extension != "whisper" {
		t.Errorf("extension = %v", tr.Extension)
	}
	if q["plex_collection"].WaitingReason == nil || *q["plex_collection"].WaitingReason != WaitNotConfigured {
		t.Errorf("plex waiting = %v", q["plex_collection"].WaitingReason)
	}

	// pools
	var nas Pool
	var cpu Pool
	for _, p := range snap.Pools {
		if p.Pool == "nas_read" {
			nas = p
		}
		if p.Pool == "cpu" {
			cpu = p
		}
	}
	if nas.Busy != 2 || nas.EffectiveSize != 2 || len(nas.Stages) != 1 || cpu.EffectiveSize != 4 {
		t.Errorf("pools nas=%+v cpu=%+v", nas, cpu)
	}

	if len(snap.Flows) != 1 || snap.Flows[0].From != "probe" || snap.Flows[0].PerMin != 2 {
		t.Errorf("flows = %+v", snap.Flows)
	}
	// issues: 1 failure group + 1 stale_graph? graph synced now -> no stale.
	if snap.IssuesCount != 1 {
		t.Errorf("issues_count = %d", snap.IssuesCount)
	}

	h := snap.Health
	if h.NAS.OpenMsP95 == nil || *h.NAS.OpenMsP95 != 80 || h.NAS.Timeouts != 3 || h.NAS.ReadsBusy != 2 || h.NAS.ReadsLimit != 2 {
		t.Errorf("nas health = %+v", h.NAS)
	}
	if h.Network.PlexMs == nil || *h.Network.PlexMs != -1 || h.Network.TvMs != nil {
		t.Errorf("network = %+v", h.Network)
	}
	if h.Backlog.Ready != 11 || len(h.Spark["nas_open_ms"]) != 2 || len(h.Spark["backlog_ready"]) != 1 {
		t.Errorf("backlog/spark = %+v %+v", h.Backlog, h.Spark)
	}
	for _, k := range []string{"nas_open_ms", "nas_timeouts", "cpu_pct", "backlog_ready"} {
		if h.Spark[k] == nil {
			t.Errorf("spark[%s] must be a non-nil array", k)
		}
	}
}

func TestSnapshot_RequiresAuth(t *testing.T) {
	f := newFixture(snapshotRules()...)
	if rec := f.do(http.MethodGet, "/api/queues", "", nil); rec.Code != http.StatusForbidden {
		t.Fatalf("status %d", rec.Code)
	}
}

func TestSnapshot_StaleGraphIssue(t *testing.T) {
	old := t0.Add(-48 * time.Hour)
	rules := []*queuestest.Rule{
		stagesRule(stageRow("transcribe", "media", 2, "gpu", withSynced(old))),
		{Match: "stage = any($2::text[])", Cols: []string{"stage", "n"}, Rows: [][]driver.Value{{"transcribe", int64(7)}}},
	}
	f := newFixture(rules...)
	rec := f.do(http.MethodGet, "/api/issues", "", viewer())
	var out struct {
		Groups []FailureGroup `json:"groups"`
	}
	decode(t, rec, &out)
	if len(out.Groups) != 1 || out.Groups[0].Cause != CauseStaleGraph || out.Groups[0].Count != 7 ||
		out.Groups[0].Action != ActionShowWorkers {
		t.Fatalf("groups = %+v", out.Groups)
	}
	args := f.db.Find("stage = any($2::text[])")[0].Args
	if !strings.Contains(args[1].(string), "transcribe") {
		t.Errorf("stale query args = %v", args)
	}
}

func TestGraph(t *testing.T) {
	f := newFixture(
		stagesRule(stageRow("probe", "episodes", 1, "nas_read", withEdges(`[{"to":"detect","map":"library_of"}]`)),
			stageRow("detect", "seasons", 1, "cpu")),
		&queuestest.Rule{Match: "from worker_pools", Cols: []string{"pool", "label", "scope", "size", "default_size", "updated_at", "updated_by"},
			Rows: [][]driver.Value{{"cpu", "CPU", "per_worker", nil, nil, t0, nil}}},
	)
	rec := f.do(http.MethodGet, "/api/queues/graph", "", viewer())
	var out struct {
		Stages []StageInfo `json:"stages"`
		Pools  []Pool      `json:"pools"`
	}
	decode(t, rec, &out)
	if len(out.Stages) != 2 || out.Stages[0].Stage != "probe" || out.Stages[1].FedBy[0] != "probe" {
		t.Fatalf("stages = %+v", out.Stages)
	}
	if out.Stages[0].Edges[0].When != "output_changed" {
		t.Errorf("edge default 'when' = %q", out.Stages[0].Edges[0].When)
	}
	if len(out.Pools) != 1 || out.Pools[0].Stages[0] != "detect" {
		t.Errorf("pools = %+v", out.Pools)
	}
}

func TestMountedRoutes(t *testing.T) {
	f := newFixture()
	want := map[string]bool{}
	for _, p := range []string{
		"GET /api/queues", "GET /api/queues/graph", "GET /api/queues/{stage}", "GET /api/queues/{stage}/rate",
		"GET /api/queues/{stage}/jobs", "GET /api/queues/{stage}/failures", "POST /api/queues/{stage}/pause",
		"POST /api/queues/{stage}/resume", "POST /api/queues/pause-all", "POST /api/queues/resume-all",
		"PATCH /api/queues/{stage}", "POST /api/queues/{stage}/retry", "POST /api/queues/{stage}/apply",
		"GET /api/pools", "PUT /api/pools/{pool}", "POST /api/queue/retrigger", "GET /api/issues",
		"GET /api/activity", "GET /api/jobs/{id}/events", "GET /api/items/{kind}/{id}/history",
		"GET /api/queues/libraries", "GET /api/queues/health", "GET /api/workers", "GET /api/extensions",
		"PUT /api/extensions/{name}", "POST /api/extensions/{name}/test",
	} {
		want[p] = true
	}
	_ = chi.Walk(f.r, func(method, route string, _ http.Handler, _ ...func(http.Handler) http.Handler) error {
		delete(want, method+" "+route)
		return nil
	})
	for missing := range want {
		t.Errorf("route not mounted: %s", missing)
	}
}

func TestRetryingService(t *testing.T) {
	ptr := func(s string) *string { return &s }
	cases := map[string]string{"plex_unreachable": "plex", "dizquetv_unreachable": "dizquetv", "nas_timeout": "", "_unreachable": ""}
	for cause, want := range cases {
		got := retryingService(ptr(cause))
		if (got == nil) != (want == "") || (got != nil && *got != want) {
			t.Fatalf("%s: got %v want %q", cause, got, want)
		}
	}
	if retryingService(nil) != nil {
		t.Fatal("nil cause must give nil")
	}
	s := summarizeStage(stageInputs{Info: StageInfo{Stage: "plex_index", Enabled: true}, PoolSize: 2, Now: t0,
		Served: Coverage{Workers: 1}, Live: liveCounts{Pending: 1, Backoff: 1, Running: 1, BackoffCause: ptr("plex_unreachable")}})
	if s.RetryingService == nil || *s.RetryingService != "plex" {
		t.Fatalf("summary while running: %v", s.RetryingService)
	}
}
