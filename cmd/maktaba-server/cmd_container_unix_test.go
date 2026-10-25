//go:build !windows

package main

import (
	"context"
	"io"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/Hamza-Labs-Core/Maktaba/cmd/maktaba-server/internal/appruntime"
	"github.com/Hamza-Labs-Core/Maktaba/shared/go/layout"
)

func containerFor(t *testing.T, payload string) (containerDeps, *syncBuf) {
	t.Helper()
	out := &syncBuf{}
	dir := t.TempDir()
	rt := appruntime.New(filepath.Join(dir, "runtime"))
	rt.Sync = func() {} // a whole-system sync takes seconds on a busy machine
	return containerDeps{rt: rt, payload: payload, self: "/opt/maktaba/server/bin/maktaba-server",
		config: filepath.Join(dir, "server.toml"), logFile: filepath.Join(dir, "server.log"), out: out,
		every: 5 * time.Millisecond, launcherRoot: dir, now: time.Now, guardFor: time.Hour,
		settle: 20 * time.Millisecond, confirmEvery: 5 * time.Millisecond,
		version: func(context.Context) string { return "" }}, out
}

func currentVersion(t *testing.T, rt appruntime.Runtime) string {
	t.Helper()
	st, err := rt.Status("")
	if err != nil {
		t.Fatal(err)
	}
	return st.Version
}

// withImage is c with the image's payload at version (a new image pulled).
func withImage(t *testing.T, c containerDeps, version string) containerDeps {
	t.Helper()
	c.payload = onePayload(t, version)
	return c
}

// A fresh volume gets the image's payload as its first runtime, owned by the machine (the
// server updates it), with no guard; the same image again changes nothing.
func TestContainerBootstrapFirst(t *testing.T) {
	c, _ := containerFor(t, onePayload(t, "1.5.0"))
	for range 2 {
		g, err := c.bootstrap(context.Background(), false)
		if err != nil || g != nil {
			t.Fatalf("guard %+v, err %v", g, err)
		}
	}
	if v := currentVersion(t, c.rt); v != "1.5.0" {
		t.Fatalf("the first runtime: %q", v)
	}
	if o := c.rt.ReadOwner(); o != appruntime.OwnerHeadless {
		t.Fatalf("owner %q: the server would not update it", o)
	}
}

// A newer image is switched to as an update is: under the launcher's guard (the runtime
// before named to go back to), the watermark left where it was, nothing pruned.
func TestContainerBootstrapNewerIsGuarded(t *testing.T) {
	c, out := containerFor(t, onePayload(t, "1.5.0"))
	ctx := context.Background()
	if _, err := c.bootstrap(ctx, false); err != nil {
		t.Fatal(err)
	}
	before, _ := c.rt.Current()
	newer := withImage(t, c, "1.6.0")
	g, err := newer.bootstrap(ctx, false)
	if err != nil {
		t.Fatal(err)
	}
	if v := currentVersion(t, c.rt); v != "1.6.0" {
		t.Fatalf("a newer image: %q", v)
	}
	onDisk, err := layout.ReadGuard(c.launcherRoot)
	if err != nil || g == nil || onDisk == nil || onDisk.RunID != imageRunID || onDisk.Version != "1.6.0" ||
		onDisk.PreviousRuntime != before || !onDisk.Deadline.After(time.Now()) {
		t.Fatalf("the guard: returned %+v, on disk %+v (%v)", g, onDisk, err)
	}
	if w := c.rt.Watermark(); w != "1.5.0" {
		t.Fatalf("the watermark rose before the release ran: %q", w)
	}
	if !strings.Contains(out.String(), "under the launcher's guard") {
		t.Fatalf("said %q", out.String())
	}
	// a restart of the container before it was kept watches on
	if again, err := newer.bootstrap(ctx, false); err != nil || again == nil || again.Version != "1.6.0" {
		t.Fatalf("restarted: %+v %v", again, err)
	}
}

