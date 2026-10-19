package queues

import (
	"context"
	"database/sql/driver"
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"testing"

	"github.com/Hamza-Labs-Core/Maktaba/api/internal/handlers/queues/queuestest"
	"github.com/Hamza-Labs-Core/Maktaba/api/internal/httperror"
	"github.com/Hamza-Labs-Core/Maktaba/api/internal/secrets"
)

type busEvent struct {
	ch, typ string
	payload map[string]any
}

type busSpy struct{ got []busEvent }

func (b *busSpy) Publish(_ context.Context, ch, typ string, payload map[string]any) error {
	b.got = append(b.got, busEvent{ch, typ, payload})
	return nil
}

func TestMutations_RequireAdmin(t *testing.T) {
	routes := []struct{ method, path, body string }{
		{"POST", "/api/queues/probe/pause", ""},
		{"POST", "/api/queues/probe/resume", ""},
		{"POST", "/api/queues/pause-all", ""},
		{"POST", "/api/queues/resume-all", ""},
		{"PATCH", "/api/queues/probe", `{"enabled":false}`},
		{"POST", "/api/queues/probe/retry", `{"cause":"x"}`},
		{"POST", "/api/queues/probe/apply", `{"all":true}`},
		{"PUT", "/api/pools/cpu", `{"size":2}`},
		{"POST", "/api/queue/retrigger", `{"items":[]}`},
		{"PUT", "/api/extensions/plex", `{"enabled":true}`},
		{"POST", "/api/extensions/plex/test", ""},
	}
	for _, rt := range routes {
		for name, p := range map[string]bool{"anonymous": false, "non-admin": true} {
			t.Run(rt.method+" "+rt.path+" "+name, func(t *testing.T) {
				f := newFixture()
				var rec = f.do(rt.method, rt.path, rt.body, nil)
				if p {
					rec = f.do(rt.method, rt.path, rt.body, viewer())
				}
				if rec.Code != http.StatusForbidden {
					t.Fatalf("status %d body %s", rec.Code, rec.Body.String())
				}
				for _, c := range f.db.Calls() {
					if c.Exec {
						t.Errorf("a forbidden request wrote to the DB: %s", c.SQL)
					}
				}
			})
		}
	}
}

func TestPauseResumeStage(t *testing.T) {
	tests := []struct {
		name        string
		method, url string
		affected    int64
		wantCode    int
		wantSQL     []string // fragments that must have run, in order
		forbidSQL   string
		wantEvent   bool
		paused      bool
	}{
		{"pause", "POST", "/api/queues/probe/pause", 1, 204, []string{"update job_stages set paused = true"}, "pause_requested", true, true},
		{"pause unknown stage", "POST", "/api/queues/nope/pause", 0, 404, []string{"update job_stages set paused = true"}, "", false, true},
		{"pause + interrupt on a non-cooperative stage is a no-op", "POST", "/api/queues/probe/pause?interrupt=true", 1, 204, nil, "pause_requested", true, true},
		{"pause + interrupt on transcribe flags running jobs", "POST", "/api/queues/transcribe/pause?interrupt=true", 1, 204,
			[]string{"update job_stages set paused = true", "update processing_jobs set pause_requested = true"}, "", true, true},
		{"bad interrupt flag", "POST", "/api/queues/probe/pause?interrupt=maybe", 1, 400, nil, "", false, true},
		{"resume", "POST", "/api/queues/probe/resume", 1, 204, []string{"update job_stages set paused = false"}, "pause_requested", true, false},
		{"resume transcribe clears interrupt flags", "POST", "/api/queues/transcribe/resume", 1, 204,
			[]string{"update job_stages set paused = false", "set pause_requested = false"}, "", true, false},
		{"resume unknown", "POST", "/api/queues/nope/resume", 0, 404, nil, "", false, false},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			f := newFixture(&queuestest.Rule{Match: "update job_stages set paused", Affected: tc.affected},
				&queuestest.Rule{Match: "update processing_jobs", Affected: 3})
			spy := &busSpy{}
			f.h.Events = spy
			rec := f.do(tc.method, tc.url, "", admin())
			if rec.Code != tc.wantCode {
				t.Fatalf("status %d body %s", rec.Code, rec.Body.String())
			}
			calls := f.db.Calls()
			idx := 0
			for _, c := range calls {
				if idx < len(tc.wantSQL) && strings.Contains(c.SQL, tc.wantSQL[idx]) {
					idx++
				}
				if tc.forbidSQL != "" && strings.Contains(c.SQL, tc.forbidSQL) {
					t.Errorf("unexpected statement %s", c.SQL)
				}
			}
			if idx != len(tc.wantSQL) {
				t.Errorf("expected statements %v in order, ran %v", tc.wantSQL, calls)
			}
			if tc.wantEvent {
				if len(spy.got) != 1 || spy.got[0].ch != "jobs" || spy.got[0].typ != "queue.control" ||
					spy.got[0].payload["paused"] != tc.paused || spy.got[0].payload["by"] != "user:u1" {
					t.Errorf("events = %+v", spy.got)
				}
			} else if len(spy.got) != 0 {
				t.Errorf("no event expected, got %+v", spy.got)
			}
		})
	}
}

