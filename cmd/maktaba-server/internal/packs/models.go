package packs

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"github.com/Hamza-Labs-Core/Maktaba/shared/components"
	"github.com/Hamza-Labs-Core/Maktaba/shared/go/layout"
	rv "github.com/Hamza-Labs-Core/Maktaba/shared/go/releaseverify"
)

// hfSource splits "hf:<org>/<name>@<revision>".
func hfSource(src string) (repo, rev string, ok bool) {
	repo, rev, ok = strings.Cut(strings.TrimPrefix(src, "hf:"), "@")
	return repo, rev, ok && strings.HasPrefix(src, "hf:") && strings.Contains(repo, "/") && rev != ""
}

// hfRepoDir is a repo's folder in the Hugging Face hub cache: models--<org>--<name>.
func hfRepoDir(repo string) string { return "models--" + strings.ReplaceAll(repo, "/", "--") }

// SnapshotDir is where a model revision's files go in the hub cache, the folder every
// loader finds them in (huggingface_hub's own layout: plain files there are used as
// they are, as it does itself where it cannot make links).
func SnapshotDir(hub, repo, rev string) string {
	return filepath.Join(hub, hfRepoDir(repo), "snapshots", rev)
}

// modelRecord is rev_<revision12>/model.json.
type modelRecord struct {
	ID       string         `json:"id"`
	Repo     string         `json:"repo"`
	Revision string         `json:"revision"`
	Files    []rv.IndexFile `json:"files"`
}

// indexModel is the index's entry of id for this platform's source.
func (m *Manager) indexModel(v *rv.VerifiedIndex, id, src string) *rv.IndexModel {
	for i := range v.Index.Models {
		im := &v.Index.Models[i]
		if im.ID == id && im.Source == src && slices.Contains(im.Platforms, m.Platform) {
			return im
		}
	}
	return nil
}

// installModel downloads each of the pinned files the index lists into the hub cache's
// snapshot of the revision (a file already there with the right size and sha256 is
// kept), checks each against the index, and writes rev_<revision12>/model.json.
func (m *Manager) installModel(ctx context.Context, s *session, comp *components.Component, src string) error {
	id := comp.ID
	repo, rev, ok := hfSource(src)
	if !ok || m.HFHub == "" {
		return fail(ReasonInstallFailed, map[string]any{"source": src}, "no Hugging Face cache for %s", src)
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
	snap := SnapshotDir(m.HFHub, repo, rev)
	var total, need int64
	var todo []rv.IndexFile
	for _, f := range im.Files {
		if !safeRel(f.Path) {
			return fail(ReasonVerifyFailed, map[string]any{"name": f.Path}, "bad model path")
		}
		total += f.Size
		if fileMatches(filepath.Join(snap, filepath.FromSlash(f.Path)), f) {
			continue
		}
		todo = append(todo, f)
		need += f.Size
	}
	if err := m.room(m.HFHub, need); err != nil {
		return err
	}
	partial := filepath.Join(m.Layout.PartialDir(), id, short(rev))
	done := total - need
	for _, f := range todo {
		dst := filepath.Join(partial, filepath.FromSlash(f.Path))
		if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
			return wrap(ReasonInstallFailed, nil, err)
		}
		start := done
		err := s.src.Model(ctx, ModelFile{ID: id, Repo: repo, Rev: rev, Path: f.Path}, dst, f.Size, func(n int64) {
			m.step(id, DetailDownloading, f.Path, start+n, total)
		})
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
		target := filepath.Join(snap, filepath.FromSlash(f.Path))
		if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
			return wrap(ReasonInstallFailed, nil, err)
		}
		_ = os.Remove(target) // a wrong file, or a link to a blob that does not match
		if err := moveFile(dst, target); err != nil {
			return wrap(ReasonInstallFailed, nil, err)
		}
		done += f.Size
	}
	_ = os.RemoveAll(partial)
	// refs/main names the revision when nothing else does (a loader asking for no
	// revision finds this one offline)
	ref := filepath.Join(m.HFHub, hfRepoDir(repo), "refs", "main")
	if _, err := os.Stat(ref); errors.Is(err, os.ErrNotExist) {
		_ = layout.WriteFileAtomic(ref, []byte(rev), 0o644)
	}
	folder := RevFolder(rev)
	staging := filepath.Join(m.Layout.Dir(id), stagingPre+folder)
	_ = os.RemoveAll(staging)
	if err := os.MkdirAll(staging, 0o755); err != nil {
		return wrap(ReasonInstallFailed, nil, err)
	}
	b, _ := json.MarshalIndent(modelRecord{ID: id, Repo: repo, Revision: rev, Files: im.Files}, "", "  ")
	if err := os.WriteFile(filepath.Join(staging, ModelRecordFile), append(b, '\n'), 0o644); err != nil {
		return wrap(ReasonInstallFailed, nil, err)
	}
	if err := m.place(ctx, id, staging, folder); err != nil {
		return err
	}
	for _, f := range m.Layout.Folders(id) {
		if f != folder {
			_ = os.RemoveAll(filepath.Join(m.Layout.Dir(id), f))
		}
	}
	m.logf("%s installed from the %s: %s at %s", id, s.src.Kind(), repo, short(rev))
	return nil
}

// fileMatches: p is a regular file (or a link to one) of f's size and sha256.
func fileMatches(p string, f rv.IndexFile) bool {
	info, err := os.Stat(p)
	if err != nil || !info.Mode().IsRegular() || info.Size() != f.Size {
		return false
	}
	sum, err := layout.SHA256File(p)
	return err == nil && sum == f.SHA256
}

// moveFile renames from to to, or copies it when they are on different disks.
func moveFile(from, to string) error {
	if err := os.Rename(from, to); err == nil {
		return nil
	}
	in, err := os.Open(from)
	if err != nil {
		return err
	}
	defer func() { _ = in.Close() }()
	tmp := to + ".tmp"
	out, err := os.OpenFile(tmp, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o644)
	if err != nil {
		return err
	}
	if _, err := io.Copy(out, in); err != nil {
		_ = out.Close()
		_ = os.Remove(tmp)
		return err
	}
	if err := out.Close(); err != nil {
		_ = os.Remove(tmp)
		return err
	}
	if err := os.Rename(tmp, to); err != nil {
		return err
	}
	_ = os.Remove(from)
	return nil
}
