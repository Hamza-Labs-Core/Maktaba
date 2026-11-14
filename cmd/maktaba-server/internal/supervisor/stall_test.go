package supervisor

import (
	"context"
	"errors"
	"net"
	"os"
	"sync/atomic"
	"syscall"
	"testing"
	"time"

	"github.com/Hamza-Labs-Core/Maktaba/cmd/maktaba-server/internal/config"
	"github.com/Hamza-Labs-Core/Maktaba/cmd/maktaba-server/internal/pgbundle"
)

// errSilent is a check the child did not answer.
var errSilent = errors.New("did not answer")

// A child asked to stop for a restart is no longer probed: the checks it fails while it
// shuts down (a database that stops answering during its fast shutdown) do not make the
// restart a hang. It was: the home's postgres, 30 s into a shutdown it had been asked
// for, read "failed 3 liveness probes", and its exit "reason hang".
func TestAChildAskedToStopIsNotProbedAsHung(t *testing.T) {
	p, addr := fakeProc(t, RoleAPI, "serve")
	var stopping atomic.Bool
	p.Check = func(context.Context) error {
		if stopping.Load() {
			return errSilent
		}
		return nil
	}
	// the ask: it stops answering at once and exits well after probe_failures checks
	p.Stop = func() {
		stopping.Store(true)
		time.Sleep(600 * time.Millisecond) // policy: 3 checks 50 ms apart
		stopping.Store(false)
		get(t, "http://"+addr+"/exit")
	}
	logs := &syncBuffer{}
	s := New([]Process{p}, testPolicy(), WithLog(logs))
	running(t, s)
	first := waitFor(t, s, "running", runningPID(RoleAPI))
	pid := proc(first, RoleAPI).PID
	if err := s.Restart(RoleAPI, ""); err != nil {
		t.Fatal(err)
	}
	snap := waitFor(t, s, "started again", func(sn Snapshot) bool {
		a := proc(sn, RoleAPI)
		return a.State == StateRunning && a.PID != 0 && a.PID != pid
	})
	a := proc(snap, RoleAPI)
	if a.LastExit == nil || a.LastExit.Reason != ReasonRestarted {
		t.Errorf("last exit %+v, want reason %q", a.LastExit, ReasonRestarted)
	}
	if hasEvent(snap, RoleAPI, EventHung) || a.Restarts != 0 {
		t.Errorf("a child stopping on request was taken for hung (restarts %d):\n%s", a.Restarts, logs.String())
	}
}

// A child with a HangAfter is not hung while its checks have failed for less than that,
// however many failed (a database slow for a minute); it is once they failed that long.
func TestHangAfterWaitsOutASlowSpell(t *testing.T) {
	p, _ := fakeProc(t, RoleAPI, "serve")
	var silent atomic.Bool
	p.Check = func(context.Context) error {
		if silent.Load() {
			return errSilent
		}
		return nil
	}
	p.HangAfter = 2 * time.Second
	s := New([]Process{p}, testPolicy(), WithLog(&syncBuffer{}))
	running(t, s)
	waitFor(t, s, "running", runningPID(RoleAPI))

	silent.Store(true)
	waitFor(t, s, "unhealthy, with more failures than probe_failures", func(sn Snapshot) bool {
		a := proc(sn, RoleAPI)
		return a.State == StateUnhealthy && a.LastProbe != nil && a.LastProbe.Failures >= 2*testPolicy().ProbeFailures
	})
	silent.Store(false)
	snap := waitFor(t, s, "running again", func(sn Snapshot) bool { return proc(sn, RoleAPI).State == StateRunning })
	if a := proc(snap, RoleAPI); a.Restarts != 0 || hasEvent(snap, RoleAPI, EventHung) {
		t.Fatalf("a slow spell shorter than HangAfter was taken for a hang: %+v", a)
	}

	silent.Store(true)
	since := time.Now()
	waitFor(t, s, "taken for hung", func(sn Snapshot) bool { return hasEvent(sn, RoleAPI, EventHung) })
	if d := time.Since(since); d < p.HangAfter {
		t.Fatalf("taken for hung after %s, before its HangAfter of %s", d, p.HangAfter)
	}
	silent.Store(false)
	snap = waitFor(t, s, "restarted", func(sn Snapshot) bool { return proc(sn, RoleAPI).Restarts >= 1 })
	if a := proc(snap, RoleAPI); a.LastExit == nil || a.LastExit.Reason != ReasonHang {
		t.Fatalf("last exit %+v, want reason hang", a.LastExit)
	}
}