func TestControlEvents_FallBackToHub(t *testing.T) {
	f := newFixture(&queuestest.Rule{Match: "update job_stages set paused", Affected: 1})
	sub := f.h.Hub.Subscribe("jobs")
	if rec := f.do("POST", "/api/queues/probe/pause", "", admin()); rec.Code != 204 {
		t.Fatalf("status %d", rec.Code)
	}
	select {
	case e := <-sub.C:
		if e.Type != "queue.control" || e.Payload["stage"] != "probe" {
			t.Errorf("event = %+v", e)
		}
	default:
		t.Fatal("expected a hub event when no bus is wired")
	}
}

func TestPauseAllResumeAll(t *testing.T) {
	for _, tc := range []struct {
		url    string
		paused bool
	}{{"/api/queues/pause-all", true}, {"/api/queues/resume-all", false}} {
		f := newFixture()
		spy := &busSpy{}
		f.h.Events = spy
		if rec := f.do("POST", tc.url, "", admin()); rec.Code != 204 {
			t.Fatalf("%s status %d", tc.url, rec.Code)
		}
		calls := f.db.Find("insert into queue_control")
		if len(calls) != 1 || calls[0].Args[0] != tc.paused {
			t.Fatalf("%s calls = %+v", tc.url, calls)
		}
		if tc.paused && calls[0].Args[2] != "user:u1" {
			t.Errorf("paused_by = %v", calls[0].Args[2])
		}
		if !tc.paused && (calls[0].Args[1] != nil || calls[0].Args[2] != nil) {
			t.Errorf("resume must clear paused_at/by: %v", calls[0].Args)
		}
		if len(spy.got) != 1 || spy.got[0].payload["stage"] != nil || spy.got[0].payload["paused"] != tc.paused {
			t.Errorf("events = %+v", spy.got)
		}
	}
}

func TestPatchStage(t *testing.T) {
	tests := []struct {
		name     string
		body     string
		stage    []driver.Value
		wantCode int
		wantArgs []any // enabled, write_mode of the UPDATE
	}{
		{"disable", `{"enabled":false}`, stageRow("probe", "episodes", 1, "nas_read"), 200, []any{false, nil}},
		{"enable", `{"enabled":true}`, stageRow("probe", "episodes", 1, "nas_read", withDisabled("operator")), 200, []any{true, nil}},
		{"write mode on external stage", `{"write_mode":"review"}`, stageRow("plex_collection", "seasons", 4, "network_plex", withExternal()), 200, []any{nil, "review"}},
		{"write mode auto accepted", `{"write_mode":"auto"}`, stageRow("plex_collection", "seasons", 4, "network_plex", withExternal()), 200, []any{nil, "auto"}},
		{"write mode on core stage", `{"write_mode":"review"}`, stageRow("probe", "episodes", 1, "nas_read"), 400, nil},
		{"bad write mode", `{"write_mode":"yolo"}`, stageRow("plex_collection", "seasons", 4, "network_plex", withExternal()), 400, nil},
		{"empty body", `{}`, stageRow("probe", "episodes", 1, "nas_read"), 400, nil},
		{"unknown field", `{"paused":true}`, stageRow("probe", "episodes", 1, "nas_read"), 400, nil},
		{"cannot enable an unconfigured stage", `{"enabled":true}`, stageRow("plex_collection", "seasons", 4, "network_plex", withExternal(), withDisabled("not_configured")), 409, nil},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			f := newFixture(singleStageRule(tc.stage), stagesRule(tc.stage))
			rec := f.do("PATCH", "/api/queues/"+tc.stage[0].(string), tc.body, admin())
			if rec.Code != tc.wantCode {
				t.Fatalf("status %d body %s", rec.Code, rec.Body.String())
			}
			ups := f.db.Find("update job_stages set enabled")
			if tc.wantArgs == nil {
				if len(ups) != 0 {
					t.Fatalf("no UPDATE expected, got %v", ups)
				}
				return
			}
			if len(ups) != 1 || ups[0].Args[1] != tc.wantArgs[0] || ups[0].Args[2] != tc.wantArgs[1] {
				t.Fatalf("update args = %+v", ups)
			}
			var info StageInfo
			decode(t, rec, &info)
			if info.Stage != tc.stage[0] {
				t.Errorf("response = %+v", info)
			}
		})
	}
	t.Run("unknown stage", func(t *testing.T) {
		f := newFixture()
		if rec := f.do("PATCH", "/api/queues/nope", `{"enabled":true}`, admin()); rec.Code != 404 {
			t.Fatalf("status %d", rec.Code)
		}
	})
}

