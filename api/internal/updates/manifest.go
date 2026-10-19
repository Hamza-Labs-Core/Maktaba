// Package updates is the home's update policy, release discovery and scheduling (Epic
// 35, story 35.3; design §4). The API stays the discoverer: it keeps a catalog of the
// releases newer than the watermark (release_catalog, slot 0132), verifies each one's
// signed small files as soon as it sees them, chooses the target by policy
// (rules.updates) and starts a run in the quiet window by asking the supervisor (story
// 35.4) to execute it.
//
// Verification itself is story 35.1's shared package (shared/go/releaseverify). This
// package depends on it only through the Verifier interface below, so the API can be
// built and tested while that package lands; main.go wires the adapter.
package updates

import (
	"context"
	"errors"
	"strings"
	"time"
)

// Release file names (design §3.1). Only these three small files are downloaded and
// verified when a release is first seen (design §4, story 35.3 AC2).
const (
	ChecksumsFile = "checksums.txt"
	BundleFile    = "checksums.txt.sigstore.json"
	ManifestFile  = "release.json"
)

// Manifest is the part of a verified release.json (design §3.1) that policy and the
// scheduler read. It mirrors releaseverify.Manifest field for field (same JSON names) so
// the adapter is a plain copy and the stored jsonb is the same document.
type Manifest struct {
	V                  int       `json:"v"`
	Version            string    `json:"version"`
	Tag                string    `json:"tag"`
	Commit             string    `json:"commit,omitempty"`
	Channel            string    `json:"channel"`
	Published          time.Time `json:"published"`
	SchemaRev          int       `json:"schema_rev"`
	MinNodeSchema      int       `json:"min_node_schema"`
	BreaksPreviousHome bool      `json:"breaks_previous_home"`
	MinUpgradeFrom     string    `json:"min_upgrade_from,omitempty"`
	EdgeProtocol       int       `json:"edge_protocol,omitempty"`
	LauncherProtocol   int       `json:"launcher_protocol,omitempty"`
	AgentProtocol      int       `json:"agent_protocol,omitempty"`
}

// ReleaseFiles are the three small signed files of a release, as downloaded.
type ReleaseFiles struct {
	Checksums []byte // checksums.txt
	Bundle    []byte // checksums.txt.sigstore.json
	Manifest  []byte // release.json
}

// Verified is what a verified release proved (releaseverify.Result).
type Verified struct {
	Manifest        Manifest
	ChecksumsSHA256 string    // digest of checksums.txt: the replay key
	SignedAt        time.Time // earliest verified transparency-log time
	Identity        string    // the certificate SAN that signed it
}

// Verifier checks a release's signed small files (design §3.2 steps 1-4) for tag.
//
// Adapter for releaseverify (story 35.1):
//
//	res, err := releaseverify.Verify(releaseverify.Input{Tag: tag, Identity: id,
//	    Checksums: f.Checksums, Bundle: f.Bundle, Manifest: f.Manifest,
//	    TrustedMaterial: tm})
//	// err: *releaseverify.Error -> &VerifyError{Kind: kindOf(e.Kind), File: e.File, Detail: e.Detail}
//	// res: Verified{Manifest: copy(*res.Manifest), ChecksumsSHA256: res.ChecksumsSHA256,
//	//      SignedAt: res.SignedAt, Identity: res.Identity}
type Verifier interface {
	VerifyRelease(ctx context.Context, tag string, f ReleaseFiles) (Verified, error)
}

// Failure kinds, the names of releaseverify's Err* sentinels (story 35.1 AC5) plus the
// two this package adds.
const (
	KindSignature   = "signature"
	KindIdentity    = "identity"
	KindTlog        = "tlog"
	KindChecksum    = "checksum"
	KindManifest    = "manifest"
	KindNoHashes    = "no_hashes"
	KindReplay      = "replay"
	KindDowngrade   = "downgrade"
	KindRootExpired = "root_expired"
	// KindNoManifest: a release published before story 35.1 (no release.json or no
	// bundle). Never an automatic target (design §3.2), and not an incident.
	KindNoManifest = "no_manifest"
	// KindUnavailable: no verifier is wired in this build (before story 35.1 lands) or
	// the files could not be downloaded. The entry stays unverified and is retried.
	KindUnavailable = "unavailable"
)

// VerifyError is a typed verification failure naming the file it is about.
type VerifyError struct {
	Kind   string `json:"kind"`
	File   string `json:"file,omitempty"`
	Detail string `json:"detail,omitempty"`
}

func (e *VerifyError) Error() string {
	var b strings.Builder
	b.WriteString("release verification: ")
	b.WriteString(e.Kind)
	if e.File != "" {
		b.WriteString(": " + e.File)
	}
	if e.Detail != "" {
		b.WriteString(": " + e.Detail)
	}
	return b.String()
}

// Incident reports whether the failure is a bad release (a critical notification at
// once, design §4) rather than "not verifiable yet".
func (e *VerifyError) Incident() bool {
	return e.Kind != KindNoManifest && e.Kind != KindUnavailable
}

// AsVerifyError returns err as a *VerifyError; any other error is KindUnavailable.
func AsVerifyError(err error) *VerifyError {
	if err == nil {
		return nil
	}
	var ve *VerifyError
	if errors.As(err, &ve) {
		return ve
	}
	return &VerifyError{Kind: KindUnavailable, Detail: err.Error()}
}

// Unwired is the Verifier of a build without story 35.1's verifier: nothing verifies,
// so nothing is ever chosen as a target (fail closed).
type Unwired struct{}

// VerifyRelease always fails with KindUnavailable.
func (Unwired) VerifyRelease(context.Context, string, ReleaseFiles) (Verified, error) {
	return Verified{}, &VerifyError{Kind: KindUnavailable, Detail: "release verification is not built in"}
}

// notifyOnce is the repeat window of a notice that should be made once about its
// subject (the hub drops a repeat of a kind about a subject inside the window).
const notifyOnce = 365 * 24 * time.Hour
