package packs

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	rv "github.com/Hamza-Labs-Core/Maktaba/shared/go/releaseverify"
)

// holdModel leaves id installed at rev (its rev_ folder and its record), as 51.16 does.
func (f *fixture) holdModel(id, repo, rev string) string {
	f.t.Helper()
	folder := RevFolder(rev)
	dir := filepath.Join(f.root, id, folder)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		f.t.Fatal(err)
	}
	b, _ := json.Marshal(modelRecord{ID: id, Repo: repo, Revision: rev, Files: []rv.IndexFile{}})
	if err := os.WriteFile(filepath.Join(dir, ModelRecordFile), b, 0o644); err != nil {
		f.t.Fatal(err)
	}
	snap := SnapshotDir(f.hub, repo, rev)
	_ = os.MkdirAll(snap, 0o755)
	_ = os.WriteFile(filepath.Join(snap, "config.json"), []byte(`{"old":true}`), 0o644)
	if err := f.m.Layout.SetCurrent(id, folder); err != nil {
		f.t.Fatal(err)
	}
	return folder
}

// Story 51.18: an update to a release whose index pins a model's new revision fetches it
// beside the one in use, which stays for a rollback; the new release's boot points current
// at the revision its catalog pins; a prune keeps the revisions a kept release pins.
func TestUpdateStagesAModelsNewRevision(t *testing.T) {
	f := newFixture(t, fixtureOpts{})
	f.m.Version = "0.4.1" // the running release; the fixture's release (0.4.2) is the next
	oldRev := strings.Repeat("1", 40)
	old := f.holdModel("model.whisper-turbo", tWhisperRepo, oldRev)
	ctx := context.Background()
	plan, err := f.m.PlanUpdate(ctx, tVersion, f.m.Net)
	if err != nil {
		t.Fatal(err)
	}
	var want int64
	for _, b := range f.model {
		want += int64(len(b))
	}
	if plan.Need != want {
		t.Fatalf("need %d, want %d", plan.Need, want)
	}
	staged, err := f.m.StageUpdate(ctx, plan, f.m.Net)
	if err != nil {
		t.Fatal(err)
	}
	folder := RevFolder(tWhisperRev)
	if len(staged) != 1 || staged[0].Dir != folder || staged[0].From != old || !staged[0].Created || staged[0].Kind != HeldModel {
		t.Fatalf("staged %+v", staged)
	}
	if cur := f.m.Layout.Current("model.whisper-turbo"); cur != old {
		t.Fatalf("current %q moved before the switch", cur)
	}
	for p, b := range f.model {
		got, err := os.ReadFile(filepath.Join(SnapshotDir(f.hub, tWhisperRepo, tWhisperRev), filepath.FromSlash(p)))
		if err != nil || string(got) != string(b) {
			t.Fatalf("%s: %v", p, err)
		}
	}
	if vs := versionsOf(filepath.Join(f.root, "model.whisper-turbo", old)); !slices.Equal(vs, []string{"0.4.1"}) {
		t.Fatalf("the revision in use lists %v", vs)
	}
	if vs := versionsOf(filepath.Join(f.root, "model.whisper-turbo", folder)); !slices.Equal(vs, []string{tVersion}) {
		t.Fatalf("the new revision lists %v", vs)
	}
	// the next release (its catalog pins tWhisperRev) boots
	f.m.Version = tVersion
	if did := f.m.RecoverModels(); len(did) != 1 {
		t.Fatalf("recover did %v", did)
	}
	if cur := f.m.Layout.Current("model.whisper-turbo"); cur != folder {
		t.Fatalf("current %q, want %q", cur, folder)
	}
	// both releases kept: both revisions stay
	if removed, errs := f.m.Prune([]string{"0.4.1", tVersion}); len(removed) != 0 || len(errs) != 0 {
		t.Fatalf("pruned %v %v", removed, errs)
	}
	// 0.4.1 no longer kept: its revision and its snapshot go
	removed, errs := f.m.Prune([]string{tVersion})
	if len(errs) != 0 || !slices.Equal(removed, []string{"model.whisper-turbo/" + old}) {
		t.Fatalf("pruned %v %v", removed, errs)
	}
	if exists(SnapshotDir(f.hub, tWhisperRepo, oldRev)) {
		t.Fatal("the old revision's snapshot stayed")
	}
	if !exists(SnapshotDir(f.hub, tWhisperRepo, tWhisperRev)) {
		t.Fatal("the revision in use went")
	}
}

