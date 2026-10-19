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
// it, fan its stdout/stderr through to ours, keep it running (Story
// 22.9, run.go: per-child restart with backoff, a crash-loop guard and
// liveness probes that catch a hung child) and — on SIGINT/SIGTERM —
// signal every child and wait for the drain. The web role is served
// in-process by the unified binary, so it is handled by the caller, not
// here.
package supervisor

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"

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

// Process describes a single supervised child: a human label, the
// fully-formed command (with env + args) ready to Start, and the
// liveness URL the supervisor polls (empty = no probe). The command is
// a template: each restart starts a fresh copy of it.
type Process struct {
	Role  Role
	Cmd   *exec.Cmd
	Probe string
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

	var cmd *exec.Cmd
	switch role {
	case RoleAPI:
		bin, err := resolveBinary("maktaba-api", "MAKTABA_API_BIN", opts.BinDir)
		if err != nil {
			return Process{}, err
		}
		cmd = exec.Command(bin, "serve")

	case RoleStreaming:
		bin, err := resolveBinary("maktaba-streaming", "MAKTABA_STREAMING_BIN", opts.BinDir)
		if err != nil {
			return Process{}, err
		}
		cmd = exec.Command(bin, "serve")

	case RolePipeline:
		cmd = exec.Command(pythonExecutable(), "-m", "maktaba_pipeline")

	default:
		return Process{}, fmt.Errorf("unknown role %q", role)
	}
	cmd.Env = env
	wire(cmd)
	return Process{Role: role, Cmd: cmd, Probe: ProbeURL(role, opts.Config)}, nil
}

// ProbeURL is the liveness endpoint the supervisor polls for a role:
// the api and streaming admin ports' /healthz, and the pipeline's
// logs-port /healthz (its event-loop heartbeat).
func ProbeURL(role Role, cfg config.Config) string {
	switch role {
	case RoleAPI:
		return "http://" + LocalAddr(cfg.Server.AdminAddr, "9100") + "/healthz"
	case RoleStreaming:
		return "http://" + LocalAddr(streamingAdminAddr, "9101") + "/healthz"
	case RolePipeline:
		return "http://" + pipelineLogsAddr + "/healthz"
	}
	return ""
}

func wire(cmd *exec.Cmd) {
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	cmd.Stdin = nil
}

// cloneCmd returns a fresh, unstarted copy of a command template: an
// exec.Cmd can only be started once, so every restart needs a new one.
func cloneCmd(tpl *exec.Cmd) *exec.Cmd {
	return &exec.Cmd{
		Path:        tpl.Path,
		Args:        append([]string(nil), tpl.Args...),
		Env:         tpl.Env,
		Dir:         tpl.Dir,
		Stdin:       tpl.Stdin,
		Stdout:      tpl.Stdout,
		Stderr:      tpl.Stderr,
		SysProcAttr: tpl.SysProcAttr,
		Err:         tpl.Err,
	}
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

	if runtime.GOOS == "windows" {
		name += ".exe" // exec.LookPath adds it on its own; the directory candidates need it
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
	if runtime.GOOS == "windows" { // no execute bits: Windows runs a program by its suffix
		return strings.EqualFold(filepath.Ext(p), ".exe")
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
