package ports

import (
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"testing"
)

// TestNoTwoEntriesShareAPort is the registry's guarantee: no two entries share a
// port, every port of every range compared, the other programs' ports included. On
// the owner's Mac two transcription workers held 9111 and 9112 (the workers were
// 9110+i) and the blue and green TV slots crash-looped on them for days, blocking
// every update.
func TestNoTwoEntriesShareAPort(t *testing.T) {
	for _, o := range Overlaps(All()) {
		t.Error(o)
	}
}

func TestOverlapsSeesInsideARange(t *testing.T) {
	workers := Entry{Name: "workers", Owner: "pipeline/stt", Port: 9111, Count: 16}
	slot := Entry{Name: "slot admin", Owner: "tv-slot", Port: 9112, Count: 3}
	got := Overlaps([]Entry{workers, slot, {Name: "x", Port: 9200}})
	if len(got) != 1 || got[0].Port != 9112 {
		t.Fatalf("Overlaps = %v, want one at 9112", got)
	}
	if !strings.Contains(got[0].String(), "pipeline/stt's workers (9111-9126)") {
		t.Errorf("message %q does not name the range", got[0])
	}
	if Overlaps([]Entry{{Port: 9110, Count: 1}, {Port: 9111, Count: 3}}) != nil {
		t.Error("adjacent entries reported as overlapping")
	}
}

// TestEveryEntryIsDescribed: each entry says who owns it and what it is for, and
// its scope is what its host says.
func TestEveryEntryIsDescribed(t *testing.T) {
	names := map[string]bool{}
	for _, e := range All() {
		if e.Name == "" || e.Owner == "" || e.Purpose == "" || e.Port <= 0 || e.Last() > 65535 {
			t.Errorf("%+v is missing its name, owner, purpose or a port", e)
		}
		if names[e.Name] {
			t.Errorf("two entries are named %q", e.Name)
		}
		names[e.Name] = true
		if (e.Host == Loopback) != (e.Scope() == ScopeLoopback) {
			t.Errorf("%s: host %q, scope %s", e.Name, e.Host, e.Scope())
		}
	}
}

func TestRanges(t *testing.T) {
	if TVSlotAdmin.Len() != TVSlots || TVSlotAdmin.Last() != 9113 || TVSlotAdmin.AddrAt(2) != "127.0.0.1:9113" {
		t.Errorf("TV slot admin: %v, last %d, %s", TVSlotAdmin, TVSlotAdmin.Last(), TVSlotAdmin.AddrAt(2))
	}
	if TranscriptionLogs.At(0) != 9131 || TranscriptionLogs.Last() != 9146 {
		t.Errorf("transcription workers: %v", TranscriptionLogs)
	}
	if e, i, ok := Lookup(9112); !ok || e.Name != TVSlotAdmin.Name || i != 1 {
		t.Errorf("Lookup(9112) = %v %d %v", e, i, ok)
	}
	if _, _, ok := Lookup(9092); ok {
		t.Error("Lookup(9092) found an entry")
	}
	if StreamingPublic.Addr() != ":8081" || APIPublic.Addr() != "0.0.0.0:8080" || APIAdmin.Local() != "127.0.0.1:9100" ||
		StreamingGRPC.Every() != ":9050" || StreamingAdmin.Scope() != ScopePublic {
		t.Error("an entry's addresses are not its defaults")
	}
	if All()[0] != APIPublic {
		t.Error("All is not in declaration order")
	}
}

func TestClashes(t *testing.T) {
	cases := []struct {
		a, b string
		want bool
	}{
		{"127.0.0.1:9111", "127.0.0.1:9111", true},
		{":9111", "127.0.0.1:9111", true},
		{"0.0.0.0:8080", "localhost:8080", true},
		{"[::]:8080", "127.0.0.1:8080", true},
		{"127.0.0.1:9090", "192.168.1.5:9090", false},
		{"127.0.0.1:9090", "127.0.0.1:9091", false},
		{"127.0.0.1:0", "127.0.0.1:0", false},
		{"nonsense", "nonsense", false},
	}
	for _, c := range cases {
		got := Clashes([]Bind{{Addr: c.a, Owner: "a", What: "x"}, {Addr: c.b, Owner: "b", What: "y"}})
		if (len(got) == 1) != c.want {
			t.Errorf("%s vs %s: %v, want clash %v", c.a, c.b, got, c.want)
		}
	}
	got := Clashes([]Bind{{Addr: "127.0.0.1:9111", Owner: "tv-slot/blue", What: "admin port"},
		{Addr: "127.0.0.1:9111", Owner: "pipeline/stt-1", What: "logs port"}})
	want := "port 9111 is both tv-slot/blue's admin port (127.0.0.1:9111) and pipeline/stt-1's logs port (127.0.0.1:9111)"
	if len(got) != 1 || got[0].String() != want {
		t.Errorf("clash = %v, want %q", got, want)
	}
}

// TestMirrorsMatch: the defaults written outside Go (the pipeline's Python, the Mac
// app's Rust) are this registry's ports, so they cannot drift from it.
func TestMirrorsMatch(t *testing.T) {
	root := filepath.Join("..", "..", "..")
	mirrors := []struct {
		file  string
		re    string // its one group is the port
		entry Entry
	}{
		{"pipeline/src/maktaba_pipeline/cluster/agent/service.py", `DEFAULT_API_URL = "http://127\.0\.0\.1:(\d+)"`, APIPublic},
		{"pipeline/src/maktaba_pipeline/cluster/discover.py", `def probe_lan\(port: int = (\d+)\)`, APIPublic},
		{"pipeline/src/maktaba_pipeline/discovery/probe.py", `port: int = (\d+),`, APIPublic},
		{"pipeline/src/maktaba_pipeline/llm/runtimes.py", `DEFAULT_OLLAMA_URL = "http://127\.0\.0\.1:(\d+)"`, Ollama},
		{"pipeline/src/maktaba_pipeline/classify/tag_check.py", `url: str = "http://localhost:(\d+)"`, Ollama},
		{"apps/desktop/src-tauri/src/status.rs", `DEFAULT_SUPERVISOR_ADDR: &str = "127\.0\.0\.1:(\d+)"`, Supervisor},
	}
	for _, m := range mirrors {
		b, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(m.file)))
		if err != nil {
			t.Errorf("%s: %v", m.file, err)
			continue
		}
		g := regexp.MustCompile(m.re).FindSubmatch(b)
		if g == nil {
			t.Errorf("%s: no %s (moved? point this test at the new place)", m.file, m.re)
			continue
		}
		if p, _ := strconv.Atoi(string(g[1])); p != m.entry.Port {
			t.Errorf("%s: port %d, the registry's %s is %d", m.file, p, m.entry.Name, m.entry.Port)
		}
	}
}
