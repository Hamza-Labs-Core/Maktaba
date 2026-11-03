package packs

import (
	"archive/tar"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"github.com/Hamza-Labs-Core/Maktaba/shared/components"
	rv "github.com/Hamza-Labs-Core/Maktaba/shared/go/releaseverify"
)

// The offline file (story 51.16 §6): an uncompressed tar (the packs are compressed
// already) of what a set of components needs, carried to a home with no network:
//
//	offline.json                  {"schema":1,"version","platform","bundle","items":[ids]}
//	checksums.txt  checksums.txt.sigstore.json  release.json  components-<version>.json
//	packs/<name>                  each release file of each pack
//	models/<id>/<path>            each model file exactly as the index lists it
//
// Import checks it as an install from the network is checked: the signed files, the
// index, then every pack and model file against the index.

// OfflineName is the file's name for version, platform and bundle ("custom" for a list).
func OfflineName(version, platform, bundle string) string {
	if bundle == "" {
		bundle = "custom"
	}
	return "maktaba-offline-" + version + "-" + platform + "-" + bundle + ".tar"
}

// OfflineManifest is offline.json.
type OfflineManifest struct {
	Schema   int      `json:"schema"`
	Version  string   `json:"version"`
	Platform string   `json:"platform"`
	Bundle   string   `json:"bundle,omitempty"`
	Items    []string `json:"items"`
}

// OfflineManifestName is offline.json's name in the file.
const OfflineManifestName = "offline.json"

// OfflineRequest says what the file holds: a bundle's assets, or the assets the listed
// components need (an asset listed is itself), for a platform ("" this machine's).
type OfflineRequest struct {
	Bundle     string   `json:"bundle,omitempty"`
	Components []string `json:"components,omitempty"`
	Platform   string   `json:"platform,omitempty"`
}

// OfflineItems are the asset ids a request carries, in the catalog's order
// (requirements first): each asset with a pack or a Hugging Face model on the platform.
func (m *Manager) OfflineItems(req OfflineRequest) ([]string, error) {
	plat := req.Platform
	if plat == "" {
		plat = m.Platform
	}
	// the platform names the offline file on disk: only a known one
	if !slices.Contains(components.Platforms, plat) {
		return nil, fail(ReasonOfflineWrongPlat, map[string]any{"platform": plat}, "unknown platform %q", plat)
	}
	want := map[string]bool{}
	var add func(id string)
	add = func(id string) {
		c, ok := m.Catalog.Get(id)
		if !ok || want[id] {
			return
		}
		if c.Kind == components.KindAsset {
			want[id] = true
		}
		for _, r := range m.Catalog.Requires(id) {
			add(r.AnyOf[0])
		}
	}
	switch {
	case req.Bundle != "" && len(req.Components) > 0:
		return nil, fail(ReasonInstallFailed, nil, "a bundle or a list of components, not both")
	case req.Bundle != "":
		ids := m.Catalog.InBundle(req.Bundle)
		if len(ids) == 0 {
			return nil, fail(ReasonInstallFailed, map[string]any{"bundle": req.Bundle}, "unknown bundle %q", req.Bundle)
		}
		for _, id := range ids {
			add(id)
		}
	default:
		for _, id := range req.Components {
			if _, ok := m.Catalog.Get(id); !ok {
				return nil, fail(ReasonInstallFailed, map[string]any{"component": id}, "unknown component %q", id)
			}
			add(id)
		}
	}
	var out []string
	for _, id := range m.Catalog.Order() {
		if !want[id] {
			continue
		}
		c, _ := m.Catalog.Get(id)
		if len(c.Platforms) > 0 && !slices.Contains(c.Platforms, plat) {
			continue
		}
		src := c.SourceFor(plat)
		if strings.HasPrefix(src, "pack:") || strings.HasPrefix(src, "hf:") {
			out = append(out, id)
		}
	}
	return out, nil
}

// offlineFile is one file of the offline tar and where its bytes are.
type offlineFile struct {
	name string // in the tar
	path string // on disk ("": data)
	data []byte
	size int64
}

