package testport

import (
	"net"
	"os"
	"path/filepath"
	"strconv"
	"testing"
)

// A port is below the ephemeral ranges, free to listen on, and held: a second reserve
// of it fails until the test that took it ends.
func TestFreeIsHeldAndBindable(t *testing.T) {
	seen := map[int]bool{}
	for range 20 {
		port := Free(t)
		if port < first || port >= last {
			t.Fatalf("port %d outside %d-%d", port, first, last)
		}
		if seen[port] {
			t.Fatalf("port %d handed out twice", port)
		}
		seen[port] = true
		lock := filepath.Join(os.TempDir(), "maktaba-test-ports", strconv.Itoa(port)+".lock")
		if release, ok := reserve(lock); ok {
			release()
			t.Fatalf("port %d not held", port)
		}
		ln, err := net.Listen("tcp", "127.0.0.1:"+strconv.Itoa(port))
		if err != nil {
			t.Fatalf("port %d: %v", port, err)
		}
		_ = ln.Close()
	}
}

// A port something listens on is never handed out.
func TestBindableSeesAListener(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	if bindable(ln.Addr().(*net.TCPAddr).Port) {
		t.Fatal("a port in use reads as bindable")
	}
}
