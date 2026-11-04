package packs

import (
	"archive/tar"
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/klauspost/compress/zstd"

	"github.com/Hamza-Labs-Core/Maktaba/shared/components"
	"github.com/Hamza-Labs-Core/Maktaba/shared/components/store"
	"github.com/Hamza-Labs-Core/Maktaba/shared/go/componentpack"
	rv "github.com/Hamza-Labs-Core/Maktaba/shared/go/releaseverify"
)

func wantReason(t *testing.T, err error, reason string) {
	t.Helper()
	if ReasonOf(err) != reason {
		t.Fatalf("got %v (reason %q), want %s", err, ReasonOf(err), reason)
	}
}

func (f *fixture) installedPack(t *testing.T) string {
	t.Helper()
	cur := f.m.Layout.Current("runtime.ml")
	want := PackFolder(tVersion, hexOf(f.pack))
	if cur != want {
		t.Fatalf("current = %q, want %q\n%s", cur, want, f.log.String())
	}
	if !exists(filepath.Join(f.root, "runtime.ml", cur, "wheels", "index.json")) {
		t.Fatal("the pack's files are not in its folder")
	}
	if !f.m.Installed("runtime.ml") {
		t.Fatal("Installed is false after the install")
	}
	return cur
}

// The install of a pack: index verified, downloaded to .partial, checked, unpacked into
// staging, prepared there, renamed, current written; the partial file goes.
func TestInstallPack(t *testing.T) {
	f := newFixture(t, fixtureOpts{})
	var prepared string
	f.m.Prepare = map[string]PrepareFunc{"runtime.ml": func(_ context.Context, _, dir string, report func(int)) error {
		prepared = dir
		report(50)
		if !exists(filepath.Join(dir, "wheels", "index.json")) {
			return errors.New("no wheels in staging")
		}
		return nil
	}}
	if err := f.m.Install(context.Background(), "runtime.ml", nil); err != nil {
		t.Fatalf("%v\n%s", err, f.log.String())
	}
	cur := f.installedPack(t)
	if filepath.Base(prepared) != stagingPre+cur {
		t.Fatalf("prepared %s, want the staging folder of %s", prepared, cur)
	}
	if exists(prepared) || exists(filepath.Join(f.m.Layout.PartialDir(), f.index.Packs[0].Name)) {
		t.Fatal("staging or the download was left behind")
	}
	if p, ok := f.m.Progress("runtime.ml"); !ok || p.Busy || p.Phase != "" || p.Percent != 100 {
		t.Fatalf("progress after the install: %+v", p)
	}
	// installed already: nothing downloaded again
	n := len(f.reqs(".tar.zst"))
	if err := f.m.Install(context.Background(), "runtime.ml", nil); err != nil || len(f.reqs(".tar.zst")) != n {
		t.Fatalf("a second install: %v, %d downloads", err, len(f.reqs(".tar.zst"))-n)
	}
}

// T04: a download cut at 40 % fails download_failed and keeps what it got; the next
// install asks for the rest with Range, verifies and installs.
func TestInstallResumesACutDownload(t *testing.T) {
	f := newFixture(t, fixtureOpts{})
	cut := int64(len(f.pack)) * 4 / 10
	f.cutAt = cut
	err := f.m.Install(context.Background(), "runtime.ml", nil)
	wantReason(t, err, ReasonDownloadFailed)
	partial := filepath.Join(f.m.Layout.PartialDir(), f.index.Packs[0].Name)
	if info, err := os.Stat(partial); err != nil || info.Size() != cut {
		t.Fatalf("the partial file after the cut: %v %v", info, err)
	}
	if p, _ := f.m.Progress("runtime.ml"); p.Phase != components.PhaseFailed || p.Reason != ReasonDownloadFailed {
		t.Fatalf("progress: %+v", p)
	}
	if err := f.m.Install(context.Background(), "runtime.ml", nil); err != nil {
		t.Fatalf("%v\n%s", err, f.log.String())
	}
	got := f.reqs(".tar.zst")
	if len(got) != 2 || !strings.HasSuffix(got[1], "bytes="+strconv.FormatInt(cut, 10)+"-") {
		t.Fatalf("pack requests: %q", got)
	}
	f.installedPack(t)
}

