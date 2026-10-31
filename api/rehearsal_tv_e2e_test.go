//go:build unix

package main

// The update rehearsal's scenario 11 (docs/update-rehearsal.md): the home updates while
// its TV slots run, and the update's TV change then stages its release on a slot that is
// already running the old one.
//
// 2026-10-31, the live update to 0.3.44: the update committed, the whole server restarted,
// and the green slot (free, not live, not the standby; started at the server's start, on
// the streaming software the held TV ran: v0.3.39) came back running the old release.
// The update's TV change (#58) was then staged on green: the front door bound it to the
// 0.3.44 release (POST /slots/green/start with the binding). The supervisor kept the
// binding for green's next start and took the start for a no-op (green ran), so green
// stayed on v0.3.39; the change's staging check failed slot.health (release_mismatch) and
// the TV was held. #443 and fix/slot-reconcile start a running slot again on the release
// it is bound to.
//
// The rehearsal's pipeline is a stand-in that runs no job, so the test plays the
// pipeline's tv_stage job for the update's change (pipeline/src/maktaba_pipeline/
// tvrelease/stage.py stage_change: the release with the change's streaming binary, the
// change moved draft -> staged). Everything after is the real code: the API's TV
// controller asks the front door for the staging slot, the front door binds it through
// the supervisor, the slot reports what it runs, the controller moves the change to
// verifying, or fails it slot.health.
//
// What it cannot cover: the rest of the change's way to the air. Its verification soaks
// on the staging slot's warm channels, and the seed has no channel airing real media (no
// encoder runs here): the change cannot pass its soak and swap. The scenario ends when the
// change is verifying on the new release, which is past the check that failed on
// 2026-10-31.

import (
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// tvSlotNames are the slot instances (streaming/internal/slot: blue, green, amber, on
// 8791-8793).
var tvSlotNames = []string{"blue", "green", "amber"}

// tvSlotStatus is the part of a slot's GET /internal/tv/slot/status the scenario reads.
type tvSlotStatus struct {
	Slot     string `json:"slot"`
	Role     string `json:"role"`
	Release  string `json:"release"`
	Mismatch bool   `json:"release_mismatch"`
	Version  string `json:"version"`
	PID      int    `json:"pid"`
}

// slotStatus is what slot instance name answers about itself (nil: it does not answer).
func slotStatus(name string) *tvSlotStatus {
	port := map[string]int{"blue": 8791, "green": 8792, "amber": 8793}[name]
	c := http.Client{Timeout: 3 * time.Second}
	resp, err := c.Get(fmt.Sprintf("http://127.0.0.1:%d/internal/tv/slot/status", port))
	if err != nil {
		return nil
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		return nil
	}
	var st tvSlotStatus
	if json.NewDecoder(resp.Body).Decode(&st) != nil {
		return nil
	}
	return &st
}

// slotPIDs are the running TV slot instances' pids, by instance, as the supervisor says
// (GET /processes on its admin address).
func (m *appMachine) slotPIDs() map[string]int {
	out := map[string]int{}
	c := http.Client{Timeout: 3 * time.Second}
	resp, err := c.Get(fmt.Sprintf("http://127.0.0.1:%d/processes", m.sup))
	if err != nil {
		return out
	}
	defer resp.Body.Close()
	var snap struct {
		Processes []struct {
			Role     string `json:"role"`
			Instance string `json:"instance"`
			State    string `json:"state"`
			PID      int    `json:"pid"`
		} `json:"processes"`
	}
	if json.NewDecoder(resp.Body).Decode(&snap) != nil {
		return out
	}
	for _, p := range snap.Processes {
		if p.Role == "tv-slot" && p.State == "running" && p.PID != 0 {
			out[p.Instance] = p.PID
		}
	}
	return out
}

// fileSHA256 is the lowercase SHA-256 of a file.
func fileSHA256(t *testing.T, path string) string {
	t.Helper()
	f, err := os.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		t.Fatal(err)
	}
	return hex.EncodeToString(h.Sum(nil))
}

