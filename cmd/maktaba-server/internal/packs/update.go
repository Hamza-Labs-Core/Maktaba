package packs

// Story 51.18: an update carries the packs. A first-party pack fits only the server
// version it was built with, so an update fetches and verifies, before its switch, the
// next release's pack of every component installed from one, into a folder of its own
// beside the running release's (<id>/<next version>_<sha12>/). current is not touched:
// the running release keeps reading its own folder, and the next one points current at
// its own at boot (Layout.Recover). A rollback goes back to the folders it kept; a commit
// prunes the folders of releases no longer kept.
//
// A model is pinned by the catalog, not by the version: it is fetched only when the next
// release's index pins another revision, and the revision before stays. Each rev_ folder
// lists the releases whose index pins it (its versions file), so a prune knows which
// revisions a kept release still needs.

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"github.com/Hamza-Labs-Core/Maktaba/shared/go/componentpack"
	"github.com/Hamza-Labs-Core/Maktaba/shared/go/layout"
	rv "github.com/Hamza-Labs-Core/Maktaba/shared/go/releaseverify"
)

// Kinds of what a component holds.
const (
	HeldPack  = "pack"
	HeldModel = "model"
)

// Held is a component installed from a pack or a pinned model: the folder current names.
type Held struct {
	ID     string
	Folder string
	Kind   string // HeldPack or HeldModel
	// Repo and Rev are a model's.
	Repo, Rev string
}

// Held are the components installed here from a pack (current names a
// <version>_<sha12> folder: the unpacker writes the pack's files only, not its pack.json)
// or a pinned model (a rev_ folder with model.json). What came with the payload, or was
// on disk before packs, is not here: it needs nothing from an update.
func (l Layout) Held() []Held {
	var out []Held
	for _, id := range l.IDs() {
		cur := l.Current(id)
		if cur == "" || l.Removing(id) {
			continue
		}
		dir := filepath.Join(l.Dir(id), cur)
		if strings.HasPrefix(cur, revPre) {
			if rec, err := readModelRecord(dir); err == nil {
				out = append(out, Held{ID: id, Folder: cur, Kind: HeldModel, Repo: rec.Repo, Rev: rec.Revision})
			}
			continue
		}
		if packFolderVersion(cur) != "" {
			out = append(out, Held{ID: id, Folder: cur, Kind: HeldPack})
		}
	}
	return out
}

// packFolderVersion is the version of a pack folder's name ("" when it is not one).
func packFolderVersion(name string) string {
	v, sum, ok := strings.Cut(name, "_")
	if !ok || !layout.ValidVersion(v) || sum == "" || strings.Trim(sum, "0123456789abcdef") != "" {
		return ""
	}
	return v
}

func readModelRecord(dir string) (modelRecord, error) {
	var rec modelRecord
	b, err := os.ReadFile(filepath.Join(dir, ModelRecordFile))
	if err != nil {
		return rec, err
	}
	if err := json.Unmarshal(b, &rec); err != nil {
		return rec, err
	}
	if rec.Repo == "" || rec.Revision == "" {
		return rec, errors.New("model.json names no revision")
	}
	return rec, nil
}

// Staged is one folder an update's packs step made ready (or found ready) for the next
// release: the journal's record of it.
type Staged struct {
	ID      string `json:"id"`
	Version string `json:"version"`
	// Dir is the folder's name in the component's folder.
	Dir  string `json:"dir"`
	Kind string `json:"kind"`
	// From is the folder the running release reads (a rollback finds it again).
	From string `json:"from,omitempty"`
	// Created: this run made Dir (a run that ends before its switch deletes it).
	Created bool `json:"created,omitempty"`
}

// UpdatePlan is what an update to Version needs of the packs: the verified index's entry
// of every component held here, and the bytes the new files take on disk.
type UpdatePlan struct {
	Version string
	Held    []Held
	// Need is the bytes to download and unpack (0 when every folder is there already).
	Need int64

	packs  map[string]rv.IndexPack
	models map[string]rv.IndexModel // the models whose revision changes
}

