// Package releaseverify is the one place a Maktaba machine decides whether a
// release may be installed (Epic 35, story 35.1; design §3.2). The API's
// self-update, `maktaba-server update`, the supervisor's orchestrator, the
// node's machine agent and the launcher's offline re-check all call Verify;
// none of them compare checksums on their own.
//
// Trust chain, all in process (no cosign binary, no network):
//
//  1. checksums.txt.sigstore.json, a Sigstore bundle made by
//     `cosign sign-blob --new-bundle-format` in release.yml, verifies over
//     checksums.txt against the Sigstore trusted root (embedded, refreshed by
//     RootStore), with at least one Rekor inclusion proof and a signed log time
//     inside the Fulcio certificate's validity;
//  2. the certificate is exactly the release workflow of the pinned repository
//     for this tag: SAN https://github.com/<repo>/.github/workflows/release.yml@refs/tags/<tag>,
//     issuer https://token.actions.githubusercontent.com, source repository URI,
//     source ref refs/tags/<tag> and trigger "push" (a fork or another tag fails);
//  3. checksums.txt then vouches for release.json and for every file installed;
//     release.json vouches for the binaries inside the archive.
package releaseverify

import (
	"bytes"
	"crypto/sha256"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"
	"time"

	protobundle "github.com/sigstore/protobuf-specs/gen/pb-go/bundle/v1"
	"github.com/sigstore/sigstore-go/pkg/bundle"
	"github.com/sigstore/sigstore-go/pkg/fulcio/certificate"
	"github.com/sigstore/sigstore-go/pkg/root"
	"github.com/sigstore/sigstore-go/pkg/verify"
	"google.golang.org/protobuf/encoding/protojson"
)

// maxBundle caps the bundle size read (a real one is a few KiB).
const maxBundle = 1 << 20

// Artifact is a release file to install: its bytes, or a path to hash.
type Artifact struct {
	Name string // the release asset name, as checksums.txt lists it
	Data []byte // the content; when nil, Path is read
	Path string
}

// Input is what Verify checks.
type Input struct {
	// Tag is the release tag ("v1.5.0") being installed.
	Tag string
	// Identity is the expected signer; the zero value is DefaultRepo.
	Identity Identity
	// Checksums, Bundle and Manifest are checksums.txt,
	// checksums.txt.sigstore.json and release.json.
	Checksums []byte
	Bundle    []byte
	Manifest  []byte
	// Artifacts are the files that will be installed (archive, wheel,
	// constraints). Each must be listed in checksums.txt with its digest.
	Artifacts []Artifact
	// TrustedMaterial is the Sigstore trust root; nil uses the embedded one.
	// Callers with a RootStore pass store.TrustedMaterial().
	TrustedMaterial root.TrustedMaterial
}

// Result is what a verified release proved.
type Result struct {
	Manifest        *Manifest
	Checksums       Checksums
	ChecksumsSHA256 string    // digest of checksums.txt (the replay key)
	SignedAt        time.Time // earliest verified log / timestamp-authority time
	Identity        string    // the certificate SAN that signed it
}

// Verify runs design §3.2 steps 1–5 and 7. It returns a *Error (see the Err*
// kinds) on any failure; nothing may be extracted, made executable or run
// before it returns nil. Binaries taken out of the archive are checked
// afterwards with Result.Manifest.VerifyExtracted (step 6).
func Verify(in Input) (*Result, error) {
	if !ValidTag(in.Tag) {
		return nil, fail(ErrManifest, "", fmt.Sprintf("invalid release tag %q", in.Tag), nil)
	}
	tm := in.TrustedMaterial
	if tm == nil {
		var err error
		if tm, err = EmbeddedTrustedRoot(); err != nil {
			return nil, err
		}
	}
	signedAt, cert, err := verifySignature(tm, in.Identity, in.Tag, in.Bundle, in.Checksums)
	if err != nil {
		return nil, err
	}
	sums, err := ParseChecksums(in.Checksums)
	if err != nil {
		return nil, err
	}
	want, ok := sums[ManifestFile]
	if !ok {
		return nil, fail(ErrManifest, ManifestFile, "not listed in checksums.txt (a release published before story 35.1 installs only by hand)", nil)
	}
	if got := sha256Hex(in.Manifest); got != want {
		return nil, fail(ErrChecksum, ManifestFile, fmt.Sprintf("sha256 %s, checksums.txt lists %s", got, want), nil)
	}
	m, err := ParseManifest(in.Manifest)
	if err != nil {
		return nil, err
	}
	if err := m.Validate(in.Tag, sums); err != nil {
		return nil, err
	}
	// release.json names the commit it was built from; the certificate names
	// the commit the signing workflow ran on. They are the same run.
	if cert.SourceRepositoryDigest != "" && cert.SourceRepositoryDigest != m.Commit {
		return nil, fail(ErrManifest, ManifestFile, fmt.Sprintf("commit %s, but the release was signed from %s", m.Commit, cert.SourceRepositoryDigest), nil)
	}
	for _, a := range in.Artifacts {
		if err := checkArtifact(a, sums, m); err != nil {
			return nil, err
		}
	}
	return &Result{
		Manifest:        m,
		Checksums:       sums,
		ChecksumsSHA256: sha256Hex(in.Checksums),
		SignedAt:        signedAt,
		Identity:        cert.SubjectAlternativeName,
	}, nil
}

