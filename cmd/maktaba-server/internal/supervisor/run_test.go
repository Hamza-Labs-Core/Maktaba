package supervisor

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/Hamza-Labs-Core/Maktaba/cmd/maktaba-server/internal/config"
)

// syncBuffer is a goroutine-safe log sink.
type syncBuffer struct {
	mu sync.Mutex
	b  bytes.Buffer
}

func (s *syncBuffer) Write(p []byte) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.b.Write(p)
}

func (s *syncBuffer) String() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.b.String()
}

func runningPID(role Role) func(Snapshot) bool {
	return func(s Snapshot) bool {
		p := proc(s, role)
		return p.State == StateRunning && p.PID > 0
	}
}

// TC1: a crash restarts only the child that crashed.
func TestCrashRestartsOnlyThatChild(t *testing.T) {
	streaming, addr := fakeProc(t, RoleStreaming, "serve")
	api, _ := fakeProc(t, RoleAPI, "serve")
	logs := &syncBuffer{}
	s := New([]Process{streaming, api}, testPolicy(), WithLog(logs))
	stop, _ := running(t, s)

	snap := waitFor(t, s, "both running", func(s Snapshot) bool {
		return runningPID(RoleStreaming)(s) && runningPID(RoleAPI)(s)
	})
	streamPID, apiPID := proc(snap, RoleStreaming).PID, proc(snap, RoleAPI).PID

	get(t, "http://"+addr+"/crash?code=3")

	snap = waitFor(t, s, "streaming restarted", func(s Snapshot) bool {
		p := proc(s, RoleStreaming)
		return p.Restarts == 1 && p.State == StateRunning && p.PID > 0 && p.PID != streamPID
	})
	st := proc(snap, RoleStreaming)
	if st.LastExit == nil || st.LastExit.Code != 3 || st.LastExit.Reason != ReasonCrash {
		t.Fatalf("last exit = %+v, want code 3 reason crash", st.LastExit)
	}
	if st.LastRestartAt == nil {
		t.Error("last_restart_at not set")
	}
	a := proc(snap, RoleAPI)
	if a.PID != apiPID || a.Restarts != 0 || a.State != StateRunning {
		t.Fatalf("api was disturbed by the streaming crash: %+v (pid was %d)", a, apiPID)
	}
	if err := stop(); err != nil {
		t.Fatalf("Run returned %v after shutdown, want nil", err)
	}
	if !strings.Contains(logs.String(), "streaming exited (pid") || !strings.Contains(logs.String(), "code 3") {
		t.Errorf("exit not logged with its code:\n%s", logs.String())
	}
	// a crash is a failure: error level (the logs page's errors-only view), with the
	// service, its exit code and the restart count on the one line
	crashLine := ""
	for _, l := range strings.Split(logs.String(), "\n") {
		if strings.Contains(l, "streaming exited (pid") {
			crashLine = l
		}
	}
	for _, want := range []string{"[supervisor] level=error ", "code 3", "reason crash", "restart #1"} {
		if !strings.Contains(crashLine, want) {
			t.Errorf("crash line %q lacks %q", crashLine, want)
		}
	}
	for _, p := range s.Snapshot().Processes {
		if p.State != StateStopped || p.PID != 0 {
			t.Errorf("%s after shutdown: %+v", p.Role, p)
		}
	}
}

// TC2: a child that stops answering its probe is killed and restarted.
func TestHangIsKilledAndRestarted(t *testing.T) {
	streaming, addr := fakeProc(t, RoleStreaming, "serve")
	s := New([]Process{streaming}, testPolicy(), WithLog(&syncBuffer{}))
	running(t, s)

	snap := waitFor(t, s, "running", runningPID(RoleStreaming))
	pid := proc(snap, RoleStreaming).PID
	get(t, "http://"+addr+"/hang")

	snap = waitFor(t, s, "restart after hang", func(s Snapshot) bool {
		p := proc(s, RoleStreaming)
		return p.Restarts >= 1 && p.PID > 0 && p.PID != pid && p.State == StateRunning
	})
	st := proc(snap, RoleStreaming)
	if st.LastExit == nil || st.LastExit.Reason != ReasonHang {
		t.Fatalf("last exit = %+v, want reason hang", st.LastExit)
	}
	if !hasEvent(snap, RoleStreaming, EventHung) {
		t.Errorf("no %q event: %+v", EventHung, snap.Events)
	}
}

