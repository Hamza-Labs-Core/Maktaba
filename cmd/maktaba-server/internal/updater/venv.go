package updater

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"github.com/Hamza-Labs-Core/Maktaba/cmd/maktaba-server/internal/appvenv"
	"github.com/Hamza-Labs-Core/Maktaba/shared/go/layout"
	"github.com/Hamza-Labs-Core/Maktaba/shared/go/releaseverify"
)

// venvBuild is appvenv.Run (tests replace it).
var venvBuild = appvenv.Run

// envPipelinePython names the Python a plain server install builds its pipeline
// environment with, when the machine's own is not the one to use.
const envPipelinePython = "MAKTABA_PIPELINE_PYTHON"

// buildVenv builds a plain server release's pipeline environment (design §3.4, story
// 35.4 AC4): the release's pipeline wheel and its hash-checked constraints, verified
// like every file of it, installed into <release>/venv with the machine's own Python by
// the pipeline's bootstrap (every package checked against its hash). The pipeline then
// runs the release's own environment: an update updates the pipeline too.
func (li *LayoutInstaller) buildVenv(ctx context.Context, j *Journal, res *releaseverify.Result, dir string, log io.Writer) error {
	if log == nil {
		log = io.Discard
	}
	p := res.Manifest.Pipeline
	if p == nil || p.Wheel == "" {
		_, _ = fmt.Fprintf(log, "[update] %s carries no pipeline wheel: the pipeline keeps the environment it has\n", j.Job.Tag)
		return nil
	}
	pipe := filepath.Join(dir, "pipeline")
	if err := os.MkdirAll(pipe, 0o755); err != nil {
		return err
	}
	names := []string{p.Wheel, p.Constraints}
	if p.ConstraintsCUDA != "" {
		names = append(names, p.ConstraintsCUDA)
	}
	for _, n := range names {
		u, ok := j.Job.Assets[n]
		if !ok || n == "" {
			return Fail("release_incomplete", "%s has no %s", j.Job.Tag, n)
		}
		path := filepath.Join(pipe, n)
		if err := li.Source.downloadTo(ctx, u, path, 512<<20); err != nil {
			return Fail("download_failed", "%s: %v", n, err)
		}
		if err := res.VerifyArtifact(releaseverify.Artifact{Name: n, Path: path}); err != nil {
			return verifyFailure(err)
		}
	}
	py := li.python()
	if py == "" {
		return Fail("no_python", "no Python for the pipeline's environment: install Python 3.12 (or uv), or name one "+
			"in %s", envPipelinePython)
	}
	_, _ = fmt.Fprintf(log, "[update] building the pipeline's environment with %s\n", py)
	said := -1
	_, err := venvBuild(ctx, appvenv.Spec{Python: py, Pipeline: pipe, Venv: filepath.Join(dir, "venv"), Log: log},
		func(pr appvenv.Progress) {
			if pct := int(pr.Fraction * 10); pct != said {
				said = pct
				_, _ = fmt.Fprintf(log, "[update] pipeline environment %d%%: %s\n", pct*10, pr.Message)
			}
		})
	if err != nil {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		return Fail("venv_failed", "%v", err)
	}
	return nil
}

// python is the interpreter the pipeline's environment is built with: the one named in
// MAKTABA_PIPELINE_PYTHON; the one the running release's environment (or the configured
// [pipeline] python's) was made from; else the first Python on PATH.
func (li *LayoutInstaller) python() string {
	if p := strings.TrimSpace(os.Getenv(envPipelinePython)); p != "" {
		return p
	}
	for _, py := range []string{venvPython(filepath.Join(li.Layout.ReleaseDir(li.Running()), "venv")), li.PipelinePython} {
		if base := baseInterpreter(py); base != "" {
			return base
		}
	}
	for _, name := range []string{"python3.12", "python3", "python"} {
		if p, err := exec.LookPath(name); err == nil {
			return p
		}
	}
	return ""
}

// venvPython is a venv's interpreter.
func venvPython(venv string) string {
	if runtime.GOOS == "windows" {
		return filepath.Join(venv, "Scripts", "python.exe")
	}
	return filepath.Join(venv, "bin", "python")
}

// baseInterpreter is the interpreter a venv's python was made from (pyvenv.cfg's
// executable, else its home's python), or py itself when it is no venv's; "" when py
// does not exist.
func baseInterpreter(py string) string {
	if py == "" {
		return ""
	}
	if _, err := os.Stat(py); err != nil {
		return ""
	}
	f, err := os.Open(filepath.Join(filepath.Dir(filepath.Dir(py)), "pyvenv.cfg"))
	if err != nil {
		return py // a Python of its own
	}
	defer f.Close()
	var home, exe string
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		k, v, ok := strings.Cut(sc.Text(), "=")
		if !ok {
			continue
		}
		switch strings.TrimSpace(k) {
		case "executable":
			exe = strings.TrimSpace(v)
		case "home":
			home = strings.TrimSpace(v)
		}
	}
	for _, c := range []string{exe, filepath.Join(home, pyName()), filepath.Join(home, "python3")} {
		if c == "" || c == pyName() || c == "python3" {
			continue
		}
		if _, err := os.Stat(c); err == nil {
			return c
		}
	}
	return ""
}

func pyName() string {
	if runtime.GOOS == "windows" {
		return "python.exe"
	}
	return "python"
}

// pipelineVersionOf runs `python -m maktaba_pipeline --version` (tests replace it).
var pipelineVersionOf = func(ctx context.Context, py string) ([]byte, error) {
	ctx, cancel := context.WithTimeout(ctx, 2*time.Minute)
	defer cancel()
	return exec.CommandContext(ctx, py, "-m", "maktaba_pipeline", "--version").CombinedOutput()
}

// smokePipeline runs the staged release's pipeline once (story 35.4 AC5): its
// environment holds the release's pipeline, which says the version of its wheel. That
// is the release's in PEP 440's spelling (1.2.3-rc.1 is 1.2.3rc1: the release build
// stamps the wheel's), so the wheel's name is what it is compared with.
func smokePipeline(ctx context.Context, staged, version string) error {
	py := venvPython(filepath.Join(staged, "venv"))
	if _, err := os.Stat(py); err != nil {
		return nil // a release without its own environment
	}
	want := layout.NormalizeVersion(version)
	if wheel, err := appvenv.Wheel(staged); err == nil {
		want = appvenv.WheelVersion(wheel)
	}
	out, err := pipelineVersionOf(ctx, py)
	if err != nil {
		return Fail("smoke_failed", "the pipeline (%s) did not run: %v: %s", py, err, tailBytes(out, 400))
	}
	if got := lastLine(out); got != want {
		return Fail("smoke_version", "the pipeline says %q, not %s", tailBytes([]byte(got), 200), want)
	}
	return nil
}

// lastLine is the last non-empty line of out, trimmed.
func lastLine(out []byte) string {
	lines := strings.Split(strings.TrimSpace(string(out)), "\n")
	return strings.TrimSpace(lines[len(lines)-1])
}

func tailBytes(b []byte, n int) string {
	if len(b) > n {
		b = b[len(b)-n:]
	}
	return string(b)
}
