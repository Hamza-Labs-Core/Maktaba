package layout

import (
	"os"
	"os/exec"
	"runtime"
	"strings"
	"testing"
	"time"
)

// The record round-trips, says whose handover it is, and is of this boot only.
func TestHandoverRecord(t *testing.T) {
	dir := t.TempDir()
	if h, err := ReadHandover(dir); err != nil || h != nil {
		t.Fatalf("no record: %+v, %v", h, err)
	}
	in := Handover{BootID: BootID(), Writer: 4242, Handover: true, WrittenAt: time.Now().UTC().Truncate(time.Second),
		Children: []HandoverChild{{Role: "tv-slot", Instance: "blue", PID: 77, Start: 123, PGID: 77,
			Probe: "http://127.0.0.1:9111/healthz", Release: "1.4.0", Binding: []byte(`{"release_id":"r1"}`)}}}
	if err := WriteHandover(dir, in); err != nil {
		t.Fatal(err)
	}
	h, err := ReadHandover(dir)
	if err != nil || h == nil || len(h.Children) != 1 || h.Children[0].Instance != "blue" ||
		!strings.Contains(string(h.Children[0].Binding), `"r1"`) {
		t.Fatalf("read back: %+v, %v", h, err)
	}
	if BootID() != "" {
		if !h.HandedOverBy(4242) || h.HandedOverBy(4243) || !h.CurrentBoot() {
			t.Fatalf("whose handover: %+v", h)
		}
		h.BootID = "an-earlier-boot"
		if h.HandedOverBy(4242) || h.CurrentBoot() {
			t.Fatal("a record of an earlier boot was taken for this one's")
		}
	}
	h.Handover = false
	if h.HandedOverBy(4242) {
		t.Fatal("a kept record was taken for a handover")
	}
	if err := RemoveHandover(dir); err != nil {
		t.Fatal(err)
	}
	if err := RemoveHandover(dir); err != nil {
		t.Fatal("removing no record:", err)
	}
}

// One boot's ids agree, a fallback boot time within its drift too.
func TestSameBoot(t *testing.T) {
	for _, c := range []struct {
		a, b string
		same bool
	}{
		{"b126", "b126", true}, {"b126", "b127", false}, {"", "", false},
		{"t1000000", "t1000059", true}, {"t1000000", "t1000300", true}, {"t1000000", "t1000301", false},
		{"t1000000", "b126", false}, {"tx", "tx", true}, {"tx", "ty", false},
	} {
		if got := SameBoot(c.a, c.b); got != c.same {
			t.Errorf("SameBoot(%q, %q) = %v", c.a, c.b, got)
		}
	}
}

// A release says the handover its supervisor speaks in its release.json.
func TestReleaseHandoverProtocol(t *testing.T) {
	dir := t.TempDir()
	if ReleaseHandoverProtocol(dir) != 0 {
		t.Fatal("no release.json")
	}
	if err := os.WriteFile(dir+"/release.json", []byte(`{"v":1,"handover_protocol":1}`), 0o644); err != nil {
		t.Fatal(err)
	}
	if ReleaseHandoverProtocol(dir) != HandoverProtocol {
		t.Fatal("handover_protocol not read")
	}
}

// The named job is the install's own.
func TestTVJobName(t *testing.T) {
	a, b := TVJobName(t.TempDir()), TVJobName(t.TempDir())
	if a == b || !strings.HasPrefix(a, `Local\Maktaba-tv-`) {
		t.Fatalf("job names %q %q", a, b)
	}
	dir := t.TempDir()
	if first, again := TVJobName(dir), TVJobName(dir); first != again || !strings.HasPrefix(TVChildJobName(dir, 12), first+"-12") {
		t.Fatal("the names are not stable")
	}
}

// A process is known by its pid and its start time; one that exited is not running.
func TestProcessIdentity(t *testing.T) {
	if runtime.GOOS != "linux" && runtime.GOOS != "darwin" && runtime.GOOS != "windows" {
		t.Skip("no process identity on " + runtime.GOOS)
	}
	start, err := ProcessStart(os.Getpid())
	if err != nil || start == 0 {
		t.Fatalf("this process: %d, %v", start, err)
	}
	if again, _ := ProcessStart(os.Getpid()); again != start {
		t.Fatalf("start time changed: %d then %d", start, again)
	}
	if !ProcessIs(os.Getpid(), start) || ProcessIs(os.Getpid(), start+1) || ProcessIs(0, start) {
		t.Fatal("identity")
	}
	if first, again := BootID(), BootID(); first == "" || first != again {
		t.Fatalf("boot id %q", BootID())
	}
	cmd := exec.Command(os.Args[0], "-test.run=^$")
	if err := cmd.Run(); err != nil {
		t.Fatal(err)
	}
	if _, err := ProcessStart(cmd.Process.Pid); err == nil && ProcessIs(cmd.Process.Pid, start) {
		t.Fatal("an exited process taken for a running one")
	}
}
