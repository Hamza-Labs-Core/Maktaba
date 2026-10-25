package main

import (
	"bytes"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"math/big"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Hamza-Labs-Core/Maktaba/cmd/maktaba-server/internal/config"
)

// For a Postgres Maktaba does not run, setup --pg-tls issues its server certificate from
// the cluster CA and prints the settings (story 35.16 AC12, EC5); without a CA it says to
// run it on the home.
func TestSetupPGTLSForAnOperatorPostgres(t *testing.T) {
	keys := t.TempDir()
	cfg := config.Config{}
	cfg.Postgres.Enabled = "off"
	var out bytes.Buffer
	if err := runSetupPGTLS(&out, cfg, keys, []string{"db.lan"}, time.Now()); err == nil {
		t.Fatal("no cluster CA, yet it went on")
	}
	key, _ := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	tmpl := &x509.Certificate{SerialNumber: big.NewInt(1), Subject: pkix.Name{CommonName: "CA"}, IsCA: true,
		BasicConstraintsValid: true, KeyUsage: x509.KeyUsageCertSign, NotBefore: time.Now().Add(-time.Hour),
		NotAfter: time.Now().Add(10 * 365 * 24 * time.Hour)}
	der, _ := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &key.PublicKey, key)
	kder, _ := x509.MarshalECPrivateKey(key)
	_ = os.WriteFile(filepath.Join(keys, "cluster-ca.crt"), pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der}), 0o600)
	_ = os.WriteFile(filepath.Join(keys, "cluster-ca.key"), pem.EncodeToMemory(&pem.Block{Type: "EC PRIVATE KEY", Bytes: kder}), 0o600)
	out.Reset()
	if err := runSetupPGTLS(&out, cfg, keys, []string{"db.lan", "192.168.1.5"}, time.Now()); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), "ssl_ca_file") || !strings.Contains(out.String(), "clientcert=verify-full") {
		t.Fatalf("printed:\n%s", out.String())
	}
	b, err := os.ReadFile(filepath.Join(keys, pgServerCertFile))
	if err != nil {
		t.Fatal(err)
	}
	blk, _ := pem.Decode(b)
	leaf, _ := x509.ParseCertificate(blk.Bytes)
	ca, _ := x509.ParseCertificate(der)
	pool := x509.NewCertPool()
	pool.AddCert(ca)
	if _, err := leaf.Verify(x509.VerifyOptions{Roots: pool, DNSName: "db.lan"}); err != nil {
		t.Fatal(err)
	}
}
