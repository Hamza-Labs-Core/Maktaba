package adopt

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"sort"
	"strings"
	"testing"
	"time"
)

// TestStopSparesTheAppAndItself: a user `setup --service` roots at
// %LOCALAPPDATA%\Maktaba, where the app is installed and keeps its state too.
// Its launcher and server stop; the app's own programs (the setup step doing
// the move, its pipeline venv) and this very process are never ended, even
// though they are in the old install's folder.
func TestStopSparesTheAppAndItself(t *testing.T) {
	fastStop(t)
	app := filepath.Join(t.TempDir(), "Local", "Maktaba")
	payload := filepath.Join(app, "server")
	launcher := filepath.Join(app, exeName("maktaba-launcher"))
	sys := newFakeSys(map[string]string{"MAKTABA_NODE_CONFIG": filepath.Join(t.TempDir(), "node.json")})
	name := `\Maktaba`
	sys.tasks = []Task{{Name: name, Command: launcher, Args: `--root "` + app + `" serve`, Enabled: true}}
	sys.xml[name] = []byte("<Task/>")
	server := exeName("maktaba-server")
	sys.procs = []Proc{
		{PID: 300, Name: exeName("maktaba-launcher"), Exe: launcher, Cmdline: "maktaba-launcher --root " + app + " serve"},
		{PID: 301, Name: server, Exe: filepath.Join(app, "bin", "releases", "1.0.0", server), Cmdline: server + " serve"},
		// the app's setup step, moving it
		{PID: 200, Name: server, Exe: filepath.Join(payload, "bin", server), Cmdline: server + " setup --json --step adopt"},
		// this process, wherever its program is
		{PID: os.Getpid(), Name: server, Exe: filepath.Join(app, "bin", server), Cmdline: server + " setup --json --step adopt"},
		// the app's pipeline venv
		{PID: 400, Name: "python.exe", Exe: filepath.Join(app, "venv", "Scripts", "python.exe"), Cmdline: "python -m maktaba_pipeline worker"},
	}
	for _, p := range sys.procs {
		sys.stubborn[p.PID] = true // none ends with the task: the grace runs out
		sys.taskOf[p.PID] = name
	}
	tgt := Target{ConfigPath: filepath.Join(app, "server.toml"), StateDir: app, KeysDir: filepath.Join(app, "keys"),
		NodeConfig: filepath.Join(app, "node.json"), AppPayload: payload}

	srcs, err := Find(sys, payload)
	if err != nil || len(srcs) != 1 || srcs[0].Kind != KindLauncher || !samePath(srcs[0].Dir, app) {
		t.Fatalf("find: %v %+v", err, srcs)
	}
	if slices.Contains(srcs[0].PIDs, 200) || slices.Contains(srcs[0].PIDs, os.Getpid()) {
		t.Errorf("the app's own programs counted as the old install's: %v", srcs[0].PIDs)
	}
	if err := stopSource(context.Background(), sys, srcs[0], tgt.self()); err != nil {
		t.Fatal(err)
	}
	sort.Strings(sys.killed)
	if want := []string{"300", "301"}; !reflect.DeepEqual(sys.killed, want) {
		t.Errorf("killed %q, want only the old launcher and its server %q", sys.killed, want)
	}
	for _, pid := range []int{200, os.Getpid(), 400} {
		if !sys.alive(pid) {
			t.Errorf("process %d of the app was ended", pid)
		}
	}
}

// loopingPC is this PC with its script's restart loop, a cmd.exe started by
// hand (so ending the task does not end it): while it lives, the server comes
// back once it is gone, as `timeout /t 10` and `goto run` bring it back. A
// hidden loop (no command line to know it by) does the same.
func loopingPC(t *testing.T, hidden bool) (*thisPC, *int) {
	t.Helper()
	pc := newThisPC(t)
	script := filepath.Join(pc.dir, "start-maktaba-node.cmd")
	cmdline := `C:\Windows\system32\cmd.exe /c ""` + script + `" "`
	if hidden {
		cmdline = ""
	}
	pc.sys.procs = append(pc.sys.procs, Proc{PID: 90, Name: "cmd.exe", Exe: `C:\Windows\System32\cmd.exe`, Cmdline: cmdline})
	server := filepath.Join(pc.dir, "bin", exeName("maktaba-server"))
	next, back := 500, 0
	var gone time.Time
	pc.sys.onList = func(f *fakeSys) {
		running := false
		for _, p := range f.procs {
			running = running || samePath(p.Exe, server)
		}
		switch {
		case running || !f.alive(90):
			gone = time.Time{}
		case gone.IsZero():
			gone = time.Now()
		case time.Since(gone) > 20*time.Millisecond:
			f.procs = append(f.procs, Proc{PID: next, Name: exeName("maktaba-server"), Exe: server, Cmdline: "maktaba-server.exe serve"})
			next, back, gone = next+1, back+1, time.Time{}
		}
	}
	return pc, &back
}

