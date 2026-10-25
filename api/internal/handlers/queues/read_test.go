package queues

import (
	"context"
	"database/sql/driver"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/Hamza-Labs-Core/Maktaba/api/internal/auth/principal"
	"github.com/Hamza-Labs-Core/Maktaba/api/internal/handlers/queues/queuestest"
	"github.com/Hamza-Labs-Core/Maktaba/api/internal/handlers/ws"
)

func TestDetail(t *testing.T) {
	rules := append(snapshotRules(),
		singleStageRule(stageRow("probe", "episodes", 1, "nas_read", withEdges(`[{"to":"detect","map":"library_of"}]`))),
		&queuestest.Rule{Match: "percentile_cont(0.5)", Cols: []string{"done", "failed", "med", "p95", "skipped", "changed"},
			Rows: [][]driver.Value{{int64(80), int64(5), 1200.0, 4500.0, int64(20), int64(40)}}},
		&queuestest.Rule{Match: "order by j.priority, j.id limit 20", Cols: jobRefCols(),
			Rows: [][]driver.Value{jobRefRow(21, "probe", "pending"), jobRefRow(22, "probe", "pending")}},
		&queuestest.Rule{Match: "from job_events e", Cols: []string{"id", "at", "job_id", "stage", "kind_s", "sid", "label", "level", "kind", "message", "data"},
			Rows: [][]driver.Value{{int64(5), t0, int64(11), "probe", "video", uidA, "Episode 1", "info", "done", "finished", `{"x":1}`}}},
	)
	f := newFixture(rules...)

	t.Run("ok", func(t *testing.T) {
		rec := f.do("GET", "/api/queues/probe", "", admin())
		if rec.Code != 200 {
			t.Fatalf("status %d: %s", rec.Code, rec.Body.String())
		}
		var d QueueDetail
		decode(t, rec, &d)
		if d.Info.Stage != "probe" || d.Summary.Stage != "probe" || d.Summary.Counts.Pending != 10 {
			t.Errorf("info/summary = %+v / %+v", d.Info.Stage, d.Summary.Counts)
		}
		if d.KPIs.Done24h != 80 || d.KPIs.Failed24h != 5 || *d.KPIs.MedianDurationMs != 1200 ||
			d.KPIs.SkippedShare24h != 0.25 || d.KPIs.ChangedShare24h != 0.5 {
			t.Errorf("kpis = %+v", d.KPIs)
		}
		if len(d.NextUp) != 2 || len(d.Running) != 1 || len(d.Log) != 1 || len(d.Failures) != 1 {
			t.Errorf("lists: next=%d running=%d log=%d failures=%d", len(d.NextUp), len(d.Running), len(d.Log), len(d.Failures))
		}
		if d.Failures[0].Label != "NAS read timed out" || d.Failures[0].SampleSubjects[0].ID != uidA {
			t.Errorf("failure group = %+v", d.Failures[0])
		}
		if string(d.Log[0].Data) != `{"x":1}` {
			t.Errorf("log data = %s", d.Log[0].Data)
		}
	})
	t.Run("unknown stage", func(t *testing.T) {
		g := newFixture()
		if rec := g.do("GET", "/api/queues/nope", "", admin()); rec.Code != 404 {
			t.Fatalf("status %d", rec.Code)
		}
	})
	t.Run("needs auth", func(t *testing.T) {
		if rec := f.do("GET", "/api/queues/probe", "", nil); rec.Code != 403 {
			t.Fatalf("status %d", rec.Code)
		}
	})
}

