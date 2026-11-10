package pgbundle

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// Epic 59 story 59.08 (T06): on a NAS (the host's network, so the NAS's whole LAN) the
// bundled Postgres listens on loopback until a node may join; a node's pairing opens it to
// the LAN with TLS, and it is back on loopback once the last node left.
func TestNASPostgresStaysOnLoopbackUntilANodeJoins(t *testing.T) {
	dir := t.TempDir()
	b := Bundle{Data: filepath.Join(dir, "postgres"), Keys: filepath.Join(dir, "keys"), Port: DefaultPort, LAN: true, TLS: true}
	if err := os.MkdirAll(b.Data, 0o700); err != nil {
		t.Fatal(err)
	}
	listen := func() string {
		args := strings.Join(b.ServerArgs(), " ")
		_, v, _ := strings.Cut(args, "listen_addresses=")
		v, _, _ = strings.Cut(v, " ")
		return v
	}
	if got := listen(); got != "127.0.0.1" {
		t.Fatalf("no node: listens on %q", got)
	}
	if err := b.KeepReach(Reach{Open: true, Addrs: []string{"192.168.1.7"}}); err != nil {
		t.Fatal(err)
	}
	if got := listen(); got != "*" {
		t.Fatalf("a node paired: listens on %q", got)
	}
	if err := b.KeepReach(Reach{}); err != nil {
		t.Fatal(err)
	}
	if got := listen(); got != "127.0.0.1" {
		t.Fatalf("the last node left: listens on %q", got)
	}
}
