package supervisor

import (
	"context"
	"io"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"testing"
	"time"

	"github.com/Hamza-Labs-Core/Maktaba/shared/go/layout"
)

// handoverSupervisorMain is a supervisor in a process of its own (the test binary with
// MAKTABA_FAKE_CHILD=handover-supervisor): a TV slot that survives (a "serve" fake child
// at HO_ADDR) and an api that does not, handed over once both run, then exit 75 as
// serve does. HO_DIR is the record's directory, HO_LAUNCHER the launcher's pid (the
// test: it holds the boot's job on Windows).
func handoverSupervisorMain() {
	dir, addr, gc := os.Getenv("HO_DIR"), os.Getenv("HO_ADDR"), os.Getenv("HO_GRANDCHILD")
	launcher, _ := strconv.Atoi(os.Getenv("HO_LAUNCHER"))
	child := func(mode string, env ...string) *exec.Cmd {
		cmd := exec.Command(os.Args[0], "-test.run=^$")
		cmd.Env = append(append(os.Environ(), "MAKTABA_FAKE_CHILD="+mode, "HO_DIR=", "HO_ADDR=", "HO_GRANDCHILD=",
			"HO_PASSFILE="), env...)
		return cmd
	}
	other := freeAddrOrDie()
	slotEnv := []string{"FAKE_ADDR=" + addr, "FAKE_GRANDCHILD_PIDFILE=" + gc} // its ffmpeg
	if pf := os.Getenv("HO_PASSFILE"); pf != "" {
		// a database login, its password in a passfile (as dsn.ProcessEnv gives one)
		slotEnv = append(slotEnv, "DATABASE_URL=postgres://maktaba_node_x@home:5432/maktaba", "PGPASSFILE="+pf)
	}
	procs := []Process{
		{Role: RoleTVSlot, Instance: "blue", Survives: true,
			Cmd:   child("serve", slotEnv...),
			Probe: "http://" + addr + "/healthz"},
		{Role: RoleAPI, Cmd: child("serve", "FAKE_ADDR="+other), Probe: "http://" + other + "/healthz"},
	}
	s := New(procs, testPolicy(), WithLog(os.Stderr), WithHandover(dir, launcher))
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- s.Run(ctx, 5*time.Second) }()
	deadline := time.Now().Add(20 * time.Second)
	for {
		snap := s.Snapshot()
		up := 0
		for _, p := range snap.Processes {
			if p.State == StateRunning {
				up++
			}
		}
		if up == 2 {
			break
		}
		if time.Now().After(deadline) {
			os.Exit(96)
		}
		time.Sleep(20 * time.Millisecond)
	}
	if ok, why := s.HandOver(); !ok {
		_, _ = os.Stderr.WriteString("handover refused: " + why + "\n")
		os.Exit(97)
	}
	cancel()
	<-done
	os.Exit(75)
}

func freeAddrOrDie() string {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		os.Exit(95)
	}
	addr := ln.Addr().String()
	_ = ln.Close()
	return addr
}

func healthy(url string) bool {
	c := &http.Client{Timeout: time.Second}
	resp, err := c.Get(url)
	if err != nil {
		return false
	}
	_ = resp.Body.Close()
	return resp.StatusCode == http.StatusOK
}

// Story 35.7 TC1, TC2, TC4: a supervisor that hands over and exits (75) leaves its TV
// slot running and stops the rest, with the record naming the slot; the next supervisor
// adopts it (the same pid, no fresh start), replaces it with an ordinary child when it
// dies, and stops what it adopted at its own shutdown. Here under a launcher that speaks
// the handover (it keeps the job the server runs in across it, on Windows).
func TestHandoverSurvivesTheSupervisorsExit(t *testing.T) {
	t.Setenv(layout.EnvLauncherHandover, "1")
	handoverScenario(t, true)
}

