// Package appruntime is the Linux app's runtime copy (Epic 39 story 39.3,
// specs/epics/39-linux-app/contract-runtime.md): the server never runs from the
// AppImage (a FUSE mount at a new path on every run) or from a package's
// /usr/lib (replaced under a running server by an upgrade). It runs from a copy
// the user owns, <data>/runtime/<version>_<digest>, picked by the `current`
// symlink, which only moves while the service is stopped.
package appruntime

import (
	"bufio"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
)

// File names inside a payload (tools/release/linux-app/payload.sh).
const (
	PayloadJSON = "payload.json"
	Manifest    = "files.sha256"
	installedFn = ".installed"
)

// ErrInvalid: the payload is not one this runtime can install (no or a bad
// payload.json, a missing or changed file); the message names it.
var ErrInvalid = errors.New("payload_invalid")

// Payload is the part of payload.json the runtime reads.
type Payload struct {
	Schema   int    `json:"schema"`
	Version  string `json:"version"`
	Platform string `json:"platform"`
	MinGlibc string `json:"min_glibc"`
	Files    struct {
		Manifest string `json:"manifest"`
		Count    int    `json:"count"`
		Bytes    int64  `json:"bytes"`
		SHA256   string `json:"sha256"`
	} `json:"files"`
}

var hexDigest = regexp.MustCompile(`^[0-9a-f]{64}$`)

// ReadPayload reads and checks dir's payload.json.
func ReadPayload(dir string) (Payload, error) {
	var p Payload
	b, err := os.ReadFile(filepath.Join(dir, PayloadJSON))
	if err != nil {
		return p, fmt.Errorf("%w: %v", ErrInvalid, err)
	}
	if err := json.Unmarshal(b, &p); err != nil {
		return p, fmt.Errorf("%w: %s: %v", ErrInvalid, filepath.Join(dir, PayloadJSON), err)
	}
	switch {
	case p.Schema != 1:
		return p, fmt.Errorf("%w: payload.json schema %d (this server knows 1)", ErrInvalid, p.Schema)
	case strings.TrimSpace(p.Version) == "":
		return p, fmt.Errorf("%w: payload.json has no version", ErrInvalid)
	case p.Files.Manifest != Manifest || !hexDigest.MatchString(p.Files.SHA256):
		return p, fmt.Errorf("%w: payload.json lists no %s with its SHA-256 (a payload from before Epic 39)", ErrInvalid, Manifest)
	}
	return p, nil
}

// DirName is the directory a payload is installed in: its version (made safe
// for a file name) and the first 8 hex digits of its manifest, so two builds
// with the same `git describe` never share one.
func DirName(p Payload) string {
	v := strings.Map(func(r rune) rune {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9', r == '.', r == '-', r == '+':
			return r
		}
		return '-'
	}, p.Version)
	if len(v) > 64 {
		v = v[:64]
	}
	return v + "_" + p.Files.SHA256[:8]
}

// Entry is one line of files.sha256: a file (Kind 'f') or a symlink ('l').
type Entry struct {
	Kind   byte
	Mode   os.FileMode
	Size   int64
	SHA256 string
	Target string
	Path   string // relative, "/"-separated
}

// ReadManifest reads dir's files.sha256, after checking it is the one
// payload.json names (its SHA-256), and checks every entry's path and target.
func ReadManifest(dir string, p Payload) ([]Entry, error) {
	mpath := filepath.Join(dir, Manifest)
	sum, err := sha256File(mpath)
	if err != nil {
		return nil, fmt.Errorf("%w: %v", ErrInvalid, err)
	}
	if sum != p.Files.SHA256 {
		return nil, fmt.Errorf("%w: %s has SHA-256 %s, payload.json says %s", ErrInvalid, mpath, sum, p.Files.SHA256)
	}
	f, err := os.Open(mpath)
	if err != nil {
		return nil, fmt.Errorf("%w: %v", ErrInvalid, err)
	}
	defer f.Close()
	var out []Entry
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 64<<10), 1<<20)
	for n := 1; sc.Scan(); n++ {
		e, err := parseEntry(sc.Text())
		if err != nil {
			return nil, fmt.Errorf("%w: %s line %d: %v", ErrInvalid, Manifest, n, err)
		}
		out = append(out, e)
	}
	if err := sc.Err(); err != nil {
		return nil, fmt.Errorf("%w: %v", ErrInvalid, err)
	}
	if len(out) != p.Files.Count {
		return nil, fmt.Errorf("%w: %s has %d entries, payload.json says %d", ErrInvalid, Manifest, len(out), p.Files.Count)
	}
	return out, nil
}

func parseEntry(line string) (Entry, error) {
	parts := strings.Split(line, "\t")
	var e Entry
	switch {
	case len(parts) == 5 && parts[0] == "f":
		mode, err := strconv.ParseUint(parts[1], 8, 32)
		if err != nil || mode&^0o7777 != 0 {
			return e, fmt.Errorf("bad mode %q", parts[1])
		}
		size, err := strconv.ParseInt(parts[2], 10, 64)
		if err != nil || size < 0 {
			return e, fmt.Errorf("bad size %q", parts[2])
		}
		if !hexDigest.MatchString(parts[3]) {
			return e, fmt.Errorf("bad SHA-256 %q", parts[3])
		}
		e = Entry{Kind: 'f', Mode: os.FileMode(mode), Size: size, SHA256: parts[3], Path: parts[4]}
	case len(parts) == 3 && parts[0] == "l":
		e = Entry{Kind: 'l', Target: parts[1], Path: parts[2]}
		if e.Target == "" || path.IsAbs(e.Target) || !inside(path.Join(path.Dir(e.Path), e.Target)) {
			return e, fmt.Errorf("symlink %s → %q leaves the payload", e.Path, e.Target)
		}
	default:
		return e, fmt.Errorf("not an entry: %q", line)
	}
	if !inside(e.Path) || e.Path != path.Clean(e.Path) || e.Path == PayloadJSON || e.Path == Manifest || e.Path == installedFn {
		return e, fmt.Errorf("bad path %q", e.Path)
	}
	return e, nil
}

// inside reports a clean relative path that stays in the payload.
func inside(p string) bool {
	p = path.Clean(p)
	return p != "." && p != ".." && !path.IsAbs(p) && !strings.HasPrefix(p, "../")
}