func TestRate(t *testing.T) {
	// t0 = 12:00; 1h window -> buckets 11:00 .. 12:00 inclusive = 7 points.
	b := t0.Add(-30*time.Minute).Unix() / 600 * 600
	tests := []struct {
		name     string
		url      string
		wantCode int
		wantLen  int
	}{
		{"1h", "/api/queues/probe/rate?hours=1", 200, 7},
		{"default 24h", "/api/queues/probe/rate", 200, 24*6 + 1},
		{"max 168h", "/api/queues/probe/rate?hours=168", 200, 168*6 + 1},
		{"zero hours", "/api/queues/probe/rate?hours=0", 400, 0},
		{"too many hours", "/api/queues/probe/rate?hours=169", 400, 0},
		{"not a number", "/api/queues/probe/rate?hours=x", 400, 0},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			f := newFixture(singleStageRule(stageRow("probe", "episodes", 1, "nas_read")), stagesRule(stageRow("probe", "episodes", 1, "nas_read")),
				&queuestest.Rule{Match: "from processing_jobs where stage = $1 and finished_at", Cols: []string{"b", "d", "f", "s"},
					Rows: [][]driver.Value{{b, int64(12), int64(2), int64(30)}}},
				&queuestest.Rule{Match: "from health_samples", Cols: []string{"b", "n"}, Rows: [][]driver.Value{{b, 4.0}}})
			rec := f.do("GET", tc.url, "", admin())
			if rec.Code != tc.wantCode {
				t.Fatalf("status %d: %s", rec.Code, rec.Body.String())
			}
			if tc.wantCode != 200 {
				return
			}
			var out struct {
				BucketSec int         `json:"bucket_sec"`
				Points    []RatePoint `json:"points"`
			}
			decode(t, rec, &out)
			if out.BucketSec != 600 || len(out.Points) != tc.wantLen {
				t.Fatalf("bucket %d, %d points want %d", out.BucketSec, len(out.Points), tc.wantLen)
			}
			var hit *RatePoint
			for i := range out.Points {
				if out.Points[i].T.Unix() == b {
					hit = &out.Points[i]
				}
			}
			if tc.name == "1h" && (hit == nil || hit.Done != 12 || hit.Failed != 2 || hit.Skipped != 30 || hit.NasTimeouts != 4) {
				t.Errorf("bucket = %+v", hit)
			}
			for i := 1; i < len(out.Points); i++ {
				if out.Points[i].T.Sub(out.Points[i-1].T) != 10*time.Minute {
					t.Fatalf("axis not continuous at %d", i)
				}
			}
		})
	}
}

func TestJobs(t *testing.T) {
	rows := func(n int) [][]driver.Value {
		out := [][]driver.Value{}
		for i := 0; i < n; i++ {
			out = append(out, jobRefRow(int64(100-i), "probe", "pending"))
		}
		return out
	}
	tests := []struct {
		name     string
		url      string
		rows     int
		wantCode int
		wantLen  int
		wantNext string
		wantSQL  []string
		wantArg  []any
	}{
		{"default page, no more", "/api/queues/probe/jobs", 3, 200, 3, "", []string{"order by j.id desc limit $2"}, []any{"probe", int64(51)}},
		{"limit with more -> next cursor", "/api/queues/probe/jobs?limit=2", 3, 200, 2, "99", nil, []any{"probe", int64(3)}},
		{"states filter + cursor", "/api/queues/probe/jobs?state=pending,running&cursor=500&limit=10", 1, 200, 1, "",
			[]string{"j.state = any($2::text[])", "j.id < $3"}, nil},
		{"limit clamps to 200", "/api/queues/probe/jobs?limit=9999", 0, 200, 0, "", nil, []any{"probe", int64(201)}},
		{"unknown state", "/api/queues/probe/jobs?state=bogus", 0, 400, 0, "", nil, nil},
		{"bad cursor", "/api/queues/probe/jobs?cursor=abc", 0, 400, 0, "", nil, nil},
		{"bad limit", "/api/queues/probe/jobs?limit=abc", 0, 400, 0, "", nil, nil},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			f := newFixture(singleStageRule(stageRow("probe", "episodes", 1, "nas_read")), stagesRule(stageRow("probe", "episodes", 1, "nas_read")),
				&queuestest.Rule{Match: "where j.stage = $1", Cols: jobRefCols(), Rows: rows(tc.rows)})
			rec := f.do("GET", tc.url, "", admin())
			if rec.Code != tc.wantCode {
				t.Fatalf("status %d: %s", rec.Code, rec.Body.String())
			}
			if tc.wantCode != 200 {
				return
			}
			var out struct {
				Items []JobRef `json:"items"`
				Next  *string  `json:"next"`
			}
			decode(t, rec, &out)
			if len(out.Items) != tc.wantLen {
				t.Errorf("items %d want %d", len(out.Items), tc.wantLen)
			}
			if (tc.wantNext == "") != (out.Next == nil) || (out.Next != nil && *out.Next != tc.wantNext) {
				t.Errorf("next = %v want %q", out.Next, tc.wantNext)
			}
			calls := f.db.Find("where j.stage = $1")
			for _, frag := range tc.wantSQL {
				if !strings.Contains(calls[0].SQL, frag) {
					t.Errorf("sql missing %q: %s", frag, calls[0].SQL)
				}
			}
			for i, a := range tc.wantArg {
				if calls[0].Args[i] != a {
					t.Errorf("arg[%d] = %v want %v", i, calls[0].Args[i], a)
				}
			}
		})
	}
}

