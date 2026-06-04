// Package supervisor launches and watches the role processes that make
// up a running Maktaba instance.
//
// Each role maps to an existing service entrypoint that is already
// fully env-driven:
//
//	api       -> maktaba-api serve        (Go binary, sibling of self)
//	streaming -> maktaba-streaming serve  (Go binary, sibling of self)
//	pipeline  -> python -m maktaba_pipeline (Python module)
//
// The supervisor's job is lifecycle, not logic: resolve the executable
// for a role, hand it an environment translated from server.toml, start
// it, fan its stdout/stderr through to ours, and — on SIGINT/SIGTERM —
// signal every child and wait for the drain. The web role is served
// in-process by the unified binary, so it is handled by the caller, not
// here.
package supervisor

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sync"
	"time"

	"github.com/Hamza-Labs-Core/Maktaba/cmd/maktaba-server/internal/config"
)

// Role identifies one supervised service.
type Role string

const (
	// RoleAPI is the public + admin HTTP API service (maktaba-api).
	RoleAPI Role = "api"
	// RoleStreaming is the byte-pumping media service (maktaba-streaming).
	RoleStreaming Role = "streaming"
	// RolePipeline is the Python transcription/index worker daemon.
	RolePipeline Role = "pipeline"
)

// Process describes a single supervised child: a human label and the
// fully-formed command (with env + args) ready to Start.
type Process struct {
	Role Role
	Cmd  *exec.Cmd
}

// Options configures process construction.
type Options struct {
	Config config.Config
	// BinDir is searched (alongside the directory of the running
	// executable) for sibling maktaba-api / maktaba-streaming binaries.
	// Empty means "next to me only".
	BinDir string
}

// ErrBinaryNotFound is returned when a role's executable can't be
// located. Callers surface it with an actionable hint (run `make build`
// or set MAKTABA_API_BIN).
var ErrBinaryNotFound = errors.New("service binary not found")

// Build constructs the *exec.Cmd for a role without starting it, so the
// caller (and tests) can inspect the resolved path, args, and env. The
// child inherits the parent env and then has the config-derived
// variables layered on top, so an operator can still override any single
// var from the shell without editing server.toml.
func Build(role Role, opts Options) (Process, error) {
	env := append(os.Environ(), ChildEnv(role, opts.Config)...)

	switch role {
	case RoleAPI:
		bin, err := resolveBinary("maktaba-api", "MAKTABA_API_BIN", opts.BinDir)
		if err != nil {
			return Process{}, err
		}
		cmd := exec.Command(bin, "serve")
		cmd.Env = env
		wire(cmd)
		return Process{Role: role, Cmd: cmd}, nil

	case RoleStreaming:
		bin, err := resolveBinary("maktaba-streaming", "MAKTABA_STREAMING_BIN", opts.BinDir)
		if err != nil {
			return Process{}, err
		}
		cmd := exec.Command(bin, "serve")
		cmd.Env = env
		wire(cmd)
		return Process{Role: role, Cmd: cmd}, nil

	case RolePipeline:
		py := pythonExecutable()
		cmd := exec.Command(py, "-m", "maktaba_pipeline")
		cmd.Env = env
		wire(cmd)
		return Process{Role: role, Cmd: cmd}, nil

	default:
		return Process{}, fmt.Errorf("unknown role %q", role)
	}
}

// Run starts every process and blocks until ctx is cancelled or any
// child exits. On ctx cancellation it sends SIGTERM to all children and
// waits up to grace for them to drain before SIGKILL. The first
// non-nil child error (other than a clean shutdown) is returned.
func Run(ctx context.Context, procs []Process, grace time.Duration) error {
	if len(procs) == 0 {
		return errors.New("no processes to run")
	}

	var wg sync.WaitGroup
	errCh := make(chan error, len(procs))

	for _, p := range procs {
		if err := p.Cmd.Start(); err != nil {
			// Tear down anything already started before bailing.
			signalAll(procs, os.Kill)
			return fmt.Errorf("start %s: %w", p.Role, err)
		}
		fmt.Fprintf(os.Stderr, "[supervisor] started %s (pid %d)\n", p.Role, p.Cmd.Process.Pid)
	}

	for _, p := range procs {
		wg.Add(1)
		go func(p Process) {
			defer wg.Done()
			if err := p.Cmd.Wait(); err != nil {
				// A child killed by our own SIGTERM during shutdown is
				// not a failure; the ctx.Err() check in Run decides.
				errCh <- fmt.Errorf("%s exited: %w", p.Role, err)
			} else {
				errCh <- nil
			}
		}(p)
	}

	select {
	case <-ctx.Done():
		// Graceful: ask everyone to drain, then hard-kill stragglers.
		signalAll(procs, terminationSignal())
		done := make(chan struct{})
		go func() { wg.Wait(); close(done) }()
		select {
		case <-done:
		case <-time.After(grace):
			signalAll(procs, os.Kill)
			<-done
		}
		return nil

	case err := <-errCh:
		// A child died on its own — take the rest down with it so the
		// supervisor never lingers half-up, then surface the cause.
		signalAll(procs, terminationSignal())
		go func() { wg.Wait() }()
		return err
	}
}

func signalAll(procs []Process, sig os.Signal) {
	for _, p := range procs {
		if p.Cmd.Process != nil {
			_ = p.Cmd.Process.Signal(sig)
		}
	}
}

func wire(cmd *exec.Cmd) {
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	cmd.Stdin = nil
}

// LocateAPI resolves the maktaba-api binary, used by the passthrough
// CLI subcommands (migrate/adduser/keys) that delegate to it. Same
// resolution order as the supervised api role.
func LocateAPI(binDir string) (string, error) {
	return resolveBinary("maktaba-api", "MAKTABA_API_BIN", binDir)
}

// resolveBinary finds a sibling service binary. Resolution order:
//  1. explicit override env var (MAKTABA_API_BIN / MAKTABA_STREAMING_BIN)
//  2. opts.BinDir, if set
//  3. the directory of the running maktaba-server executable
//  4. $PATH
func resolveBinary(name, overrideEnv, binDir string) (string, error) {
	if p := os.Getenv(overrideEnv); p != "" {
		if isExecutable(p) {
			return p, nil
		}
		return "", fmt.Errorf("%w: %s=%q is not an executable file", ErrBinaryNotFound, overrideEnv, p)
	}

	candidates := []string{}
	if binDir != "" {
		candidates = append(candidates, filepath.Join(binDir, name))
	}
	if self, err := os.Executable(); err == nil {
		candidates = append(candidates, filepath.Join(filepath.Dir(self), name))
	}
	for _, c := range candidates {
		if isExecutable(c) {
			return c, nil
		}
	}
	if p, err := exec.LookPath(name); err == nil {
		return p, nil
	}
	return "", fmt.Errorf("%w: %q (build it with `make build` or set %s)", ErrBinaryNotFound, name, overrideEnv)
}

func isExecutable(p string) bool {
	info, err := os.Stat(p)
	if err != nil || info.IsDir() {
		return false
	}
	return info.Mode()&0o111 != 0
}

// pythonExecutable honours $MAKTABA_PYTHON, then falls back to python3.
func pythonExecutable() string {
	if p := os.Getenv("MAKTABA_PYTHON"); p != "" {
		return p
	}
	return "python3"
}
