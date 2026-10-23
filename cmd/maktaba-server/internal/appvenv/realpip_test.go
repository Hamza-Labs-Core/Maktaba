package appvenv

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
)

// envRealVenv names a folder with a release's pipeline wheel and its constraints
// (deploy/packaging/node/build-release.sh's dist: maktaba_pipeline-<v>-py3-none-any.whl,
// maktaba-pipeline-<v>-constraints.txt). CI's node-packaging gate sets it; elsewhere the
// test skips (it downloads every pinned package from PyPI).
const envRealVenv = "MAKTABA_TEST_REAL_VENV"

// envRealVenvPython names the Python the venv is built with (default: python3.12, then
// python3, then python on PATH).
const envRealVenvPython = "MAKTABA_TEST_REAL_VENV_PYTHON"

// A plain server's update builds the release's pipeline environment as this does (Epic 35
// design §3.4, review round 2): Run with the machine's own Python and the release's
// wheel and plain constraints in <release>/pipeline under their asset names, the real
// bootstrap from the wheel, every package from PyPI checked against its hash. The venv's
// pipeline then says the wheel's version, and a second run finds it up to date.
func TestRealVenvFromARelease(t *testing.T) {
	dist := os.Getenv(envRealVenv)
	if dist == "" {
		t.Skipf("%s is not set: a release's wheel and constraints, and PyPI, are needed", envRealVenv)
	}
	wheels, _ := filepath.Glob(filepath.Join(dist, "maktaba_pipeline-*-py3-none-any.whl"))
	if len(wheels) != 1 {
		t.Fatalf("%s holds %d pipeline wheels, want 1", dist, len(wheels))
	}
	cons, _ := filepath.Glob(filepath.Join(dist, "maktaba-pipeline-*-constraints.txt"))
	if len(cons) != 1 {
		t.Fatalf("%s holds %d plain constraints files, want 1", dist, len(cons))
	}
	py := os.Getenv(envRealVenvPython)
	for _, name := range []string{"python3.12", "python3", "python"} {
		if py != "" {
			break
		}
		py, _ = exec.LookPath(name)
	}
	if py == "" {
		t.Fatal("no Python on PATH")
	}

	// <release>/pipeline as the update downloads it: the wheel and the plain constraints
	// (a machine without the CUDA set's choice; the bootstrap takes torch's CPU build)
	release := t.TempDir()
	pipeline := filepath.Join(release, "pipeline")
	if err := os.MkdirAll(pipeline, 0o755); err != nil {
		t.Fatal(err)
	}
	for _, src := range []string{wheels[0], cons[0]} {
		copyFile(t, src, filepath.Join(pipeline, filepath.Base(src)))
	}
	want := WheelVersion(wheels[0])

	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Minute)
	defer cancel()
	spec := Spec{Python: py, Pipeline: pipeline, Venv: filepath.Join(release, "venv"), Log: testLog{t}}
	steps := map[string]bool{}
	res, err := Run(ctx, spec, func(p Progress) { steps[p.Step] = true })
	if err != nil {
		t.Fatalf("the release's venv: %v", err)
	}
	if res.Reused || res.Pipeline != want {
		t.Fatalf("result %+v, want a build of the pipeline %s", res, want)
	}
	t.Logf("built with %s; steps %v", py, steps)

	vpy := filepath.Join(spec.Venv, "bin", "python")
	if runtime.GOOS == "windows" {
		vpy = filepath.Join(spec.Venv, "Scripts", "python.exe")
	}
	cmd := exec.CommandContext(ctx, vpy, "-m", "maktaba_pipeline", "--version")
	cmd.Dir = release // not the checkout: nothing on sys.path but the venv
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("%s -m maktaba_pipeline --version: %v: %s", vpy, err, out)
	}
	lines := strings.Split(strings.TrimSpace(string(out)), "\n")
	if got := strings.TrimSpace(lines[len(lines)-1]); got != want {
		t.Fatalf("the venv's pipeline says %q, want the wheel's %s", got, want)
	}

	again, err := Run(ctx, spec, nil)
	if err != nil || !again.Reused {
		t.Fatalf("a second run: %+v, %v (want it up to date)", again, err)
	}
}

// testLog is the bootstrap's stderr in the test's log.
type testLog struct{ t *testing.T }

func (l testLog) Write(p []byte) (int, error) {
	l.t.Log(strings.TrimRight(string(p), "\n"))
	return len(p), nil
}
