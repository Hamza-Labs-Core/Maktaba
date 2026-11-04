// Package testport hands a test a TCP port for a server it starts itself (a real
// Postgres, an API): one no other program on the machine is given meanwhile.
//
// A port from the kernel (listen on :0, close, hand the number on) is one of its
// ephemeral ports, which it also gives every outgoing connection; between the close and
// the server's bind, a connection of any test package running in parallel can take it,
// and the server exits with "Address already in use". Ports from here lie below the
// ephemeral range, so no outgoing connection is given one, and each is reserved with a
// lock file the operating system releases when the test process ends, so two test
// processes never pick the same one.
package testport

import (
	"fmt"
	"math/rand/v2"
	"net"
	"os"
	"path/filepath"
	"strconv"
	"testing"
)

// The ports handed out: below every system's ephemeral range (Linux 32768-60999,
// macOS and Windows 49152-65535) and above the registered services most machines run.
const (
	first = 20000
	last  = 32000
)

// Free is a loopback port nothing listens on, held for this test (its lock is released
// at the test's end).
func Free(t testing.TB) int {
	t.Helper()
	dir := filepath.Join(os.TempDir(), "maktaba-test-ports")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	for range 200 {
		port := first + rand.IntN(last-first)
		release, ok := reserve(filepath.Join(dir, strconv.Itoa(port)+".lock"))
		if !ok {
			continue // another test process holds it
		}
		if !bindable(port) {
			release()
			continue
		}
		t.Cleanup(release)
		return port
	}
	t.Fatal(fmt.Errorf("no free port in %d-%d", first, last))
	return 0
}

// bindable: a server may listen on port, on loopback and on every address (a LAN
// cluster's Postgres listens on *).
func bindable(port int) bool {
	for _, addr := range []string{"127.0.0.1", ""} {
		ln, err := net.Listen("tcp", net.JoinHostPort(addr, strconv.Itoa(port)))
		if err != nil {
			return false
		}
		_ = ln.Close()
	}
	return true
}
