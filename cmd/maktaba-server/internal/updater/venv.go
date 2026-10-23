package updater

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"sort"
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
	extras := li.extras(ctx)
	_, _ = fmt.Fprintf(log, "[update] building the pipeline's environment with %s (extras: %s)\n", py, orDefault(extras))
	said := -1
	_, err := venvBuild(ctx, appvenv.Spec{Python: py, Pipeline: pipe, Venv: filepath.Join(dir, "venv"), Log: log,
		Extras: extras},
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
		if networkFailure(err.Error()) {
			// the machine's, not the release's: a person gives it PyPI (or a mirror)
			return Fail("venv_network", "the pipeline's Python packages could not be downloaded: this machine cannot "+
				"reach PyPI (or the mirror or proxy it is given: PIP_INDEX_URL, HTTPS_PROXY): %v", err)
		}
		return Fail("venv_failed", "%v", err)
	}
	return nil
}

// networkFailure says pip could not download the packages (no route to PyPI, a name
// that does not resolve, a proxy or a certificate in the way), not that they failed.
func networkFailure(msg string) bool {
	msg = strings.ToLower(msg)
	for _, s := range []string{"could not find a version that satisfies", "no matching distribution found",
		"failed to establish a new connection", "max retries exceeded", "name or service not known",
		"temporary failure in name resolution", "getaddrinfo failed", "nodename nor servname",
		"network is unreachable", "connection refused", "connection timed out", "read timed out",
		"connectionerror", "connection reset", "proxyerror", "sslerror", "certificate verify failed"} {
		if strings.Contains(msg, s) {
			return true
		}
	}
	return false
}

func orDefault(extras string) string {
	if extras == "" {
		return "the default"
	}
	return extras
}

// extras are the pipeline's extras the environment the release replaces has (review
// A12: a converted worker keeps what it was given): those its bootstrap recorded
// (maktaba-venv.json), else those whose requirements it has installed; "" (the
// bootstrap's default) with no environment to ask. The release's constraints cover every
// extra but dev.
func (li *LayoutInstaller) extras(ctx context.Context) string {
	py := li.currentPython()
	if py == "" {
		return ""
	}
	var names []string
	if raw, err := os.ReadFile(filepath.Join(filepath.Dir(filepath.Dir(py)), venvStamp)); err == nil {
		var st struct {
			Extras []string `json:"extras"`
		}
		if json.Unmarshal(raw, &st) == nil {
			names = st.Extras
		}
	}
	if names == nil {
		names = probeExtras(ctx, py)
	}
	var keep []string
	for _, n := range names {
		if n = strings.TrimSpace(n); n != "" && n != "dev" && strings.Trim(n, "abcdefghijklmnopqrstuvwxyz0123456789-_.") == "" {
			keep = append(keep, n)
		}
	}
	sort.Strings(keep)
	return strings.Join(keep, ",")
}

// venvStamp is the pipeline bootstrap's record of a venv it built (its extras).
const venvStamp = "maktaba-venv.json"

// currentPython is the interpreter of the pipeline's environment now: the running
// release's own, else the configured [pipeline] python; "" when neither is there.
func (li *LayoutInstaller) currentPython() string {
	for _, py := range []string{venvPython(filepath.Join(li.Layout.ReleaseDir(li.Running()), "venv")), li.PipelinePython} {
		if py == "" {
			continue
		}
		if _, err := os.Stat(py); err == nil {
			return py
		}
	}
	return ""
}

// probeExtras asks an environment which of the pipeline's extras it has every
// requirement of (tests replace it).
var probeExtras = func(ctx context.Context, py string) []string {
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	out, err := exec.CommandContext(ctx, py, "-I", "-c", extrasProbe).Output()
	if err != nil {
		return nil
	}
	var names []string
	if json.Unmarshal([]byte(lastLine(out)), &names) != nil {
		return nil
	}
	return names
}

// extrasProbe prints the extras of maktaba-pipeline whose requirements (for this
// platform) are all installed.
const extrasProbe = `
import json
from importlib import metadata
try:
    from packaging.requirements import Requirement
except ImportError:
    from pip._vendor.packaging.requirements import Requirement
dist = metadata.distribution("maktaba-pipeline")
def installed(r):
    try:
        metadata.version(r.name)
        return True
    except metadata.PackageNotFoundError:
        return False
have = []
for extra in dist.metadata.get_all("Provides-Extra") or []:
    reqs = [r for r in map(Requirement, dist.requires or [])
            if r.marker is not None and r.marker.evaluate({"extra": extra}) and not r.marker.evaluate({"extra": ""})]
    if reqs and all(installed(r) for r in reqs):
        have.append(extra)
print(json.dumps(have))
`

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
