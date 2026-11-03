//go:build unix

package main

// Update-rehearsal scenarios 13 and 14 (Epic 51 story 51.18; docs/update-rehearsal.md):
// an update carries the packs. A pack fits only the server version it was built with, so
// an update fetches and verifies the new release's pack of every installed component
// before its switch, beside the running release's (the payload's staging: all or
// nothing), and a rollback's release finds the pack it kept.
//
//	13  a home on this commit with the tool.yt-dlp pack installed updates to a release
//	    that fails its watch: the new pack is staged before the switch, the new release
//	    points current at it, the rollback points current back at the kept one; then an
//	    update to the release after passes (N13: the downtime budget of an update with
//	    packs is the one without), and its commit prunes the packs of the releases no
//	    longer kept.
//	14  the new release's pack fails its verification (one byte changed after signing):
//	    the update ends prepare_failed at the packs step with pack_verify_failed, before
//	    any switch; nothing on the home changed.
//
// The pack is the release pack build's own (tools/rehearsal/build.sh runs
// tools/release/packs/build.sh --only tool.yt-dlp for cur, next and after), published
// with a components index signed with the release by the fake Sigstore. The home's
// first install of it is the offline import (POST /components/import on the
// supervisor's admin): a network install would go to github.com.

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"slices"
	"sort"
	"strings"
	"sync"
	"testing"
	"time"

	rv "github.com/Hamza-Labs-Core/Maktaba/shared/go/releaseverify"
	"github.com/Hamza-Labs-Core/Maktaba/shared/go/releaseverify/releaseverifytest"
)

// rehearsalPack is the component the rehearsal's packs are of.
const rehearsalPack = "tool.yt-dlp"

// ---- the releases' packs ----

// packsKit skips the scenario when the kit has no packs (a kit of a tree from before
// story 51.15's pack build).
func (r *rehearsal) packsKit() {
	r.t.Helper()
	k := r.kit
	if k.after == "" || k.platform == "" {
		r.skipUnlessRequired("the kit has no packs (tools/rehearsal/build.sh of a tree without tools/release/packs)")
	}
	for _, v := range []string{k.cur, k.next, k.after} {
		if _, err := os.Stat(filepath.Join(k.dir, "packs", v, rv.ComponentsIndexName(v))); err != nil {
			r.skipUnlessRequired(fmt.Sprintf("the kit has no packs of %s: %v", v, err))
		}
	}
	r.packs = true
}

// afterSpec is this commit as the release after next: the additive migration of cur, a
// release that runs (scenario 13's update after the rollback).
func (k *rehearsalKit) afterSpec() payloadSpec {
	n, body := additiveMigration(k.rev + 1)
	return payloadSpec{variant: "after", version: k.after, extra: map[string]string{n: body}}
}

// attachPacks puts the kit's packs of version in the release, with their index made the
// release's: the release's commit, published an hour ago, good for IndexLifetime.
func (r *rehearsal) attachPacks(rel *releaseverifytest.FakeRelease, version string) {
	t := r.t
	t.Helper()
	dir := filepath.Join(r.kit.dir, "packs", version)
	ents, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("the kit's packs of %s: %v", version, err)
	}
	var index map[string]any
	for _, e := range ents {
		if e.IsDir() {
			continue
		}
		raw, err := os.ReadFile(filepath.Join(dir, e.Name()))
		if err != nil {
			t.Fatal(err)
		}
		if e.Name() == rv.ComponentsIndexName(version) {
			if err := json.Unmarshal(raw, &index); err != nil {
				t.Fatalf("%s: %v", e.Name(), err)
			}
			continue
		}
		rel.Files[e.Name()] = raw
	}
	if index == nil {
		t.Fatalf("the kit has no %s", rv.ComponentsIndexName(version))
	}
	pub := time.Now().UTC().Add(-time.Hour).Truncate(time.Second)
	index["commit"] = rel.Manifest["commit"]
	index["published"] = pub.Format(time.RFC3339)
	index["expires"] = pub.Add(rv.IndexLifetime).Format(time.RFC3339)
	b, err := json.MarshalIndent(index, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	rel.SetComponentsIndex(append(b, '\n'))
}

