//go:build windows

package supervisor

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Hamza-Labs-Core/Maktaba/cmd/maktaba-server/internal/config"
	"github.com/Hamza-Labs-Core/Maktaba/cmd/maktaba-server/internal/pluginrelay"
)

// canary counts the connections that exchanged data with it (two-way: a bind or a send can
// succeed inside an AppContainer while the Windows Filtering Platform drops the traffic).
type canary struct {
	l    net.Listener
	hits atomic.Int32
}

func newCanary(t *testing.T, host string) *canary {
	t.Helper()
	l, err := net.Listen("tcp", net.JoinHostPort(host, "0"))
	if err != nil {
		t.Fatal(err)
	}
	c := &canary{l: l}
	go func() {
		for {
			conn, err := l.Accept()
			if err != nil {
				return
			}
			go func() {
				defer conn.Close()
				_ = conn.SetDeadline(time.Now().Add(5 * time.Second))
				b := make([]byte, 4)
				if _, err := io.ReadFull(conn, b); err == nil && string(b) == "ping" {
					if _, err := conn.Write([]byte("pong")); err == nil {
						c.hits.Add(1)
					}
				}
			}()
		}
	}()
	t.Cleanup(func() { _ = l.Close() })
	return c
}

func lanAddress() string {
	addrs, _ := net.InterfaceAddrs()
	for _, a := range addrs {
		if ip, ok := a.(*net.IPNet); ok && ip.IP.To4() != nil && !ip.IP.IsLoopback() && !ip.IP.IsLinkLocalUnicast() {
			return ip.IP.String()
		}
	}
	return ""
}

