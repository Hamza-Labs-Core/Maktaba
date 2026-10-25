// Package ca is the home's cluster CA (Epic 35 story 35.16, design §18.2): a small
// ECDSA P-256 root, valid ten years, created by the home's API at its first start after
// the story. It signs every machine's certificate from a CSR (the machine's key never
// leaves it) and the home's own certificates (server, pg_server, client, web).
//
// The CA's key lives on the home only: cluster-ca.key (0600) beside secrets.key, and a
// sealed copy in home_secrets (cluster.ca.key) so that a restore of the database and the
// secrets key brings the same CA back (EC6) and every machine's certificate stays valid.
// It is never written on a node, on the share, or in a diagnostics bundle; nothing serves
// it (only the CA certificate is public: GET /api/cluster/ca.crt).
package ca

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/sha256"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/hex"
	"encoding/json"
	"encoding/pem"
	"errors"
	"fmt"
	"math/big"
	"net"
	"net/url"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/Hamza-Labs-Core/Maktaba/api/internal/cluster"
	"github.com/Hamza-Labs-Core/Maktaba/api/internal/secrets"
)

// File and secret names.
const (
	KeyFile    = "cluster-ca.key"
	CertFile   = "cluster-ca.crt"
	SecretName = "cluster.ca.key"
)

// Lifetimes of design §18.2.
const (
	CALifetime = 10 * 365 * 24 * time.Hour
	// LeafDays is a machine certificate's life (cluster.tls.cert_days).
	LeafDays = 30
	// Skew is how far back a certificate's not_before goes (a machine's clock may lag).
	Skew = 5 * time.Minute
)

// Certificate kinds (cluster_certs.kind).
const (
	KindNode     = "node"
	KindServer   = "server"
	KindPGServer = "pg_server"
	KindClient   = "client"
	KindWeb      = "web"
)

// NodeURI is the SAN that names a machine in its certificate.
func NodeURI(nodeID string) string { return "maktaba://node/" + nodeID }

// HomeURI is the SAN of the home's client certificate.
func HomeURI(instanceID string) string { return "maktaba://home/" + instanceID }

// NodeIDOf is the machine a certificate names (its maktaba://node/<id> SAN), "" for none.
func NodeIDOf(c *x509.Certificate) string {
	for _, u := range c.URIs {
		if u.Scheme == "maktaba" && u.Host == "node" {
			id := strings.TrimPrefix(u.Path, "/")
			if _, ok := canonical(id); ok {
				return id
			}
		}
	}
	return ""
}

// Errors.
var (
	ErrBadCSR = errors.New("ca: not a valid P-256 certificate request")
	// ErrNoCA: the home has no CA (it could not be created or loaded).
	ErrNoCA = errors.New("ca: the home has no cluster CA")
)

// Issued is one certificate the CA signed, as cluster_certs records it.
type Issued struct {
	Serial    string
	NodeID    string // "" for the home's own certificates
	Kind      string
	Subject   string
	SHA256    string
	NotBefore time.Time
	NotAfter  time.Time
	IssuedAt  time.Time
	RevokedAt *time.Time
	PEM       []byte
}

// CA is the home's cluster CA.
type CA struct {
	Cert    *x509.Certificate
	CertPEM []byte
	// SHA256 is the CA certificate's fingerprint (hex of the DER's SHA-256), which the
	// Machines page shows and the join bundle pins.
	SHA256 string

	key   *ecdsa.PrivateKey
	Store Store
	Now   func() time.Time
}

func (c *CA) now() time.Time {
	if c.Now != nil {
		return c.Now().UTC()
	}
	return time.Now().UTC()
}

// Pool is a pool holding the CA certificate.
func (c *CA) Pool() *x509.CertPool {
	p := x509.NewCertPool()
	p.AddCert(c.Cert)
	return p
}

type sealedCA struct {
	Key  string `json:"key_pem"`
	Cert string `json:"cert_pem"`
}

// Dir is where the CA's files live: beside secrets.key.
func Dir(getenv func(string) string) string {
	return filepath.Dir(secrets.KeyPath(getenv))
}