// T05: a pack whose bytes are not the index's: verify_failed, the download deleted,
// nothing unpacked.
func TestInstallRefusesAPackOfAnotherDigest(t *testing.T) {
	f := newFixture(t, fixtureOpts{})
	bad := append([]byte(nil), f.pack...)
	bad[len(bad)/2] ^= 0xff
	f.served[f.index.Packs[0].Name] = bad
	err := f.m.Install(context.Background(), "runtime.ml", nil)
	wantReason(t, err, ReasonVerifyFailed)
	if exists(filepath.Join(f.m.Layout.PartialDir(), f.index.Packs[0].Name)) {
		t.Fatal("the bad download was kept")
	}
	if f.m.Layout.Current("runtime.ml") != "" || len(f.m.Layout.Folders("runtime.ml")) != 0 {
		t.Fatal("something was unpacked")
	}
}

// T06: too little free space: disk_space before any download.
func TestInstallRefusesWithoutRoom(t *testing.T) {
	f := newFixture(t, fixtureOpts{})
	f.m.FreeDisk = func(string) uint64 { return 10 }
	err := f.m.Install(context.Background(), "runtime.ml", nil)
	wantReason(t, err, ReasonDiskSpace)
	if need, _ := ParamsOf(err)["need"].(int64); need < int64(len(f.pack)) {
		t.Fatalf("need = %v", ParamsOf(err))
	}
	if n := len(f.reqs(".tar.zst")); n != 0 {
		t.Fatalf("%d pack downloads before the space check", n)
	}
}

// The index's own refusals: a release before 51.15, an expired index, a replayed one.
func TestInstallIndexRefusals(t *testing.T) {
	f := newFixture(t, fixtureOpts{noIndex: true})
	wantReason(t, f.m.Install(context.Background(), "runtime.ml", nil), ReasonIndexMissing)

	f = newFixture(t, fixtureOpts{})
	f.m.Now = func() time.Time { return f.index.Expires.Add(time.Hour) }
	wantReason(t, f.m.Install(context.Background(), "runtime.ml", nil), ReasonIndexExpired)

	f = newFixture(t, fixtureOpts{})
	seen := rv.IndexWatermarkFile{Path: f.m.Watermark}
	if err := seen.Commit(tVersion, f.index.Published.Add(time.Hour), strings.Repeat("ef", 32)); err != nil {
		t.Fatal(err)
	}
	wantReason(t, f.m.Install(context.Background(), "runtime.ml", nil), ReasonIndexReplayed)

	f = newFixture(t, fixtureOpts{edit: func(i *rv.ComponentsIndex) {
		i.Packs[0].Platform = "darwin-arm64"
		i.Packs[0].Name = componentpack.FileName("runtime.ml", tVersion, "darwin-arm64")
	}})
	wantReason(t, f.m.Install(context.Background(), "runtime.ml", nil), ReasonPackMissing)
}

// a pack signed in the index whose tar climbs out of its folder (T01 through the
// installer): unpack_refused, staging gone, nothing written outside it.
func TestInstallRefusesAnUnsafePack(t *testing.T) {
	body := []byte("x")
	sums := componentpack.FormatSums([]componentpack.Entry{{Kind: 'f', Mode: 0o644, Size: 1, SHA256: hexOf(body), Path: "a"}})
	man := []byte(`{"schema":1,"id":"runtime.ml","version":"` + tVersion + `","platform":"` + tPlatform +
		`","files_sha256":"` + hexOf(sums) + `","unpacked_bytes":1}`)
	var buf bytes.Buffer
	zw, _ := zstd.NewWriter(&buf)
	tw := tar.NewWriter(zw)
	for _, e := range []struct {
		name string
		b    []byte
	}{{componentpack.ManifestName, man}, {componentpack.SumsName, sums}, {"../../escape", body}} {
		if err := tw.WriteHeader(&tar.Header{Name: e.name, Typeflag: tar.TypeReg, Mode: 0o644, Size: int64(len(e.b))}); err != nil {
			t.Fatal(err)
		}
		_, _ = tw.Write(e.b)
	}
	_ = tw.Close()
	_ = zw.Close()
	f := newFixture(t, fixtureOpts{pack: buf.Bytes()})
	err := f.m.Install(context.Background(), "runtime.ml", nil)
	wantReason(t, err, ReasonUnpackRefused)
	if e, _ := ParamsOf(err)["entry"].(string); !strings.Contains(e, "escape") {
		t.Fatalf("entry = %v", ParamsOf(err))
	}
	if exists(filepath.Join(f.root, "escape")) || exists(filepath.Join(filepath.Dir(f.root), "escape")) {
		t.Fatal("a file was written outside staging")
	}
	if len(f.m.Layout.Folders("runtime.ml")) != 0 || f.m.Layout.Current("runtime.ml") != "" {
		t.Fatal("staging or a folder was left")
	}
	entries, _ := os.ReadDir(f.m.Layout.Dir("runtime.ml"))
	for _, e := range entries {
		if strings.HasPrefix(e.Name(), stagingPre) {
			t.Fatalf("staging left: %s", e.Name())
		}
	}
}

