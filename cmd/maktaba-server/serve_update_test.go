package main

import (
	"context"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/Hamza-Labs-Core/Maktaba/cmd/maktaba-server/internal/supervisor"
)

// The watch checks that the pipeline runs the new release's pipeline (Epic 35 story 35.4
// AC7, review A12): a plain server's pipeline runs <release>/venv, and its /healthz says
// the version of the release's wheel, in PEP 440's spelling.
func TestMachineHealthChecksThePipelineVersion(t *testing.T) {
	t.Setenv("MAKTABA_PYTHON", "")
	t.Setenv("MAKTABA_APP_BUNDLE", "")
	release := t.TempDir()
	py := filepath.Join(release, "venv", "bin", "python")
	if runtime.GOOS == "windows" {
		py = filepath.Join(release, "venv", "Scripts", "python.exe")
	}
	for _, f := range []string{py, filepath.Join(release, "pipeline", "maktaba_pipeline-1.5.0rc1-py3-none-any.whl")} {
		if err := os.MkdirAll(filepath.Dir(f), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(f, nil, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	status, body := http.StatusOK, ""
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(status)
		_, _ = w.Write([]byte(body))
	}))
	defer srv.Close()
	snap := supervisor.Snapshot{Processes: []supervisor.ChildStatus{{Role: supervisor.RolePipeline,
		State: supervisor.StateRunning, ProbeURL: srv.URL + "/healthz"}}}
	h := &machineHealth{releaseDir: release, snap: func() (supervisor.Snapshot, bool) { return snap, true }}

	for _, c := range []struct {
		name           string
		status         int
		body           string
		healthy, fatal bool
	}{
		{"the release's pipeline", 200, `{"status":"ok","version":"1.5.0rc1"}`, true, false},
		{"the old environment's", 200, `{"status":"ok","version":"1.4.0"}`, false, true},
		{"a pipeline from before it said its version", 200, `{"status":"ok","service":"pipeline"}`, true, false},
		{"a stale loop", 503, `{"status":"fail","version":"1.5.0rc1"}`, false, false},
	} {
		status, body = c.status, c.body
		v := h.Check(context.Background(), "v1.5.0-rc.1")
		if v.Healthy != c.healthy || v.Fatal != c.fatal {
			t.Errorf("%s: %+v, want healthy %v fatal %v", c.name, v, c.healthy, c.fatal)
		}
	}

	// a pipeline under MAKTABA_PYTHON runs another environment: its version is not the
	// release's to check
	t.Setenv("MAKTABA_PYTHON", py)
	status, body = 200, `{"status":"ok","version":"0.0.1"}`
	if v := h.Check(context.Background(), "v1.5.0-rc.1"); !v.Healthy {
		t.Fatalf("under MAKTABA_PYTHON: %+v", v)
	}
}

// Review round 3: the start step's progress is every waiting process's detail (whichever
// runs the step), and a migration's statement or a dump put back is unmeasured (no stall
// limit: only the watch's cap bounds it).
func TestMachineHealthStartSteps(t *testing.T) {
	procs := []supervisor.ChildStatus{
		{Role: supervisor.RoleStreaming, State: supervisor.StateStarting, DetailCode: "db_waiting", Detail: "Waiting for the library database"},
		{Role: supervisor.RoleAPI, State: supervisor.StateStarting, DetailCode: "db_dumping", Detail: "Saving (100 MB so far)"},
	}
	h := &machineHealth{snap: func() (supervisor.Snapshot, bool) { return supervisor.Snapshot{Processes: procs}, true }}
	v := h.Check(context.Background(), "v1.5.0")
	if !v.Pending || v.Unmeasured || !strings.Contains(v.Progress, "100 MB so far") {
		t.Fatalf("dumping: %+v", v)
	}
	procs[1].DetailCode, procs[1].Detail = "db_migrating", "Updating the library database for v1.5.0 (2 migrations done)"
	if v := h.Check(context.Background(), "v1.5.0"); !v.Pending || !v.Unmeasured {
		t.Fatalf("migrating: %+v", v)
	}
}

