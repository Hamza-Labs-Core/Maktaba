package queues

import (
	"database/sql/driver"
	"strings"
	"testing"

	"github.com/Hamza-Labs-Core/Maktaba/api/internal/handlers/queues/queuestest"
)

const uidV = "33333333-3333-3333-3333-333333333333"
const uidL = "44444444-4444-4444-4444-444444444444"

var itemCols = []string{"id", "stage", "kind", "sid", "label", "path", "lib", "lib_name", "state", "status",
	"error", "cause", "class", "parked", "attempts", "max", "first", "last", "missing", "yt",
	"chk_ok", "chk_err", "chk_host", "chk_at"}

func itemRow(id int64, status, cause, class string) []driver.Value {
	return []driver.Value{id, "content_hash", "video", uidV, "Lesson 1", "smb://nas/O/a.mp4", uidL, "Lessons",
		"failed", status, `{"kind":"file_missing","message":"a.mp4: not found"}`, cause, class, nil,
		int64(1), int64(3), t0, t0, t0, "yt123", false, "PermissionError: denied", "mac", t0}
}

func listRules(rows ...[]driver.Value) []*queuestest.Rule {
	return []*queuestest.Rule{
		stagesRule(stageRow("content_hash", "episodes", 0, "nas_read")),
		{Match: "from items i left join libraries lib", Cols: itemCols, Rows: rows},
		{Match: "select 'stage', stage", Cols: []string{"dim", "value", "label", "n"},
			Rows: [][]driver.Value{{"stage", "content_hash", "", int64(853)}, {"status", "needs_you", "", int64(853)},
				{"library", uidL, "Lessons", int64(853)}}},
	}
}

func TestFailuresList(t *testing.T) {
	f := newFixture(listRules(itemRow(9, "needs_you", "file_missing", "item_permanent"),
		itemRow(8, "needs_you", "file_missing", "item_permanent"))...)
	if rec := f.do("GET", "/api/queues/failures", "", allViewer()); rec.Code != 403 {
		t.Fatalf("a non-admin reads the failures: %d", rec.Code)
	}
	rec := f.do("GET", "/api/queues/failures?stage=content_hash&path_prefix=smb://nas/O_x%25&limit=1", "", admin())
	if rec.Code != 200 {
		t.Fatalf("%d %s", rec.Code, rec.Body.String())
	}
	var got struct {
		Items  []FailureItem           `json:"items"`
		Next   *string                 `json:"next"`
		Facets map[string][]FacetCount `json:"facets"`
	}
	decode(t, rec, &got)
	if len(got.Items) != 1 || got.Next == nil || *got.Next != "9" {
		t.Fatalf("paging: %d items, next %v", len(got.Items), got.Next)
	}
	it := got.Items[0]
	if it.Reason != "missing_confirmed" || it.Message != "a.mp4: not found" || *it.YouTubeID != "yt123" ||
		it.Check == nil || it.Check.OK || *it.LibraryName != "Lessons" {
		t.Fatalf("item = %+v", it)
	}
	if got.Facets["stage"][0].Count != 853 || got.Facets["library"][0].Label != "Lessons" {
		t.Fatalf("facets = %+v", got.Facets)
	}
	call := f.db.Find("from items i left join libraries lib")[0]
	if call.Args[1] != "content_hash" || call.Args[5] != `smb://nas/O\_x\%%` || call.Args[8] != int64(2) {
		t.Fatalf("args = %v", call.Args)
	}
	if rec := f.do("GET", "/api/queues/failures?status=nope", "", admin()); rec.Code != 400 {
		t.Fatalf("bad status: %d", rec.Code)
	}
}

func TestItemReasons(t *testing.T) {
	s := func(v string) *string { return &v }
	tests := []struct {
		it   FailureItem
		want string
	}{
		{FailureItem{Status: StatusWaiting, ParkedOn: s("dependency:model:tagcheck")}, "waiting:dependency:model:tagcheck"},
		{FailureItem{Status: StatusNeedsYou, Signature: s("file_missing")}, "missing_unconfirmed"},
		{FailureItem{Status: StatusNeedsYou, Signature: s("permission_denied")}, "permission"},
		{FailureItem{Status: StatusNeedsYou, Signature: s("ffmpeg_decode")}, "damaged"},
		{FailureItem{Status: StatusNeedsYou, State: "failed", Signature: s("nas_timeout"), Class: s("transient")}, "retries_spent"},
		{FailureItem{Status: StatusFailing, Signature: s("boom:x")}, "stage_failing"},
		{FailureItem{Status: StatusNeedsYou, Signature: s("boom:x"), Class: s("unknown")}, "unexplained"},
	}
	for _, tc := range tests {
		if got := itemReason(tc.it); got != tc.want {
			t.Errorf("%+v: %q, want %q", tc.it, got, tc.want)
		}
	}
}

var targetCols = []string{"id", "stage", "kind", "sid", "state", "status", "parked", "lib", "path"}

func targetsRule(rows ...[]driver.Value) *queuestest.Rule {
	return &queuestest.Rule{Match: "i.status, i.parked_on, i.lib_id::text, i.path from items i", Cols: targetCols, Rows: rows}
}

func failedTarget(id int64, kind, sid string) []driver.Value {
	return []driver.Value{id, "content_hash", kind, sid, "failed", "needs_you", nil, uidL, "smb://nas/O/a.mp4"}
}

