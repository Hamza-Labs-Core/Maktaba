package supervisor

import (
	"context"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"os/exec"
	"strconv"
	"sync/atomic"
	"testing"
	"time"
)

// The test binary doubles as the fake child process: with
// MAKTABA_FAKE_CHILD set, TestMain runs fakeChild instead of the tests.
//
// Modes:
//
//	serve  - serve FAKE_ADDR: /healthz answers 200; /crash?code=N exits N;
//	         /exit exits 0; /hang makes /healthz block (a wedged process)
//	crash  - exit FAKE_CODE (default 7) at once (a crash loop)
//	exit0  - exit 0 at once
func TestMain(m *testing.M) {
	if mode := os.Getenv("MAKTABA_FAKE_CHILD"); mode != "" {
		fakeChild(mode)
		return
	}
	os.Exit(m.Run())
}

func fakeChild(mode string) {
	switch mode {
	case "crash":
		code, err := strconv.Atoi(os.Getenv("FAKE_CODE"))
		if err != nil {
			code = 7
		}
		os.Exit(code)
	case "exit0":
		os.Exit(0)
	case "serve":
		var hung atomic.Bool
		mux := http.NewServeMux()
		mux.HandleFunc("/healthz", func(w http.ResponseWriter, _ *http.Request) {
			if hung.Load() {
				select {} // never answer
			}
			_, _ = io.WriteString(w, "ok")
		})
		mux.HandleFunc("/hang", func(w http.ResponseWriter, _ *http.Request) {
			hung.Store(true)
			_, _ = io.WriteString(w, "hung")
		})
		mux.HandleFunc("/crash", func(_ http.ResponseWriter, r *http.Request) {
			code, err := strconv.Atoi(r.URL.Query().Get("code"))
			if err != nil {
				code = 3
			}
			os.Exit(code)
		})
		mux.HandleFunc("/exit", func(http.ResponseWriter, *http.Request) { os.Exit(0) })
		addr := os.Getenv("FAKE_ADDR")
		var ln net.Listener
		var err error
		// The previous incarnation may still be releasing the port.
		for i := 0; i < 100; i++ {
			if ln, err = net.Listen("tcp", addr); err == nil {
				break
			}
			time.Sleep(20 * time.Millisecond)
		}
		if err != nil {
			fmt.Fprintln(os.Stderr, "fake child listen:", err)
			os.Exit(90)
		}
		_ = http.Serve(ln, mux)
		os.Exit(91)
	default:
		fmt.Fprintln(os.Stderr, "unknown fake child mode", mode)
		os.Exit(92)
	}
}

// freeAddr returns a loopback address with a free port.
func freeAddr(t *testing.T) string {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	addr := ln.Addr().String()
	_ = ln.Close()
	return addr
}

// fakeProc builds a Process running the test binary as a fake child.
// For mode "serve" it gets a free address and a liveness probe.
//
// These tests run real processes and take seconds, past the unit tier's
// per-test cap, so they are skipped under -short and run in the
// integration tier (`go test ./...` without -short).
func fakeProc(t *testing.T, role Role, mode string, extraEnv ...string) (Process, string) {
	t.Helper()
	if testing.Short() {
		t.Skip("spawns child processes; runs without -short")
	}
	cmd := exec.Command(os.Args[0], "-test.run=^$")
	addr := ""
	env := append(os.Environ(), "MAKTABA_FAKE_CHILD="+mode)
	p := Process{Role: role, Cmd: cmd}
	if mode == "serve" {
		addr = freeAddr(t)
		env = append(env, "FAKE_ADDR="+addr)
		p.Probe = "http://" + addr + "/healthz"
	}
	cmd.Env = append(env, extraEnv...)
	return p, addr
}

// testPolicy is fast enough for unit tests.
func testPolicy() Policy {
	return Policy{
		Restart:        RestartAlways,
		BackoffInitial: 50 * time.Millisecond,
		BackoffMax:     400 * time.Millisecond,
		HealthyAfter:   time.Hour,
		MaxRestarts:    100,
		Window:         time.Minute,
		FailedRetry:    time.Hour,
		ProbeInterval:  50 * time.Millisecond,
		ProbeTimeout:   300 * time.Millisecond,
		ProbeFailures:  3,
		StartupGrace:   10 * time.Second,
	}
}

// running starts s.Run in the background. stop cancels it and returns
// Run's error (failing if it takes too long); done yields Run's error
// once it returns on its own.
func running(t *testing.T, s *Supervisor) (stop func() error, done <-chan error) {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	finished := make(chan struct{})
	var result error
	go func() { result = s.Run(ctx, 5*time.Second); close(finished) }()
	doneCh := make(chan error, 1)
	go func() { <-finished; doneCh <- result }()
	stop = func() error {
		cancel()
		select {
		case <-finished:
		case <-time.After(15 * time.Second):
			t.Fatal("supervisor did not shut down")
		}
		return result
	}
	t.Cleanup(func() { _ = stop() })
	return stop, doneCh
}

// waitFor polls the snapshot until cond holds.
func waitFor(t *testing.T, s *Supervisor, what string, cond func(Snapshot) bool) Snapshot {
	t.Helper()
	deadline := time.Now().Add(20 * time.Second)
	for {
		snap := s.Snapshot()
		if cond(snap) {
			return snap
		}
		if time.Now().After(deadline) {
			t.Fatalf("timed out waiting for %s; last snapshot: %+v", what, snap.Processes)
		}
		time.Sleep(20 * time.Millisecond)
	}
}

func proc(snap Snapshot, role Role) ChildStatus {
	for _, p := range snap.Processes {
		if p.Role == role {
			return p
		}
	}
	return ChildStatus{}
}

func get(t *testing.T, url string) {
	t.Helper()
	c := &http.Client{Timeout: 2 * time.Second}
	resp, err := c.Get(url)
	if err == nil {
		_ = resp.Body.Close()
	}
}
