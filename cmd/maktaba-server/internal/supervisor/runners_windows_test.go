//go:build windows

package supervisor

import (
	"context"
	"encoding/binary"
	"encoding/json"
	"io"
	"net"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/Hamza-Labs-Core/Maktaba/cmd/maktaba-server/internal/config"
	"github.com/Hamza-Labs-Core/Maktaba/cmd/maktaba-server/internal/pluginrelay"
)

func writeFrame(t *testing.T, w io.Writer, v any) {
	t.Helper()
	body, _ := json.Marshal(v)
	head := make([]byte, 4)
	binary.BigEndian.PutUint32(head, uint32(len(body)))
	if _, err := w.Write(append(head, body...)); err != nil {
		t.Errorf("write: %v", err)
	}
}

func readFrame(r io.Reader) (map[string]any, error) {
	head := make([]byte, 4)
	if _, err := io.ReadFull(r, head); err != nil {
		return nil, err
	}
	body := make([]byte, binary.BigEndian.Uint32(head))
	if _, err := io.ReadFull(r, body); err != nil {
		return nil, err
	}
	var out map[string]any
	return out, json.Unmarshal(body, &out)
}

// fakeRunner waits on a runner's back pipe as the runner does from its AppContainer: once
// handed a caller (Ready), it answers one hello.
func fakeRunner(t *testing.T, back string) {
	t.Helper()
	c, err := pluginrelay.Dial(back)
	if err != nil {
		t.Errorf("dial the back pipe: %v", err)
		return
	}
	go func() {
		defer c.Close()
		one := make([]byte, 1)
		if _, err := io.ReadFull(c, one); err != nil || one[0] != pluginrelay.Ready {
			return
		}
		if msg, err := readFrame(c); err == nil && msg["type"] == "hello" {
			writeFrame(t, c, map[string]any{"type": "hello", "group": "light"})
		}
	}()
}

// Story 54.23 on Windows: a runner is built as the launcher's runner-sandbox-run around the
// interpreter, with its back pipe and its way to the plugin API in its environment; the
// supervisor's health check and the worker's pipe both reach the runner waiting on its back
// pipe; with none waiting, the worker's connection is closed without Ready (it runs the job
// itself); and the worker learns the pipes' prefix.
func TestARunnersPipesOnWindows(t *testing.T) {
	cfg := config.Config{}
	cfg.Database.URL = "postgres://h/db"
	cfg.Server.DataDir = t.TempDir()
	ps, err := BuildPluginRunners(Options{Config: cfg})
	if err != nil {
		t.Fatal(err)
	}
	var light *Process
	for i := range ps {
		if ps[i].Instance == "light" {
			light = &ps[i]
		}
	}
	if light == nil {
		t.Fatalf("no light runner in %d", len(ps))
	}
	args := light.Cmd.Args
	if len(args) < 10 || args[1] != "runner-sandbox-run" || args[3] != "light" ||
		args[5] != filepath.Join(RunnerDir(cfg), "light") ||
		args[9] != filepath.Join(cfg.Server.DataDir, "run", "runner-light.grants") ||
		!slices.Contains(args, filepath.Join(cfg.Server.DataDir, "supervisor")) {
		t.Fatalf("command %v", args)
	}
	env := map[string]string{}
	for _, kv := range light.Cmd.Env {
		k, v, _ := strings.Cut(kv, "=")
		env[k] = v
	}
	prefix := RunnerPipePrefix()
	back, api := env[EnvRunnerBackPipe], env[EnvRunnerAPIPipe]
	if prefix == "" || !strings.HasPrefix(back, prefix) || !strings.HasSuffix(back, "-back") ||
		!strings.HasPrefix(api, prefix) || !strings.HasSuffix(api, "-api") {
		t.Fatalf("pipes: prefix %q back %q api %q", prefix, back, api)
	}
	caller := strings.TrimPrefix(light.Probe, "pipe:")
	if caller == light.Probe || caller != strings.TrimSuffix(back, "-back") {
		t.Fatalf("probe %q", light.Probe)
	}

	// no runner waiting: the worker's connection ends without Ready
	c, err := pluginrelay.Dial(caller)
	if err != nil {
		t.Fatal(err)
	}
	start := time.Now()
	if n, err := c.Read(make([]byte, 1)); n != 0 || err == nil {
		t.Fatalf("read %d bytes (%v) with no runner", n, err)
	}
	_ = c.Close()
	if time.Since(start) > 3*RunnerWait {
		t.Fatalf("the close took %v", time.Since(start))
	}

	// the supervisor's check
	fakeRunner(t, back)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := light.Check(ctx); err != nil {
		t.Fatalf("the check through the broker: %v", err)
	}

	// the worker's pipe
	fakeRunner(t, back)
	w, err := pluginrelay.Dial(caller)
	if err != nil {
		t.Fatal(err)
	}
	defer w.Close()
	one := make([]byte, 1)
	if _, err := io.ReadFull(w, one); err != nil || one[0] != pluginrelay.Ready {
		t.Fatalf("no Ready: %v %v", one, err)
	}
	writeFrame(t, w, map[string]any{"type": "hello"})
	if msg, err := readFrame(w); err != nil || msg["group"] != "light" {
		t.Fatalf("hello through the worker's pipe: %v %v", msg, err)
	}

	// the API pipe relays to the plugin API's AF_UNIX socket
	sock := PluginsPaths(cfg).API
	_ = os.MkdirAll(filepath.Dir(sock), 0o700)
	l, err := net.Listen("unix", sock)
	if err != nil {
		t.Fatal(err)
	}
	defer l.Close()
	go func() {
		s, err := l.Accept()
		if err == nil {
			_, _ = io.Copy(s, s)
		}
	}()
	a, err := pluginrelay.Dial(api)
	if err != nil {
		t.Fatal(err)
	}
	defer a.Close()
	if _, err := io.ReadFull(a, one); err != nil || one[0] != pluginrelay.Ready {
		t.Fatalf("no Ready from the API pipe: %v", err)
	}
	_, _ = a.Write([]byte("ping"))
	got := make([]byte, 4)
	if _, err := io.ReadFull(a, got); err != nil || string(got) != "ping" {
		t.Fatalf("through the API pipe: %q %v", got, err)
	}

	// the worker's environment names the prefix
	wenv, err := ChildEnv(RolePipeline, cfg)
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, kv := range wenv {
		found = found || kv == EnvRunnerPipes+"="+prefix
	}
	if !found {
		t.Fatalf("the worker has no %s", EnvRunnerPipes)
	}
}