func TestPools(t *testing.T) {
	poolCols := []string{"pool", "label", "scope", "size", "default_size", "updated_at", "updated_by"}
	poolRules := func(affected int64) []*queuestest.Rule {
		return []*queuestest.Rule{
			{Match: "update worker_pools", Affected: affected},
			{Match: "from worker_pools", Cols: poolCols, Rows: [][]driver.Value{{"nas_read", "NAS reads", "global", int64(3), int64(2), t0, "user:u1"}}},
			stagesRule(stageRow("probe", "episodes", 1, "nas_read")),
		}
	}
	tests := []struct {
		name     string
		body     string
		affected int64
		wantCode int
		wantSize any
	}{
		{"resize", `{"size":3}`, 1, 200, int64(3)},
		{"zero is allowed", `{"size":0}`, 1, 200, int64(0)},
		{"null means auto", `{"size":null}`, 1, 200, nil},
		{"max", `{"size":64}`, 1, 200, int64(64)},
		{"too big", `{"size":65}`, 1, 400, nil},
		{"negative", `{"size":-1}`, 1, 400, nil},
		{"not a number", `{"size":"x"}`, 1, 400, nil},
		{"fractional", `{"size":1.5}`, 1, 400, nil},
		{"missing size", `{}`, 1, 400, nil},
		{"unknown pool", `{"size":2}`, 0, 404, nil},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			f := newFixture(poolRules(tc.affected)...)
			spy := &busSpy{}
			f.h.Events = spy
			rec := f.do("PUT", "/api/pools/nas_read", tc.body, admin())
			if rec.Code != tc.wantCode {
				t.Fatalf("status %d body %s", rec.Code, rec.Body.String())
			}
			if tc.wantCode != 200 {
				if len(spy.got) != 0 {
					t.Errorf("no event expected")
				}
				return
			}
			up := f.db.Find("update worker_pools")[0]
			if up.Args[1] != tc.wantSize || up.Args[3] != "user:u1" {
				t.Errorf("args = %v", up.Args)
			}
			var p Pool
			decode(t, rec, &p)
			if p.Pool != "nas_read" || p.Stages[0] != "probe" {
				t.Errorf("pool = %+v", p)
			}
			if len(spy.got) != 1 || spy.got[0].typ != "pool.updated" || spy.got[0].payload["pool"] != "nas_read" {
				t.Errorf("events = %+v", spy.got)
			}
		})
	}
	t.Run("list", func(t *testing.T) {
		f := newFixture(poolRules(1)...)
		rec := f.do("GET", "/api/pools", "", viewer())
		var out struct {
			Pools []Pool `json:"pools"`
		}
		decode(t, rec, &out)
		if rec.Code != 200 || len(out.Pools) != 1 || out.Pools[0].EffectiveSize != 3 {
			t.Fatalf("%d %s", rec.Code, rec.Body.String())
		}
	})
}

// ---------- retry / apply ----------

func enqueueRule(rows ...[]driver.Value) *queuestest.Rule {
	return &queuestest.Rule{Match: "processing_jobs_enqueue", Cols: []string{"job_id", "outcome"}, Rows: rows}
}