// packFiles are the release's pack files of the rehearsal's component.
func packFiles(rel *releaseverifytest.FakeRelease) []string {
	var out []string
	for n := range rel.Files {
		if strings.HasPrefix(n, rehearsalPack+"-") && strings.Contains(n, ".tar.zst") {
			out = append(out, n)
		}
	}
	sort.Strings(out)
	return out
}

// corruptPacks changes one byte of each pack file after the release was signed: its
// checksums.txt and index still list the bytes it was built with.
func (r *rehearsal) corruptPacks(rel *releaseverifytest.FakeRelease) {
	files := packFiles(rel)
	if len(files) == 0 {
		r.t.Fatalf("the release %s has no pack to corrupt", rel.Tag)
	}
	for _, n := range files {
		b := append([]byte(nil), rel.Files[n]...)
		b[len(b)/2] ^= 0xff
		rel.Files[n] = b
		r.logf("%s: one byte of %s changed after signing", rel.Tag, n)
	}
}

// packFolderOf is the folder the rehearsal's pack of rel unpacks into:
// <version>_<the pack's sha256's first 12>.
func packFolderOf(t *testing.T, rel *releaseverifytest.FakeRelease, version string) string {
	t.Helper()
	var idx rv.ComponentsIndex
	if err := json.Unmarshal(rel.Files[rv.ComponentsIndexName(version)], &idx); err != nil {
		t.Fatalf("the index of %s: %v", version, err)
	}
	for _, p := range idx.Packs {
		if p.ID == rehearsalPack && len(p.SHA256) >= 12 {
			return version + "_" + p.SHA256[:12]
		}
	}
	t.Fatalf("the index of %s has no %s", version, rehearsalPack)
	return ""
}

// ---- the home's packs ----

func (m *appMachine) componentsDir() string {
	return filepath.Join(m.root, "components", rehearsalPack)
}

// packCurrent is the folder the component's current pointer names.
func (m *appMachine) packCurrent() string {
	b, _ := os.ReadFile(filepath.Join(m.componentsDir(), "current"))
	return strings.TrimSpace(string(b))
}

// packFolders are the component's pack folders (<version>_<sha12>).
func (m *appMachine) packFolders() []string {
	ents, _ := os.ReadDir(m.componentsDir())
	var out []string
	for _, e := range ents {
		if e.IsDir() && !strings.HasPrefix(e.Name(), ".") && strings.Contains(e.Name(), "_") {
			out = append(out, e.Name())
		}
	}
	sort.Strings(out)
	return out
}

// supervisorCall calls the supervisor's admin with its per-boot local token.
func (m *appMachine) supervisorCall(method, path string, body []byte, d time.Duration) (int, []byte) {
	tok, err := os.ReadFile(filepath.Join(m.root, "supervisor", "local.token"))
	if err != nil {
		m.r.t.Fatalf("%s: the local token: %v", m.name, err)
	}
	req, _ := http.NewRequest(method, fmt.Sprintf("http://127.0.0.1:%d%s", m.sup, path), bytes.NewReader(body))
	req.Header.Set("Authorization", "Bearer "+strings.TrimSpace(string(tok)))
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := (&http.Client{Timeout: d}).Do(req)
	if err != nil {
		return 0, []byte(err.Error())
	}
	defer resp.Body.Close()
	b, _ := io.ReadAll(resp.Body)
	return resp.StatusCode, b
}