// On Windows, a slot whose own job nobody kept (no launcher held it when the supervisor
// before exited): the adoption makes a job anew around it and its ffmpeg.
func TestHandoverRebuildsALostJob(t *testing.T) {
	if runtime.GOOS != "windows" {
		t.Skip("POSIX keeps a process group, never a job")
	}
	t.Setenv(layout.EnvLauncherHandover, "1")
	handoverScenario(t, false)
}

// The same under a Windows launcher from before the handover: the slot leaves the
// launcher's job for the boot's, whose handle the "launcher" (this test) holds.
func TestHandoverInTheBootJob(t *testing.T) {
	if !bootJobPossible() {
		t.Skip("the job this test runs in lets no child leave it (and POSIX has no boot job)")
	}
	t.Setenv(layout.EnvLauncherHandover, "")
	handoverScenario(t, true)
}

// handoverScenario: held, the "launcher" (this test) holds the jobs the supervisor before
// made (it is its launcher), else nobody does.
func handoverScenario(t *testing.T, held bool) {
	if testing.Short() {
		t.Skip("spawns child processes; runs without -short")
	}
	dir := t.TempDir()
	addr := freeAddr(t)
	probe := "http://" + addr + "/healthz"
	gcFile := filepath.Join(t.TempDir(), "ffmpeg.pid")
	helper := exec.Command(os.Args[0], "-test.run=^$")
	holder := 0
	if held {
		holder = os.Getpid()
	}
	helper.Env = append(os.Environ(), "MAKTABA_FAKE_CHILD=handover-supervisor", "HO_DIR="+dir, "HO_ADDR="+addr,
		"HO_LAUNCHER="+strconv.Itoa(holder), "HO_GRANDCHILD="+gcFile)
	helper.Stdout, helper.Stderr = io.Discard, os.Stderr
	err := helper.Run()
	if code := helper.ProcessState.ExitCode(); code != 75 {
		t.Fatalf("the supervisor exited %d (%v)", code, err)
	}
	rec, err := layout.ReadHandover(dir)
	if err != nil || rec == nil || !rec.HandedOverBy(helper.Process.Pid) || len(rec.Children) != 1 {
		t.Fatalf("record %+v, %v", rec, err)
	}
	slot := rec.Children[0]
	t.Cleanup(func() {
		if p, err := os.FindProcess(slot.PID); err == nil && layout.ProcessIs(slot.PID, slot.Start) {
			_ = p.Kill()
		}
	})
	if slot.Role != string(RoleTVSlot) || slot.Instance != "blue" || !layout.ProcessIs(slot.PID, slot.Start) {
		t.Fatalf("the slot did not outlive its supervisor: %+v", slot)
	}
	if !healthy(probe) {
		t.Fatal("the slot does not answer after the supervisor exited")
	}
	// the slot's own child (its ffmpeg) lives on with it
	gcPID := waitPIDFile(t, gcFile)
	gcStart, err := layout.ProcessStart(gcPID)
	if err != nil {
		t.Fatalf("the slot's ffmpeg did not outlive the supervisor: %v", err)
	}
	t.Cleanup(func() {
		if p, err := os.FindProcess(gcPID); err == nil && layout.ProcessIs(gcPID, gcStart) {
			_ = p.Kill()
		}
	})

	// the next supervisor: the same slot (its address), adopted
	cmd := exec.Command(os.Args[0], "-test.run=^$")
	cmd.Env = append(os.Environ(), "MAKTABA_FAKE_CHILD=serve", "FAKE_ADDR="+addr)
	next := New([]Process{{Role: RoleTVSlot, Instance: "blue", Survives: true, Cmd: cmd, Probe: probe}}, testPolicy(),
		WithLog(io.Discard), WithHandover(dir, os.Getpid()))
	stop, _ := running(t, next)
	snap := waitFor(t, next, "the slot adopted", func(s Snapshot) bool {
		p := proc(s, RoleTVSlot)
		return p.Adopted && p.State == StateRunning
	})
	if p := proc(snap, RoleTVSlot); p.PID != slot.PID || p.Restarts != 0 {
		t.Fatalf("adopted %+v, want pid %d", p, slot.PID)
	}
	if next.HandedOver() {
		t.Fatal("a handover it did not make")
	}
	if !layout.ProcessIs(gcPID, gcStart) {
		t.Fatal("the adoption ended the slot's ffmpeg")
	}
	// it dies: an ordinary child takes its place, and the record follows
	get(t, "http://"+addr+"/crash?code=3")
	snap = waitFor(t, next, "a fresh slot", func(s Snapshot) bool {
		p := proc(s, RoleTVSlot)
		return p.State == StateRunning && p.PID != 0 && p.PID != slot.PID && !p.Adopted
	})
	fresh := proc(snap, RoleTVSlot).PID
	// what the adopted slot started ended with it (its job, its process group)
	for deadline := time.Now().Add(10 * time.Second); layout.ProcessIs(gcPID, gcStart); time.Sleep(20 * time.Millisecond) {
		if time.Now().After(deadline) {
			t.Fatal("the adopted slot's ffmpeg runs on as an orphan after the slot ended")
		}
	}
	deadline := time.Now().Add(5 * time.Second)
	for {
		r, _ := layout.ReadHandover(dir)
		if r != nil && len(r.Children) == 1 && r.Children[0].PID == fresh && !r.Handover {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("the record does not name the fresh slot %d: %+v", fresh, r)
		}
		time.Sleep(20 * time.Millisecond)
	}
	if err := stop(); err != nil {
		t.Fatal(err)
	}
	if r, _ := layout.ReadHandover(dir); r != nil {
		t.Fatalf("a record after every process stopped: %+v", r)
	}
	if healthy(probe) {
		t.Fatal("the slot runs on after its supervisor stopped")
	}
}

