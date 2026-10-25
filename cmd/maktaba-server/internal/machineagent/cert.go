package machineagent

// The machine's cluster-link certificate (Epic 35 story 35.16, design §18.3). A machine
// joined before the link (or whose certificate expired while it was off for a month)
// asks for one on its channel session (cert.csr): the session's v2 handshake proved both
// sides, so the CA that comes back on it is as trustworthy as a join's. Its key is made
// here and never leaves the machine. The files sit beside node.json, where the node's API
// (which holds the machine's link and renews the certificate over it) reads them; the
// file names and checks are the API's (api/internal/cluster/link/files.go).

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/sha256"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/hex"
	"encoding/pem"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// Certificate files beside node.json.
const (
	CertKeyFile  = "cluster-node.key"
	CertFile     = "cluster-node.crt"
	CertCAFile   = "cluster-ca.crt"
	csrKeyFile   = "cluster-node.key.pending"
	maxCertBytes = 64 << 10
)

// needCert reports whether the machine must ask for a certificate on its channel
// session: none yet, unreadable, or expired (a valid one is renewed over the link).
func needCert(dir string, now time.Time) bool {
	if dir == "" {
		return false
	}
	certPEM, err1 := os.ReadFile(filepath.Join(dir, CertFile))
	keyPEM, err2 := os.ReadFile(filepath.Join(dir, CertKeyFile))
	_, err3 := os.Stat(filepath.Join(dir, CertCAFile))
	if err1 != nil || err2 != nil || err3 != nil {
		return true
	}
	pair, err := tls.X509KeyPair(certPEM, keyPEM)
	if err != nil {
		return true
	}
	leaf, err := x509.ParseCertificate(pair.Certificate[0])
	return err != nil || !now.Before(leaf.NotAfter)
}

// newCSR makes the machine's next key (kept aside until its certificate comes) and its
// certificate request.
func newCSR(dir, nodeID string) (string, error) {
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return "", err
	}
	der, err := x509.CreateCertificateRequest(rand.Reader, &x509.CertificateRequest{Subject: pkix.Name{CommonName: nodeID}}, key)
	if err != nil {
		return "", err
	}
	kder, err := x509.MarshalECPrivateKey(key)
	if err != nil {
		return "", err
	}
	if err := writeFile(filepath.Join(dir, csrKeyFile), pem.EncodeToMemory(&pem.Block{Type: "EC PRIVATE KEY", Bytes: kder})); err != nil {
		return "", err
	}
	return string(pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE REQUEST", Bytes: der})), nil
}

// saveCert puts a certificate the home issued in place with the key kept aside for it:
// the CA must match its fingerprint, the certificate the key, and chain to the CA.
func saveCert(dir, certPEM, caPEM, caSHA256 string) error {
	if len(certPEM) > maxCertBytes || len(caPEM) > maxCertBytes {
		return errors.New("machineagent: the certificate is too large")
	}
	keyPEM, err := os.ReadFile(filepath.Join(dir, csrKeyFile))
	if err != nil {
		return fmt.Errorf("machineagent: the key for the certificate is gone: %w", err)
	}
	cb, _ := pem.Decode([]byte(caPEM))
	if cb == nil {
		return errors.New("machineagent: the cluster CA is not PEM")
	}
	ca, err := x509.ParseCertificate(cb.Bytes)
	if err != nil || !ca.IsCA {
		return errors.New("machineagent: the cluster CA is not a CA certificate")
	}
	sum := sha256.Sum256(ca.Raw)
	if caSHA256 != "" && !strings.EqualFold(hex.EncodeToString(sum[:]), strings.TrimSpace(caSHA256)) {
		return errors.New("machineagent: the cluster CA does not match its fingerprint")
	}
	pair, err := tls.X509KeyPair([]byte(certPEM), keyPEM)
	if err != nil {
		return fmt.Errorf("machineagent: the certificate does not match its key: %w", err)
	}
	leaf, err := x509.ParseCertificate(pair.Certificate[0])
	if err != nil {
		return err
	}
	pool := x509.NewCertPool()
	pool.AddCert(ca)
	if _, err := leaf.Verify(x509.VerifyOptions{Roots: pool, KeyUsages: []x509.ExtKeyUsage{x509.ExtKeyUsageClientAuth},
		CurrentTime: leaf.NotBefore.Add(time.Minute)}); err != nil {
		return fmt.Errorf("machineagent: the certificate does not chain to the cluster CA: %w", err)
	}
	// the CA first, the key last: a reader in between sees a key that does not match its
	// certificate (refused, read again) rather than a half-new set
	for _, f := range []struct {
		name string
		b    []byte
	}{{CertCAFile, []byte(caPEM)}, {CertFile, []byte(certPEM)}, {CertKeyFile, keyPEM}} {
		if err := writeFile(filepath.Join(dir, f.name), f.b); err != nil {
			return err
		}
	}
	_ = os.Remove(filepath.Join(dir, csrKeyFile))
	return nil
}

func writeFile(path string, b []byte) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, b, 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}
