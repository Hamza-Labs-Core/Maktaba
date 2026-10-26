package supervisor

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Hamza-Labs-Core/Maktaba/cmd/maktaba-server/internal/config"
)

func chromaConfig(t *testing.T) (config.Config, string) {
	t.Helper()
	cfg := indexConfig(t)
	t.Setenv("MAKTABA_CHROMA_DIR", "")
	cfg.Database.URL = "postgres://maktaba@127.0.0.1:54329/maktaba"
	return cfg, filepath.Join(cfg.StateDir(), "chroma")
}

func makeChroma(t *testing.T, dir string) {
	t.Helper()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "chroma.sqlite3"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
}

// TestTheChromaCopyIsAHomeJob: a home with a Chroma folder and Postgres runs
// the copy as a job of the index worker's environment; without the folder, on SQLite
// or once the copy is done, it does not.
func TestTheChromaCopyIsAHomeJob(t *testing.T) {
	cfg, dir := chromaConfig(t)
	if procs, err := BuildChromaCopy(Options{Config: cfg}); err != nil || len(procs) != 0 {
		t.Fatalf("no Chroma folder: %d processes, %v", len(procs), err)
	}
	makeChroma(t, dir)
	procs, err := BuildChromaCopy(Options{Config: cfg})
	if err != nil || len(procs) != 1 {
		t.Fatalf("copy = %d, %v", len(procs), err)
	}
	p := procs[0]
	if p.Name() != "pipeline/chroma-copy" || !p.Once || p.Tries != ChromaCopyTries || p.Probe != "" {
		t.Errorf("process = %s once=%v tries=%d probe=%q", p.Name(), p.Once, p.Tries, p.Probe)
	}
	if args := strings.Join(p.Cmd.Args[1:], " "); args != "-m maktaba_pipeline copy-chroma" {
		t.Errorf("args = %q", args)
	}
	m := envMap(p.Cmd.Env)
	state := cfg.StateDir()
	for k, want := range map[string]string{
		"MAKTABA_CHROMA_DIR":           dir,
		"MAKTABA_COPY_CHROMA_DONE":     filepath.Join(state, ChromaCopyDone),
		"MAKTABA_COPY_CHROMA_PROGRESS": filepath.Join(state, ChromaCopyProgress),
		"MAKTABA_PIPELINE_SHARE":       "off",
		"MAKTABA_DATABASE_URL":         cfg.Database.URL,
	} {
		if m[k] != want {
			t.Errorf("%s = %q, want %q", k, m[k], want)
		}
	}

	sqlite := cfg
	sqlite.Database.URL = "sqlite:///" + filepath.Join(state, "maktaba.db")
	if procs, _ := BuildChromaCopy(Options{Config: sqlite}); len(procs) != 0 {
		t.Error("SQLite has no semantic search to copy into")
	}
}

// TestTheChromaFolderGoesAStartAfterTheCopy: the start after the copy deletes the folder
// it names (the start of the copy kept it, for a rollback); never another folder.
func TestTheChromaFolderGoesAStartAfterTheCopy(t *testing.T) {
	cfg, dir := chromaConfig(t)
	makeChroma(t, dir)
	done := filepath.Join(cfg.StateDir(), ChromaCopyDone)
	other := filepath.Join(t.TempDir(), "chroma")
	if err := os.WriteFile(done, []byte(`{"chroma_dir":"`+other+`"}`), 0o644); err != nil {
		t.Fatal(err)
	}
	if procs, _ := BuildChromaCopy(Options{Config: cfg}); len(procs) != 0 {
		t.Fatal("a done copy is not run again")
	}
	time.Sleep(200 * time.Millisecond)
	if _, err := os.Stat(dir); err != nil {
		t.Fatalf("a folder the copy did not name is kept: %v", err)
	}
	if err := os.WriteFile(done, []byte(`{"chroma_dir":"`+dir+`","done_at":1}`), 0o644); err != nil {
		t.Fatal(err)
	}
	if procs, _ := BuildChromaCopy(Options{Config: cfg}); len(procs) != 0 {
		t.Fatal("a done copy is not run again")
	}
	deadline := time.Now().Add(5 * time.Second)
	for {
		if _, err := os.Stat(dir); os.IsNotExist(err) {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("the copied folder was not deleted")
		}
		time.Sleep(20 * time.Millisecond)
	}
}