// PlanUpdate reads what is held here and, when anything is, verifies version's index
// from src and finds each one's entry in it. Nothing held: an empty plan, and nothing is
// fetched. A release with no index while something is held is index_missing; a held
// component the index does not list for this platform is pack_missing.
func (m *Manager) PlanUpdate(ctx context.Context, version string, src Source) (*UpdatePlan, error) {
	plan := &UpdatePlan{Version: version, Held: m.Layout.Held(), packs: map[string]rv.IndexPack{},
		models: map[string]rv.IndexModel{}}
	if len(plan.Held) == 0 {
		return plan, nil
	}
	if m.Disabled {
		return nil, fail(ReasonPacksDisabled, nil, "[components] packs = \"off\", and %d components are installed from packs",
			len(plan.Held))
	}
	if src == nil {
		return nil, fail(ReasonDownloadFailed, nil, "no source to fetch %s's packs from", version)
	}
	v, _, err := m.VerifyIndex(ctx, version, src)
	if err != nil {
		return nil, err
	}
	for _, h := range plan.Held {
		switch h.Kind {
		case HeldPack:
			var p *rv.IndexPack
			for i := range v.Index.Packs {
				if v.Index.Packs[i].ID == h.ID && v.Index.Packs[i].Platform == m.Platform {
					p = &v.Index.Packs[i]
				}
			}
			if p == nil {
				return nil, fail(ReasonPackMissing, map[string]any{"component": h.ID, "platform": m.Platform},
					"%s's index lists no %s pack for %s", version, h.ID, m.Platform)
			}
			plan.packs[h.ID] = *p
			if !isDir(filepath.Join(m.Layout.Dir(h.ID), PackFolder(version, p.SHA256))) {
				plan.Need += p.Size + p.UnpackedBytes
			}
		case HeldModel:
			im := m.indexModelOf(v, h.ID)
			if im == nil {
				return nil, fail(ReasonPackMissing, map[string]any{"component": h.ID, "platform": m.Platform},
					"%s's index lists no %s for %s", version, h.ID, m.Platform)
			}
			repo, rev, ok := hfSource(im.Source)
			if !ok {
				return nil, fail(ReasonVerifyFailed, map[string]any{"source": im.Source}, "bad model source")
			}
			if rev == h.Rev && repo == h.Repo {
				continue
			}
			plan.models[h.ID] = *im
			snap := SnapshotDir(m.HFHub, repo, rev)
			for _, f := range im.Files {
				if safeRel(f.Path) && !fileMatches(filepath.Join(snap, filepath.FromSlash(f.Path)), f) {
					plan.Need += f.Size
				}
			}
		}
	}
	return plan, nil
}

// indexModelOf is the index's entry of model id for this platform, whatever revision it pins.
func (m *Manager) indexModelOf(v *rv.VerifiedIndex, id string) *rv.IndexModel {
	for i := range v.Index.Models {
		im := &v.Index.Models[i]
		if im.ID == id && strings.HasPrefix(im.Source, "hf:") && slices.Contains(im.Platforms, m.Platform) {
			return im
		}
	}
	return nil
}

func isDir(p string) bool {
	info, err := os.Stat(p)
	return err == nil && info.IsDir()
}

// StageUpdate fetches, verifies and unpacks the plan's packs and models for its version,
// all or nothing: on any failure every folder it made is deleted and the error (an
// *Error with its reason) returned. Each component's lock is held while it is staged, so
// an install of it under way here is waited for until ctx ends (the caller bounds it).
// Nothing is prepared: the next release's venv is built at its first start.
func (m *Manager) StageUpdate(ctx context.Context, plan *UpdatePlan, src Source) (staged []Staged, err error) {
	defer func() {
		if err != nil {
			m.DiscardStaged(staged)
			staged = nil
		}
	}()
	for _, h := range plan.Held {
		s, err := m.stageOne(ctx, plan, h, src)
		if s != nil {
			staged = append(staged, *s)
		}
		if err != nil {
			if ctx.Err() != nil {
				return staged, wrap(ReasonBusy, map[string]any{"component": h.ID}, ctx.Err())
			}
			return staged, err
		}
	}
	return staged, nil
}

func (m *Manager) stageOne(ctx context.Context, plan *UpdatePlan, h Held, src Source) (*Staged, error) {
	unlock, err := m.Layout.Lock(ctx, h.ID)
	if err != nil {
		return nil, wrap(ReasonBusy, map[string]any{"component": h.ID}, err)
	}
	defer unlock()
	switch h.Kind {
	case HeldPack:
		return m.stagePack(ctx, plan.Version, h, plan.packs[h.ID], src)
	case HeldModel:
		return m.stageModel(ctx, plan.Version, h, plan.models, src)
	}
	return nil, nil
}