func TestFailures(t *testing.T) {
	legacyRule := &queuestest.Rule{Match: "limit 5000", Cols: []string{"id", "stage", "kind", "sid", "error", "at"},
		Rows: [][]driver.Value{
			{int64(9), "probe", "video", uidA, `{"kind":"OSError","message":"disk gone"}`, t0.Add(-time.Hour)},
			{int64(8), "probe", "video", uidB, `{"kind":"OSError","message":"disk gone"}`, t0.Add(-2 * time.Hour)},
			{int64(7), "probe", "video", uidB, "plain text failure", t0.Add(-3 * time.Hour)},
		}}
	codedRule := &queuestest.Rule{Match: "group by stage, error_cause",
		Cols: []string{"stage", "cause", "n", "first", "last", "msg", "samples"},
		Rows: [][]driver.Value{
			{"probe", "nas_timeout", int64(4), t0.Add(-time.Hour), t0, `{"message":"ETIMEDOUT"}`, `{video:` + uidA + `,video:` + uidB + `}`},
			{"probe", "OSError", int64(1), t0.Add(-time.Hour), t0.Add(-time.Minute), "x", `{}`},
		}}
	reviewRule := &queuestest.Rule{Match: "s.external_write", Cols: []string{"stage", "n", "first", "last", "samples"},
		Rows: [][]driver.Value{{"plex_collection", int64(2), t0.Add(-time.Hour), t0, `{season:` + uidA + `}`}}}
	labels := &queuestest.Rule{Match: "from unnest", Cols: []string{"k", "id", "label"},
		Rows: [][]driver.Value{{"video", uidA, "Episode 1"}, {"season", uidA, "Show · S1"}}}

	f := newFixture(stagesRule(stageRow("probe", "episodes", 1, "nas_read")), singleStageRule(stageRow("probe", "episodes", 1, "nas_read")),
		codedRule, legacyRule, reviewRule, labels)

	rec := f.do("GET", "/api/queues/probe/failures", "", admin())
	if rec.Code != 200 {
		t.Fatalf("status %d: %s", rec.Code, rec.Body.String())
	}
	var out struct {
		Groups []FailureGroup `json:"groups"`
	}
	decode(t, rec, &out)
	by := map[string]FailureGroup{}
	for _, g := range out.Groups {
		by[g.Stage+"/"+g.Cause] = g
	}
	// legacy OSError rows (2) merge with the coded OSError group (1).
	if g := by["probe/OSError"]; g.Count != 3 {
		t.Errorf("OSError merged count = %d, want 3", g.Count)
	}
	if g := by["probe/unknown"]; g.Count != 1 || g.SampleMessage != "plain text failure" {
		t.Errorf("plain-text legacy group = %+v", g)
	}
	nas := by["probe/nas_timeout"]
	if nas.Count != 4 || nas.Action != ActionRetry || nas.SampleMessage != "ETIMEDOUT" ||
		len(nas.SampleSubjects) != 2 || nas.SampleSubjects[0].Label != "Episode 1" {
		t.Errorf("nas group = %+v", nas)
	}
	if nas.SampleSubjects[1].Label != uidB {
		t.Errorf("unlabelled subject falls back to its id: %+v", nas.SampleSubjects[1])
	}
	rv := by["plex_collection/awaiting_review"]
	if rv.Count != 2 || rv.Action != ActionApply || rv.SampleSubjects[0].Label != "Show · S1" {
		t.Errorf("review group = %+v", rv)
	}
	if out.Groups[0].Count < out.Groups[len(out.Groups)-1].Count {
		t.Errorf("groups must be sorted by count desc: %+v", out.Groups)
	}
	if args := f.db.Find("group by stage, error_cause")[0].Args; args[0] != "probe" {
		t.Errorf("stage filter arg = %v", args)
	}
}

func TestIssues_AllStages(t *testing.T) {
	f := newFixture(stagesRule(stageRow("probe", "episodes", 1, "nas_read")))
	rec := f.do("GET", "/api/issues", "", admin())
	var out struct {
		Groups []FailureGroup `json:"groups"`
	}
	decode(t, rec, &out)
	if rec.Code != 200 || out.Groups == nil {
		t.Fatalf("%d %s", rec.Code, rec.Body.String())
	}
	if f.db.Find("group by stage, error_cause")[0].Args[0] != "" {
		t.Errorf("all-stage query must pass an empty stage filter")
	}
}

