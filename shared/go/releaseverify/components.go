package releaseverify

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"

	"github.com/sigstore/sigstore-go/pkg/root"
)

// The signed component index (Epic 51, story 51.15): one components-<version>.json per
// release, listing every pack and every model file with its sha256 and size. It is
// signed by the one release signature: checksums.txt lists it (and every pack), and
// release.json names it with its digest.

// ComponentsIndexSchema is the index schema this package reads.
const ComponentsIndexSchema = 1

// IndexLifetime is how long after its publish time an index is good (story 51.15 §3).
const IndexLifetime = 365 * 24 * time.Hour

// Index refusals beyond the release's own (a refusal of the signature, the identity, the
// log proof or a digest is the component's verify_failed).
var (
	// ErrIndexMissing: the release has no component index (released before 51.15):
	// nothing can be added later from it.
	ErrIndexMissing = errors.New("component index missing")
	// ErrIndexExpired: now is past the index's expires.
	ErrIndexExpired = errors.New("component index expired")
	// ErrIndexWrongVersion: the index is another version's than this server's.
	ErrIndexWrongVersion = errors.New("component index is for another version")
	// ErrIndexReplayed: this machine has seen a newer index for the same version.
	ErrIndexReplayed = errors.New("component index replayed")
)

// IndexReason is the component status reason for a refusal: index_missing,
// index_expired, index_wrong_version, index_replayed, else verify_failed.
func IndexReason(err error) string {
	switch {
	case err == nil:
		return ""
	case errors.Is(err, ErrIndexMissing):
		return "index_missing"
	case errors.Is(err, ErrIndexExpired):
		return "index_expired"
	case errors.Is(err, ErrIndexWrongVersion):
		return "index_wrong_version"
	case errors.Is(err, ErrIndexReplayed):
		return "index_replayed"
	default:
		return "verify_failed"
	}
}

// ComponentsIndexName is the index's release file name for version ("0.4.2").
func ComponentsIndexName(version string) string { return "components-" + version + ".json" }

// ComponentsRef is release.json's "components": the index's name and digest.
type ComponentsRef struct {
	Index  string `json:"index"`
	SHA256 string `json:"sha256"`
}

// ComponentsIndex is components-<version>.json.
type ComponentsIndex struct {
	Schema    int       `json:"schema"`
	Version   string    `json:"version"`
	Commit    string    `json:"commit"`
	Published time.Time `json:"published"`
	Expires   time.Time `json:"expires"`
	// Server is the server versions the packs are for: always the exact version.
	Server string       `json:"server"`
	Packs  []IndexPack  `json:"packs"`
	Models []IndexModel `json:"models"`
}

// IndexPack is one pack: one asset component on one platform.
type IndexPack struct {
	ID            string `json:"id"`
	Platform      string `json:"platform"`
	Name          string `json:"name"`
	SHA256        string `json:"sha256"` // the whole pack's (its parts joined)
	Size          int64  `json:"size"`
	UnpackedBytes int64  `json:"unpacked_bytes"`
	FilesSHA256   string `json:"files_sha256"`
	// Requires are other components this pack needs installed first (ids).
	Requires []string `json:"requires"`
	// Parts are the release files of a pack too big for one GitHub asset, in order.
	Parts []IndexPart `json:"parts,omitempty"`
}

// IndexPart is one release file of a split pack.
type IndexPart struct {
	Name   string `json:"name"`
	SHA256 string `json:"sha256"`
	Size   int64  `json:"size"`
}

// Files are the release files that carry the pack: its parts, or the one file.
func (p IndexPack) Files() []IndexPart {
	if len(p.Parts) > 0 {
		return p.Parts
	}
	return []IndexPart{{Name: p.Name, SHA256: p.SHA256, Size: p.Size}}
}