// Story 35.7 TC2: an entry whose pid is another process now (its start time differs) is
// not adopted, and that process is left alone: a fresh one starts.
func TestAdoptRejectsReusedPid(t *testing.T) {
	p, addr := fakeProc(t, RoleTVSlot, "serve")
	p.Instance, p.Survives = "blue", true
	dir := t.TempDir()
	start, err := layout.ProcessStart(os.Getpid())
	if err != nil {
		t.Skip("no process identity here")
	}
	// this test process stands in for the reused pid: it must not be touched
	rec := layout.Handover{BootID: layout.BootID(), Writer: 1, Handover: true, WrittenAt: time.Now(),
		Children: []layout.HandoverChild{{Role: string(RoleTVSlot), Instance: "blue", PID: os.Getpid(), Start: start + 1,
			Probe: "http://" + addr + "/healthz"}}}
	if err := layout.WriteHandover(dir, rec); err != nil {
		t.Fatal(err)
	}
	s := New([]Process{p}, testPolicy(), WithLog(io.Discard), WithHandover(dir, os.Getpid()))
	running(t, s)
	snap := waitFor(t, s, "a fresh slot", func(s Snapshot) bool {
		c := proc(s, RoleTVSlot)
		return c.State == StateRunning && c.PID != os.Getpid()
	})
	if c := proc(snap, RoleTVSlot); c.Adopted {
		t.Fatalf("adopted a reused pid: %+v", c)
	}
	found := false
	for _, e := range snap.Events {
		if e.Kind == EventAdoptFailed && e.PID == os.Getpid() {
			found = true
		}
	}
	if !found {
		t.Fatalf("no %s event: %+v", EventAdoptFailed, snap.Events)
	}
}

