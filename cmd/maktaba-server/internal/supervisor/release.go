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

	"github.com/Hamza-Labs-Core/Maktaba/shared/go/layout"
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
	return resolveReleaseBinary(binRoot, version, wantSHA256, currentVersion, fallback, nil)
}

// resolveReleaseBinary is ResolveReleaseBinary that also looks at kept(version): the
// streaming binaries of that release this install keeps elsewhere (a desktop app's copy
// kept before its update, another runtime directory), the first whose SHA-256 is the
// release's. A TV held on the release before (story 35.6, the owner's decision of
// 2026-10-28) restarts its slots on that release's binary from there.
func resolveReleaseBinary(binRoot, version, wantSHA256, currentVersion, fallback string,
	kept func(version string) []string) (ReleaseBinary, error) {
	v := strings.TrimSpace(version)
	if v == "" || strings.ContainsAny(v, `/\`) || v == "." || v == ".." {
		return ReleaseBinary{}, fmt.Errorf("%w: bad version %q", ErrReleaseBinaryMissing, version)
	}
	// a release directory is named by the bare version (layout.NormalizeVersion); a release
	// records the binary's own spelling (a release build says "v1.5.0")
	path := filepath.Join(binRoot, "releases", bareVersion(v), streamingExe())
	if _, err := os.Stat(path); err != nil {
		if found := keptBinary(kept, v, wantSHA256); found != "" {
			path = found
		} else if bareVersion(v) != bareVersion(currentVersion) || fallback == "" {
			return ReleaseBinary{}, fmt.Errorf("%w: %s", ErrReleaseBinaryMissing, path)
		} else {
			path = fallback
		}
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

// keptBinary is the first of kept(version) that exists (and, with a want SHA-256, is that
// file): "" when none.
func keptBinary(kept func(string) []string, version, wantSHA256 string) string {
	if kept == nil {
		return ""
	}
	want := strings.ToLower(strings.TrimSpace(wantSHA256))
	for _, p := range kept(version) {
		if st, err := os.Stat(p); err != nil || st.IsDir() {
			continue
		}
		if want != "" {
			if sum, err := fileSHA256(p); err != nil || sum != want {
				continue
			}
		}
		return p
	}
	return ""
}

// bareVersion is a version without its "v" (a release directory's name).
func bareVersion(v string) string {
	v = strings.TrimSpace(v)
	if len(v) > 1 && (v[0] == 'v' || v[0] == 'V') && v[1] >= '0' && v[1] <= '9' {
		return v[1:]
	}
	return v
}

// ReleaseStreaming is this release's streaming binary as a TV release records it (story
// 35.6: the update's TV change): the file the supervisor runs, its SHA-256 from the
// release directory's .verified when the launcher runs one, else hashed. version is the
// binary's own spelling of its version.
func ReleaseStreaming(opts Options, version string) (ReleaseBinary, error) {
	bin, err := opts.resolve("maktaba-streaming", "MAKTABA_STREAMING_BIN")
	if err != nil {
		return ReleaseBinary{}, err
	}
	if opts.ReleaseDir != "" && filepath.Dir(bin) == filepath.Clean(opts.ReleaseDir) {
		if v, err := layout.ReadVerifiedDir(opts.ReleaseDir); err == nil && v != nil {
			if sum := strings.ToLower(v.SHA256[streamingExe()]); sum != "" {
				return ReleaseBinary{Path: bin, Version: version, SHA256: sum}, nil
			}
		}
	}
	sum, err := fileSHA256(bin)
	if err != nil {
		return ReleaseBinary{}, err
	}
	return ReleaseBinary{Path: bin, Version: version, SHA256: sum}, nil
}

// ReleasesInUse are the release versions this supervisor's running processes run from
// (bare: a release directory's name): a TV slot bound to a release's binary, any other
// process run from <install>/bin/releases/<v>/. Pruning keeps them (story 35.6 AC6: a
// front door or a slot outliving its release's turn keeps its binary).
func (s *Supervisor) ReleasesInUse() []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	seen := map[string]bool{}
	var out []string
	add := func(v string) {
		if v = bareVersion(v); v != "" && !seen[v] {
			seen[v] = true
			out = append(out, v)
		}
	}
	if s.held.path != "" {
		// the release the live TV is held on: the unbound slots run its binary
		add(s.held.version)
	}
	for _, c := range s.children {
		// a slot's binding counts while it is bound, running or not (between two starts:
		// a slot restarting must find its release's binary)
		if c.binding != nil && c.binding.Version != "" {
			add(c.binding.Version)
		}
		if c.cmd == nil {
			continue
		}
		add(releaseOfPath(c.cmd.Path))
	}
	return out
}

// releaseOfPath is the release a binary runs from: <…>/releases/<v>/<exe> ("" when it is
// not under a releases directory).
func releaseOfPath(p string) string {
	if p == "" {
		return ""
	}
	dir := filepath.Dir(filepath.Clean(p))
	if filepath.Base(filepath.Dir(dir)) != "releases" {
		return ""
	}
	return filepath.Base(dir)
}

// releaseBinRoot is the root ResolveReleaseBinary looks under: <install>/bin
// when the launcher started us (MAKTABA_RELEASE_DIR = <install>/bin/releases/
// <v>, Epic 35 design §2.1), else the directory of the built binary (story
// 33.2, before side-by-side installs).
func releaseBinRoot(base string) string {
	if rd := os.Getenv("MAKTABA_RELEASE_DIR"); rd != "" {
		return filepath.Dir(filepath.Dir(filepath.Clean(rd)))
	}
	return filepath.Dir(base)
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