// TestStopEndsTheRestartLoop: the cmd.exe running the script is ended (alone,
// not its tree), so the server does not come back after the stop.
func TestStopEndsTheRestartLoop(t *testing.T) {
	fastStop(t)
	pc, back := loopingPC(t, false)
	srcs, _ := Find(pc.sys, "")
	if !srcs[0].Loops || srcs[0].RestartDelay != 10*time.Second {
		t.Fatalf("the script's loop: %v, every %s", srcs[0].Loops, srcs[0].RestartDelay)
	}
	if err := stopSource(context.Background(), pc.sys, srcs[0], pc.tgt.self()); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(pc.sys.endedProcs, []int{90}) {
		t.Errorf("ended %v, want the script's cmd.exe alone", pc.sys.endedProcs)
	}
	// a while on: nothing comes back
	for deadline := time.Now().Add(80 * time.Millisecond); time.Now().Before(deadline); time.Sleep(5 * time.Millisecond) {
		_, _ = pc.sys.Processes()
	}
	if *back != 0 {
		t.Errorf("the server came back %d times", *back)
	}
}

// TestStopCatchesALoopItCannotSee: a loop the stop cannot tell (no command
// line) brings the server back while the stop watches the quiet install for
// longer than its delay: the move stops, rather than count it stopped.
func TestStopCatchesALoopItCannotSee(t *testing.T) {
	fastStop(t)
	stopGrace, stopWait = 20*time.Millisecond, 200*time.Millisecond
	settleFor = func(Source) time.Duration { return 60 * time.Millisecond }
	pc, back := loopingPC(t, true)
	srcs, _ := Find(pc.sys, "")
	err := stopSource(context.Background(), pc.sys, srcs[0], pc.tgt.self())
	if ae := codeOf(err); ae == nil || ae.Code != "adopt_still_running" {
		t.Fatalf("a hidden loop: %v", err)
	}
	if *back == 0 {
		t.Error("the loop never brought the server back: the test proves nothing")
	}
	if len(pc.sys.endedProcs) != 0 {
		t.Errorf("ended %v: a console it cannot tell is not ended", pc.sys.endedProcs)
	}
}

// TestTheSettleOutlastsTheLoop: the stop watches a quiet install for longer
// than its script's restart delay (this PC's waits 10 s).
func TestTheSettleOutlastsTheLoop(t *testing.T) {
	s, ok := ParseScript(filepath.Join(t.TempDir(), "start-maktaba-node.cmd"), thisPCScript, func(string) string { return "" })
	if !ok || !s.Loops || s.RestartDelay != 10*time.Second {
		t.Fatalf("this PC's script: loops %v every %s", s.Loops, s.RestartDelay)
	}
	if got := loopSettle(Source{Loops: true, RestartDelay: s.RestartDelay}); got <= s.RestartDelay {
		t.Errorf("settle %s, not beyond the loop's %s", got, s.RestartDelay)
	}
	for text, want := range map[string]time.Duration{
		":again\r\nmaktaba-server.exe serve\r\nping -n 6 127.0.0.1 >nul\r\ngoto again\r\n":   5 * time.Second,
		":again\r\nmaktaba-server.exe serve\r\nchoice /c x /t 7 /d x >nul\r\ngoto again\r\n": 7 * time.Second,
		"maktaba-server.exe serve\r\ntimeout /t 30\r\n":                                      0, // no loop: no delay to outlast
	} {
		s, ok := ParseScript(`C:\x\run.cmd`, text, func(string) string { return "" })
		if !ok || s.RestartDelay != want || s.Loops != (want > 0) {
			t.Errorf("%q: loops %v every %s, want %s", text, s.Loops, s.RestartDelay, want)
		}
	}
	if got := loopSettle(Source{Loops: true}); got <= stopSettle {
		t.Errorf("a loop with no delay it names: settle %s", got)
	}
}

// TestAProcessListItCannotReadStops: not sure what runs is not "nothing runs":
// Find fails, the stop fails (its tasks off, so Undo can turn them on again),
// and Undo will not guess either.
func TestAProcessListItCannotReadStops(t *testing.T) {
	fastStop(t)
	pc := newThisPC(t)
	srcs, _ := Find(pc.sys, "")
	plan, _ := MakePlan(pc.sys, srcs[0], pc.tgt)
	pc.sys.procErr = errors.New("the CIM query failed")
	if _, err := Find(pc.sys, ""); err == nil || !strings.Contains(err.Error(), "CIM") {
		t.Errorf("find without the process list: %v", err)
	}
	_, err := Move(context.Background(), pc.sys, plan, pc.tgt, nil, nil)
	if ae := codeOf(err); ae == nil || ae.Code != "adopt_processes_unknown" {
		t.Fatalf("move without the process list: %v", err)
	}
	name := srcs[0].Task
	rec, _, _ := ReadRecord(pc.tgt.StateDir)
	if rec.Stage != StageFound || len(rec.Saved) != 1 || pc.sys.enabled[name] {
		t.Fatalf("stage %s saved %+v enabled %v", rec.Stage, rec.Saved, pc.sys.enabled[name])
	}
	if err := Undo(pc.sys, pc.tgt, nil); codeOf(err) == nil || codeOf(err).Code != "adopt_processes_unknown" {
		t.Errorf("undo without the process list: %v", err)
	}
	if len(pc.sys.run) != 0 {
		t.Errorf("run %q on a guess", pc.sys.run)
	}
}

