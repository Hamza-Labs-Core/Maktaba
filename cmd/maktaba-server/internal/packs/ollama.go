package packs

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"os"
	"path"
	"path/filepath"
	"strings"

	"github.com/Hamza-Labs-Core/Maktaba/shared/components"
	"github.com/Hamza-Labs-Core/Maktaba/shared/go/layout"
	rv "github.com/Hamza-Labs-Core/Maktaba/shared/go/releaseverify"
)

// Story 51.16: a language model (an "ollama:" source, model.llm.*) put into the bundled
// model server's store (<state>/models/ollama) without the server: its blobs from Ollama's
// registry, each checked against the catalog's pinned sha256 and size (the signed index),
// then its manifest, checked against the pinned digest. The store is Ollama's own layout
// (blobs/sha256-<hex>, manifests/registry.ollama.ai/<repo>/<tag>), so the server, started
// later, finds the model there.

// ollamaRegistry is the host of Ollama's model names (the manifests' folder).
const ollamaRegistry = "registry.ollama.ai"

// ollamaSource splits "ollama:<name>:<tag>@sha256:<hex>" ("ollama:qwen2.5:7b@sha256:...").
func ollamaSource(src string) (repo, tag, digest string, ok bool) {
	ref, digest, ok := strings.Cut(strings.TrimPrefix(src, "ollama:"), "@")
	if !ok || !strings.HasPrefix(src, "ollama:") || !isDigest(digest) {
		return "", "", "", false
	}
	name, tag, ok := strings.Cut(ref, ":")
	if !ok || name == "" || tag == "" || !safeRel(name) || !safeRel(tag) || strings.Contains(tag, "/") {
		return "", "", "", false
	}
	if !strings.Contains(name, "/") {
		name = "library/" + name
	}
	return name, tag, digest, true
}

func isDigest(d string) bool {
	h, ok := strings.CutPrefix(d, "sha256:")
	if !ok || len(h) != 64 {
		return false
	}
	_, err := hex.DecodeString(h)
	return err == nil && strings.ToLower(h) == h
}

// OllamaManifestPath is the manifest of repo:tag in the store models.
func OllamaManifestPath(models, repo, tag string) string {
	return filepath.Join(models, "manifests", ollamaRegistry, filepath.FromSlash(repo), tag)
}

// ollamaInstalled: the manifest of src is in the store and is the pinned one.
func (m *Manager) ollamaInstalled(src string) bool {
	repo, tag, digest, ok := ollamaSource(src)
	if !ok || m.OllamaModels == "" {
		return false
	}
	b, err := readSmall(OllamaManifestPath(m.OllamaModels, repo, tag))
	return err == nil && sumOf(b) == digest
}

func sumOf(b []byte) string {
	s := sha256.Sum256(b)
	return "sha256:" + hex.EncodeToString(s[:])
}

// OllamaManifests is a Source that also gives a model's manifest by its digest (the
// network: Ollama's registry; a folder: models/<id>/manifest).
type OllamaManifests interface {
	OllamaManifest(ctx context.Context, f ModelFile) ([]byte, error)
}

// ollamaManifest is what the manifest names: its config and layers, by digest.
type ollamaManifest struct {
	Config struct {
		Digest string `json:"digest"`
	} `json:"config"`
	Layers []struct {
		Digest string `json:"digest"`
	} `json:"layers"`
}

// blobPath is a blob's path in the store ("blobs/sha256-<hex>").
func blobPath(digest string) string { return "blobs/" + strings.Replace(digest, ":", "-", 1) }

