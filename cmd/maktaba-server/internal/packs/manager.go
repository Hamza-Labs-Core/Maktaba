package packs

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/sigstore/sigstore-go/pkg/root"

	"github.com/Hamza-Labs-Core/Maktaba/shared/components"
	"github.com/Hamza-Labs-Core/Maktaba/shared/go/componentpack"
	"github.com/Hamza-Labs-Core/Maktaba/shared/go/layout"
	rv "github.com/Hamza-Labs-Core/Maktaba/shared/go/releaseverify"
)

// Progress is what an install or a removal is doing now, for the progress route and the
// component's status row.
type Progress struct {
	// Phase is installing, removing or failed; done ("") once it finished well.
	Phase   string `json:"phase"`
	Percent int    `json:"percent"`
	// Detail is the step's code (pack_downloading, ...), Name the file it is on.
	Detail string `json:"detail,omitempty"`
	Name   string `json:"name,omitempty"`
	Done   int64  `json:"done"`
	Total  int64  `json:"total"`
	// Reason and Params say why it failed.
	Reason  string         `json:"reason,omitempty"`
	Params  map[string]any `json:"params,omitempty"`
	Busy    bool           `json:"busy"`
	Started time.Time      `json:"started"`
	Updated time.Time      `json:"updated"`
}

// PrepareFunc readies an unpacked pack in its staging folder (dir) before it is renamed
// into place: runtime.ml's venv rebuilt from its wheels. report says how far it is.
type PrepareFunc func(ctx context.Context, id, dir string, report func(percent int)) error

// CleanupFunc runs after a removal deleted an asset's files (runtime.ml: the venv rebuilt
// without its extras).
type CleanupFunc func(ctx context.Context, id string) error

// Manager installs and removes this machine's asset components. Set its fields, then
// call its methods from any goroutine.
type Manager struct {
	Layout  Layout
	Catalog *components.Catalog
	// Version is the running server's version ("0.4.2"); Platform this machine's
	// (darwin-arm64).
	Version  string
	Platform string
	// Net is the network source (nil: installs only from a file).
	Net Source
	// Trusted is the Sigstore trusted root, Identity the release's signer.
	Trusted  func(ctx context.Context) (root.TrustedMaterial, error)
	Identity rv.Identity
	// Watermark is the newest index seen per version (releaseverify.IndexWatermarkPath).
	Watermark string
	// HFHub is the Hugging Face cache's hub folder (<HF_HOME>/hub) models go into.
	HFHub string
	// FreeDisk is the free bytes of the disk a path is on (0: unknown, not checked).
	FreeDisk func(path string) uint64
	// OnDisk answers for an asset installed before packs (a Full home's venv, a model the
	// pipeline downloaded itself): it counts as installed and nothing is downloaded.
	OnDisk func(id string) bool
	// Prepare and Cleanup are per asset (runtime.ml, tool.yt-dlp).
	Prepare map[string]PrepareFunc
	Cleanup map[string]CleanupFunc
	// Disabled is [components] packs = "off": nothing is installed (packs_disabled);
	// what is on disk is still reported.
	Disabled bool
	Log      io.Writer
	Now      func() time.Time

	mu   sync.Mutex
	prog map[string]*Progress
}

func (m *Manager) now() time.Time {
	if m.Now != nil {
		return m.Now()
	}
	return time.Now()
}

func (m *Manager) logf(format string, a ...any) {
	if m.Log != nil {
		fmt.Fprintf(m.Log, "[packs] "+format+"\n", a...)
	}
}

// Progress is id's install or removal now (ok false when none ran this boot).
func (m *Manager) Progress(id string) (Progress, bool) {
	m.mu.Lock()
	defer m.mu.Unlock()
	p, ok := m.prog[id]
	if !ok {
		return Progress{}, false
	}
	out := *p
	return out, true
}

// Busy reports an install or a removal of id running in this process.
func (m *Manager) Busy(id string) bool {
	p, ok := m.Progress(id)
	return ok && p.Busy
}

