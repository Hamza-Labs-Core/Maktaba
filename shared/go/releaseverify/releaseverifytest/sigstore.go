// Package releaseverifytest is a local, fake Sigstore for tests: a Fulcio-like
// CA that issues GitHub-Actions-shaped certificates, a Rekor-like log that
// returns a signed entry timestamp and an inclusion proof with a signed
// checkpoint, and the trusted_root.json that trusts both. Bundles it makes are
// real protobuf Sigstore bundles (v0.3), verified by releaseverify through the
// same sigstore-go path as production; only the trust root differs.
//
// It also builds a complete signed release (FakeRelease) for the tests of the
// API's self-update and of `maktaba-server update`. Never import it outside
// tests.
package releaseverifytest

import (
	"bytes"
	"context"
	"crypto"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/sha256"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/asn1"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"math/big"
	"net/url"
	"time"

	"github.com/cyberphone/json-canonicalization/go/src/webpki.org/jsoncanonicalizer"
	"github.com/go-openapi/runtime"
	protobundle "github.com/sigstore/protobuf-specs/gen/pb-go/bundle/v1"
	protocommon "github.com/sigstore/protobuf-specs/gen/pb-go/common/v1"
	protorekor "github.com/sigstore/protobuf-specs/gen/pb-go/rekor/v1"
	"github.com/sigstore/rekor/pkg/generated/models"
	"github.com/sigstore/rekor/pkg/pki"
	"github.com/sigstore/rekor/pkg/types"
	_ "github.com/sigstore/rekor/pkg/types/hashedrekord/v0.0.1" // registers hashedrekord 0.0.1
	"github.com/sigstore/rekor/pkg/util"
	"github.com/sigstore/sigstore-go/pkg/root"
	"github.com/sigstore/sigstore-go/pkg/tlog"
	"github.com/sigstore/sigstore/pkg/cryptoutils"
	"github.com/sigstore/sigstore/pkg/signature"
	"google.golang.org/protobuf/encoding/protojson"
)

// GitHub Actions' OIDC issuer, as Fulcio records it.
const githubIssuer = "https://token.actions.githubusercontent.com"

var (
	oidIssuerV1            = asn1.ObjectIdentifier{1, 3, 6, 1, 4, 1, 57264, 1, 1}
	oidIssuerV2            = asn1.ObjectIdentifier{1, 3, 6, 1, 4, 1, 57264, 1, 8}
	oidSourceRepositoryURI = asn1.ObjectIdentifier{1, 3, 6, 1, 4, 1, 57264, 1, 12}
	oidSourceRepositoryRef = asn1.ObjectIdentifier{1, 3, 6, 1, 4, 1, 57264, 1, 14}
	oidBuildTrigger        = asn1.ObjectIdentifier{1, 3, 6, 1, 4, 1, 57264, 1, 20}
)

// Sigstore is the fake instance.
type Sigstore struct {
	rootCert  *x509.Certificate
	interCert *x509.Certificate
	interKey  *ecdsa.PrivateKey
	rekorKey  *ecdsa.PrivateKey
	start     time.Time
	end       time.Time
}

// New makes a fake Sigstore whose keys are valid from a year ago to a year
// ahead, so signatures can be dated in the past (replay tests).
func New() (*Sigstore, error) {
	s := &Sigstore{start: time.Now().Add(-365 * 24 * time.Hour).Truncate(time.Second), end: time.Now().Add(365 * 24 * time.Hour).Truncate(time.Second)}
	rootKey, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return nil, err
	}
	rootTmpl := &x509.Certificate{
		SerialNumber: big.NewInt(1), Subject: pkix.Name{CommonName: "fake-sigstore", Organization: []string{"maktaba-test"}},
		NotBefore: s.start, NotAfter: s.end, IsCA: true, BasicConstraintsValid: true,
		KeyUsage: x509.KeyUsageCertSign | x509.KeyUsageCRLSign,
	}
	if s.rootCert, err = createCert(rootTmpl, rootTmpl, &rootKey.PublicKey, rootKey); err != nil {
		return nil, err
	}
	if s.interKey, err = ecdsa.GenerateKey(elliptic.P256(), rand.Reader); err != nil {
		return nil, err
	}
	interTmpl := &x509.Certificate{
		SerialNumber: big.NewInt(2), Subject: pkix.Name{CommonName: "fake-sigstore-intermediate", Organization: []string{"maktaba-test"}},
		NotBefore: s.start, NotAfter: s.end, IsCA: true, BasicConstraintsValid: true,
		KeyUsage:    x509.KeyUsageCertSign | x509.KeyUsageCRLSign,
		ExtKeyUsage: []x509.ExtKeyUsage{x509.ExtKeyUsageCodeSigning},
	}
	if s.interCert, err = createCert(interTmpl, s.rootCert, &s.interKey.PublicKey, rootKey); err != nil {
		return nil, err
	}
	if s.rekorKey, err = ecdsa.GenerateKey(elliptic.P256(), rand.Reader); err != nil {
		return nil, err
	}
	return s, nil
}