func TestTheChromaCopyProgress(t *testing.T) {
	path := filepath.Join(t.TempDir(), "p.json")
	if d := chromaCopyDetail(path); d.Text != "" {
		t.Errorf("no file: %q", d.Text)
	}
	for raw, want := range map[string]string{
		`{"phase":"load","elements":250,"total":1000,"eta_sec":600}`: "Copying the search index into the database: 250 of 1000 (25%), about 10m0s left",
		`{"phase":"index","copied":990}`:                             "Building the search index in the database (990 vectors copied)",
		`{"phase":"verify"}`:                                         "Checking the copied search index",
		`{"phase":"failed","reason":"recall@10 0.8 below 0.95"}`:     "The copied search index failed its check: recall@10 0.8 below 0.95",
		`{"phase":"done"}`:                                           "",
	} {
		if err := os.WriteFile(path, []byte(raw), 0o644); err != nil {
			t.Fatal(err)
		}
		if d := chromaCopyDetail(path); d.Text != want {
			t.Errorf("%s: %q, want %q", raw, d.Text, want)
		}
	}
}

// TestAJobThatEnds: a job's clean exit leaves it exited (not restarted, not wanted:
// nothing is down), whatever the restart policy.
func TestAJobThatEnds(t *testing.T) {
	p, _ := fakeProc(t, RolePipeline, "exit0")
	p.Instance, p.Once, p.Tries = ChromaCopyInstance, true, 3
	p.Progress = func() Detail { return Detail{Text: "copying"} }
	s := New([]Process{p}, testPolicy(), WithLog(&syncBuffer{}))
	running(t, s)
	snap := waitFor(t, s, "exited", func(s Snapshot) bool { return proc(s, RolePipeline).State == StateExited })
	time.Sleep(300 * time.Millisecond) // past the policy's backoff: still not restarted
	c := proc(s.Snapshot(), RolePipeline)
	if c.Restarts != 0 || c.Wanted || c.Detail != "" {
		t.Errorf("after the job: %+v", c)
	}
	if n := countEvents(snap, RolePipeline, EventStarted); n != 1 {
		t.Errorf("started %d times", n)
	}
}

// TestAJobThatKeepsFailing: a failing job is started again until its Tries, then left
// failed with an event that says so; the stack keeps running.
func TestAJobThatKeepsFailing(t *testing.T) {
	p, _ := fakeProc(t, RolePipeline, "crash", "FAKE_CODE=1")
	p.Instance, p.Once, p.Tries = ChromaCopyInstance, true, 3
	pol := testPolicy()
	pol.Restart = RestartNever // not the stack's end: a job's failure is its own
	log := &syncBuffer{}
	s := New([]Process{p}, pol, WithLog(log))
	running(t, s)
	snap := waitFor(t, s, "failed", func(s Snapshot) bool { return proc(s, RolePipeline).State == StateFailed })
	time.Sleep(300 * time.Millisecond)
	c := proc(s.Snapshot(), RolePipeline)
	if c.Restarts != 2 || c.NextRestartAt != nil || !c.CrashLoop {
		t.Errorf("after 3 failures: %+v", c)
	}
	if n := countEvents(snap, RolePipeline, EventFailed); n != 1 {
		t.Errorf("%d failed events", n)
	}
	if !strings.Contains(log.String(), "gave up: 3 failed runs in a row") {
		t.Errorf("log: %s", log.String())
	}
}