func TestActivity(t *testing.T) {
	logCols := []string{"id", "at", "job_id", "stage", "kind_s", "sid", "label", "level", "kind", "message", "data"}
	line := func(id int64) []driver.Value {
		return []driver.Value{id, t0, int64(3), "probe", "video", uidA, "Episode 1", "warn", "retry_scheduled", "will retry", nil}
	}
	tests := []struct {
		name     string
		url      string
		rows     [][]driver.Value
		wantCode int
		wantLen  int
		wantNext string
		wantSQL  []string
	}{
		{"defaults to info and above", "/api/activity", [][]driver.Value{line(9)}, 200, 1, "",
			[]string{"e.level = any($1::text[])", "order by e.id desc limit $2"}},
		{"filters", "/api/activity?stage=probe&library_id=" + uidA + "&level=error&cursor=50&limit=1",
			[][]driver.Value{line(49), line(48)}, 200, 1, "49",
			[]string{"e.stage = $1", "e.library_id = $2", "e.id < $4"}},
		{"debug has no level filter", "/api/activity?level=debug", nil, 200, 0, "", nil},
		{"bad level", "/api/activity?level=trace", nil, 400, 0, "", nil},
		{"bad library", "/api/activity?library_id=x", nil, 400, 0, "", nil},
		{"bad cursor", "/api/activity?cursor=-3", nil, 400, 0, "", nil},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			f := newFixture(&queuestest.Rule{Match: "from job_events e", Cols: logCols, Rows: tc.rows})
			rec := f.do("GET", tc.url, "", admin())
			if rec.Code != tc.wantCode {
				t.Fatalf("status %d: %s", rec.Code, rec.Body.String())
			}
			if tc.wantCode != 200 {
				return
			}
			var out struct {
				Items []LogLine `json:"items"`
				Next  *string   `json:"next"`
			}
			decode(t, rec, &out)
			if len(out.Items) != tc.wantLen || (tc.wantNext == "") != (out.Next == nil) || (out.Next != nil && *out.Next != tc.wantNext) {
				t.Errorf("items=%d next=%v", len(out.Items), out.Next)
			}
			sqlText := f.db.Calls()[0].SQL
			for _, frag := range tc.wantSQL {
				if !strings.Contains(sqlText, frag) {
					t.Errorf("sql missing %q: %s", frag, sqlText)
				}
			}
			if tc.name == "debug has no level filter" && strings.Contains(sqlText, "e.level = any") {
				t.Errorf("debug must not filter levels")
			}
			if tc.wantLen > 0 && string(out.Items[0].Data) != "null" {
				t.Errorf("null data must serialise as null, got %s", out.Items[0].Data)
			}
		})
	}
}

func TestJobEvents(t *testing.T) {
	f := newFixture(&queuestest.Rule{Match: "from processing_jobs where id = $1", Cols: []string{"library_id"},
		Rows: [][]driver.Value{{libL}}},
		&queuestest.Rule{Match: "from job_events e",
			Cols: []string{"id", "at", "job_id", "stage", "k", "sid", "label", "level", "kind", "message", "data"},
			Rows: [][]driver.Value{{int64(1), t0, int64(7), "probe", "video", uidA, "E1", "debug", "skipped", "unchanged", `{"a":1}`}}})
	// non-admins, even one granted the job's library, are refused by the
	// package's admin-only Mount (the API enforces roles): 403, no read
	for _, p := range []*principal.Principal{viewer(), libViewer(), allViewer()} {
		if rec := f.do("GET", "/api/jobs/7/events", "", p); rec.Code != http.StatusForbidden {
			t.Fatalf("non-admin %s: %d", p.UserID, rec.Code)
		}
	}
	if n := len(f.db.Calls()); n != 0 {
		t.Fatalf("queried %d times for refused callers", n)
	}
	rec := f.do("GET", "/api/jobs/7/events", "", admin())
	var out struct{ Items []LogLine }
	decode(t, rec, &out)
	if rec.Code != 200 || len(out.Items) != 1 || out.Items[0].Level != "debug" {
		t.Fatalf("%d %s", rec.Code, rec.Body.String())
	}
	var c queuestest.Call
	for _, call := range f.db.Calls() {
		if strings.Contains(call.SQL, "from job_events e") {
			c = call
		}
	}
	if !strings.Contains(c.SQL, "e.job_id = $1") || !strings.Contains(c.SQL, "order by e.id limit") || c.Args[0] != int64(7) {
		t.Errorf("sql/args = %s %v", c.SQL, c.Args)
	}
	for _, bad := range []string{"/api/jobs/x/events", "/api/jobs/0/events"} {
		if rec := f.do("GET", bad, "", admin()); rec.Code != 400 {
			t.Errorf("%s -> %d", bad, rec.Code)
		}
	}
}

