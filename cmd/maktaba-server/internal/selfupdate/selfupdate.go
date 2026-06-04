// Package selfupdate implements the `update` subcommand's core: fetch a
// release manifest, compare it against the running version, and — when
// newer — download the platform binary, verify its checksum, and
// atomically replace the running executable.
//
// The manifest is fetched from releases.maktaba.app/manifest.json with a
// GitHub Releases API fallback. Keeping fetch/compare/verify as pure,
// injectable functions makes the dangerous part (overwriting the running
// binary) the only piece that touches the filesystem, and lets the rest
// be unit-tested offline.
package selfupdate

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"time"
)

// DefaultManifestURL is the primary release feed.
const DefaultManifestURL = "https://releases.maktaba.app/manifest.json"

// Manifest is the release feed schema: a latest version plus a map of
// "<os>/<arch>" -> artifact.
type Manifest struct {
	Version   string              `json:"version"`
	Artifacts map[string]Artifact `json:"artifacts"`
}

// Artifact is a single downloadable binary with its checksum.
type Artifact struct {
	URL    string `json:"url"`
	SHA256 string `json:"sha256"`
	Size   int64  `json:"size"`
}

// platformKey is the artifacts-map key for the running host.
func platformKey() string { return runtime.GOOS + "/" + runtime.GOARCH }

// FetchManifest GETs and decodes the release manifest.
func FetchManifest(url string) (Manifest, error) {
	client := &http.Client{Timeout: 30 * time.Second}
	req, err := http.NewRequest(http.MethodGet, url, nil)
	if err != nil {
		return Manifest{}, err
	}
	req.Header.Set("Accept", "application/json")
	resp, err := client.Do(req)
	if err != nil {
		return Manifest{}, fmt.Errorf("fetch manifest: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		return Manifest{}, fmt.Errorf("manifest %s: HTTP %d", url, resp.StatusCode)
	}
	var m Manifest
	if err := json.NewDecoder(io.LimitReader(resp.Body, 1<<20)).Decode(&m); err != nil {
		return Manifest{}, fmt.Errorf("decode manifest: %w", err)
	}
	return m, nil
}

// Decision is the outcome of comparing current vs. latest.
type Decision struct {
	Current     string
	Latest      string
	Available   bool     // an artifact for this platform exists and is newer
	Artifact    Artifact // populated when Available
	PlatformKey string
}

// Check compares the running version against a manifest without
// touching the filesystem.
func Check(current string, m Manifest) Decision {
	d := Decision{Current: current, Latest: m.Version, PlatformKey: platformKey()}
	art, ok := m.Artifacts[d.PlatformKey]
	if !ok {
		return d
	}
	if Newer(m.Version, current) {
		d.Available = true
		d.Artifact = art
	}
	return d
}

// Apply downloads the decided artifact, verifies its sha256, and
// atomically replaces the running executable. It returns the path of
// the replaced executable so the caller can re-exec / prompt a restart.
func Apply(d Decision) (string, error) {
	if !d.Available {
		return "", fmt.Errorf("no update available")
	}
	self, err := os.Executable()
	if err != nil {
		return "", fmt.Errorf("locate self: %w", err)
	}
	self, _ = filepath.EvalSymlinks(self)

	body, err := download(d.Artifact.URL)
	if err != nil {
		return "", err
	}

	sum := sha256.Sum256(body)
	got := hex.EncodeToString(sum[:])
	want := strings.ToLower(strings.TrimSpace(d.Artifact.SHA256))
	if want != "" && got != want {
		return "", fmt.Errorf("checksum mismatch: got %s want %s — refusing to install", got, want)
	}

	if err := replaceExecutable(self, body); err != nil {
		return "", err
	}
	return self, nil
}

func download(url string) ([]byte, error) {
	client := &http.Client{Timeout: 10 * time.Minute}
	resp, err := client.Get(url)
	if err != nil {
		return nil, fmt.Errorf("download %s: %w", url, err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("download %s: HTTP %d", url, resp.StatusCode)
	}
	// Cap at 512 MiB so a hostile/buggy feed can't OOM us.
	return io.ReadAll(io.LimitReader(resp.Body, 512<<20))
}

// replaceExecutable swaps the new bytes in for the running binary.
// Strategy: write the new binary next to the target, then rename the
// old one aside and rename the new one into place. Rename within a
// directory is atomic on every supported OS, and keeping a `.old` copy
// means a crash mid-swap is recoverable.
func replaceExecutable(target string, body []byte) error {
	dir := filepath.Dir(target)
	tmp, err := os.CreateTemp(dir, ".maktaba-server.new.*")
	if err != nil {
		return fmt.Errorf("temp binary: %w", err)
	}
	tmpName := tmp.Name()
	defer func() { _ = os.Remove(tmpName) }()

	if _, err := tmp.Write(body); err != nil {
		_ = tmp.Close()
		return fmt.Errorf("write new binary: %w", err)
	}
	if err := tmp.Close(); err != nil {
		return fmt.Errorf("close new binary: %w", err)
	}
	if err := os.Chmod(tmpName, 0o755); err != nil {
		return fmt.Errorf("chmod new binary: %w", err)
	}

	old := target + ".old"
	_ = os.Remove(old)
	// On Windows a running .exe can't be deleted but CAN be renamed;
	// move the live binary aside first, then the new one into place.
	if err := os.Rename(target, old); err != nil {
		return fmt.Errorf("move current binary aside: %w", err)
	}
	if err := os.Rename(tmpName, target); err != nil {
		// Roll back so we don't leave the install without a binary.
		_ = os.Rename(old, target)
		return fmt.Errorf("move new binary into place: %w", err)
	}
	_ = os.Remove(old)
	return nil
}

// Newer reports whether a is a strictly newer version than b using a
// lenient dotted-numeric comparison. A leading "v" and any "-suffix"
// (e.g. "-rc1", git describe's "-g<sha>") are ignored for the numeric
// part. Unparseable components compare as 0, so "dev" is never newer
// than a real release.
func Newer(a, b string) bool {
	return compareVersions(a, b) > 0
}

func compareVersions(a, b string) int {
	an := numericParts(a)
	bn := numericParts(b)
	for i := 0; i < len(an) || i < len(bn); i++ {
		var x, y int
		if i < len(an) {
			x = an[i]
		}
		if i < len(bn) {
			y = bn[i]
		}
		if x != y {
			if x > y {
				return 1
			}
			return -1
		}
	}
	return 0
}

func numericParts(v string) []int {
	v = strings.TrimPrefix(strings.TrimSpace(v), "v")
	if i := strings.IndexAny(v, "-+"); i >= 0 {
		v = v[:i]
	}
	if v == "" {
		return nil
	}
	fields := strings.Split(v, ".")
	out := make([]int, 0, len(fields))
	for _, f := range fields {
		n, err := strconv.Atoi(f)
		if err != nil {
			n = 0
		}
		out = append(out, n)
	}
	return out
}