// LoadOrCreate loads the CA from dir (cluster-ca.key and cluster-ca.crt), else from its
// sealed copy in home_secrets (a restored home: the files are written back), else creates
// it. The sealed copy is made when missing. sec may be nil (no secrets key: the files
// only). name names the CA certificate's subject (the home's instance).
func LoadOrCreate(ctx context.Context, dir string, sec secrets.Store, name string) (*CA, error) {
	keyPath, certPath := filepath.Join(dir, KeyFile), filepath.Join(dir, CertFile)
	keyPEM, kerr := os.ReadFile(keyPath)   //nolint:gosec // the home's own config directory
	certPEM, cerr := os.ReadFile(certPath) //nolint:gosec // the home's own config directory
	sealedOK := false
	if kerr != nil || cerr != nil {
		if sec != nil {
			if raw, err := sec.Get(ctx, SecretName); err == nil {
				var s sealedCA
				if json.Unmarshal(raw, &s) == nil && s.Key != "" && s.Cert != "" {
					keyPEM, certPEM, kerr, cerr = []byte(s.Key), []byte(s.Cert), nil, nil
					sealedOK = true
					if err := writeFiles(keyPath, certPath, keyPEM, certPEM); err != nil {
						return nil, err
					}
				}
			}
		}
	}
	if kerr != nil || cerr != nil {
		if !errors.Is(kerr, os.ErrNotExist) && kerr != nil {
			return nil, fmt.Errorf("ca: %s: %w", keyPath, kerr)
		}
		var err error
		keyPEM, certPEM, err = newRoot(name)
		if err != nil {
			return nil, err
		}
		if err := writeFiles(keyPath, certPath, keyPEM, certPEM); err != nil {
			return nil, err
		}
	}
	c, err := parse(keyPEM, certPEM)
	if err != nil {
		return nil, err
	}
	if sec != nil && !sealedOK {
		if has, err := sec.Has(ctx, SecretName); err == nil && !has {
			raw, _ := json.Marshal(sealedCA{Key: string(keyPEM), Cert: string(certPEM)})
			_ = sec.Put(ctx, SecretName, raw, "cluster-ca")
		}
	}
	return c, nil
}

// New makes a CA in memory (tests, and a home without a config directory).
func New(name string) (*CA, error) {
	keyPEM, certPEM, err := newRoot(name)
	if err != nil {
		return nil, err
	}
	return parse(keyPEM, certPEM)
}

func newRoot(name string) (keyPEM, certPEM []byte, err error) {
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return nil, nil, err
	}
	serial, err := newSerial()
	if err != nil {
		return nil, nil, err
	}
	now := time.Now().UTC()
	cn := "Maktaba cluster CA"
	if name = strings.TrimSpace(name); name != "" {
		cn += " (" + name + ")"
	}
	tmpl := &x509.Certificate{
		SerialNumber:          serial,
		Subject:               pkix.Name{CommonName: cn, Organization: []string{"Maktaba"}},
		NotBefore:             now.Add(-Skew),
		NotAfter:              now.Add(CALifetime),
		KeyUsage:              x509.KeyUsageCertSign | x509.KeyUsageCRLSign | x509.KeyUsageDigitalSignature,
		BasicConstraintsValid: true,
		IsCA:                  true,
		MaxPathLenZero:        true,
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &key.PublicKey, key)
	if err != nil {
		return nil, nil, err
	}
	kder, err := x509.MarshalECPrivateKey(key)
	if err != nil {
		return nil, nil, err
	}
	return pem.EncodeToMemory(&pem.Block{Type: "EC PRIVATE KEY", Bytes: kder}),
		pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der}), nil
}

func parse(keyPEM, certPEM []byte) (*CA, error) {
	kb, _ := pem.Decode(keyPEM)
	cb, _ := pem.Decode(certPEM)
	if kb == nil || cb == nil {
		return nil, errors.New("ca: the CA files are not PEM")
	}
	key, err := x509.ParseECPrivateKey(kb.Bytes)
	if err != nil {
		return nil, fmt.Errorf("ca: the CA key: %w", err)
	}
	cert, err := x509.ParseCertificate(cb.Bytes)
	if err != nil {
		return nil, fmt.Errorf("ca: the CA certificate: %w", err)
	}
	if !cert.IsCA {
		return nil, errors.New("ca: the CA certificate is not a CA")
	}
	pub, ok := cert.PublicKey.(*ecdsa.PublicKey)
	if !ok || !pub.Equal(&key.PublicKey) {
		return nil, errors.New("ca: the CA key does not match its certificate")
	}
	sum := sha256.Sum256(cert.Raw)
	return &CA{Cert: cert, CertPEM: pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: cert.Raw}),
		SHA256: hex.EncodeToString(sum[:]), key: key}, nil
}

