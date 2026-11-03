package updater

// Story 51.18's unit tests (T01–T08): the packs step of an update, its room, its discard,
// the commit's prune and the rollback's check, over real signed fake releases with a
// component index and a tool.yt-dlp pack.

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/sigstore/sigstore-go/pkg/root"

	"github.com/Hamza-Labs-Core/Maktaba/cmd/maktaba-server/internal/packs"
	"github.com/Hamza-Labs-Core/Maktaba/shared/components"
	"github.com/Hamza-Labs-Core/Maktaba/shared/go/componentpack"
	rv "github.com/Hamza-Labs-Core/Maktaba/shared/go/releaseverify"
	"github.com/Hamza-Labs-Core/Maktaba/shared/go/releaseverify/releaseverifytest"
)

const (
	pPlatform = "linux-amd64"
	pID       = "tool.yt-dlp"
)

// packRig is a machine's components folder and its packs manager (running 1.4.0), and
// the fake GitHub the releases are published on.
type packRig struct {
	rs    *releases
	state string
	root  string
	pm    *packs.Manager
	log   lockedLog

	mu   sync.Mutex
	hits []string // the paths the fake GitHub was asked for
}

type lockedLog struct {
	mu sync.Mutex
	b  bytes.Buffer
}

func (l *lockedLog) Write(p []byte) (int, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.b.Write(p)
}
func (l *lockedLog) String() string { l.mu.Lock(); defer l.mu.Unlock(); return l.b.String() }

func newPackRig(t *testing.T) *packRig {
	t.Helper()
	pr := &packRig{rs: newReleases(t), state: t.TempDir()}
	pr.root = filepath.Join(pr.state, "components")
	inner := pr.rs.srv.Config.Handler
	pr.rs.srv.Config.Handler = http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		pr.mu.Lock()
		pr.hits = append(pr.hits, r.URL.Path)
		pr.mu.Unlock()
		inner.ServeHTTP(w, r)
	})
	tm := pr.rs.tm
	pr.pm = &packs.Manager{Layout: packs.Layout{Root: pr.root}, Catalog: components.Default(), Version: "1.4.0",
		Platform: pPlatform, Trusted: func(context.Context) (root.TrustedMaterial, error) { return tm, nil },
		Identity: pr.rs.source().Identity, Watermark: rv.IndexWatermarkPath(pr.state),
		HFHub: filepath.Join(pr.state, "hub"), Log: &pr.log}
	return pr
}

func (pr *packRig) requests(sub string) []string {
	pr.mu.Lock()
	defer pr.mu.Unlock()
	var out []string
	for _, h := range pr.hits {
		if strings.Contains(h, sub) {
			out = append(out, h)
		}
	}
	return out
}

func (pr *packRig) packsOf(src Source) *Packs {
	return &Packs{M: pr.pm, Source: src, Wait: time.Second}
}

