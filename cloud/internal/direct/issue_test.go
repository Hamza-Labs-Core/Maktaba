package direct

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"crypto/x509/pkix"
	"strings"
	"testing"
	"time"
)

func csr(t *testing.T, cn string, names ...string) []byte {
	t.Helper()
	key, _ := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	der, err := x509.CreateCertificateRequest(rand.Reader, &x509.CertificateRequest{Subject: pkix.Name{CommonName: cn}, DNSNames: names}, key)
	if err != nil {
		t.Fatal(err)
	}
	return der
}

// A home's CSR names its own wildcard and nothing else: the certificate proves only that home.
func TestOnlyTheHomesOwnWildcard(t *testing.T) {
	id, other := HomeID("s1"), HomeID("s2")
	if _, err := CheckCSR(csr(t, "", "*."+id+".direct-a.example"), id, "direct-a.example"); err != nil {
		t.Fatalf("its own: %v", err)
	}
	for name, der := range map[string][]byte{
		"another home's":   csr(t, "", "*."+other+".direct-a.example"),
		"another domain's": csr(t, "", "*."+id+".direct-b.example"),
		"two names":        csr(t, "", "*."+id+".direct-a.example", "relay.maktaba.tv"),
		"a bare name":      csr(t, "", "81-2-3-4."+id+".direct-a.example"),
		"a common name":    csr(t, "api.maktaba.tv", "*."+id+".direct-a.example"),
		"not a CSR":        []byte("hello"),
	} {
		if _, err := CheckCSR(der, id, "direct-a.example"); err == nil {
			t.Errorf("%s accepted", name)
		}
	}
	if _, err := CheckCSR(csr(t, "", "*."+id+".direct-a.example"), id, "direct-a.example"); err != nil || !strings.HasPrefix(Wildcard(id, "x"), "*.") {
		t.Fatal(err)
	}
}

func TestRenewalAtTwoThirds(t *testing.T) {
	nb := time.Date(2026, 11, 1, 0, 0, 0, 0, time.UTC)
	if got := RenewAt(nb, nb.Add(90*24*time.Hour)); !got.Equal(nb.Add(60 * 24 * time.Hour)) {
		t.Fatalf("%s", got)
	}
}
