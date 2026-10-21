package main

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/json"
	"encoding/pem"
	"math/big"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/Hamza-Labs-Core/Maktaba/cmd/maktaba-server/internal/config"
)

func testCAPEM(t *testing.T) string {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	tmpl := &x509.Certificate{SerialNumber: big.NewInt(7), Subject: pkix.Name{CommonName: "home database CA"},
		NotBefore: time.Now().Add(-time.Hour), NotAfter: time.Now().Add(time.Hour), IsCA: true,
		BasicConstraintsValid: true, KeyUsage: x509.KeyUsageCertSign}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &key.PublicKey, key)
	if err != nil {
		t.Fatal(err)
	}
	return string(pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der}))
}

// The home hands its database CA over the machine channel: node.json keeps it (and
// what else the file has), the machine starts again with it; the same CA again changes
// nothing; a CA that is not one is refused.
func TestHomeDatabaseOverTheChannel(t *testing.T) {
	dir := t.TempDir()
	node := filepath.Join(dir, "node.json")
	t.Setenv("MAKTABA_NODE_CONFIG", node)
	t.Setenv("MAKTABA_CONFIG", filepath.Join(dir, "server.toml"))
	if err := os.WriteFile(node, []byte(`{"node_id":"n1","name":"studio","private_key":"k",
	  "home":{"url":"http://home:8080"},"database_url":"postgres://r:p@home:54329/maktaba?sslmode=prefer",
	  "joined_at":"2026-10-01T10:00:00Z"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	for len(reloadCh) > 0 {
		<-reloadCh
	}
	s := newSwitching(config.Config{})
	ca := testCAPEM(t)
	args, _ := json.Marshal(map[string]string{"ca_pem": ca})
	handled, ok, detail := s.command(t.Context(), "home.database", args)
	if !handled || !ok {
		t.Fatalf("the CA: %v %v", ok, detail)
	}
	n := config.LocalNodeConfig()
	if n.DatabaseCA != ca || !n.DatabaseVerified() {
		t.Fatalf("node.json: %+v", n)
	}
	var raw map[string]any
	b, _ := os.ReadFile(node)
	_ = json.Unmarshal(b, &raw)
	if raw["joined_at"] != "2026-10-01T10:00:00Z" {
		t.Fatalf("node.json lost the pipeline's own fields: %s", b)
	}
	select {
	case <-reloadCh:
	case <-time.After(5 * time.Second):
		t.Fatal("the machine does not start again with the CA")
	}
	if _, ok, detail := s.command(t.Context(), "home.database", args); !ok || detail.(map[string]any)["changed"] != false {
		t.Fatalf("the same CA again: %v %v", ok, detail)
	}
	bad, _ := json.Marshal(map[string]string{"ca_pem": "not a certificate"})
	if _, ok, _ := s.command(t.Context(), "home.database", bad); ok {
		t.Fatal("a CA that is not one was taken")
	}
	// the home's database without TLS: plain, on its word
	off, _ := json.Marshal(map[string]bool{"tls_off": true})
	if _, ok, _ := s.command(t.Context(), "home.database", off); !ok {
		t.Fatal("TLS off")
	}
	if n := config.LocalNodeConfig(); n.DatabaseCA != "" || n.DatabaseTLS != "off" {
		t.Fatalf("node.json with TLS off: %+v", n)
	}
}