func TestItemHistory(t *testing.T) {
	timelineCols := append(jobRefCols(), "reasons", "dur_ms", "summary")
	tl := func(id int64, stage string) []driver.Value {
		return append(jobRefRow(id, stage, "done"),
			`[{"at":"2026-10-15T11:00:00Z","why":"probe output changed","by":"job:3"}]`, 1500.0, `{"diff":{"added":["a"]}}`)
	}
	rules := []*queuestest.Rule{
		stagesRule(
			stageRow("probe", "episodes", 1, "nas_read"),
			stageRow("detect", "seasons", 1, "cpu", withKinds("{library}")),
			stageRow("tag", "seasons", 2, "cpu", withKinds("{season}")),
		),
		{Match: "from videos v join libraries l", Cols: []string{"lib", "libname", "dur", "inseries", "isextra"},
			Rows: [][]driver.Value{{uidB, "Kids", 600.0, true, false}}},
		{Match: "from unnest", Cols: []string{"k", "id", "label"}, Rows: [][]driver.Value{
			{"video", uidA, "Episode 1"}, {"library", uidB, "Kids"}}},
		{Match: "select distinct on (stage) id", Cols: jobRefCols(), Rows: [][]driver.Value{jobRefRow(5, "probe", "done")}},
		{Match: "from external_links el", Cols: []string{"target", "title", "ext", "via"}, Rows: [][]driver.Value{
			{"plex_collection", "Show S1", "rk1", "season"}, {"maktaba_channel", "Ch", "c1", "video"}}},
		{Match: "from channel_programs", Cols: []string{"id", "name", "n"}, Rows: [][]driver.Value{{uidB, "Cartoons", int64(3)}}},
		{Match: "where j.subject_kind = $1 and j.subject_id = $2::uuid", Cols: timelineCols,
			Rows: [][]driver.Value{tl(12, "probe"), tl(11, "probe"), tl(10, "probe")}},
		{Match: "kind = 'triggered'", Cols: []string{"job_id", "data"}, Rows: [][]driver.Value{
			{int64(12), `{"to":[{"stage":"detect","subject_kind":"library","subject_id":"` + uidB + `","outcome":"coalesced"}]}`}}},
	}

	t.Run("video", func(t *testing.T) {
		f := newFixture(rules...)
		rec := f.do("GET", "/api/items/video/"+uidA+"/history?limit=2", "", admin())
		if rec.Code != 200 {
			t.Fatalf("status %d: %s", rec.Code, rec.Body.String())
		}
		var h ItemHistory
		decode(t, rec, &h)
		if h.Subject.Label != "Episode 1" || *h.Subject.LibraryLabel != "Kids" || h.Kinds[0] != "episode" {
			t.Errorf("subject = %+v kinds=%v", h.Subject, h.Kinds)
		}
		if len(h.Queues) != 1 || h.Queues[0].Stage != "probe" || h.Queues[0].LastJob == nil || !h.Queues[0].CanRequeue {
			t.Errorf("queues = %+v (only video stages belong here)", h.Queues)
		}
		if len(h.Timeline) != 2 || h.Next == nil || *h.Next != "11" {
			t.Errorf("timeline=%d next=%v", len(h.Timeline), h.Next)
		}
		e := h.Timeline[0]
		if e.DurationMs == nil || *e.DurationMs != 1500 || len(e.Reasons) != 1 || e.Reasons[0].Why != "probe output changed" {
			t.Errorf("entry = %+v", e)
		}
		if len(e.Triggered) != 1 || e.Triggered[0].Stage != "detect" || e.Triggered[0].SubjectLabel != "Kids" || e.Triggered[0].Outcome != "coalesced" {
			t.Errorf("triggered = %+v", e.Triggered)
		}
		if string(e.Summary) != `{"diff":{"added":["a"]}}` {
			t.Errorf("output_summary (applied diff) must be exposed: %s", e.Summary)
		}
		if len(h.AppearsIn) != 3 || h.AppearsIn[0].Detail != "via season" || h.AppearsIn[2].Target != "channel_slot" {
			t.Errorf("appears_in = %+v", h.AppearsIn)
		}
	})
	t.Run("short unplaced video", func(t *testing.T) {
		f := newFixture(&queuestest.Rule{Match: "from videos v join libraries l", Cols: []string{"lib", "libname", "dur", "inseries", "isextra"},
			Rows: [][]driver.Value{{uidB, "Kids", 60.0, false, false}}}, stagesRule())
		rec := f.do("GET", "/api/items/video/"+uidA+"/history", "", admin())
		var h ItemHistory
		decode(t, rec, &h)
		if h.Kinds[0] != "short" {
			t.Errorf("kinds = %v", h.Kinds)
		}
	})
	t.Run("extra video", func(t *testing.T) {
		// detect stores extras as series_seasons.metadata.extras: a JSON array of video ids
		// (text). A video listed there is an extra even if it also has a short duration.
		f := newFixture(&queuestest.Rule{Match: "from videos v join libraries l", Cols: []string{"lib", "libname", "dur", "inseries", "isextra"},
			Rows: [][]driver.Value{{uidB, "Kids", 60.0, false, true}}}, stagesRule())
		rec := f.do("GET", "/api/items/video/"+uidA+"/history", "", admin())
		var h ItemHistory
		decode(t, rec, &h)
		if len(h.Kinds) != 1 || h.Kinds[0] != "extra" {
			t.Errorf("kinds = %v, want [extra]", h.Kinds)
		}
	})
	t.Run("extra lookup reads the detect metadata format", func(t *testing.T) {
		for _, want := range []string{"ss.metadata->'extras' @> to_jsonb(v.id::text)", "ss.retired_at IS NULL"} {
			if !strings.Contains(videoSubjectSQL, want) {
				t.Errorf("videoSubjectSQL lacks %q", want)
			}
		}
	})
	t.Run("library lists seasons and kinds", func(t *testing.T) {
		f := newFixture(
			&queuestest.Rule{Match: "select id::text, name from libraries", Cols: []string{"id", "name"}, Rows: [][]driver.Value{{uidB, "Kids"}}},
			&queuestest.Rule{Match: "where library_id = $1::uuid and retired_at is null", Cols: []string{"id", "name", "season", "kind", "n"},
				Rows: [][]driver.Value{{uidA, "Show", int64(1), "episodic", int64(10)}, {uidB, "Show", int64(2), "ongoing", int64(3)}}},
			stagesRule(stageRow("detect", "seasons", 1, "cpu", withKinds("{library}"))))
		rec := f.do("GET", "/api/items/library/"+uidB+"/history", "", admin())
		var h ItemHistory
		decode(t, rec, &h)
		if rec.Code != 200 || len(h.Seasons) != 2 || h.Seasons[1].Label != "Show · S2" ||
			strings.Join(h.Kinds, ",") != "episodic,ongoing" || h.Queues[0].Stage != "detect" {
			t.Fatalf("%d %+v", rec.Code, h)
		}
	})
	t.Run("errors", func(t *testing.T) {
		f := newFixture()
		for url, want := range map[string]int{
			"/api/items/video/" + uidA + "/history":          404, // no row
			"/api/items/movie/" + uidA + "/history":          404,
			"/api/items/video/not-a-uuid/history":            400,
			"/api/items/catalog/" + uidA + "/history":        404, // not the catalog id
			"/api/items/video/" + uidA + "/history?cursor=z": 400,
		} {
			if rec := f.do("GET", url, "", admin()); rec.Code != want {
				t.Errorf("%s -> %d want %d", url, rec.Code, want)
			}
		}
	})
	t.Run("catalog", func(t *testing.T) {
		f := newFixture(stagesRule())
		if rec := f.do("GET", "/api/items/catalog/"+CatalogID+"/history", "", admin()); rec.Code != 200 {
			t.Fatalf("status %d: %s", rec.Code, rec.Body.String())
		}
	})
}

