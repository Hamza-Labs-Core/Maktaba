package ca

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"os"
	"path/filepath"
	"runtime"
	"testing"
	"time"

	"github.com/Hamza-Labs-Core/Maktaba/api/internal/secrets"
)

const nodeID = "0b6f3c1e-2a4d-4e8f-9c71-5d2a8b3e6f10"

func csrFor(t *testing.T, curve elliptic.Curve) []byte {
	t.Helper()
	key, err := ecdsa.GenerateKey(curve, rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	der, err := x509.CreateCertificateRequest(rand.Reader, &x509.CertificateRequest{Subject: pkix.Name{CommonName: "x"}}, key)
	if err != nil {
		t.Fatal(err)
	}
	return pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE REQUEST", Bytes: der})
}

func leaf(t *testing.T, is *Issued) *x509.Certificate {
	t.Helper()
	b, _ := pem.Decode(is.PEM)
	c, err := x509.ParseCertificate(b.Bytes)
	if err != nil {
		t.Fatal(err)
	}
	return c
}

// A machine's certificate names it by its SAN and its database role by its CN, lives 30
// days from five minutes back, and serves both ends of a TLS connection.
func TestLeafSANAndCN(t *testing.T) {
	c, err := New("home")
	if err != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, 10, 25, 12, 0, 0, 0, time.UTC)
	c.Now = func() time.Time { return now }
	is, err := c.SignNode(context.Background(), csrFor(t, elliptic.P256()), nodeID, 0)
	if err != nil {
		t.Fatal(err)
	}
	l := leaf(t, is)
	if NodeIDOf(l) != nodeID || l.Subject.CommonName != "maktaba_node_0b6f3c1e2a4d4e8f9c715d2a8b3e6f10" {
		t.Fatalf("SAN %v, CN %q", l.URIs, l.Subject.CommonName)
	}
	if !l.NotBefore.Equal(now.Add(-Skew)) || !l.NotAfter.Equal(now.Add(30*24*time.Hour)) {
		t.Fatalf("validity %v to %v", l.NotBefore, l.NotAfter)
	}
	if len(l.ExtKeyUsage) != 2 || l.IsCA {
		t.Fatalf("usage %v, CA %v", l.ExtKeyUsage, l.IsCA)
	}
	if id, err := c.Verify(l, now); err != nil || id != nodeID {
		t.Fatalf("verify: %q %v", id, err)
	}
	// another curve, or a request whose signature fails, is refused
	if _, err := c.SignNode(context.Background(), csrFor(t, elliptic.P384()), nodeID, 0); err == nil {
		t.Fatal("a P-384 request was signed")
	}
	bad := csrFor(t, elliptic.P256())
	bad[len(bad)/2] ^= 0x01
	if _, err := c.SignNode(context.Background(), bad, nodeID, 0); err == nil {
		t.Fatal("a damaged request was signed")
	}
	if _, err := c.SignNode(context.Background(), csrFor(t, elliptic.P256()), "../etc", 0); err == nil {
		t.Fatal("a bad node id was signed")
	}
}

// Issue, renew (the new one becomes current) and revoke (every one of the machine's).
func TestCAIssueRenewRevoke(t *testing.T) {
	st := &MemStore{}
	c, err := New("home")
	if err != nil {
		t.Fatal(err)
	}
	c.Store = st
	ctx := context.Background()
	first, err := c.SignNode(ctx, csrFor(t, elliptic.P256()), nodeID, 0)
	if err != nil {
		t.Fatal(err)
	}
	second, err := c.SignNode(ctx, csrFor(t, elliptic.P256()), nodeID, 0)
	if err != nil {
		t.Fatal(err)
	}
	if first.Serial == second.Serial {
		t.Fatal("two certificates share a serial")
	}
	if cur, _ := st.Current(ctx, nodeID); cur != second.Serial {
		t.Fatalf("current %s, want the renewed %s", cur, second.Serial)
	}
	if r, _ := st.Revoked(ctx, first.Serial); r {
		t.Fatal("revoked before the machine was")
	}
	if err := st.RevokeNode(ctx, nodeID, time.Now()); err != nil {
		t.Fatal(err)
	}
	for _, s := range []string{first.Serial, second.Serial, "deadbeef"} {
		if r, _ := st.Revoked(ctx, s); !r {
			t.Fatalf("%s not revoked", s)
		}
	}
}

// The CA is made once, kept in its files and its sealed copy, and comes back from the
// copy on a restored home whose files are gone (EC6): the same CA, so every machine's
// certificate stays valid.
func TestCALoadOrCreateRestoresFromSealedCopy(t *testing.T) {
	dir := t.TempDir()
	sec := secrets.NewMemory()
	ctx := context.Background()
	a, err := LoadOrCreate(ctx, dir, sec, "home")
	if err != nil {
		t.Fatal(err)
	}
	st, err := os.Stat(filepath.Join(dir, KeyFile))
	if err != nil || runtime.GOOS != "windows" && st.Mode().Perm()&0o077 != 0 {
		t.Fatalf("key file: %v", err)
	}
	b, err := LoadOrCreate(ctx, dir, sec, "home")
	if err != nil || b.SHA256 != a.SHA256 {
		t.Fatalf("reload: %v, %s vs %s", err, b.SHA256, a.SHA256)
	}
	if has, _ := sec.Has(ctx, SecretName); !has {
		t.Fatal("no sealed copy")
	}
	if err := os.RemoveAll(dir); err != nil {
		t.Fatal(err)
	}
	c, err := LoadOrCreate(ctx, dir, sec, "home")
	if err != nil || c.SHA256 != a.SHA256 {
		t.Fatalf("restore: %v, %s vs %s", err, c.SHA256, a.SHA256)
	}
	if _, err := os.Stat(filepath.Join(dir, KeyFile)); err != nil {
		t.Fatalf("the restored key file: %v", err)
	}
}

// The home's own server certificate names its addresses; SameSANs tells when they moved.
func TestHomeServerCertificateSANs(t *testing.T) {
	c, err := New("home")
	if err != nil {
		t.Fatal(err)
	}
	pair, is, err := c.IssueHome(context.Background(), KindServer, []string{"192.168.1.10", "home.local", "HOME.local.", "[::1]:8080"}, "", 0)
	if err != nil || is.Kind != KindServer {
		t.Fatal(err)
	}
	l, _ := x509.ParseCertificate(pair.Certificate[0])
	if !SameSANs(l, []string{"home.local", "::1", "192.168.1.10"}) {
		t.Fatalf("SANs %v %v", l.IPAddresses, l.DNSNames)
	}
	if SameSANs(l, []string{"home.local", "192.168.1.11"}) {
		t.Fatal("a moved address matched")
	}
	if _, err := l.Verify(x509.VerifyOptions{Roots: c.Pool(), DNSName: "home.local"}); err != nil {
		t.Fatal(err)
	}
}