func writeFiles(keyPath, certPath string, keyPEM, certPEM []byte) error {
	if err := os.MkdirAll(filepath.Dir(keyPath), 0o700); err != nil {
		return err
	}
	for _, f := range []struct {
		path string
		b    []byte
	}{{keyPath, keyPEM}, {certPath, certPEM}} {
		tmp := f.path + ".tmp"
		if err := os.WriteFile(tmp, f.b, 0o600); err != nil {
			return err
		}
		if err := os.Rename(tmp, f.path); err != nil {
			return err
		}
	}
	return nil
}

func newSerial() (*big.Int, error) {
	return rand.Int(rand.Reader, new(big.Int).Lsh(big.NewInt(1), 127))
}

// SerialHex is a certificate serial as cluster_certs keeps it.
func SerialHex(n *big.Int) string { return hex.EncodeToString(n.Bytes()) }

func canonical(id string) (string, bool) {
	if len(id) != 36 {
		return "", false
	}
	for i, ch := range id {
		switch i {
		case 8, 13, 18, 23:
			if ch != '-' {
				return "", false
			}
		default:
			if (ch < '0' || ch > '9') && (ch < 'a' || ch > 'f') {
				return "", false
			}
		}
	}
	return id, true
}

// ParseCSR reads a PEM (or DER) certificate request with a valid signature and a P-256
// key.
func ParseCSR(raw []byte) (*x509.CertificateRequest, error) {
	der := raw
	if b, _ := pem.Decode(raw); b != nil {
		der = b.Bytes
	}
	csr, err := x509.ParseCertificateRequest(der)
	if err != nil {
		return nil, ErrBadCSR
	}
	if csr.CheckSignature() != nil {
		return nil, ErrBadCSR
	}
	pub, ok := csr.PublicKey.(*ecdsa.PublicKey)
	if !ok || pub.Curve != elliptic.P256() {
		return nil, ErrBadCSR
	}
	return csr, nil
}

// SignNode signs a machine's certificate from its CSR (design §18.2): SAN
// maktaba://node/<id>, CN = its database role, not_before now − 5 min, `days` days (0:
// LeafDays), client and server use. It is recorded in the store and becomes the
// machine's current certificate there.
func (c *CA) SignNode(ctx context.Context, csrRaw []byte, nodeID string, days int) (*Issued, error) {
	id, ok := canonical(strings.ToLower(strings.TrimSpace(nodeID)))
	if !ok {
		return nil, fmt.Errorf("ca: bad node id %q", nodeID)
	}
	csr, err := ParseCSR(csrRaw)
	if err != nil {
		return nil, err
	}
	if days <= 0 {
		days = LeafDays
	}
	u, _ := url.Parse(NodeURI(id))
	tmpl := &x509.Certificate{
		Subject:     pkix.Name{CommonName: cluster.RoleName(id)},
		URIs:        []*url.URL{u},
		KeyUsage:    x509.KeyUsageDigitalSignature,
		ExtKeyUsage: []x509.ExtKeyUsage{x509.ExtKeyUsageClientAuth, x509.ExtKeyUsageServerAuth},
	}
	return c.sign(ctx, tmpl, csr.PublicKey, KindNode, id, time.Duration(days)*24*time.Hour)
}

func (c *CA) sign(ctx context.Context, tmpl *x509.Certificate, pub any, kind, nodeID string, life time.Duration) (*Issued, error) {
	if c == nil || c.key == nil {
		return nil, ErrNoCA
	}
	serial, err := newSerial()
	if err != nil {
		return nil, err
	}
	now := c.now()
	tmpl.SerialNumber = serial
	tmpl.NotBefore = now.Add(-Skew)
	tmpl.NotAfter = now.Add(life)
	if tmpl.NotAfter.After(c.Cert.NotAfter) {
		tmpl.NotAfter = c.Cert.NotAfter
	}
	tmpl.BasicConstraintsValid = true
	der, err := x509.CreateCertificate(rand.Reader, tmpl, c.Cert, pub, c.key)
	if err != nil {
		return nil, err
	}
	sum := sha256.Sum256(der)
	is := &Issued{Serial: SerialHex(serial), NodeID: nodeID, Kind: kind, Subject: tmpl.Subject.CommonName,
		SHA256: hex.EncodeToString(sum[:]), NotBefore: tmpl.NotBefore, NotAfter: tmpl.NotAfter, IssuedAt: now,
		PEM: pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der})}
	if c.Store != nil {
		if err := c.Store.Record(ctx, is); err != nil {
			return nil, err
		}
	}
	return is, nil
}

