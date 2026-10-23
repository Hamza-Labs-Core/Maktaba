package appvenv

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/signal"
	"path/filepath"
	"runtime"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/Hamza-Labs-Core/Maktaba/cmd/maktaba-server/internal/ctrlbreak"
)

// In these tests the test binary is the payload's Python: run as
// `-m maktaba_pipeline.venv_bootstrap ...` it plays the bootstrap (TestMain),
// doing what <payload>/fake-mode says and noting each run in
// <payload>/fake-calls.jsonl. Its environment is the one Run gives the
// bootstrap, so the mode travels in a file.
func TestMain(m *testing.M) {
	if len(os.Args) > 2 && os.Args[1] == "-m" && os.Args[2] == "maktaba_pipeline.venv_bootstrap" {
		os.Exit(fakeBootstrap(os.Args[3:]))
	}
	os.Exit(m.Run())
}

type fakeCall struct {
	Args []string `json:"args"`
	Env  []string `json:"env"`
}

func fakeBootstrap(args []string) int {
	payload, check := "", false
	for i, a := range args {
		switch a {
		case "--payload":
			payload = filepath.Dir(args[i+1])
		case "--check":
			check = true
		}
	}
	if raw, err := json.Marshal(fakeCall{Args: args, Env: os.Environ()}); err == nil {
		if f, err := os.OpenFile(filepath.Join(payload, "fake-calls.jsonl"), os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o600); err == nil {
			_, _ = f.Write(append(raw, '\n'))
			_ = f.Close()
		}
	}
	mode, _ := os.ReadFile(filepath.Join(payload, "fake-mode"))
	out := bufio.NewWriter(os.Stdout)
	say := func(state, step, msg, errText string, progress float64) {
		raw, _ := json.Marshal(map[string]any{"event": "status", "state": state, "step": step,
			"progress": progress, "message": msg, "error": errText, "done": 61, "total": 153})
		_, _ = out.Write(append(raw, '\n'))
		_ = out.Flush()
	}
	m := strings.TrimSpace(string(mode))
	if check {
		if m == "check-ok" {
			return 0
		}
		return 10
	}
	switch m {
	case "build-ok":
		say("building", "deps", "Collecting torch", "", 0.41)
		say("ready", "done", "maktaba-pipeline 1.4.0 ready", "", 1)
		return 0
	case "fail":
		say("building", "deps", "Collecting torch", "", 0.41)
		say("failed", "deps", "deps failed (exit 1):", "deps failed (exit 1):\none\ntwo\nERROR: no torch", 0.41)
		return 1
	case "exit130":
		say("interrupted", "deps", "stopped; the next run resumes", "", 0.41)
		return exitInterrupted
	case "wait":
		sig := make(chan os.Signal, 1)
		signal.Notify(sig, os.Interrupt, syscall.SIGTERM)
		say("building", "deps", "Collecting torch", "", 0.41)
		select {
		case <-sig:
			_ = os.WriteFile(filepath.Join(payload, "fake-stopped"), []byte("stopped"), 0o600)
			say("interrupted", "deps", "stopped; the next run resumes", "", 0.41)
			return exitInterrupted
		case <-time.After(20 * time.Second):
			return 3
		}
	}
	return 2
}