// A hung child is asked to stop first: one that drains (exits 0) is not killed, so a
// write it was busy with is not cut short.
func TestHangIsAskedToStopBeforeAKill(t *testing.T) {
	streaming, addr := fakeProc(t, RoleStreaming, "serve", "FAKE_ON_STOP=exit")
	logs := &syncBuffer{}
	s := New([]Process{streaming}, testPolicy(), WithLog(logs))
	running(t, s)
	waitFor(t, s, "running", runningPID(RoleStreaming))
	get(t, "http://"+addr+"/hang")
	snap := waitFor(t, s, "restart after hang", func(s Snapshot) bool { return proc(s, RoleStreaming).Restarts >= 1 })
	st := proc(snap, RoleStreaming)
	if st.LastExit == nil || st.LastExit.Reason != ReasonHang || st.LastExit.Signal != "" || st.LastExit.Code != 0 {
		t.Fatalf("last exit = %+v, want a clean exit (code 0, no signal) for a hang", st.LastExit)
	}
	if !strings.Contains(logs.String(), "asking it to stop") || strings.Contains(logs.String(), "killing it") {
		t.Errorf("want an ask and no kill:\n%s", logs.String())
	}
}

// A hung child that does not stop when asked is killed after HangGrace.
func TestHangIgnoringTheAskIsKilled(t *testing.T) {
	old := HangGrace
	HangGrace = 300 * time.Millisecond
	t.Cleanup(func() { HangGrace = old })
	streaming, addr := fakeProc(t, RoleStreaming, "serve", "FAKE_ON_STOP=hold")
	logs := &syncBuffer{}
	s := New([]Process{streaming}, testPolicy(), WithLog(logs))
	running(t, s)
	waitFor(t, s, "running", runningPID(RoleStreaming))
	get(t, "http://"+addr+"/hang")
	snap := waitFor(t, s, "restart after hang", func(s Snapshot) bool { return proc(s, RoleStreaming).Restarts >= 1 })
	if st := proc(snap, RoleStreaming); st.LastExit == nil || st.LastExit.Reason != ReasonHang {
		t.Fatalf("last exit = %+v, want reason hang", proc(snap, RoleStreaming).LastExit)
	}
	if !strings.Contains(logs.String(), "did not stop within") {
		t.Errorf("no kill after the grace:\n%s", logs.String())
	}
}

// A child's own ProbeTimeout covers a /healthz slower than the policy's timeout (a busy
// Python worker): it is not taken for a hang. Without it, the same child is.
func TestAProcessProbeTimeoutCoversASlowHealthz(t *testing.T) {
	slow, _ := fakeProc(t, RoleStreaming, "serve", "FAKE_HEALTHZ_DELAY=500ms") // policy: 300ms
	slow.ProbeTimeout = 2 * time.Second
	s := New([]Process{slow}, testPolicy(), WithLog(&syncBuffer{}))
	running(t, s)
	waitFor(t, s, "running", runningPID(RoleStreaming))
	time.Sleep(2 * time.Second)
	if p := proc(s.Snapshot(), RoleStreaming); p.Restarts != 0 || p.State != StateRunning {
		t.Fatalf("a slow but answering child was restarted: %+v", p)
	}

	strict, _ := fakeProc(t, RoleAPI, "serve", "FAKE_HEALTHZ_DELAY=500ms")
	s2 := New([]Process{strict}, testPolicy(), WithLog(&syncBuffer{}))
	running(t, s2)
	waitFor(t, s2, "taken for a hang", func(s Snapshot) bool { return proc(s, RoleAPI).Restarts >= 1 })
}

// TC3: a crash loop backs off, then marks the child failed, and the
// sibling keeps running.
func TestCrashLoopBacksOffThenFails(t *testing.T) {
	pipeline, _ := fakeProc(t, RolePipeline, "crash")
	api, _ := fakeProc(t, RoleAPI, "serve")
	pol := testPolicy()
	pol.MaxRestarts = 4
	s := New([]Process{pipeline, api}, pol, WithLog(&syncBuffer{}))
	running(t, s)

	snap := waitFor(t, s, "api running", runningPID(RoleAPI))
	apiPID := proc(snap, RoleAPI).PID

	snap = waitFor(t, s, "pipeline failed", func(s Snapshot) bool {
		return proc(s, RolePipeline).State == StateFailed
	})
	p := proc(snap, RolePipeline)
	if p.Restarts != pol.MaxRestarts-1 {
		t.Errorf("restarts = %d, want %d before being marked failed", p.Restarts, pol.MaxRestarts-1)
	}
	if !p.CrashLoop || p.NextRestartAt == nil || p.LastExit == nil || p.LastExit.Code != 7 {
		t.Errorf("failed child = %+v", p)
	}
	var delays []float64
	for _, e := range snap.Events {
		if e.Role == RolePipeline && e.Kind == EventRestarting {
			delays = append(delays, e.DelaySec)
		}
	}
	if len(delays) < 3 || !(delays[0] < delays[1] && delays[1] < delays[2]) {
		t.Errorf("restart delays %v do not grow", delays)
	}
	if !hasEvent(snap, RolePipeline, EventFailed) {
		t.Error("no failed event")
	}
	if a := proc(s.Snapshot(), RoleAPI); a.PID != apiPID || a.State != StateRunning {
		t.Errorf("api disturbed by the crash loop: %+v", a)
	}
}