// The home's watch judges the home's own processes: a TV slot (its change's staging slot
// starting, a slot its swap stopped, one that crash-loops) is the TV step's, never the
// home's verdict (2026-10-28: "tv-slot/amber is starting" counted against the watch).
func TestMachineHealthLeavesTheTVSlotsToTheTVStep(t *testing.T) {
	t.Setenv("MAKTABA_APP_BUNDLE", "")
	procs := []supervisor.ChildStatus{
		{Role: supervisor.RoleStreaming, State: supervisor.StateRunning},
		{Role: supervisor.RoleTVSlot, Instance: "amber", Wanted: true, State: supervisor.StateStarting},
		{Role: supervisor.RoleTVSlot, Instance: "blue", Wanted: true, State: supervisor.StateBackoff, CrashLoop: true},
	}
	changing := true // the release's TV change under way: amber stages it, blue's crash its check's
	h := &machineHealth{snap: func() (supervisor.Snapshot, bool) { return supervisor.Snapshot{Processes: procs}, true },
		tvChanging: func() bool { return changing }}
	if v := h.Check(context.Background(), "v1.5.0"); !v.Healthy || v.Fatal {
		t.Fatalf("the TV's slots judged the home: %+v", v)
	}
	// the review of #433: with no TV change under way, a slot that crash-loops is the live
	// TV off the air: fatal (a release that breaks the live slot does not commit)
	changing = false
	if v := h.Check(context.Background(), "v1.5.0"); !v.Fatal || !strings.Contains(v.Why, "tv-slot/blue") {
		t.Fatalf("a live slot crash-looping = %+v", v)
	}
	procs[2].CrashLoop, procs[2].DetailCode = false, "db_restore_failed" // a slot halted
	if v := h.Check(context.Background(), "v1.5.0"); !v.Fatal {
		t.Fatalf("a halted live slot = %+v", v)
	}
	procs[2].DetailCode, procs[2].State = "", supervisor.StateStopped // stopped by a swap: not unhealthy
	if v := h.Check(context.Background(), "v1.5.0"); !v.Healthy {
		t.Fatalf("a slot a swap stopped = %+v", v)
	}
}

// an api that does not answer at all says why in the verdict (a rollback's reason): the
// transport's error, not "answers 0"; one that answers says its status.
func TestMachineHealthSaysWhyTheAPIDidNotAnswer(t *testing.T) {
	t.Setenv("MAKTABA_APP_BUNDLE", "")
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	gone := ln.Addr().String()
	_ = ln.Close() // nothing listens there now
	procs := []supervisor.ChildStatus{{Role: supervisor.RoleAPI, State: supervisor.StateRunning}}
	h := &machineHealth{snap: func() (supervisor.Snapshot, bool) { return supervisor.Snapshot{Processes: procs}, true }}
	h.cfg.Server.AdminAddr = gone
	v := h.Check(context.Background(), "v1.5.0")
	if v.Healthy || !strings.HasPrefix(v.Why, "api /readyz: ") || strings.Contains(v.Why, "answers 0") {
		t.Fatalf("api down: %+v", v)
	}

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusServiceUnavailable)
	}))
	defer srv.Close()
	h.cfg.Server.AdminAddr = strings.TrimPrefix(srv.URL, "http://")
	if v := h.Check(context.Background(), "v1.5.0"); v.Why != "api /readyz answers 503" {
		t.Fatalf("api not ready: %+v", v)
	}
}

// A server whose agent answers the home's update orders (the Windows app's, which says so
// in MAKTABA_AGENT_ORDERS) opens no window app for an order: no maktaba:// link, which a
// Windows without the app's link registered answers with "Get an app to open this link"
// every 90 seconds. Any other server still starts the app.
func TestNoAppLinkWhenTheAgentAnswersTheOrders(t *testing.T) {
	t.Setenv(envAgentOrders, "1")
	if appOpener() != nil {
		t.Fatal("the agent answers the orders, yet the server would open the app")
	}
	t.Setenv(envAgentOrders, "")
	if appOpener() == nil {
		t.Fatal("no agent answers the orders, yet the server would not start the app")
	}
}