// fakePayload is a payload whose Python is this test binary, doing mode.
func fakePayload(t *testing.T, mode string) string {
	t.Helper()
	if testing.Short() {
		t.Skip("runs the stand-in bootstrap; runs without -short")
	}
	payload := t.TempDir()
	py := Python(payload)
	for _, d := range []string{filepath.Dir(py), filepath.Join(payload, "pipeline")} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	self, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	// a link where it can be (Windows cannot remove a link to a running program:
	// this test binary is one there)
	if runtime.GOOS == "windows" || os.Link(self, py) != nil {
		copyFile(t, self, py)
	}
	for name, body := range map[string]string{
		"pipeline/maktaba_pipeline-1.4.0-py3-none-any.whl": "wheel",
		"payload.json": `{"python":{"version":"3.12.15"}}`,
		"fake-mode":    mode,
	} {
		if err := os.WriteFile(filepath.Join(payload, filepath.FromSlash(name)), []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return payload
}

func copyFile(t *testing.T, from, to string) {
	t.Helper()
	in, err := os.Open(from)
	if err != nil {
		t.Fatal(err)
	}
	defer in.Close()
	out, err := os.OpenFile(to, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0o755)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := io.Copy(out, in); err != nil {
		t.Fatal(err)
	}
	if err := out.Close(); err != nil {
		t.Fatal(err)
	}
}

func calls(t *testing.T, payload string) []fakeCall {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join(payload, "fake-calls.jsonl"))
	if err != nil {
		t.Fatal(err)
	}
	var out []fakeCall
	for _, line := range strings.Split(strings.TrimSpace(string(raw)), "\n") {
		var c fakeCall
		if err := json.Unmarshal([]byte(line), &c); err != nil {
			t.Fatal(err)
		}
		out = append(out, c)
	}
	return out
}

func spec(payload string) Spec {
	return Spec{Payload: payload, Venv: filepath.Join(payload, "venv")}
}

// The bootstrap gets the payload's Python as --python and an environment of its
// own: the server's MAKTABA_PYTHON, extras, venv and PYTHONHOME do not reach it.
func TestRunGivesTheBootstrapItsOwnEnvironment(t *testing.T) {
	payload := fakePayload(t, "build-ok")
	t.Setenv("MAKTABA_PYTHON", "/elsewhere/python3")
	t.Setenv("MAKTABA_PIPELINE_EXTRAS", "dev")
	t.Setenv("MAKTABA_VENV", "/elsewhere/venv")
	t.Setenv("PYTHONHOME", "/elsewhere")
	t.Setenv("HTTPS_PROXY", "http://proxy.lan:3128")
	var seen []Progress
	res, err := Run(context.Background(), spec(payload), func(p Progress) { seen = append(seen, p) })
	if err != nil || res.Reused || res.Pipeline != "1.4.0" || res.Python != "3.12.15" {
		t.Fatalf("Run = %+v, %v", res, err)
	}
	if len(seen) != 2 || seen[0].Fraction != 0.41 || seen[0].Message != "Collecting torch" || seen[0].Step != "deps" ||
		seen[0].Done != 61 || seen[0].Total != 153 {
		t.Fatalf("progress = %+v", seen)
	}
	cs := calls(t, payload)
	if len(cs) != 2 || cs[0].Args[len(cs[0].Args)-1] != "--check" {
		t.Fatalf("calls = %+v (a --check, then the run)", cs)
	}
	args := strings.Join(cs[1].Args, " ")
	if !strings.Contains(args, "--python "+Python(payload)+" ") || !strings.Contains(args, "--venv "+filepath.Join(payload, "venv")) {
		t.Fatalf("args = %s", args)
	}
	env := map[string]string{}
	for _, kv := range cs[1].Env {
		k, v, _ := strings.Cut(kv, "=")
		if runtime.GOOS == "windows" {
			k = strings.ToUpper(k)
		}
		env[k] = v
	}
	for _, gone := range []string{"MAKTABA_PYTHON", "MAKTABA_PIPELINE_EXTRAS", "MAKTABA_VENV", "PYTHONHOME"} {
		if v, ok := env[gone]; ok {
			t.Errorf("%s=%q reached the bootstrap", gone, v)
		}
	}
	wheel, _ := Wheel(payload)
	if env["PYTHONPATH"] != wheel || env["PYTHONUTF8"] != "1" || env["PYTHONNOUSERSITE"] != "1" ||
		env["HTTPS_PROXY"] != "http://proxy.lan:3128" || env["PATH"] == "" {
		t.Errorf("env = %v", env)
	}
}

// Up to date: --check alone, a reuse.
func TestRunReusesAnUpToDateVenv(t *testing.T) {
	payload := fakePayload(t, "check-ok")
	res, err := Run(context.Background(), spec(payload), nil)
	if err != nil || !res.Reused {
		t.Fatalf("Run = %+v, %v", res, err)
	}
	if n := len(calls(t, payload)); n != 1 {
		t.Fatalf("%d runs, want the --check alone", n)
	}
}

// A failed run is an Error with the failing step's last lines; an exit 130
// (stopped from elsewhere: a console closed) is an interruption, never a failure.
func TestRunFailureAndInterruption(t *testing.T) {
	_, err := Run(context.Background(), spec(fakePayload(t, "fail")), nil)
	var ve *Error
	if !errors.As(err, &ve) || ve.Message != "one\ntwo\nERROR: no torch" {
		t.Fatalf("failure = %#v", err)
	}
	_, err = Run(context.Background(), spec(fakePayload(t, "exit130")), nil)
	if !errors.Is(err, ErrInterrupted) {
		t.Fatalf("exit 130 = %v, want ErrInterrupted", err)
	}
}

// A run stopped by its context asks the bootstrap to stop (SIGTERM; on Windows
// CTRL_BREAK to its own process group) rather than killing it: the bootstrap
// stops pip and says so, and Run returns ErrInterrupted.
func TestRunStoppedAsksTheBootstrapToStop(t *testing.T) {
	payload := fakePayload(t, "wait")
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan error, 1)
	started := make(chan struct{}, 1)
	go func() {
		_, err := Run(ctx, spec(payload), func(Progress) {
			select {
			case started <- struct{}{}:
			default:
			}
		})
		done <- err
	}()
	select {
	case <-started:
	case <-time.After(20 * time.Second):
		t.Fatal("the bootstrap never said it was building")
	}
	t0 := time.Now()
	cancel()
	var err error
	select {
	case err = <-done:
	case <-time.After(30 * time.Second):
		t.Fatal("Run did not end after its context")
	}
	if !errors.Is(err, ErrInterrupted) {
		t.Fatalf("Run = %v, want ErrInterrupted", err)
	}
	if _, serr := os.Stat(filepath.Join(payload, "fake-stopped")); serr != nil {
		if !ctrlbreak.CanAsk() {
			t.Skip("no console here to send CTRL_BREAK through: the bootstrap was killed instead")
		}
		t.Fatalf("the bootstrap was not asked to stop (killed): %v", serr)
	}
	if d := time.Since(t0); d > 10*time.Second {
		t.Fatalf("the stop took %v (a kill after the wait, not the stop asked for)", d)
	}
}

