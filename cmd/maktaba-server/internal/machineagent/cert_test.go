package machineagent

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/sha256"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/hex"
	"encoding/pem"
	"math/big"
	"net/url"
	"os"
	"path/filepath"
	"testing"
	"time"
)

// testCA signs a CSR the way the home's cluster CA does (a P-256 root, a 30-day leaf).
func testCA(t *testing.T) (sign func(csrPEM string, life time.Duration) string, caPEM, caSHA string) {
	t.Helper()
	key, _ := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	tmpl := &x509.Certificate{SerialNumber: big.NewInt(1), Subject: pkix.Name{CommonName: "test CA"}, IsCA: true,
		BasicConstraintsValid: true, KeyUsage: x509.KeyUsageCertSign, NotBefore: time.Now().Add(-time.Hour),
		NotAfter: time.Now().Add(365 * 24 * time.Hour)}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &key.PublicKey, key)
	if err != nil {
		t.Fatal(err)
	}
	ca, _ := x509.ParseCertificate(der)
	sum := sha256.Sum256(der)
	sign = func(csrPEM string, life time.Duration) string {
		b, _ := pem.Decode([]byte(csrPEM))
		csr, err := x509.ParseCertificateRequest(b.Bytes)
		if err != nil {
			t.Fatal(err)
		}
		u, _ := url.Parse("maktaba://node/n1")
		leaf := &x509.Certificate{SerialNumber: big.NewInt(time.Now().UnixNano()), URIs: []*url.URL{u},
			NotBefore: time.Now().Add(-5 * time.Minute), NotAfter: time.Now().Add(life), KeyUsage: x509.KeyUsageDigitalSignature,
			ExtKeyUsage: []x509.ExtKeyUsage{x509.ExtKeyUsageClientAuth}}
		ld, err := x509.CreateCertificate(rand.Reader, leaf, ca, csr.PublicKey, key)
		if err != nil {
			t.Fatal(err)
		}
		return string(pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: ld}))
	}
	return sign, string(pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der})), hex.EncodeToString(sum[:])
}

// A machine joined before the link asks for its certificate on its channel session and
// keeps it beside node.json with the key it made; an expired one is asked for again.
func TestCertRequestedAndKept(t *testing.T) {
	dir := t.TempDir()
	if !needCert(dir, time.Now()) {
		t.Fatal("a machine without a certificate does not need one")
	}
	sign, caPEM, caSHA := testCA(t)
	csr, err := newCSR(dir, "n1")
	if err != nil {
		t.Fatal(err)
	}
	// another fingerprint: its first digit always differs ("00"+… matched one CA in 256)
	other := "0" + caSHA[1:]
	if caSHA[0] == '0' {
		other = "1" + caSHA[1:]
	}
	if err := saveCert(dir, sign(csr, 30*24*time.Hour), caPEM, other); err == nil {
		t.Fatal("a CA that does not match its fingerprint was kept")
	}
	if err := saveCert(dir, sign(csr, 30*24*time.Hour), caPEM, caSHA); err != nil {
		t.Fatal(err)
	}
	if needCert(dir, time.Now()) {
		t.Fatal("a fresh certificate is asked for again")
	}
	if _, err := os.Stat(filepath.Join(dir, csrKeyFile)); !os.IsNotExist(err) {
		t.Fatal("the pending key was left behind")
	}
	if needCert(dir, time.Now().Add(31*24*time.Hour)) != true {
		t.Fatal("an expired certificate is not asked for again")
	}
	// a certificate for another key is refused
	other, _ := newCSR(t.TempDir(), "n1")
	if _, err := newCSR(dir, "n1"); err != nil {
		t.Fatal(err)
	}
	if err := saveCert(dir, sign(other, time.Hour), caPEM, caSHA); err == nil {
		t.Fatal("a certificate that does not match the pending key was kept")
	}
}
