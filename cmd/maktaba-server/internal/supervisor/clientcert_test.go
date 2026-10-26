package supervisor

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/Hamza-Labs-Core/Maktaba/cmd/maktaba-server/internal/config"
)

// A machine says its processes present its cluster certificate to its home's database
// only when the connection they log in with (HomeDSN) offers it: its certificate and key
// beside node.json, on a verified TLS connection; never on a plain one, nor before it
// joined.
func TestPresentsClientCertFollowsTheRolesConnection(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("MAKTABA_CONFIG", filepath.Join(dir, "config.toml")) // the keys folder: the test's
	was := nodeConfigDir
	nodeConfigDir = func() string { return dir }
	t.Cleanup(func() { nodeConfigDir = was })
	n := config.NodeConfig{DatabaseURL: "postgres://maktaba_node_x:p@home:54329/maktaba?sslmode=verify-ca&sslrootcert=ca.crt"}
	if PresentsClientCert(n) {
		t.Fatal("no certificate beside node.json yet, but presented")
	}
	for _, f := range []string{"cluster-node.crt", "cluster-node.key"} {
		if err := os.WriteFile(filepath.Join(dir, f), []byte("x"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	if !PresentsClientCert(n) {
		t.Fatal("its certificate and key beside node.json, on verified TLS: not presented")
	}
	off := n
	off.DatabaseTLS = "off"
	if PresentsClientCert(off) {
		t.Fatal("a plain connection presents no certificate")
	}
	if PresentsClientCert(config.NodeConfig{}) {
		t.Fatal("not joined, but presented")
	}
	if err := os.Remove(filepath.Join(dir, "cluster-node.key")); err != nil {
		t.Fatal(err)
	}
	if PresentsClientCert(n) {
		t.Fatal("no key: not presented")
	}
}
