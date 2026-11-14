package testport

import (
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// kernelPort is a test taking a port from the kernel: listening on :0 and handing the
// number on after the listener closes.
var kernelPort = regexp.MustCompile(`net\.Listen\("tcp", "127\.0\.0\.1:0"\)`)

// bundleWithPort is a test giving a bundled Postgres a port of its own.
var bundleWithPort = regexp.MustCompile(`(?s)Bundle\{[^}]*Port:`)

// kernelPortUsers lists the test files under root that start a bundled Postgres
// (a pgbundle.Bundle with a Port) and also take a port from the kernel.
func kernelPortUsers(t *testing.T, root string) []string {
	t.Helper()
	var found []string
	err := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() || !strings.HasSuffix(path, "_test.go") {
			return nil
		}
		if d.Name() == "guard_test.go" && filepath.Base(filepath.Dir(path)) == "testport" {
			return nil // this file: its fixtures spell the pattern out
		}
		raw, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		if bundleWithPort.Match(raw) && kernelPort.Match(raw) {
			rel, _ := filepath.Rel(root, path)
			found = append(found, rel)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	return found
}

// Every test that starts a real Postgres takes its port from Free: a kernel port
// (listen on :0, close, hand on) is an ephemeral one, which an outgoing connection of a
// test package running in parallel can take before postgres binds it, and the server
// exits with "could not bind ... Address already in use".
func TestPostgresTestsTakePortsFromFree(t *testing.T) {
	if found := kernelPortUsers(t, filepath.Join("..", "..")); len(found) > 0 {
		t.Fatalf("these tests start a bundled Postgres on a kernel port; use testport.Free: %v", found)
	}
}

// The check sees the pattern it guards against (a positive control), and not a test
// whose kernel port serves something else.
func TestKernelPortUsersSeesTheRace(t *testing.T) {
	dir := t.TempDir()
	write := func(name, body string) {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	write("racy_test.go", `func freePort(t *testing.T) int {
	l, _ := net.Listen("tcp", "127.0.0.1:0")
	defer l.Close()
	return l.Addr().(*net.TCPAddr).Port
}
var b = pgbundle.Bundle{Home: home, Data: dir,
	Port: freePort(t)}`)
	write("fine_test.go", `var b = pgbundle.Bundle{Home: home, Port: testport.Free(t)}`)
	write("fake_test.go", `ln, _ := net.Listen("tcp", "127.0.0.1:0") // a fake server, kept open`)
	found := kernelPortUsers(t, dir)
	if len(found) != 1 || found[0] != "racy_test.go" {
		t.Fatalf("found %v, want [racy_test.go]", found)
	}
}