// A TV process handed over with a database login that is not its successor's (the home
// re-made the machine's access: a new password in the passfile) is not adopted: it would
// be refused by the home's database every few seconds for as long as it ran. It is ended
// and a fresh one starts with the login of now. The same login is adopted as ever.
func TestHandoverRestartsAStaleLogin(t *testing.T) {
	if testing.Short() {
		t.Skip("spawns child processes; runs without -short")
	}
	t.Setenv(layout.EnvLauncherHandover, "1")
	for _, remade := range []bool{false, true} {
		dir := t.TempDir()
		addr := freeAddr(t)
		probe := "http://" + addr + "/healthz"
		pf := filepath.Join(t.TempDir(), "pgpass-maktaba_node_x")
		if err := os.WriteFile(pf, []byte("home:5432:maktaba:maktaba_node_x:first-password\n"), 0o600); err != nil {
			t.Fatal(err)
		}
		helper := exec.Command(os.Args[0], "-test.run=^$")
		helper.Env = append(os.Environ(), "MAKTABA_FAKE_CHILD=handover-supervisor", "HO_DIR="+dir, "HO_ADDR="+addr,
			"HO_LAUNCHER="+strconv.Itoa(os.Getpid()), "HO_GRANDCHILD="+filepath.Join(t.TempDir(), "ffmpeg.pid"),
			"HO_PASSFILE="+pf)
		helper.Stdout, helper.Stderr = io.Discard, os.Stderr
		err := helper.Run()
		if code := helper.ProcessState.ExitCode(); code != 75 {
			t.Fatalf("the supervisor exited %d (%v)", code, err)
		}
		rec, err := layout.ReadHandover(dir)
		if err != nil || rec == nil || len(rec.Children) != 1 || rec.Children[0].Login == "" {
			t.Fatalf("record %+v, %v: want the slot with its login", rec, err)
		}
		slot := rec.Children[0]
		t.Cleanup(func() {
			if p, err := os.FindProcess(slot.PID); err == nil && layout.ProcessIs(slot.PID, slot.Start) {
				_ = p.Kill()
			}
		})
		if remade {
			if err := os.WriteFile(pf, []byte("home:5432:maktaba:maktaba_node_x:re-made-password\n"), 0o600); err != nil {
				t.Fatal(err)
			}
		}
		cmd := exec.Command(os.Args[0], "-test.run=^$")
		cmd.Env = append(os.Environ(), "MAKTABA_FAKE_CHILD=serve", "FAKE_ADDR="+addr,
			"DATABASE_URL=postgres://maktaba_node_x@home:5432/maktaba", "PGPASSFILE="+pf)
		next := New([]Process{{Role: RoleTVSlot, Instance: "blue", Survives: true, Cmd: cmd, Probe: probe}}, testPolicy(),
			WithLog(io.Discard), WithHandover(dir, os.Getpid()))
		stop, _ := running(t, next)
		snap := waitFor(t, next, "the slot running", func(s Snapshot) bool {
			p := proc(s, RoleTVSlot)
			return p.State == StateRunning && p.PID != 0 && (p.Adopted || p.PID != slot.PID)
		})
		p := proc(snap, RoleTVSlot)
		switch {
		case !remade && (!p.Adopted || p.PID != slot.PID):
			t.Fatalf("the same login: %+v, want pid %d adopted", p, slot.PID)
		case remade && (p.Adopted || p.PID == slot.PID):
			t.Fatalf("a re-made login: %+v, want a fresh slot", p)
		}
		if remade {
			for deadline := time.Now().Add(10 * time.Second); layout.ProcessIs(slot.PID, slot.Start); time.Sleep(20 * time.Millisecond) {
				if time.Now().After(deadline) {
					t.Fatal("the slot with the stale login runs on")
				}
			}
		}
		if err := stop(); err != nil {
			t.Fatal(err)
		}
	}
}

// Story 35.7 EC3: a record of an earlier boot is ignored and removed.
func TestHandoverOfAnEarlierBootIgnored(t *testing.T) {
	p, _ := fakeProc(t, RoleTVSlot, "serve")
	p.Instance, p.Survives = "blue", true
	dir := t.TempDir()
	rec := layout.Handover{BootID: "an-earlier-boot", Writer: 1, Handover: true, WrittenAt: time.Now(),
		Children: []layout.HandoverChild{{Role: string(RoleTVSlot), Instance: "blue", PID: os.Getpid(), Start: 1}}}
	if err := layout.WriteHandover(dir, rec); err != nil {
		t.Fatal(err)
	}
	s := New([]Process{p}, testPolicy(), WithLog(io.Discard), WithHandover(dir, os.Getpid()))
	running(t, s)
	waitFor(t, s, "the slot", func(s Snapshot) bool {
		c := proc(s, RoleTVSlot)
		return c.State == StateRunning && !c.Adopted
	})
	r, err := layout.ReadHandover(dir)
	if err != nil || r == nil || r.BootID == "an-earlier-boot" {
		t.Fatalf("the record of the earlier boot was kept: %+v, %v", r, err)
	}
}