func TestRetry(t *testing.T) {
	failedCols := []string{"id", "kind", "sid", "cause", "error"}
	tests := []struct {
		name        string
		body        string
		rules       []*queuestest.Rule
		wantCode    int
		wantEnq     int
		wantCoal    int
		wantEnqCall int
		wantPrio    any
	}{
		{
			name: "by cause: coded rows and legacy rows with the same kind",
			body: `{"cause":"nas_timeout"}`,
			rules: []*queuestest.Rule{
				{Match: "error_cause = $2::text", Cols: failedCols, Rows: [][]driver.Value{
					{int64(1), "video", uidA, "nas_timeout", nil},
					{int64(2), "video", uidB, nil, `{"kind":"nas_timeout","message":"x"}`},
					{int64(3), "video", uidB, nil, `{"kind":"OSError"}`}, // other legacy cause: skipped
				}},
				enqueueRule([]driver.Value{int64(50), "inserted"}),
			},
			wantCode: 200, wantEnqCall: 2, wantEnq: 2, wantCoal: 0, wantPrio: int64(200),
		},
		{
			name: "by ids: only failed rows are retried; coalesced counted",
			body: `{"job_ids":[7,8]}`,
			rules: []*queuestest.Rule{
				// only rows still latest for their subject and stage (a superseded failure is history)
				{Match: "j.id = any($2::bigint[]) and not exists", Cols: []string{"id", "kind", "sid", "state"}, Rows: [][]driver.Value{
					{int64(7), "video", uidA, "failed"}, {int64(8), "video", uidB, "done"}}},
				enqueueRule([]driver.Value{int64(60), "coalesced"}),
			},
			wantCode: 200, wantEnqCall: 1, wantEnq: 0, wantCoal: 1, wantPrio: nil,
		},
		{name: "neither cause nor ids", body: `{}`, wantCode: 400},
		{name: "unknown field", body: `{"cause":"x","nope":1}`, wantCode: 400},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			rules := append([]*queuestest.Rule{singleStageRule(stageRow("probe", "episodes", 1, "nas_read")), stagesRule(stageRow("probe", "episodes", 1, "nas_read"))}, tc.rules...)
			f := newFixture(rules...)
			rec := f.do("POST", "/api/queues/probe/retry", tc.body, admin())
			if rec.Code != tc.wantCode {
				t.Fatalf("status %d body %s", rec.Code, rec.Body.String())
			}
			if tc.wantCode != 200 {
				return
			}
			var out struct{ Enqueued, Coalesced int }
			decode(t, rec, &out)
			if out.Enqueued != tc.wantEnq || out.Coalesced != tc.wantCoal {
				t.Errorf("out = %+v", out)
			}
			calls := f.db.Find("processing_jobs_enqueue")
			if len(calls) != tc.wantEnqCall {
				t.Fatalf("enqueue calls = %d", len(calls))
			}
			a := calls[0].Args
			if a[2] != "probe" || a[4] != true || a[3] != tc.wantPrio {
				t.Errorf("args = %v (want force=true, prio %v)", a, tc.wantPrio)
			}
			if !strings.Contains(a[6].(string), `"of_job"`) || !strings.Contains(a[6].(string), `"why":"retry"`) {
				t.Errorf("reason = %v", a[6])
			}
			// one transaction
			if len(f.db.Find("commit")) != 1 {
				t.Errorf("expected exactly one COMMIT")
			}
		})
	}
	t.Run("unknown stage", func(t *testing.T) {
		f := newFixture()
		if rec := f.do("POST", "/api/queues/nope/retry", `{"cause":"x"}`, admin()); rec.Code != 404 {
			t.Fatalf("status %d", rec.Code)
		}
	})
	t.Run("enqueue error rolls back", func(t *testing.T) {
		f := newFixture(singleStageRule(stageRow("probe", "episodes", 1, "nas_read")), stagesRule(stageRow("probe", "episodes", 1, "nas_read")),
			&queuestest.Rule{Match: "id = any($2::bigint[])", Cols: []string{"id", "kind", "sid", "state"}, Rows: [][]driver.Value{{int64(7), "video", uidA, "failed"}}},
			&queuestest.Rule{Match: "processing_jobs_enqueue", Err: errors.New("boom")})
		if rec := f.do("POST", "/api/queues/probe/retry", `{"job_ids":[7]}`, admin()); rec.Code != 500 {
			t.Fatalf("status %d", rec.Code)
		}
		if len(f.db.Find("rollback")) != 1 || len(f.db.Find("commit")) != 0 {
			t.Errorf("want rollback, no commit")
		}
	})
}

