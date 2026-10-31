//go:build unix

package portholder

import (
	"os"
	"path/filepath"
	"testing"
)

// TestFromProc: a fake /proc whose net/tcp lists a listener and whose process 77
// holds its socket.
func TestFromProc(t *testing.T) {
	root := t.TempDir()
	must := func(err error) {
		t.Helper()
		if err != nil {
			t.Fatal(err)
		}
	}
	must(os.MkdirAll(filepath.Join(root, "net"), 0o755))
	must(os.WriteFile(filepath.Join(root, "net", "tcp"), []byte("  sl  local_address rem_address   st tx_queue rx_queue tr tm->when retrnsmt   uid  timeout inode\n"+
		"   0: 0100007F:23A7 00000000:0000 0A 00000000:00000000 00:00000000 00000000  1000        0 31337 1\n"), 0o644))
	must(os.MkdirAll(filepath.Join(root, "77", "fd"), 0o755))
	must(os.WriteFile(filepath.Join(root, "77", "comm"), []byte("python3\n"), 0o644))
	must(os.Symlink("socket:[31337]", filepath.Join(root, "77", "fd", "5")))
	h, ok := fromProc(root, 9127)
	if !ok || h.PID != 77 || h.Command != "python3" {
		t.Fatalf("fromProc = %+v %v", h, ok)
	}
}
