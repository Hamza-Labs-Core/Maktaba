package releaseverify

import (
	"errors"
	"strings"
)

// Failure kinds (story 35.1 AC5). Every error Verify, CheckMonotonic or the root
// store returns is an *Error whose Kind is one of these, so callers test with
// errors.Is(err, releaseverify.ErrIdentity) and show Error.File to the operator.
var (
	// ErrSignature: the Sigstore bundle is missing, unparseable, or its signature
	// does not verify over checksums.txt against the trusted root.
	ErrSignature = errors.New("release signature invalid")
	// ErrIdentity: the signature is valid but was made by another workflow, repo,
	// tag, trigger or OIDC issuer than the pinned release identity.
	ErrIdentity = errors.New("release signed by an unexpected identity")
	// ErrTlog: no Rekor inclusion proof, or the proof / its signed time does not
	// verify.
	ErrTlog = errors.New("transparency log proof missing or invalid")
	// ErrChecksum: a file's SHA-256 is not the one checksums.txt (or release.json)
	// lists, or the file is not listed at all.
	ErrChecksum = errors.New("checksum mismatch")
	// ErrManifest: checksums.txt or release.json is malformed or disagrees with the
	// tag or with itself.
	ErrManifest = errors.New("release manifest invalid")
	// ErrNoHashes: a constraints line installs a package without --hash=sha256:.
	ErrNoHashes = errors.New("constraints entry without --hash=sha256")
	// ErrReplay: an already-installed version with different content, or a release
	// signed before the one installed.
	ErrReplay = errors.New("release replay refused")
	// ErrDowngrade: a version below the machine's watermark.
	ErrDowngrade = errors.New("downgrade refused")
	// ErrRootExpired: the Sigstore trusted root is too old (no successful TUF
	// refresh within RootStore.MaxAge) or TUF reports expired metadata.
	// Verification fails closed.
	ErrRootExpired = errors.New("sigstore trusted root expired")
)

// Error is a typed verification failure naming the file it is about.
type Error struct {
	Kind   error  // one of the Err* sentinels
	File   string // the release file concerned ("checksums.txt", an archive, …)
	Detail string // human detail, may be empty
	Err    error  // underlying cause, may be nil
}

func (e *Error) Error() string {
	var b strings.Builder
	b.WriteString(e.Kind.Error())
	if e.File != "" {
		b.WriteString(": ")
		b.WriteString(e.File)
	}
	if e.Detail != "" {
		b.WriteString(": ")
		b.WriteString(e.Detail)
	}
	if e.Err != nil {
		b.WriteString(": ")
		b.WriteString(e.Err.Error())
	}
	return b.String()
}

// Is matches the failure kind.
func (e *Error) Is(target error) bool { return target == e.Kind }

// Unwrap exposes the underlying cause.
func (e *Error) Unwrap() error { return e.Err }

func fail(kind error, file, detail string, cause error) *Error {
	return &Error{Kind: kind, File: file, Detail: detail, Err: cause}
}