func TestApply(t *testing.T) {
	ext := stageRow("plex_collection", "seasons", 4, "network_plex", withExternal())
	reviewRows := &queuestest.Rule{Match: "outcome = 'awaiting_review'", Cols: []string{"id", "kind", "sid", "hash"},
		Rows: [][]driver.Value{{int64(31), "season", uidA, `["d1","d2"]`}, {int64(32), "season", uidB, "[]"}}}
	tests := []struct {
		name     string
		stage    []driver.Value
		body     string
		wantCode int
		wantEnq  int
	}{
		{"apply all", ext, `{"all":true}`, 200, 2},
		{"apply ids", ext, `{"job_ids":[31]}`, 200, 2}, // fake returns both rows; the SQL filters by id
		{"nothing selected", ext, `{}`, 400, 0},
		{"core stage has no review", stageRow("probe", "episodes", 1, "nas_read"), `{"all":true}`, 409, 0},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			f := newFixture(singleStageRule(tc.stage), stagesRule(tc.stage), reviewRows,
				enqueueRule([]driver.Value{int64(90), "inserted"}))
			rec := f.do("POST", "/api/queues/"+tc.stage[0].(string)+"/apply", tc.body, admin())
			if rec.Code != tc.wantCode {
				t.Fatalf("status %d body %s", rec.Code, rec.Body.String())
			}
			if tc.wantCode != 200 {
				return
			}
			var out struct{ Enqueued int }
			decode(t, rec, &out)
			if out.Enqueued != tc.wantEnq {
				t.Errorf("enqueued %d", out.Enqueued)
			}
			a := f.db.Find("processing_jobs_enqueue")[0].Args
			if a[4] != true {
				t.Errorf("apply must force: %v", a)
			}
			var payload map[string]any
			_ = json.Unmarshal([]byte(a[8].(string)), &payload)
			digests, _ := payload["plan_digests"].([]any)
			if payload["apply"] != true || len(digests) != 2 || digests[0] != "d1" || digests[1] != "d2" {
				t.Errorf("payload = %v", payload)
			}
			// the approval is merged into the row the enqueue landed on (it may have
			// coalesced into a pending or running job, whose payload the function keeps)
			merges := f.db.Find("'plan_digests'")
			if len(merges) != 2 || merges[0].Args[0] != int64(90) || merges[0].Args[1] != `["d1","d2"]` {
				t.Errorf("approval merges = %+v", merges)
			}
		})
	}
}

// ---------- retrigger ----------

