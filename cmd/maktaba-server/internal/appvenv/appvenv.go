// Package appvenv builds the desktop app's pipeline venv from its server payload
// (the payload's Python, the pipeline wheel and its constraints) with the
// pipeline's own bootstrap, maktaba_pipeline.venv_bootstrap
// (specs/epics/36-mac-app/contract-venv.md). Setup's venv step runs it (Epic 38
// story 38.6), and so does the supervisor before the pipeline's first start, so
// an update whose constraints changed rebuilds the venv (story 38.12). The
// bootstrap decides what to do itself (nothing, a build, a resume, a repair, the
// wheel alone) from the stamp it left.
package appvenv

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"time"
)

// Spec is one run.
type Spec struct {
	Payload string    // the app's server payload (python/, pipeline/, payload.json)
	Venv    string    // the venv to build (<state>/venv)
	Log     io.Writer // the bootstrap's stderr (the app's venv.log)
}

// Progress is one line of the bootstrap: its share of the whole run (0..1,
// never going back), its step and a line for the screen.
type Progress struct {
	Fraction float64
	Step     string
	Message  string
}

// Result is a finished run.
type Result struct {
	// Reused: the venv was up to date (nothing was built).
	Reused bool
	// Python is the payload's Python version, Pipeline the wheel's.
	Python, Pipeline string
}

// ErrInterrupted: the run was stopped; the next run goes on from there.
var ErrInterrupted = errors.New("the venv build was stopped; it goes on from where it was next time")

// Error is a failed run: the failing step's last lines.
type Error struct{ Message string }

func (e *Error) Error() string { return e.Message }

// exitInterrupted is the bootstrap's exit after a stop (SIGTERM, CTRL_BREAK,
// Ctrl-C): the stamp says building, the next run resumes.
const exitInterrupted = 130

// stopWait is how long a run whose context ends (the supervisor stopping, a
// setup step cancelled) waits for the bootstrap to stop on its own before it
// is killed. Asked to stop, the bootstrap stops pip and waits for it — 30 s
// after SIGTERM, 10 s after CTRL_BREAK on Windows — then kills it, writes its
// status (interrupted) and exits 130. 45 s covers the longer of those with
// room for the rest, and stays under the 60 s the app's launcher gives the
// whole server to stop (contract-update.md), so the server still ends on its own.
const stopWait = 45 * time.Second

// Python is the payload's bare Python (python-build-standalone).
func Python(payload string) string {
	if runtime.GOOS == "windows" {
		return filepath.Join(payload, "python", "python.exe")
	}
	return filepath.Join(payload, "python", "bin", "python3")
}

// Wheel is the payload's pipeline wheel (there is exactly one).
func Wheel(payload string) (string, error) {
	wheels, _ := filepath.Glob(filepath.Join(payload, "pipeline", "maktaba_pipeline-*.whl"))
	if len(wheels) != 1 {
		return "", fmt.Errorf("the app's pipeline folder must hold one maktaba_pipeline wheel (found %d)", len(wheels))
	}
	return wheels[0], nil
}

// status is one line of the bootstrap's stdout.
type status struct {
	Event    string  `json:"event"`
	State    string  `json:"state"`
	Step     string  `json:"step"`
	Progress float64 `json:"progress"`
	Message  string  `json:"message"`
	Error    string  `json:"error"`
}

// keptEnv are the variables of the server's environment the bootstrap gets
// (bootstrapEnv). Nothing else of it reaches the bootstrap, nor pip under it,
// but what MAKTABA_VENV_KEEP_ENV names: not MAKTABA_PYTHON,
// MAKTABA_PIPELINE_EXTRAS, MAKTABA_VENV or MAKTABA_PIPELINE_PAYLOAD (which would
// change what it builds), nor PYTHONHOME, PYTHONPATH or a VIRTUAL_ENV (which
// would change the Python that runs it).
var keptEnv = []string{
	// the system: where the programs, the home folder and the temporary files are
	"PATH", "HOME", "USER", "LOGNAME", "TMPDIR", "TEMP", "TMP", "LANG", "LC_ALL", "LC_CTYPE",
	"XDG_CACHE_HOME", "XDG_CONFIG_HOME", // pip's cache and configuration on Linux
	// Windows: a Python does not start without SYSTEMROOT; pip keeps its cache
	// in LOCALAPPDATA and its configuration in APPDATA
	"SYSTEMROOT", "WINDIR", "SYSTEMDRIVE", "COMSPEC", "PATHEXT", "USERPROFILE", "USERNAME",
	"HOMEDRIVE", "HOMEPATH", "APPDATA", "LOCALAPPDATA", "PROGRAMDATA",
	"PROCESSOR_ARCHITECTURE", "NUMBER_OF_PROCESSORS",
	// the network: a proxy and the certificates it needs, a package mirror
	// (every package is checked against its hash in the constraints whatever
	// serves it), pip's cache and how long it waits
	"HTTP_PROXY", "HTTPS_PROXY", "NO_PROXY", "ALL_PROXY", "http_proxy", "https_proxy", "no_proxy", "all_proxy",
	"SSL_CERT_FILE", "SSL_CERT_DIR", "REQUESTS_CA_BUNDLE",
	"PIP_INDEX_URL", "PIP_EXTRA_INDEX_URL", "PIP_TRUSTED_HOST", "PIP_CERT", "PIP_CLIENT_CERT",
	"PIP_PROXY", "PIP_CACHE_DIR", "PIP_TIMEOUT", "PIP_RETRIES",
}