func TestFailureActions(t *testing.T) {
	parked := []driver.Value{int64(12), "check_tags", "season", uidB, "pending", "waiting", "dependency:model:tagcheck", uidL, nil}
	tests := []struct {
		name, body string
		rules      []*queuestest.Rule
		affected   int
		want       []string // statement fragments that must run
	}{
		{"retry", `{"action":"retry","job_ids":[11,12]}`,
			[]*queuestest.Rule{targetsRule(failedTarget(11, "video", uidV), parked), enqueueRule([]driver.Value{int64(99), "inserted"}),
				{Match: "set parked_on = null, not_before = now()", Affected: 1}},
			2, []string{"processing_jobs_enqueue", "set parked_on = null"}},
		{"ignore a whole group", `{"action":"ignore","filter":{"stage":"content_hash","signature":"file_missing"}}`,
			[]*queuestest.Rule{targetsRule(failedTarget(11, "video", uidV)), {Match: "insert into failure_ignores", Affected: 1}},
			1, []string{"insert into failure_ignores"}},
		{"unignore", `{"action":"unignore","job_ids":[11]}`,
			[]*queuestest.Rule{targetsRule(failedTarget(11, "video", uidV)), {Match: "delete from failure_ignores", Affected: 1}},
			1, []string{"delete from failure_ignores"}},
		{"rescan", `{"action":"rescan","job_ids":[11]}`,
			[]*queuestest.Rule{targetsRule(failedTarget(11, "video", uidV)), enqueueRule([]driver.Value{int64(5), "inserted"})},
			1, []string{"processing_jobs_enqueue"}},
		{"remove", `{"action":"remove","job_ids":[11]}`,
			[]*queuestest.Rule{targetsRule(failedTarget(11, "video", uidV)), {Match: "delete from videos", Affected: 1}},
			1, []string{"set state = 'cancelled'", "delete from videos"}},
		{"redownload", `{"action":"redownload","job_ids":[11]}`,
			[]*queuestest.Rule{targetsRule(failedTarget(11, "video", uidV)),
				{Match: "select external_id from media_metadata_enrichment", Cols: []string{"x"}, Rows: [][]driver.Value{{"yt1"}}},
				enqueueRule([]driver.Value{int64(5), "inserted"})},
			1, []string{"insert into item_requests", "processing_jobs_enqueue"}},
		{"fix a path", `{"action":"fix_path","job_ids":[11],"path":"smb://nas/O/b.mp4"}`,
			[]*queuestest.Rule{targetsRule(failedTarget(11, "video", uidV)), enqueueRule([]driver.Value{int64(5), "inserted"})},
			1, []string{"update videos set path", "processing_jobs_enqueue"}},
		{"add a path-map rule", `{"action":"path_map","filter":{"signature":"file_missing"},"path_map":{"from":"smb://nas/O/","to":"/Volumes/O/"}}`,
			[]*queuestest.Rule{targetsRule(failedTarget(11, "video", uidV)), enqueueRule([]driver.Value{int64(5), "inserted"}),
				{Match: "update instance_identity", Affected: 1}},
			1, []string{"update instance_identity", "processing_jobs_enqueue"}},
		{"check access", `{"action":"check_access","job_ids":[11]}`,
			[]*queuestest.Rule{targetsRule(failedTarget(11, "video", uidV)), enqueueRule([]driver.Value{int64(5), "inserted"})},
			1, []string{"processing_jobs_enqueue"}},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			f := newFixture(append([]*queuestest.Rule{stagesRule(stageRow("content_hash", "episodes", 0, "nas_read"))}, tc.rules...)...)
			rec := f.do("POST", "/api/queues/failures/actions", tc.body, admin())
			if rec.Code != 200 {
				t.Fatalf("%d %s", rec.Code, rec.Body.String())
			}
			var res FixResult
			decode(t, rec, &res)
			if res.Affected != tc.affected {
				t.Fatalf("result = %+v", res)
			}
			for _, w := range tc.want {
				if len(f.db.Find(w)) == 0 {
					t.Errorf("no statement with %q", w)
				}
			}
			audit := f.db.Find("insert into audit_log")
			if len(audit) != 1 || audit[0].Args[0] != "queue.failures."+strings.Split(tc.body, `"`)[3] || audit[0].Args[1] != nil {
				t.Fatalf("audit = %+v", audit)
			}
			if len(f.db.Find("commit")) != 1 {
				t.Error("not committed")
			}
		})
	}
}

func TestFailureActionsRefuse(t *testing.T) {
	f := newFixture(stagesRule(stageRow("content_hash", "episodes", 0, "nas_read")))
	cases := map[string]string{
		`{"action":"retry","job_ids":[1]}`:                            "",
		`{"action":"explode","job_ids":[1]}`:                          "unknown action",
		`{"action":"retry"}`:                                          "one of job_ids or filter",
		`{"action":"fix_path","job_ids":[1,2],"path":"x"}`:            "one job_id",
		`{"action":"path_map","job_ids":[1],"path_map":{"from":"a"}}`: "path_map takes",
		`{"action":"retry","filter":{"status":"bogus"}}`:              "status must be",
	}
	if rec := f.do("POST", "/api/queues/failures/actions", `{"action":"retry","job_ids":[1]}`, allViewer()); rec.Code != 403 {
		t.Fatalf("a non-admin acts: %d", rec.Code)
	}
	for body, want := range cases {
		if want == "" {
			continue
		}
		rec := f.do("POST", "/api/queues/failures/actions", body, admin())
		if rec.Code != 400 || !strings.Contains(rec.Body.String(), want) {
			t.Errorf("%s: %d %s", body, rec.Code, rec.Body.String())
		}
	}
}
