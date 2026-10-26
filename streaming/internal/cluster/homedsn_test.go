package cluster

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/json"
	"encoding/pem"
	"math/big"
	"net/url"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func testCAPEM(t *testing.T) string {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	tmpl := &x509.Certificate{SerialNumber: big.NewInt(1), Subject: pkix.Name{CommonName: "home database CA"},
		NotBefore: time.Now().Add(-time.Hour), NotAfter: time.Now().Add(time.Hour), IsCA: true,
		BasicConstraintsValid: true, KeyUsage: x509.KeyUsageCertSign}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &key.PublicKey, key)
	if err != nil {
		t.Fatal(err)
	}
	return string(pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der}))
}

// The node's connection from node.json is the roles' (the server's supervisor.HomeDSN):
// the home's CA verified, the machine's certificate offered; plain on the home's word that
// it has no TLS; as node.json has it while neither is known.
func TestHomeDSN(t *testing.T) {
	dir := t.TempDir()
	const joined = "postgres://maktaba_node_6f1c2a3b:pw@192.168.50.177:54329/maktaba?sslmode=prefer"
	s := NodeSettings{DatabaseURL: joined, ConfigPath: filepath.Join(dir, "node.json"), DatabaseCA: testCAPEM(t)}
	query := func(dsn string) url.Values {
		u, err := url.Parse(dsn)
		if err != nil {
			t.Fatal(err)
		}
		return u.Query()
	}
	dsn, err := s.HomeDSN()
	if err != nil {
		t.Fatal(err)
	}
	if q := query(dsn); q.Get("sslmode") != "verify-ca" || q.Get("sslcert") != "" ||
		q.Get("sslrootcert") != filepath.ToSlash(filepath.Join(dir, HomeDatabaseCAFile)) {
		t.Fatalf("no certificate yet: %v", q)
	}
	if b, _ := os.ReadFile(filepath.Join(dir, HomeDatabaseCAFile)); string(b) != s.DatabaseCA {
		t.Fatal("the CA kept beside node.json is not node.json's")
	}
	for _, f := range []string{"cluster-node.crt", "cluster-node.key"} {
		if err := os.WriteFile(filepath.Join(dir, f), []byte("x"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	dsn, _ = s.HomeDSN()
	if q := query(dsn); q.Get("sslcert") != filepath.ToSlash(filepath.Join(dir, "cluster-node.crt")) ||
		q.Get("sslkey") != filepath.ToSlash(filepath.Join(dir, "cluster-node.key")) {
		t.Fatalf("the certificate: %v", q)
	}
	off := NodeSettings{DatabaseURL: joined, ConfigPath: s.ConfigPath, DatabaseTLS: "off"}
	if dsn, _ := off.HomeDSN(); query(dsn).Get("sslmode") != "disable" || query(dsn).Get("sslcert") != "" {
		t.Fatalf("no TLS: %s", dsn)
	}
	if dsn, _ := (NodeSettings{DatabaseURL: joined, ConfigPath: s.ConfigPath}).HomeDSN(); dsn != joined {
		t.Fatalf("neither known: %s", dsn)
	}
	bad := NodeSettings{DatabaseURL: joined, ConfigPath: s.ConfigPath, DatabaseCA: "not a CA"}
	if _, err := bad.HomeDSN(); err == nil {
		t.Fatal("a CA that is no certificate")
	}
}

// node.json's CA and TLS word reach the node's settings.
func TestNodeModeReadsTheDatabaseTLS(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "node.json")
	ca := testCAPEM(t)
	b, _ := json.Marshal(map[string]string{"node_id": "n1", "database_url": "postgres://r:p@h:54329/maktaba",
		"database_ca": ca, "database_tls": "off"})
	if err := os.WriteFile(path, b, 0o600); err != nil {
		t.Fatal(err)
	}
	env := map[string]string{"MAKTABA_NODE_CONFIG": path}
	ns, ok, err := NodeMode(func(k string) string { return env[k] }, "linux", dir, os.ReadFile)
	if err != nil || !ok || ns.DatabaseCA != ca || ns.DatabaseTLS != "off" {
		t.Fatalf("%+v %v %v", ns, ok, err)
	}
}
