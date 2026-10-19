package procattr

import (
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"testing"
	"time"
)

// The test binary doubles as the helper: PROCATTR_HELPER=parent contains
// itself, starts a tied "sleep" grandchild (standing in for ffmpeg),
// writes its pid and waits to be killed.
func TestMain(m *testing.M) {
	switch os.Getenv("PROCATTR_HELPER") {
	case "parent":
		if err := ContainSelf(); err != nil || !Contained() {
			os.Exit(80)
		}
		gc := exec.Command(os.Args[0], "-test.run=^$")
		gc.Env = append(os.Environ(), "PROCATTR_HELPER=sleep")
		Tie(gc)
		if err := gc.Start(); err != nil {
			os.Exit(81)
		}
		pidfile := os.Getenv("PROCATTR_PIDFILE")
		if os.WriteFile(pidfile+".tmp", []byte(strconv.Itoa(gc.Process.Pid)), 0o644) != nil ||
			os.Rename(pidfile+".tmp", pidfile) != nil {
			os.Exit(82)
		}
		time.Sleep(time.Hour)
		os.Exit(0)
	case "sleep":
		time.Sleep(time.Hour)
		os.Exit(0)
	}
	os.Exit(m.Run())
}

// A hard kill of the streaming process (no chance to clean up) still
// ends its ffmpeg children.
func TestChildrenDieWithAHardKilledParent(t *testing.T) {
	if runtime.GOOS != "linux" && runtime.GOOS != "windows" {
		t.Skip("parent-death containment is Linux and Windows only")
	}
	if testing.Short() {
		t.Skip("spawns processes; runs without -short")
	}
	pidfile := filepath.Join(t.TempDir(), "gc.pid")
	parent := exec.Command(os.Args[0], "-test.run=^$")
	parent.Env = append(os.Environ(), "PROCATTR_HELPER=parent", "PROCATTR_PIDFILE="+pidfile)
	if err := parent.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = parent.Process.Kill(); _ = parent.Wait() })

	var gc int
	deadline := time.Now().Add(20 * time.Second)
	for gc == 0 {
		if raw, err := os.ReadFile(pidfile); err == nil {
			gc, _ = strconv.Atoi(strings.TrimSpace(string(raw)))
		} else if time.Now().After(deadline) {
			t.Fatal("grandchild never started")
		} else {
			time.Sleep(20 * time.Millisecond)
		}
	}
	t.Cleanup(func() {
		if p, err := os.FindProcess(gc); err == nil && alive(gc) {
			_ = p.Kill()
		}
	})
	if !alive(gc) {
		t.Fatal("grandchild not running")
	}

	if err := parent.Process.Kill(); err != nil {
		t.Fatal(err)
	}
	_ = parent.Wait()
	deadline = time.Now().Add(10 * time.Second)
	for alive(gc) {
		if time.Now().After(deadline) {
			t.Fatalf("grandchild %d outlived its hard-killed parent", gc)
		}
		time.Sleep(20 * time.Millisecond)
	}
}