// begin marks id busy with phase; ErrBusy-reason when it already is.
func (m *Manager) begin(id, phase string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.prog == nil {
		m.prog = map[string]*Progress{}
	}
	if p, ok := m.prog[id]; ok && p.Busy {
		return fail(ReasonBusy, map[string]any{"component": id}, "%s is %s already", id, p.Phase)
	}
	now := m.now()
	m.prog[id] = &Progress{Phase: phase, Busy: true, Started: now, Updated: now}
	return nil
}

func (m *Manager) end(id string, err error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	p := m.prog[id]
	if p == nil {
		return
	}
	p.Busy, p.Updated = false, m.now()
	if err != nil {
		p.Phase, p.Reason, p.Params = components.PhaseFailed, ReasonOf(err), ParamsOf(err)
		return
	}
	p.Phase, p.Percent, p.Detail, p.Name = "", 100, "", ""
}

func (m *Manager) step(id, detail, name string, done, total int64) {
	m.mu.Lock()
	defer m.mu.Unlock()
	p := m.prog[id]
	if p == nil {
		return
	}
	p.Detail, p.Name, p.Done, p.Total, p.Updated = detail, name, done, total, m.now()
	if total > 0 {
		p.Percent = int(done * 100 / total)
	}
}

// percent sets a step's share in whole percent (preparing, unpacking).
func (m *Manager) percent(id, detail string, pct int) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if p := m.prog[id]; p != nil {
		p.Detail, p.Percent, p.Updated = detail, pct, m.now()
	}
}

// asset is id's catalog entry when it is an asset of this platform.
func (m *Manager) asset(id string) (*components.Component, error) {
	comp, ok := m.Catalog.Get(id)
	if !ok || comp.Kind != components.KindAsset {
		return nil, fail(ReasonInstallFailed, map[string]any{"component": id}, "%s is not an asset component", id)
	}
	if len(comp.Platforms) > 0 && !slices.Contains(comp.Platforms, m.Platform) {
		return nil, fail(ReasonPlatform, map[string]any{"platform": m.Platform}, "%s is not for %s", id, m.Platform)
	}
	return comp, nil
}

// Installed reports whether id is on this machine for this release: its current pointer
// names this release's pack folder (or the pinned revision's record), or it was there
// before packs (OnDisk). A removal not finished is not installed.
func (m *Manager) Installed(id string) bool {
	comp, ok := m.Catalog.Get(id)
	if !ok || comp.Kind != components.KindAsset || m.Layout.Removing(id) {
		return false
	}
	if cur := m.Layout.Current(id); cur != "" {
		src := comp.SourceFor(m.Platform)
		switch {
		case strings.HasPrefix(src, "pack:"):
			if strings.HasPrefix(cur, m.Version+"_") {
				return true
			}
		case strings.HasPrefix(src, "hf:"):
			if _, rev, ok := hfSource(src); ok && cur == RevFolder(rev) {
				return true
			}
		}
	}
	return m.OnDisk != nil && m.OnDisk(id)
}

// session is one install's source and the index it verified (fetched once).
type session struct {
	src    Source
	idx    *rv.VerifiedIndex
	signed Signed
}

// Install puts id on this machine, its requirements first (one of each "any:" group),
// from src (nil: the network). It returns at once with nil when id is installed already,
// and a busy error when an install or removal of id runs in this process; another
// process's is waited for (the lock). Every failure is an *Error with its reason.
func (m *Manager) Install(ctx context.Context, id string, src Source) error {
	if src == nil {
		src = m.Net
	}
	s := &session{src: src}
	return m.install(ctx, id, s, map[string]bool{})
}

