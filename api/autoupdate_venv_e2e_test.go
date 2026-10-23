package main

import (
	"archive/zip"
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// The end-to-end update's pipeline (review A12: an update updates the pipeline too):
// every release carries a small maktaba_pipeline wheel whose venv_bootstrap makes a venv
// of the machine's Python offline (python -m venv, the wheel unpacked into it) and whose
// module answers --version and the supervisor's /healthz probe. The update's stage builds
// <release>/venv from that wheel, verified, with the real Go side; the pipeline then runs
// from the release's venv.

const fakeBootstrap = `import argparse, glob, json, os, subprocess, sys, zipfile
p = argparse.ArgumentParser()
p.add_argument("--python"); p.add_argument("--payload"); p.add_argument("--venv")
p.add_argument("--check", action="store_true")
a = p.parse_args()
py = os.path.join(a.venv, "Scripts", "python.exe") if os.name == "nt" else os.path.join(a.venv, "bin", "python")
ok = os.path.join(a.venv, ".e2e-ok")
if a.check:
    sys.exit(0 if os.path.exists(py) and os.path.exists(ok) else 1)
subprocess.check_call([a.python, "-m", "venv", "--without-pip", a.venv])
wheel = glob.glob(os.path.join(a.payload, "maktaba_pipeline-*.whl"))[0]
site = subprocess.check_output([py, "-c", "import sysconfig; print(sysconfig.get_paths()['purelib'])"], text=True).strip()
zipfile.ZipFile(wheel).extractall(site)
open(ok, "w").close()
print(json.dumps({"event": "status", "state": "ready", "step": "done", "progress": 1.0, "message": "ready"}))
`

const fakePipelineMain = `import os, sys, time
from http.server import BaseHTTPRequestHandler, HTTPServer
from . import __version__
if "--version" in sys.argv[1:] or "-V" in sys.argv[1:]:
    print(__version__)
    sys.exit(0)
class Health(BaseHTTPRequestHandler):
    def do_GET(self):
        self.send_response(200)
        self.end_headers()
        self.wfile.write(b"ok")
    def log_message(self, *args):
        pass
addr = os.environ.get("MAKTABA_PIPELINE_LOGS_ADDR", "")
if addr:
    host, _, port = addr.rpartition(":")
    HTTPServer((host or "127.0.0.1", int(port)), Health).serve_forever()
while True:
    time.sleep(3600)
`

// fakeWheel is the e2e's maktaba_pipeline wheel of version.
func fakeWheel(t *testing.T, version string) []byte {
	t.Helper()
	var buf bytes.Buffer
	z := zip.NewWriter(&buf)
	for name, body := range map[string]string{
		"maktaba_pipeline/__init__.py":       "__version__ = \"" + version + "\"\n",
		"maktaba_pipeline/__main__.py":       fakePipelineMain,
		"maktaba_pipeline/venv_bootstrap.py": fakeBootstrap,
	} {
		w, err := z.Create(name)
		if err != nil {
			t.Fatal(err)
		}
		_, _ = w.Write([]byte(body))
	}
	if err := z.Close(); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

// pipelinePython is a Python 3 with venv on this machine (not the Store's stand-in on
// Windows); the run is skipped without one.
func pipelinePython(t *testing.T) string {
	t.Helper()
	names := []string{"python3", "python"}
	if runtime.GOOS == "windows" {
		names = []string{"python", "python3"}
	}
	for _, n := range names {
		p, err := exec.LookPath(n)
		if err != nil {
			continue
		}
		if out, err := exec.Command(p, "-c", "import venv, sys; print(sys.version_info[0])").Output(); err == nil &&
			strings.TrimSpace(string(out)) == "3" {
			return p
		}
	}
	t.Skip("no Python 3 with venv for the pipeline's environment")
	return ""
}

// makeVenv builds a release's venv as its stage would (the first install, which the
// update did not stage): the wheel's own bootstrap.
func makeVenv(t *testing.T, python string, wheel []byte, venv string) {
	t.Helper()
	dir := t.TempDir()
	path := filepath.Join(dir, "maktaba_pipeline-0.0.0-py3-none-any.whl")
	if err := os.WriteFile(path, wheel, 0o644); err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command(python, "-m", "maktaba_pipeline.venv_bootstrap", "--python", python, "--payload", dir, "--venv", venv)
	cmd.Env = append(os.Environ(), "PYTHONPATH="+path)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("the first install's venv: %v\n%s", err, out)
	}
}

// releaseVenvBuilt reports whether a release directory holds the venv its stage built.
func releaseVenvBuilt(dir string) bool {
	_, err := os.Stat(filepath.Join(dir, "venv", ".e2e-ok"))
	return err == nil
}