// playStage is the pipeline's tv_stage job for change id (stage.py stage_change, for a
// change of the streaming binary alone): a release that is the live one with the
// change's streaming binary, and the change moved draft -> staged with it. It returns the
// new release's id.
func playStage(t *testing.T, db *sql.DB, id string) string {
	t.Helper()
	var pending []byte
	var live string
	if err := db.QueryRow(`SELECT c.diff->'pending'->'streaming', t.live_release_id::text FROM tv_changes c,
		tv_routing t WHERE c.id = $1 AND t.scope = 'cluster'`, id).Scan(&pending, &live); err != nil {
		t.Fatalf("the change %s and the live release: %v", id, err)
	}
	var streaming []string
	if err := json.Unmarshal(pending, &streaming); err != nil || len(streaming) != 2 {
		t.Fatalf("the change %s names no streaming binary (diff.pending.streaming %s)", id, pending)
	}
	tx, err := db.Begin()
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = tx.Rollback() }()
	var rel string
	var gen int64
	if err := tx.QueryRow(`INSERT INTO tv_releases (digest, kinds, sched_gen, streaming_version, streaming_sha256,
			settings, lineup_digest, base_release_id, created_by)
		SELECT 'rehearsal-' || gen_random_uuid()::text, '["binary"]', (SELECT COALESCE(max(sched_gen), 0) + 1 FROM tv_releases),
			$2, $3, settings, lineup_digest, id, 'rehearsal'
		FROM tv_releases WHERE id = $1
		RETURNING id::text, sched_gen`, live, streaming[0], streaming[1]).Scan(&rel, &gen); err != nil {
		t.Fatalf("the change's release: %v", err)
	}
	res, err := tx.Exec(`UPDATE tv_changes SET state = 'staged', release_id = $2, base_release_id = $3,
		kinds = '["binary"]', anchor_at = now(), staged_at = now(), updated_at = now()
		WHERE id = $1 AND state = 'draft'`, id, rel, live)
	if err != nil {
		t.Fatalf("staging the change: %v", err)
	}
	if n, _ := res.RowsAffected(); n != 1 {
		var state string
		_ = tx.QueryRow(`SELECT state FROM tv_changes WHERE id = $1`, id).Scan(&state)
		t.Fatalf("the change %s is %s, not a draft to stage", id, state)
	}
	if _, err := tx.Exec(`INSERT INTO tv_change_events (change_id, actor_source, from_state, to_state, action, detail)
		VALUES ($1, 'system', 'draft', 'staged', 'stage', jsonb_build_object('release_id', $2::text, 'sched_gen', $3::bigint,
			'reused', false, 'by', 'the rehearsal (the pipeline stand-in runs no job)'))`, id, rel, gen); err != nil {
		t.Fatalf("the change's stage event: %v", err)
	}
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}
	return rel
}

// =====================================================================================
// 11 The update's TV change restarts a slot left running on the old release.
// =====================================================================================