// IndexModel is one model source: the files a component downloads from it, on the
// platforms that use it.
type IndexModel struct {
	ID        string      `json:"id"`
	Source    string      `json:"source"` // hf:<repo>@<commit> or ollama:<name>:<tag>@sha256:<digest>
	Platforms []string    `json:"platforms"`
	Gated     bool        `json:"gated"`
	Files     []IndexFile `json:"files"`
}

// IndexFile is one model file.
type IndexFile struct {
	Path   string `json:"path"`
	SHA256 string `json:"sha256"`
	Size   int64  `json:"size"`
}

var (
	indexIDRe       = regexp.MustCompile(`^[a-z][a-z0-9_]*(\.[a-z0-9_-]+)+$`)
	indexPlatformRe = regexp.MustCompile(`^(darwin|windows|linux)-(arm64|amd64)$`)
	// HFSourceRe and OllamaSourceRe are the pinned model sources: a Hugging Face repo at a
	// commit (never a branch), an ollama model at its manifest digest.
	HFSourceRe     = regexp.MustCompile(`^hf:[A-Za-z0-9][A-Za-z0-9._-]*/[A-Za-z0-9][A-Za-z0-9._-]*@[0-9a-f]{40}$`)
	OllamaSourceRe = regexp.MustCompile(`^ollama:[a-z0-9][a-z0-9._/-]*:[A-Za-z0-9._-]+@sha256:[0-9a-f]{64}$`)
)

// ParseComponentsIndex decodes an index and checks its form. Unknown fields are allowed
// (a later minor addition); another schema is not.
func ParseComponentsIndex(b []byte) (*ComponentsIndex, error) {
	var idx ComponentsIndex
	if err := json.Unmarshal(b, &idx); err != nil {
		return nil, fail(ErrManifest, "components index", "decode", err)
	}
	name := ComponentsIndexName(idx.Version)
	bad := func(format string, a ...any) error {
		return fail(ErrManifest, name, fmt.Sprintf(format, a...), nil)
	}
	if idx.Schema != ComponentsIndexSchema {
		return nil, bad("unsupported schema %d (want %d)", idx.Schema, ComponentsIndexSchema)
	}
	if !ValidTag("v" + idx.Version) {
		return nil, bad("bad version %q", idx.Version)
	}
	if !commitRe.MatchString(idx.Commit) {
		return nil, bad("commit %q is not a 40-hex git sha", idx.Commit)
	}
	if idx.Published.IsZero() || !idx.Expires.After(idx.Published) {
		return nil, bad("published %s / expires %s", idx.Published, idx.Expires)
	}
	if want := ServerRange(idx.Version); idx.Server != want {
		return nil, bad("server %q, the exact version is %q", idx.Server, want)
	}
	seenPack := map[string]bool{}
	seenFile := map[string]bool{}
	for i, p := range idx.Packs {
		if !indexIDRe.MatchString(p.ID) || !indexPlatformRe.MatchString(p.Platform) {
			return nil, bad("packs[%d]: bad id %q or platform %q", i, p.ID, p.Platform)
		}
		key := p.ID + " " + p.Platform
		if seenPack[key] {
			return nil, bad("packs: %s on %s twice", p.ID, p.Platform)
		}
		seenPack[key] = true
		if want := p.ID + "-" + idx.Version + "-" + p.Platform + ".tar.zst"; p.Name != want {
			return nil, bad("packs[%d]: name %q, want %q", i, p.Name, want)
		}
		if !hexRe.MatchString(p.SHA256) || !hexRe.MatchString(p.FilesSHA256) || p.Size <= 0 || p.UnpackedBytes < 0 {
			return nil, bad("packs[%d]: bad sha256, files_sha256, size or unpacked_bytes", i)
		}
		for _, r := range p.Requires {
			if !indexIDRe.MatchString(r) {
				return nil, bad("packs[%d]: bad requires %q", i, r)
			}
		}
		var sum int64
		for n, part := range p.Parts {
			if want := fmt.Sprintf("%s.part%d", p.Name, n+1); part.Name != want {
				return nil, bad("packs[%d].parts[%d]: name %q, want %q", i, n, part.Name, want)
			}
			if !hexRe.MatchString(part.SHA256) || part.Size <= 0 {
				return nil, bad("packs[%d].parts[%d]: bad sha256 or size", i, n)
			}
			sum += part.Size
		}
		if len(p.Parts) == 1 || (len(p.Parts) > 0 && sum != p.Size) {
			return nil, bad("packs[%d]: parts do not add up to the pack", i)
		}
		for _, f := range p.Files() {
			if seenFile[f.Name] {
				return nil, bad("packs: %s listed twice", f.Name)
			}
			seenFile[f.Name] = true
		}
	}
	for i, m := range idx.Models {
		if !indexIDRe.MatchString(m.ID) {
			return nil, bad("models[%d]: bad id %q", i, m.ID)
		}
		if !HFSourceRe.MatchString(m.Source) && !OllamaSourceRe.MatchString(m.Source) {
			return nil, bad("models[%d] (%s): source %q is not pinned to a commit or digest", i, m.ID, m.Source)
		}
		if len(m.Platforms) == 0 || len(m.Files) == 0 {
			return nil, bad("models[%d] (%s): no platforms or no files", i, m.ID)
		}
		for _, p := range m.Platforms {
			if !indexPlatformRe.MatchString(p) {
				return nil, bad("models[%d] (%s): bad platform %q", i, m.ID, p)
			}
		}
		paths := map[string]bool{}
		for _, f := range m.Files {
			if !safeModelPath(f.Path) || paths[f.Path] || !hexRe.MatchString(f.SHA256) || f.Size <= 0 {
				return nil, bad("models[%d] (%s): bad file %q", i, m.ID, f.Path)
			}
			paths[f.Path] = true
		}
	}
	return &idx, nil
}