// An older image changes nothing; nor does a newer one before a command (a `docker
// compose run` beside a container that may be running): no switch, no guard.
func TestContainerBootstrapOlderAndCommand(t *testing.T) {
	c, out := containerFor(t, onePayload(t, "1.5.0"))
	ctx := context.Background()
	if _, err := c.bootstrap(ctx, false); err != nil {
		t.Fatal(err)
	}
	if g, err := withImage(t, c, "1.4.0").bootstrap(ctx, false); err != nil || g != nil {
		t.Fatalf("an older image: %+v %v", g, err)
	}
	if g, err := withImage(t, c, "1.6.0").bootstrap(ctx, true); err != nil || g != nil {
		t.Fatalf("a command from a newer image: %+v %v", g, err)
	}
	if v := currentVersion(t, c.rt); v != "1.5.0" {
		t.Fatalf("current moved to %q", v)
	}
	if g, _ := layout.ReadGuard(c.launcherRoot); g != nil {
		t.Fatalf("a guard: %+v", g)
	}
	if !strings.Contains(out.String(), "older than 1.5.0") || !strings.Contains(out.String(), "when the container starts") {
		t.Fatalf("said %q", out.String())
	}
}

// A newer image whose server answers with its version for the settle time is kept, as an
// update's commit: the guard cleared, the watermark raised.
func TestContainerConfirmKeepsAHealthyImage(t *testing.T) {
	c, out := containerFor(t, onePayload(t, "1.5.0"))
	ctx := context.Background()
	if _, err := c.bootstrap(ctx, false); err != nil {
		t.Fatal(err)
	}
	newer := withImage(t, c, "1.6.0")
	g, err := newer.bootstrap(ctx, false)
	if err != nil || g == nil {
		t.Fatalf("%+v %v", g, err)
	}
	newer.version = func(context.Context) string { return "v1.6.0" }
	done := make(chan struct{})
	go func() { newer.confirm(ctx, *g); close(done) }()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("not kept")
	}
	if g, _ := layout.ReadGuard(c.launcherRoot); g != nil {
		t.Fatalf("the guard stayed: %+v", g)
	}
	if w := c.rt.Watermark(); w != "1.6.0" {
		t.Fatalf("the watermark: %q", w)
	}
	if !strings.Contains(out.String(), "runs: kept") {
		t.Fatalf("said %q", out.String())
	}
}

// When the launcher goes back (the new release could not start), the image's version is
// refused: the image that ran before still runs (the watermark never rose), that newer
// image is not switched to again, and a newer one still is.
func TestContainerConfirmRecordsARevert(t *testing.T) {
	c, out := containerFor(t, onePayload(t, "1.5.0"))
	ctx := context.Background()
	if _, err := c.bootstrap(ctx, false); err != nil {
		t.Fatal(err)
	}
	newer := withImage(t, c, "1.6.0")
	g, err := newer.bootstrap(ctx, false)
	if err != nil || g == nil {
		t.Fatalf("%+v %v", g, err)
	}
	// the launcher's fallback: `current` back, then the guard cleared
	if _, err := c.rt.Point(ctx, g.PreviousRuntime, appruntime.Options{AllowDowngrade: true}); err != nil {
		t.Fatal(err)
	}
	if err := layout.ClearGuard(c.launcherRoot); err != nil {
		t.Fatal(err)
	}
	newer.confirm(ctx, *g)
	if r := c.refused(); r != "1.6.0" {
		t.Fatalf("refused %q", r)
	}
	if g, err := c.bootstrap(ctx, false); err != nil || g != nil || currentVersion(t, c.rt) != "1.5.0" {
		t.Fatalf("the image before: %+v %v %q", g, err, currentVersion(t, c.rt))
	}
	if g, err := newer.bootstrap(ctx, false); err != nil || g != nil || currentVersion(t, c.rt) != "1.5.0" {
		t.Fatalf("the refused image again: %+v %v %q", g, err, currentVersion(t, c.rt))
	}
	if !strings.Contains(out.String(), "could not start here") {
		t.Fatalf("said %q", out.String())
	}
	if g, err := withImage(t, c, "1.7.0").bootstrap(ctx, false); err != nil || g == nil || currentVersion(t, c.rt) != "1.7.0" {
		t.Fatalf("a newer image: %+v %v", g, err)
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