func checkArtifact(a Artifact, sums Checksums, m *Manifest) error {
	want, ok := sums[a.Name]
	if !ok || !validFileName(a.Name) {
		return fail(ErrChecksum, a.Name, "not listed in checksums.txt", nil)
	}
	var got string
	if a.Data != nil {
		got = sha256Hex(a.Data)
	} else {
		var err error
		if got, err = SHA256File(a.Path); err != nil {
			return fail(ErrChecksum, a.Name, "read", err)
		}
	}
	if got != want {
		return fail(ErrChecksum, a.Name, fmt.Sprintf("sha256 %s, checksums.txt lists %s", got, want), nil)
	}
	if m.Pipeline != nil && a.Name == m.Pipeline.Constraints {
		data := a.Data
		if data == nil {
			b, err := os.ReadFile(a.Path)
			if err != nil {
				return fail(ErrChecksum, a.Name, "read", err)
			}
			if sha256Hex(b) != want { // changed between the two reads
				return fail(ErrChecksum, a.Name, "changed while verifying", nil)
			}
			data = b
		}
		return CheckConstraints(a.Name, data)
	}
	return nil
}

// verifySignature is design §3.2 steps 1–2.
func verifySignature(tm root.TrustedMaterial, id Identity, tag string, rawBundle, checksums []byte) (time.Time, certificate.Summary, error) {
	var none certificate.Summary
	if len(rawBundle) == 0 {
		return time.Time{}, none, fail(ErrSignature, BundleFile,
			"missing (a release published before story 35.1 has no Sigstore bundle: install it by hand)", nil)
	}
	if len(rawBundle) > maxBundle {
		return time.Time{}, none, fail(ErrSignature, BundleFile, "too large", nil)
	}
	var pb protobundle.Bundle
	if err := protojson.Unmarshal(rawBundle, &pb); err != nil {
		return time.Time{}, none, fail(ErrSignature, BundleFile, "decode", err)
	}
	// Offline verification needs the Rekor inclusion proof in the bundle itself:
	// refuse a bundle without one before sigstore-go would.
	entries := pb.GetVerificationMaterial().GetTlogEntries()
	if len(entries) == 0 {
		return time.Time{}, none, fail(ErrTlog, BundleFile, "no transparency log entry", nil)
	}
	for _, e := range entries {
		if e.GetInclusionProof() == nil {
			return time.Time{}, none, fail(ErrTlog, BundleFile, "transparency log entry without an inclusion proof", nil)
		}
	}
	b, err := bundle.NewBundle(&pb)
	if err != nil {
		return time.Time{}, none, fail(ErrSignature, BundleFile, "invalid bundle", err)
	}
	certID, err := id.certificateIdentity(tag)
	if err != nil {
		return time.Time{}, none, fail(ErrIdentity, BundleFile, "build expected identity", err)
	}
	v, err := verify.NewVerifier(tm,
		verify.WithTransparencyLog(1),    // ≥1 Rekor entry, inclusion proof checked
		verify.WithObserverTimestamps(1), // a signed time: Rekor SET or RFC 3161 TSA
	)
	if err != nil {
		return time.Time{}, none, fail(ErrSignature, BundleFile, "verifier", err)
	}
	res, err := v.Verify(b, verify.NewPolicy(
		verify.WithArtifact(bytes.NewReader(checksums)),
		verify.WithCertificateIdentity(certID),
	))
	if err != nil {
		return time.Time{}, none, classify(err)
	}
	var signedAt time.Time
	for _, ts := range res.VerifiedTimestamps {
		if signedAt.IsZero() || ts.Timestamp.Before(signedAt) {
			signedAt = ts.Timestamp
		}
	}
	var cert certificate.Summary
	if res.Signature != nil && res.Signature.Certificate != nil {
		cert = *res.Signature.Certificate
	}
	return signedAt.UTC(), cert, nil
}

// classify maps sigstore-go's errors onto the failure kinds.
func classify(err error) error {
	var noID *verify.ErrNoMatchingCertificateIdentity
	msg := err.Error()
	switch {
	case errors.As(err, &noID), strings.HasPrefix(msg, "failed to verify certificate identity"),
		strings.HasPrefix(msg, "can't verify certificate identities"):
		return fail(ErrIdentity, BundleFile, "", err)
	case strings.HasPrefix(msg, "failed to verify log inclusion"),
		strings.HasPrefix(msg, "failed to verify timestamps"):
		return fail(ErrTlog, BundleFile, "", err)
	default:
		return fail(ErrSignature, BundleFile, "", err)
	}
}

func sha256Sum(b []byte) [32]byte { return sha256.Sum256(b) }

// SHA256File hashes a file without reading it all into memory.
func SHA256File(path string) (string, error) {
	f, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer func() { _ = f.Close() }()
	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		return "", err
	}
	return fmt.Sprintf("%x", h.Sum(nil)), nil
}
