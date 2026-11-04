// Package componentsindex writes a release's signed component index,
// components-<version>.json (Epic 51, story 51.15): every pack the release built, read
// back from the files in dist/, and every pinned model from the catalog.
//
// The index is signed with the release: it and every pack go into dist/ before
// checksums.txt is written, and release.json names it with its digest.
package componentsindex

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"sort"
	"strings"
	"time"

	"github.com/Hamza-Labs-Core/Maktaba/shared/components"
	"github.com/Hamza-Labs-Core/Maktaba/shared/go/componentpack"
	rv "github.com/Hamza-Labs-Core/Maktaba/shared/go/releaseverify"
)

// Pack is one pack a release builds.
type Pack struct {
	ID       string
	Platform string
	Requires []string
}

// Name is its file name for version.
func (p Pack) Name(version string) string {
	return componentpack.FileName(p.ID, version, p.Platform)
}

// Packs are the packs the catalog asks of a release on platform ("" every platform): an
// asset that is not in the payload, whose source there is its own pack. Each has a recipe
// in tools/release/packs/recipes.
func Packs(cat *components.Catalog, platform string) []Pack {
	var out []Pack
	for _, c := range cat.Components() {
		if c.Kind != components.KindAsset || c.InPayload {
			continue
		}
		for _, p := range c.Platforms {
			if (platform != "" && p != platform) || c.SourceFor(p) != "pack:"+c.ID {
				continue
			}
			var req []string
			for _, r := range cat.Requires(c.ID) {
				if len(r.AnyOf) == 1 {
					req = append(req, r.AnyOf[0])
				}
			}
			out = append(out, Pack{ID: c.ID, Platform: p, Requires: nonNil(req)})
		}
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].ID != out[j].ID {
			return out[i].ID < out[j].ID
		}
		return out[i].Platform < out[j].Platform
	})
	return out
}

// Models are the catalog's pinned model sources: one entry per (component, source), with
// the platforms that use that source.
func Models(cat *components.Catalog) []rv.IndexModel {
	var out []rv.IndexModel
	for _, c := range cat.Components() {
		if c.Kind != components.KindAsset {
			continue
		}
		bySource := map[string][]string{}
		for _, p := range c.Platforms {
			if s := c.SourceFor(p); components.IsModelSource(s) {
				bySource[s] = append(bySource[s], p)
			}
		}
		for _, s := range sortedKeys(bySource) {
			m := rv.IndexModel{ID: c.ID, Source: s, Platforms: bySource[s], Gated: c.Gated}
			for _, f := range c.FilesOf(s) {
				m.Files = append(m.Files, rv.IndexFile{Path: f.Path, SHA256: f.SHA256, Size: f.Size})
			}
			out = append(out, m)
		}
	}
	return out
}

// Options say what to index.
type Options struct {
	Catalog   *components.Catalog
	Version   string    // without "v"
	Commit    string    // the 40-hex commit the release is built from
	Published time.Time // the commit's time: a re-run writes the same index
	Dist      string    // the folder with the release files (the packs among them)
	// Platform and Only narrow the index to the packs of one platform ("" every platform)
	// and to the listed ids (none: all of them): a build of part of a release (one
	// platform's packs in CI, the update rehearsal's tool.yt-dlp). A release sets neither.
	Platform string
	Only     []string
}

// ErrMissing is the error of an index whose packs are not all in dist.
var ErrMissing = errors.New("components index: packs missing")

var packFileRe = regexp.MustCompile(`\.tar\.zst(\.part[0-9]+)?$`)