// A child's own StartupGrace covers a start slower than the policy's grace (a pipeline
// worker's cold start): it is not stopped as hung before it first answers. Without it,
// the same child is.
func TestAProcessStartupGraceCoversASlowStart(t *testing.T) {
	policy := testPolicy()
	policy.StartupGrace = 100 * time.Millisecond
	slowStart := func(role Role) Process {
		p, _ := fakeProc(t, role, "serve")
		up := time.Now().Add(1200 * time.Millisecond) // its health port "opens" then
		p.Check = func(context.Context) error {
			if time.Now().Before(up) {
				return errors.New("connection refused")
			}
			return nil
		}
		return p
	}
	patient := slowStart(RolePipeline)
	patient.StartupGrace = 10 * time.Second
	s := New([]Process{patient}, policy, WithLog(&syncBuffer{}))
	running(t, s)
	snap := waitFor(t, s, "running", runningPID(RolePipeline))
	if a := proc(snap, RolePipeline); a.Restarts != 0 || hasEvent(snap, RolePipeline, EventHung) {
		t.Fatalf("a slow start inside the child's own grace was taken for a hang: %+v", a)
	}

	strict := slowStart(RoleAPI)
	s2 := New([]Process{strict}, policy, WithLog(&syncBuffer{}))
	running(t, s2)
	waitFor(t, s2, "taken for a hang", func(sn Snapshot) bool { return hasEvent(sn, RoleAPI, EventHung) })
}

// A check that could not leave this machine (no local port to connect from) is not the
// child's failure: any number of them in a row leave it running.
func TestALocalProbeErrorIsNotAHang(t *testing.T) {
	local := &net.OpError{Op: "dial", Net: "tcp", Err: os.NewSyscallError("connect", syscall.EADDRNOTAVAIL)}
	if !localProbeError(local) || localProbeError(errSilent) || localProbeError(syscall.ECONNREFUSED) {
		t.Fatal("localProbeError: only EADDRNOTAVAIL is this machine's")
	}
	p, _ := fakeProc(t, RoleAPI, "serve")
	var checks atomic.Int32
	var noPorts atomic.Bool
	p.Check = func(context.Context) error {
		if noPorts.Load() {
			checks.Add(1)
			return local
		}
		return nil
	}
	s := New([]Process{p}, testPolicy(), WithLog(&syncBuffer{}))
	running(t, s)
	waitFor(t, s, "running", runningPID(RoleAPI))
	noPorts.Store(true)
	waitFor(t, s, "ten such checks", func(Snapshot) bool { return checks.Load() >= 10 })
	snap := s.Snapshot()
	if a := proc(snap, RoleAPI); a.Restarts != 0 || a.State != StateRunning || hasEvent(snap, RoleAPI, EventHung) {
		t.Fatalf("a check that failed on this machine's side was counted against the child: %+v", a)
	}
}

// Settle runs before each start, after BeforeStart, with the loop's context.
func TestSettleRunsBeforeEachStart(t *testing.T) {
	p, addr := fakeProc(t, RoleAPI, "serve")
	var order []string // written by the child's loop only, read once it runs
	var settled atomic.Int32
	p.BeforeStart = func() { order = append(order, "before") }
	p.Settle = func(ctx context.Context) {
		if ctx == nil || ctx.Err() != nil {
			t.Error("Settle without the loop's live context")
		}
		order = append(order, "settle")
		settled.Add(1)
	}
	s := New([]Process{p}, testPolicy(), WithLog(&syncBuffer{}))
	stop, _ := running(t, s)
	first := waitFor(t, s, "running", runningPID(RoleAPI))
	pid := proc(first, RoleAPI).PID
	get(t, "http://"+addr+"/crash")
	waitFor(t, s, "started again", func(sn Snapshot) bool {
		a := proc(sn, RoleAPI)
		return a.State == StateRunning && a.PID != 0 && a.PID != pid
	})
	if n := settled.Load(); n != 2 {
		t.Fatalf("Settle ran %d times, want once per start (2)", n)
	}
	_ = stop()
	want := []string{"before", "settle", "before", "settle"}
	if len(order) < 4 || order[0] != want[0] || order[1] != want[1] || order[2] != want[2] || order[3] != want[3] {
		t.Fatalf("order = %v, want %v", order, want)
	}
}

