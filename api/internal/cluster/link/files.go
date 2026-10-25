package link

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

// A machine's certificate files, beside its node.json (design §18.2: its key is made on
// the machine and never leaves it). The pipeline's join writes them when the home seals
// a certificate into the approval; a machine joined before gets them over its channel
// session (cert.csr) and renews them over the link (cert.renew).
const (
	NodeKeyFile  = "cluster-node.key"
	NodeCertFile = "cluster-node.crt"
	NodeCAFile   = "cluster-ca.crt"
)

// WebCertFile and WebKeyFile are the machine's certificate for browser HTTPS on 8443
// (AC15), kept while the home's rule says so (the supervisor serves 8443 while they are).
const (
	WebCertFile = "cluster-web.crt"
	WebKeyFile  = "cluster-web.key"
)

// SaveWeb keeps a web certificate with its key, or (nil) removes them.
func (f CertFiles) SaveWeb(certPEM, keyPEM []byte) error {
	if certPEM == nil {
		_ = os.Remove(f.path(WebCertFile))
		_ = os.Remove(f.path(WebKeyFile))
		return nil
	}
	if _, err := tls.X509KeyPair(certPEM, keyPEM); err != nil {
		return err
	}
	for _, w := range []struct {
		name string
		b    []byte
	}{{WebKeyFile, keyPEM}, {WebCertFile, certPEM}} {
		tmp := f.path(w.name) + ".tmp"
		if err := os.WriteFile(tmp, w.b, 0o600); err != nil {
			return err
		}
		if err := os.Rename(tmp, f.path(w.name)); err != nil {
			return err
		}
	}
	return nil
}

// webDue reports whether the web certificate is missing or within 30 days of its end.
func (f CertFiles) webDue(now time.Time) bool {
	pair, err := tls.LoadX509KeyPair(f.path(WebCertFile), f.path(WebKeyFile))
	if err != nil {
		return true
	}
	leaf, err := x509.ParseCertificate(pair.Certificate[0])
	return err != nil || leaf.NotAfter.Sub(now) < 30*24*time.Hour
}

// CertFiles are a machine's certificate files in Dir.
type CertFiles struct{ Dir string }

func (f CertFiles) path(name string) string { return filepath.Join(f.Dir, name) }

// Material is a machine's loaded certificate.
type Material struct {
	Pair     tls.Certificate
	Leaf     *x509.Certificate
	CA       *x509.CertPool
	CACert   *x509.Certificate
	CASHA256 string
}

// Remaining is the share of the certificate's life left at now (0 when expired).
func (m *Material) Remaining(now time.Time) float64 {
	life := m.Leaf.NotAfter.Sub(m.Leaf.NotBefore)
	if life <= 0 {
		return 0
	}
	left := m.Leaf.NotAfter.Sub(now)
	if left <= 0 {
		return 0
	}
	return float64(left) / float64(life)
}

// ErrNoCertificate (files): the machine has no certificate yet.
var ErrNoMaterial = errors.New("link: this machine has no cluster certificate yet")

// Load reads the machine's key, certificate and the pinned CA.
func (f CertFiles) Load() (*Material, error) {
	if f.Dir == "" {
		return nil, ErrNoMaterial
	}
	keyPEM, err1 := os.ReadFile(f.path(NodeKeyFile))
	certPEM, err2 := os.ReadFile(f.path(NodeCertFile))
	caPEM, err3 := os.ReadFile(f.path(NodeCAFile))
	if err1 != nil || err2 != nil || err3 != nil {
		return nil, ErrNoMaterial
	}
	pair, err := tls.X509KeyPair(certPEM, keyPEM)
	if err != nil {
		return nil, fmt.Errorf("link: the machine's certificate: %w", err)
	}
	leaf, err := x509.ParseCertificate(pair.Certificate[0])
	if err != nil {
		return nil, err
	}
	pair.Leaf = leaf
	cb, _ := pem.Decode(caPEM)
	if cb == nil {
		return nil, errors.New("link: the pinned cluster CA is not PEM")
	}
	caCert, err := x509.ParseCertificate(cb.Bytes)
	if err != nil || !caCert.IsCA {
		return nil, errors.New("link: the pinned cluster CA is not a CA certificate")
	}
	pool := x509.NewCertPool()
	pool.AddCert(caCert)
	sum := sha256.Sum256(caCert.Raw)
	return &Material{Pair: pair, Leaf: leaf, CA: pool, CACert: caCert, CASHA256: hex.EncodeToString(sum[:])}, nil
}

// Save writes a key, its certificate and the CA (0600, each replaced atomically). The
// CA must match casha256 when given (the join bundle pins it); a certificate that does not
// chain to it, or whose key is not keyPEM's, is refused.
func (f CertFiles) Save(keyPEM, certPEM, caPEM []byte, casha256 string) error {
	cb, _ := pem.Decode(caPEM)
	if cb == nil {
		return errors.New("link: the cluster CA is not PEM")
	}
	caCert, err := x509.ParseCertificate(cb.Bytes)
	if err != nil || !caCert.IsCA {
		return errors.New("link: the cluster CA is not a CA certificate")
	}
	if casha256 != "" {
		sum := sha256.Sum256(caCert.Raw)
		if !strings.EqualFold(hex.EncodeToString(sum[:]), strings.TrimSpace(casha256)) {
			return errors.New("link: the cluster CA does not match its fingerprint")
		}
	}
	pair, err := tls.X509KeyPair(certPEM, keyPEM)
	if err != nil {
		return fmt.Errorf("link: the certificate does not match the key: %w", err)
	}
	leaf, err := x509.ParseCertificate(pair.Certificate[0])
	if err != nil {
		return err
	}
	pool := x509.NewCertPool()
	pool.AddCert(caCert)
	if _, err := leaf.Verify(x509.VerifyOptions{Roots: pool, KeyUsages: []x509.ExtKeyUsage{x509.ExtKeyUsageClientAuth},
		CurrentTime: leaf.NotBefore.Add(time.Minute)}); err != nil {
		return fmt.Errorf("link: the certificate does not chain to the cluster CA: %w", err)
	}
	if err := os.MkdirAll(f.Dir, 0o700); err != nil {
		return err
	}
	// the CA first, the key last: a reader between two files sees a key that does not
	// match its certificate (refused, it tries again) rather than a half-new set
	for _, w := range []struct {
		name string
		b    []byte
	}{{NodeCAFile, caPEM}, {NodeCertFile, certPEM}, {NodeKeyFile, keyPEM}} {
		tmp := f.path(w.name) + ".tmp"
		if err := os.WriteFile(tmp, w.b, 0o600); err != nil {
			return err
		}
		if err := os.Rename(tmp, f.path(w.name)); err != nil {
			return err
		}
	}
	return nil
}

// NewKeyAndCSR makes a machine's key (P-256) and its certificate request.
func NewKeyAndCSR(nodeID string) (keyPEM, csrPEM []byte, err error) {
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return nil, nil, err
	}
	der, err := x509.CreateCertificateRequest(rand.Reader, &x509.CertificateRequest{
		Subject: pkix.Name{CommonName: nodeID}}, key)
	if err != nil {
		return nil, nil, err
	}
	kder, err := x509.MarshalECPrivateKey(key)
	if err != nil {
		return nil, nil, err
	}
	return pem.EncodeToMemory(&pem.Block{Type: "EC PRIVATE KEY", Bytes: kder}),
		pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE REQUEST", Bytes: der}), nil
}