func TestRetrigger(t *testing.T) {
	stages := stagesRule(
		stageRow("probe", "episodes", 1, "nas_read", withEntry("{video}")),
		stageRow("detect", "seasons", 1, "cpu", withKinds("{library}"), withEntry("{library}")),
		stageRow("youtube_sync", "sources", 0, "network_yt", withKinds("{library}"), withEntry("{library}")),
		stageRow("tag", "seasons", 2, "cpu", withKinds("{season}"), withEntry("{season}")),
		stageRow("rate_style", "seasons", 3, "cpu", withKinds("{season}"), withEntry("{season}")),
	)
	ins := func(id int64) []driver.Value { return []driver.Value{id, "inserted"} }
	tests := []struct {
		name       string
		body       string
		rules      []*queuestest.Rule
		wantCode   int
		wantStages []string
		wantForce  bool
		wantCasc   bool
		wantPrio   int64
		wantOut    []string
	}{
		{
			name: "video enqueues its entry stage at default priority",
			body: `{"items":[{"subject_kind":"video","subject_id":"` + uidA + `"}]}`, wantCode: 200,
			rules: []*queuestest.Rule{enqueueRule(ins(1))}, wantStages: []string{"probe"}, wantPrio: 50, wantOut: []string{"inserted"},
		},
		{
			name: "library enqueues every entry stage",
			body: `{"items":[{"subject_kind":"library","subject_id":"` + uidA + `"}],"mode":"force","priority":10}`, wantCode: 200,
			rules: []*queuestest.Rule{enqueueRule(ins(2))}, wantStages: []string{"youtube_sync", "detect"}, wantForce: true, wantPrio: 10,
			wantOut: []string{"inserted", "inserted"},
		},
		{
			name: "explicit stage + force_cascade",
			body: `{"items":[{"subject_kind":"season","subject_id":"` + uidA + `","stage":"tag"}],"mode":"force_cascade"}`, wantCode: 200,
			rules: []*queuestest.Rule{enqueueRule([]driver.Value{3, "coalesced"})}, wantStages: []string{"tag"}, wantCasc: true, wantPrio: 50,
			wantOut: []string{"coalesced"},
		},
		{
			name: "series expands to its seasons",
			body: `{"items":[{"subject_kind":"series","subject_id":"` + uidA + `"}]}`, wantCode: 200,
			rules: []*queuestest.Rule{
				{Match: "from series_seasons where series_id", Cols: []string{"id"}, Rows: [][]driver.Value{{uidA}, {uidB}}},
				enqueueRule(ins(4)),
			},
			wantStages: []string{"tag", "rate_style", "tag", "rate_style"}, wantPrio: 50,
			wantOut: []string{"inserted", "inserted", "inserted", "inserted"},
		},
		{
			name: "catalog has no entry stage here -> unknown_stage row",
			body: `{"items":[{"subject_kind":"catalog","subject_id":"00000000-0000-0000-0000-00000000ca7a"}]}`, wantCode: 200,
			wantOut: []string{"unknown_stage"},
		},
		{
			name: "disabled stage outcome is passed through",
			body: `{"items":[{"subject_kind":"video","subject_id":"` + uidA + `"}]}`, wantCode: 200,
			rules: []*queuestest.Rule{enqueueRule([]driver.Value{nil, "disabled"})}, wantStages: []string{"probe"}, wantPrio: 50,
			wantOut: []string{"disabled"},
		},
		{name: "empty items", body: `{"items":[]}`, wantCode: 400},
		{name: "bad kind", body: `{"items":[{"subject_kind":"movie","subject_id":"` + uidA + `"}]}`, wantCode: 400},
		{name: "bad id", body: `{"items":[{"subject_kind":"video","subject_id":"nope"}]}`, wantCode: 400},
		{name: "bad mode", body: `{"items":[{"subject_kind":"video","subject_id":"` + uidA + `"}],"mode":"nuke"}`, wantCode: 400},
		{name: "bad priority", body: `{"items":[{"subject_kind":"video","subject_id":"` + uidA + `"}],"priority":-5}`, wantCode: 400},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			f := newFixture(append([]*queuestest.Rule{stages}, tc.rules...)...)
			rec := f.do("POST", "/api/queue/retrigger", tc.body, admin())
			if rec.Code != tc.wantCode {
				t.Fatalf("status %d body %s", rec.Code, rec.Body.String())
			}
			if tc.wantCode != 200 {
				return
			}
			var out RetriggerResponse
			decode(t, rec, &out)
			if len(out.Results) != len(tc.wantOut) {
				t.Fatalf("results = %+v", out.Results)
			}
			for i, o := range tc.wantOut {
				if out.Results[i].Outcome != o {
					t.Errorf("result[%d] = %+v want %s", i, out.Results[i], o)
				}
			}
			calls := f.db.Find("processing_jobs_enqueue")
			if len(calls) != len(tc.wantStages) {
				t.Fatalf("enqueue calls %d want %v", len(calls), tc.wantStages)
			}
			for i, st := range tc.wantStages {
				a := calls[i].Args
				if a[2] != st || a[4] != tc.wantForce || a[5] != tc.wantCasc || a[3] != tc.wantPrio {
					t.Errorf("call %d args = %v", i, a)
				}
			}
			if len(f.db.Find("commit")) != 1 {
				t.Errorf("all items must share one transaction")
			}
		})
	}
	t.Run("too many items", func(t *testing.T) {
		items := make([]string, 5001)
		for i := range items {
			items[i] = `{"subject_kind":"video","subject_id":"` + uidA + `"}`
		}
		f := newFixture(stages)
		rec := f.do("POST", "/api/queue/retrigger", `{"items":[`+strings.Join(items, ",")+`]}`, admin())
		if rec.Code != 400 {
			t.Fatalf("status %d", rec.Code)
		}
	})
}

// ---------- extensions ----------