// TestUndoAMoveThatStoppedPartWay: Undo turns the old tasks on again from the
// record of a move that failed (a stop that failed, a copy that failed), and
// runs them only when nothing of the old install still runs.
func TestUndoAMoveThatStoppedPartWay(t *testing.T) {
	fastStop(t)
	stopGrace, stopWait = 20*time.Millisecond, 80*time.Millisecond
	pc := newThisPC(t)
	name := `\Maktaba node (maktaba-local)`
	srcs, _ := Find(pc.sys, "")
	plan, _ := MakePlan(pc.sys, srcs[0], pc.tgt)
	// the old server survives every kill: the stop fails, its task already off
	stuck := &undying{fakeSys: pc.sys, pid: 100}
	pc.sys.stubborn[100] = true
	if _, err := Move(context.Background(), stuck, plan, pc.tgt, nil, nil); codeOf(err) == nil || codeOf(err).Code != "adopt_still_running" {
		t.Fatalf("a stop that fails: %v", err)
	}
	if rec, _, _ := ReadRecord(pc.tgt.StateDir); rec.Stage != StageFound || len(rec.Saved) != 1 || pc.sys.enabled[name] {
		t.Fatalf("after the failed stop: stage %s, saved %+v, enabled %v", rec.Stage, rec.Saved, pc.sys.enabled[name])
	}
	if err := Undo(stuck, pc.tgt, nil); err != nil {
		t.Fatal(err)
	}
	if !pc.sys.enabled[name] || len(pc.sys.run) != 0 {
		t.Errorf("enabled %v run %q: on again, not run beside the one that still runs", pc.sys.enabled[name], pc.sys.run)
	}
	if !UndoneHere(pc.tgt.StateDir) {
		t.Error("not recorded as undone")
	}

	// a copy that fails after the stop: on again, and run
	pc = newThisPC(t)
	write(t, filepath.Join(pc.tgt.StateDir, "cache"), "in the way")
	srcs, _ = Find(pc.sys, "")
	plan, _ = MakePlan(pc.sys, srcs[0], pc.tgt)
	if _, err := Move(context.Background(), pc.sys, plan, pc.tgt, nil, nil); err == nil {
		t.Fatal("the copy went through a file")
	}
	if err := Undo(pc.sys, pc.tgt, nil); err != nil {
		t.Fatal(err)
	}
	if !pc.sys.enabled[name] || !reflect.DeepEqual(pc.sys.run, []string{name}) {
		t.Errorf("enabled %v run %q", pc.sys.enabled[name], pc.sys.run)
	}

	// nothing recorded: nothing to undo, said by its code
	if err := Undo(newFakeSys(nil), Target{StateDir: t.TempDir()}, nil); codeOf(err) == nil || codeOf(err).Code != "adopt_no_move" {
		t.Errorf("no move: %v", err)
	}
}

// TestAServiceInstallIsFoundAndRefused: a setup --service --system install
// runs as the system (its processes have no path the user can read): the
// service list shows it, and its plan refuses it before anything changes.
func TestAServiceInstallIsFoundAndRefused(t *testing.T) {
	fastStop(t)
	pc := newThisPC(t)
	pc.sys.services = []Service{
		{Name: "Maktaba", PathName: `"C:\ProgramData\Maktaba\maktaba-launcher.exe" --root "C:\ProgramData\Maktaba" serve`, State: "Running"},
		{Name: "Spooler", PathName: `C:\Windows\System32\spoolsv.exe`, State: "Running"},
	}
	pc.sys.procs = append(pc.sys.procs, Proc{PID: 700, Name: "maktaba-server.exe"}) // the system's: no path
	srcs, err := Find(pc.sys, "")
	if err != nil {
		t.Fatal(err)
	}
	var svc *Source
	for i := range srcs {
		if srcs[i].Kind == KindService {
			svc = &srcs[i]
		}
	}
	if len(srcs) != 2 || svc == nil || svc.ID != "service:Maktaba" || svc.Service != "Maktaba" ||
		!strings.HasSuffix(filepath.ToSlash(svc.Dir), "ProgramData/Maktaba") {
		t.Fatalf("sources %+v", srcs)
	}
	plan, err := MakePlan(pc.sys, *svc, pc.tgt)
	if err != nil || len(plan.Problems) != 1 || plan.Problems[0].Code != "service_install" || plan.Problems[0].Params["name"] != "Maktaba" {
		t.Fatalf("plan: %v %+v", err, plan.Problems)
	}
	if _, err := Move(context.Background(), pc.sys, plan, pc.tgt, nil, nil); err == nil || len(pc.sys.ended) != 0 || len(pc.sys.killed) != 0 {
		t.Errorf("a service moved: %v (ended %q, killed %q)", err, pc.sys.ended, pc.sys.killed)
	}
}