// T07: a crash between the rename and the pointer (and one mid-unpack): the boot points
// current at this release's folder and deletes staging; a kept folder of another
// release is left alone.
func TestRecoverAfterACrash(t *testing.T) {
	l := Layout{Root: t.TempDir()}
	for _, d := range []string{"runtime.ml/0.4.2_3f9a1c2b7d10", "runtime.ml/0.4.1_a01b55e9c3d4", "runtime.ml/.staging-0.4.2_ffffffffffff/wheels"} {
		if err := os.MkdirAll(filepath.Join(l.Root, filepath.FromSlash(d)), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	if err := l.SetCurrent("runtime.ml", "0.4.1_a01b55e9c3d4"); err != nil {
		t.Fatal(err)
	}
	did := l.Recover("0.4.2")
	if got := l.Current("runtime.ml"); got != "0.4.2_3f9a1c2b7d10" {
		t.Fatalf("current = %q (%v)", got, did)
	}
	if exists(filepath.Join(l.Root, "runtime.ml", ".staging-0.4.2_ffffffffffff")) {
		t.Fatal("staging was not deleted")
	}
	if !exists(filepath.Join(l.Root, "runtime.ml", "0.4.1_a01b55e9c3d4")) {
		t.Fatal("the kept release's folder was deleted")
	}
	// a rollback to 0.4.1 points back at its own
	l.Recover("0.4.1")
	if got := l.Current("runtime.ml"); got != "0.4.1_a01b55e9c3d4" {
		t.Fatalf("after a rollback current = %q", got)
	}
}

// T09: a Full home whose venv came from PyPI before packs: installed, nothing downloaded.
func TestFullHomeCountsAsInstalled(t *testing.T) {
	f := newFixture(t, fixtureOpts{})
	f.m.OnDisk = func(id string) bool { return id == "runtime.ml" }
	if err := f.m.Install(context.Background(), "runtime.ml", nil); err != nil {
		t.Fatal(err)
	}
	if n := len(f.reqs("")); n != 0 {
		t.Fatalf("%d requests for an asset on disk", n)
	}
	r := &Runner{M: f.m, Machine: store.Home}
	r.ctx = context.Background()
	r.Desired(map[string]store.Desired{"runtime.ml": {ID: "runtime.ml", Want: components.WantInstalled, Generation: 1}})
	time.Sleep(50 * time.Millisecond)
	if n := len(f.reqs("")); n != 0 || f.m.Busy("runtime.ml") {
		t.Fatalf("the runner acted on an installed asset: %d requests", n)
	}
	for _, row := range r.Rows() {
		if row.ID == "runtime.ml" && (row.Phase != components.PhaseOff || row.Conditions[0].Status != "true") {
			t.Fatalf("runtime.ml's row: %+v", row)
		}
	}
}

// A release whose signed files are not there (not published yet) is pack_not_published
// naming the asset, not a download cut short (download_failed); the row says so.
func TestAReleaseNotPublishedIsSaidSo(t *testing.T) {
	f := newFixture(t, fixtureOpts{})
	delete(f.served, rv.ChecksumsFile)
	err := f.m.Install(context.Background(), "runtime.ml", nil)
	if ReasonOf(err) != ReasonPackNotPublished || ParamsOf(err)["name"] != "runtime.ml" {
		t.Fatalf("install from a release not published: %v (%s %v)", err, ReasonOf(err), ParamsOf(err))
	}
	r := &Runner{M: f.m, Machine: store.Home}
	for _, row := range r.Rows() {
		if row.ID == "runtime.ml" && (row.Phase != components.PhaseFailed || row.Reason() != ReasonPackNotPublished) {
			t.Fatalf("runtime.ml's row: %+v", row)
		}
	}
	// the catalog lists it (its words, components.reason.pack_not_published)
	if !slices.Contains(components.Default().Reasons, ReasonPackNotPublished) {
		t.Fatal("the catalog does not list pack_not_published")
	}
}

// Story 51.14: an installed pack's row carries its folder's size on disk (disk_mb); an
// asset from before packs (no folder) carries none, the catalog's size stands for it.
func TestInstalledPackRowCarriesItsDiskSize(t *testing.T) {
	f := newFixture(t, fixtureOpts{})
	r := &Runner{M: f.m, Machine: store.Home}
	if err := f.m.Install(context.Background(), "runtime.ml", nil); err != nil {
		t.Fatal(err)
	}
	dir := filepath.Join(f.m.Layout.Dir("runtime.ml"), f.m.Layout.Current("runtime.ml"))
	if err := os.WriteFile(filepath.Join(dir, "big.bin"), make([]byte, 3<<20), 0o644); err != nil {
		t.Fatal(err)
	}
	f.m.OnDisk = func(id string) bool { return id == "tool.yt-dlp" }
	got := map[string]any{}
	for _, row := range r.Rows() {
		got[row.ID] = row.Detail["disk_mb"]
	}
	if got["runtime.ml"] != int64(4) || got["tool.yt-dlp"] != nil {
		t.Fatalf("disk_mb runtime.ml %v (want 4: 3 MB and the pack's files), tool.yt-dlp %v", got["runtime.ml"], got["tool.yt-dlp"])
	}
}

// T08 (the check the routes use): an enabled component that needs the asset holds it;
// another installed model of an "any:" group frees it.
func TestRequiredBy(t *testing.T) {
	cat := components.Default()
	desired := map[string]store.Desired{"transcription": {ID: "transcription", Want: components.WantEnabled}}
	if got := RequiredBy(cat, "runtime.ml", desired, nil); len(got) == 0 || got[0] != "transcription" {
		t.Fatalf("runtime.ml required by %v", got)
	}
	reqs := cat.Requires("transcription")
	var group []string
	for _, r := range reqs {
		if len(r.AnyOf) > 1 {
			group = r.AnyOf
		}
	}
	if len(group) < 2 {
		t.Skip("transcription has no any: group in this catalog")
	}
	other := func(id string) bool { return id == group[1] }
	if got := RequiredBy(cat, group[0], desired, other); len(got) != 0 {
		t.Fatalf("%s required by %v though %s is installed", group[0], got, group[1])
	}
	desired["transcription"] = store.Desired{ID: "transcription", Want: components.WantInstalled}
	if got := RequiredBy(cat, "runtime.ml", desired, nil); len(got) != 0 {
		t.Fatalf("a disabled dependent holds it: %v", got)
	}
}

// A model goes into the Hugging Face cache's snapshot, its record in rev_<12>/.
func TestInstallModel(t *testing.T) {
	f := newFixture(t, fixtureOpts{})
	f.m.OnDisk = func(id string) bool { return id == "runtime.ml" }
	if err := f.m.Install(context.Background(), "model.whisper-turbo", nil); err != nil {
		t.Fatalf("%v\n%s", err, f.log.String())
	}
	snap := SnapshotDir(f.hub, tWhisperRepo, tWhisperRev)
	for p, b := range f.model {
		got, err := os.ReadFile(filepath.Join(snap, filepath.FromSlash(p)))
		if err != nil || !bytes.Equal(got, b) {
			t.Fatalf("%s in the snapshot: %v", p, err)
		}
	}
	if ref, _ := os.ReadFile(filepath.Join(f.hub, hfRepoDir(tWhisperRepo), "refs", "main")); string(ref) != tWhisperRev {
		t.Fatalf("refs/main = %q", ref)
	}
	if f.m.Layout.Current("model.whisper-turbo") != RevFolder(tWhisperRev) ||
		!exists(filepath.Join(f.root, "model.whisper-turbo", RevFolder(tWhisperRev), ModelRecordFile)) {
		t.Fatal("no model record")
	}
	if !f.m.Installed("model.whisper-turbo") {
		t.Fatal("not installed")
	}
	// removed: the repo's cache and the record go
	if err := f.m.Remove(context.Background(), "model.whisper-turbo"); err != nil {
		t.Fatal(err)
	}
	if exists(filepath.Join(f.hub, hfRepoDir(tWhisperRepo))) || f.m.Installed("model.whisper-turbo") {
		t.Fatal("the model is still there")
	}
}

// A model file that does not match: verify_failed, not in the cache.
func TestInstallModelRefusesABadFile(t *testing.T) {
	f := newFixture(t, fixtureOpts{})
	f.m.OnDisk = func(id string) bool { return id == "runtime.ml" }
	good := f.model["sub/model.bin"]
	f.model["sub/model.bin"] = bytes.Repeat([]byte("x"), len(good))
	err := f.m.Install(context.Background(), "model.whisper-turbo", nil)
	wantReason(t, err, ReasonVerifyFailed)
	if exists(filepath.Join(SnapshotDir(f.hub, tWhisperRepo, tWhisperRev), "sub", "model.bin")) {
		t.Fatal("a bad file is in the cache")
	}
}

// The model's requirement (runtime.ml) is installed first.
func TestInstallRequirementsFirst(t *testing.T) {
	f := newFixture(t, fixtureOpts{})
	if err := f.m.Install(context.Background(), "model.whisper-turbo", nil); err != nil {
		t.Fatalf("%v\n%s", err, f.log.String())
	}
	f.installedPack(t)
	if !f.m.Installed("model.whisper-turbo") {
		t.Fatal("the model is not installed")
	}
	if n := len(f.reqs("/" + rv.ChecksumsFile + " ")); n != 1 {
		t.Fatalf("the index was fetched %d times for one install", n)
	}
}

// Remove: this release's folders and the pointer go, the kept release's stay, Cleanup
// runs while the files are there; a pack not installed is not Installed after.
func TestRemovePack(t *testing.T) {
	f := newFixture(t, fixtureOpts{})
	if err := f.m.Install(context.Background(), "runtime.ml", nil); err != nil {
		t.Fatal(err)
	}
	cur := f.installedPack(t)
	kept := filepath.Join(f.root, "runtime.ml", "0.4.1_a01b55e9c3d4")
	if err := os.MkdirAll(kept, 0o755); err != nil {
		t.Fatal(err)
	}
	sawFiles := false
	f.m.Cleanup = map[string]CleanupFunc{"runtime.ml": func(context.Context, string) error {
		sawFiles = exists(filepath.Join(f.root, "runtime.ml", cur, "wheels"))
		return nil
	}}
	if err := f.m.Remove(context.Background(), "runtime.ml"); err != nil {
		t.Fatal(err)
	}
	if !sawFiles {
		t.Fatal("Cleanup ran after the files were deleted")
	}
	if exists(filepath.Join(f.root, "runtime.ml", cur)) || exists(filepath.Join(f.root, "runtime.ml", CurrentFile)) {
		t.Fatal("this release's pack is still there")
	}
	if !exists(kept) {
		t.Fatal("the kept release's folder was deleted")
	}
	if f.m.Installed("runtime.ml") || f.m.Layout.Removing("runtime.ml") || !f.m.Layout.Removed("runtime.ml") {
		t.Fatal("removal not final")
	}
	// installed again: the removed mark goes
	if err := f.m.Install(context.Background(), "runtime.ml", nil); err != nil {
		t.Fatal(err)
	}
	if f.m.Layout.Removed("runtime.ml") {
		t.Fatal("the removed mark stayed after an install")
	}
}

// A removal cut short (.removing left) is not installed, and the runner finishes it.
func TestRunnerFinishesARemoval(t *testing.T) {
	f := newFixture(t, fixtureOpts{})
	if err := f.m.Install(context.Background(), "runtime.ml", nil); err != nil {
		t.Fatal(err)
	}
	if err := f.m.Layout.markRemoving("runtime.ml"); err != nil {
		t.Fatal(err)
	}
	if f.m.Installed("runtime.ml") {
		t.Fatal("installed during a removal")
	}
	r := &Runner{M: f.m, Machine: store.Home}
	r.ctx = context.Background()
	r.Desired(map[string]store.Desired{"runtime.ml": {ID: "runtime.ml", Want: components.WantAbsent, Generation: 3}})
	waitIdle(t, f.m, "runtime.ml")
	if f.m.Layout.Removing("runtime.ml") || f.m.Layout.Current("runtime.ml") != "" {
		t.Fatalf("the removal was not finished\n%s", f.log.String())
	}
}

func waitIdle(t *testing.T, m *Manager, id string) {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	time.Sleep(20 * time.Millisecond)
	for m.Busy(id) {
		if time.Now().After(deadline) {
			t.Fatalf("%s still busy", id)
		}
		time.Sleep(20 * time.Millisecond)
	}
}

// The runner's rules: a model installs only on a change seen while running; a pack
// asset wanted and missing installs at once; each action once per generation.
func TestRunnerRules(t *testing.T) {
	f := newFixture(t, fixtureOpts{})
	r := &Runner{M: f.m, Machine: store.Home}
	r.ctx = context.Background()
	d := map[string]store.Desired{
		"model.whisper-turbo": {ID: "model.whisper-turbo", Want: components.WantInstalled, Generation: 1},
	}
	r.Desired(d) // the first read: a model wanted is left to the pipeline
	time.Sleep(50 * time.Millisecond)
	if f.m.Busy("model.whisper-turbo") || len(f.reqs("")) != 0 {
		t.Fatal("a model was installed from the first desired state")
	}
	d2 := map[string]store.Desired{
		"model.whisper-turbo": {ID: "model.whisper-turbo", Want: components.WantAbsent, Generation: 2},
	}
	r.Desired(d2)
	d3 := map[string]store.Desired{
		"model.whisper-turbo": {ID: "model.whisper-turbo", Want: components.WantInstalled, Generation: 3},
	}
	r.Desired(d3) // seen to change to installed
	waitIdle(t, f.m, "model.whisper-turbo")
	if !f.m.Installed("model.whisper-turbo") || !f.m.Installed("runtime.ml") {
		t.Fatalf("not installed after a change to installed\n%s", f.log.String())
	}

	// a pack wanted and missing: at once, also from the first read; a failure once
	g := newFixture(t, fixtureOpts{noIndex: true})
	rg := &Runner{M: g.m, Machine: store.Home}
	rg.ctx = context.Background()
	want := map[string]store.Desired{"runtime.ml": {ID: "runtime.ml", Want: components.WantInstalled, Generation: 5}}
	rg.Desired(want)
	waitIdle(t, g.m, "runtime.ml")
	n := len(g.reqs("/" + rv.ChecksumsFile + " "))
	if n != 1 {
		t.Fatalf("%d tries", n)
	}
	rg.Desired(want)
	waitIdle(t, g.m, "runtime.ml")
	if len(g.reqs("/"+rv.ChecksumsFile+" ")) != n {
		t.Fatal("a failed install was tried again in the same generation")
	}
	var row store.Status
	for _, x := range rg.Rows() {
		if x.ID == "runtime.ml" {
			row = x
		}
	}
	if row.Phase != components.PhaseFailed || row.Conditions[0].Reason != ReasonIndexMissing {
		t.Fatalf("row: %+v", row)
	}
	// Off: nothing at all
	h := newFixture(t, fixtureOpts{})
	rh := &Runner{M: h.m, Machine: store.Home, Off: true}
	rh.ctx = context.Background()
	rh.Desired(want)
	time.Sleep(50 * time.Millisecond)
	if len(h.reqs("")) != 0 {
		t.Fatal("reconcile = off acted")
	}
}

// packs = off: installs are refused, what is on disk is reported.
func TestPacksDisabled(t *testing.T) {
	f := newFixture(t, fixtureOpts{})
	f.m.Disabled = true
	wantReason(t, f.m.Install(context.Background(), "runtime.ml", nil), ReasonPacksDisabled)
	if len(f.reqs("")) != 0 {
		t.Fatal("a disabled install downloaded")
	}
	f.m.OnDisk = func(id string) bool { return id == "tool.yt-dlp" }
	if !f.m.Installed("tool.yt-dlp") {
		t.Fatal("what is on disk is not reported")
	}
}

// Two installs of one asset at once in a process: the second is busy.
func TestInstallBusy(t *testing.T) {
	f := newFixture(t, fixtureOpts{})
	block := make(chan struct{})
	f.m.Prepare = map[string]PrepareFunc{"runtime.ml": func(context.Context, string, string, func(int)) error {
		<-block
		return nil
	}}
	done := make(chan error, 1)
	go func() { done <- f.m.Install(context.Background(), "runtime.ml", nil) }()
	deadline := time.Now().Add(5 * time.Second)
	for {
		if p, _ := f.m.Progress("runtime.ml"); p.Detail == DetailPreparing {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("never prepared")
		}
		time.Sleep(10 * time.Millisecond)
	}
	wantReason(t, f.m.Install(context.Background(), "runtime.ml", nil), ReasonBusy)
	close(block)
	if err := <-done; err != nil {
		t.Fatal(err)
	}
}

// T11: an offline file made from the network installs on a home with none, exactly as
// online; a file of another version or platform is refused first.
// The platform names the offline file on disk, so an unknown one (a path) is refused.
func TestOfflineUnknownPlatform(t *testing.T) {
	f := newFixture(t, fixtureOpts{})
	for _, plat := range []string{"../../etc", "linux-amd64/../x", "plan9-386"} {
		if _, err := f.m.OfflineItems(OfflineRequest{Bundle: "full", Platform: plat}); err == nil {
			t.Fatalf("platform %q accepted", plat)
		}
	}
	if _, err := f.m.OfflineItems(OfflineRequest{Bundle: "full", Platform: "linux-amd64"}); err != nil {
		t.Fatal(err)
	}
}

func TestOfflineFileAndImport(t *testing.T) {
	f := newFixture(t, fixtureOpts{})
	var buf bytes.Buffer
	items, err := f.m.MakeOffline(context.Background(), &buf, OfflineRequest{Components: []string{"model.whisper-turbo"}})
	if err != nil {
		t.Fatalf("%v\n%s", err, f.log.String())
	}
	if strings.Join(items, ",") != "runtime.ml,model.whisper-turbo" {
		t.Fatalf("items %v", items)
	}
	// a home with no network
	g := newFixture(t, fixtureOpts{})
	g.m.Net, g.m.Trusted = nil, f.m.Trusted
	ids, err := g.m.ImportFrom(context.Background(), bytes.NewReader(buf.Bytes()))
	if err != nil {
		t.Fatalf("%v\n%s", err, g.log.String())
	}
	if strings.Join(ids, ",") != "runtime.ml,model.whisper-turbo" || !g.m.Installed("runtime.ml") || !g.m.Installed("model.whisper-turbo") {
		t.Fatalf("imported %v", ids)
	}
	if len(g.reqs("")) != 0 {
		t.Fatal("the import used the network")
	}
	if exists(filepath.Join(g.m.Layout.PartialDir(), ".import")) {
		t.Fatal("the import's files were left")
	}
	// another version, another platform
	h := newFixture(t, fixtureOpts{})
	h.m.Version = "0.4.3"
	_, err = h.m.ImportFrom(context.Background(), bytes.NewReader(buf.Bytes()))
	wantReason(t, err, ReasonOfflineWrongVersion)
	h.m.Version, h.m.Platform = tVersion, "darwin-arm64"
	_, err = h.m.ImportFrom(context.Background(), bytes.NewReader(buf.Bytes()))
	wantReason(t, err, ReasonOfflineWrongPlat)
	// a file whose pack was changed after it was made: verify_failed, nothing installed
	bad := tamper(t, buf.Bytes(), "packs/")
	k := newFixture(t, fixtureOpts{})
	k.m.Net, k.m.Trusted = nil, f.m.Trusted
	_, err = k.m.ImportFrom(context.Background(), bytes.NewReader(bad))
	wantReason(t, err, ReasonVerifyFailed)
	if k.m.Installed("runtime.ml") {
		t.Fatal("a changed pack was installed")
	}
}

// tamper flips a byte of the first entry under prefix.
func tamper(t *testing.T, b []byte, prefix string) []byte {
	t.Helper()
	tr := tar.NewReader(bytes.NewReader(b))
	var out bytes.Buffer
	tw := tar.NewWriter(&out)
	done := false
	for {
		h, err := tr.Next()
		if err != nil {
			break
		}
		var body bytes.Buffer
		_, _ = body.ReadFrom(tr)
		data := body.Bytes()
		if !done && strings.HasPrefix(h.Name, prefix) {
			data[len(data)/2] ^= 0xff
			done = true
		}
		_ = tw.WriteHeader(h)
		_, _ = tw.Write(data)
	}
	_ = tw.Close()
	return out.Bytes()
}

// An offline file that holds anything else (a path that climbs out) is refused.
func TestImportRefusesStrangeEntries(t *testing.T) {
	f := newFixture(t, fixtureOpts{})
	var out bytes.Buffer
	tw := tar.NewWriter(&out)
	man := []byte(`{"schema":1,"version":"` + tVersion + `","platform":"` + tPlatform + `","items":[]}`)
	_ = tw.WriteHeader(&tar.Header{Name: OfflineManifestName, Typeflag: tar.TypeReg, Mode: 0o644, Size: int64(len(man))})
	_, _ = tw.Write(man)
	_ = tw.WriteHeader(&tar.Header{Name: "models/x/../../../escape", Typeflag: tar.TypeReg, Mode: 0o644, Size: 1})
	_, _ = tw.Write([]byte("x"))
	_ = tw.Close()
	_, err := f.m.ImportFrom(context.Background(), &out)
	wantReason(t, err, ReasonUnpackRefused)
	if exists(filepath.Join(filepath.Dir(f.root), "escape")) {
		t.Fatal("written outside")
	}
}

// A name the layout joins is one element of its folder: an id, a version or a digest
// with "..", a separator or a volume stays inside Root.
func TestLayoutNamesStayInside(t *testing.T) {
	root := filepath.Join(t.TempDir(), "components")
	l := Layout{Root: root}
	for _, id := range []string{"../../etc", "a/../../b", `..\x`, "/abs"} {
		d := l.Dir(id)
		if filepath.Dir(d) != root {
			t.Errorf("Dir(%q) = %q, not one element of %q", id, d, root)
		}
	}
	if d := l.Dir("model.whisper-turbo"); d != filepath.Join(root, "model.whisper-turbo") {
		t.Errorf("a catalog id moved: %q", d)
	}
	for _, f := range []string{PackFolder("../1.0", "abc"), PackFolder("1.0", "../../x"), RevFolder("../r")} {
		if f != filepath.Base(f) || f == ".." {
			t.Errorf("folder %q is not one element", f)
		}
	}
	if f := PackFolder("0.4.2", "0123456789abcdef"); f != "0.4.2_0123456789ab" {
		t.Errorf("PackFolder = %q", f)
	}
}

// Import opens only a file or folder named absolutely, with no "..".
func TestImportRefusesOtherPaths(t *testing.T) {
	m := &Manager{}
	for _, p := range []string{"offline.tar", "../offline.tar", "/srv/../etc/offline.tar", filepath.Join("a", "b")} {
		_, err := m.Import(context.Background(), p)
		if err == nil || !strings.Contains(err.Error(), "not an absolute path") && !strings.Contains(err.Error(), "a path with a .. element") {
			t.Errorf("Import(%q) = %v, want refused", p, err)
		}
	}
}

// A folder source reads only its own files: a version naming another folder is refused.
func TestFolderSourceReadsOnlyItsFiles(t *testing.T) {
	dir := t.TempDir()
	for _, n := range []string{rv.ChecksumsFile, rv.BundleFile, rv.ManifestFile} {
		if err := os.WriteFile(filepath.Join(dir, n), []byte("x"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	f := &FolderSource{Dir: dir}
	if _, err := f.Signed(context.Background(), "1.2.3"); err != nil {
		t.Fatalf("a plain version: %v", err)
	}
	for _, v := range []string{"../../etc/x", "1.2.3/../../x", `1.2.3\..\x`} {
		if _, err := f.Signed(context.Background(), v); err == nil || !strings.Contains(err.Error(), "not a file of the release's folder") {
			t.Errorf("Signed(%q) = %v, want refused", v, err)
		}
	}
}