func TestLibraries(t *testing.T) {
	f := newFixture(
		stagesRule(
			stageRow("probe", "episodes", 1, "nas_read"),
			stageRow("detect", "seasons", 1, "cpu", withKinds("{library}")),
			stageRow("tag", "seasons", 2, "cpu", withKinds("{season}")),
			stageRow("fp_gate", "seasons", 1, "cpu", withKinds("{season}"), func(r []driver.Value) { r[8] = true }),
			stageRow("write_channels", "channels", 2, "network_tv", withKinds("{catalog}")),
		),
		&queuestest.Rule{Match: "select id::text, name from libraries", Cols: []string{"id", "name"}, Rows: [][]driver.Value{{uidA, "Kids"}, {uidB, "Empty"}}},
		&queuestest.Rule{Match: "from videos group by library_id", Cols: []string{"id", "n"}, Rows: [][]driver.Value{{uidA, int64(40)}}},
		&queuestest.Rule{Match: "from series_seasons", Cols: []string{"id", "n"}, Rows: [][]driver.Value{{uidA, int64(4)}}},
		&queuestest.Rule{Match: "count(distinct subject_id)", Cols: []string{"lib", "stage", "done", "running", "pending", "last"},
			Rows: [][]driver.Value{{uidA, "probe", int64(30), int64(2), int64(8), t0}, {uidA, "detect", int64(1), int64(0), int64(0), t0.Add(-time.Hour)}}},
		&queuestest.Rule{Match: "select library_id::text, stage, count(*) from latest", Cols: []string{"lib", "stage", "n"},
			Rows: [][]driver.Value{{uidA, "probe", int64(3)}}},
	)
	rec := f.do("GET", "/api/queues/libraries", "", admin())
	var out struct{ Items []LibraryQueueRow }
	decode(t, rec, &out)
	if rec.Code != 200 || len(out.Items) != 2 {
		t.Fatalf("%d %s", rec.Code, rec.Body.String())
	}
	kids := out.Items[0]
	if kids.Videos != 40 || kids.Seasons != 4 || kids.LastActivityAt == nil || !kids.LastActivityAt.Equal(t0) {
		t.Errorf("kids = %+v", kids)
	}
	// strip: probe (videos), detect (library), tag (seasons); no hidden fp_gate, no catalog stage.
	if len(kids.Strip) != 3 {
		t.Fatalf("strip = %+v", kids.Strip)
	}
	if c := kids.Strip[0]; c.Stage != "probe" || c.Total != 40 || c.Done != 30 || c.Running != 2 || c.Pending != 8 || c.Failed != 3 {
		t.Errorf("probe cell = %+v", c)
	}
	if kids.Strip[1].Total != 1 || kids.Strip[2].Stage != "tag" || kids.Strip[2].Total != 4 || kids.Strip[2].Done != 0 {
		t.Errorf("cells = %+v", kids.Strip)
	}
	if empty := out.Items[1]; empty.Videos != 0 || empty.LastActivityAt != nil || len(empty.Strip) != 3 {
		t.Errorf("empty = %+v", empty)
	}

	// cached for 5 s
	n := len(f.db.Calls())
	f.do("GET", "/api/queues/libraries", "", admin())
	if len(f.db.Calls()) != n {
		t.Errorf("second call within 5s must be served from cache")
	}
	now := t0.Add(6 * time.Second)
	f.h.NowFunc = func() time.Time { return now }
	f.do("GET", "/api/queues/libraries", "", admin())
	if len(f.db.Calls()) == n {
		t.Errorf("cache must expire after 5s")
	}
}