// stagePack is installPack for another version, with no prepare and no pointer.
func (m *Manager) stagePack(ctx context.Context, version string, h Held, p rv.IndexPack, src Source) (*Staged, error) {
	id := h.ID
	folder := PackFolder(version, p.SHA256)
	out := &Staged{ID: id, Version: version, Dir: folder, Kind: HeldPack, From: h.Folder}
	final := filepath.Join(m.Layout.Dir(id), folder)
	if isDir(final) {
		m.logf("%s %s: its pack is here already (%s)", id, version, folder)
		return out, nil
	}
	partial := m.Layout.PartialDir()
	if err := os.MkdirAll(partial, 0o755); err != nil {
		return nil, wrap(ReasonInstallFailed, nil, err)
	}
	for _, f := range p.Files() {
		if err := src.Pack(ctx, version, f.Name, filepath.Join(partial, f.Name), f.Size, func(int64) {}); err != nil {
			if ctx.Err() != nil {
				return nil, ctx.Err()
			}
			return nil, wrap(ReasonDownloadFailed, map[string]any{"name": f.Name}, err)
		}
	}
	dropPartial := func() {
		for _, f := range p.Files() {
			_ = os.Remove(filepath.Join(partial, f.Name))
		}
	}
	if err := rv.CheckPackFiles(p, partial); err != nil {
		dropPartial()
		return nil, wrap(ReasonVerifyFailed, map[string]any{"name": p.Name}, err)
	}
	staging := filepath.Join(m.Layout.Dir(id), stagingPre+folder)
	if err := m.unpackFor(version, p, partial, staging); err != nil {
		dropPartial()
		return nil, err
	}
	_ = os.RemoveAll(final)
	if err := renameWithRetry(ctx, staging, final); err != nil {
		_ = os.RemoveAll(staging)
		return nil, err
	}
	dropPartial()
	out.Created = true
	m.logf("%s %s: its pack is ready for the update (%s)", id, version, folder)
	return out, nil
}

// unpackFor is unpack checking the pack's version against version.
func (m *Manager) unpackFor(version string, p rv.IndexPack, dir, staging string) error {
	if err := os.MkdirAll(filepath.Dir(staging), 0o755); err != nil {
		return wrap(ReasonInstallFailed, nil, err)
	}
	_ = os.RemoveAll(staging)
	r, _, closeAll, err := componentpack.Open(dir, p.Name)
	if err != nil {
		return wrap(ReasonInstallFailed, map[string]any{"name": p.Name}, err)
	}
	defer func() { _ = closeAll() }()
	_, err = componentpack.Unpack(r, staging, componentpack.Expect{ID: p.ID, Version: version, Platform: p.Platform,
		FilesSHA256: p.FilesSHA256, UnpackedBytes: p.UnpackedBytes})
	if err != nil {
		_ = os.RemoveAll(staging)
		if errors.Is(err, componentpack.ErrInvalid) {
			return wrap(ReasonUnpackRefused, map[string]any{"entry": strings.TrimPrefix(err.Error(), componentpack.ErrInvalid.Error()+": ")}, err)
		}
		return wrap(ReasonInstallFailed, map[string]any{"name": p.Name}, err)
	}
	return nil
}

