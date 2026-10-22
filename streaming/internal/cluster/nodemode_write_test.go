package cluster

import (
	"os"
	"path/filepath"
	"testing"
)

// A write of node.json that does not end (its lock taken a moment ago, node.json
// missing) never makes this host the home: after the readers' wait NodeMode is an
// error, so streaming does not start, rather than run as the home.
func TestNodeModeDuringAWriteThatDoesNotEnd(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "node.json")
	if err := os.WriteFile(filepath.Join(dir, ".node.json.lock"), nil, 0o600); err != nil {
		t.Fatal(err)
	}
	getenv := func(k string) string {
		if k == "MAKTABA_NODE_CONFIG" {
			return path
		}
		return ""
	}
	if ns, ok, err := NodeMode(getenv, "windows", dir, os.ReadFile); err == nil || ok {
		t.Fatalf("NodeMode = %+v %v %v, want an error", ns, ok, err)
	}
}
