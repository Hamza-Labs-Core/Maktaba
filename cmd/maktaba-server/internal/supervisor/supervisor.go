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
	"context"
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
	// Instance names one of several processes of a role (tv-slot: blue,
	// green, amber).
	Instance string
	// OnDemand: not started until asked for (Supervisor.Want).
	OnDemand bool
	// Check, when set, is the liveness check in place of an HTTP GET of Probe
	// (postgres speaks its own protocol); Probe then only labels it.
	Check func(ctx context.Context) error
	// StopSignal, when set, is the graceful-stop signal in place of SIGTERM
	// (postgres: SIGINT, its fast shutdown).
	StopSignal os.Signal
	// First: started before every other process, which wait until it runs (its
	// check answered), and stopped after them (Epic 36 story 36.1: the bundled
	// postgres).
	First bool
	// Prepare, when set, runs before the process's first start and must succeed
	// before it starts (prepare.go, Epic 38 story 38.12: the desktop app builds
	// the pipeline's venv, or takes the database's dump before an update's
	// migrations). report sets the process's detail in /processes.
	Prepare func(ctx context.Context, report func(detail string)) error
}

// stopSignal is the process's graceful-stop signal.
func (p Process) stopSignal() os.Signal {
	if p.StopSignal != nil {
		return p.StopSignal
	}
	return terminationSignal()
}

// Name is the process's label: the role, or role/instance.
func (p Process) Name() string {
	if p.Instance != "" {
		return string(p.Role) + "/" + p.Instance
	}
	return string(p.Role)
}

// Options configures process construction.
type Options struct {
	Config config.Config
	// BinDir is searched (alongside the directory of the running
	// executable) for sibling maktaba-api / maktaba-streaming binaries.
	// Empty means "next to me only".
	BinDir string
	// ReleaseDir is the current release's directory when the launcher
	// started us (MAKTABA_RELEASE_DIR = <install>/bin/releases/<v>/, Epic 35
	// story 35.2 AC2): every role binary is resolved there and only there —
	// never from PATH or next to the launcher — and the pipeline runs from
	// the release's venv when it has one.
	ReleaseDir string
}

// resolve finds a role binary: in ReleaseDir only when set, else the
// classic order of resolveBinary.
func (o Options) resolve(name, overrideEnv string) (string, error) {
	if o.ReleaseDir != "" {
		return resolveInRelease(name, overrideEnv, o.ReleaseDir)
	}
	return resolveBinary(name, overrideEnv, o.BinDir)
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
		bin, err := opts.resolve("maktaba-api", "MAKTABA_API_BIN")
		if err != nil {
			return Process{}, err
		}
		cmd = exec.Command(bin, "serve")

	case RoleStreaming:
		bin, err := opts.resolve("maktaba-streaming", "MAKTABA_STREAMING_BIN")
		if err != nil {
			return Process{}, err
		}
		cmd = exec.Command(bin, "serve")

	case RolePipeline:
		cmd = exec.Command(pythonFor(opts), "-m", "maktaba_pipeline")

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
		return "http://" + LocalAddr(streamingAdmin(cfg), "9101") + "/healthz"
	case RolePipeline:
		return "http://" + pipelineLogs(cfg) + "/healthz"
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

// resolveInRelease resolves a binary inside the launcher's release
// directory only (the explicit override env var still wins: an operator's
// choice, never a request's).
func resolveInRelease(name, overrideEnv, releaseDir string) (string, error) {
	if p := os.Getenv(overrideEnv); p != "" {
		if isExecutable(p) {
			return p, nil
		}
		return "", fmt.Errorf("%w: %s=%q is not an executable file", ErrBinaryNotFound, overrideEnv, p)
	}
	if runtime.GOOS == "windows" {
		name += ".exe"
	}
	p := filepath.Join(releaseDir, name)
	if isExecutable(p) {
		return p, nil
	}
	return "", fmt.Errorf("%w: %q is not in the release directory %s", ErrBinaryNotFound, name, releaseDir)
}

// pythonFor is pythonExecutable with [pipeline] python (a converted worker's own
// environment, Epic 35 story 35.8) between MAKTABA_PYTHON and the release's venv.
func pythonFor(opts Options) string {
	if p := os.Getenv("MAKTABA_PYTHON"); p != "" {
		return p
	}
	if p := strings.TrimSpace(opts.Config.Pipeline.Python); p != "" {
		return p
	}
	// The desktop app: the venv built from the payload's wheel (Epic 36 story
	// 36.3), also before it exists: the pipeline's Prepare builds it before the
	// first start (Epic 38 story 38.12, serve_appbundle.go)
	if config.AppBundle() != "" {
		return venvPython(filepath.Join(opts.Config.StateDir(), "venv"))
	}
	return pythonExecutable(opts.ReleaseDir)
}

// pythonExecutable honours $MAKTABA_PYTHON, then the release's pipeline
// environment (<release>/venv, design §2.1), then falls back to python3.
func pythonExecutable(releaseDir string) string {
	if p := os.Getenv("MAKTABA_PYTHON"); p != "" {
		return p
	}
	if releaseDir != "" {
		if venvPy := venvPython(filepath.Join(releaseDir, "venv")); isExecutable(venvPy) {
			return venvPy
		}
	}
	return "python3"
}

// venvPython is a venv's interpreter.
func venvPython(venv string) string {
	if runtime.GOOS == "windows" {
		return filepath.Join(venv, "Scripts", "python.exe")
	}
	return filepath.Join(venv, "bin", "python")
}