// Story 54.23 on Windows, end to end (CI's plugins-sandbox job, never a developer's or the
// owner's machine): a runner group started as the supervisor starts one, through the
// launcher's runner-sandbox-run (MAKTABA_PLUGINS_SANDBOX_RUN: CI's aclaunch, the same code),
// runs in its own AppContainer and Job Object. From inside (testdata/runner_probe.py, the
// runner's own sandbox and pipe code) it reads and writes only its granted folders, reaches
// no canary on loopback or the LAN address (no network without net), starts no process
// (its guard, and the Job Object past it), and opens neither another group's pipe nor the
// worker's; it reaches the plugin API through its API pipe, and the worker and the
// supervisor reach it through the broker. The same probes outside the sandbox reach all of
// it (the positive control). A group with net reaches the internet and still no canary.
func TestARunnerOnWindowsInItsAppContainer(t *testing.T) {
	python := os.Getenv("MAKTABA_RUNNER_E2E_PYTHON")
	if python == "" || os.Getenv("MAKTABA_PLUGINS_SANDBOX_RUN") == "" {
		if os.Getenv("MAKTABA_RUNNER_E2E_REQUIRE") == "1" {
			t.Fatal("MAKTABA_RUNNER_E2E_PYTHON and MAKTABA_PLUGINS_SANDBOX_RUN are required")
		}
		t.Skip("no interpreter or launcher (MAKTABA_RUNNER_E2E_PYTHON, MAKTABA_PLUGINS_SANDBOX_RUN)")
	}
	repo, err := filepath.Abs(filepath.Join("..", "..", "..", ".."))
	if err != nil {
		t.Fatal(err)
	}
	probeScript, _ := filepath.Abs(filepath.Join("testdata", "runner_probe.py"))
	dir, err := os.MkdirTemp("", "mkr")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(dir) })
	cfg := config.Config{}
	cfg.Server.DataDir = filepath.Join(dir, "data")
	cfg.Database.URL = "postgres://localhost/maktaba"
	for _, d := range []string{"read", "write", "deny", "secret", "data"} {
		if err := os.MkdirAll(filepath.Join(dir, d), 0o700); err != nil {
			t.Fatal(err)
		}
	}
	_ = os.WriteFile(filepath.Join(dir, "read", "granted.txt"), []byte("ok"), 0o600)
	secret := filepath.Join(dir, "secret", "id_ed25519")
	_ = os.WriteFile(secret, []byte("key"), 0o600)

	// the plugin API: an HTTP server on its AF_UNIX socket, as the API serves it
	apiSock := PluginsPaths(cfg).API
	_ = os.MkdirAll(filepath.Dir(apiSock), 0o700)
	apiL, err := net.Listen("unix", apiSock)
	if err != nil {
		t.Fatal(err)
	}
	var apiCalls atomic.Int32
	srv := &http.Server{Handler: http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		apiCalls.Add(1)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"ok":true}`))
	})}
	go func() { _ = srv.Serve(apiL) }()
	t.Cleanup(func() { _ = srv.Close() })

	loop := newCanary(t, "127.0.0.1")
	canaries := []*canary{loop}
	if lan := lanAddress(); lan != "" {
		canaries = append(canaries, newCanary(t, lan))
	}
	var addrs []string
	for _, c := range canaries {
		addrs = append(addrs, c.l.Addr().String())
	}
	other, err := runnerPipesFor(cfg, "e2e-other")
	if err != nil {
		t.Fatal(err)
	}

	baseEnv := func(net bool) []string {
		n := "0"
		if net {
			n = "1"
		}
		return append(os.Environ(),
			"PYTHONPATH="+filepath.Join(repo, "pipeline", "src")+";"+filepath.Join(repo, "plugins", "sdk", "python"),
			"PYTHONDONTWRITEBYTECODE=1",
			"MAKTABA_DATA_DIR="+cfg.Server.DataDir,
			"PROBE_READ_DIR="+filepath.Join(dir, "read"),
			"PROBE_WRITE_DIR="+filepath.Join(dir, "write"),
			"PROBE_DENY_DIR="+filepath.Join(dir, "deny"),
			"PROBE_RUN_DIR="+RunnerDir(cfg),
			"PROBE_SECRET="+secret,
			"PROBE_CANARIES="+strings.Join(addrs, ";"),
			"PROBE_OTHER_PIPE="+other.Back,
			"PROBE_NET="+n,
		)
	}

	// the positive control: outside the sandbox every probe reaches its target
	ctl := exec.Command(python, probeScript)
	ctl.Env = append(baseEnv(false), "PROBE_CONTROL=1", "PROBE_CALLER_PIPE="+other.Caller)
	out, err := ctl.Output()
	if err != nil {
		t.Fatalf("the control: %v", err)
	}
	var control map[string]string
	if err := json.Unmarshal(lastJSONLine(out), &control); err != nil {
		t.Fatalf("the control's output %q: %v", out, err)
	}
	for k, v := range control {
		if v != "allowed" {
			t.Errorf("control: %s %s (the probe itself is broken)", k, v)
		}
	}
	before := make([]int32, len(canaries))
	for i, c := range canaries {
		if before[i] = c.hits.Load(); before[i] == 0 {
			t.Errorf("control: canary %d never answered", i)
		}
	}
	if t.Failed() {
		t.FailNow()
	}

	start := func(group string, net bool) (map[string]any, *exec.Cmd) {
		t.Helper()
		pipes, err := runnerPipesFor(cfg, group)
		if err != nil {
			t.Fatal(err)
		}
		name, args := runnerCommand("windows", SandboxLauncher(), python, group, apiSock, RunnerDir(cfg),
			filepath.Join(cfg.Server.DataDir, "run", "runner-"+group+".grants"))
		// the probe in place of the runner module: the same launcher, the same pipes
		for i, a := range args {
			if a == "--" {
				args = append(append([]string{}, args[:i+2]...), probeScript)
				break
			}
		}
		cmd := exec.Command(name, args...)
		cmd.Env = append(baseEnv(net), EnvRunnerBackPipe+"="+pipes.Back, EnvRunnerAPIPipe+"="+pipes.API,
			"PROBE_CALLER_PIPE="+pipes.Caller)
		cmd.Stdout = os.Stderr
		stderr, _ := cmd.StderrPipe()
		go func() {
			sc := bufio.NewScanner(stderr)
			for sc.Scan() {
				t.Logf("[%s] %s", group, sc.Text())
			}
		}()
		if err := cmd.Start(); err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() {
			_ = cmd.Process.Kill() // the Job Object takes the runner with the launcher
			_, _ = cmd.Process.Wait()
		})
		exited := make(chan error, 1)
		go func() { exited <- cmd.Wait() }()
		ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
		defer cancel()
		for {
			select {
			case err := <-exited:
				t.Fatalf("%s: the runner exited before it served: %v", group, err)
			default:
			}
			hello, err := helloThrough(pipes.Caller)
			if err == nil {
				// the supervisor's own check reaches it too
				if err := askRunnerHelloVia(ctx, pipes.dial); err != nil {
					t.Fatalf("%s: the supervisor's check: %v", group, err)
				}
				return hello, cmd
			}
			if ctx.Err() != nil {
				t.Fatalf("%s: never served through its pipe: %v", group, err)
			}
			time.Sleep(time.Second)
		}
	}

	hello, _ := start("e2e", false)
	probes, _ := hello["probes"].(map[string]any)
	t.Logf("inside (no net): %v", probes)
	want := map[string]string{
		"sandbox": "full", "read_granted": "allowed", "write_granted": "allowed", "api": "allowed",
	}
	for k, v := range want {
		if probes[k] != v {
			t.Errorf("%s = %v, want %s", k, probes[k], v)
		}
	}
	for _, k := range []string{"read_secret", "write_elsewhere", "exec", "exec_kernel", "other_group_pipe", "own_caller_pipe"} {
		if s, _ := probes[k].(string); !strings.HasPrefix(s, "denied:") {
			t.Errorf("%s = %v, want denied", k, probes[k])
		}
	}
	for i := range canaries {
		if s, _ := probes[fmt.Sprintf("tcp_%d", i)].(string); !strings.HasPrefix(s, "denied:") {
			t.Errorf("tcp_%d = %v, want denied", i, s)
		}
	}
	if apiCalls.Load() == 0 {
		t.Error("the plugin API was never called")
	}

	netHello, _ := start("e2e-net", true)
	netProbes, _ := netHello["probes"].(map[string]any)
	t.Logf("inside (net): %v", netProbes)
	if netProbes["sandbox"] != "full" || netProbes["internet"] != "allowed" {
		t.Errorf("a net group: sandbox %v, internet %v", netProbes["sandbox"], netProbes["internet"])
	}
	for i, c := range canaries {
		if got := c.hits.Load(); got != before[i] {
			t.Errorf("canary %d answered %d sandboxed connections", i, got-before[i])
		}
	}
}

// helloThrough sends hello through a runner's caller pipe, as the worker does.
func helloThrough(pipe string) (map[string]any, error) {
	c, err := pluginrelay.Dial(pipe)
	if err != nil {
		return nil, err
	}
	defer c.Close()
	one := make([]byte, 1)
	if _, err := io.ReadFull(c, one); err != nil || one[0] != pluginrelay.Ready {
		return nil, fmt.Errorf("no Ready: %v", err)
	}
	body, _ := json.Marshal(map[string]any{"type": "hello"})
	head := []byte{byte(len(body) >> 24), byte(len(body) >> 16), byte(len(body) >> 8), byte(len(body))}
	if _, err := c.Write(append(head, body...)); err != nil {
		return nil, err
	}
	msg, err := readFrame(c)
	if err != nil {
		return nil, err
	}
	if msg["type"] != "hello" {
		return nil, fmt.Errorf("answered %v", msg)
	}
	return msg, nil
}

func lastJSONLine(b []byte) []byte {
	lines := strings.Split(strings.TrimSpace(string(b)), "\n")
	return []byte(strings.TrimSpace(lines[len(lines)-1]))
}