// A model whose revision does not change downloads nothing: its folder is marked as the
// next release's too.
func TestUpdateKeepsAnUnchangedModel(t *testing.T) {
	f := newFixture(t, fixtureOpts{})
	f.m.Version = "0.4.1"
	cur := f.holdModel("model.whisper-turbo", tWhisperRepo, tWhisperRev)
	ctx := context.Background()
	plan, err := f.m.PlanUpdate(ctx, tVersion, f.m.Net)
	if err != nil {
		t.Fatal(err)
	}
	if plan.Need != 0 {
		t.Fatalf("need %d", plan.Need)
	}
	staged, err := f.m.StageUpdate(ctx, plan, f.m.Net)
	if err != nil || len(staged) != 1 || staged[0].Created || staged[0].Dir != cur {
		t.Fatalf("staged %+v %v", staged, err)
	}
	if got := f.reqs("/hf/"); len(got) != 0 {
		t.Fatalf("model files fetched: %v", got)
	}
	if vs := versionsOf(filepath.Join(f.root, "model.whisper-turbo", cur)); !slices.Equal(vs, []string{"0.4.1", tVersion}) {
		t.Fatalf("versions %v", vs)
	}
}

// A pack that fails its check mid-update leaves no folder of the next release, no
// download, and the running release's folder and pointer as they were.
func TestStageUpdateAllOrNothing(t *testing.T) {
	f := newFixture(t, fixtureOpts{})
	f.m.Version = "0.4.1"
	held := "0.4.1_" + strings.Repeat("e", 12)
	if err := os.MkdirAll(filepath.Join(f.root, "runtime.ml", held), 0o755); err != nil {
		t.Fatal(err)
	}
	_ = f.m.Layout.SetCurrent("runtime.ml", held)
	name := f.index.Packs[0].Name
	bad := append([]byte(nil), f.served[name]...)
	bad[len(bad)/3] ^= 1
	f.served[name] = bad
	ctx := context.Background()
	plan, err := f.m.PlanUpdate(ctx, tVersion, f.m.Net)
	if err != nil {
		t.Fatal(err)
	}
	staged, err := f.m.StageUpdate(ctx, plan, f.m.Net)
	if ReasonOf(err) != ReasonVerifyFailed || staged != nil {
		t.Fatalf("staged %+v err %v", staged, err)
	}
	if got := f.m.Layout.Folders("runtime.ml"); !slices.Equal(got, []string{held}) {
		t.Fatalf("folders %v", got)
	}
	if f.m.Layout.Current("runtime.ml") != held {
		t.Fatal("current moved")
	}
	if ents, _ := os.ReadDir(f.m.Layout.PartialDir()); len(ents) != 0 {
		t.Fatalf("downloads left: %v", ents)
	}
}

// Nothing held: nothing is fetched, not even the index.
func TestPlanUpdateWithNothingHeld(t *testing.T) {
	f := newFixture(t, fixtureOpts{})
	plan, err := f.m.PlanUpdate(context.Background(), tVersion, f.m.Net)
	if err != nil || len(plan.Held) != 0 || plan.Need != 0 {
		t.Fatalf("plan %+v %v", plan, err)
	}
	if got := f.reqs("/"); len(got) != 0 {
		t.Fatalf("fetched %v", got)
	}
}