func TestExtensions(t *testing.T) {
	row := func(name string, enabled bool, settings string) *queuestest.Rule {
		return &queuestest.Rule{Match: "from extensions", Cols: []string{"name", "enabled", "settings", "updated_at"},
			Rows: [][]driver.Value{{name, enabled, settings, t0}}}
	}
	ctx := context.Background()
	plexToken := secrets.ExtensionName("plex", "token")
	t.Run("list masks secrets and never leaks unknown keys", func(t *testing.T) {
		// a token still in the settings (before the start-up move) is masked too
		f := newFixture(row("plex", true, `{"url":"http://plex:32400","token":"SUPERSECRET","stray":"LEAK"}`))
		rec := f.do("GET", "/api/extensions", "", viewer())
		if rec.Code != 200 {
			t.Fatalf("status %d", rec.Code)
		}
		body := rec.Body.String()
		if strings.Contains(body, "SUPERSECRET") || strings.Contains(body, "LEAK") {
			t.Fatalf("secret leaked: %s", body)
		}
		var out struct{ Extensions []Extension }
		decode(t, rec, &out)
		if len(out.Extensions) != 3 || out.Extensions[0].Name != "cloudflare_stream" || out.Extensions[1].Name != "dizquetv" {
			t.Fatalf("extensions = %+v", out.Extensions)
		}
		plex := out.Extensions[2]
		if !plex.Enabled || !plex.Configured || plex.Settings["token"] != SecretMask || plex.SecretsSet[0] != "token" {
			t.Errorf("plex = %+v", plex)
		}
		dq := out.Extensions[1]
		if dq.Enabled || dq.Configured {
			t.Errorf("dizquetv without a row must be disabled+unconfigured: %+v", dq)
		}
	})

	// TC5: the token lives in home_secrets and the API only says "set".
	t.Run("token in home_secrets is masked", func(t *testing.T) {
		f := newFixture(row("plex", true, `{"url":"http://plex:32400"}`))
		f.h.Secrets = secrets.NewMemory()
		_ = f.h.Secrets.Put(ctx, plexToken, []byte("SUPERSECRET"), "")
		_ = f.h.Secrets.Put(ctx, "cf_api_token", []byte("CFSECRET"), "")
		rec := f.do("GET", "/api/extensions", "", viewer())
		body := rec.Body.String()
		if strings.Contains(body, "SUPERSECRET") || strings.Contains(body, "CFSECRET") {
			t.Fatalf("secret leaked: %s", body)
		}
		var out struct{ Extensions []Extension }
		decode(t, rec, &out)
		cfx, plex := out.Extensions[0], out.Extensions[2]
		if !plex.Configured || plex.Settings["token"] != SecretMask || len(plex.SecretsSet) != 1 {
			t.Errorf("plex = %+v", plex)
		}
		if cfx.Settings["api_token"] != SecretMask || cfx.Configured {
			t.Errorf("cloudflare (token set, no account id) = %+v", cfx)
		}
	})

	puts := []struct {
		name     string
		ext      string
		existing string
		secret   string // already in home_secrets as ext.plex.token
		body     string
		wantCode int
		check    func(t *testing.T, saved map[string]any, enabled any, store *secrets.Memory)
	}{
		{"enable only keeps settings", "plex", `{"url":"http://p"}`, "s", `{"enabled":true}`, 200,
			func(t *testing.T, s map[string]any, en any, m *secrets.Memory) {
				if v, _ := m.Get(ctx, plexToken); en != true || string(v) != "s" || s["url"] != "http://p" {
					t.Errorf("saved %v enabled %v token %q", s, en, v)
				}
			}},
		{"set secret goes to home_secrets", "plex", `{"url":"http://p"}`, "", `{"settings":{"token":"new"}}`, 200,
			func(t *testing.T, s map[string]any, _ any, m *secrets.Memory) {
				if v, _ := m.Get(ctx, plexToken); string(v) != "new" {
					t.Errorf("token %q", v)
				}
				if _, has := s["token"]; has {
					t.Errorf("token in the settings row: %v", s)
				}
			}},
		{"mask keeps the stored secret", "plex", `{"url":"http://p"}`, "keep", `{"settings":{"token":"********","url":"http://q"}}`, 200,
			func(t *testing.T, s map[string]any, _ any, m *secrets.Memory) {
				if v, _ := m.Get(ctx, plexToken); string(v) != "keep" || s["url"] != "http://q" {
					t.Errorf("saved %v token %q", s, v)
				}
			}},
		{"null clears", "plex", `{"url":"http://p"}`, "x", `{"settings":{"token":null}}`, 200,
			func(t *testing.T, _ map[string]any, _ any, m *secrets.Memory) {
				if ok, _ := m.Has(ctx, plexToken); ok {
					t.Error("token not cleared")
				}
			}},
		{"a legacy token in the row moves on save", "plex", `{"url":"http://p","token":"legacy"}`, "", `{"enabled":true}`, 200,
			func(t *testing.T, s map[string]any, _ any, m *secrets.Memory) {
				if v, _ := m.Get(ctx, plexToken); string(v) != "legacy" {
					t.Errorf("token %q", v)
				}
				if _, has := s["token"]; has {
					t.Errorf("token left in the row: %v", s)
				}
			}},
		{"cloudflare token under its own name", "cloudflare_stream", `{}`, "", `{"settings":{"account_id":"acc","api_token":"cf"}}`, 200,
			func(t *testing.T, s map[string]any, _ any, m *secrets.Memory) {
				if v, _ := m.Get(ctx, "cf_api_token"); string(v) != "cf" || s["account_id"] != "acc" || len(s) != 1 {
					t.Errorf("saved %v token %q", s, v)
				}
			}},
		{"unknown setting", "plex", `{}`, "", `{"settings":{"password":"x"}}`, 400, nil},
		{"bad url", "plex", `{}`, "", `{"settings":{"url":"ftp://x"}}`, 400, nil},
		{"non-string setting", "plex", `{}`, "", `{"settings":{"url":5}}`, 400, nil},
		{"nothing to change", "plex", `{}`, "", `{}`, 400, nil},
		{"unknown extension", "kodi", `{}`, "", `{"enabled":true}`, 404, nil},
	}
	for _, tc := range puts {
		t.Run("put "+tc.name, func(t *testing.T) {
			f := newFixture(row(tc.ext, false, tc.existing))
			store := secrets.NewMemory()
			f.h.Secrets = store
			if tc.secret != "" {
				_ = store.Put(ctx, plexToken, []byte(tc.secret), "")
			}
			rec := f.do("PUT", "/api/extensions/"+tc.ext, tc.body, admin())
			if rec.Code != tc.wantCode {
				t.Fatalf("status %d body %s", rec.Code, rec.Body.String())
			}
			if tc.wantCode != 200 {
				if len(f.db.Find("insert into extensions")) != 0 {
					t.Error("rejected request must not write")
				}
				return
			}
			for _, leak := range []string{`"new"`, `"keep"`, `"legacy"`, `"cf"`} {
				if strings.Contains(rec.Body.String(), leak) {
					t.Errorf("response leaked a secret: %s", rec.Body.String())
				}
			}
			up := f.db.Find("insert into extensions")[0]
			var saved map[string]any
			_ = json.Unmarshal([]byte(up.Args[2].(string)), &saved)
			tc.check(t, saved, up.Args[1], store)
		})
	}

	t.Run("a secret without a store is refused", func(t *testing.T) {
		f := newFixture(row("plex", false, `{}`))
		rec := f.do("PUT", "/api/extensions/plex", `{"settings":{"token":"x"}}`, admin())
		if rec.Code != http.StatusServiceUnavailable || len(f.db.Find("insert into extensions")) != 0 {
			t.Fatalf("status %d", rec.Code)
		}
	})

	t.Run("validator refuses a change", func(t *testing.T) {
		f := newFixture(row("cloudflare_stream", false, `{"account_id":"a1"}`))
		f.h.Secrets = secrets.NewMemory()
		f.h.Validators = map[string]ExtensionValidator{"cloudflare_stream": func(_ context.Context, cur, next map[string]any, _ []string) error {
			if cur["account_id"] != next["account_id"] {
				return httperror.Conflict("", "remove the integration first")
			}
			return nil
		}}
		rec := f.do("PUT", "/api/extensions/cloudflare_stream", `{"settings":{"account_id":"a2","api_token":"t"}}`, admin())
		if rec.Code != http.StatusConflict || len(f.db.Find("insert into extensions")) != 0 {
			t.Fatalf("status %d %s", rec.Code, rec.Body.String())
		}
		if ok, _ := f.h.Secrets.Has(ctx, "cf_api_token"); ok {
			t.Error("a refused change must not store the token")
		}
	})

	t.Run("test endpoint is not implemented", func(t *testing.T) {
		f := newFixture()
		rec := f.do("POST", "/api/extensions/plex/test", "", admin())
		var out struct {
			OK      bool   `json:"ok"`
			Message string `json:"message"`
		}
		decode(t, rec, &out)
		if rec.Code != http.StatusOK || out.OK || !strings.HasPrefix(out.Message, "not_implemented") {
			t.Fatalf("status %d out %+v", rec.Code, out)
		}
		if rec := f.do("POST", "/api/extensions/kodi/test", "", admin()); rec.Code != 404 {
			t.Fatalf("status %d", rec.Code)
		}
	})

	t.Run("an extension's tester answers its test", func(t *testing.T) {
		f := newFixture()
		f.h.Testers = map[string]ExtensionTester{"cloudflare_stream": func(context.Context) (any, error) {
			return map[string]any{"ok": true, "message": "ready", "lines": []any{}}, nil
		}}
		rec := f.do("POST", "/api/extensions/cloudflare_stream/test", "", admin())
		if rec.Code != 200 || !strings.Contains(rec.Body.String(), `"ready"`) {
			t.Fatalf("status %d %s", rec.Code, rec.Body.String())
		}
		if rec := f.do("POST", "/api/extensions/cloudflare_stream/test", "", viewer()); rec.Code != 403 {
			t.Fatalf("viewer: %d", rec.Code)
		}
	})
}