// stageModel: a revision that does not change is marked as the next release's too; a
// new one is fetched into the hub cache's snapshot of it, beside the one in use, and
// gets its rev_ folder (no pointer).
func (m *Manager) stageModel(ctx context.Context, version string, h Held, changes map[string]rv.IndexModel, src Source) (*Staged, error) {
	id := h.ID
	// the running release's revision is kept for a rollback to it
	if err := addVersion(filepath.Join(m.Layout.Dir(id), h.Folder), m.Version); err != nil {
		return nil, wrap(ReasonInstallFailed, nil, err)
	}
	im, changed := changes[id]
	if !changed {
		if err := addVersion(filepath.Join(m.Layout.Dir(id), h.Folder), version); err != nil {
			return nil, wrap(ReasonInstallFailed, nil, err)
		}
		return &Staged{ID: id, Version: version, Dir: h.Folder, Kind: HeldModel, From: h.Folder}, nil
	}
	repo, rev, _ := hfSource(im.Source)
	folder := RevFolder(rev)
	out := &Staged{ID: id, Version: version, Dir: folder, Kind: HeldModel, From: h.Folder}
	final := filepath.Join(m.Layout.Dir(id), folder)
	if _, err := readModelRecord(final); err == nil {
		return out, addVersion(final, version)
	}
	if m.HFHub == "" {
		return nil, fail(ReasonInstallFailed, map[string]any{"source": im.Source}, "no Hugging Face cache for %s", im.Source)
	}
	snap := SnapshotDir(m.HFHub, repo, rev)
	partial := filepath.Join(m.Layout.PartialDir(), id, short(rev))
	for _, f := range im.Files {
		if !safeRel(f.Path) {
			return nil, fail(ReasonVerifyFailed, map[string]any{"name": f.Path}, "bad model path")
		}
		target := filepath.Join(snap, filepath.FromSlash(f.Path))
		if fileMatches(target, f) {
			continue
		}
		dst := filepath.Join(partial, filepath.FromSlash(f.Path))
		if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
			return nil, wrap(ReasonInstallFailed, nil, err)
		}
		if err := src.Model(ctx, ModelFile{ID: id, Repo: repo, Rev: rev, Path: f.Path}, dst, f.Size, func(int64) {}); err != nil {
			if ctx.Err() != nil {
				return nil, ctx.Err()
			}
			return nil, wrap(ReasonDownloadFailed, map[string]any{"name": f.Path}, err)
		}
		if !fileMatches(dst, f) {
			_ = os.Remove(dst)
			return nil, fail(ReasonVerifyFailed, map[string]any{"name": f.Path}, "%s does not match the index", f.Path)
		}
		if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
			return nil, wrap(ReasonInstallFailed, nil, err)
		}
		_ = os.Remove(target)
		if err := moveFile(dst, target); err != nil {
			return nil, wrap(ReasonInstallFailed, nil, err)
		}
	}
	_ = os.RemoveAll(partial)
	staging := filepath.Join(m.Layout.Dir(id), stagingPre+folder)
	_ = os.RemoveAll(staging)
	if err := os.MkdirAll(staging, 0o755); err != nil {
		return nil, wrap(ReasonInstallFailed, nil, err)
	}
	b, _ := json.MarshalIndent(modelRecord{ID: id, Repo: repo, Revision: rev, Files: im.Files}, "", "  ")
	if err := os.WriteFile(filepath.Join(staging, ModelRecordFile), append(b, '\n'), 0o644); err != nil {
		return nil, wrap(ReasonInstallFailed, nil, err)
	}
	if err := os.WriteFile(filepath.Join(staging, versionsFile), []byte(version+"\n"), 0o644); err != nil {
		return nil, wrap(ReasonInstallFailed, nil, err)
	}
	if err := renameWithRetry(ctx, staging, final); err != nil {
		_ = os.RemoveAll(staging)
		return nil, err
	}
	out.Created = true
	m.logf("%s %s: revision %s is ready for the update", id, version, short(rev))
	return out, nil
}

// versionsOf is the releases a rev_ folder's versions file lists (nil when it has none).
func versionsOf(dir string) []string {
	b, err := os.ReadFile(filepath.Join(dir, versionsFile))
	if err != nil {
		return nil
	}
	var out []string
	for _, l := range strings.Split(string(b), "\n") {
		if l = strings.TrimSpace(l); l != "" {
			out = append(out, l)
		}
	}
	return out
}

func addVersion(dir, version string) error {
	if version == "" {
		return nil
	}
	vs := versionsOf(dir)
	if slices.Contains(vs, version) {
		return nil
	}
	vs = append(vs, version)
	slices.Sort(vs)
	return layout.WriteFileAtomic(filepath.Join(dir, versionsFile), []byte(strings.Join(vs, "\n")+"\n"), 0o644)
}