func (m *Manager) install(ctx context.Context, id string, s *session, seen map[string]bool) (err error) {
	if seen[id] {
		return nil
	}
	seen[id] = true
	if err := m.begin(id, components.PhaseInstalling); err != nil {
		return err
	}
	defer func() {
		m.end(id, err)
		if err != nil {
			m.logf("level=error %s: not installed (%s): %v", id, ReasonOf(err), err)
		}
	}()
	if m.Disabled {
		return fail(ReasonPacksDisabled, nil, "[components] packs = \"off\"")
	}
	comp, err := m.asset(id)
	if err != nil {
		return err
	}
	if m.Installed(id) {
		return nil
	}
	for _, req := range m.Catalog.Requires(id) {
		if m.anyInstalled(req.AnyOf) {
			continue
		}
		alt := req.AnyOf[0]
		if c, ok := m.Catalog.Get(alt); !ok || c.Kind != components.KindAsset {
			continue
		}
		// the requirement's own progress shows while it installs; this one waits at 0 %
		if err := m.install(ctx, alt, s, seen); err != nil {
			return wrap(ReasonInstallFailed, map[string]any{"component": alt}, err)
		}
	}
	if s.src == nil {
		return fail(ReasonDownloadFailed, nil, "no source to install from")
	}
	unlock, err := m.Layout.Lock(ctx, id)
	if err != nil {
		return wrap(ReasonBusy, map[string]any{"component": id}, err)
	}
	defer unlock()
	if m.Installed(id) { // another process installed it while this one waited
		return nil
	}
	src := comp.SourceFor(m.Platform)
	switch {
	case strings.HasPrefix(src, "pack:"):
		return m.installPack(ctx, s, comp)
	case strings.HasPrefix(src, "hf:"):
		return m.installModel(ctx, s, comp, src)
	}
	return fail(ReasonInstallFailed, map[string]any{"source": src}, "%s from %s cannot be installed here yet", id, src)
}

func (m *Manager) anyInstalled(ids []string) bool {
	for _, a := range ids {
		if m.Installed(a) {
			return true
		}
	}
	return false
}

// index fetches and verifies the release's component index once per session (story
// 51.15 §4: the signature, the identity, the digests, expiry, version, watermark).
func (m *Manager) index(ctx context.Context, id string, s *session) (*rv.VerifiedIndex, error) {
	if s.idx != nil {
		return s.idx, nil
	}
	m.step(id, DetailIndex, rv.ComponentsIndexName(m.Version), 0, 0)
	signed, err := s.src.Signed(ctx, m.Version)
	if err != nil {
		return nil, wrap(ReasonDownloadFailed, map[string]any{"name": "release"}, err)
	}
	if m.Trusted == nil {
		return nil, fail(ReasonVerifyFailed, nil, "this build cannot verify releases")
	}
	tm, err := m.Trusted(ctx)
	if err != nil {
		return nil, wrap(ReasonVerifyFailed, nil, err)
	}
	v, err := rv.VerifyComponentsIndex(rv.ComponentsInput{Tag: "v" + m.Version, Identity: m.Identity,
		Checksums: signed.Checksums, Bundle: signed.Bundle, Manifest: signed.Manifest, TrustedMaterial: tm,
		Index: signed.Index, ServerVersion: m.Version, Now: m.now(), Seen: rv.IndexWatermarkFile{Path: m.Watermark}})
	if err != nil {
		reason := rv.IndexReason(err)
		if signed.Index == nil && reason == ReasonVerifyFailed {
			// the release names an index the source did not give
			return nil, wrap(ReasonDownloadFailed, map[string]any{"name": rv.ComponentsIndexName(m.Version)}, err)
		}
		return nil, wrap(reason, map[string]any{"version": m.Version}, err)
	}
	s.idx, s.signed = v, signed
	return v, nil
}

// room refuses an install that needs more than the disk has: need plus 10 %.
func (m *Manager) room(dir string, need int64) error {
	if m.FreeDisk == nil {
		return nil
	}
	_ = os.MkdirAll(dir, 0o755)
	free := m.FreeDisk(dir)
	need += need / 10
	if free > 0 && int64(free) < need {
		return fail(ReasonDiskSpace, map[string]any{"need": need, "free": free}, "%d bytes needed, %d free", need, free)
	}
	return nil
}