// IssueHome makes one of the home's own certificates (server, pg_server, web: the SANs
// are its addresses; client: uri) with a key made here. Their keys stay on the home.
func (c *CA) IssueHome(ctx context.Context, kind string, sans []string, uri string, days int) (tls.Certificate, *Issued, error) {
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return tls.Certificate{}, nil, err
	}
	tmpl := &x509.Certificate{Subject: pkix.Name{CommonName: "maktaba-home-" + kind}, KeyUsage: x509.KeyUsageDigitalSignature}
	switch kind {
	case KindClient:
		tmpl.ExtKeyUsage = []x509.ExtKeyUsage{x509.ExtKeyUsageClientAuth}
	default:
		tmpl.ExtKeyUsage = []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth}
	}
	for _, s := range normalSANs(sans) {
		if ip := net.ParseIP(s); ip != nil {
			tmpl.IPAddresses = append(tmpl.IPAddresses, ip)
		} else {
			tmpl.DNSNames = append(tmpl.DNSNames, s)
		}
	}
	if uri != "" {
		if u, err := url.Parse(uri); err == nil {
			tmpl.URIs = []*url.URL{u}
		}
	}
	if days <= 0 {
		days = 365
	}
	is, err := c.sign(ctx, tmpl, &key.PublicKey, kind, "", time.Duration(days)*24*time.Hour)
	if err != nil {
		return tls.Certificate{}, nil, err
	}
	kder, err := x509.MarshalECPrivateKey(key)
	if err != nil {
		return tls.Certificate{}, nil, err
	}
	pair, err := tls.X509KeyPair(is.PEM, pem.EncodeToMemory(&pem.Block{Type: "EC PRIVATE KEY", Bytes: kder}))
	return pair, is, err
}

// KeyPEM is a home certificate's key in PEM (setup --pg-tls writes the pg_server one).
func KeyPEM(pair tls.Certificate) ([]byte, error) {
	k, ok := pair.PrivateKey.(*ecdsa.PrivateKey)
	if !ok {
		return nil, errors.New("ca: not an ECDSA key")
	}
	der, err := x509.MarshalECPrivateKey(k)
	if err != nil {
		return nil, err
	}
	return pem.EncodeToMemory(&pem.Block{Type: "EC PRIVATE KEY", Bytes: der}), nil
}

// normalSANs is a SAN list sorted, de-duplicated and without empty names or ports.
func normalSANs(sans []string) []string {
	seen := map[string]bool{}
	var out []string
	for _, s := range sans {
		s = strings.TrimSpace(s)
		if h, _, err := net.SplitHostPort(s); err == nil {
			s = h
		}
		s = strings.Trim(s, "[]")
		s = strings.ToLower(strings.TrimSuffix(s, "."))
		if s == "" || seen[s] {
			continue
		}
		seen[s] = true
		out = append(out, s)
	}
	sort.Strings(out)
	return out
}

// SameSANs reports whether a certificate names exactly these addresses (a home whose
// address set changed gets its server certificate again, EC1).
func SameSANs(c *x509.Certificate, sans []string) bool {
	var have []string
	for _, ip := range c.IPAddresses {
		have = append(have, ip.String())
	}
	have = append(have, c.DNSNames...)
	a, b := normalSANs(have), normalSANs(sans)
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

// Verify checks a machine's certificate chain against the CA (any time check is the
// caller's: tls does it at the handshake) and returns the machine it names.
func (c *CA) Verify(cert *x509.Certificate, at time.Time) (string, error) {
	if _, err := cert.Verify(x509.VerifyOptions{Roots: c.Pool(), CurrentTime: at,
		KeyUsages: []x509.ExtKeyUsage{x509.ExtKeyUsageClientAuth}}); err != nil {
		return "", err
	}
	id := NodeIDOf(cert)
	if id == "" {
		return "", errors.New("ca: the certificate names no machine")
	}
	return id, nil
}
