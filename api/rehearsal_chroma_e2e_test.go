//go:build unix

package main

// Update-rehearsal scenario 17 (Epic 43 story 43.5; docs/update-rehearsal.md): a home
// that still has Chroma data updates to the release that retired Chroma.
//
// The releases before 43.5 kept semantic search's vectors in Chroma (<StateDir>/chroma)
// until a one-time copy moved them into Postgres (story 43.3); this commit has no Chroma
// at all. A home whose copy was not done when it updates (its semantic_store_state still
// says chroma, its folder full) must start as any other: nothing may fail because Chroma
// is gone. The folder is kept by the first start (a rollback's release reads it) and
// deleted by a start a day after (here MAKTABA_CHROMA_KEEP_SECONDS shortens the day); a
// folder whose copy was done is deleted at once. Each release runs its own
// pipeline (as in 16.x), so the pipeline's own start on the old state row is rehearsed.

import (
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// plantChroma makes a Chroma folder as the releases before 43.5 left one: its SQLite
// database and an HNSW segment folder.
func plantChroma(t *testing.T, dir string) {
	t.Helper()
	seg := filepath.Join(dir, "6f1f4f8a-2c1e-4b8e-9d55-0d1f6b6d3a10")
	if err := os.MkdirAll(seg, 0o755); err != nil {
		t.Fatal(err)
	}
	for path, body := range map[string]string{
		filepath.Join(dir, "chroma.sqlite3"):  "SQLite format 3\x00 the rehearsal's Chroma",
		filepath.Join(seg, "data_level0.bin"): strings.Repeat("v", 4096),
		filepath.Join(seg, "header.bin"):      "h",
	} {
		if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
}

func pathExists(p string) bool {
	_, err := os.Stat(p)
	return err == nil
}

// waitPathGone waits for p to be deleted (the supervisor deletes the folder in the
// background).
func waitPathGone(t *testing.T, p, what string, d time.Duration) {
	t.Helper()
	waitFor(t, what, d, func() bool { return !pathExists(p) })
}

// searchAnswers: the owner's search answers (full-text when the semantic leg is off: the
// rehearsal's pipelines carry no ML extras).
func (m *appMachine) searchAnswers(what string) {
	t := m.r.t
	t.Helper()
	owner := m.signIn()
	resp, body := owner.b.do("POST", m.webURL()+"/api/search", map[string]any{"q": "rehearsal"}, owner.csrf)
	if resp.StatusCode != http.StatusOK && resp.StatusCode != http.StatusAccepted {
		t.Errorf("%s: the search answered %d: %s", what, resp.StatusCode, tail(string(body), 300))
	}
}

func TestUpdateRehearsal17ChromaRetired(t *testing.T) {
	r := newRehearsal(t)
	k := r.kit
	r.realPipeline(nil)
	home, _ := r.homeOnPrev(profile("small"))
	db := home.superDB("maktaba")
	defer db.Close()
	state := home.root // the state dir: data_dir on Linux (config.StateDir)
	chroma := filepath.Join(state, "chroma")
	retired := filepath.Join(state, "chroma-retired")

	// 1. the home as one whose copy was not done: the folder full, the row on chroma, the
	// copy part way (planted while the previous release serves: its copy runs at a start)
	plantChroma(t, chroma)
	if _, err := db.Exec(`UPDATE semantic_store_state SET store = 'chroma', copied_rows = 1000,
		copy_cursor = '{"offset": 1000}', copy_done_at = NULL`); err != nil {
		t.Fatalf("the state row: %v", err)
	}
	before := seedCounts(t, db)
	logFrom := len(home.log.String())
	// the day a folder never copied is kept, shortened (the previous release ignores it)
	const keep = 90 * time.Second
	home.env = append(home.env, fmt.Sprintf("MAKTABA_CHROMA_KEEP_SECONDS=%d", int(keep/time.Second)))

	// 2. the update to this commit: done, no restore, nothing lost
	run, mon, states := r.update(home, k.curSpec(), 20*time.Minute)
	if run.State != "done" {
		t.Fatalf("the update to %s ended %s: %s (steps %+v)", k.cur, run.State, run.Error, run.Steps)
	}
	home.waitServing(k.cur, 5*time.Minute)
	r.budget(mon, "17: an update of a home with Chroma data", 2*time.Minute)
	home.assertServing(k.cur)
	home.assertNoCrashLoop("17: after the update to " + k.cur)
	r.noRestore(home, states)
	r.countsUnchanged(before, seedCounts(t, db))
	home.searchAnswers("17: after the update")

	// the first start keeps the folder (for a rollback) and marks it; the row is the
	// previous release's still
	if !pathExists(filepath.Join(chroma, "chroma.sqlite3")) {
		t.Errorf("the first start of %s deleted the Chroma folder never copied: a rollback would lose it", k.cur)
	}
	if !pathExists(retired) {
		t.Errorf("no %s after the first start of %s", retired, k.cur)
	}
	var store string
	var copied int64
	if err := db.QueryRow(`SELECT store, copied_rows FROM semantic_store_state`).Scan(&store, &copied); err != nil {
		t.Fatalf("the state row: %v", err)
	}
	if store != "chroma" || copied != 1000 {
		t.Errorf("the state row is %s/%d after the update, want the previous release's chroma/1000", store, copied)
	}
	log := home.log.String()[logFrom:]
	if !strings.Contains(log, "kept for this start") {
		t.Errorf("the start of %s does not say it kept the Chroma folder", k.cur)
	}
	for _, bad := range []string{"copy-chroma", "No module named 'chromadb'", "chroma_copy"} {
		if strings.Contains(log, bad) {
			t.Errorf("the start of %s still runs Chroma's copy: %q in its log", k.cur, bad)
		}
	}
	r.logf("17: %s serves with the Chroma folder kept for this start", k.cur)

	// 3. a start past the keep deletes it
	var mark struct {
		KeptAt string `json:"kept_at"`
	}
	if raw, err := os.ReadFile(retired); err == nil {
		_ = json.Unmarshal(raw, &mark)
	}
	if at, err := time.Parse(time.RFC3339, mark.KeptAt); err == nil {
		if wait := time.Until(at.Add(keep + time.Second)); wait > 0 {
			r.logf("17: %s until the keep is over", wait.Round(time.Second))
			time.Sleep(wait)
		}
	} else {
		t.Errorf("the mark %s has no kept_at: %v", retired, err)
	}
	home.stop()
	logFrom = len(home.log.String())
	home.start()
	home.waitServing(k.cur, 5*time.Minute)
	waitPathGone(t, chroma, "the Chroma folder deleted at a start past the keep", 2*time.Minute)
	waitPathGone(t, retired, "the retirement mark gone with it", 30*time.Second)
	home.assertNoCrashLoop("17: the second start")
	if !strings.Contains(home.log.String()[logFrom:], "deleted "+chroma) {
		t.Errorf("the start past the keep does not say it deleted %s", chroma)
	}

	// 4. a folder whose copy was done goes at the start that finds it
	plantChroma(t, chroma)
	done := `{"chroma_dir": "` + chroma + `", "done_at": 1}`
	if err := os.WriteFile(filepath.Join(state, "chroma-copy.done"), []byte(done), 0o600); err != nil {
		t.Fatal(err)
	}
	home.stop()
	home.start()
	home.waitServing(k.cur, 5*time.Minute)
	waitPathGone(t, chroma, "a copied Chroma folder deleted at the start", 2*time.Minute)
	if pathExists(retired) {
		t.Error("a copied folder was marked kept")
	}
	home.assertServing(k.cur)
	home.assertNoCrashLoop("17: after the copied folder went")
	home.searchAnswers("17: after the copied folder went")
	r.countsUnchanged(before, seedCounts(t, db))
	r.summary("17 a home with Chroma data updates: the folder kept by the first start, deleted past the keep; a copied one at once")
}