// ServerRange is the index's "server" for version: exactly that version.
func ServerRange(version string) string { return "=" + version }

func safeModelPath(p string) bool {
	if p == "" || strings.HasPrefix(p, "/") || strings.ContainsAny(p, "\\:\x00\t\n\r") {
		return false
	}
	for _, el := range strings.Split(p, "/") {
		if el == "" || el == "." || el == ".." {
			return false
		}
	}
	return true
}

// ComponentsInput is what VerifyComponentsIndex checks: the release's three signed files,
// its index, and this machine's facts.
type ComponentsInput struct {
	// Tag, Identity, Checksums, Bundle, Manifest and TrustedMaterial are Verify's.
	Tag             string
	Identity        Identity
	Checksums       []byte
	Bundle          []byte
	Manifest        []byte
	TrustedMaterial root.TrustedMaterial
	// Index is components-<version>.json's bytes.
	Index []byte
	// ServerVersion is the version this server runs ("0.4.2").
	ServerVersion string
	// Now is the clock (zero: time.Now).
	Now time.Time
	// Seen is the newest index seen per version (story 51.15 §4 step 6); "" checks no
	// replay and writes nothing (a test, or a one-off check by hand).
	Seen IndexWatermarkFile
}

// VerifiedIndex is an index that passed.
type VerifiedIndex struct {
	Index   *ComponentsIndex
	SHA256  string // the index file's digest
	Release *Result
}