// importPacks installs the release's pack on the machine from an offline folder of it
// (the signed files, the index, packs/<name>), as an owner's import does.
func (m *appMachine) importPacks(rel *releaseverifytest.FakeRelease, version string) {
	t := m.r.t
	t.Helper()
	dir := filepath.Join(m.r.work, "offline-"+version)
	_ = os.RemoveAll(dir)
	if err := os.MkdirAll(filepath.Join(dir, "packs"), 0o755); err != nil {
		t.Fatal(err)
	}
	man, _ := json.Marshal(map[string]any{"schema": 1, "version": version, "platform": m.r.kit.platform,
		"items": []string{rehearsalPack}})
	files := map[string][]byte{"offline.json": man}
	for _, n := range []string{rv.ChecksumsFile, rv.BundleFile, rv.ManifestFile, rv.ComponentsIndexName(version)} {
		files[n] = rel.Files[n]
	}
	for _, n := range packFiles(rel) {
		files[filepath.Join("packs", n)] = rel.Files[n]
	}
	for n, b := range files {
		if err := os.WriteFile(filepath.Join(dir, n), b, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	body, _ := json.Marshal(map[string]string{"path": dir})
	var code int
	var out []byte
	// the asset half may be busy a moment with the component (its first desired state)
	waitFor(t, m.name+" imports "+rehearsalPack, 5*time.Minute, func() bool {
		code, out = m.supervisorCall(http.MethodPost, "/components/import", body, 5*time.Minute)
		if code == http.StatusOK {
			return true
		}
		m.r.logf("%s: the import answered %d %s", m.name, code, out)
		time.Sleep(3 * time.Second)
		return false
	})
	var res struct {
		Installed []string `json:"installed"`
	}
	if json.Unmarshal(out, &res) != nil || !slices.Contains(res.Installed, rehearsalPack) {
		t.Fatalf("%s: the import installed %s, not %s", m.name, out, rehearsalPack)
	}
	m.r.logf("%s: %s imported, current %s", m.name, rehearsalPack, m.packCurrent())
}

// packInstalled is the component's progress route's installed.
func (m *appMachine) packInstalled() bool {
	code, out := m.supervisorCall(http.MethodGet, "/components/"+rehearsalPack+"/progress", nil, 10*time.Second)
	var p struct {
		Installed bool `json:"installed"`
	}
	return code == http.StatusOK && json.Unmarshal(out, &p) == nil && p.Installed
}

// keptVersions are the releases the app keeps: its own payload's and each kept copy's.
func (m *appMachine) keptVersions() []string {
	out := []string{payloadJSONVersion(m.payload)}
	dirs, _ := filepath.Glob(filepath.Join(m.root, "updates", "payloads", "*", ".kept"))
	for _, f := range dirs {
		if b, err := os.ReadFile(f); err == nil {
			out = append(out, strings.TrimPrefix(strings.TrimSpace(string(b)), "v"))
		}
	}
	return out
}

// currentWatch records every folder the component's current pointer named.
type currentWatch struct {
	mu   sync.Mutex
	seen []string
	stop chan struct{}
	done chan struct{}
}

func (m *appMachine) watchCurrent() *currentWatch {
	w := &currentWatch{stop: make(chan struct{}), done: make(chan struct{})}
	go func() {
		defer close(w.done)
		tick := time.NewTicker(100 * time.Millisecond)
		defer tick.Stop()
		for {
			c := m.packCurrent()
			w.mu.Lock()
			if c != "" && (len(w.seen) == 0 || w.seen[len(w.seen)-1] != c) {
				w.seen = append(w.seen, c)
			}
			w.mu.Unlock()
			select {
			case <-w.stop:
				return
			case <-tick.C:
			}
		}
	}()
	return w
}

func (w *currentWatch) end() []string {
	close(w.stop)
	<-w.done
	w.mu.Lock()
	defer w.mu.Unlock()
	return append([]string(nil), w.seen...)
}

// journalSteps is the journal's steps (updates/journal.json), its error and its packs.
type journalSteps struct {
	State string `json:"state"`
	Steps []struct {
		Step       string     `json:"step"`
		State      string     `json:"state"`
		StartedAt  time.Time  `json:"started_at"`
		FinishedAt *time.Time `json:"finished_at"`
	} `json:"steps"`
	Error *struct {
		Step string `json:"step"`
		Code string `json:"code"`
	} `json:"error"`
	Packs []struct {
		ID, Dir, From string
	} `json:"packs"`
}

func (m *appMachine) journalSteps() journalSteps {
	var j journalSteps
	_ = json.Unmarshal([]byte(m.readFile(filepath.Join("updates", "journal.json"))), &j)
	return j
}

// homeWithPack is the small seed's home on this commit with the pack of this commit
// imported (a rollback's release must be one that knows the packs).
func (r *rehearsal) homeWithPack() *appMachine {
	r.t.Helper()
	k := r.kit
	home, _ := r.homeOnPrev(profile("small"))
	home.installByHand(k.curSpec())
	home.waitServing(k.cur, 10*time.Minute)
	dir := home.offer(k.curSpec())
	rel := r.publish(k.cur, dir)
	home.importPacks(rel, k.cur)
	want := packFolderOf(r.t, rel, k.cur)
	if c := home.packCurrent(); c != want {
		r.t.Fatalf("after the import current is %q, want %q", c, want)
	}
	if !home.packInstalled() {
		r.t.Fatalf("the progress route does not say %s is installed", rehearsalPack)
	}
	if !strings.Contains(home.log.String(), "its venv is not rebuilt") {
		r.logf("note: the log does not say the pack's venv was skipped (MAKTABA_PYTHON)")
	}
	return home
}

// =====================================================================================
// 13 An update carries the packs; a rollback finds the kept one.
// =====================================================================================

func TestUpdateRehearsal13UpdateWithPacks(t *testing.T) {
	r := newRehearsal(t)
	r.packsKit()
	k := r.kit
	home := r.homeWithPack()
	curFolder := home.packCurrent()
	db := home.superDB("maktaba")
	before := seedCounts(t, db)

	// the update to a release that fails its watch
	watch := home.watchCurrent()
	run, mon, states := r.update(home, k.nextAdditiveFailing(), 25*time.Minute)
	seen := watch.end()
	r.logf("current named %v during the update", seen)
	if run.State != "rolled_back" {
		t.Fatalf("the update to %s ended %s, want rolled_back: %s (steps %+v)", k.next, run.State, run.Error, run.Steps)
	}
	home.waitServing(k.cur, 5*time.Minute)
	r.budget(mon, "a rollback after additive migrations, with a pack installed", 4*time.Minute)
	home.assertServing(k.cur)
	r.noRestore(home, states)

	// the packs step ran, and ended before the apply started (the app's order, the
	// pre-update dump, the switch)
	j := home.journalSteps()
	var packsEnd *time.Time
	var applyStart time.Time
	switched := false
	for _, s := range j.Steps {
		switch s.Step {
		case "packs":
			if s.State != "done" {
				t.Errorf("the packs step ended %s", s.State)
			}
			packsEnd = s.FinishedAt
		case "order", "backup", "switch":
			switched = switched || s.Step == "switch"
			if applyStart.IsZero() || s.StartedAt.Before(applyStart) {
				applyStart = s.StartedAt
			}
		}
	}
	switch {
	case packsEnd == nil:
		t.Errorf("the journal has no finished packs step: %+v", j.Steps)
	case !switched:
		t.Errorf("the journal has no switch step: %+v", j.Steps)
	case packsEnd.After(applyStart):
		t.Errorf("the packs step ended %s, after the apply started %s", packsEnd, applyStart)
	}
	nextFolder := ""
	for _, p := range j.Packs {
		if p.ID == rehearsalPack {
			nextFolder = p.Dir
			if p.From != curFolder {
				t.Errorf("the staged pack says it replaces %q, want %q", p.From, curFolder)
			}
		}
	}
	if nextFolder == "" || nextFolder == curFolder {
		t.Fatalf("the journal names no new folder of %s: %+v", rehearsalPack, j.Packs)
	}
	if !strings.HasPrefix(nextFolder, k.next+"_") {
		t.Errorf("the staged folder %q is not %s's", nextFolder, k.next)
	}
	// the new release ran on its own pack, the rollback's release on the kept one
	if !slices.Contains(seen, nextFolder) {
		t.Errorf("current never named %s's folder %s while it ran (seen %v)", k.next, nextFolder, seen)
	}
	if c := home.packCurrent(); c != curFolder {
		t.Errorf("after the rollback current is %q, want the kept folder %q", c, curFolder)
	}
	if _, err := os.Stat(filepath.Join(home.componentsDir(), curFolder)); err != nil {
		t.Errorf("the kept folder is gone: %v", err)
	}
	waitFor(t, "the progress route to say installed", time.Minute, func() bool {
		if home.packInstalled() {
			return true
		}
		time.Sleep(time.Second)
		return false
	})
	r.countsUnchanged(before, seedCounts(t, db))

	// N13: an update that passes, with the pack installed, keeps the update's budget
	run, mon, _ = r.update(home, k.afterSpec(), 20*time.Minute)
	if run.State != "done" {
		t.Fatalf("the update to %s ended %s: %s (steps %+v)", k.after, run.State, run.Error, run.Steps)
	}
	home.waitServing(k.after, 3*time.Minute)
	r.budget(mon, "an update with a pack installed (N13)", 2*time.Minute)
	home.assertServing(k.after)
	afterFolder := home.packCurrent()
	if !strings.HasPrefix(afterFolder, k.after+"_") {
		t.Errorf("after the update current is %q, not %s's folder", afterFolder, k.after)
	}

	// the commit pruned the packs of the releases no longer kept
	kept := home.keptVersions()
	folders := home.packFolders()
	r.logf("kept releases %v; pack folders %v", kept, folders)
	for _, f := range folders {
		v, _, _ := strings.Cut(f, "_")
		if f != afterFolder && !slices.Contains(kept, v) {
			t.Errorf("the pack folder %s of %s stayed after the commit (kept %v)", f, v, kept)
		}
	}
	for _, v := range kept {
		want := ""
		switch v {
		case k.cur:
			want = curFolder
		case k.after:
			want = afterFolder
		}
		if want != "" && !slices.Contains(folders, want) {
			t.Errorf("the pack of the kept release %s (%s) was pruned", v, want)
		}
	}
	if !slices.Contains(kept, k.next) && slices.Contains(folders, nextFolder) {
		t.Errorf("the rolled-back release's pack %s stayed", nextFolder)
	}
	r.countsUnchanged(before, seedCounts(t, db))
}

// =====================================================================================
// 14 A pack that fails its verification stops the update before anything changes.
// =====================================================================================

func TestUpdateRehearsal14PackFailsVerification(t *testing.T) {
	r := newRehearsal(t)
	r.packsKit()
	k := r.kit
	home := r.homeWithPack()
	curFolder := home.packCurrent()
	foldersBefore := home.packFolders()
	db := home.superDB("maktaba")
	before := seedCounts(t, db)
	recBefore, _ := home.record()

	r.corruptPack = map[string]bool{k.next: true}
	run, mon, states := r.update(home, k.nextAdditiveFailing(), 15*time.Minute)
	r.budget(mon, "an update refused for a pack that fails its verification", 5*time.Second)
	if run.State == "done" || run.State == "rolled_back" {
		t.Fatalf("the update ran with a pack that fails its verification: %s", run.State)
	}
	j := home.journalSteps()
	if j.State != "prepare_failed" {
		t.Errorf("the journal ended %s, want prepare_failed", j.State)
	}
	if j.Error == nil || j.Error.Step != "packs" || j.Error.Code != "pack_verify_failed" {
		t.Errorf("the journal's error is %+v, want pack_verify_failed at the packs step (run error %s)", j.Error, run.Error)
	}
	if !strings.Contains(string(run.Error), "pack_verify_failed") {
		t.Errorf("the run's error does not say pack_verify_failed: %s", run.Error)
	}
	for _, s := range states.journalStates() {
		if s == "applying" || s == "restarting" || s == "watching" {
			t.Errorf("the home switched although the pack failed its verification (journal %v)", states.journalStates())
		}
	}
	if got := home.packFolders(); !slices.Equal(got, foldersBefore) {
		t.Errorf("the pack folders changed: %v, then %v", foldersBefore, got)
	}
	if c := home.packCurrent(); c != curFolder {
		t.Errorf("current changed: %q, then %q", curFolder, c)
	}
	recAfter, _ := home.record()
	if recAfter.State != recBefore.State || recAfter.Version != recBefore.Version {
		t.Errorf("the database's record changed: %+v, then %+v", recBefore, recAfter)
	}
	if !home.packInstalled() {
		t.Errorf("the progress route no longer says %s is installed", rehearsalPack)
	}
	home.assertServing(k.cur)
	r.countsUnchanged(before, seedCounts(t, db))
}
