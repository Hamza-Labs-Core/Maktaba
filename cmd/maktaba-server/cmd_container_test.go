package main

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/Hamza-Labs-Core/Maktaba/cmd/maktaba-server/internal/appruntime"
)

// The image's payload against the volume's runtime: the first runtime, a newer image, an
// image older than the newest the volume ran (never installed), or what runs already.
func TestPlanContainer(t *testing.T) {
	for _, c := range []struct {
		st   appruntime.Status
		want containerPlan
	}{
		{appruntime.Status{FromVersion: "1.5.0", UpdatePending: true}, containerFirst},
		{appruntime.Status{FromVersion: "1.6.0", Current: "1.5.0_a", Version: "1.5.0", UpdatePending: true}, containerNewer},
		{appruntime.Status{FromVersion: "1.4.0", Current: "1.5.0_a", Version: "1.5.0", Watermark: "1.5.0", Downgrade: true}, containerOlder},
		{appruntime.Status{FromVersion: "1.5.0", Current: "1.5.0_a", Version: "1.5.0", From: "1.5.0_a"}, containerRuns},
		{appruntime.Status{Current: "1.5.0_a", Version: "1.5.0"}, containerRuns},
	} {
		if got := planContainer(c.st); got != c.want {
			t.Errorf("%+v: %s, want %s", c.st, got, c.want)
		}
	}
}

func TestContainerEnv(t *testing.T) {
	env := containerEnv([]string{"PATH=/bin"})
	if !slices.Contains(env, "MAKTABA_CONTAINER=docker") {
		t.Errorf("not marked as the Docker install's: %v", env)
	}
	kept := containerEnv([]string{"MAKTABA_CONTAINER=podman"})
	if len(kept) != 1 || kept[0] != "MAKTABA_CONTAINER=podman" {
		t.Errorf("the image's own word replaced: %v", kept)
	}
}

// In the image the setup installs no service (the entrypoint starts the server once
// server.toml is there) and runs no sudo; elsewhere it is left as it is.
func TestContainerSetupInTheImage(t *testing.T) {
	service := func(context.Context) error { return nil }
	install := func(context.Context, string) error { return nil }
	a := &appSetup{afterFinish: service, install: install}
	containerSetup(a, "")
	if a.afterFinish == nil || a.install == nil || a.finished != "" {
		t.Fatalf("outside a container: %+v", a)
	}
	containerSetup(a, "docker")
	if a.afterFinish != nil || a.install != nil || a.finished != "terminal.container" {
		t.Fatalf("in the image: %+v", a)
	}
	if _, ok := terminalTexts[a.finished]; !ok {
		t.Fatalf("%s has no words", a.finished)
	}
}

// A container not set up waits for server.toml, says once how to set up, and stops with
// the container.
func TestContainerWaitsForSetup(t *testing.T) {
	dir := t.TempDir()
	var out bytes.Buffer
	c := containerDeps{config: filepath.Join(dir, "server.toml"), out: &out, every: 10 * time.Millisecond}
	cfg := c.config // the goroutine's own copy: c.config changes below
	go func() {
		time.Sleep(50 * time.Millisecond)
		_ = os.WriteFile(cfg, []byte("[server]\n"), 0o600)
	}()
	if !c.waitSetup(context.Background()) {
		t.Fatal("did not go on once set up")
	}
	if n := strings.Count(out.String(), "not set up yet"); n != 1 {
		t.Fatalf("said %d times: %q", n, out.String())
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	c.config = filepath.Join(dir, "none.toml")
	if c.waitSetup(ctx) {
		t.Fatal("a stopped container went on")
	}
}

// syncBuf is a buffer two goroutines use.
type syncBuf struct {
	mu sync.Mutex
	b  bytes.Buffer
}

func (s *syncBuf) Write(p []byte) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.b.Write(p)
}

func (s *syncBuf) String() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.b.String()
}

func appendTo(t *testing.T, path, text string) {
	t.Helper()
	f, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o600)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.WriteString(text); err != nil {
		t.Fatal(err)
	}
	_ = f.Close()
}

func waitFor(t *testing.T, what string, ok func() bool) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for !ok() {
		if time.Now().After(deadline) {
			t.Fatalf("%s: timed out", what)
		}
		time.Sleep(5 * time.Millisecond)
	}
}

// docker logs show the launcher's log: what is appended (not what it held before the
// container started), and a log cut short is read again from its start.
func TestFollowLogCopiesWhatIsAppended(t *testing.T) {
	path := filepath.Join(t.TempDir(), "server.log")
	appendTo(t, path, "before the container\n")
	var out syncBuf
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { followLog(ctx, path, &out, 5*time.Millisecond); close(done) }()
	time.Sleep(30 * time.Millisecond)
	appendTo(t, path, "one\n")
	waitFor(t, "an appended line", func() bool { return strings.Contains(out.String(), "one\n") })
	if err := os.WriteFile(path, []byte("cut\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	waitFor(t, "a log cut short", func() bool { return strings.Contains(out.String(), "cut\n") })
	appendTo(t, path, "last\n")
	time.Sleep(20 * time.Millisecond)
	cancel()
	<-done
	got := out.String()
	if strings.Contains(got, "before the container") || !strings.Contains(got, "last\n") {
		t.Fatalf("copied %q", got)
	}
}

// The launcher renames its log aside (server.log.1) past 50 MB: the rest of the old one is
// copied, then the new one from its start. A log that appears later is copied whole.
func TestFollowLogAcrossARename(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("a file open for reading cannot be renamed on Windows; the image is Linux")
	}
	dir := t.TempDir()
	path := filepath.Join(dir, "server.log")
	var out syncBuf
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { followLog(ctx, path, &out, 5*time.Millisecond); close(done) }()
	time.Sleep(20 * time.Millisecond)
	appendTo(t, path, "first\n") // the log appears after the container started
	waitFor(t, "a new log", func() bool { return strings.Contains(out.String(), "first\n") })
	appendTo(t, path, "old tail\n")
	if err := os.Rename(path, path+".1"); err != nil {
		t.Fatal(err)
	}
	appendTo(t, path, "new head\n")
	waitFor(t, "the renamed log", func() bool { return strings.Contains(out.String(), "new head\n") })
	cancel()
	<-done
	if got := out.String(); !strings.Contains(got, "old tail\n") || strings.Index(got, "old tail") > strings.Index(got, "new head") {
		t.Fatalf("copied %q", got)
	}
}