// DiscardStaged deletes the folders a run made (Created), and a new model revision's
// files with them: a run that ended before its switch leaves nothing behind.
func (m *Manager) DiscardStaged(staged []Staged) {
	for _, s := range staged {
		if !s.Created || s.Dir == "" || strings.ContainsAny(s.Dir, `/\`) || strings.HasPrefix(s.Dir, ".") {
			continue
		}
		dir := filepath.Join(m.Layout.Dir(s.ID), s.Dir)
		if m.Layout.Current(s.ID) == s.Dir {
			continue // in use (never a run's own: current is not touched before the switch)
		}
		if s.Kind == HeldModel {
			if rec, err := readModelRecord(dir); err == nil && m.HFHub != "" {
				_ = os.RemoveAll(SnapshotDir(m.HFHub, rec.Repo, rec.Revision))
			}
		}
		if err := os.RemoveAll(dir); err != nil {
			m.logf("level=warn %s: %s could not be deleted: %v", s.ID, s.Dir, err)
			continue
		}
		m.logf("%s: %s deleted (the update did not go on)", s.ID, s.Dir)
	}
}

// Prune deletes the pack folders of every release not in keep, and the model revisions no
// release in keep pins (their rev_ folder and their snapshot in the hub cache). What
// current names is never deleted, nor a revision whose folder lists no release (installed
// before an update recorded any). A folder that cannot be deleted (busy on Windows) is
// returned with its error: the next prune tries again.
func (m *Manager) Prune(keep []string) (removed []string, errs []error) {
	kept := map[string]bool{}
	for _, v := range keep {
		if v = strings.TrimPrefix(strings.TrimSpace(v), "v"); v != "" {
			kept[v] = true
		}
	}
	if len(kept) == 0 {
		return nil, nil
	}
	for _, id := range m.Layout.IDs() {
		cur := m.Layout.Current(id)
		for _, f := range m.Layout.Folders(id) {
			if f == cur {
				continue
			}
			dir := filepath.Join(m.Layout.Dir(id), f)
			drop := false
			snapshot := ""
			if strings.HasPrefix(f, revPre) {
				vs := versionsOf(dir)
				drop = len(vs) > 0 && !slices.ContainsFunc(vs, func(v string) bool { return kept[v] })
				if rec, err := readModelRecord(dir); drop && err == nil && m.HFHub != "" {
					snapshot = SnapshotDir(m.HFHub, rec.Repo, rec.Revision)
				}
			} else if v := packFolderVersion(f); v != "" {
				drop = !kept[v]
			}
			if !drop {
				continue
			}
			if snapshot != "" {
				if err := os.RemoveAll(snapshot); err != nil {
					errs = append(errs, fmt.Errorf("%s: %w", snapshot, err))
					continue
				}
			}
			if err := os.RemoveAll(dir); err != nil {
				errs = append(errs, fmt.Errorf("%s/%s: %w", id, f, err))
				continue
			}
			removed = append(removed, id+"/"+f)
		}
	}
	for _, r := range removed {
		m.logf("pruned %s (no kept release uses it)", r)
	}
	return removed, errs
}

// CheckKept looks for the folder each staged record's running release read (From) before
// a rollback to that release: one deleted by hand is marked missing for version (the
// component then reports Failed, pack_missing, under that release: Rows), and named in
// the answer. The rollback goes ahead either way.
func (m *Manager) CheckKept(version string, staged []Staged) []string {
	var missing []string
	for _, s := range staged {
		if s.From == "" || strings.ContainsAny(s.From, `/\`) {
			continue
		}
		if isDir(filepath.Join(m.Layout.Dir(s.ID), s.From)) {
			continue
		}
		missing = append(missing, s.ID)
		_ = layout.WriteFileAtomic(filepath.Join(m.Layout.Dir(s.ID), missingFile), []byte(version+"\n"), 0o644)
		m.logf("level=warn %s: %s's folder %s is gone: it reports pack_missing there", s.ID, version, s.From)
	}
	return missing
}

// Missing is the release a rollback found id's folder gone for ("" none).
func (l Layout) Missing(id string) string {
	b, err := os.ReadFile(filepath.Join(l.Dir(id), missingFile))
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(b))
}

// RecoverModels points each pinned model's current at the revision this release's
// catalog pins when that revision's folder is here and current names another (an update
// staged it, or a rollback came back to a release that pins the one before). It returns
// what it did, for the log.
func (m *Manager) RecoverModels() []string {
	var did []string
	for _, h := range m.Layout.Held() {
		if h.Kind != HeldModel {
			continue
		}
		comp, ok := m.Catalog.Get(h.ID)
		if !ok {
			continue
		}
		_, rev, ok := hfSource(comp.SourceFor(m.Platform))
		if !ok || rev == h.Rev {
			continue
		}
		folder := RevFolder(rev)
		if _, err := readModelRecord(filepath.Join(m.Layout.Dir(h.ID), folder)); err != nil {
			continue
		}
		if err := m.Layout.SetCurrent(h.ID, folder); err == nil {
			did = append(did, fmt.Sprintf("%s: current now names %s", h.ID, folder))
		}
	}
	return did
}