// installOllama downloads the blobs the index pins into the store (one there already with
// the right size and sha256 is kept), then the manifest, which must be the pinned digest
// and name only those blobs; the manifest written last makes the model there.
func (m *Manager) installOllama(ctx context.Context, s *session, comp *components.Component, src string) error {
	id := comp.ID
	repo, tag, digest, ok := ollamaSource(src)
	if !ok || m.OllamaModels == "" {
		return fail(ReasonInstallFailed, map[string]any{"source": src}, "no model server store for %s", src)
	}
	v, err := m.index(ctx, id, s)
	if err != nil {
		return err
	}
	im := m.indexModel(v, id, src)
	if im == nil {
		return fail(ReasonPackMissing, map[string]any{"component": id, "platform": m.Platform},
			"the index lists no %s for %s", src, m.Platform)
	}
	pinned := map[string]rv.IndexFile{}
	var total, need int64
	var todo []rv.IndexFile
	for _, f := range im.Files {
		if !safeRel(f.Path) || path.Dir(f.Path) != "blobs" || blobPath("sha256:"+f.SHA256) != f.Path {
			return fail(ReasonVerifyFailed, map[string]any{"name": f.Path}, "bad model path")
		}
		pinned[f.Path] = f
		total += f.Size
		if fileMatches(filepath.Join(m.OllamaModels, filepath.FromSlash(f.Path)), f) {
			continue
		}
		todo = append(todo, f)
		need += f.Size
	}
	if err := m.room(m.OllamaModels, need); err != nil {
		return err
	}
	partial := filepath.Join(m.Layout.PartialDir(), id, short(strings.TrimPrefix(digest, "sha256:")))
	done := total - need
	for _, f := range todo {
		dst := filepath.Join(partial, filepath.FromSlash(f.Path))
		if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
			return wrap(ReasonInstallFailed, nil, err)
		}
		start := done
		err := s.src.Model(ctx, ModelFile{ID: id, Repo: repo, Rev: digest, Path: f.Path, Ollama: true}, dst, f.Size,
			func(n int64) { m.step(id, DetailDownloading, f.Path, start+n, total) })
		if err != nil {
			if ctx.Err() != nil {
				return ctx.Err()
			}
			return wrap(ReasonDownloadFailed, map[string]any{"name": f.Path}, err)
		}
		m.step(id, DetailVerifying, f.Path, start+f.Size, total)
		if !fileMatches(dst, f) {
			_ = os.Remove(dst)
			return fail(ReasonVerifyFailed, map[string]any{"name": f.Path}, "%s does not match the index", f.Path)
		}
		target := filepath.Join(m.OllamaModels, filepath.FromSlash(f.Path))
		if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
			return wrap(ReasonInstallFailed, nil, err)
		}
		_ = os.Remove(target)
		if err := moveFile(dst, target); err != nil {
			return wrap(ReasonInstallFailed, nil, err)
		}
		done += f.Size
	}
	_ = os.RemoveAll(partial)
	ms, ok := s.src.(OllamaManifests)
	if !ok {
		return fail(ReasonDownloadFailed, map[string]any{"name": "manifest"}, "the %s gives no model manifests", s.src.Kind())
	}
	b, err := ms.OllamaManifest(ctx, ModelFile{ID: id, Repo: repo, Rev: digest, Path: "manifest", Ollama: true})
	if err != nil {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		return wrap(ReasonDownloadFailed, map[string]any{"name": "manifest"}, err)
	}
	if got := sumOf(b); got != digest {
		return fail(ReasonVerifyFailed, map[string]any{"name": "manifest"}, "the manifest is %s, pinned %s", got, digest)
	}
	var man ollamaManifest
	if err := json.Unmarshal(b, &man); err != nil {
		return wrap(ReasonVerifyFailed, map[string]any{"name": "manifest"}, err)
	}
	digests := []string{man.Config.Digest}
	for _, l := range man.Layers {
		digests = append(digests, l.Digest)
	}
	for _, d := range digests {
		if _, ok := pinned[blobPath(d)]; !ok || !isDigest(d) {
			return fail(ReasonVerifyFailed, map[string]any{"name": "manifest"}, "the manifest names %s, which is not pinned", d)
		}
	}
	mp := OllamaManifestPath(m.OllamaModels, repo, tag)
	if err := os.MkdirAll(filepath.Dir(mp), 0o755); err != nil {
		return wrap(ReasonInstallFailed, nil, err)
	}
	if err := layout.WriteFileAtomic(mp, b, 0o644); err != nil {
		return wrap(ReasonInstallFailed, nil, err)
	}
	m.logf("%s installed from the %s: %s:%s at %s", id, s.src.Kind(), repo, tag, short(strings.TrimPrefix(digest, "sha256:")))
	return nil
}

// removeOllama deletes the model's manifest, then each of its blobs no other manifest in
// the store names (a blob two models share stays).
func (m *Manager) removeOllama(comp *components.Component, src string) error {
	repo, tag, _, ok := ollamaSource(src)
	if !ok || m.OllamaModels == "" {
		return nil
	}
	if err := os.Remove(OllamaManifestPath(m.OllamaModels, repo, tag)); err != nil && !errors.Is(err, os.ErrNotExist) {
		return wrap(ReasonBusy, map[string]any{"path": "manifest"}, err)
	}
	used := map[string]bool{}
	_ = filepath.WalkDir(filepath.Join(m.OllamaModels, "manifests"), func(p string, d os.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return nil
		}
		b, err := readSmall(p)
		if err != nil {
			return nil
		}
		var man ollamaManifest
		if json.Unmarshal(b, &man) == nil {
			used[man.Config.Digest] = true
			for _, l := range man.Layers {
				used[l.Digest] = true
			}
		}
		return nil
	})
	for _, f := range comp.FilesOf(src) {
		if !strings.HasPrefix(f.Path, "blobs/") || used["sha256:"+f.SHA256] || !safeRel(f.Path) {
			continue
		}
		if err := os.Remove(filepath.Join(m.OllamaModels, filepath.FromSlash(f.Path))); err != nil && !errors.Is(err, os.ErrNotExist) {
			return wrap(ReasonBusy, map[string]any{"path": f.Path}, err)
		}
	}
	return nil
}

// ollamaURL is a model file's address in Ollama's registry: a blob by its digest, the
// manifest by the pinned one.
func (n *Net) ollamaURL(f ModelFile) string {
	base := strings.TrimRight(n.OllamaRegistry, "/")
	if base == "" {
		base = "https://" + ollamaRegistry
	}
	if f.Path == "manifest" {
		return base + "/v2/" + f.Repo + "/manifests/" + f.Rev
	}
	return base + "/v2/" + f.Repo + "/blobs/" + strings.Replace(path.Base(f.Path), "-", ":", 1)
}

// OllamaManifest fetches the manifest from Ollama's registry (the Docker registry API's
// v2 manifest).
func (n *Net) OllamaManifest(ctx context.Context, f ModelFile) ([]byte, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, n.ollamaURL(f), nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Accept", "application/vnd.docker.distribution.manifest.v2+json")
	return n.fetchSmall(req)
}

// OllamaManifest reads models/<id>/manifest of the folder.
func (f *FolderSource) OllamaManifest(_ context.Context, m ModelFile) ([]byte, error) {
	if !safeRel(m.ID) {
		return nil, fmt.Errorf("bad model id %q", m.ID)
	}
	b, err := readSmall(filepath.Join(f.Dir, "models", m.ID, "manifest"))
	if errors.Is(err, os.ErrNotExist) {
		return nil, errNotFound
	}
	return b, err
}