// VerifyComponentsIndex runs story 51.15 §4: the release's signature and release.json
// (Verify), the index's digest against checksums.txt and release.json, its form, its
// expiry, its version, and the per-version watermark, which it then raises. Every pack
// and part the index lists must be in checksums.txt with the same digest. On any refusal
// nothing is written. A refusal's component reason is IndexReason(err).
func VerifyComponentsIndex(in ComponentsInput) (*VerifiedIndex, error) {
	res, err := Verify(Input{Tag: in.Tag, Identity: in.Identity, Checksums: in.Checksums, Bundle: in.Bundle,
		Manifest: in.Manifest, TrustedMaterial: in.TrustedMaterial})
	if err != nil {
		return nil, err
	}
	m := res.Manifest
	name := ComponentsIndexName(m.Version)
	if m.Components == nil {
		return nil, fail(ErrIndexMissing, ManifestFile, "release.json names no component index", nil)
	}
	if m.Components.Index != name {
		return nil, fail(ErrManifest, ManifestFile, fmt.Sprintf("components.index %q, want %q", m.Components.Index, name), nil)
	}
	got := sha256Hex(in.Index)
	if want, ok := res.Checksums[name]; !ok || want != got {
		return nil, fail(ErrChecksum, name, fmt.Sprintf("sha256 %s, checksums.txt lists %q", got, want), nil)
	}
	if got != m.Components.SHA256 {
		return nil, fail(ErrChecksum, name, fmt.Sprintf("sha256 %s, release.json says %s", got, m.Components.SHA256), nil)
	}
	idx, err := ParseComponentsIndex(in.Index)
	if err != nil {
		return nil, err
	}
	if idx.Version != m.Version || idx.Commit != m.Commit {
		return nil, fail(ErrManifest, name, fmt.Sprintf("index of %s at %s in the release of %s at %s", idx.Version, idx.Commit, m.Version, m.Commit), nil)
	}
	for _, p := range idx.Packs {
		for _, f := range p.Files() {
			if want, ok := res.Checksums[f.Name]; !ok || want != f.SHA256 {
				return nil, fail(ErrChecksum, f.Name, "the index's digest is not the one checksums.txt lists", nil)
			}
		}
	}
	now := in.Now
	if now.IsZero() {
		now = time.Now()
	}
	if now.After(idx.Expires) {
		return nil, fail(ErrIndexExpired, name, "expired "+idx.Expires.UTC().Format(time.RFC3339), nil)
	}
	if v := strings.TrimPrefix(in.ServerVersion, "v"); v != idx.Version {
		return nil, fail(ErrIndexWrongVersion, name, fmt.Sprintf("this server runs %q", v), nil)
	}
	out := &VerifiedIndex{Index: idx, SHA256: got, Release: res}
	if in.Seen.Path == "" {
		return out, nil
	}
	if err := in.Seen.Commit(idx.Version, idx.Published, got); err != nil {
		return nil, err
	}
	return out, nil
}

// IndexSeen is the newest index seen for one version.
type IndexSeen struct {
	Published time.Time `json:"published"`
	SHA256    string    `json:"sha256"`
}

// IndexWatermarkFile keeps the newest index seen per version: <data>/state/components-index.json.
// It only goes up. The rule is per version (H6): a home rolled back to 0.4.2 after it saw
// 0.4.3's index still installs 0.4.2's packs.
type IndexWatermarkFile struct {
	Path string
}

// IndexWatermarkPath is the watermark's place under a data folder.
func IndexWatermarkPath(dataDir string) string {
	return filepath.Join(dataDir, "state", "components-index.json")
}

type indexWatermark struct {
	Versions map[string]IndexSeen `json:"versions"`
}

// Load reads the watermark; a missing file is empty. A file that cannot be read is an
// error: the check fails closed.
func (f IndexWatermarkFile) Load() (map[string]IndexSeen, error) {
	b, err := os.ReadFile(f.Path)
	if errors.Is(err, os.ErrNotExist) {
		return map[string]IndexSeen{}, nil
	}
	if err != nil {
		return nil, err
	}
	var w indexWatermark
	if err := json.Unmarshal(b, &w); err != nil {
		return nil, fmt.Errorf("releaseverify: %s: %w", f.Path, err)
	}
	if w.Versions == nil {
		w.Versions = map[string]IndexSeen{}
	}
	return w.Versions, nil
}

// Check refuses an index older than the newest seen for its version, or published at
// the same time with other bytes (ErrIndexReplayed).
func (f IndexWatermarkFile) Check(version string, published time.Time, sha string) error {
	seen, err := f.Load()
	if err != nil {
		return err
	}
	return checkSeen(seen, version, published, sha)
}