func TestHealthSeries(t *testing.T) {
	tests := []struct {
		url        string
		wantCode   int
		wantBucket int64
	}{
		{"/api/queues/health", 200, 60},
		{"/api/queues/health?hours=168", 200, 168 * 3600 / 360},
		{"/api/queues/health?hours=0", 400, 0},
		{"/api/queues/health?hours=169", 400, 0},
	}
	for _, tc := range tests {
		t.Run(tc.url, func(t *testing.T) {
			f := newFixture(&queuestest.Rule{Match: "from health_samples", Cols: []string{"metric", "t", "v"}, Rows: [][]driver.Value{
				{"nas.open_ms_p95", t0.Add(-time.Minute), 80.0}, {"nas.open_ms_p95", t0, 90.0}, {"cpu.pct", t0, 10.0}}})
			rec := f.do("GET", tc.url, "", admin())
			if rec.Code != tc.wantCode {
				t.Fatalf("status %d", rec.Code)
			}
			if tc.wantCode != 200 {
				return
			}
			var out struct{ Series []HealthSeries }
			decode(t, rec, &out)
			if len(out.Series) != 2 || out.Series[0].Metric != "cpu.pct" || len(out.Series[1].Points) != 2 {
				t.Errorf("series = %+v", out.Series)
			}
			a := f.db.Calls()[0].Args
			if a[2] != tc.wantBucket {
				t.Errorf("bucket = %v want %d", a[2], tc.wantBucket)
			}
		})
	}
}

