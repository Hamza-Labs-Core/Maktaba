package mdns

import (
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
	"unicode/utf8"
)

// A Mac announces through its own responder; nothing else has one we use.
func TestSystemResponderIsDNSSDOnAMacOnly(t *testing.T) {
	t.Parallel()
	found := func(string) (string, error) { return "/usr/bin/dns-sd", nil }
	missing := func(string) (string, error) { return "", errors.New("not found") }
	if got := systemResponderFor("darwin", found); got != "/usr/bin/dns-sd" {
		t.Fatalf("darwin = %q", got)
	}
	if got := systemResponderFor("darwin", missing); got != "" {
		t.Fatalf("darwin without dns-sd = %q, want zeroconf", got)
	}
	if got := systemResponderFor("linux", found); got != "" {
		t.Fatalf("linux = %q, want zeroconf", got)
	}
}

// fakeDNSSD is a dns-sd that writes its arguments and its pid to log, one line per run,
// then waits to be ended.
func fakeDNSSD(t *testing.T) (path, log string) {
	t.Helper()
	dir := t.TempDir()
	log = filepath.Join(dir, "runs")
	path = filepath.Join(dir, "dns-sd")
	script := "#!/bin/sh\necho \"$$ $*\" >> '" + log + "'\nexec sleep 600\n"
	if err := os.WriteFile(path, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	return path, log
}

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

func exec0(name string, args ...string) error { return exec.Command(name, args...).Run() }

// A home name too long for DNS-SD, or with control characters, is fitted to it rather than
// refused (a refused name would be retried for ever).
func TestDNSLabelFitsDNSSD(t *testing.T) {
	t.Parallel()
	if got := dnsLabel("Maktaba (studio)\n\t", 63); got != "Maktaba (studio)" {
		t.Fatalf("control characters: %q", got)
	}
	long := "Maktaba (" + strings.Repeat("مكتبة", 10) + ")"
	got := dnsLabel(long, 63)
	if len(got) > 63 || !utf8.ValidString(got) {
		t.Fatalf("long name: %d bytes, valid %v", len(got), utf8.ValidString(got))
	}
	if !strings.HasPrefix(long, got) {
		t.Fatalf("not a prefix: %q", got)
	}
	if got := txtStrings([]string{strings.Repeat("a", 300)}); len(got[0]) != 255 {
		t.Fatalf("txt entry: %d bytes", len(got[0]))
	}
}
