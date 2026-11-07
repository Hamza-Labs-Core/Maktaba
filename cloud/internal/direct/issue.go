package direct

import (
	"context"
	"crypto"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"encoding/base64"
	"encoding/pem"
	"errors"
	"fmt"
	"net/http"
	"os"
	"time"

	"golang.org/x/crypto/acme"
)

// ChallengeStore keeps the DNS-01 TXT values the responder serves (acme_challenges).
type ChallengeStore interface {
	PutChallenge(ctx context.Context, name, value string, ttl time.Duration) error
	DeleteChallenge(ctx context.Context, name, value string) error
}

// CA is one ACME CA's settings.
type CA struct {
	Directory string        // https://acme-v02.api.letsencrypt.org/directory, ZeroSSL's, Google's
	Key       crypto.Signer // the account's key (direct.acme.<ca>.account_key)
	// EAB is the external account binding ZeroSSL and Google Trust Services need.
	EABKeyID string
	EABHMAC  string // base64url
	Contact  string // mailto:
}

// Issuer orders a home's certificate from its CSR by DNS-01, answered by the responder (story
// 50.15 §3). The home made the key and the CSR; the key never reaches the cloud.
type Issuer struct {
	CAs        map[string]CA
	Challenges ChallengeStore
	HTTPClient *http.Client // the rehearsal's trusts Pebble's CA
}

// CheckCSR parses a home's CSR and refuses any but one for *.<home id>.<domain> exactly.
func CheckCSR(der []byte, homeID, domain string) (*x509.CertificateRequest, error) {
	csr, err := x509.ParseCertificateRequest(der)
	if err != nil {
		return nil, fmt.Errorf("not a CSR: %w", err)
	}
	if err := csr.CheckSignature(); err != nil {
		return nil, fmt.Errorf("the CSR's signature: %w", err)
	}
	want := Wildcard(homeID, domain)
	if len(csr.DNSNames) != 1 || csr.DNSNames[0] != want || len(csr.IPAddresses) > 0 || len(csr.EmailAddresses) > 0 || len(csr.URIs) > 0 {
		return nil, fmt.Errorf("the CSR must name %s and nothing else (it names %v)", want, csr.DNSNames)
	}
	if csr.Subject.CommonName != "" && csr.Subject.CommonName != want {
		return nil, fmt.Errorf("the CSR's common name %q is not %s", csr.Subject.CommonName, want)
	}
	return csr, nil
}

// Issue orders the certificate from ca and returns its chain as PEM and its end.
func (is *Issuer) Issue(ctx context.Context, csrDER []byte, homeID, domain, ca string) ([]byte, time.Time, error) {
	if _, err := CheckCSR(csrDER, homeID, domain); err != nil {
		return nil, time.Time{}, err
	}
	conf, ok := is.CAs[ca]
	if !ok || conf.Key == nil || conf.Directory == "" {
		return nil, time.Time{}, fmt.Errorf("CA %s is not configured", ca)
	}
	client := &acme.Client{Key: conf.Key, DirectoryURL: conf.Directory, HTTPClient: is.HTTPClient}
	acct := &acme.Account{}
	if conf.Contact != "" {
		acct.Contact = []string{conf.Contact}
	}
	if conf.EABKeyID != "" {
		hmac, err := base64.RawURLEncoding.DecodeString(conf.EABHMAC)
		if err != nil {
			return nil, time.Time{}, fmt.Errorf("%s's EAB key: %w", ca, err)
		}
		acct.ExternalAccountBinding = &acme.ExternalAccountBinding{KID: conf.EABKeyID, Key: hmac}
	}
	if _, err := client.Register(ctx, acct, acme.AcceptTOS); err != nil && !errors.Is(err, acme.ErrAccountAlreadyExists) {
		return nil, time.Time{}, fmt.Errorf("%s account: %w", ca, err)
	}
	order, err := client.AuthorizeOrder(ctx, acme.DomainIDs(Wildcard(homeID, domain)))
	if err != nil {
		return nil, time.Time{}, fmt.Errorf("%s order: %w", ca, err)
	}
	txtName := "_acme-challenge." + homeID + "." + domain
	var put []string
	defer func() {
		for _, v := range put {
			_ = is.Challenges.DeleteChallenge(context.WithoutCancel(ctx), txtName, v)
		}
	}()
	for _, u := range order.AuthzURLs {
		authz, err := client.GetAuthorization(ctx, u)
		if err != nil {
			return nil, time.Time{}, err
		}
		if authz.Status == acme.StatusValid {
			continue
		}
		var chal *acme.Challenge
		for _, c := range authz.Challenges {
			if c.Type == "dns-01" {
				chal = c
			}
		}
		if chal == nil {
			return nil, time.Time{}, fmt.Errorf("%s offers no dns-01 for %s", ca, authz.Identifier.Value)
		}
		val, err := client.DNS01ChallengeRecord(chal.Token)
		if err != nil {
			return nil, time.Time{}, err
		}
		if err := is.Challenges.PutChallenge(ctx, txtName, val, 10*time.Minute); err != nil {
			return nil, time.Time{}, fmt.Errorf("the challenge's TXT: %w", err)
		}
		put = append(put, val)
		if _, err := client.Accept(ctx, chal); err != nil {
			return nil, time.Time{}, fmt.Errorf("%s accept: %w", ca, err)
		}
		if _, err := client.WaitAuthorization(ctx, authz.URI); err != nil {
			return nil, time.Time{}, fmt.Errorf("%s validation: %w", ca, err)
		}
	}
	if _, err := client.WaitOrder(ctx, order.URI); err != nil {
		return nil, time.Time{}, fmt.Errorf("%s order: %w", ca, err)
	}
	ders, _, err := client.CreateOrderCert(ctx, order.FinalizeURL, csrDER, true)
	if err != nil {
		return nil, time.Time{}, fmt.Errorf("%s certificate: %w", ca, err)
	}
	if len(ders) == 0 {
		return nil, time.Time{}, errors.New("an empty chain")
	}
	leaf, err := x509.ParseCertificate(ders[0])
	if err != nil {
		return nil, time.Time{}, err
	}
	var out []byte
	for _, d := range ders {
		out = append(out, pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: d})...)
	}
	return out, leaf.NotAfter, nil
}

// RenewAt is when a certificate is renewed: at two thirds of its life.
func RenewAt(notBefore, notAfter time.Time) time.Time {
	return notBefore.Add(notAfter.Sub(notBefore) * 2 / 3)
}

// LoadOrCreateKey reads an ACME account's key (PEM, EC P-256), making it on first use (0600).
func LoadOrCreateKey(path string) (crypto.Signer, error) {
	b, err := os.ReadFile(path)
	if err == nil {
		blk, _ := pem.Decode(b)
		if blk == nil {
			return nil, fmt.Errorf("%s: not PEM", path)
		}
		k, err := x509.ParseECPrivateKey(blk.Bytes)
		if err != nil {
			return nil, fmt.Errorf("%s: %w", path, err)
		}
		return k, nil
	}
	if !errors.Is(err, os.ErrNotExist) {
		return nil, err
	}
	k, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return nil, err
	}
	der, err := x509.MarshalECPrivateKey(k)
	if err != nil {
		return nil, err
	}
	if err := os.WriteFile(path, pem.EncodeToMemory(&pem.Block{Type: "EC PRIVATE KEY", Bytes: der}), 0o600); err != nil {
		return nil, err
	}
	return k, nil
}