// hold leaves id installed from its pack of version, as 51.16's install does.
func (pr *packRig) hold(t *testing.T, id, version string) string {
	t.Helper()
	folder := packs.PackFolder(version, strings.Repeat("c", 64))
	dir := filepath.Join(pr.root, id, folder)
	if err := os.MkdirAll(filepath.Join(dir, "wheels"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, componentpack.ManifestName), []byte(`{"id":"`+id+`","version":"`+version+`"}`), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := pr.pm.Layout.SetCurrent(id, folder); err != nil {
		t.Fatal(err)
	}
	return folder
}

type pubOpts struct {
	noIndex bool // a release from before 51.15
	corrupt bool // the pack's bytes changed after signing (one byte)
}

// publish signs tag with a tool.yt-dlp pack of its version in its index; it returns the
// job's assets and the pack's folder name.
func (pr *packRig) publish(t *testing.T, tag string, o pubOpts) (map[string]string, string) {
	t.Helper()
	version := strings.TrimPrefix(tag, "v")
	tree := t.TempDir()
	for p, b := range map[string]string{"wheels/index.json": `{"wheels":[]}`, "wheels/yt_dlp-2026.1.1-py3-none-any.whl": "not really yt-dlp " + version,
		"constraints.txt": "yt-dlp==2026.1.1\n"} {
		full := filepath.Join(tree, filepath.FromSlash(p))
		_ = os.MkdirAll(filepath.Dir(full), 0o755)
		if err := os.WriteFile(full, []byte(b), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	out := t.TempDir()
	built, err := componentpack.Build(componentpack.Options{Dir: tree, ID: pID, Version: version, Platform: pPlatform,
		ModTime: time.Unix(1_800_000_000, 0), Out: out})
	if err != nil {
		t.Fatal(err)
	}
	raw, err := os.ReadFile(filepath.Join(out, built.Name))
	if err != nil {
		t.Fatal(err)
	}
	pub := time.Now().UTC().Add(-time.Hour).Truncate(time.Second)
	idx := rv.ComponentsIndex{Schema: 1, Version: version, Commit: strings.Repeat("0a", 20), Published: pub,
		Expires: pub.Add(rv.IndexLifetime), Server: rv.ServerRange(version),
		Packs: []rv.IndexPack{{ID: pID, Platform: pPlatform, Name: built.Name, SHA256: built.SHA256, Size: built.Size,
			UnpackedBytes: built.Manifest.UnpackedBytes, FilesSHA256: built.Manifest.FilesSHA256, Requires: []string{}}},
		Models: []rv.IndexModel{}}
	ib, _ := json.MarshalIndent(idx, "", "  ")
	ro := releaseverifytest.ReleaseOptions{MinUpgradeFrom: "1.0.0", Extra: map[string][]byte{built.Name: raw}}
	if !o.noIndex {
		ro.ComponentsIndex = ib
	}
	rel, assets := pr.rs.publish(t, tag, ro, nil)
	if o.corrupt {
		bad := append([]byte(nil), rel.Files[built.Name]...)
		bad[len(bad)/2] ^= 0xff
		pr.rs.files[tag+"/"+built.Name] = bad
	}
	return assets, packs.PackFolder(version, built.SHA256)
}

// packJob is a home's job to tag with assets.
func packJob(tag string, assets map[string]string) Job {
	j := jobFor(tag, assets)
	j.Rehearse = false
	return j
}

// T01: the packs step fetches and verifies the new release's pack into its own folder
// before the switch, and current is not touched; the journal records it.
func TestPacksStepStagesTheNewPack(t *testing.T) {
	pr := newPackRig(t)
	from := pr.hold(t, pID, "1.4.0")
	assets, folder := pr.publish(t, "v1.5.0", pubOpts{})
	w := newWorld("1.4.0")
	r := startRig(t, t.TempDir(), w, healthy)
	r.m.Packs = pr.packsOf(pr.rs.source())
	if err := r.m.Prepare(packJob("v1.5.0", assets)); err != nil {
		t.Fatal(err)
	}
	j := r.waitState(StatePrepared)
	st := stepOf(j, StepPacks)
	if st == nil || st.State != "done" || !strings.Contains(st.Detail, folder) {
		t.Fatalf("packs step %+v (steps %+v)", st, j.Steps)
	}
	var order []string
	for _, s := range j.Steps {
		order = append(order, s.Step)
	}
	if got := strings.Join(order, ","); !strings.HasPrefix(got, "preflight,stage,packs,smoke") {
		t.Fatalf("steps %s", got)
	}
	if _, err := os.Stat(filepath.Join(pr.root, pID, folder, "wheels", "yt_dlp-2026.1.1-py3-none-any.whl")); err != nil {
		t.Fatalf("the new pack's files: %v", err)
	}
	if cur := pr.pm.Layout.Current(pID); cur != from {
		t.Fatalf("current is %q, want the running release's %q", cur, from)
	}
	if len(j.Packs) != 1 || j.Packs[0].ID != pID || j.Packs[0].Version != "1.5.0" || j.Packs[0].Dir != folder ||
		j.Packs[0].From != from || !j.Packs[0].Created {
		t.Fatalf("journal packs %+v", j.Packs)
	}
	// the journal on disk says it too (the next release and the home read it)
	disk, err := LoadJournal(r.dir)
	if err != nil || disk == nil || len(disk.Packs) != 1 {
		t.Fatalf("journal on disk %+v %v", disk, err)
	}
	// the new release's boot points current at its own folder (51.16's Recover)
	pr.pm.Layout.Recover("1.5.0")
	if cur := pr.pm.Layout.Current(pID); cur != folder {
		t.Fatalf("after the new release's boot current is %q, want %q", cur, folder)
	}
}

// T02: a pack that fails its sha256 fails the step (pack_verify_failed): prepare_failed,
// nothing switched, the folders the run made gone, the running release's untouched.
func TestPacksStepRefusesABadPack(t *testing.T) {
	pr := newPackRig(t)
	from := pr.hold(t, pID, "1.4.0")
	assets, folder := pr.publish(t, "v1.5.0", pubOpts{corrupt: true})
	w := newWorld("1.4.0")
	r := startRig(t, t.TempDir(), w, healthy)
	r.m.Packs = pr.packsOf(pr.rs.source())
	if err := r.m.Prepare(packJob("v1.5.0", assets)); err != nil {
		t.Fatal(err)
	}
	j := r.waitState(StatePrepareFailed)
	if j.Error == nil || j.Error.Step != StepPacks || j.Error.Code != "pack_verify_failed" {
		t.Fatalf("error %+v", j.Error)
	}
	r.m.Wait()
	if w.called(StepSwitch) || w.called(StepSmoke) || !w.called("discard") || w.next != "1.4.0" {
		t.Fatalf("calls %v next %s", w.calls, w.next)
	}
	if _, err := os.Stat(filepath.Join(pr.root, pID, folder)); !os.IsNotExist(err) {
		t.Fatalf("the refused pack's folder is there: %v", err)
	}
	if got := pr.pm.Layout.Folders(pID); len(got) != 1 || got[0] != from {
		t.Fatalf("folders %v, want only %s", got, from)
	}
	if cur := pr.pm.Layout.Current(pID); cur != from {
		t.Fatalf("current %q", cur)
	}
	ents, _ := os.ReadDir(pr.pm.Layout.PartialDir())
	if len(ents) != 0 {
		t.Fatalf("downloads left: %v", ents)
	}
}

// A later step's failure (the test run) deletes the folders the packs step made too.
func TestPrepareFailureAfterPacksDeletesThem(t *testing.T) {
	pr := newPackRig(t)
	from := pr.hold(t, pID, "1.4.0")
	assets, folder := pr.publish(t, "v1.5.0", pubOpts{})
	w := newWorld("1.4.0")
	r := startRig(t, t.TempDir(), w, healthy)
	r.inst.failAt = StepSmoke
	r.m.Packs = pr.packsOf(pr.rs.source())
	_ = r.m.Prepare(packJob("v1.5.0", assets))
	r.waitState(StatePrepareFailed)
	r.m.Wait()
	if _, err := os.Stat(filepath.Join(pr.root, pID, folder)); !os.IsNotExist(err) {
		t.Fatalf("the run's pack folder stayed: %v", err)
	}
	if got := pr.pm.Layout.Folders(pID); len(got) != 1 || got[0] != from {
		t.Fatalf("folders %v", got)
	}
}

// T03: a Full home with no pack: the step is skipped, nothing is fetched.
func TestPacksStepSkippedWithNoPacks(t *testing.T) {
	pr := newPackRig(t)
	assets, _ := pr.publish(t, "v1.5.0", pubOpts{})
	w := newWorld("1.4.0")
	r := startRig(t, t.TempDir(), w, healthy)
	r.m.Packs = pr.packsOf(pr.rs.source())
	_ = r.m.Prepare(packJob("v1.5.0", assets))
	j := r.waitState(StatePrepared)
	st := stepOf(j, StepPacks)
	if st == nil || st.State != "skipped" || st.Detail != "skipped: no packs" {
		t.Fatalf("packs step %+v", st)
	}
	if got := pr.requests("components-"); len(got) != 0 {
		t.Fatalf("the index was fetched: %v", got)
	}
	if got := pr.requests(".tar.zst"); len(got) != 0 {
		t.Fatalf("a pack was fetched: %v", got)
	}
	if len(j.Packs) != 0 {
		t.Fatalf("journal packs %+v", j.Packs)
	}
}

// T04: packs installed and a release with no index: index_missing, nothing changes.
func TestPacksStepNeedsAnIndex(t *testing.T) {
	pr := newPackRig(t)
	from := pr.hold(t, pID, "1.4.0")
	assets, _ := pr.publish(t, "v1.5.0", pubOpts{noIndex: true})
	w := newWorld("1.4.0")
	r := startRig(t, t.TempDir(), w, healthy)
	r.m.Packs = pr.packsOf(pr.rs.source())
	_ = r.m.Prepare(packJob("v1.5.0", assets))
	j := r.waitState(StatePrepareFailed)
	// the preflight reads the index for the room (story 51.18 §2): it fails there, before
	// the release is fetched
	if j.Error == nil || j.Error.Code != "index_missing" {
		t.Fatalf("error %+v", j.Error)
	}
	r.m.Wait()
	if w.called(StepStage) || w.called(StepSwitch) {
		t.Fatalf("calls %v", w.calls)
	}
	if got := pr.pm.Layout.Folders(pID); len(got) != 1 || got[0] != from {
		t.Fatalf("folders %v", got)
	}
}

// [components] packs = "off" with packs installed refuses the update (packs_disabled)
// rather than switch to a release whose packs are missing.
func TestPacksStepRefusedWithPacksOff(t *testing.T) {
	pr := newPackRig(t)
	pr.hold(t, pID, "1.4.0")
	assets, _ := pr.publish(t, "v1.5.0", pubOpts{})
	pr.pm.Disabled = true
	w := newWorld("1.4.0")
	r := startRig(t, t.TempDir(), w, healthy)
	r.m.Packs = pr.packsOf(pr.rs.source())
	_ = r.m.Prepare(packJob("v1.5.0", assets))
	j := r.waitState(StatePrepareFailed)
	if j.Error == nil || j.Error.Code != "packs_disabled" {
		t.Fatalf("error %+v", j.Error)
	}
}

// T05: free space below the release plus the pack: disk_space at the preflight, before
// anything is fetched.
func TestPacksRoomAtPreflight(t *testing.T) {
	pr := newPackRig(t)
	pr.hold(t, pID, "1.4.0")
	assets, _ := pr.publish(t, "v1.5.0", pubOpts{})
	prev := freeDisk
	t.Cleanup(func() { freeDisk = prev })
	// room for the release alone: the pack does not fit beside it
	freeDisk = func(string) uint64 { return roomRelease + 10 }
	w := newWorld("1.4.0")
	r := startRig(t, t.TempDir(), w, healthy)
	r.m.Packs = pr.packsOf(pr.rs.source())
	_ = r.m.Prepare(packJob("v1.5.0", assets))
	j := r.waitState(StatePrepareFailed)
	if j.Error == nil || j.Error.Step != StepPreflight || j.Error.Code != "disk_space" {
		t.Fatalf("error %+v", j.Error)
	}
	if got := pr.requests(".tar.zst"); len(got) != 0 {
		t.Fatalf("a pack was fetched: %v", got)
	}
	// with room for both it goes on
	freeDisk = func(string) uint64 { return 10 * roomRelease }
	j2 := &Journal{Job: packJob("v1.5.0", assets)}
	if err := pr.packsOf(pr.rs.source()).room(context.Background(), j2); err != nil {
		t.Fatalf("room with enough free: %v", err)
	}
}

// keeperInst is the fake installer naming the releases it keeps.
type keeperInst struct {
	*fakeInst
	kept []string
}

func (k *keeperInst) Kept() []KeptRelease {
	var out []KeptRelease
	for _, v := range k.kept {
		out = append(out, KeptRelease{Version: v})
	}
	return out
}
func (k *keeperInst) RunKept(context.Context, KeptRelease) error { return nil }

// T06: the commit (KeepVersions 2) prunes the pack folders of releases not kept: the
// running release's and the kept previous one's stay. A folder that cannot be deleted
// is logged and the commit goes on; the next prune deletes it.
func TestCommitPrunesPacksOfReleasesNotKept(t *testing.T) {
	if runtime.GOOS == "windows" || os.Geteuid() == 0 {
		t.Skip("a folder made undeletable by its mode (not on Windows, nor as root)")
	}
	pr := newPackRig(t)
	old := packs.PackFolder("1.3.0", strings.Repeat("d", 64))
	if err := os.MkdirAll(filepath.Join(pr.root, pID, old, "wheels"), 0o755); err != nil {
		t.Fatal(err)
	}
	_ = os.WriteFile(filepath.Join(pr.root, pID, old, "wheels", "a.whl"), []byte("a"), 0o644)
	from := pr.hold(t, pID, "1.4.0")
	assets, folder := pr.publish(t, "v1.5.0", pubOpts{})
	dir := t.TempDir()
	w := newWorld("1.4.0")
	r := startRig(t, dir, w, healthy)
	r.m.Packs = pr.packsOf(pr.rs.source())
	_ = r.m.Prepare(packJob("v1.5.0", assets))
	r.waitState(StatePrepared)
	_ = r.m.Apply("run-1")
	r.waitExit()
	r.waitState(StateRestarting)
	pr.pm.Layout.Recover("1.5.0") // the new release's boot
	r.stop()
	// a file in the oldest folder held open the Windows way: its folder cannot be deleted
	if err := os.Chmod(filepath.Join(pr.root, pID, old, "wheels"), 0o500); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(filepath.Join(pr.root, pID, old, "wheels"), 0o755) })
	var log lockedLog
	n := startRig(t, dir, w, healthy)
	n.m.Log = &log
	k := &keeperInst{fakeInst: n.inst, kept: []string{"1.5.0", "1.4.0"}}
	n.m.Installer = k
	n.m.Packs = pr.packsOf(pr.rs.source())
	n.m.Resume()
	n.waitState(StateCommitted)
	if !strings.Contains(log.String(), "could not be pruned") {
		t.Fatalf("the busy folder was not logged: %s", log.String())
	}
	if !isDirT(filepath.Join(pr.root, pID, old)) {
		t.Fatal("the busy folder is gone")
	}
	// the next start's prune deletes it; the kept releases' folders stay
	_ = os.Chmod(filepath.Join(pr.root, pID, old, "wheels"), 0o755)
	n.m.PrunePacks()
	got := pr.pm.Layout.Folders(pID)
	if len(got) != 2 || !contains(got, from) || !contains(got, folder) {
		t.Fatalf("folders %v, want %s and %s", got, from, folder)
	}
	// once 1.4.0 is no longer kept its folder goes too
	k.kept = []string{"1.5.0"}
	n.m.PrunePacks()
	if got := pr.pm.Layout.Folders(pID); len(got) != 1 || got[0] != folder {
		t.Fatalf("folders %v, want only %s", got, folder)
	}
}

func isDirT(p string) bool {
	info, err := os.Stat(p)
	return err == nil && info.IsDir()
}

func contains(s []string, v string) bool {
	for _, x := range s {
		if x == v {
			return true
		}
	}
	return false
}

// T07: a rollback whose release before had its folder deleted by hand goes ahead, and
// that component reports Failed (pack_missing) under it; a kept folder is found again.
func TestRollbackWithAKeptPackDeleted(t *testing.T) {
	for _, deleted := range []bool{false, true} {
		t.Run(map[bool]string{false: "kept", true: "deleted"}[deleted], func(t *testing.T) {
			pr := newPackRig(t)
			from := pr.hold(t, pID, "1.4.0")
			assets, folder := pr.publish(t, "v1.5.0", pubOpts{})
			dir := t.TempDir()
			w := newWorld("1.4.0")
			r := startRig(t, dir, w, healthy)
			r.m.Packs = pr.packsOf(pr.rs.source())
			_ = r.m.Prepare(packJob("v1.5.0", assets))
			r.waitState(StatePrepared)
			_ = r.m.Apply("run-1")
			r.waitExit()
			pr.pm.Layout.Recover("1.5.0")
			if deleted {
				_ = os.RemoveAll(filepath.Join(pr.root, pID, from))
			}
			r.stop()
			n := startRig(t, dir, w, Verdict{Fatal: true, Why: "api crash loop"})
			n.m.Packs = pr.packsOf(pr.rs.source())
			n.m.Resume()
			n.waitExit()
			n.waitState(StateRollingBack)
			if w.next != "1.4.0" {
				t.Fatalf("next %s: the rollback did not go ahead", w.next)
			}
			// the release before boots: current names its kept folder, or reports pack_missing
			pr.pm.Layout.Recover("1.4.0")
			run := &packs.Runner{M: pr.pm}
			var row *struct{ phase, reason string }
			for _, s := range run.Rows() {
				if s.ID == pID {
					row = &struct{ phase, reason string }{s.Phase, s.Conditions[0].Reason}
				}
			}
			if row == nil {
				t.Fatal("no row")
			}
			if deleted {
				if row.phase != components.PhaseFailed || row.reason != packs.ReasonPackMissing {
					t.Fatalf("row %+v, want failed pack_missing", *row)
				}
				return
			}
			if cur := pr.pm.Layout.Current(pID); cur != from || !pr.pm.Installed(pID) {
				t.Fatalf("current %q installed %v, want the kept %s", cur, pr.pm.Installed(pID), from)
			}
			if row.phase != components.PhaseOff {
				t.Fatalf("row %+v", *row)
			}
			// the new release's folder stays until a commit prunes it
			if !isDirT(filepath.Join(pr.root, pID, folder)) {
				t.Fatal("the new release's folder went at the rollback")
			}
		})
	}
}

// T08: a joined machine fetches the new pack through its home's mirror first (the
// source's client); every file is verified here all the same.
func TestPacksFromTheHomesMirror(t *testing.T) {
	pr := newPackRig(t)
	pr.hold(t, pID, "1.4.0")
	assets, folder := pr.publish(t, "v1.5.0", pubOpts{})
	gh := map[string]string{}
	for n := range assets {
		gh[n] = "https://github.com/" + testRepo + "/releases/download/v1.5.0/" + n // what the job names
	}
	var mu sync.Mutex
	var viaHome []string
	src := pr.rs.source()
	src.HTTP = &http.Client{Transport: roundTrip(func(req *http.Request) (*http.Response, error) {
		// the home's mirror answers the release's files (homemirror.Transport)
		mu.Lock()
		viaHome = append(viaHome, req.URL.Path)
		mu.Unlock()
		name := filepath.Base(req.URL.Path)
		u, _ := http.NewRequestWithContext(req.Context(), http.MethodGet, assets[name], nil)
		for k, v := range req.Header {
			u.Header[k] = v
		}
		return http.DefaultTransport.RoundTrip(u)
	})}
	p := pr.packsOf(src)
	j := &Journal{Job: packJob("v1.5.0", gh)}
	detail, staged, err := p.stage(context.Background(), j)
	if err != nil {
		t.Fatal(err)
	}
	if len(staged) != 1 || staged[0].Dir != folder || !strings.Contains(detail, folder) {
		t.Fatalf("staged %+v detail %q", staged, detail)
	}
	mu.Lock()
	defer mu.Unlock()
	if !containsSub(viaHome, ".tar.zst") || !containsSub(viaHome, "components-1.5.0.json") {
		t.Fatalf("not through the home: %v", viaHome)
	}
	// a pack the home changed is refused here
	pr2 := newPackRig(t)
	pr2.hold(t, pID, "1.4.0")
	assets2, _ := pr2.publish(t, "v1.5.0", pubOpts{corrupt: true})
	_, _, err = pr2.packsOf(pr2.rs.source()).stage(context.Background(), &Journal{Job: packJob("v1.5.0", assets2)})
	var f *Failure
	if !asFailure(err, &f) || f.Code != "pack_verify_failed" {
		t.Fatalf("a changed pack: %v", err)
	}
}

type roundTrip func(*http.Request) (*http.Response, error)

func (f roundTrip) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

func containsSub(s []string, sub string) bool {
	for _, x := range s {
		if strings.Contains(x, sub) {
			return true
		}
	}
	return false
}

func asFailure(err error, f **Failure) bool {
	if err == nil {
		return false
	}
	ff, ok := err.(*Failure)
	if ok {
		*f = ff
	}
	return ok
}

// An install of the same component under way (its lock held) is waited for, then the
// step fails components_busy.
func TestPacksStepWaitsForAnInstall(t *testing.T) {
	pr := newPackRig(t)
	pr.hold(t, pID, "1.4.0")
	assets, _ := pr.publish(t, "v1.5.0", pubOpts{})
	unlock, err := pr.pm.Layout.Lock(context.Background(), pID)
	if err != nil {
		t.Fatal(err)
	}
	defer unlock()
	p := pr.packsOf(pr.rs.source())
	p.Wait = 700 * time.Millisecond
	_, _, err = p.stage(context.Background(), &Journal{Job: packJob("v1.5.0", assets)})
	var f *Failure
	if !asFailure(err, &f) || f.Code != "components_busy" {
		t.Fatalf("err %v", err)
	}
}
