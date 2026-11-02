//go:build !windows

package discovery

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// fakeCommand is a dns-sd (or avahi-publish-service) that writes its arguments and its
// pid to log, one line per run, then waits to be ended.
func fakeCommand(t *testing.T, name string) (path, log string) {
	t.Helper()
	dir := t.TempDir()
	log = filepath.Join(dir, "runs")
	path = filepath.Join(dir, name)
	script := "#!/bin/sh\necho \"$$ $*\" >> '" + log + "'\nexec sleep 600\n"
	if err := os.WriteFile(path, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	return path, log
}

func fakeDNSSD(t *testing.T) (path, log string) { return fakeCommand(t, "dns-sd") }

// runs are the fake's lines so far, waiting up to a few seconds for at least n.
func runs(t *testing.T, log string, n int) []string {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for {
		b, _ := os.ReadFile(log)
		lines := strings.Split(strings.TrimSpace(string(b)), "\n")
		if len(b) > 0 && len(lines) >= n {
			return lines
		}
		if time.Now().After(deadline) {
			t.Fatalf("wanted %d runs, have %q", n, string(b))
		}
		time.Sleep(20 * time.Millisecond)
	}
}

// alive is whether the process of a fake's line still runs.
func alive(line string) bool {
	pid := strings.Fields(line)[0]
	_, err := os.Stat("/proc/" + pid)
	if err == nil {
		return true
	}
	// no /proc (macOS): ask kill -0 through the shell
	return exec0("kill", "-0", pid) == nil
}

// gone waits a few seconds for a fake's process to end.
func gone(t *testing.T, line string) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for alive(line) {
		if time.Now().After(deadline) {
			t.Fatalf("still running: %s", line)
		}
		time.Sleep(20 * time.Millisecond)
	}
}

// The announcement is dns-sd -R with the service and TXT as arguments; a new TXT ends
// that dns-sd and starts one with it; Shutdown ends it, and it is not started again.
func TestDNSSDAnnouncesChangesAndWithdraws(t *testing.T) {
	path, log := fakeDNSSD(t)
	a, err := startDNSSD(path, "Maktaba (studio)", "_maktaba._tcp", "local.", 8080, []string{"role=home", "web=8088"})
	if err != nil {
		t.Fatal(err)
	}
	first := runs(t, log, 1)[0]
	if want := "-R Maktaba (studio) _maktaba._tcp local. 8080 role=home web=8088"; !strings.HasSuffix(first, want) {
		t.Fatalf("run = %q, want … %q", first, want)
	}

	a.SetText([]string{"role=home", "web=8090"})
	second := runs(t, log, 2)[1]
	if !strings.HasSuffix(second, "8080 role=home web=8090") {
		t.Fatalf("after SetText = %q", second)
	}
	gone(t, first)

	a.Shutdown()
	gone(t, second)
	time.Sleep(100 * time.Millisecond)
	if n := len(runs(t, log, 2)); n != 2 {
		t.Fatalf("started again after Shutdown: %d runs", n)
	}
}

// A dns-sd that ends by itself (mDNSResponder restarted) is started again.
func TestDNSSDAnnouncesAgainWhenItEnds(t *testing.T) {
	was := dnssdRestart
	dnssdRestart = 50 * time.Millisecond
	t.Cleanup(func() { dnssdRestart = was })

	path, log := fakeDNSSD(t)
	a, err := startDNSSD(path, "Maktaba (studio)", "_maktaba._tcp", "local.", 8080, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer a.Shutdown()
	first := runs(t, log, 1)[0]
	if err := exec0("kill", strings.Fields(first)[0]); err != nil {
		t.Fatal(err)
	}
	runs(t, log, 2)
}

// A name with shell characters reaches dns-sd as one argument, never as script.
func TestDNSSDTakesTheNameAsAnArgument(t *testing.T) {
	path, log := fakeDNSSD(t)
	a, err := startDNSSD(path, `Maktaba ($(touch pwned); "x")`, "_maktaba._tcp", "local.", 8080, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer a.Shutdown()
	line := runs(t, log, 1)[0]
	if !strings.Contains(line, `-R Maktaba ($(touch pwned); "x") _maktaba._tcp`) {
		t.Fatalf("run = %q", line)
	}
	if _, err := os.Stat("pwned"); err == nil {
		_ = os.Remove("pwned")
		t.Fatal("the name ran as a command")
	}
}

// Through Avahi: avahi-publish-service -- name type port txt… (the domain only when it is
// not .local); a new TXT ends it and starts one with it; one that ends by itself
// (avahi-daemon restarted) is started again; Shutdown ends it for good.
func TestAvahiAnnouncesChangesRestartsAndWithdraws(t *testing.T) {
	was := dnssdRestart
	dnssdRestart = 50 * time.Millisecond
	t.Cleanup(func() { dnssdRestart = was })

	path, log := fakeCommand(t, "avahi-publish-service")
	a, err := startAvahi(path, "-Maktaba (studio)\n", "_maktaba._tcp", "local.", 8080, []string{"role=home", "web=8088"})
	if err != nil {
		t.Fatal(err)
	}
	first := runs(t, log, 1)[0]
	if want := " -- -Maktaba (studio) _maktaba._tcp 8080 role=home web=8088"; !strings.HasSuffix(first, want) {
		t.Fatalf("run = %q, want … %q", first, want)
	}

	a.SetText([]string{"role=home", "web=8090"})
	second := runs(t, log, 2)[1]
	if !strings.HasSuffix(second, "8080 role=home web=8090") {
		t.Fatalf("after SetText = %q", second)
	}
	gone(t, first)

	if err := exec0("kill", strings.Fields(second)[0]); err != nil {
		t.Fatal(err)
	}
	third := runs(t, log, 3)[2]
	if !strings.HasSuffix(third, "8080 role=home web=8090") {
		t.Fatalf("restarted = %q", third)
	}

	a.Shutdown()
	gone(t, third)
	time.Sleep(150 * time.Millisecond)
	if n := len(runs(t, log, 3)); n != 3 {
		t.Fatalf("started again after Shutdown: %d runs", n)
	}

	other, log2 := fakeCommand(t, "avahi-publish-service")
	b, err := startAvahi(other, "Maktaba", "_maktaba._tcp", "example.org.", 8080, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer b.Shutdown()
	if line := runs(t, log2, 1)[0]; !strings.HasSuffix(line, " -d example.org -- Maktaba _maktaba._tcp 8080") {
		t.Fatalf("other domain: %q", line)
	}
}

func exec0(name string, args ...string) error { return exec.Command(name, args...).Run() }