// Generate is the index. Every pack the catalog asks for (of Platform and Only, when set)
// must be in dist: a missing one (a failed build leg) is ErrMissing, naming each, and no
// index is made, so a release never ships without some platform's packs. A pack file in
// dist the catalog does not ask for, or one whose pack.json is not what its name says, is
// an error too.
func Generate(o Options) (*rv.ComponentsIndex, error) {
	idx := &rv.ComponentsIndex{
		Schema: rv.ComponentsIndexSchema, Version: o.Version, Commit: strings.ToLower(o.Commit),
		Published: o.Published.UTC().Truncate(time.Second), Server: rv.ServerRange(o.Version),
		Packs: []rv.IndexPack{}, Models: Models(o.Catalog),
	}
	idx.Expires = idx.Published.Add(rv.IndexLifetime)
	want, err := wanted(o)
	if err != nil {
		return nil, err
	}
	names := map[string]bool{}
	var missing []string
	for _, p := range want {
		name := p.Name(o.Version)
		names[name] = true
		ip, err := readPack(o.Dist, name, p, o.Version)
		if errors.Is(err, os.ErrNotExist) {
			missing = append(missing, name)
			continue
		}
		if err != nil {
			return nil, err
		}
		idx.Packs = append(idx.Packs, *ip)
	}
	if len(missing) > 0 {
		return nil, fmt.Errorf("%w from %s: %s", ErrMissing, o.Dist, strings.Join(missing, ", "))
	}
	entries, err := os.ReadDir(o.Dist)
	if err != nil {
		return nil, err
	}
	for _, e := range entries {
		if !packFileRe.MatchString(e.Name()) {
			continue
		}
		base := regexp.MustCompile(`\.part[0-9]+$`).ReplaceAllString(e.Name(), "")
		if !names[base] {
			return nil, fmt.Errorf("components index: %s is not a pack the catalog asks for", e.Name())
		}
	}
	// the index checks itself as a machine will
	b, err := Marshal(idx)
	if err != nil {
		return nil, err
	}
	if _, err := rv.ParseComponentsIndex(b); err != nil {
		return nil, err
	}
	return idx, nil
}

// wanted are the packs o asks for: the catalog's, narrowed by Platform and Only. An Only
// id that is no pack of the platform is an error, not an empty index.
func wanted(o Options) ([]Pack, error) {
	all := Packs(o.Catalog, o.Platform)
	if len(o.Only) == 0 {
		return all, nil
	}
	var out []Pack
	for _, id := range o.Only {
		n := len(out)
		for _, p := range all {
			if p.ID == id {
				out = append(out, p)
			}
		}
		if len(out) == n {
			where := o.Platform
			if where == "" {
				where = "any platform"
			}
			return nil, fmt.Errorf("components index: %s is not a pack of %s", id, where)
		}
	}
	return out, nil
}

func readPack(dist, name string, p Pack, version string) (*rv.IndexPack, error) {
	r, parts, closeAll, err := componentpack.Open(dist, name)
	if err != nil {
		return nil, err
	}
	defer func() { _ = closeAll() }()
	h := sha256.New()
	cr := &countReader{r: io.TeeReader(r, h)}
	man, _, err := componentpack.ReadHead(cr)
	if err != nil {
		return nil, fmt.Errorf("%s: %w", name, err)
	}
	if man.ID != p.ID || man.Version != version || man.Platform != p.Platform {
		return nil, fmt.Errorf("%s: pack.json says %s %s %s", name, man.ID, man.Version, man.Platform)
	}
	// the rest of the file, for the whole digest and size
	if _, err := io.Copy(io.Discard, cr); err != nil {
		return nil, err
	}
	ip := &rv.IndexPack{ID: p.ID, Platform: p.Platform, Name: name, SHA256: hex.EncodeToString(h.Sum(nil)),
		Size: cr.n, UnpackedBytes: man.UnpackedBytes, FilesSHA256: man.FilesSHA256, Requires: p.Requires}
	for _, pn := range parts {
		sum, err := rv.SHA256File(filepath.Join(dist, pn))
		if err != nil {
			return nil, err
		}
		info, err := os.Stat(filepath.Join(dist, pn))
		if err != nil {
			return nil, err
		}
		ip.Parts = append(ip.Parts, rv.IndexPart{Name: pn, SHA256: sum, Size: info.Size()})
	}
	return ip, nil
}

type countReader struct {
	r io.Reader
	n int64
}

func (c *countReader) Read(p []byte) (int, error) {
	n, err := c.r.Read(p)
	c.n += int64(n)
	return n, err
}

// Marshal is the index as it is published: indented, with a final newline.
func Marshal(idx *rv.ComponentsIndex) ([]byte, error) {
	b, err := json.MarshalIndent(idx, "", "  ")
	if err != nil {
		return nil, err
	}
	return append(b, '\n'), nil
}

// CheckDist checks that every pack and part an index lists is in dist with its size and
// digest (gen-release-json --check, story 51.15 T10).
func CheckDist(idx *rv.ComponentsIndex, dist string) error {
	var errs []string
	for _, p := range idx.Packs {
		if err := rv.CheckPackFiles(p, dist); err != nil {
			errs = append(errs, err.Error())
		}
	}
	if len(errs) > 0 {
		return fmt.Errorf("components index: %s", strings.Join(errs, "; "))
	}
	return nil
}

func sortedKeys[V any](m map[string]V) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	slices.Sort(out)
	return out
}

func nonNil(s []string) []string {
	if s == nil {
		return []string{}
	}
	return s
}