func createCert(tmpl, parent *x509.Certificate, pub any, priv crypto.Signer) (*x509.Certificate, error) {
	der, err := x509.CreateCertificate(rand.Reader, tmpl, parent, pub, priv)
	if err != nil {
		return nil, err
	}
	return x509.ParseCertificate(der)
}

func (s *Sigstore) rekorLogID() ([]byte, error) {
	der, err := x509.MarshalPKIXPublicKey(s.rekorKey.Public())
	if err != nil {
		return nil, err
	}
	sum := sha256.Sum256(der)
	return sum[:], nil
}

// TrustedRoot is the trust root that accepts this instance.
func (s *Sigstore) TrustedRoot() (*root.TrustedRoot, error) {
	j, err := s.TrustedRootJSON()
	if err != nil {
		return nil, err
	}
	return root.NewTrustedRootFromJSON(j)
}

// TrustedRootJSON is trusted_root.json for this instance.
func (s *Sigstore) TrustedRootJSON() ([]byte, error) {
	id, err := s.rekorLogID()
	if err != nil {
		return nil, err
	}
	hexID := hex.EncodeToString(id)
	tr, err := root.NewTrustedRoot(root.TrustedRootMediaType01,
		[]root.CertificateAuthority{&root.FulcioCertificateAuthority{
			Root: s.rootCert, Intermediates: []*x509.Certificate{s.interCert},
			ValidityPeriodStart: s.start, ValidityPeriodEnd: s.end, URI: "https://fulcio.localhost",
		}},
		map[string]*root.TransparencyLog{},
		nil,
		map[string]*root.TransparencyLog{hexID: {
			BaseURL: "https://rekor.localhost", ID: id, HashFunc: crypto.SHA256,
			PublicKey: s.rekorKey.Public(), SignatureHashFunc: crypto.SHA256,
			ValidityPeriodStart: s.start,
		}},
	)
	if err != nil {
		return nil, err
	}
	return tr.MarshalJSON()
}

// SignOptions shapes the certificate and the log entry.
type SignOptions struct {
	SAN           string // certificate URI SAN
	Issuer        string
	RepositoryURI string
	Ref           string
	Trigger       string
	// SignedAt is the Rekor integrated time (default now).
	SignedAt time.Time
	// NoTlog drops the transparency log entry; NoInclusionProof keeps the
	// entry with its signed timestamp but without the proof.
	NoTlog           bool
	NoInclusionProof bool
}

// GitHubRelease is the identity release.yml in repo gets for tag.
func GitHubRelease(repo, tag string) SignOptions {
	return SignOptions{
		SAN:           "https://github.com/" + repo + "/.github/workflows/release.yml@refs/tags/" + tag,
		Issuer:        githubIssuer,
		RepositoryURI: "https://github.com/" + repo,
		Ref:           "refs/tags/" + tag,
		Trigger:       "push",
	}
}

func derString(v string) []byte {
	b, err := asn1.MarshalWithParams(v, "utf8")
	if err != nil {
		panic(err)
	}
	return b
}