// MakeOffline writes the offline file of req to w: it fetches (from the network) and
// verifies every pack and model file first, then writes the tar. A gated model this home
// cannot download is left out, and said in the log. It returns the ids it carries.
func (m *Manager) MakeOffline(ctx context.Context, w io.Writer, req OfflineRequest) (items []string, err error) {
	const key = "offline"
	if err := m.begin(key, components.PhaseInstalling); err != nil {
		return nil, err
	}
	defer func() { m.end(key, err) }()
	plat := req.Platform
	if plat == "" {
		plat = m.Platform
	}
	ids, err := m.OfflineItems(req)
	if err != nil {
		return nil, err
	}
	if m.Net == nil {
		return nil, fail(ReasonDownloadFailed, nil, "no network source")
	}
	s := &session{src: m.Net}
	v, err := m.index(ctx, key, s)
	if err != nil {
		return nil, err
	}
	signed := s.signed
	files := []offlineFile{
		{name: rv.ChecksumsFile, data: signed.Checksums},
		{name: rv.BundleFile, data: signed.Bundle},
		{name: rv.ManifestFile, data: signed.Manifest},
		{name: rv.ComponentsIndexName(m.Version), data: signed.Index},
	}
	work := filepath.Join(m.Layout.PartialDir(), ".offline")
	defer func() { _ = os.RemoveAll(work) }()
	var total, done int64
	type job struct {
		id   string
		pack *rv.IndexPack
		mod  *rv.IndexModel
		repo string
		rev  string
	}
	var jobs []job
	for _, id := range ids {
		c, _ := m.Catalog.Get(id)
		src := c.SourceFor(plat)
		switch {
		case strings.HasPrefix(src, "pack:"):
			var p *rv.IndexPack
			for i := range v.Index.Packs {
				if v.Index.Packs[i].ID == id && v.Index.Packs[i].Platform == plat {
					p = &v.Index.Packs[i]
				}
			}
			if p == nil {
				m.logf("level=warn offline file: the index lists no %s pack for %s: left out", id, plat)
				continue
			}
			jobs = append(jobs, job{id: id, pack: p})
			total += p.Size
		case strings.HasPrefix(src, "hf:"):
			repo, rev, _ := hfSource(src)
			var im *rv.IndexModel
			for i := range v.Index.Models {
				if x := &v.Index.Models[i]; x.ID == id && x.Source == src && slices.Contains(x.Platforms, plat) {
					im = x
				}
			}
			if im == nil {
				m.logf("level=warn offline file: the index lists no %s for %s: left out", src, plat)
				continue
			}
			jobs = append(jobs, job{id: id, mod: im, repo: repo, rev: rev})
			for _, f := range im.Files {
				total += f.Size
			}
		}
	}
	if err := m.room(m.Layout.Root, total); err != nil {
		return nil, err
	}
	for _, j := range jobs {
		switch {
		case j.pack != nil:
			dir := filepath.Join(work, "packs")
			if err := os.MkdirAll(dir, 0o755); err != nil {
				return nil, wrap(ReasonInstallFailed, nil, err)
			}
			for _, f := range j.pack.Files() {
				dst := filepath.Join(dir, f.Name)
				start := done
				if err := s.src.Pack(ctx, m.Version, f.Name, dst, f.Size, func(n int64) {
					m.step(key, DetailOffline, f.Name, start+n, total)
				}); err != nil {
					return nil, wrap(ReasonDownloadFailed, map[string]any{"name": f.Name}, err)
				}
				done += f.Size
			}
			if err := rv.CheckPackFiles(*j.pack, dir); err != nil {
				return nil, wrap(ReasonVerifyFailed, map[string]any{"name": j.pack.Name}, err)
			}
			for _, f := range j.pack.Files() {
				files = append(files, offlineFile{name: "packs/" + f.Name, path: filepath.Join(dir, f.Name), size: f.Size})
			}
		case j.mod != nil:
			var mine []offlineFile
			skipped := false
			for _, f := range j.mod.Files {
				if !safeRel(f.Path) {
					return nil, fail(ReasonVerifyFailed, map[string]any{"name": f.Path}, "bad model path")
				}
				p := filepath.Join(SnapshotDir(m.HFHub, j.repo, j.rev), filepath.FromSlash(f.Path))
				if m.HFHub == "" || !fileMatches(p, f) {
					p = filepath.Join(work, "models", j.id, filepath.FromSlash(f.Path))
					if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
						return nil, wrap(ReasonInstallFailed, nil, err)
					}
					start := done
					err := s.src.Model(ctx, ModelFile{ID: j.id, Repo: j.repo, Rev: j.rev, Path: f.Path}, p, f.Size,
						func(n int64) { m.step(key, DetailOffline, f.Path, start+n, total) })
					if err != nil && j.mod.Gated {
						m.logf("level=warn offline file: %s is gated and this home cannot download it (%v): left out", j.id, err)
						skipped = true
						break
					}
					if err != nil {
						return nil, wrap(ReasonDownloadFailed, map[string]any{"name": f.Path}, err)
					}
					if !fileMatches(p, f) {
						return nil, fail(ReasonVerifyFailed, map[string]any{"name": f.Path}, "%s does not match the index", f.Path)
					}
				}
				done += f.Size
				mine = append(mine, offlineFile{name: "models/" + j.id + "/" + f.Path, path: p, size: f.Size})
			}
			if skipped {
				continue
			}
			files = append(files, mine...)
		}
		items = append(items, j.id)
	}
	man, _ := json.MarshalIndent(OfflineManifest{Schema: 1, Version: m.Version, Platform: plat, Bundle: req.Bundle,
		Items: items}, "", "  ")
	files = append([]offlineFile{{name: OfflineManifestName, data: append(man, '\n')}}, files...)
	if err := writeTar(ctx, w, files, m.now()); err != nil {
		return nil, wrap(ReasonInstallFailed, nil, err)
	}
	m.logf("offline file for %s %s written: %s", m.Version, plat, strings.Join(items, ", "))
	return items, nil
}

