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

// Run brings the venv up to date. A run that finds it up to date (`--check`)
// builds nothing and reports Reused.
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
	bootstrap := func(extra ...string) *exec.Cmd {
		args := append([]string{"-m", "maktaba_pipeline.venv_bootstrap",
			"--payload", filepath.Join(spec.Payload, "pipeline"), "--venv", spec.Venv}, extra...)
		cmd := exec.CommandContext(ctx, py, args...)
		// the bootstrap runs from the wheel itself (no -I / -E: they would drop PYTHONPATH)
		cmd.Env = append(os.Environ(), "PYTHONPATH="+wheel, "PYTHONUTF8=1")
		cmd.Stderr = logw
		return cmd
	}
	check := bootstrap("--check")
	check.Stdout = io.Discard
	if err := check.Run(); err == nil {
		return result(spec.Payload, wheel, true), nil
	}

	cmd := bootstrap()
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return Result{}, err
	}
	if err := cmd.Start(); err != nil {
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
	case err == nil:
		return result(spec.Payload, wheel, false), nil
	case code == 130 || ctx.Err() != nil:
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
