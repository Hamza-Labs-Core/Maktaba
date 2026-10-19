package supervisor

// TV release binaries (Epic 33, story 33.2 AC4; design §3.3): a TV slot runs the
// streaming binary of its release, installed side by side under
// <install>/bin/releases/<version>/, and only when that file's SHA-256 is the one the
// release records. The two-slot runtime (story 33.1) resolves a slot's executable
// through ResolveReleaseBinary at each (re)start; the path never comes from a request.

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"strings"
)

// Refusal reasons (event tv_slot_refused).
var (
	// ErrReleaseBinaryMissing: no binary for that version (binary_missing).
	ErrReleaseBinaryMissing = errors.New("binary_missing")
	// ErrReleaseChecksum: the file is not the release's binary (checksum).
	ErrReleaseChecksum = errors.New("checksum")
)

// ReleaseBinary is a resolved slot executable.
type ReleaseBinary struct {
	Path    string
	Version string
	SHA256  string
}

func streamingExe() string {
	if runtime.GOOS == "windows" {
		return "maktaba-streaming.exe"
	}
	return "maktaba-streaming"
}

// ResolveReleaseBinary finds the streaming binary of a release: binRoot/releases/
// <version>/maktaba-streaming[.exe]. When that is absent and the release is the running
// version (release 1, before side-by-side installs: plan 33.2 step 4) the current
// binary (fallback, may be "") is used. A non-empty want SHA-256 must match the file.
func ResolveReleaseBinary(binRoot, version, wantSHA256, currentVersion, fallback string) (ReleaseBinary, error) {
	v := strings.TrimSpace(version)
	if v == "" || strings.ContainsAny(v, `/\`) || v == "." || v == ".." {
		return ReleaseBinary{}, fmt.Errorf("%w: bad version %q", ErrReleaseBinaryMissing, version)
	}
	path := filepath.Join(binRoot, "releases", v, streamingExe())
	if _, err := os.Stat(path); err != nil {
		if v != currentVersion || fallback == "" {
			return ReleaseBinary{}, fmt.Errorf("%w: %s", ErrReleaseBinaryMissing, path)
		}
		path = fallback
	}
	sum, err := fileSHA256(path)
	if err != nil {
		return ReleaseBinary{}, fmt.Errorf("%w: %v", ErrReleaseBinaryMissing, err)
	}
	if w := strings.ToLower(strings.TrimSpace(wantSHA256)); w != "" && w != sum {
		return ReleaseBinary{}, fmt.Errorf("%w: %s is %s, the release wants %s", ErrReleaseChecksum, path, sum, w)
	}
	return ReleaseBinary{Path: path, Version: v, SHA256: sum}, nil
}

func fileSHA256(path string) (string, error) {
	f, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer f.Close()
	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		return "", err
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}