// SignBlob signs artifact the way `cosign sign-blob --new-bundle-format` does
// and returns the bundle JSON.
func (s *Sigstore) SignBlob(artifact []byte, o SignOptions) ([]byte, error) {
	at := o.SignedAt
	if at.IsZero() {
		at = time.Now()
	}
	at = at.Truncate(time.Second)
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return nil, err
	}
	san, err := url.Parse(o.SAN)
	if err != nil {
		return nil, err
	}
	ext := []pkix.Extension{
		{Id: oidIssuerV1, Value: []byte(o.Issuer)},
		{Id: oidIssuerV2, Value: derString(o.Issuer)},
	}
	if o.RepositoryURI != "" {
		ext = append(ext, pkix.Extension{Id: oidSourceRepositoryURI, Value: derString(o.RepositoryURI)})
	}
	if o.Ref != "" {
		ext = append(ext, pkix.Extension{Id: oidSourceRepositoryRef, Value: derString(o.Ref)})
	}
	if o.Trigger != "" {
		ext = append(ext, pkix.Extension{Id: oidBuildTrigger, Value: derString(o.Trigger)})
	}
	leafTmpl := &x509.Certificate{
		SerialNumber: big.NewInt(time.Now().UnixNano()),
		URIs:         []*url.URL{san},
		NotBefore:    at.Add(-time.Minute), NotAfter: at.Add(9 * time.Minute),
		KeyUsage:        x509.KeyUsageDigitalSignature,
		ExtKeyUsage:     []x509.ExtKeyUsage{x509.ExtKeyUsageCodeSigning},
		ExtraExtensions: ext,
	}
	leaf, err := createCert(leafTmpl, s.interCert, &key.PublicKey, s.interKey)
	if err != nil {
		return nil, err
	}
	digest := sha256.Sum256(artifact)
	sig, err := ecdsa.SignASN1(rand.Reader, key, digest[:])
	if err != nil {
		return nil, err
	}

	pb := &protobundle.Bundle{
		MediaType: "application/vnd.dev.sigstore.bundle.v0.3+json",
		VerificationMaterial: &protobundle.VerificationMaterial{
			Content: &protobundle.VerificationMaterial_Certificate{Certificate: &protocommon.X509Certificate{RawBytes: leaf.Raw}},
		},
		Content: &protobundle.Bundle_MessageSignature{MessageSignature: &protocommon.MessageSignature{
			MessageDigest: &protocommon.HashOutput{Algorithm: protocommon.HashAlgorithm_SHA2_256, Digest: digest[:]},
			Signature:     sig,
		}},
	}
	if !o.NoTlog {
		tle, err := s.logEntry(leaf, digest[:], sig, at, !o.NoInclusionProof)
		if err != nil {
			return nil, err
		}
		pb.VerificationMaterial.TlogEntries = []*protorekor.TransparencyLogEntry{tle}
	}
	return protojson.Marshal(pb)
}

// logEntry is a one-leaf Rekor v1 log holding the hashedrekord entry.
func (s *Sigstore) logEntry(leaf *x509.Certificate, digest, sig []byte, at time.Time, withProof bool) (*protorekor.TransparencyLogEntry, error) {
	ctx := context.Background()
	certPEM, err := cryptoutils.MarshalCertificateToPEM(leaf)
	if err != nil {
		return nil, err
	}
	pe, err := types.NewProposedEntry(ctx, "hashedrekord", "0.0.1", types.ArtifactProperties{
		PublicKeyBytes: [][]byte{certPEM}, PKIFormat: string(pki.X509),
		ArtifactHash: hex.EncodeToString(digest), SignatureBytes: sig,
	})
	if err != nil {
		return nil, err
	}
	impl, err := types.CreateVersionedEntry(pe)
	if err != nil {
		return nil, err
	}
	canon, err := types.CanonicalizeEntry(ctx, impl)
	if err != nil {
		return nil, err
	}
	if _, err := models.UnmarshalProposedEntry(bytes.NewReader(canon), runtime.JSONConsumer()); err != nil {
		return nil, err
	}
	logID, err := s.rekorLogID()
	if err != nil {
		return nil, err
	}
	const logIndex = 0
	payload, err := json.Marshal(tlog.RekorPayload{
		Body: base64.StdEncoding.EncodeToString(canon), IntegratedTime: at.Unix(),
		LogIndex: logIndex, LogID: hex.EncodeToString(logID),
	})
	if err != nil {
		return nil, err
	}
	canonPayload, err := jsoncanonicalizer.Transform(payload)
	if err != nil {
		return nil, err
	}
	signer, err := signature.LoadECDSASignerVerifier(s.rekorKey, crypto.SHA256)
	if err != nil {
		return nil, err
	}
	set, err := signer.SignMessage(bytes.NewReader(canonPayload))
	if err != nil {
		return nil, err
	}
	tle := &protorekor.TransparencyLogEntry{
		LogIndex:          logIndex,
		LogId:             &protocommon.LogId{KeyId: logID},
		KindVersion:       &protorekor.KindVersion{Kind: "hashedrekord", Version: "0.0.1"},
		IntegratedTime:    at.Unix(),
		InclusionPromise:  &protorekor.InclusionPromise{SignedEntryTimestamp: set},
		CanonicalizedBody: canon,
	}
	if withProof {
		leafHash := sha256.Sum256(append([]byte{0}, canon...))
		cp, err := util.CreateAndSignCheckpoint(ctx, "rekor.localhost", 1, 1, leafHash[:], signer)
		if err != nil {
			return nil, err
		}
		tle.InclusionProof = &protorekor.InclusionProof{
			LogIndex: logIndex, RootHash: leafHash[:], TreeSize: 1,
			Checkpoint: &protorekor.Checkpoint{Envelope: string(cp)},
		}
	}
	return tle, nil
}

// MustNew is New for tests.
func MustNew() *Sigstore {
	s, err := New()
	if err != nil {
		panic(fmt.Sprintf("releaseverifytest: %v", err))
	}
	return s
}