func (m *Manager) installPack(ctx context.Context, s *session, comp *components.Component) error {
	id := comp.ID
	v, err := m.index(ctx, id, s)
	if err != nil {
		return err
	}
	var p *rv.IndexPack
	for i := range v.Index.Packs {
		if v.Index.Packs[i].ID == id && v.Index.Packs[i].Platform == m.Platform {
			p = &v.Index.Packs[i]
		}
	}
	if p == nil {
		return fail(ReasonPackMissing, map[string]any{"component": id, "platform": m.Platform},
			"the index lists no %s pack for %s", id, m.Platform)
	}
	partial := m.Layout.PartialDir()
	held := int64(0)
	for _, f := range p.Files() {
		if info, err := os.Stat(filepath.Join(partial, f.Name)); err == nil && info.Size() <= f.Size {
			held += info.Size()
		}
	}
	if err := m.room(m.Layout.Root, p.Size-held+p.UnpackedBytes); err != nil {
		return err
	}
	if err := os.MkdirAll(partial, 0o755); err != nil {
		return wrap(ReasonInstallFailed, nil, err)
	}
	// download (resumed from what a cut download left)
	before := int64(0)
	for _, f := range p.Files() {
		dst := filepath.Join(partial, f.Name)
		start := before
		err := s.src.Pack(ctx, m.Version, f.Name, dst, f.Size, func(n int64) {
			m.step(id, DetailDownloading, f.Name, start+n, p.Size)
		})
		if err != nil {
			if ctx.Err() != nil {
				return ctx.Err()
			}
			return wrap(ReasonDownloadFailed, map[string]any{"name": f.Name}, err)
		}
		before += f.Size
	}
	// verify: size, then sha256 (a mismatch is deleted: the next try downloads anew)
	m.step(id, DetailVerifying, p.Name, p.Size, p.Size)
	if err := rv.CheckPackFiles(*p, partial); err != nil {
		for _, f := range p.Files() {
			_ = os.Remove(filepath.Join(partial, f.Name))
		}
		return wrap(ReasonVerifyFailed, map[string]any{"name": p.Name}, err)
	}
	folder := PackFolder(m.Version, p.SHA256)
	staging := filepath.Join(m.Layout.Dir(id), stagingPre+folder)
	if err := m.unpack(id, *p, partial, staging); err != nil {
		return err
	}
	if hook := m.Prepare[id]; hook != nil {
		m.percent(id, DetailPreparing, 0)
		if err := hook(ctx, id, staging, func(pct int) { m.percent(id, DetailPreparing, pct) }); err != nil {
			_ = os.RemoveAll(staging)
			if ctx.Err() != nil {
				return ctx.Err()
			}
			return wrap(ReasonInstallFailed, map[string]any{"component": id}, err)
		}
	}
	if err := m.place(ctx, id, staging, folder); err != nil {
		return err
	}
	for _, f := range p.Files() {
		_ = os.Remove(filepath.Join(partial, f.Name))
	}
	// another pack of this release (an earlier build of the same version) is replaced
	for _, f := range m.Layout.Folders(id) {
		if f != folder && strings.HasPrefix(f, m.Version+"_") {
			_ = os.RemoveAll(filepath.Join(m.Layout.Dir(id), f))
		}
	}
	m.logf("%s %s installed from the %s: %s", id, m.Version, s.src.Kind(), folder)
	return nil
}

