package releaseverifytest

import (
	"archive/tar"
	"archive/zip"
	"bytes"
	"compress/gzip"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

// FakeRelease is a complete signed release: one platform archive with stub
// binaries, a pipeline wheel and hash-checked constraints, release.json,
// checksums.txt and its Sigstore bundle.
type FakeRelease struct {
	Tag      string
	Repo     string
	Platform string            // "os/arch"
	Files    map[string][]byte // asset name → content (includes the three release files)
	Binaries map[string][]byte // archive entry name → content
	Archive  string            // the platform archive's asset name
	Wheel    string
	Cons     string
	Manifest map[string]any // release.json as written
}

// Release file names, duplicated here so the package has no import cycle.
const (
	checksumsName = "checksums.txt"
	bundleName    = "checksums.txt.sigstore.json"
	manifestName  = "release.json"
)

func hexsum(b []byte) string {
	s := sha256.Sum256(b)
	return hex.EncodeToString(s[:])
}

// NewRelease builds and signs a release of repo at tag for platform ("os/arch").
func (s *Sigstore) NewRelease(repo, tag, platform string) (*FakeRelease, error) {
	ver := strings.TrimPrefix(tag, "v")
	goos, goarch, _ := strings.Cut(platform, "/")
	exe := ""
	if goos == "windows" {
		exe = ".exe"
	}
	r := &FakeRelease{Tag: tag, Repo: repo, Platform: platform, Files: map[string][]byte{}, Binaries: map[string][]byte{}}
	for _, b := range []string{"maktaba-server", "maktaba-api", "maktaba-streaming"} {
		r.Binaries[b+exe] = []byte(fmt.Sprintf("#!fake %s %s %s\n", b, tag, platform))
	}
	base := fmt.Sprintf("maktaba-server-%s-%s-%s", ver, goos, goarch)
	var archive []byte
	var err error
	if goos == "windows" {
		r.Archive = base + ".zip"
		archive, err = zipOf(r.Binaries)
	} else {
		r.Archive = base + ".tar.gz"
		archive, err = tarGzOf(r.Binaries)
	}
	if err != nil {
		return nil, err
	}
	r.Files[r.Archive] = archive
	r.Wheel = "maktaba_pipeline-" + ver + "-py3-none-any.whl"
	r.Files[r.Wheel] = []byte("fake wheel " + tag)
	r.Cons = "maktaba-pipeline-" + ver + "-constraints.txt"
	r.Files[r.Cons] = []byte("# uv export\nanyio==4.4.0 \\\n    --hash=sha256:" + strings.Repeat("ab", 32) + "\n    # via maktaba-pipeline\nidna==3.7 ; python_version >= \"3.12\" \\\n    --hash=sha256:" + strings.Repeat("cd", 32) + " \\\n    --hash=sha256:" + strings.Repeat("ef", 32) + "\n")

	files := map[string]string{}
	for n, b := range r.Binaries {
		files[n] = hexsum(b)
	}
	r.Manifest = map[string]any{
		"v": 1, "version": ver, "tag": tag, "commit": fakeCommit, "channel": "stable",
		"published": time.Now().UTC().Format(time.RFC3339), "schema_rev": 128, "min_node_schema": 128,
		"breaks_previous_home": false, "min_upgrade_from": ver,
		"edge_protocol": 1, "launcher_protocol": 1, "agent_protocol": 2,
		"assets": map[string]any{platform: map[string]any{"archive": r.Archive, "sha256": hexsum(archive), "files": files}},
		"pipeline": map[string]any{
			"wheel": r.Wheel, "wheel_sha256": hexsum(r.Files[r.Wheel]),
			"constraints": r.Cons, "constraints_sha256": hexsum(r.Files[r.Cons]), "python": "3.12",
		},
	}
	if err := r.Rebuild(s, GitHubRelease(repo, tag)); err != nil {
		return nil, err
	}
	return r, nil
}

// MustRelease is NewRelease for tests.
func (s *Sigstore) MustRelease(repo, tag, platform string) *FakeRelease {
	r, err := s.NewRelease(repo, tag, platform)
	if err != nil {
		panic(err)
	}
	return r
}

// Rebuild rewrites release.json from r.Manifest, recomputes checksums.txt over
// every asset and signs it with o (used after a test edits a file).
func (r *FakeRelease) Rebuild(s *Sigstore, o SignOptions) error {
	mj, err := json.MarshalIndent(r.Manifest, "", "  ")
	if err != nil {
		return err
	}
	r.Files[manifestName] = append(mj, '\n')
	delete(r.Files, checksumsName)
	delete(r.Files, bundleName)
	r.Files[checksumsName] = r.checksums()
	return r.Resign(s, o)
}

// Resign signs the current checksums.txt again with o.
func (r *FakeRelease) Resign(s *Sigstore, o SignOptions) error {
	b, err := s.SignBlob(r.Files[checksumsName], o)
	if err != nil {
		return err
	}
	r.Files[bundleName] = b
	return nil
}

func (r *FakeRelease) checksums() []byte {
	names := make([]string, 0, len(r.Files))
	for n := range r.Files {
		names = append(names, n)
	}
	sort.Strings(names)
	var b bytes.Buffer
	for _, n := range names {
		fmt.Fprintf(&b, "%s  ./%s\n", hexsum(r.Files[n]), n)
	}
	return b.Bytes()
}

// WriteDir writes every asset into dir.
func (r *FakeRelease) WriteDir(dir string) error {
	for n, b := range r.Files {
		if err := os.WriteFile(filepath.Join(dir, n), b, 0o644); err != nil {
			return err
		}
	}
	return nil
}

func tarGzOf(files map[string][]byte) ([]byte, error) {
	var buf bytes.Buffer
	gz := gzip.NewWriter(&buf)
	tw := tar.NewWriter(gz)
	for _, n := range sortedKeys(files) {
		if err := tw.WriteHeader(&tar.Header{Name: "./" + n, Mode: 0o755, Size: int64(len(files[n])), Typeflag: tar.TypeReg}); err != nil {
			return nil, err
		}
		if _, err := tw.Write(files[n]); err != nil {
			return nil, err
		}
	}
	if err := tw.Close(); err != nil {
		return nil, err
	}
	if err := gz.Close(); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

func zipOf(files map[string][]byte) ([]byte, error) {
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	for _, n := range sortedKeys(files) {
		w, err := zw.Create(n)
		if err != nil {
			return nil, err
		}
		if _, err := w.Write(files[n]); err != nil {
			return nil, err
		}
	}
	if err := zw.Close(); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

func sortedKeys(m map[string][]byte) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}