func writeTar(ctx context.Context, w io.Writer, files []offlineFile, at time.Time) error {
	tw := tar.NewWriter(w)
	for _, f := range files {
		if err := ctx.Err(); err != nil {
			return err
		}
		size := f.size
		if f.path == "" {
			size = int64(len(f.data))
		}
		if err := tw.WriteHeader(&tar.Header{Name: f.name, Typeflag: tar.TypeReg, Mode: 0o644, Size: size,
			ModTime: at.UTC().Truncate(time.Second), Format: tar.FormatPAX}); err != nil {
			return err
		}
		if f.path == "" {
			if _, err := tw.Write(f.data); err != nil {
				return err
			}
			continue
		}
		in, err := os.Open(f.path)
		if err != nil {
			return err
		}
		_, err = io.Copy(tw, io.LimitReader(in, size))
		_ = in.Close()
		if err != nil {
			return err
		}
	}
	return tw.Close()
}

// Import installs every item of an offline file (a .tar, or a folder holding its
// contents) as an install from the network is done, from the file. A file for another
// version or platform is refused before anything else is read. It returns the ids
// installed. p is named absolutely, with no ".." (the CLI and the package's folder make
// it so): anything else is refused before it is opened.
func (m *Manager) Import(ctx context.Context, p string) ([]string, error) {
	if !filepath.IsAbs(p) {
		return nil, wrap(ReasonInstallFailed, nil, fmt.Errorf("%q: not an absolute path", p))
	}
	if strings.Contains(p, "..") {
		return nil, wrap(ReasonInstallFailed, nil, fmt.Errorf("%q: a path with a .. element in it", p))
	}
	if info, err := os.Stat(p); err == nil && info.IsDir() {
		return m.importDir(ctx, p)
	}
	f, err := os.Open(p)
	if err != nil {
		return nil, wrap(ReasonInstallFailed, nil, err)
	}
	defer func() { _ = f.Close() }()
	return m.ImportFrom(ctx, f)
}