func checkSeen(seen map[string]IndexSeen, version string, published time.Time, sha string) error {
	s, ok := seen[version]
	if !ok {
		return nil
	}
	name := ComponentsIndexName(version)
	switch {
	case published.Before(s.Published):
		return fail(ErrIndexReplayed, name, fmt.Sprintf("published %s, this machine saw one published %s",
			published.UTC().Format(time.RFC3339), s.Published.UTC().Format(time.RFC3339)), nil)
	case published.Equal(s.Published) && sha != s.SHA256:
		return fail(ErrIndexReplayed, name, fmt.Sprintf("published %s with other bytes (%s, seen %s)",
			published.UTC().Format(time.RFC3339), sha, s.SHA256), nil)
	}
	return nil
}

// Commit checks the index and raises the watermark to it (never lowers it).
func (f IndexWatermarkFile) Commit(version string, published time.Time, sha string) error {
	seen, err := f.Load()
	if err != nil {
		return err
	}
	if err := checkSeen(seen, version, published, sha); err != nil {
		return err
	}
	if s, ok := seen[version]; ok && s.Published.Equal(published) && s.SHA256 == sha {
		return nil
	}
	seen[version] = IndexSeen{Published: published.UTC(), SHA256: sha}
	keys := make([]string, 0, len(seen))
	for k := range seen {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	var b bytes.Buffer
	b.WriteString("{\n  \"versions\": {")
	for i, k := range keys {
		vj, err := json.Marshal(seen[k])
		if err != nil {
			return err
		}
		kj, _ := json.Marshal(k)
		if i > 0 {
			b.WriteByte(',')
		}
		fmt.Fprintf(&b, "\n    %s: %s", kj, vj)
	}
	b.WriteString("\n  }\n}\n")
	if err := os.MkdirAll(filepath.Dir(f.Path), 0o755); err != nil {
		return err
	}
	return writeFileAtomic(f.Path, b.Bytes())
}

// CheckPackFiles checks a pack's release files in dir against the index entry: each
// file's size first (refused before it is read), then its sha256; for a split pack, the
// parts joined must have the whole pack's digest too.
func CheckPackFiles(p IndexPack, dir string) error {
	for _, f := range p.Files() {
		if !validFileName(f.Name) {
			return fail(ErrChecksum, f.Name, "bad file name", nil)
		}
		info, err := os.Stat(filepath.Join(dir, f.Name))
		if err != nil {
			return fail(ErrChecksum, f.Name, "read", err)
		}
		if info.Size() != f.Size {
			return fail(ErrChecksum, f.Name, fmt.Sprintf("%d bytes, the index lists %d", info.Size(), f.Size), nil)
		}
	}
	for _, f := range p.Files() {
		got, err := SHA256File(filepath.Join(dir, f.Name))
		if err != nil {
			return fail(ErrChecksum, f.Name, "read", err)
		}
		if got != f.SHA256 {
			return fail(ErrChecksum, f.Name, fmt.Sprintf("sha256 %s, the index lists %s", got, f.SHA256), nil)
		}
	}
	if len(p.Parts) > 0 {
		got, err := sha256Files(dir, p.Parts)
		if err != nil {
			return fail(ErrChecksum, p.Name, "read", err)
		}
		if got != p.SHA256 {
			return fail(ErrChecksum, p.Name, fmt.Sprintf("parts joined: sha256 %s, the index lists %s", got, p.SHA256), nil)
		}
	}
	return nil
}

// sha256Files is the digest of files in dir read one after the other.
func sha256Files(dir string, parts []IndexPart) (string, error) {
	h := sha256.New()
	for _, p := range parts {
		f, err := os.Open(filepath.Join(dir, p.Name))
		if err != nil {
			return "", err
		}
		_, err = io.Copy(h, f)
		_ = f.Close()
		if err != nil {
			return "", err
		}
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}
