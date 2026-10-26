package supervisor

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"math/big"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/Hamza-Labs-Core/Maktaba/cmd/maktaba-server/internal/config"
)

// writeNodeCert writes a certificate whose CN is cn, and its key, beside node.json (dir).
func writeNodeCert(t *testing.T, dir, cn string) {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	tmpl := &x509.Certificate{SerialNumber: big.NewInt(time.Now().UnixNano()), Subject: pkix.Name{CommonName: cn},
		NotBefore: time.Now().Add(-time.Hour), NotAfter: time.Now().Add(time.Hour)}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &key.PublicKey, key)
	if err != nil {
		t.Fatal(err)
	}
	kder, err := x509.MarshalECPrivateKey(key)
	if err != nil {
		t.Fatal(err)
	}
	for name, b := range map[string][]byte{
		"cluster-node.crt": pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der}),
		"cluster-node.key": pem.EncodeToMemory(&pem.Block{Type: "EC PRIVATE KEY", Bytes: kder}),
	} {
		if err := os.WriteFile(filepath.Join(dir, name), b, 0o600); err != nil {
			t.Fatal(err)
		}
	}
}

// A machine says its processes present its cluster certificate to its home's database
// only when the connection they log in with (HomeDSN) offers it, on a verified TLS
// connection, and it names the role they log in as (its CN): never on a plain one, never
// before it joined, and never with a certificate that names another role (a machine
// joined before 2026-10-18 logs in as its 8-hex role, its certificate may name the 32-hex
// one: the home would refuse it).
func TestPresentsClientCertFollowsTheRolesConnection(t *testing.T) {
	const role = "maktaba_node_0b6f3c1e2a4d4e8f9c715d2a8b3e6f10"
	dir := t.TempDir()
	t.Setenv("MAKTABA_CONFIG", filepath.Join(dir, "config.toml")) // the keys folder: the test's
	was := nodeConfigDir
	nodeConfigDir = func() string { return dir }
	t.Cleanup(func() { nodeConfigDir = was })
	n := config.NodeConfig{DatabaseURL: "postgres://" + role + ":p@home:54329/maktaba?sslmode=verify-ca&sslrootcert=ca.crt"}
	if PresentsClientCert(n) {
		t.Fatal("no certificate beside node.json yet, but presented")
	}
	writeNodeCert(t, dir, role)
	if !PresentsClientCert(n) {
		t.Fatal("its certificate, naming its role, and key beside node.json, on verified TLS: not presented")
	}
	off := n
	off.DatabaseTLS = "off"
	if PresentsClientCert(off) {
		t.Fatal("a plain connection presents no certificate")
	}
	if PresentsClientCert(config.NodeConfig{}) {
		t.Fatal("not joined, but presented")
	}
	// joined before 2026-10-18: it logs in as its 8-hex role, its certificate names the
	// whole id's
	early := config.NodeConfig{DatabaseURL: "postgres://maktaba_node_0b6f3c1e:p@home:54329/maktaba?sslmode=verify-ca&sslrootcert=ca.crt"}
	if PresentsClientCert(early) {
		t.Fatal("a certificate naming another role than the one it logs in as: presented")
	}
	writeNodeCert(t, dir, "maktaba_node_0b6f3c1e")
	if !PresentsClientCert(early) {
		t.Fatal("its certificate names its 8-hex role now: not presented")
	}
	if err := os.WriteFile(filepath.Join(dir, "cluster-node.crt"), []byte("not a certificate"), 0o600); err != nil {
		t.Fatal(err)
	}
	if PresentsClientCert(early) {
		t.Fatal("an unreadable certificate: presented")
	}
	if err := os.Remove(filepath.Join(dir, "cluster-node.key")); err != nil {
		t.Fatal(err)
	}
	if PresentsClientCert(n) {
		t.Fatal("no key: not presented")
	}
}