// envKeepMore names more variables of the server's environment for the
// bootstrap, comma-separated: a network that needs one keptEnv lacks (a
// netrc, another mirror's setting).
const envKeepMore = "MAKTABA_VENV_KEEP_ENV"

// bootstrapEnv is the bootstrap's whole environment: keptEnv's variables of
// environ and those MAKTABA_VENV_KEEP_ENV names, then the bootstrap's own (it
// runs from the wheel, with UTF-8 I/O and no user site-packages, which run code
// at the start of a Python).
func bootstrapEnv(environ []string, wheel string) []string {
	keep := keptEnv
	for _, kv := range environ {
		if more, ok := strings.CutPrefix(kv, envKeepMore+"="); ok {
			for _, k := range strings.Split(more, ",") {
				if k = strings.TrimSpace(k); k != "" {
					keep = append(keep[:len(keep):len(keep)], k)
				}
			}
		}
	}
	var env []string
	for _, kv := range environ {
		k, _, ok := strings.Cut(kv, "=")
		if !ok || k == "" {
			continue // Windows' hidden per-drive "=C:" entries
		}
		for _, keep := range keep {
			if k == keep || (runtime.GOOS == "windows" && strings.EqualFold(k, keep)) {
				env = append(env, kv)
				break
			}
		}
	}
	// the bootstrap runs from the wheel itself (no -I / -E: they would drop PYTHONPATH)
	return append(env, "PYTHONPATH="+wheel, "PYTHONUTF8=1", "PYTHONNOUSERSITE=1")
}

// Run brings the venv up to date. A run that finds it up to date (`--check`)
// builds nothing and reports Reused. When ctx ends the bootstrap is asked to
// stop (SIGTERM; CTRL_BREAK on Windows), which it does at once, stopping pip
// first; Run then returns ErrInterrupted.
func Run(ctx context.Context, spec Spec, progress func(Progress)) (Result, error) {
	py := Python(spec.Payload)
	wheel, err := Wheel(spec.Payload)
	if err != nil {
		return Result{}, err
	}
	logw := spec.Log
	if logw == nil {
		logw = io.Discard
	}
	env := bootstrapEnv(os.Environ(), wheel)
	bootstrap := func(extra ...string) *exec.Cmd {
		args := append([]string{"-m", "maktaba_pipeline.venv_bootstrap", "--python", py,
			"--payload", filepath.Join(spec.Payload, "pipeline"), "--venv", spec.Venv}, extra...)
		cmd := exec.CommandContext(ctx, py, args...)
		cmd.Env = env
		cmd.Stderr = logw
		ownGroup(cmd)
		cmd.Cancel = func() error { return askStop(cmd) }
		cmd.WaitDelay = stopWait
		return cmd
	}
	check := bootstrap("--check")
	check.Stdout = io.Discard
	if err := check.Run(); err == nil {
		return result(spec.Payload, wheel, true), nil
	} else if ctx.Err() != nil {
		return Result{}, ErrInterrupted
	}

	cmd := bootstrap()
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return Result{}, err
	}
	if err := cmd.Start(); err != nil {
		if ctx.Err() != nil {
			return Result{}, ErrInterrupted
		}
		return Result{}, fmt.Errorf("cannot start the app's Python (%s): %w", py, err)
	}
	var last status
	sc := bufio.NewScanner(stdout)
	sc.Buffer(make([]byte, 0, 64<<10), 1<<20)
	for sc.Scan() {
		var st status
		if json.Unmarshal(sc.Bytes(), &st) != nil || st.Event != "status" {
			continue
		}
		last = st
		if progress != nil {
			progress(Progress{Fraction: st.Progress, Step: st.Step, Message: st.Message})
		}
	}
	err = cmd.Wait()
	switch code := exitCode(cmd, err); {
	case code == 0:
		// done (Wait still says the context ended when it ended as the run did)
		return result(spec.Payload, wheel, false), nil
	case code == exitInterrupted || ctx.Err() != nil:
		return Result{}, ErrInterrupted
	default:
		msg := last.Error
		if msg == "" {
			msg = last.Message
		}
		if lines := strings.Split(strings.TrimSpace(msg), "\n"); len(lines) > 3 {
			msg = strings.Join(lines[len(lines)-3:], "\n")
		}
		if strings.TrimSpace(msg) == "" {
			msg = fmt.Sprintf("the venv bootstrap exited %d", code)
		}
		return Result{}, &Error{Message: strings.TrimSpace(msg)}
	}
}

func exitCode(cmd *exec.Cmd, err error) int {
	if cmd.ProcessState != nil {
		return cmd.ProcessState.ExitCode()
	}
	var ee *exec.ExitError
	if errors.As(err, &ee) {
		return ee.ExitCode()
	}
	return -1
}

func result(payload, wheel string, reused bool) Result {
	var pl struct {
		Python struct {
			Version string `json:"version"`
		} `json:"python"`
	}
	if raw, err := os.ReadFile(filepath.Join(payload, "payload.json")); err == nil {
		_ = json.Unmarshal(raw, &pl)
	}
	// maktaba_pipeline-<version>-py3-none-any.whl
	ver := strings.TrimPrefix(filepath.Base(wheel), "maktaba_pipeline-")
	ver, _, _ = strings.Cut(ver, "-")
	return Result{Reused: reused, Python: pl.Python.Version, Pipeline: ver}
}
