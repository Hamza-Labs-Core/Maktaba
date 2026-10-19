package cluster

import (
	"os"
	"path/filepath"
	"testing"
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
	code := m.Run()
	_ = os.RemoveAll(dir)
	os.Exit(code)
}