func TestBootstrapEnvKeepsOnlyItsList(t *testing.T) {
	env := bootstrapEnv([]string{"PATH=/bin", `=C:=C:\x`, "MAKTABA_PYTHON=/p", "VIRTUAL_ENV=/v",
		"PIP_INDEX_URL=https://mirror", "PIP_USER=1", "HOME=/h", "PYTHONPATH=/elsewhere"}, "/w.whl")
	got := strings.Join(env, " ")
	want := "PATH=/bin PIP_INDEX_URL=https://mirror HOME=/h PYTHONPATH=/w.whl PYTHONUTF8=1 PYTHONNOUSERSITE=1"
	if got != want {
		t.Fatalf("env = %s\nwant  %s", got, want)
	}
	more := strings.Join(bootstrapEnv([]string{"NETRC=/n", "MAKTABA_VENV_KEEP_ENV=NETRC, UV_INDEX_URL", "OTHER=1"}, "/w.whl"), " ")
	if more != "NETRC=/n PYTHONPATH=/w.whl PYTHONUTF8=1 PYTHONNOUSERSITE=1" {
		t.Fatalf("MAKTABA_VENV_KEEP_ENV: env = %s", more)
	}
	if runtime.GOOS == "windows" {
		env := fmt.Sprint(bootstrapEnv([]string{`SystemRoot=C:\Windows`, `Path=C:\x`}, "w"))
		if !strings.Contains(env, "SystemRoot=") || !strings.Contains(env, "Path=") {
			t.Fatalf("Windows names are matched without case: %v", env)
		}
	}
}

// Review round 2 (B-H1): the Windows installer's update deletes nothing, so an app
// installed over an older one may hold the older wheel too; the one payload.json names is
// the payload's. A folder with no payload.json beside it (a plain server's release) has
// its only wheel.
func TestWheelIsTheOnePayloadJSONNames(t *testing.T) {
	payload := t.TempDir()
	pipe := filepath.Join(payload, "pipeline")
	if err := os.MkdirAll(pipe, 0o755); err != nil {
		t.Fatal(err)
	}
	for _, w := range []string{"maktaba_pipeline-1.4.0-py3-none-any.whl", "maktaba_pipeline-1.5.0rc1-py3-none-any.whl"} {
		if err := os.WriteFile(filepath.Join(pipe, w), nil, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := Wheel(payload); err == nil {
		t.Fatal("two wheels and no payload.json: one picked")
	}
	pj := `{"version": "v1.5.0-rc.1", "pipeline": {"wheel": "maktaba_pipeline-1.5.0rc1-py3-none-any.whl"}}`
	if err := os.WriteFile(filepath.Join(payload, "payload.json"), []byte(pj), 0o644); err != nil {
		t.Fatal(err)
	}
	w, err := Wheel(payload)
	if err != nil || filepath.Base(w) != "maktaba_pipeline-1.5.0rc1-py3-none-any.whl" || WheelVersion(w) != "1.5.0rc1" {
		t.Fatalf("wheel %q (%v)", w, err)
	}
	_ = os.Remove(filepath.Join(pipe, "maktaba_pipeline-1.5.0rc1-py3-none-any.whl"))
	if _, err := Wheel(payload); err == nil || !strings.Contains(err.Error(), "does not hold") {
		t.Fatalf("the named wheel is gone: %v", err)
	}
	_ = os.Remove(filepath.Join(payload, "payload.json"))
	if w, err := Wheel(payload); err != nil || filepath.Base(w) != "maktaba_pipeline-1.4.0-py3-none-any.whl" {
		t.Fatalf("the only wheel: %q (%v)", w, err)
	}
}
