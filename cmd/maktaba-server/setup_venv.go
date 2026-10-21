package main

import (
	"bufio"
	"encoding/json"
	"errors"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"

	"github.com/Hamza-Labs-Core/Maktaba/cmd/maktaba-server/internal/config"
)

// setupPayload is the app's server payload (bin/, python/, pipeline/,
// payload.json): MAKTABA_APP_BUNDLE when the launcher set it, else the payload
// holding this executable (the app runs setup steps with its bundled
// maktaba-server, outside the launcher).
func setupPayload() (string, error) {
	if b := config.AppBundle(); b != "" {
		return b, nil
	}
	exe, err := os.Executable()
	if err == nil {
		if resolved, rerr := filepath.EvalSymlinks(exe); rerr == nil {
			exe = resolved
		}
		dir := filepath.Dir(filepath.Dir(exe)) // <payload>/bin/maktaba-server
		if _, err := os.Stat(filepath.Join(dir, "payload.json")); err == nil {
			return dir, nil
		}
	}
	return "", failStep("server_missing", "", "This maktaba-server is not inside the Maktaba app (no payload.json beside its bin folder).")
}

// payloadPython is the payload's bare Python (python-build-standalone).
func payloadPython(payload string) string {
	if runtime.GOOS == "windows" {
		return filepath.Join(payload, "python", "python.exe")
	}
	return filepath.Join(payload, "python", "bin", "python3")
}

// venvStatus is one line of venv_bootstrap's stdout (contract-venv.md §1).
type venvStatus struct {
	Event    string  `json:"event"`
	Action   string  `json:"action"`
	State    string  `json:"state"`
	Step     string  `json:"step"`
	Progress float64 `json:"progress"`
	Message  string  `json:"message"`
	Error    string  `json:"error"`
}

// stepVenv builds (or checks) the pipeline's venv from the payload's wheel and
// constraints with the payload's Python, relaying the bootstrap's progress.
// The full log is <logs>/venv.log.
func stepVenv(s *setupRun, _ json.RawMessage) (any, error) {
	payload, err := setupPayload()
	if err != nil {
		return nil, err
	}
	py := payloadPython(payload)
	wheels, _ := filepath.Glob(filepath.Join(payload, "pipeline", "maktaba_pipeline-*.whl"))
	if len(wheels) != 1 {
		return nil, failStep("venv_failed", "", "The app's pipeline folder must hold one maktaba_pipeline wheel (found %d).", len(wheels))
	}
	venv := filepath.Join(s.cfg.StateDir(), "venv")
	logDir := config.LogsDir()
	if err := os.MkdirAll(logDir, 0o755); err != nil {
		return nil, err
	}
	logPath := filepath.Join(logDir, "venv.log")
	logFile, err := os.OpenFile(logPath, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o644)
	if err != nil {
		return nil, err
	}
	defer logFile.Close()

	bootstrap := func(extra ...string) *exec.Cmd {
		args := append([]string{"-m", "maktaba_pipeline.venv_bootstrap",
			"--payload", filepath.Join(payload, "pipeline"), "--venv", venv}, extra...)
		cmd := exec.CommandContext(s.ctx, py, args...)
		// the bootstrap runs from the wheel itself (no -I / -E: they would drop PYTHONPATH)
		cmd.Env = append(os.Environ(), "PYTHONPATH="+wheels[0], "PYTHONUTF8=1")
		cmd.Stderr = io.MultiWriter(logFile, s.log)
		stopGently(cmd) // a stop asks the bootstrap, which stops pip and says where it was
		return cmd
	}
	// --check first: an up-to-date venv is a reuse, not a build
	check := bootstrap("--check")
	check.Stdout = io.Discard
	if err := check.Run(); err == nil {
		return s.venvResult(payload, wheels[0], true), nil
	}

	cmd := bootstrap()
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return nil, err
	}
	if err := cmd.Start(); err != nil {
		return nil, failStep("venv_failed", "", "Cannot start the app's Python (%s): %v", py, err)
	}
	var last venvStatus
	sc := bufio.NewScanner(stdout)
	sc.Buffer(make([]byte, 0, 64<<10), 1<<20)
	for sc.Scan() {
		var st venvStatus
		if json.Unmarshal(sc.Bytes(), &st) != nil || st.Event != "status" {
			continue
		}
		last = st
		// the bootstrap's progress is 0..1 over the whole run and never goes back
		s.progress("venv", st.Message, uint64(st.Progress*1000), 1000)
	}
	err = cmd.Wait()
	switch code := exitCodeOf(cmd, err); {
	case err == nil:
		return s.venvResult(payload, wheels[0], false), nil
	case code == 130:
		return nil, failStep("interrupted", "", "The install stopped; it goes on from where it was next time.")
	default:
		msg := last.Error
		if msg == "" {
			msg = last.Message
		}
		if lines := strings.Split(strings.TrimSpace(msg), "\n"); len(lines) > 3 {
			msg = strings.Join(lines[len(lines)-3:], "\n")
		}
		return nil, failStep("venv_failed", "", "%s (the full log: %s)", strings.TrimSpace(msg), logPath)
	}
}

func exitCodeOf(cmd *exec.Cmd, err error) int {
	if cmd.ProcessState != nil {
		return cmd.ProcessState.ExitCode()
	}
	var ee *exec.ExitError
	if errors.As(err, &ee) {
		return ee.ExitCode()
	}
	return -1
}

func (s *setupRun) venvResult(payload, wheel string, reused bool) map[string]any {
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
	return map[string]any{"python": pl.Python.Version, "pipeline": ver, "reused": reused}
}