// The home airs a TV release on the previous release's streaming software; its slot roles
// are live blue, standby amber, so green is the free instance a staging takes, and green
// (a slot every start of the server starts) runs. The home updates to this commit while
// they run: the whole server restarts, green comes back on the old streaming software
// (the TV is held on it until a checked change moves it). The update's TV change is then
// staged: the staging slot must be started again on the change's release, and the change
// must move to verifying on it; it fails if the change goes back to a draft with
// slot.health (release_mismatch above all), or the staging slot keeps the process it had.
func TestUpdateRehearsal11TVChangeRestartsARunningSlot(t *testing.T) {
	r := newRehearsal(t)
	k := r.kit
	if !tvSlotPortsFree() {
		r.skipUnlessRequired("the TV slots' fixed ports (8791-8793, 9111-9113) are taken: another Maktaba airs TV on " +
			"this machine, and the rehearsal's TV would meet it")
	}
	home, _ := r.seeded("home", profile("small"))
	home.tvSlots = "on"
	home.start()
	home.waitServing(k.prev, 8*time.Minute)
	db := home.superDB("maktaba")
	before := seedCounts(t, db)

	// the live TV release, on the previous release's streaming binary (its own checksum:
	// after the update the held TV's slots run that binary from the kept payload)
	prevSum := fileSHA256(t, filepath.Join(home.payload, "bin", "maktaba-streaming"))
	const liveRel = "7e1e0000-0000-4000-8000-000000000011"
	if _, err := db.Exec(`INSERT INTO tv_releases (id, digest, kinds, sched_gen, streaming_version, streaming_sha256, created_by)
		VALUES ($1, 'rehearsal-live-11', '["binary"]', 1, $2, $3, 'rehearsal')`, liveRel, k.prev, prevSum); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`INSERT INTO tv_routing (scope, live_release_id, live_gen, epoch, switched_at)
		VALUES ('cluster', $1, 1, 1, now())
		ON CONFLICT (scope) DO UPDATE SET live_release_id = EXCLUDED.live_release_id, live_gen = 1`, liveRel); err != nil {
		t.Fatal(err)
	}

	// the front door's roles (it wrote them at its first start: blue live, green the
	// standby, amber free), moved one epoch up as a swap leaves them: live blue, standby
	// amber, green free. The next front door (the update's restart) starts from them.
	waitFor(t, "the front door's slot roles and the live slot's beat", 3*time.Minute, func() bool {
		var rows, beating int64
		_ = db.QueryRow(`SELECT count(*), count(*) FILTER (WHERE role = 'live' AND COALESCE(version, '') <> '')
			FROM tv_slots`).Scan(&rows, &beating)
		return rows == 3 && beating == 1
	})
	if _, err := db.Exec(`UPDATE tv_slots SET role = CASE slot WHEN 'blue' THEN 'live' WHEN 'amber' THEN 'standby'
		ELSE 'idle' END, role_epoch = (SELECT max(role_epoch) + 1 FROM tv_slots)`); err != nil {
		t.Fatal(err)
	}
	waitFor(t, "green running before the update", 3*time.Minute, func() bool { return home.slotPIDs()["green"] != 0 })
	r.logf("before the update: slots %v; green answers %+v", home.slotPIDs(), slotStatus("green"))

	run, mon, states := r.update(home, k.curSpec(), 25*time.Minute)
	if run.State != "done" {
		t.Fatalf("the update to %s ended %s: %s", k.cur, run.State, run.Error)
	}
	home.waitServing(k.cur, 3*time.Minute)
	r.budget(mon, "an update while the TV slots run", 2*time.Minute)
	j := home.journal()
	if j.TV == nil || j.TV.ChangeID == "" {
		t.Fatalf("the update made no TV change (journal TV %+v): the TV airs a release on %s's streaming", j.TV, k.prev)
	}
	change := j.TV.ChangeID
	r.logf("the update's TV change %s: %q", change, j.TV.Outcome)

	// what runs before the change is staged: the slot the staging takes must be running
	// already (else its start is a fresh one, and this is not the case of 2026-10-31)
	waitFor(t, "the slots running after the update's restart", 3*time.Minute, func() bool {
		p := home.slotPIDs()
		return p["blue"] != 0 && p["green"] != 0
	})
	pidsBefore := home.slotPIDs()
	statusBefore := map[string]*tvSlotStatus{}
	for _, n := range tvSlotNames {
		statusBefore[n] = slotStatus(n)
		r.logf("after the restart, before the stage: %s pid %d answers %+v", n, pidsBefore[n], statusBefore[n])
	}

	newRel := playStage(t, db, change)
	r.logf("staged the change %s on its release %s (the pipeline's tv_stage job, played)", change, newRel)

	// the controller asks for the staging slot: verifying on the new release, or a draft
	// with slot.health (its slot start timeout is 90 s)
	var verify struct {
		Slot      string `json:"slot"`
		ReleaseID string `json:"release_id"`
		Version   string `json:"version"`
	}
	var failed string
	waitFor(t, "the TV change verifying (or failed back to a draft)", 5*time.Minute, func() bool {
		var detail []byte
		err := db.QueryRow(`SELECT detail FROM tv_change_events WHERE change_id = $1 AND action = 'start_verify'
			ORDER BY id LIMIT 1`, change).Scan(&detail)
		if err == nil {
			_ = json.Unmarshal(detail, &verify)
			return true
		}
		var state string
		var report []byte
		if db.QueryRow(`SELECT state, verify_report FROM tv_changes WHERE id = $1`, change).Scan(&state, &report) == nil &&
			state != "staged" {
			failed = state + ": " + string(report)
			return true
		}
		return false
	})
	if failed != "" {
		if strings.Contains(failed, "release_mismatch") {
			t.Fatalf("the TV change was held: its staging slot stayed on the old release (slot.health, release_mismatch): %s",
				tail(failed, 1500))
		}
		t.Fatalf("the TV change did not start verifying: %s", tail(failed, 1500))
	}
	r.logf("the TV change verifies on slot %s: release %s, version %s", verify.Slot, verify.ReleaseID, verify.Version)
	if verify.ReleaseID != newRel {
		t.Errorf("the change verifies on release %q, want its own %s", verify.ReleaseID, newRel)
	}
	if strings.TrimPrefix(verify.Version, "v") != strings.TrimPrefix(k.cur, "v") {
		t.Errorf("the staging slot runs streaming %q, want this commit's %s", verify.Version, k.cur)
	}
	was := pidsBefore[verify.Slot]
	if was == 0 {
		t.Fatalf("the staging slot %s was not running before its bind (slots %v): the case of 2026-10-31 (a running "+
			"slot bound to another release) did not happen here", verify.Slot, pidsBefore)
	}
	// the supervisor lists it running again once its probe answers (a moment after the
	// controller saw it healthy)
	now := 0
	waitFor(t, "the staging slot "+verify.Slot+" running again", time.Minute, func() bool {
		now = home.slotPIDs()[verify.Slot]
		return now != 0
	})
	if now == was {
		t.Errorf("the staging slot %s kept its process (pid %d) across its bind to %s", verify.Slot, was, newRel)
	}
	if st := slotStatus(verify.Slot); st == nil || st.Release != newRel || st.Mismatch {
		t.Errorf("the staging slot %s answers %+v, want release %s and no mismatch", verify.Slot, st, newRel)
	}
	if b := statusBefore[verify.Slot]; b != nil {
		r.logf("the staging slot %s: pid %d (release %q, streaming %s) -> pid %d (release %s, streaming %s)",
			verify.Slot, was, b.Release, b.Version, now, verify.ReleaseID, verify.Version)
	}

	home.assertServing(k.cur)
	r.countsUnchanged(before, seedCounts(t, db))
	r.noRestore(home, states)
}
