package config

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"errors"
	"io/fs"
	"math/big"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
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

// testCA is a CA certificate (PEM), and a leaf one that is not a CA.
func testCA(t *testing.T, isCA bool) string {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	tmpl := &x509.Certificate{SerialNumber: big.NewInt(1), Subject: pkix.Name{CommonName: "test CA"},
		NotBefore: time.Now().Add(-time.Hour), NotAfter: time.Now().Add(time.Hour), IsCA: isCA,
		BasicConstraintsValid: true, KeyUsage: x509.KeyUsageCertSign}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &key.PublicKey, key)
	if err != nil {
		t.Fatal(err)
	}
	return string(pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der}))
}

// The home's database is reached verifying its CA (no fallback to a plain connection),
// in the clear only on the home's word that it has no TLS, and as node.json says while
// neither is known.
func TestHomeDSN(t *testing.T) {
	keys := t.TempDir()
	base := NodeConfig{DatabaseURL: "postgres://maktaba_node_x:pw@192.168.1.5:54329/maktaba?sslmode=prefer"}
	if got, err := base.HomeDSN(keys); err != nil || got != base.DatabaseURL || base.DatabaseVerified() {
		t.Fatalf("unknown: %q %v", got, err)
	}
	ca := testCA(t, true)
	n := base
	n.DatabaseCA = ca
	got, err := n.HomeDSN(keys)
	if err != nil || !n.DatabaseVerified() {
		t.Fatal(err)
	}
	u, _ := url.Parse(got)
	if u.Query().Get("sslmode") != "verify-ca" || u.Query().Get("sslrootcert") != filepath.ToSlash(filepath.Join(keys, HomeDatabaseCAFile)) ||
		u.User.Username() != "maktaba_node_x" || u.Host != "192.168.1.5:54329" {
		t.Fatalf("with the CA: %q", got)
	}
	if b, err := os.ReadFile(filepath.Join(keys, HomeDatabaseCAFile)); err != nil || string(b) != ca {
		t.Fatalf("the CA pinned: %v", err)
	}
	off := base
	off.DatabaseTLS = "off"
	if got, _ := off.HomeDSN(keys); !strings.Contains(got, "sslmode=disable") || !off.DatabaseVerified() {
		t.Fatalf("TLS off: %q", got)
	}
	for _, bad := range []string{"not a certificate", testCA(t, false)} {
		n.DatabaseCA = bad
		got, err := n.HomeDSN(keys)
		if err == nil || !strings.Contains(got, "sslmode=verify-ca") {
			t.Fatalf("a CA that is not one: %v, and the DSN %q (it must still verify)", err, got)
		}
		if b, _ := os.ReadFile(filepath.Join(keys, HomeDatabaseCAFile)); string(b) != ca {
			t.Fatal("a CA that is not one replaced the pinned one")
		}
	}
}