// The bundled postgres and a node's copy get the patient bounds: a check that waits for
// a busy postmaster, minutes of failed checks before a hang, a fast shutdown given time
// to checkpoint, and a start that waits for what a killed postmaster left.
func TestPostgresIsGivenPatientBounds(t *testing.T) {
	if PostgresProbeTimeout < 10*time.Second || PostgresHangAfter < 3*time.Minute || PostgresStopGrace <= restartGrace ||
		PostgresStopGrace <= HangGrace {
		t.Fatalf("bounds too tight: check %s, hang after %s, stop grace %s", PostgresProbeTimeout, PostgresHangAfter, PostgresStopGrace)
	}
	root := t.TempDir()
	b := pgbundle.Bundle{Home: root, Data: root + "/pg", Keys: root + "/keys", Backups: root + "/backups", Port: 1}
	p, err := BuildPostgres(b)
	if err != nil {
		t.Skipf("no postgres process is built here: %v", err)
	}
	if p.ProbeTimeout != PostgresProbeTimeout || p.HangAfter != PostgresHangAfter || p.StopGrace != PostgresStopGrace {
		t.Errorf("postgres: check %s, hang after %s, stop grace %s", p.ProbeTimeout, p.HangAfter, p.StopGrace)
	}
	if p.Settle == nil || p.BeforeStart == nil {
		t.Error("postgres starts without waiting for what the last one left")
	}
	if p.StopSignal != os.Interrupt {
		t.Errorf("postgres is asked to stop with %v, want its fast shutdown (SIGINT)", p.StopSignal)
	}
	if g := p.graceFor(restartGrace); g != PostgresStopGrace {
		t.Errorf("a restart gives its shutdown %s, want %s", g, PostgresStopGrace)
	}
	q := patient(Process{Role: RoleCopy})
	if q.ProbeTimeout != PostgresProbeTimeout || q.HangAfter != PostgresHangAfter || q.StopGrace != PostgresStopGrace {
		t.Errorf("patient: %+v", q)
	}
}

// The pipeline's Python workers (main, transcription, index) get PythonStartupGrace,
// longer than the policy's and apart from the bound of a running worker.
func TestPythonWorkersGetTheLongStartupGrace(t *testing.T) {
	if PythonStartupGrace <= DefaultPolicy().StartupGrace {
		t.Fatalf("PythonStartupGrace %s is not longer than the policy's %s", PythonStartupGrace, DefaultPolicy().StartupGrace)
	}
	var cfg config.Config
	cfg.Database.URL = "postgresql://local/ignored"
	cfg.Pipeline.Instances = 1
	if p := mainProcess(t, cfg); p.StartupGrace != PythonStartupGrace || p.HangAfter != 0 {
		t.Errorf("main worker: startup grace %v, hang after %v", p.StartupGrace, p.HangAfter)
	}
	procs, err := BuildPipelineInstances(Options{Config: cfg})
	if err != nil || len(procs) != 1 || procs[0].StartupGrace != PythonStartupGrace {
		t.Fatalf("transcription worker: %v, %+v", err, procs)
	}
	s := New(nil, DefaultPolicy())
	if g := s.startupGrace(Process{StartupGrace: PythonStartupGrace}); g != PythonStartupGrace {
		t.Errorf("a longer grace of the child: %s", g)
	}
	if g := s.startupGrace(Process{StartupGrace: time.Second}); g != DefaultPolicy().StartupGrace {
		t.Errorf("a shorter grace of the child: %s", g)
	}
}
