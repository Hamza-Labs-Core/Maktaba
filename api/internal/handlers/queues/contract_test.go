package queues

import (
	"context"
	"database/sql/driver"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/Hamza-Labs-Core/Maktaba/api/internal/handlers/queues/queuestest"
)

// Pins the JSON shape workstream C codes against
// (web/src/lib/queueApi.ts): where the spec and queueApi.ts differ,
// queueApi.ts wins.

func TestWorkersJSON_MatchesQueueAPI(t *testing.T) {
	f := newFixture(
		stagesRule(stageRow("probe", "episodes", 1, "nas_read"), stageRow("transcribe", "media", 2, "gpu")),
		&queuestest.Rule{Match: "from workers w left join cluster_nodes",
			Cols: []string{"id", "host", "stages", "pools", "started_at", "last_seen_at", "version", "node_id", "node_name", "role"},
			Rows: [][]driver.Value{{"mac-1", "Mac", "{transcribe,probe,x1,x2,x3}", nil, t0, t0, "1", nil, nil, "home"}}})
	if rec := f.do("GET", "/api/workers", "", viewer()); rec.Code != 403 {
		t.Fatalf("workers for a non-admin: %d (host detail is admin-only)", rec.Code)
	}
	rec := f.do("GET", "/api/workers", "", admin())
	var raw struct {
		Workers []map[string]any `json:"workers"`
	}
	decode(t, rec, &raw)
	w := raw.Workers[0]
	for _, k := range []string{"worker_id", "host", "online", "last_seen_at", "serves", "serves_label", "node_id", "node_name", "role"} {
		if _, ok := w[k]; !ok {
			t.Errorf("worker missing %q: %v", k, w)
		}
	}
	if w["worker_id"] != "mac-1" || w["online"] != true {
		t.Errorf("worker = %v", w)
	}
	if label := w["serves_label"].(string); !strings.HasPrefix(label, "Transcribe, Probe, x1, x2, +1 more") {
		t.Errorf("serves_label = %q", label)
	}
}

func TestQueueSummaryJSON_ServedByIsHostList(t *testing.T) {
	b, _ := json.Marshal(summarizeStage(stageInputs{
		Info: StageInfo{Stage: "probe", Enabled: true}, Served: Coverage{Workers: 2, Hosts: []string{"Mac", "PC"}}, Now: t0}))
	var m map[string]any
	_ = json.Unmarshal(b, &m)
	hosts, ok := m["served_by"].([]any)
	if !ok || len(hosts) != 2 || hosts[0] != "Mac" {
		t.Errorf("served_by = %v (queueApi.ts: string[])", m["served_by"])
	}
	if _, ok := m["no_worker"].(bool); !ok {
		t.Errorf("no_worker must be a bool: %v", m["no_worker"])
	}
	if _, ok := m["extension"]; !ok {
		t.Errorf("extension key missing")
	}
}

func TestExtensionJSON_MatchesQueueAPI(t *testing.T) {
	f := newFixture()
	rec := f.do("GET", "/api/extensions", "", admin())
	var out struct {
		Extensions []map[string]any `json:"extensions"`
	}
	decode(t, rec, &out)
	for _, e := range out.Extensions {
		for _, k := range []string{"name", "label", "description", "version", "enabled", "configured", "settings_schema", "settings", "secrets_set"} {
			if _, ok := e[k]; !ok {
				t.Errorf("%v missing %q", e["name"], k)
			}
		}
		for _, f := range e["settings_schema"].([]any) {
			typ := f.(map[string]any)["type"]
			switch typ {
			case "text", "url", "secret", "number", "boolean", "select":
			default:
				t.Errorf("schema type %v is not in queueApi.ts ExtensionSettingField", typ)
			}
		}
	}
}

func TestTimelineChanges(t *testing.T) {
	timelineCols := append(jobRefCols(), "reasons", "dur_ms", "summary")
	row := func(id int64, summary any) []driver.Value {
		return append(jobRefRow(id, "plex_collection", "done"), "[]", 10.0, summary)
	}
	f := newFixture(
		stagesRule(),
		&queuestest.Rule{Match: "from videos v join libraries l", Cols: []string{"a", "b", "c", "d", "e"}, Rows: [][]driver.Value{{uidB, "Kids", 600.0, true, false}}},
		&queuestest.Rule{Match: "where j.subject_kind = $1 and j.subject_id = $2::uuid", Cols: timelineCols, Rows: [][]driver.Value{
			row(3, nil),
			row(2, `{"changes":[{"action":"added","what":"episodes","target":"Show S1","count":2,"items":["e1","e2"]}]}`),
		}},
		&queuestest.Rule{Match: "kind in ('applied', 'review')", Cols: []string{"job_id", "data"}, Rows: [][]driver.Value{
			{int64(3), `{"changes":[{"action":"created","what":"collection","target":"Show S1"}]}`}}},
	)
	rec := f.do("GET", "/api/items/video/"+uidA+"/history", "", admin())
	var h ItemHistory
	decode(t, rec, &h)
	if len(h.Timeline) != 2 {
		t.Fatalf("timeline = %d (%s)", len(h.Timeline), rec.Body.String())
	}
	if c := h.Timeline[0].Changes; len(c) != 1 || c[0].Action != "created" || c[0].What != "collection" {
		t.Errorf("changes from job_events = %+v", c)
	}
	if c := h.Timeline[1].Changes; len(c) != 1 || c[0].Count == nil || *c[0].Count != 2 || c[0].Items[1] != "e2" {
		t.Errorf("changes from output_summary fallback = %+v", c)
	}
}

func TestRecordBacklog(t *testing.T) {
	f := newFixture(snapshotRules()...)
	sub := f.h.Hub.Subscribe(SnapshotChannel)
	_ = sub
	if !f.h.publishSnapshotOnce(context.Background()) {
		t.Fatal("expected publish")
	}
	calls := f.db.Find("insert into health_samples")
	if len(calls) != 1 || !strings.Contains(calls[0].SQL, "'backlog.ready'") || !strings.Contains(calls[0].SQL, "on conflict do nothing") {
		t.Fatalf("calls = %+v", calls)
	}
	if calls[0].Args[1] != 11.0 {
		t.Errorf("backlog value = %v", calls[0].Args[1])
	}
	if _, ok := calls[0].Args[0].(time.Time); !ok {
		t.Errorf("at arg = %T", calls[0].Args[0])
	}
	// A plain GET never writes.
	g := newFixture(snapshotRules()...)
	g.do("GET", "/api/queues", "", admin())
	if len(g.db.Find("insert into health_samples")) != 0 {
		t.Errorf("GET /api/queues must not write health samples")
	}
}