// A process that does not survive, or a supervisor that keeps no record, never hands
// over; without a launcher holding the TV job (Windows) neither does a survivor.
func TestCanHandOver(t *testing.T) {
	s := New([]Process{{Role: RoleAPI}}, testPolicy(), WithLog(io.Discard))
	if ok, _ := s.CanHandOver(); ok {
		t.Fatal("no record kept, yet a handover")
	}
	if ok, why := s.HandOver(); ok || why == "" {
		t.Fatalf("HandOver without a record: %v %q", ok, why)
	}
	if releaseOfPath(filepath.Join("x", "bin", "releases", "1.4.0", "maktaba-streaming")) != "1.4.0" ||
		releaseOfPath(filepath.Join("x", "bin", "maktaba-streaming")) != "" {
		t.Fatal("releaseOf")
	}
}

// waitPIDFile reads the pid a fake child writes to file once it runs.
func waitPIDFile(t *testing.T, file string) int {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for {
		if b, err := os.ReadFile(file); err == nil {
			if pid, err := strconv.Atoi(string(b)); err == nil {
				return pid
			}
		}
		if time.Now().After(deadline) {
			t.Fatalf("no pid in %s", file)
		}
		time.Sleep(20 * time.Millisecond)
	}
}

// Story 35.7 (review L9): a supervisor that never gets ready (its first process never
// runs) leaves the record of the TV processes it never adopted, for the launcher to stop
// them or the next start to adopt them.
func TestUnadoptedRecordStays(t *testing.T) {
	// the first process never answers its check, so the others (the slot's adoption) never
	// start: a "crash" child with no check counted as running at once, and whether the
	// adoption began before the stop was a race (on Windows a failed adoption ends the
	// process and drops it from the record)
	first, _ := fakeProc(t, RolePostgres, "sleep")
	first.First = true
	first.Probe = "http://" + freeAddr(t) + "/healthz"
	slot, _ := fakeProc(t, RoleTVSlot, "serve")
	slot.Instance, slot.Survives = "blue", true
	dir := t.TempDir()
	// the record names a live process of its own, never this test's: were it adopted, the
	// stop would kill it (on Windows the test binary went with no word)
	held := exec.Command(os.Args[0], "-test.run=^$")
	held.Env = append(os.Environ(), "MAKTABA_FAKE_CHILD=sleep")
	if err := held.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = held.Process.Kill(); _ = held.Wait() })
	start, err := layout.ProcessStart(held.Process.Pid)
	if err != nil {
		t.Skip("no process identity here")
	}
	rec := layout.Handover{BootID: layout.BootID(), Writer: 1, Handover: true, WrittenAt: time.Now(),
		Children: []layout.HandoverChild{{Role: string(RoleTVSlot), Instance: "blue", PID: held.Process.Pid, Start: start}}}
	if err := layout.WriteHandover(dir, rec); err != nil {
		t.Fatal(err)
	}
	s := New([]Process{first, slot}, testPolicy(), WithLog(io.Discard), WithHandover(dir, os.Getpid()))
	stop, _ := running(t, s)
	time.Sleep(200 * time.Millisecond)
	if err := stop(); err != nil {
		t.Fatal(err)
	}
	if r, _ := layout.ReadHandover(dir); r == nil || len(r.Children) != 1 {
		t.Fatalf("the record of a TV process never adopted went: %+v", r)
	}
}
