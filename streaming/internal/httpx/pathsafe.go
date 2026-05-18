package httpx

import (
	"errors"
	"path/filepath"
	"strings"
)

// ErrUnsafePath is returned by CanonicalUnder when the candidate path
// escapes its base directory, is absolute, contains a NUL byte, or
// (when the path exists) symlink-resolves outside the base. Callers
// translate this to a 404 problem+json so a probe can't distinguish
// "traversal rejected" from "no such file".
var ErrUnsafePath = errors.New("path escapes its permitted root")

// CanonicalUnder is the single path-canonicalization gate for the
// Streaming file-serving boundary (Story 23.5 AC-2). Every handler
// that turns request-derived path segments (chi URL params: rendition,
// segment, thumb name) into an on-disk path MUST route through this
// helper instead of calling filepath.Join directly.
//
// Contract:
//
//   - base is a trusted, server-computed directory (e.g. the cache
//     layout's HLSDir(session) or thumbs dir). It is cleaned and made
//     absolute.
//   - parts are the UNTRUSTED segments from the request. A NUL byte,
//     an absolute segment, or any segment that (after joining +
//     Clean) walks above base is rejected with ErrUnsafePath.
//   - the joined path is symlink-resolved (filepath.EvalSymlinks)
//     when it exists; the resolved real path must still be inside the
//     resolved base, defeating a symlink planted inside the cache that
//     points outside it. A not-yet-existing path (FFmpeg hasn't
//     written the segment) is allowed through on the lexical guarantee
//     alone — the caller's os.ReadFile then 404s normally.
//
// Returns the cleaned, absolute, traversal-safe path to hand to
// os.Open / os.ReadFile.
func CanonicalUnder(base string, parts ...string) (string, error) {
	for _, p := range parts {
		if strings.ContainsRune(p, 0) {
			return "", ErrUnsafePath
		}
		// An absolute segment (or one that looks rooted) is never a
		// legitimate sub-path of a server-chosen base.
		if filepath.IsAbs(p) {
			return "", ErrUnsafePath
		}
	}

	absBase, err := filepath.Abs(filepath.Clean(base))
	if err != nil {
		return "", ErrUnsafePath
	}

	joined := filepath.Join(append([]string{absBase}, parts...)...)
	joined = filepath.Clean(joined)

	// Lexical containment: joined must be absBase itself or live
	// strictly under absBase + separator. This rejects "..", encoded
	// traversal, and sibling-prefix tricks (/root-evil vs /root).
	if !withinBase(absBase, joined) {
		return "", ErrUnsafePath
	}

	// Symlink containment: if the path exists, its real location must
	// still be under the real base. EvalSymlinks errors when the leaf
	// doesn't exist yet — that's fine, the lexical guarantee above
	// already holds and the caller will 404 on the missing file.
	realBase, err := filepath.EvalSymlinks(absBase)
	if err != nil {
		// Base itself missing/unreadable: fall back to the lexical
		// guarantee. The file open will fail naturally.
		return joined, nil
	}
	realPath, err := filepath.EvalSymlinks(joined)
	if err != nil {
		// Leaf not yet on disk (common: FFmpeg still writing). The
		// lexical guarantee stands; caller handles the missing file.
		return joined, nil
	}
	if !withinBase(realBase, realPath) {
		return "", ErrUnsafePath
	}
	return realPath, nil
}

// withinBase reports whether candidate is base or a descendant of it,
// using a separator-terminated prefix so "/srv/cache" does not match
// "/srv/cache-evil".
func withinBase(base, candidate string) bool {
	if candidate == base {
		return true
	}
	return strings.HasPrefix(candidate, base+string(filepath.Separator))
}
