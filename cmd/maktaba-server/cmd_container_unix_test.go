//go:build !windows

package main

import (
	"bytes"
	"context"
	"io"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/Hamza-Labs-Core/Maktaba/cmd/maktaba-server/internal/appruntime"
)

func containerFor(t *testing.T, payload string) (containerDeps, *bytes.Buffer) {
	t.Helper()
	var out bytes.Buffer
	dir := t.TempDir()
	return containerDeps{rt: appruntime.New(filepath.Join(dir, "runtime")), payload: payload, self: "/opt/maktaba/server/bin/maktaba-server",
		config: filepath.Join(dir, "server.toml"), logFile: filepath.Join(dir, "server.log"), out: &out, every: 5 * time.Millisecond}, &out
}

func currentVersion(t *testing.T, rt appruntime.Runtime) string {
	t.Helper()
	st, err := rt.Status("")
	if err != nil {
		t.Fatal(err)
	}
	return st.Version
}

// The image's payload goes on the volume's runtime: the first one becomes current (the
// machine its owner: the server updates it), a newer image replaces it, an older image
// changes nothing (the volume's newer runtime runs), the same image again changes nothing.
func TestContainerBootstrap(t *testing.T) {
	c, out := containerFor(t, onePayload(t, "1.5.0"))
	ctx := context.Background()
	if err := c.bootstrap(ctx); err != nil {
		t.Fatal(err)
	}
	if v := currentVersion(t, c.rt); v != "1.5.0" {
		t.Fatalf("the first runtime: %q", v)
	}
	if o := c.rt.ReadOwner(); o != appruntime.OwnerHeadless {
		t.Fatalf("owner %q: the server would not update it", o)
	}
	if err := c.bootstrap(ctx); err != nil { // the same image restarted
		t.Fatal(err)
	}

	newer := c
	newer.payload = onePayload(t, "1.6.0")
	if err := newer.bootstrap(ctx); err != nil {
		t.Fatal(err)
	}
	if v := currentVersion(t, c.rt); v != "1.6.0" {
		t.Fatalf("a newer image: %q", v)
	}
	if !strings.Contains(out.String(), "replaces 1.5.0") {
		t.Fatalf("said %q", out.String())
	}

	older := c
	older.payload = onePayload(t, "1.4.0")
	if err := older.bootstrap(ctx); err != nil {
		t.Fatal(err)
	}
	if v := currentVersion(t, c.rt); v != "1.6.0" {
		t.Fatalf("an older image replaced the volume's newer runtime: %q", v)
	}
	if !strings.Contains(out.String(), "older than 1.6.0") {
		t.Fatalf("said %q", out.String())
	}
}

// With no command the container waits for setup, then runs the runtime's launcher with
// the log it copies to docker logs, and ends with the launcher's exit code.
func TestContainerRunsTheLauncherOnceSetUp(t *testing.T) {
	c, _ := containerFor(t, onePayload(t, "1.5.0"))
	if err := os.WriteFile(c.config, []byte("[server]\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	var started, env []string
	var followed string
	c.start = func(_ context.Context, path string, args, e []string) (int, error) {
		started, env = append([]string{path}, args...), e
		return 3, nil
	}
	c.follow = func(_ context.Context, path string, _ io.Writer) { followed = path }
	code, err := c.run(context.Background(), nil)
	if err != nil || code != 3 {
		t.Fatalf("code %d, err %v", code, err)
	}
	want := []string{filepath.Join(c.rt.Root, "current", "bin", "maktaba-launcher"), "--log-file", c.logFile, "serve"}
	if !slices.Equal(started, want) || followed != c.logFile {
		t.Fatalf("started %v (want %v), followed %q", started, want, followed)
	}
	if !slices.ContainsFunc(env, func(kv string) bool { return strings.HasPrefix(kv, "MAKTABA_CONTAINER=") }) {
		t.Fatalf("the launcher is not told it runs in the container: %v", env)
	}
}

// With a command (setup) the container runs it from this payload after the bootstrap.
func TestContainerRunsAGivenCommand(t *testing.T) {
	c, _ := containerFor(t, onePayload(t, "1.5.0"))
	var argv []string
	c.exec = func(path string, a, _ []string) error { argv = append([]string{path}, a...); return nil }
	if _, err := c.run(context.Background(), []string{"setup", "--lang", "ar"}); err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(argv, []string{c.self, c.self, "setup", "--lang", "ar"}) {
		t.Fatalf("exec %v", argv)
	}
	if v := currentVersion(t, c.rt); v != "1.5.0" {
		t.Fatalf("no bootstrap before the command: %q", v)
	}
}

// docker stop: the launcher is asked to stop (SIGTERM) and the container ends with it.
func TestRunLauncherPassesTheStopOn(t *testing.T) {
	script := filepath.Join(t.TempDir(), "launcher")
	if err := os.WriteFile(script, []byte("#!/bin/sh\ntrap 'echo stopped; exit 0' TERM\nwhile :; do sleep 0.05; done\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	var out syncBuf
	done := make(chan int, 1)
	go func() {
		code, _ := runLauncher(ctx, script, nil, os.Environ(), &out)
		done <- code
	}()
	time.Sleep(200 * time.Millisecond)
	cancel()
	select {
	case code := <-done:
		if code != 0 || !strings.Contains(out.String(), "stopped") {
			t.Fatalf("code %d, out %q", code, out.String())
		}
	case <-time.After(10 * time.Second):
		t.Fatal("the launcher was not stopped")
	}
	code, err := runLauncher(context.Background(), "/bin/sh", []string{"-c", "exit 75"}, os.Environ(), io.Discard)
	if err != nil || code != 75 {
		t.Fatalf("an exit of its own: %d %v", code, err)
	}
}
