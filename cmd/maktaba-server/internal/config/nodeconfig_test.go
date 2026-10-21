package config

import (
	"errors"
	"io/fs"
	"strings"
	"testing"
)

// TestReadNodeConfigUnreadable: a node.json with a syntax error (a typo, a truncated
// file) read as a zero config, not joined, and a joined node started as a home. It
// now carries the path and the error; only a missing file is "not joined".
func TestReadNodeConfigUnreadable(t *testing.T) {
	getenv := func(k string) string {
		if k == "MAKTABA_NODE_CONFIG" {
			return "/etc/maktaba/node.json"
		}
		return ""
	}
	read := func(b []byte, err error) func(string) ([]byte, error) {
		return func(string) ([]byte, error) { return b, err }
	}

	n := ReadNodeConfig(getenv, "linux", "/home/u", read([]byte(`{"node_id": "6f1c9a2e", "database_url": "postgres://n:pw@h`), nil))
	if n.Err == nil || n.Joined() || !strings.Contains(n.Err.Error(), "/etc/maktaba/node.json") ||
		!strings.Contains(n.Err.Error(), "unexpected end of JSON input") || strings.Contains(n.Err.Error(), "pw@h") {
		t.Fatalf("truncated node.json: %+v (err %v)", n, n.Err)
	}
	if n := ReadNodeConfig(getenv, "linux", "/home/u", read(nil, fs.ErrPermission)); !errors.Is(n.Err, fs.ErrPermission) {
		t.Fatalf("unreadable node.json: err %v", n.Err)
	}
	if n := ReadNodeConfig(getenv, "linux", "/home/u", read(nil, fs.ErrNotExist)); n.Err != nil || n.Joined() {
		t.Fatalf("no node.json: %+v (err %v)", n, n.Err)
	}
	n = ReadNodeConfig(getenv, "linux", "/home/u", read([]byte(`{"node_id": " 6f1c9a2e ", "database_url": "postgres://n@h/db"}`), nil))
	if n.Err != nil || !n.Joined() || n.NodeID != "6f1c9a2e" {
		t.Fatalf("node.json: %+v (err %v)", n, n.Err)
	}
}
