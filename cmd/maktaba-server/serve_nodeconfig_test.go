package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/Hamza-Labs-Core/Maktaba/cmd/maktaba-server/internal/config"
)

// maktaba-server's writes of node.json (a switch of the home, a standby taking over, the
// home's database CA) keep what only the pipeline writes (joined_at, machine_id), also
// from a node.json an editor saved with a byte order mark (UTF-8 or UTF-16), and leave
// plain UTF-8 JSON behind.
func TestWriteNodeConfigKeepsThePipelinesFields(t *testing.T) {
	const text = `{"node_id":"n1","private_key":"k","database_url":"postgres://h","joined_at":"2026-01-01T00:00:00Z","machine_id":"pc-a"}`
	utf16le := []byte{0xff, 0xfe}
	for _, c := range text {
		utf16le = append(utf16le, byte(c), 0)
	}
	for name, saved := range map[string][]byte{
		"UTF-8 with a BOM": append([]byte("\xef\xbb\xbf"), text...),
		"UTF-16":           utf16le,
	} {
		t.Run(name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "node.json")
			t.Setenv("MAKTABA_NODE_CONFIG", path)
			if err := os.WriteFile(path, saved, 0o600); err != nil {
				t.Fatal(err)
			}
			n := config.LocalNodeConfig()
			if n.Err != nil || !n.Joined() {
				t.Fatalf("read: %+v (err %v)", n, n.Err)
			}
			n.DatabaseCA = "PEM"
			if err := writeNodeConfig(n); err != nil {
				t.Fatal(err)
			}
			b, err := os.ReadFile(path) //nolint:gosec // the test's temp file
			if err != nil {
				t.Fatal(err)
			}
			var got map[string]any
			if err := json.Unmarshal(b, &got); err != nil {
				t.Fatalf("not plain UTF-8 JSON: %v (% x)", err, b[:4])
			}
			if got["joined_at"] != "2026-01-01T00:00:00Z" || got["machine_id"] != "pc-a" || got["database_ca"] != "PEM" {
				t.Fatalf("after the write: %v", got)
			}
		})
	}
}
