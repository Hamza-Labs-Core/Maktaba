package main

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/Hamza-Labs-Core/Maktaba/cmd/maktaba-server/internal/supervisor"
)

// serve's own stderr lines get their time in front; the roles keep serve's own stderr (their
// lines carry their times); stop puts stderr back with every line out.
func TestStampStderr(t *testing.T) {
	out := filepath.Join(t.TempDir(), "server.log")
	f, err := os.Create(out)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	saved := os.Stderr
	os.Stderr = f
	defer func() { os.Stderr = saved }()

	stop := stampStderr()
	if supervisor.ChildStderr != f {
		t.Fatal("the roles do not keep serve's stderr")
	}
	fmt.Fprintf(os.Stderr, "[serve] dumping the database before 0.3.2's migrations\n")
	fmt.Fprintf(os.Stderr, "[update] the last line, without its newline")
	stop()
	if os.Stderr != f || supervisor.ChildStderr != nil {
		t.Fatal("stderr not put back")
	}
	b, _ := os.ReadFile(out)
	lines := strings.Split(strings.TrimRight(string(b), "\n"), "\n")
	stamped := regexp.MustCompile(`^\d{4}-\d\d-\d\dT\d\d:\d\d:\d\d\.\d{3}Z \[(serve|update)\] `)
	if len(lines) != 2 || !stamped.MatchString(lines[0]) || !stamped.MatchString(lines[1]) {
		t.Fatalf("server.log:\n%s", b)
	}
	if got := stampLine(time.Date(2026, 10, 24, 5, 30, 0, 0, time.FixedZone("EDT", -4*3600)), "x\n"); got != "2026-10-24T09:30:00.000Z x\n" {
		t.Fatalf("stampLine: %q", got)
	}
}
