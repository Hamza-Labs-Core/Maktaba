package portholder

import (
	"net"
	"os"
	"os/exec"
	"runtime"
	"testing"
)

func TestParseLsof(t *testing.T) {
	h, ok := parseLsof("p4242\ncmaktaba-pipeline\np99\ncother\n")
	if !ok || h.PID != 4242 || h.Command != "maktaba-pipeline" {
		t.Fatalf("parseLsof = %+v %v", h, ok)
	}
	if _, ok := parseLsof(""); ok {
		t.Error("nothing listening, yet a holder")
	}
}

func TestParseNetstat(t *testing.T) {
	out := `
Active Connections

  Proto  Local Address          Foreign Address        State           PID
  TCP    0.0.0.0:135            0.0.0.0:0              LISTENING       1012
  TCP    127.0.0.1:91110        0.0.0.0:0              LISTENING       7
  TCP    127.0.0.1:9111         127.0.0.1:50000        ESTABLISHED     8
  TCP    127.0.0.1:9111         0.0.0.0:0              LISTENING       4242
  TCP    [::]:9112              [::]:0                 LISTENING       5151
`
	if pid, ok := parseNetstat(out, 9111); !ok || pid != 4242 {
		t.Errorf("9111: %d %v", pid, ok)
	}
	if pid, ok := parseNetstat(out, 9112); !ok || pid != 5151 {
		t.Errorf("9112 (IPv6): %d %v", pid, ok)
	}
	if _, ok := parseNetstat(out, 9113); ok {
		t.Error("9113 found")
	}
}

func TestParseTasklist(t *testing.T) {
	if got := parseTasklist(`"maktaba-streaming.exe","4242","Console","1","25,000 K"` + "\r\n"); got != "maktaba-streaming.exe" {
		t.Errorf("parseTasklist = %q", got)
	}
	if got := parseTasklist("INFO: No tasks are running which match the specified criteria.\r\n"); got != "" {
		t.Errorf("no task, yet %q", got)
	}
}

func TestParseProcNetTCP(t *testing.T) {
	table := `  sl  local_address rem_address   st tx_queue rx_queue tr tm->when retrnsmt   uid  timeout inode
   0: 0100007F:23A7 00000000:0000 0A 00000000:00000000 00:00000000 00000000  1000        0 31337 1 0000000000000000 100 0 0 10 0
   1: 0100007F:23A8 0100007F:C350 01 00000000:00000000 00:00000000 00000000  1000        0 4444 1 0000000000000000 100 0 0 10 0
`
	if in, ok := parseProcNetTCP(table, 9127); !ok || in != "31337" {
		t.Errorf("9127: %q %v", in, ok)
	}
	if _, ok := parseProcNetTCP(table, 9128); ok {
		t.Error("an established socket read as a listener")
	}
}

// TestFindsItsOwnListener: on a machine with lsof, a port this test listens on is
// found as this process's.
func TestFindsItsOwnListener(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("netstat is slow to start on CI's Windows runners; its parsing is tested above")
	}
	if _, err := exec.LookPath("lsof"); err != nil {
		if _, err := os.Stat("/usr/sbin/lsof"); err != nil {
			t.Skip("no lsof")
		}
	}
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = ln.Close() }()
	h, ok := Find(ln.Addr().(*net.TCPAddr).Port)
	if !ok || h.PID != os.Getpid() {
		t.Fatalf("Find = %+v %v, want pid %d", h, ok, os.Getpid())
	}
}
