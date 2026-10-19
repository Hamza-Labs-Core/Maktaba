package agent

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func writeJSONL(t *testing.T, p string, lines ...string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte(strings.Join(lines, "\n")+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
}

const day = "2026-10-19"

// seedFiles lays out two machines' files as the processes write them.
func seedFiles(t *testing.T, dir string) {
	t.Helper()
	ip := filepath.Join(dir, "192.168.50.31")
	writeJSONL(t, filepath.Join(ip, "processes", "w1.json"), `{"worker_id":"w1","pid":1,"role":"node"}`)
	writeJSONL(t, filepath.Join(ip, "processes", "w2.json"), `{"worker_id":"w2","pid":2,"role":"node"}`)
	writeJSONL(t, filepath.Join(ip, "processes", "broken.json"), `{not json`)
	writeJSONL(t, filepath.Join(ip, "logs", day, "w1.jsonl"),
		`{"ts":"2026-10-19T10:00:00Z","level":"info","event":"start"}`,
		`{"ts":"2026-10-19T10:00:02Z","level":"error","event":"boom","fields":{"file":"a.mp4"}}`,
		`not json at all`,
		`{"ts":"2026-10-19T10:00:04+00:00","level":"debug","event":"tick"}`,
	)
	writeJSONL(t, filepath.Join(ip, "logs", day, "w2.jsonl"),
		`{"ts":"2026-10-19T10:00:01Z","level":"warning","event":"slow","fields":{"file":"A.MP4"}}`,
		`{"ts":"2026-10-19T10:00:03.5Z","level":"info","event":"done"}`,
	)
	writeJSONL(t, filepath.Join(ip, "logs", "2026-10-18", "w1.jsonl"), `{"ts":"2026-10-18T10:00:00Z","level":"info"}`)
	writeJSONL(t, filepath.Join(ip, "jobs", day, "w1.jsonl"),
		`{"ts":"2026-10-19T10:00:00Z","event":"claimed","stage":"transcribe","job_id":1}`,
		`{"ts":"2026-10-19T10:01:00Z","event":"done","stage":"transcribe","job_id":1}`,
		`{"ts":"2026-10-19T10:02:00Z","event":"done","stage":"probe","job_id":2}`,
	)
	var m1, m2 []string
	for i := 0; i < 60; i++ {
		m1 = append(m1, fmt.Sprintf(`{"ts":"2026-10-19T10:%02d:00Z","system":{"cpu_pct":%d}}`, i, i))
		m2 = append(m2, fmt.Sprintf(`{"ts":"2026-10-19T10:%02d:30Z","system":{"cpu_pct":%d}}`, i, 100+i))
	}
	writeJSONL(t, filepath.Join(ip, "metrics", day, "w1.jsonl"), m1...)
	writeJSONL(t, filepath.Join(ip, "metrics", day, "w2.jsonl"), m2...)
	// the hub's events about this machine sit in the home's folder
	home := filepath.Join(dir, testHomeIP)
	writeJSONL(t, filepath.Join(home, "events", day, "hub.jsonl"),
		`{"ts":"2026-10-19T09:00:00Z","event":"connected","ip":"192.168.50.31","session":"s1"}`,
		`{"ts":"2026-10-19T09:00:01Z","event":"connected","ip":"192.168.50.1","session":"s2"}`,
		`{"ts":"2026-10-19T09:00:02Z","event":"refused","ip":"192.168.50.31","reason":"bad_proof"}`,
	)
	writeJSONL(t, filepath.Join(home, "queues", day, "hub.jsonl"),
		`{"ts":"2026-10-19T09:00:00Z","stages":{"probe":{"waiting":1}}}`,
		`{"ts":"2026-10-19T09:00:10Z","stages":{"probe":{"waiting":0}}}`,
	)
}

type recordsResp struct {
	Records []map[string]any `json:"records"`
	Before  *string          `json:"before"`
}

func (f *fixture) records(path string) (int, recordsResp) {
	f.t.Helper()
	resp := f.do("GET", path, "", "admin")
	var out recordsResp
	if resp.StatusCode == 200 {
		decode(f.t, resp, &out)
	}
	return resp.StatusCode, out
}

func events(rs recordsResp) []string {
	var out []string
	for _, r := range rs.Records {
		e, _ := r["event"].(string)
		out = append(out, e)
	}
	return out
}

func TestFilesIndex(t *testing.T) {
	f := newFixture(t)
	seedFiles(t, f.dir)
	resp := f.do("GET", "/api/cluster/files", "", "admin")
	var idx FilesIndex
	decode(t, resp, &idx)
	if idx.Files.Dir != f.dir || idx.Files.OnShare {
		t.Fatalf("files = %+v", idx.Files)
	}
	var node *MachineFiles
	for i := range idx.IPs {
		if idx.IPs[i].IP == "192.168.50.31" {
			node = &idx.IPs[i]
		}
	}
	if node == nil || len(node.Processes) != 2 {
		t.Fatalf("index = %+v", idx)
	}
	if fmt.Sprint(node.Dates["logs"]) != "[2026-10-18 2026-10-19]" || fmt.Sprint(node.Dates["metrics"]) != "[2026-10-19]" ||
		len(node.Dates["queues"]) != 0 || node.Dates["library"] == nil {
		t.Fatalf("dates = %v", node.Dates)
	}
	if r := f.do("GET", "/api/cluster/files", "", "viewer"); r.StatusCode != 403 {
		t.Fatalf("viewer: %d", r.StatusCode)
	}
}

func TestHistoryLogs(t *testing.T) {
	f := newFixture(t)
	seedFiles(t, f.dir)
	base := "/api/cluster/files/192.168.50.31/logs?date=" + day

	_, all := f.records(base)
	if got := strings.Join(events(all), ","); got != "start,slow,boom,done,tick" || all.Before != nil {
		t.Fatalf("merged = %s (before %v)", got, all.Before)
	}
	if all.Records[1]["worker_id"] != "w2" || all.Records[0]["worker_id"] != "w1" {
		t.Fatalf("worker ids: %v", all.Records)
	}
	_, warn := f.records(base + "&level=warning")
	if got := strings.Join(events(warn), ","); got != "slow,boom" {
		t.Fatalf("level = %s", got)
	}
	_, q := f.records(base + "&q=a.mp4")
	if got := strings.Join(events(q), ","); got != "slow,boom" {
		t.Fatalf("q (case-insensitive) = %s", got)
	}
	_, w := f.records(base + "&worker_id=w2")
	if got := strings.Join(events(w), ","); got != "slow,done" {
		t.Fatalf("worker = %s", got)
	}
	// paging back: the newest page first, then before its oldest record
	_, p1 := f.records(base + "&limit=2")
	if got := strings.Join(events(p1), ","); got != "done,tick" || p1.Before == nil {
		t.Fatalf("page 1 = %s %v", got, p1.Before)
	}
	_, p2 := f.records(base + "&limit=2&before=" + *p1.Before)
	if got := strings.Join(events(p2), ","); got != "slow,boom" || p2.Before == nil {
		t.Fatalf("page 2 = %s", got)
	}
	_, p3 := f.records(base + "&limit=2&before=" + *p2.Before)
	if got := strings.Join(events(p3), ","); got != "start" || p3.Before != nil {
		t.Fatalf("page 3 = %s %v", got, p3.Before)
	}
	// no files for a day: empty, not an error
	if code, none := f.records("/api/cluster/files/192.168.50.31/logs?date=2020-01-01"); code != 200 || len(none.Records) != 0 {
		t.Fatalf("empty day = %d %+v", code, none)
	}
}

func TestHistoryJobsEventsMetricsQueues(t *testing.T) {
	f := newFixture(t)
	seedFiles(t, f.dir)
	_, jobs := f.records("/api/cluster/files/192.168.50.31/jobs?date=" + day + "&stage=transcribe&event=done")
	if len(jobs.Records) != 1 || jobs.Records[0]["job_id"] != float64(1) {
		t.Fatalf("jobs = %+v", jobs)
	}
	// a node's events come from the hub's file in the home's folder
	_, ev := f.records("/api/cluster/files/192.168.50.31/events?date=" + day)
	if got := strings.Join(events(ev), ","); got != "connected,refused" {
		t.Fatalf("node events = %s", got)
	}
	_, homeEv := f.records("/api/cluster/files/" + testHomeIP + "/events?date=" + day)
	if len(homeEv.Records) != 3 {
		t.Fatalf("home events = %d", len(homeEv.Records))
	}

	type samples struct {
		Samples []struct {
			TS       string         `json:"ts"`
			WorkerID string         `json:"worker_id"`
			Metrics  map[string]any `json:"metrics"`
		} `json:"samples"`
		Thinned bool `json:"thinned"`
	}
	var s samples
	decode(t, f.do("GET", "/api/cluster/files/192.168.50.31/metrics?date="+day+"&from=2026-10-19T10:10:00Z&to=2026-10-19T10:12:00Z", "", "admin"), &s)
	if len(s.Samples) != 4 || s.Samples[0].WorkerID != "w1" || s.Samples[1].WorkerID != "w2" || s.Thinned ||
		s.Samples[0].Metrics["system"].(map[string]any)["cpu_pct"] != float64(10) {
		t.Fatalf("metrics = %+v", s)
	}
	var many []string
	for i := 0; i < 1000; i++ {
		many = append(many, fmt.Sprintf(`{"ts":"2026-10-19T11:%02d:%02dZ","i":%d}`, i/60, i%60, i))
	}
	writeJSONL(t, filepath.Join(f.dir, "10.0.0.5", "metrics", day, "w9.jsonl"), many...)
	var thin samples
	decode(t, f.do("GET", "/api/cluster/files/10.0.0.5/metrics?date="+day+"&limit=150", "", "admin"), &thin)
	if !thin.Thinned || len(thin.Samples) > 300 || len(thin.Samples) < 150 || thin.Samples[0].Metrics["i"] != float64(0) {
		t.Fatalf("thinning: %d samples, thinned %v", len(thin.Samples), thin.Thinned)
	}

	var qs struct {
		Samples []map[string]any `json:"samples"`
	}
	decode(t, f.do("GET", "/api/cluster/files/"+testHomeIP+"/queues?date="+day, "", "admin"), &qs)
	if len(qs.Samples) != 2 || qs.Samples[0]["worker_id"] != nil {
		t.Fatalf("queues = %+v", qs)
	}
}

func TestHistoryRejectsBadInput(t *testing.T) {
	f := newFixture(t)
	seedFiles(t, f.dir)
	// a secret outside the cluster folder
	outside := filepath.Join(filepath.Dir(f.dir), "secret")
	writeJSONL(t, filepath.Join(outside, "logs", day, "x.jsonl"), `{"ts":"2026-10-19T10:00:00Z","event":"secret"}`)

	for _, c := range []struct {
		path   string
		status int
	}{
		{"/api/cluster/files/../logs?date=" + day, 0}, // cleaned by the client or the router: never 200 with data
		{"/api/cluster/files/%2e%2e/logs?date=" + day, 400},
		{"/api/cluster/files/..%2Fsecret/logs?date=" + day, 400},
		{"/api/cluster/files/a..b/logs?date=" + day, 400},
		{"/api/cluster/files/192.168.50.31/logs?date=../../x", 400},
		{"/api/cluster/files/192.168.50.31/logs?date=2026-13-01", 400},
		{"/api/cluster/files/192.168.50.31/secrets?date=" + day, 404},
		{"/api/cluster/files/192.168.50.31/logs?date=" + day + "&level=loud", 400},
		{"/api/cluster/files/192.168.50.31/logs?date=" + day + "&limit=-1", 400},
		{"/api/cluster/files/192.168.50.31/logs?date=" + day + "&before=yesterday", 400},
		{"/api/cluster/files/192.168.50.31/metrics?date=" + day + "&from=noon", 400},
	} {
		resp := f.do("GET", c.path, "", "admin")
		body, _ := io.ReadAll(resp.Body)
		if strings.Contains(string(body), `"event":"secret"`) {
			t.Fatalf("%s leaked: %s", c.path, body)
		}
		if c.status != 0 && resp.StatusCode != c.status {
			t.Errorf("%s: %d, want %d", c.path, resp.StatusCode, c.status)
		}
	}

	// a symlinked machine folder is not followed
	if err := os.Symlink(outside, filepath.Join(f.dir, "10.9.9.9")); err != nil {
		t.Skip("no symlinks here")
	}
	if code, rs := f.records("/api/cluster/files/10.9.9.9/logs?date=" + day); code != 200 || len(rs.Records) != 0 {
		t.Fatalf("symlink followed: %d %+v", code, rs)
	}
	var idx FilesIndex
	decode(t, f.do("GET", "/api/cluster/files", "", "admin"), &idx)
	for _, m := range idx.IPs {
		if m.IP == "10.9.9.9" {
			t.Fatal("symlinked folder listed")
		}
	}
	if r := f.do("GET", "/api/cluster/files/192.168.50.31/logs?date="+day, "", "viewer"); r.StatusCode != 403 {
		t.Fatalf("viewer: %d", r.StatusCode)
	}
}

func TestParseTSForms(t *testing.T) {
	for raw, want := range map[string]string{
		`"2026-10-19T10:00:00Z"`:        "2026-10-19T10:00:00Z",
		`"2026-10-19T12:00:00+02:00"`:   "2026-10-19T10:00:00Z",
		`"2026-10-19T10:00:00.123456"`:  "2026-10-19T10:00:00.123456Z",
		`"2026-10-19 10:00:00.5+00:00"`: "2026-10-19T10:00:00.5Z",
		`1760868000`:                    time.Unix(1760868000, 0).UTC().Format(time.RFC3339Nano),
	} {
		ts, ok := parseTS(json.RawMessage(raw))
		if !ok || ts.Format(time.RFC3339Nano) != want {
			t.Errorf("parseTS(%s) = %v %v, want %s", raw, ts, ok, want)
		}
	}
	if _, ok := parseTS(json.RawMessage(`"soon"`)); ok {
		t.Error("garbage parsed")
	}
}

func TestLongLinesSkipped(t *testing.T) {
	dir := t.TempDir()
	long := `{"ts":"2026-10-19T10:00:00Z","pad":"` + strings.Repeat("x", maxLineBytes+10) + `"}`
	mid := `{"ts":"2026-10-19T10:00:01Z","pad":"` + strings.Repeat("y", 200_000) + `"}`
	writeJSONL(t, filepath.Join(dir, "x.jsonl"), `{"a":1}`, long, mid, `{"b":2}`)
	root, err := os.OpenRoot(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = root.Close() }()
	var got []int
	eachLine(root, "x.jsonl", func(l []byte) { got = append(got, len(l)) })
	if len(got) != 3 || got[0] != 7 || got[1] != len(mid) || got[2] != 7 {
		t.Fatalf("lines = %v", got)
	}
}