// TC4: a shutdown during a backoff wait returns promptly and starts
// nothing new.
func TestShutdownDuringBackoff(t *testing.T) {
	pipeline, _ := fakeProc(t, RolePipeline, "crash")
	pol := testPolicy()
	pol.BackoffInitial, pol.BackoffMax = time.Hour, time.Hour
	s := New([]Process{pipeline}, pol, WithLog(&syncBuffer{}))
	stop, _ := running(t, s)

	waitFor(t, s, "backoff", func(s Snapshot) bool { return proc(s, RolePipeline).State == StateBackoff })
	start := time.Now()
	if err := stop(); err != nil {
		t.Fatal(err)
	}
	if d := time.Since(start); d > 3*time.Second {
		t.Errorf("shutdown during backoff took %s", d)
	}
	snap := s.Snapshot()
	if p := proc(snap, RolePipeline); p.State != StateStopped || p.Restarts != 0 {
		t.Errorf("after shutdown: %+v", p)
	}
	if n := countEvents(snap, RolePipeline, EventStarted); n != 1 {
		t.Errorf("started %d times, want 1", n)
	}
}

// TC5a: restart = on-failure leaves a clean exit stopped.
func TestOnFailureLeavesCleanExitStopped(t *testing.T) {
	pipeline, _ := fakeProc(t, RolePipeline, "exit0")
	pol := testPolicy()
	pol.Restart = RestartOnFailure
	s := New([]Process{pipeline}, pol, WithLog(&syncBuffer{}))
	running(t, s)
	snap := waitFor(t, s, "exited", func(s Snapshot) bool { return proc(s, RolePipeline).State == StateExited })
	p := proc(snap, RolePipeline)
	if p.Restarts != 0 || p.LastExit == nil || p.LastExit.Reason != ReasonClean {
		t.Errorf("on-failure clean exit: %+v", p)
	}
}

// TC5b: restart = never keeps the old behaviour — any exit stops the
// stack and is returned.
func TestNeverStopsTheStack(t *testing.T) {
	pipeline, _ := fakeProc(t, RolePipeline, "crash", "FAKE_CODE=2")
	api, _ := fakeProc(t, RoleAPI, "serve")
	pol := testPolicy()
	pol.Restart = RestartNever
	s := New([]Process{pipeline, api}, pol, WithLog(&syncBuffer{}))
	_, done := running(t, s)
	select {
	case err := <-done:
		if err == nil || !strings.Contains(err.Error(), "pipeline exited") || !strings.Contains(err.Error(), "code 2") {
			t.Fatalf("Run = %v, want the pipeline exit", err)
		}
	case <-time.After(20 * time.Second):
		t.Fatal("Run did not return after a child exit with restart = never")
	}
	if a := proc(s.Snapshot(), RoleAPI); a.State != StateStopped || a.PID != 0 {
		t.Errorf("api left running: %+v", a)
	}
}

// EC1: a binary that cannot start goes through backoff into failed.
func TestStartFailureIsACrash(t *testing.T) {
	if testing.Short() {
		t.Skip("waits on backoff; runs without -short")
	}
	cmd := exec.Command(filepath.Join(t.TempDir(), "no-such-binary"))
	pol := testPolicy()
	pol.MaxRestarts = 2
	s := New([]Process{{Role: RoleStreaming, Cmd: cmd}}, pol, WithLog(&syncBuffer{}))
	running(t, s)
	snap := waitFor(t, s, "failed", func(s Snapshot) bool { return proc(s, RoleStreaming).State == StateFailed })
	if p := proc(snap, RoleStreaming); p.LastExit == nil || p.LastExit.Reason != ReasonStartFailed {
		t.Errorf("last exit = %+v", p.LastExit)
	}
}

// A child that stays up past healthy_after resets its backoff.
func TestHealthyRunResetsBackoff(t *testing.T) {
	streaming, addr := fakeProc(t, RoleStreaming, "serve")
	pol := testPolicy()
	pol.HealthyAfter = 300 * time.Millisecond
	pol.MaxRestarts = 3
	s := New([]Process{streaming}, pol, WithLog(&syncBuffer{}))
	running(t, s)
	for i := 1; i <= 4; i++ {
		waitFor(t, s, "running", runningPID(RoleStreaming))
		time.Sleep(400 * time.Millisecond) // > healthy_after
		get(t, "http://"+addr+"/crash?code=4")
		n := i
		waitFor(t, s, "restarted", func(s Snapshot) bool { return proc(s, RoleStreaming).Restarts == n })
	}
	snap := s.Snapshot()
	if p := proc(snap, RoleStreaming); p.State == StateFailed || p.CrashLoop {
		t.Errorf("healthy runs between crashes must not count as a crash loop: %+v", p)
	}
	for _, e := range snap.Events {
		if e.Kind == EventRestarting && e.DelaySec != pol.BackoffInitial.Seconds() {
			t.Errorf("delay %v after a healthy run, want the initial %v", e.DelaySec, pol.BackoffInitial.Seconds())
		}
	}
}

