package directaccess

import (
	"crypto"
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
	"os"
	"path/filepath"
	"sync"
	"time"
)

// HomeID is the home's stable direct-name label: the first 32 hex characters of
// SHA-256("direct-v1" ‖ server_id) (the cloud's direct.HomeID).
func HomeID(serverID string) string {
	sum := sha256.Sum256([]byte("direct-v1" + serverID))
	return hex.EncodeToString(sum[:])[:32]
}

// Name is the home's certificate name as the cloud assigned it.
type Name struct {
	Domain string `json:"domain"`
	HomeID string `json:"home_id"`
}

// Wildcard is *.<home id>.<domain>, the only name the certificate carries.
func (n Name) Wildcard() string { return "*." + n.HomeID + "." + n.Domain }

// Files keeps the home's key, its name and its chain in a directory of its own (0700): key.pem
// (0600, never sent anywhere), name.json and chain.pem. The last good chain is kept when a
// renewal fails.
type Files struct {
	Dir string

	mu   sync.Mutex
	key  crypto.Signer
	cert *tls.Certificate
}

func (f *Files) path(name string) string { return filepath.Join(f.Dir, name) }

// Key loads the home's key, making it on first use.
func (f *Files) Key() (crypto.Signer, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.keyLocked()
}

func (f *Files) keyLocked() (crypto.Signer, error) {
	if f.key != nil {
		return f.key, nil
	}
	p := f.path("key.pem")
	if b, err := os.ReadFile(p); err == nil {
		blk, _ := pem.Decode(b)
		if blk == nil {
			return nil, fmt.Errorf("directaccess: %s is not PEM", p)
		}
		k, err := x509.ParsePKCS8PrivateKey(blk.Bytes)
		if err != nil {
			return nil, err
		}
		s, ok := k.(crypto.Signer)
		if !ok {
			return nil, errors.New("directaccess: the key can't sign")
		}
		f.key = s
		return s, nil
	} else if !errors.Is(err, os.ErrNotExist) {
		return nil, err
	}
	k, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return nil, err
	}
	der, err := x509.MarshalPKCS8PrivateKey(k)
	if err != nil {
		return nil, err
	}
	if err := os.MkdirAll(f.Dir, 0o700); err != nil {
		return nil, err
	}
	if err := writeAtomic(p, pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: der}), 0o600); err != nil {
		return nil, err
	}
	f.key = k
	return k, nil
}

// CSR is a request for n's wildcard, signed by the home's key (DER).
func (f *Files) CSR(n Name) ([]byte, error) {
	k, err := f.Key()
	if err != nil {
		return nil, err
	}
	return x509.CreateCertificateRequest(rand.Reader, &x509.CertificateRequest{
		Subject:  pkix.Name{CommonName: n.Wildcard()},
		DNSNames: []string{n.Wildcard()},
	}, k)
}

// Name is the name the cloud assigned (zero: none yet).
func (f *Files) Name() Name {
	var n Name
	if b, err := os.ReadFile(f.path("name.json")); err == nil {
		_ = json.Unmarshal(b, &n)
	}
	return n
}

// SetName keeps the assigned name.
func (f *Files) SetName(n Name) error {
	if err := os.MkdirAll(f.Dir, 0o700); err != nil {
		return err
	}
	b, _ := json.Marshal(n)
	return writeAtomic(f.path("name.json"), b, 0o600)
}

// Cert is the certificate the listener presents (nil: none yet).
func (f *Files) Cert() *tls.Certificate {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.cert != nil {
		return f.cert
	}
	b, err := os.ReadFile(f.path("chain.pem"))
	if err != nil {
		return nil
	}
	c, err := f.pairLocked(b)
	if err != nil {
		return nil
	}
	f.cert = c
	return c
}

func (f *Files) pairLocked(chainPEM []byte) (*tls.Certificate, error) {
	k, err := f.keyLocked()
	if err != nil {
		return nil, err
	}
	var c tls.Certificate
	for rest := chainPEM; ; {
		var blk *pem.Block
		blk, rest = pem.Decode(rest)
		if blk == nil {
			break
		}
		if blk.Type == "CERTIFICATE" {
			c.Certificate = append(c.Certificate, blk.Bytes)
		}
	}
	if len(c.Certificate) == 0 {
		return nil, errors.New("directaccess: no certificate in the chain")
	}
	leaf, err := x509.ParseCertificate(c.Certificate[0])
	if err != nil {
		return nil, err
	}
	pub, ok := k.Public().(interface{ Equal(crypto.PublicKey) bool })
	if !ok || !pub.Equal(leaf.PublicKey) {
		return nil, errors.New("directaccess: the chain is not for this home's key")
	}
	c.PrivateKey, c.Leaf = k, leaf
	return &c, nil
}

// SetChain keeps a chain the cloud delivered when it is for this home's key and n's name;
// anything else is refused and the last good one stays.
func (f *Files) SetChain(chainPEM []byte, n Name) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	c, err := f.pairLocked(chainPEM)
	if err != nil {
		return err
	}
	if err := c.Leaf.VerifyHostname("x." + n.HomeID + "." + n.Domain); err != nil {
		return fmt.Errorf("directaccess: the chain is not for %s: %w", n.Wildcard(), err)
	}
	if err := writeAtomic(f.path("chain.pem"), chainPEM, 0o600); err != nil {
		return err
	}
	f.cert = c
	return nil
}

// RenewDue says whether c should be renewed now: at two thirds of its life, or once it no
// longer names n.
func RenewDue(c *tls.Certificate, n Name, now time.Time) bool {
	if c == nil || c.Leaf == nil {
		return true
	}
	if c.Leaf.VerifyHostname("x."+n.HomeID+"."+n.Domain) != nil {
		return true
	}
	life := c.Leaf.NotAfter.Sub(c.Leaf.NotBefore)
	return !now.Before(c.Leaf.NotBefore.Add(life * 2 / 3))
}

func writeAtomic(path string, b []byte, mode os.FileMode) error {
	tmp, err := os.CreateTemp(filepath.Dir(path), ".tmp-*")
	if err != nil {
		return err
	}
	name := tmp.Name()
	if _, err := tmp.Write(b); err != nil {
		_ = tmp.Close()
		_ = os.Remove(name)
		return err
	}
	if err := tmp.Chmod(mode); err != nil {
		_ = tmp.Close()
		_ = os.Remove(name)
		return err
	}
	if err := tmp.Close(); err != nil {
		_ = os.Remove(name)
		return err
	}
	return os.Rename(name, path)
}

// firstDER is the first certificate of a PEM chain (nil: none).
func firstDER(chainPEM string) []byte {
	for rest := []byte(chainPEM); ; {
		var blk *pem.Block
		blk, rest = pem.Decode(rest)
		if blk == nil {
			return nil
		}
		if blk.Type == "CERTIFICATE" {
			return blk.Bytes
		}
	}
}
