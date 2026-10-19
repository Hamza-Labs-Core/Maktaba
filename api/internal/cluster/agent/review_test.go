package agent

import (
	"bytes"
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// The jobs snapshot carries the running jobs of every live process (from
// its latest metrics), in the shape a `job` message carries.
func TestJobsSnapshotRunning(t *testing.T) {
	f := newFixture(t)
	a, _ := f.node("busy")
	a.send(map[string]any{"t": "metrics", "ts": "2026-10-19T08:00:02Z", "pipeline": map[string]any{"jobs": []any{
		map[string]any{"job_id": 812, "stage": "extract", "subject": "video:4f1c", "title": "Lecture 1",
			"since": "2026-10-19T07:59:00Z", "progress": map[string]any{"step": "decode", "done": 30, "total": 100}},
		map[string]any{"job_id": 813, "stage": "probe", "subject": "video:None", "title": nil,
			"since": "2026-10-19T08:00:00Z", "progress": nil},
	}}})
	waitFor(t, "metrics", func() bool { return f.hub.Processes()[0].Metrics != nil })
	b := f.browser("viewer")
	snap := b.subscribeSnap(TopicJobs)
	var s struct {
		Jobs    []json.RawMessage `json:"jobs"`
		Running []struct {
			Session  string         `json:"session"`
			IP       string         `json:"ip"`
			NodeName *string        `json:"node_name"`
			WorkerID string         `json:"worker_id"`
			Host     string         `json:"host"`
			Job      map[string]any `json:"job"`
		} `json:"running"`
	}
	if err := json.Unmarshal(snap, &s); err != nil || len(s.Running) != 2 || s.Jobs == nil {
		t.Fatalf("snapshot = %s", snap)
	}
	r := s.Running[0]
	if r.Session != a.Session || r.IP != "127.0.0.1" || r.NodeName == nil || *r.NodeName != "busy" || r.WorkerID == "" ||
		r.Host != "test-host" {
		t.Fatalf("running[0] = %+v", r)
	}
	j := r.Job
	if j["event"] != "progress" || j["job_id"] != float64(812) || j["stage"] != "extract" || j["subject_kind"] != "video" ||
		j["subject_id"] != "4f1c" || j["title"] != "Lecture 1" || j["started_at"] != "2026-10-19T07:59:00Z" ||
		j["ts"] != "2026-10-19T08:00:02Z" || j["progress"].(map[string]any)["done"] != float64(30) {
		t.Fatalf("job = %v", j)
	}
	if j2 := s.Running[1].Job; j2["event"] != "claimed" || j2["subject_id"] != nil || j2["progress"] != nil {
		t.Fatalf("job 2 = %v", j2)
	}
}

// A frame too large for the channel is refused without closing it; the
// router fails such a request with too_large; the door answers 413.
func TestFrameSizeLimit(t *testing.T) {
	f := newFixture(t)
	runner, _ := f.node("runner")
	s := f.hub.Session(runner.Session)
	if err := s.sendErr(map[string]any{"t": "x", "pad": strings.Repeat("a", MaxFrame)}); err != ErrFrameTooLarge {
		t.Fatalf("err = %v", err)
	}
	runner.send(map[string]any{"t": "ping"})
	runner.recvType("pong") // still open

	announce(runner, model("m", true))
	waitFor(t, "model", func() bool { return len(f.hub.Router().Models()) == 1 })
	// from a process: the request does not fit the runner's channel
	asker, _ := f.node("asker")
	// the asker's frame just fits; the runner's (a longer id, plus
	// "stream") does not
	frame := func(pad string) map[string]any {
		return map[string]any{"t": "llm.request", "id": "big", "model": "m", "op": "chat",
			"body": map[string]any{"pad": pad}}
	}
	empty, _ := json.Marshal(frame(""))
	asker.send(frame(strings.Repeat("b", MaxFrame-sealOverhead-len(empty))))
	done := asker.recvType("llm.done")
	if e, _ := done["error"].(map[string]any); e["code"] != LLMTooLarge {
		t.Fatalf("done = %v", done)
	}
	// the door: over the limit before it reaches the router
	for _, n := range []int{MaxFrame - 100, MaxFrame + 10} {
		body := `{"model":"m","messages":[{"role":"user","content":"` + strings.Repeat("c", n) + `"}]}`
		resp := f.do("POST", "/api/cluster/llm/v1/chat/completions", body, "admin")
		raw, _ := io.ReadAll(resp.Body)
		if resp.StatusCode != 413 || !strings.Contains(string(raw), `"code":"too_large"`) {
			t.Fatalf("door %d bytes: %d %.200s", n, resp.StatusCode, raw)
		}
	}
	// the runner was never touched
	runner.send(map[string]any{"t": "ping"})
	runner.recvType("pong")
	if len(f.hub.Processes()) != 2 {
		t.Fatal("a session was closed")
	}
}

func TestHomeKeyModes(t *testing.T) {
	dir := t.TempDir()
	perm := func(p string) os.FileMode {
		st, err := os.Stat(p)
		if err != nil {
			t.Fatal(err)
		}
		return st.Mode().Perm()
	}
	// created at the mode asked for
	p600 := filepath.Join(dir, "a", "home-agent.key")
	if _, err := LoadOrCreateKey(p600, KeyMode); err != nil || perm(p600) != 0o600 {
		t.Fatalf("0600: %v %v", err, perm(p600))
	}
	p640 := filepath.Join(dir, "b", "home-agent.key")
	k, err := LoadOrCreateKey(p640, GroupKeyMode)
	if err != nil || perm(p640) != 0o640 || perm(filepath.Dir(p640))&0o050 != 0o050 {
		t.Fatalf("0640: %v file %v dir %v", err, perm(p640), perm(filepath.Dir(p640)))
	}
	// an existing 0600 key opens to 0640 only when asked
	if _, err := LoadOrCreateKey(p600, KeyMode); err != nil || perm(p600) != 0o600 {
		t.Fatal("0600 changed")
	}
	if k2, err := LoadOrCreateKey(p600, GroupKeyMode); err != nil || perm(p600) != 0o640 || len(k2) != 32 {
		t.Fatalf("0600 → 0640: %v", perm(p600))
	}
	// looser modes are tightened to the setting; the key itself is kept
	_ = os.Chmod(p640, 0o644)
	if k2, _ := LoadOrCreateKey(p640, GroupKeyMode); perm(p640) != 0o640 || !bytes.Equal(k, k2) {
		t.Fatalf("0644 with 0640: %v", perm(p640))
	}
	if _, _ = LoadOrCreateKey(p640, KeyMode); perm(p640) != 0o600 {
		t.Fatalf("0640 with 0600: %v", perm(p640))
	}
	// a stricter mode is never loosened beyond the setting
	_ = os.Chmod(p640, 0o400)
	if _, _ = LoadOrCreateKey(p640, KeyMode); perm(p640) != 0o400 {
		t.Fatalf("0400 loosened: %v", perm(p640))
	}
	// the cluster key is 0600, always
	pc := filepath.Join(dir, "c", "home-cluster.key")
	if _, _, err := LoadOrCreateClusterKey(pc); err != nil || perm(pc) != 0o600 {
		t.Fatalf("cluster key: %v %v", err, perm(pc))
	}
	// the setting
	for v, want := range map[string]os.FileMode{"0600": 0o600, "600": 0o600, "0640": 0o640, "640": 0o640} {
		if m, ok := ParseKeyMode(v); !ok || m != want {
			t.Errorf("ParseKeyMode(%q) = %v %v", v, m, ok)
		}
	}
	for _, v := range []string{"0644", "0777", "rw", ""} {
		if _, ok := ParseKeyMode(v); ok {
			t.Errorf("ParseKeyMode(%q) accepted", v)
		}
	}
	cfg, warn := FromEnv(func(k string) string {
		return map[string]string{"MAKTABA_HOME_AGENT_KEY_MODE": "0640"}[k]
	})
	if cfg.HomeKeyMode != GroupKeyMode || len(warn) != 0 {
		t.Fatalf("env 0640: %v %v", cfg.HomeKeyMode, warn)
	}
	cfg, warn = FromEnv(func(k string) string {
		return map[string]string{"MAKTABA_HOME_AGENT_KEY_MODE": "0666"}[k]
	})
	if cfg.HomeKeyMode != KeyMode || len(warn) != 1 {
		t.Fatalf("env 0666: %v %v", cfg.HomeKeyMode, warn)
	}
}

func TestSharedDirsAreGroupWritable(t *testing.T) {
	old := setUmask(0o022)
	defer setUmask(old)
	dir := t.TempDir()
	// an existing folder the hub did not create keeps its mode
	pre := filepath.Join(dir, "10.0.0.1", KindEvents)
	if err := os.MkdirAll(pre, 0o755); err != nil {
		t.Fatal(err)
	}
	_ = os.Chmod(pre, 0o755)
	fw := NewFileWriter(4, 0, time.Now, quiet())
	fw.SetTarget(dir, "10.0.0.1")
	fw.write(fileRecord{kind: KindEvents, at: time.Now().UTC(), line: []byte(`{}`)})
	fw.write(fileRecord{kind: KindQueues, at: time.Now().UTC(), line: []byte(`{}`)})
	fw.Flush()
	date := time.Now().UTC().Format("2006-01-02")
	for p, want := range map[string]os.FileMode{
		filepath.Join(dir, "10.0.0.1", KindEvents, date):          0o775,
		filepath.Join(dir, "10.0.0.1", KindQueues):                0o775,
		filepath.Join(dir, "10.0.0.1", KindQueues, date):          0o775,
		filepath.Join(dir, "10.0.0.1", KindEvents):                0o755, // not the hub's
		filepath.Join(dir, "10.0.0.1", KindEvents, date, hubFile): 0o644,
	} {
		st, err := os.Stat(p)
		if err != nil {
			t.Fatal(err)
		}
		if st.Mode().Perm() != want {
			t.Errorf("%s = %v, want %v", p, st.Mode().Perm(), want)
		}
	}
	// the cluster directory the locator makes on the share, too
	share := filepath.Join(dir, "share")
	_ = os.MkdirAll(filepath.Join(share, "videos"), 0o755)
	loc := (&Locator{ShareRoot: filepath.ToSlash(share), Fallback: filepath.Join(dir, "local")}).Resolve(t.Context())
	if !loc.OnShare {
		t.Fatalf("loc = %+v", loc)
	}
	for _, p := range []string{filepath.Join(share, "maktaba"), filepath.Join(share, "maktaba", "cluster")} {
		if st, _ := os.Stat(p); st.Mode().Perm() != 0o775 {
			t.Errorf("%s = %v", p, st.Mode().Perm())
		}
	}
}
