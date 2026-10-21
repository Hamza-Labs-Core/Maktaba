package config

import (
	"fmt"
	"os"
	"path/filepath"
	"testing"
)

// TestMain points MAKTABA_NODE_CONFIG at a file that never exists, so no test
// here (nor a process it spawns) reads this machine's real node config: on a
// joined machine it holds a database credential and would switch the code under
// test into node mode. A test that needs a node config sets its own (t.Setenv).
func TestMain(m *testing.M) {
	// the test binary as a second maktaba-server start (move_lock_test.go)
	if mode := os.Getenv("MAKTABA_TEST_MOVE_CHILD"); mode != "" {
		os.Exit(moveChild(mode))
	}
	dir, err := os.MkdirTemp("", "maktaba-nodecfg-")
	if err != nil {
		panic(err)
	}
	_ = os.Setenv("MAKTABA_NODE_CONFIG", filepath.Join(dir, "node.json"))
	_ = os.Unsetenv("MAKTABA_TV_HOST_ROLE")
	// no test binds a wildcard or LAN address (loopback_guard_test.go)
	listenTCP = guardedListen
	code := m.Run()
	if code == 0 && len(guardViolations()) > 0 {
		for _, v := range guardViolations() {
			fmt.Fprintln(os.Stderr, "loopback guard:", v)
		}
		code = 1
	}
	_ = os.RemoveAll(dir)
	os.Exit(code)
}
