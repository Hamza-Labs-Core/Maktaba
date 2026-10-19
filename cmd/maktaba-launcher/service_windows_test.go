//go:build windows

package main

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	"golang.org/x/sys/windows/svc"
	"golang.org/x/sys/windows/svc/mgr"
)

// TestWindowsServiceControl (story 35.2 TC7) registers the launcher as a real
// Windows service, starts it, checks it started the current release, stops it
// through the SCM and checks the server was stopped gracefully. It needs an
// administrator and changes the machine's services, so it runs only where
// MAKTABA_TEST_WINDOWS_SERVICE=1 (the windows-latest CI job), never on a
// developer's machine by accident.
func TestWindowsServiceControl(t *testing.T) {
	processTest(t)
	if os.Getenv("MAKTABA_TEST_WINDOWS_SERVICE") != "1" {
		t.Skip("set MAKTABA_TEST_WINDOWS_SERVICE=1 (CI) to install a test service")
	}
	l := installed(t, map[string]string{"1.0.0": "run"}, "1.0.0")
	launcher := filepath.Join(t.TempDir(), "maktaba-launcher.exe")
	if out, err := exec.Command("go", "build", "-o", launcher, ".").CombinedOutput(); err != nil {
		t.Fatalf("build launcher: %v\n%s", err, out)
	}
	m, err := mgr.Connect()
	if err != nil {
		t.Fatalf("connect to the SCM (needs admin): %v", err)
	}
	defer func() { _ = m.Disconnect() }()
	name := fmt.Sprintf("MaktabaLauncherTest%d", time.Now().UnixNano()%100000)
	s, err := m.CreateService(name, launcher, mgr.Config{DisplayName: name, StartType: mgr.StartManual},
		"--root", l.Root, "--stop-grace", "20s", "serve")
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		_, _ = s.Control(svc.Stop)
		_ = s.Delete()
		_ = s.Close()
	}()
	// the fake server recognises itself by its release's behavior file; the
	// service's environment does not carry the test's
	if err := s.Start(); err != nil {
		t.Fatal(err)
	}
	waitState(t, s, svc.Running)
	waitFor(t, "the service started the current release", func() bool { return countAct(readTrace(l), "1.0.0", "run") == 1 })
	if _, err := s.Control(svc.Stop); err != nil {
		t.Fatal(err)
	}
	waitState(t, s, svc.Stopped)
	if countAct(readTrace(l), "1.0.0", "stopped") != 1 {
		t.Fatal("the server was killed, not stopped")
	}
	if len(events(l, "launcher_stopped")) != 1 {
		t.Fatalf("launcher.log: %+v", readLog(l.LauncherLog()))
	}
}

func waitState(t *testing.T, s *mgr.Service, want svc.State) {
	t.Helper()
	deadline := time.Now().Add(60 * time.Second)
	for time.Now().Before(deadline) {
		st, err := s.Query()
		if err == nil && st.State == want {
			return
		}
		time.Sleep(200 * time.Millisecond)
	}
	st, _ := s.Query()
	t.Fatalf("service state %d, want %d", st.State, want)
}
