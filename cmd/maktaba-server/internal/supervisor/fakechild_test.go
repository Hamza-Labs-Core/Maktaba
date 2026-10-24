package supervisor

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"strconv"
	"sync/atomic"
	"syscall"
	"testing"
	"time"

	"github.com/Hamza-Labs-Core/Maktaba/cmd/maktaba-server/internal/config"
)

// The test binary doubles as the fake child process: with
// MAKTABA_FAKE_CHILD set, TestMain runs fakeChild instead of the tests.
//
// Modes:
//
//	serve  - serve FAKE_ADDR: /healthz answers 200; /crash?code=N exits N;
//	         /exit exits 0; /hang makes /healthz block (a wedged process);
//	         FAKE_HEALTHZ_DELAY (a duration) makes /healthz that slow
//	crash  - exit FAKE_CODE (default 7) at once (a crash loop)
//	exit0  - exit 0 at once
//	sleep  - sleep (a grandchild standing in for ffmpeg); with FAKE_PIDFILE
//	         set, write its pid there first
//
// With FAKE_GRANDCHILD_PIDFILE set, "serve" first starts a "sleep"
// grandchild (no process-group or job changes, like ffmpeg under
// streaming), which writes its pid to that file once it runs.
//
// With FAKE_ON_STOP set, any mode takes the supervisor's ask to stop as a
// role does (onAsk): "exit" drains and exits 0, "hold" never stops.
func TestMain(m *testing.M) {
	if mode := os.Getenv("MAKTABA_FAKE_CHILD"); mode != "" {
		fakeChild(mode)
		return
	}
	// never this machine's real node config (it holds a database credential)
	nodeConfig = func() config.NodeConfig { return config.NodeConfig{} }
	// nor its GPU: the transcription plan of a test is the one it fakes (stt_test.go)
	nvidiaSMI = func() ([]byte, error) { return nil, errors.New("no nvidia-smi in the tests") }
	// ...nor its pipeline's Python (TestSTTProbe asks the test binary)
	sttInstalled = func(string) bool { return true }
	// ...nor for the children the tests start (they inherit the environment)
	dir, err := os.MkdirTemp("", "maktaba-nodecfg-")
	if err != nil {
		panic(err)
	}
	_ = os.Setenv("MAKTABA_NODE_CONFIG", filepath.Join(dir, "node.json"))
	code := m.Run()
	_ = os.RemoveAll(dir)
	os.Exit(code)
}

func fakeChild(mode string) {
	if f := os.Getenv("FAKE_WINDOW_FILE"); f != "" {
		_ = os.WriteFile(f, []byte(strconv.FormatBool(hasConsoleWindow())), 0o644)
	}
	if on := os.Getenv("FAKE_ON_STOP"); on != "" {
		onAsk(on, os.Getenv("FAKE_MARKER"))
	}
	switch mode {
	case "crash":
		code, err := strconv.Atoi(os.Getenv("FAKE_CODE"))
		if err != nil {
			code = 7
		}
		if out := os.Getenv("FAKE_STDOUT"); out != "" {
			fmt.Println(out)
		}
		os.Exit(code)
	case "exit0":
		os.Exit(0)
	case "sleep":
		if pidfile := os.Getenv("FAKE_PIDFILE"); pidfile != "" {
			if err := os.WriteFile(pidfile+".tmp", []byte(strconv.Itoa(os.Getpid())), 0o644); err != nil ||
				os.Rename(pidfile+".tmp", pidfile) != nil {
				os.Exit(94)
			}
		}
		time.Sleep(time.Hour)
		os.Exit(0)
	case "serve":
		if pidfile := os.Getenv("FAKE_GRANDCHILD_PIDFILE"); pidfile != "" {
			gc := exec.Command(os.Args[0], "-test.run=^$")
			// the grandchild takes an ask as its parent does, but leaves the marker to it
			gc.Env = append(os.Environ(), "MAKTABA_FAKE_CHILD=sleep", "FAKE_GRANDCHILD_PIDFILE=",
				"FAKE_PIDFILE="+pidfile, "FAKE_MARKER=")
			if err := gc.Start(); err != nil {
				fmt.Fprintln(os.Stderr, "fake child: grandchild:", err)
				os.Exit(93)
			}
		}
		var hung atomic.Bool
		mux := http.NewServeMux()
		slow, _ := time.ParseDuration(os.Getenv("FAKE_HEALTHZ_DELAY"))
		mux.HandleFunc("/healthz", func(w http.ResponseWriter, _ *http.Request) {
			if hung.Load() {
				select {} // never answer
			}
			time.Sleep(slow) // a busy process (a worker whose GIL a native call holds)
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

// onAsk catches the supervisor's ask to stop as a role's signal.NotifyContext
// does (SIGTERM on POSIX; on Windows CTRL_BREAK, which Go delivers as
// os.Interrupt), writes the signal to marker (when set), and then with "exit"
// exits 0 (a drain that ends) or with "hold" runs on (one that never does).
func onAsk(on, marker string) {
	asked := make(chan os.Signal, 1)
	signal.Notify(asked, os.Interrupt, syscall.SIGTERM)
	go func() {
		for sig := range asked {
			if marker != "" {
				_ = os.WriteFile(marker, []byte(sig.String()), 0o644)
			}
			if on == "exit" {
				os.Exit(0)
			}
		}
	}()
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
	return runningGrace(t, s, 5*time.Second)
}

// runningGrace is running with its own shutdown grace.
func runningGrace(t *testing.T, s *Supervisor, grace time.Duration) (stop func() error, done <-chan error) {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	finished := make(chan struct{})
	var result error
	go func() { result = s.Run(ctx, grace); close(finished) }()
	doneCh := make(chan error, 1)
	go func() { <-finished; doneCh <- result }()
	stop = func() error {
		cancel()
		select {
		case <-finished:
		case <-time.After(grace + 10*time.Second):
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