// ImportFrom is Import of an offline tar read from r (an upload, streamed): it is
// written out under the components folder, then installed from there.
func (m *Manager) ImportFrom(ctx context.Context, r io.Reader) (ids []string, err error) {
	const key = "import"
	if err := m.begin(key, components.PhaseInstalling); err != nil {
		return nil, err
	}
	defer func() { m.end(key, err) }()
	if m.Disabled {
		return nil, fail(ReasonPacksDisabled, nil, "[components] packs = \"off\"")
	}
	dir := filepath.Join(m.Layout.PartialDir(), ".import")
	_ = os.RemoveAll(dir)
	defer func() { _ = os.RemoveAll(dir) }()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil, wrap(ReasonInstallFailed, nil, err)
	}
	tr := tar.NewReader(r)
	first := true
	var done int64
	for {
		h, err := tr.Next()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return nil, fail(ReasonUnpackRefused, map[string]any{"entry": "tar"}, "read: %v", err)
		}
		if h.Typeflag == tar.TypeDir {
			continue
		}
		name := h.Name
		if h.Typeflag != tar.TypeReg || strings.Contains(name, "..") || !offlineEntry(name) {
			return nil, fail(ReasonUnpackRefused, map[string]any{"entry": name}, "%s does not belong in an offline file", name)
		}
		if first != (name == OfflineManifestName) {
			return nil, fail(ReasonUnpackRefused, map[string]any{"entry": name}, "the file does not start with %s", OfflineManifestName)
		}
		dst := filepath.Join(dir, filepath.FromSlash(name))
		if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
			return nil, wrap(ReasonInstallFailed, nil, err)
		}
		out, err := os.OpenFile(dst, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o644)
		if err != nil {
			return nil, fail(ReasonUnpackRefused, map[string]any{"entry": name}, "%s: %v", name, err)
		}
		start := done
		_, err = copyProgress(ctx, out, tr, 0, func(n int64) { m.step(key, DetailImporting, name, start+n, 0) })
		if cerr := out.Close(); err == nil {
			err = cerr
		}
		if err != nil {
			return nil, wrap(ReasonInstallFailed, nil, err)
		}
		done += h.Size
		if first {
			first = false
			if err := m.checkOffline(dst); err != nil {
				return nil, err
			}
		}
	}
	if first {
		return nil, fail(ReasonUnpackRefused, map[string]any{"entry": OfflineManifestName}, "no %s", OfflineManifestName)
	}
	return m.installFrom(ctx, dir)
}

func (m *Manager) importDir(ctx context.Context, dir string) ([]string, error) {
	if m.Disabled {
		return nil, fail(ReasonPacksDisabled, nil, "[components] packs = \"off\"")
	}
	if err := m.checkOffline(filepath.Join(dir, OfflineManifestName)); err != nil {
		return nil, err
	}
	return m.installFrom(ctx, dir)
}

// checkOffline refuses an offline.json of another version or platform.
func (m *Manager) checkOffline(p string) error {
	b, err := readSmall(p)
	if err != nil {
		return wrap(ReasonUnpackRefused, map[string]any{"entry": OfflineManifestName}, err)
	}
	var man OfflineManifest
	if err := json.Unmarshal(b, &man); err != nil || man.Schema != 1 {
		return fail(ReasonUnpackRefused, map[string]any{"entry": OfflineManifestName}, "unreadable %s", OfflineManifestName)
	}
	if man.Version != m.Version {
		return fail(ReasonOfflineWrongVersion, map[string]any{"version": man.Version},
			"the file is for %s, this server runs %s", man.Version, m.Version)
	}
	if man.Platform != m.Platform {
		return fail(ReasonOfflineWrongPlat, map[string]any{"platform": man.Platform},
			"the file is for %s, this machine is %s", man.Platform, m.Platform)
	}
	return nil
}

// installFrom installs offline.json's items from dir, in order.
func (m *Manager) installFrom(ctx context.Context, dir string) ([]string, error) {
	b, err := readSmall(filepath.Join(dir, OfflineManifestName))
	if err != nil {
		return nil, wrap(ReasonInstallFailed, nil, err)
	}
	var man OfflineManifest
	if err := json.Unmarshal(b, &man); err != nil {
		return nil, wrap(ReasonInstallFailed, nil, err)
	}
	s := &session{src: &FolderSource{Dir: dir}}
	var done []string
	for _, id := range m.Catalog.Order() {
		if !slices.Contains(man.Items, id) {
			continue
		}
		if err := m.install(ctx, id, s, map[string]bool{}); err != nil {
			return done, fmt.Errorf("%s: %w", id, err)
		}
		done = append(done, id)
	}
	return done, nil
}

// offlineEntry is a name an offline file may hold.
func offlineEntry(name string) bool {
	switch name {
	case OfflineManifestName, rv.ChecksumsFile, rv.BundleFile, rv.ManifestFile:
		return true
	}
	if strings.HasPrefix(name, "components-") && strings.HasSuffix(name, ".json") && !strings.ContainsAny(name, "/\\") {
		return true
	}
	if rest, ok := strings.CutPrefix(name, "packs/"); ok {
		return safeRel(rest) && !strings.Contains(rest, "/")
	}
	if rest, ok := strings.CutPrefix(name, "models/"); ok {
		id, p, ok := strings.Cut(rest, "/")
		return ok && safeRel(id) && safeRel(p) && path.Clean(p) == p
	}
	return false
}