func TestEventsPersistAcrossSupervisorRestart(t *testing.T) {
	path := filepath.Join(t.TempDir(), "supervisor", "events.jsonl")
	pipeline, _ := fakeProc(t, RolePipeline, "crash")
	pol := testPolicy()
	pol.MaxRestarts = 2
	s := New([]Process{pipeline}, pol, WithLog(&syncBuffer{}), WithEventSink(NewFileSink(path, 0)))
	stop, _ := running(t, s)
	waitFor(t, s, "failed", func(s Snapshot) bool { return proc(s, RolePipeline).State == StateFailed })
	_ = stop()
	before := s.Snapshot().Events

	s2 := New([]Process{pipeline}, pol, WithEventSink(NewFileSink(path, 0)))
	after := s2.Snapshot().Events
	if len(after) != len(before) || after[len(after)-1].Seq != before[len(before)-1].Seq {
		t.Fatalf("reloaded %d events (last %+v), want %d", len(after), after[len(after)-1], len(before))
	}
	s2.emit(Event{Role: RoleAPI, Kind: EventStarted})
	if got := s2.Snapshot().Events; got[len(got)-1].Seq != before[len(before)-1].Seq+1 {
		t.Errorf("seq does not continue after reload: %d", got[len(got)-1].Seq)
	}
}

func TestFileSinkRotates(t *testing.T) {
	path := filepath.Join(t.TempDir(), "events.jsonl")
	f := NewFileSink(path, 200)
	for i := 1; i <= 20; i++ {
		if err := f.Append(Event{Seq: int64(i), Role: RoleAPI, Kind: EventStarted}); err != nil {
			t.Fatal(err)
		}
	}
	got, err := f.Load(5)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 5 || got[4].Seq != 20 || got[0].Seq != 16 {
		t.Fatalf("Load(5) = %+v", got)
	}
}

func TestHandlerServesSnapshot(t *testing.T) {
	streaming, _ := fakeProc(t, RoleStreaming, "serve")
	s := New([]Process{streaming}, testPolicy(), WithLog(&syncBuffer{}))
	running(t, s)
	waitFor(t, s, "running", runningPID(RoleStreaming))

	rr := httptest.NewRecorder()
	s.Handler().ServeHTTP(rr, httptest.NewRequest(http.MethodGet, "/processes", nil))
	if rr.Code != http.StatusOK {
		t.Fatalf("status %d", rr.Code)
	}
	var snap Snapshot
	if err := json.Unmarshal(rr.Body.Bytes(), &snap); err != nil {
		t.Fatal(err)
	}
	p := proc(snap, RoleStreaming)
	if p.State != StateRunning || p.PID == 0 || p.LastProbe == nil || !p.LastProbe.OK || p.UptimeSec <= 0 {
		t.Errorf("served status = %+v", p)
	}
	if snap.Policy.Restart != RestartAlways {
		t.Errorf("policy = %+v", snap.Policy)
	}
	rr = httptest.NewRecorder()
	s.Handler().ServeHTTP(rr, httptest.NewRequest(http.MethodPost, "/processes", nil))
	if rr.Code != http.StatusMethodNotAllowed {
		t.Errorf("POST status %d", rr.Code)
	}
}

func hasEvent(s Snapshot, role Role, kind string) bool { return countEvents(s, role, kind) > 0 }

func countEvents(s Snapshot, role Role, kind string) int {
	n := 0
	for _, e := range s.Events {
		if e.Role == role && e.Kind == kind {
			n++
		}
	}
	return n
}

// The pipeline's Python workers, main and transcription, get PythonProbeTimeout.
func TestPythonWorkersGetTheLongProbeTimeout(t *testing.T) {
	var cfg config.Config
	cfg.Database.URL = "postgresql://local/ignored"
	cfg.Pipeline.Instances = 1
	if p := mainProcess(t, cfg); p.ProbeTimeout != PythonProbeTimeout {
		t.Errorf("main worker probe timeout = %v", p.ProbeTimeout)
	}
	procs, err := BuildPipelineInstances(Options{Config: cfg})
	if err != nil || len(procs) != 1 || procs[0].ProbeTimeout != PythonProbeTimeout {
		t.Fatalf("transcription worker: %v, %+v", err, procs)
	}
}