func TestWorkers(t *testing.T) {
	f := newFixture(&queuestest.Rule{Match: "from workers w left join cluster_nodes",
		Cols: []string{"id", "host", "stages", "pools", "started_at", "last_seen_at", "version", "node_id", "node_name", "role"},
		Rows: [][]driver.Value{
			{"w1", "mac", "{transcribe}", `{"gpu":1}`, t0.Add(-time.Hour), t0.Add(-3 * time.Second), "1.2", nil, nil, "home"},
			{"w2", "nas", "{probe}", nil, t0.Add(-time.Hour), t0.Add(-2 * time.Minute), nil, "6f1c2a3b-4d5e-4f60-8a71-92b3c4d5e6f7", "linux-box", "node"},
		}})
	rec := f.do("GET", "/api/workers", "", admin())
	var out struct{ Workers []Worker }
	decode(t, rec, &out)
	if rec.Code != 200 || len(out.Workers) != 2 {
		t.Fatalf("%d %s", rec.Code, rec.Body.String())
	}
	if !out.Workers[0].Online || out.Workers[1].Online {
		t.Errorf("online flags = %v %v", out.Workers[0].Online, out.Workers[1].Online)
	}
	if string(out.Workers[1].Pools) != "null" || out.Workers[1].Version != nil || *out.Workers[0].Version != "1.2" {
		t.Errorf("workers = %+v", out.Workers)
	}
	if out.Workers[0].Role != "home" || out.Workers[0].NodeID != nil || out.Workers[0].NodeName != nil {
		t.Errorf("home worker = %+v", out.Workers[0])
	}
	if w := out.Workers[1]; w.Role != "node" || w.NodeID == nil || *w.NodeID != "6f1c2a3b-4d5e-4f60-8a71-92b3c4d5e6f7" ||
		w.NodeName == nil || *w.NodeName != "linux-box" {
		t.Errorf("node worker = %+v", w)
	}
	if rec := f.do("GET", "/api/workers", "", nil); rec.Code != 403 {
		t.Errorf("anonymous -> %d", rec.Code)
	}
}

// ---------- streaming ----------

func TestSnapshotStream(t *testing.T) {
	f := newFixture(snapshotRules()...)

	if f.h.publishSnapshotOnce(context.Background()) {
		t.Fatal("must not build or publish a snapshot with zero subscribers")
	}
	if n := len(f.db.Calls()); n != 0 {
		t.Fatalf("no DB work expected without subscribers, ran %d statements", n)
	}

	sub := f.h.Hub.Subscribe(SnapshotChannel)
	if !f.h.publishSnapshotOnce(context.Background()) {
		t.Fatal("expected a publish with one subscriber")
	}
	select {
	case e := <-sub.C:
		if e.Type != "queues.snapshot" {
			t.Fatalf("type = %s", e.Type)
		}
		if _, ok := e.Payload["_event_id"]; ok {
			t.Error("snapshots are ephemeral: no _event_id")
		}
		for _, k := range []string{"at", "paused_all", "stages", "queues", "pools", "flows", "health", "issues_count"} {
			if _, ok := e.Payload[k]; !ok {
				t.Errorf("payload missing %q", k)
			}
		}
	default:
		t.Fatal("no event delivered")
	}

	f.h.Hub.Unsubscribe(SnapshotChannel, sub)
	if f.h.publishSnapshotOnce(context.Background()) {
		t.Error("must stop publishing once the last subscriber leaves")
	}
}

func TestPublishSnapshots_TickerStopsOnCancel(t *testing.T) {
	f := newFixture(snapshotRules()...)
	sub := f.h.Hub.Subscribe(SnapshotChannel)
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { f.h.PublishSnapshots(ctx, 10*time.Millisecond); close(done) }()
	select {
	case <-sub.C:
	case <-time.After(2 * time.Second):
		t.Fatal("no snapshot within 2s")
	}
	cancel()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("publisher did not stop")
	}
}

func TestInitialFrame(t *testing.T) {
	f := newFixture(snapshotRules()...)
	if ev := f.h.InitialFrame(context.Background(), "jobs"); ev != nil {
		t.Errorf("only the queues channel has an initial frame")
	}
	ev := f.h.InitialFrame(context.Background(), SnapshotChannel)
	if ev == nil || ev.Type != "queues.snapshot" {
		t.Fatalf("ev = %+v", ev)
	}
	_ = ws.Event(*ev)
	if rec := f.do(http.MethodGet, "/api/queues", "", admin()); rec.Code != 200 {
		t.Fatal(rec.Code)
	}
}