// unpack opens the verified pack and unpacks it into staging with componentpack's rules.
func (m *Manager) unpack(id string, p rv.IndexPack, dir, staging string) error {
	if err := os.MkdirAll(filepath.Dir(staging), 0o755); err != nil {
		return wrap(ReasonInstallFailed, nil, err)
	}
	_ = os.RemoveAll(staging)
	r, _, closeAll, err := componentpack.Open(dir, p.Name)
	if err != nil {
		return wrap(ReasonInstallFailed, map[string]any{"name": p.Name}, err)
	}
	defer func() { _ = closeAll() }()
	m.step(id, DetailUnpacking, p.Name, 0, p.Size)
	cr := &countReader{r: r, f: func(n int64) { m.step(id, DetailUnpacking, p.Name, n, p.Size) }}
	_, err = componentpack.Unpack(cr, staging, componentpack.Expect{ID: p.ID, Version: m.Version, Platform: p.Platform,
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

// place renames staging to the component's folder and points current at it.
func (m *Manager) place(ctx context.Context, id, staging, folder string) error {
	final := filepath.Join(m.Layout.Dir(id), folder)
	_ = os.RemoveAll(final)
	if err := renameWithRetry(ctx, staging, final); err != nil {
		_ = os.RemoveAll(staging)
		return err
	}
	if err := m.Layout.SetCurrent(id, folder); err != nil {
		return wrap(ReasonInstallFailed, nil, err)
	}
	_ = os.Remove(filepath.Join(m.Layout.Dir(id), removingFile))
	_ = os.Remove(filepath.Join(m.Layout.Dir(id), removedFile))
	return nil
}

type countReader struct {
	r    io.Reader
	n    int64
	f    func(int64)
	last time.Time
}

func (c *countReader) Read(p []byte) (int, error) {
	n, err := c.r.Read(p)
	c.n += int64(n)
	if now := time.Now(); now.Sub(c.last) >= 250*time.Millisecond {
		c.last = now
		c.f(c.n)
	}
	return n, err
}

// Remove deletes id from this machine: this release's pack folders and its pointer, or
// the model's files in the Hugging Face cache and its record, then the asset's Cleanup.
// A .removing marker is written first, so a removal cut short is finished by the next
// pass. Settings and data are not touched. Whether a component that is enabled requires
// id is the caller's check (the routes refuse that with component_required_by).
func (m *Manager) Remove(ctx context.Context, id string) (err error) {
	if err := m.begin(id, components.PhaseRemoving); err != nil {
		return err
	}
	defer func() {
		m.end(id, err)
		if err != nil {
			m.logf("level=error %s: not removed (%s): %v", id, ReasonOf(err), err)
		}
	}()
	comp, ok := m.Catalog.Get(id)
	if !ok || comp.Kind != components.KindAsset {
		return fail(ReasonInstallFailed, map[string]any{"component": id}, "%s is not an asset component", id)
	}
	unlock, err := m.Layout.Lock(ctx, id)
	if err != nil {
		return wrap(ReasonBusy, map[string]any{"component": id}, err)
	}
	defer unlock()
	src := comp.SourceFor(m.Platform)
	if !strings.HasPrefix(src, "pack:") && !strings.HasPrefix(src, "hf:") {
		return fail(ReasonInstallFailed, map[string]any{"source": src}, "%s from %s cannot be removed here yet", id, src)
	}
	if err := m.Layout.markRemoving(id); err != nil {
		return wrap(ReasonInstallFailed, nil, err)
	}
	m.percent(id, DetailRemoving, 10)
	dir := m.Layout.Dir(id)
	// the asset's own undoing first, while its files are there (runtime.ml: the venv
	// rebuilt without its extras, from the pack's wheels)
	if hook := m.Cleanup[id]; hook != nil {
		if err := hook(ctx, id); err != nil {
			if ctx.Err() != nil {
				return ctx.Err()
			}
			return wrap(ReasonInstallFailed, map[string]any{"component": id}, err)
		}
	}
	m.percent(id, DetailRemoving, 50)
	switch {
	case strings.HasPrefix(src, "pack:"):
		// this release's folders; kept folders of other releases stay for a rollback
		for _, f := range m.Layout.Folders(id) {
			if strings.HasPrefix(f, m.Version+"_") {
				if err := os.RemoveAll(filepath.Join(dir, f)); err != nil {
					return wrap(ReasonBusy, map[string]any{"path": f}, err)
				}
			}
		}
		if m.Layout.Current(id) == "" {
			_ = os.Remove(filepath.Join(dir, CurrentFile))
		}
	case strings.HasPrefix(src, "hf:"):
		repo, _, _ := hfSource(src)
		if m.HFHub != "" && repo != "" {
			if err := os.RemoveAll(filepath.Join(m.HFHub, hfRepoDir(repo))); err != nil {
				return wrap(ReasonBusy, map[string]any{"path": hfRepoDir(repo)}, err)
			}
		}
		for _, f := range m.Layout.Folders(id) {
			_ = os.RemoveAll(filepath.Join(dir, f))
		}
		_ = os.Remove(filepath.Join(dir, CurrentFile))
	}
	m.percent(id, DetailRemoving, 90)
	_ = layout.WriteFileAtomic(filepath.Join(dir, removedFile), []byte(m.now().UTC().Format(time.RFC3339)+"\n"), 0o644)
	if err := os.Remove(filepath.Join(dir, removingFile)); err != nil && !errors.Is(err, os.ErrNotExist) {
		return wrap(ReasonInstallFailed, nil, err)
	}
	m.logf("%s removed", id)
	return nil
}
