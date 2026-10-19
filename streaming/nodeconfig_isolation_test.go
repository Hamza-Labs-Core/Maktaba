package main

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/Hamza-Labs-Core/Maktaba/streaming/internal/cluster"
)

// TestMain points MAKTABA_NODE_CONFIG at a file that never exists, so no test
// here (nor a process it spawns) reads this machine's real node config: on a
// joined machine it holds a database credential and would switch the code under
// test into node mode. A test that needs a node config sets its own (t.Setenv).
func TestMain(m *testing.M) {
	dir, err := os.MkdirTemp("", "maktaba-nodecfg-")
	if err != nil {
		panic(err)
	}
	_ = os.Setenv("MAKTABA_NODE_CONFIG", filepath.Join(dir, "node.json"))
	_ = os.Unsetenv("MAKTABA_TV_HOST_ROLE")
	// no test listens on the LAN (loopback_guard_test.go)
	guardReport := installLoopbackGuard()
	code := m.Run()
	if g := guardReport(); code == 0 {
		code = g
	}
	_ = os.RemoveAll(dir)
	os.Exit(code)
}

// TestNodeConfigIsolated: runServe's node-mode check sees no node config here
// (TestServeIntegration used to read %APPDATA%\Maktaba\node.json on a joined PC).
func TestNodeConfigIsolated(t *testing.T) {
	if _, node := cluster.NodeModeFromOS(runtime.GOOS); node {
		t.Fatal("a test sees a node config: MAKTABA_NODE_CONFIG must point at a temp path")
	}
	if p := os.Getenv("MAKTABA_NODE_CONFIG"); !strings.HasPrefix(filepath.Clean(p), filepath.Clean(os.TempDir())) {
		t.Fatalf("MAKTABA_NODE_CONFIG=%q is not a temp path", p)
	}
}
