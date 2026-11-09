//go:build unix

package main

// Scenario 16.7: wave 7's TV change across an update (story 54.34, T02). Wave 7 moved Epic
// 33's TV change stages into tv.maktaba.livetv-slots (tv_stage, tv_swap, tv_rollback,
// tv_gc); a wrong swap shows at once on every TV, so its row takes one change through a
// real Epic 33 swap with the update between its stage and its swap, instead of the waves'
// sleeping jobs (rehearsal_waves_e2e_test.go).
//
// The home runs its TV slots and each release's real pipeline, the four stages' own code
// witnessed (tools/rehearsal/sleepy: a start step and the side and release in each job's
// summary). On the previous release a TV setting saved with staging on makes a change;
// tv_stage stages it in-process (the stage's switch set to inproc, as a home that rolled
// the stage back by it, when that release has the plugin); the change is approved to swap
// hours later. The update to this commit runs with the change approved, its swap still to
// come: the change is neither set aside nor lost. On this commit, once the stages are
// 'plugin' and a tv_gc ran in the runner, Swap now: tv_swap runs in the runner (the commit
// relayed to the worker), the front door switches to the change's slot and the change is
// live. Then the admin's rollback: the front door switches back to the previous slot,
// tv_rollback runs in the runner, the change is rolled back and the previous release and
// slot are live again.
//
// Played by the test, as scenario 11 plays what cannot happen here: the change's
// verification. Its soak needs warm channels airing real media (streamverify.go starts the
// soak only with one), and the seed has none, so the test moves the staged change to ready
// with a report that says so; everything before (the draft, the stage job) and after (the
// approval, the swap job, the front door's operations, the rollback) is the real code.

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"net/http"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// wave7Stages are the stages scenario 16.7 runs for real and judges.
var wave7Stages = []string{"tv_stage", "tv_swap", "tv_rollback", "tv_gc"}

// wave7LiveRelease is the TV release the home airs before the change.
const wave7LiveRelease = "7e1e0000-0000-4000-8000-000000000167"

func TestUpdateRehearsal16_7Wave7(t *testing.T) { newRehearsal(t).wave7Scenario() }

// changeJobs is the jobs of stage for the TV change id (payload.change_id), as they ended.
func changeJobs(t *testing.T, db *sql.DB, stage, id string) []waveJob {
	t.Helper()
	rows, err := db.Query(`SELECT j.id, j.stage, j.state, j.attempts,
			(SELECT count(*) FROM job_events e WHERE e.job_id = j.id AND e.kind = 'step'
				AND e.data->>'rehearsal' = 'start'),
			COALESCE(j.output_summary->'rehearsal'->>'impl', ''), COALESCE(j.output_summary->'rehearsal'->>'release', '')
		FROM processing_jobs j WHERE j.stage = $1 AND j.payload->>'change_id' = $2 ORDER BY j.id`, stage, id)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	var out []waveJob
	for rows.Next() {
		var j waveJob
		if err := rows.Scan(&j.id, &j.stage, &j.state, &j.attempts, &j.starts, &j.impl, &j.release); err != nil {
			t.Fatal(err)
		}
		out = append(out, j)
	}
	return out
}

// checkRanOnce fails unless jobs is one job, done in one attempt and one start, on the
// side impl of release.
func (r *rehearsal) checkRanOnce(what string, jobs []waveJob, impl, release string) {
	t := r.t
	t.Helper()
	if len(jobs) != 1 {
		t.Errorf("%s: %d jobs (%+v), want one", what, len(jobs), jobs)
		return
	}
	j := jobs[0]
	switch {
	case j.state != "done":
		t.Errorf("%s: job %d ended %s", what, j.id, j.state)
	case j.attempts > 1:
		t.Errorf("%s: job %d took %d attempts", what, j.id, j.attempts)
	case j.starts != 1:
		t.Errorf("%s: job %d started %d times", what, j.id, j.starts)
	case j.impl != impl || j.release != release:
		t.Errorf("%s: job %d ran %s on %q, want %s on %s", what, j.id, j.impl, j.release, impl, release)
	default:
		r.logf("%s: job %d done, %s on %s", what, j.id, j.impl, j.release)
	}
}

// tvChange is the part of a TV change the scenario reads.
type tvChange struct {
	state, release, base string
	report               string
}

func readChange(t *testing.T, db *sql.DB, id string) tvChange {
	t.Helper()
	var c tvChange
	var rel, base sql.NullString
	var report []byte
	if err := db.QueryRow(`SELECT state, release_id::text, base_release_id::text, COALESCE(verify_report, '{}'::jsonb)
		FROM tv_changes WHERE id = $1`, id).Scan(&c.state, &rel, &base, &report); err != nil {
		t.Fatalf("the TV change %s: %v", id, err)
	}
	c.release, c.base, c.report = rel.String, base.String, string(report)
	return c
}

// waitChange waits until the change is in one of want; it fails at once in one of stop.
func waitChange(t *testing.T, db *sql.DB, id string, want, stop []string, d time.Duration) tvChange {
	t.Helper()
	deadline := time.Now().Add(d)
	for {
		c := readChange(t, db, id)
		for _, s := range want {
			if c.state == s {
				return c
			}
		}
		for _, s := range stop {
			if c.state == s {
				t.Fatalf("the TV change %s is %s, want %v: %s; events %s", id, c.state, want, tail(c.report, 1500),
					changeEvents(db, id))
			}
		}
		if time.Now().After(deadline) {
			t.Fatalf("the TV change %s still %s after %s, want %v: %s; events %s", id, c.state, d, want,
				tail(c.report, 1500), changeEvents(db, id))
		}
		time.Sleep(time.Second)
	}
}

// changeEvents is the change's history, for a failure's message.
func changeEvents(db *sql.DB, id string) string {
	rows, err := db.Query(`SELECT action, COALESCE(from_state, ''), COALESCE(to_state, ''), COALESCE(detail::text, '')
		FROM tv_change_events WHERE change_id = $1 ORDER BY id`, id)
	if err != nil {
		return err.Error()
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var a, f, to, d string
		_ = rows.Scan(&a, &f, &to, &d)
		out = append(out, fmt.Sprintf("%s %s->%s %s", a, f, to, tail(d, 200)))
	}
	return strings.Join(out, "; ")
}

// tvRouting is what the cluster airs: its live release and the one a rollback goes back to.
func tvRouting(t *testing.T, db *sql.DB) (live, previous string) {
	t.Helper()
	var l, p sql.NullString
	if err := db.QueryRow(`SELECT live_release_id::text, previous_release_id::text FROM tv_routing
		WHERE scope = 'cluster'`).Scan(&l, &p); err != nil {
		t.Fatalf("tv_routing: %v", err)
	}
	return l.String, p.String
}

// slotRoles is the TV slots' roles (tv_slots), by slot.
func slotRoles(t *testing.T, db *sql.DB) map[string]string {
	t.Helper()
	rows, err := db.Query(`SELECT slot, role FROM tv_slots`)
	if err != nil {
		t.Fatalf("tv_slots: %v", err)
	}
	defer rows.Close()
	out := map[string]string{}
	for rows.Next() {
		var s, role string
		_ = rows.Scan(&s, &role)
		out[s] = role
	}
	return out
}

// liveSlot is the slot whose role is live, "" when none.
func liveSlot(t *testing.T, db *sql.DB) string {
	t.Helper()
	for s, role := range slotRoles(t, db) {
		if role == "live" {
			return s
		}
	}
	return ""
}

// playVerified is the change's verification, played: staged (or verifying) -> ready, with
// a report that passed and says it was played (no "run" in it, so the controller does
// not check it again on the staging slot).
func playVerified(t *testing.T, db *sql.DB, id string) {
	t.Helper()
	tx, err := db.Begin()
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = tx.Rollback() }()
	var from string
	if err := tx.QueryRow(`SELECT state FROM tv_changes WHERE id = $1 FOR UPDATE`, id).Scan(&from); err != nil {
		t.Fatal(err)
	}
	if from != "staged" && from != "verifying" {
		t.Fatalf("the TV change %s is %s, not staged or verifying: %s", id, from, changeEvents(db, id))
	}
	const why = "the rehearsal: no channel here airs real media, so the soak cannot run (scenario 16.7)"
	if _, err := tx.Exec(`UPDATE tv_changes SET state = 'ready', ready_at = now(), updated_at = now(),
		verify_report = jsonb_build_object('passed', true, 'checks', '[]'::jsonb, 'played', $2::text)
		WHERE id = $1`, id, why); err != nil {
		t.Fatalf("the change's verification: %v", err)
	}
	if _, err := tx.Exec(`INSERT INTO tv_change_events (change_id, actor_source, from_state, to_state, action, detail)
		VALUES ($1, 'system', $2, 'ready', 'pass', jsonb_build_object('played', $3::text))`, id, from, why); err != nil {
		t.Fatalf("the change's pass event: %v", err)
	}
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}
}

func (r *rehearsal) wave7Scenario() {
	t := r.t
	t.Helper()
	k := r.kit
	if !tvSlotPortsFree() {
		r.skipUnlessRequired("the TV slots' fixed ports (8791-8793, 9111-9113) are taken: another Maktaba airs TV on " +
			"this machine, and the rehearsal's TV would meet it")
	}
	r.realPipeline(nil)
	r.pipelineEnv = append(r.pipelineEnv, "MAKTABA_REHEARSAL_WITNESS="+strings.Join(wave7Stages, ","))
	want := map[string]map[string]bool{k.prev: r.builtIn(k.prev), k.cur: r.builtIn(k.cur)}
	for _, st := range wave7Stages {
		if !want[k.cur][st] {
			t.Skipf("16.7 (story 54.34): %s is not a built-in plugin's stage in this commit: the wave has not merged", st)
		}
	}

	home, _ := r.seeded("home", profile("small"))
	home.startWithTV(k.prev, 8*time.Minute)
	db := home.superDB("maktaba")
	defer db.Close()
	before := seedCounts(t, db)

	// the live TV release, on the previous release's streaming binary (after the update the
	// slots that run it take that binary from the kept payload)
	prevSum := fileSHA256(t, filepath.Join(home.payload, "bin", "maktaba-streaming"))
	// its generation from the generations' sequence, as the stage takes the next one from it
	// (a fixed number there and the stage's release meets it: tv_releases_sched_gen_key)
	var liveGen int64
	if err := db.QueryRow(`INSERT INTO tv_releases (id, digest, kinds, sched_gen, streaming_version, streaming_sha256, created_by)
		VALUES ($1, 'rehearsal-live-16-7', '["binary"]', nextval('tv_sched_gen_seq'), $2, $3, 'rehearsal')
		RETURNING sched_gen`, wave7LiveRelease, k.prev, prevSum).Scan(&liveGen); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`INSERT INTO tv_routing (scope, live_release_id, live_gen, epoch, switched_at)
		VALUES ('cluster', $1, $2, 1, now())
		ON CONFLICT (scope) DO UPDATE SET live_release_id = EXCLUDED.live_release_id, live_gen = EXCLUDED.live_gen`,
		wave7LiveRelease, liveGen); err != nil {
		t.Fatal(err)
	}
	waitFor(t, "the live slot beating on the live TV release", 4*time.Minute, func() bool {
		s := liveSlot(t, db)
		if s == "" {
			return false
		}
		st := slotStatus(s)
		return st != nil && st.Release == wave7LiveRelease && !st.Mismatch
	})
	a := home.signIn()

	// 1. on the previous release: the change drafted and staged in-process, then approved to
	// swap hours from now
	if want[k.prev]["tv_stage"] {
		// the release's own flip first: its worker's graph sync moves each first-party stage
		// nobody set by hand to its plugin as it starts (engine/sync.py, _flip_first_party).
		// A request for inproc before that finds the stage still inproc, and a release
		// before the API recorded such a request as the admin's (pinImpl) answered 200,
		// changed nothing, and the sync then moved the stage to its plugin: the change's
		// stage ran in its runner (the failure on PR 1068's run)
		waitFor(t, "tv_stage moved to its plugin by "+k.prev+"'s graph sync", 3*time.Minute, func() bool {
			return stageImpls(db, []string{"tv_stage"})["tv_stage"] == "plugin"
		})
		code, body := a.adminJSON("PUT", "/api/queues/tv_stage/impl", map[string]any{"impl": "inproc"})
		if code != http.StatusOK {
			t.Fatalf("tv_stage in-process on %s: %d %s", k.prev, code, body)
		}
		// the switch is observable and held: inproc, set by hand, so no later sync moves it
		var impl string
		var pinned bool
		if err := db.QueryRow(`SELECT impl, impl_changed_at IS NOT NULL FROM job_stages WHERE stage = 'tv_stage'`).
			Scan(&impl, &pinned); err != nil {
			t.Fatal(err)
		}
		if impl != "inproc" || !pinned {
			t.Fatalf("tv_stage after the switch to inproc on %s: impl %s, set by hand %v; want inproc set by hand",
				k.prev, impl, pinned)
		}
		r.logf("tv_stage switched to inproc on %s (its plugin is built in there)", k.prev)
	}
	code, body := a.adminJSON("PATCH", "/api/rules/tv", map[string]any{"set": map[string]any{"guide_max_padding_s": 90}})
	if code != http.StatusOK {
		t.Fatalf("a TV setting saved with staging on: %d %s", code, body)
	}
	var saved struct {
		Draft struct {
			ID string `json:"id"`
		} `json:"draft_change"`
	}
	if err := json.Unmarshal(body, &saved); err != nil || saved.Draft.ID == "" {
		t.Fatalf("the TV setting went to no draft change: %s", body)
	}
	change := saved.Draft.ID
	if code, body := a.adminJSON("POST", "/api/tv/changes/"+change+"/stage", map[string]any{}); code != http.StatusAccepted &&
		code != http.StatusOK {
		t.Fatalf("stage the TV change %s: %d %s", change, code, body)
	}
	staged := waitChange(t, db, change, []string{"staged", "verifying"}, []string{"cancelled", "failed"}, 5*time.Minute)
	if staged.release == "" || staged.base != wave7LiveRelease {
		t.Fatalf("the staged change %s: release %q on base %q, want a release on %s", change, staged.release, staged.base,
			wave7LiveRelease)
	}
	r.checkRanOnce("16.7: the change's stage on "+k.prev, changeJobs(t, db, "tv_stage", change), "inproc", k.prev)
	playVerified(t, db, change)
	swapAt := time.Now().Add(6 * time.Hour).UTC().Format(time.RFC3339)
	if code, body := a.adminJSON("POST", "/api/tv/changes/"+change+"/approve", map[string]any{"swap_at": swapAt}); code != http.StatusOK {
		t.Fatalf("approve the TV change %s: %d %s", change, code, body)
	}
	waitChange(t, db, change, []string{"approved"}, []string{"draft", "cancelled", "failed", "swapping", "live"}, time.Minute)
	slot0 := liveSlot(t, db)
	r.logf("16.7: change %s staged on release %s and approved to swap at %s; the live slot is %s (roles %v)",
		change, staged.release, swapAt, slot0, slotRoles(t, db))

	// 2. the update to this commit, the change's swap to come
	run, mon, states := r.update(home, k.curSpec(), 25*time.Minute)
	if run.State != "done" {
		t.Fatalf("the update to %s ended %s: %s (steps %+v)", k.cur, run.State, run.Error, run.Steps)
	}
	home.waitServing(k.cur, 5*time.Minute)
	r.budget(mon, "16.7: an update with a TV change approved to swap", 2*time.Minute)
	home.assertServing(k.cur)
	if c := readChange(t, db, change); c.state != "approved" || c.release != staged.release {
		t.Fatalf("after the update the TV change %s is %s on release %q, want approved on %s: %s", change, c.state,
			c.release, staged.release, changeEvents(db, change))
	}

	// 3. this commit's runner serves the stages: each is 'plugin', and a tv_gc runs there
	deadline := time.Now().Add(5 * time.Minute)
	impls := stageImpls(db, wave7Stages)
	for time.Now().Before(deadline) && (impls["tv_swap"] != "plugin" || impls["tv_rollback"] != "plugin" ||
		impls["tv_gc"] != "plugin") {
		time.Sleep(3 * time.Second)
		impls = stageImpls(db, wave7Stages)
	}
	for _, st := range []string{"tv_swap", "tv_rollback", "tv_gc"} {
		if impls[st] != "plugin" {
			t.Fatalf("%s on %s: impl %q, want plugin once its runner serves", st, k.cur, impls[st])
		}
	}
	gc := []string{"tv_gc"}
	warm := time.Now().Add(4 * time.Minute)
	for {
		since := r.queueWave(db, gc, 1)
		jobs := waitSettled(t, db, gc, since, 5*time.Minute)
		inRunner := false
		for _, j := range jobs {
			if j.state != "done" {
				t.Fatalf("16.7: tv_gc job %d ended %s on %s", j.id, j.state, j.release)
			}
			inRunner = inRunner || (j.impl == "plugin" && j.release == k.cur)
		}
		if inRunner {
			r.logf("16.7: a tv_gc ran in its runner on %s: %+v", k.cur, jobs)
			break
		}
		if time.Now().After(warm) {
			t.Fatalf("16.7: no tv_gc ran in its runner on %s within 4 min: %+v", k.cur, jobs)
		}
		time.Sleep(5 * time.Second)
	}

	// 4. Swap now: the plugin's tv_swap commits, the front door switches to the change's
	// slot, the change is live
	if c := readChange(t, db, change); c.state != "approved" {
		t.Fatalf("the TV change %s is %s before its swap, want approved: %s", change, c.state, changeEvents(db, change))
	}
	if code, body := a.adminJSON("POST", "/api/tv/changes/"+change+"/promote", map[string]any{}); code != http.StatusOK &&
		code != http.StatusAccepted {
		t.Fatalf("Swap now for the TV change %s: %d %s", change, code, body)
	}
	waitChange(t, db, change, []string{"live"}, []string{"ready", "draft", "cancelled", "failed", "rolled_back"}, 6*time.Minute)
	r.checkRanOnce("16.7: the change's swap on "+k.cur, changeJobs(t, db, "tv_swap", change), "plugin", k.cur)
	live, previous := tvRouting(t, db)
	if live != staged.release || previous != wave7LiveRelease {
		t.Errorf("after the swap the TV airs %s (previous %s), want the change's %s (previous %s)", live, previous,
			staged.release, wave7LiveRelease)
	}
	var slot1 string
	waitFor(t, "the change's slot live and the old one previous", 2*time.Minute, func() bool {
		roles := slotRoles(t, db)
		slot1 = liveSlot(t, db)
		return slot1 != "" && slot1 != slot0 && roles[slot0] == "previous"
	})
	if st := slotStatus(slot1); st == nil || st.Release != staged.release {
		t.Errorf("the live slot %s after the swap answers %+v, want release %s", slot1, st, staged.release)
	}
	r.logf("16.7: the swap completed: slot %s live on %s, %s previous (roles %v)", slot1, staged.release, slot0,
		slotRoles(t, db))

	// 5. the admin's rollback: the previous slot and release are live again
	code, body = a.adminJSON("POST", "/api/tv/rollback", map[string]any{})
	if code != http.StatusAccepted && code != http.StatusOK {
		t.Fatalf("roll the TV back: %d %s", code, body)
	}
	var rb struct {
		Mode     string `json:"mode"`
		ChangeID string `json:"change_id"`
		OpID     int64  `json:"op_id"`
		Degraded string `json:"degraded"`
	}
	_ = json.Unmarshal(body, &rb)
	if rb.Mode != "warm" || rb.ChangeID != change || rb.OpID == 0 || rb.Degraded != "" {
		t.Errorf("the rollback answered %s, want a warm rollback of %s by the front door", body, change)
	}
	waitChange(t, db, change, []string{"rolled_back"}, []string{"failed"}, 6*time.Minute)
	r.checkRanOnce("16.7: the change's rollback on "+k.cur, changeJobs(t, db, "tv_rollback", change), "plugin", k.cur)
	if live, _ := tvRouting(t, db); live != wave7LiveRelease {
		t.Errorf("after the rollback the TV airs %s, want the previous release %s", live, wave7LiveRelease)
	}
	waitFor(t, "the previous slot "+slot0+" live again", 2*time.Minute, func() bool { return liveSlot(t, db) == slot0 })
	if st := slotStatus(slot0); st == nil || st.Release != wave7LiveRelease || st.Mismatch {
		t.Errorf("the live slot %s after the rollback answers %+v, want release %s", slot0, st, wave7LiveRelease)
	}
	r.logf("16.7: the rollback restored slot %s on %s (roles %v)", slot0, wave7LiveRelease, slotRoles(t, db))

	home.assertServing(k.cur)
	home.assertNoCrashLoop("16.7: at the end on " + k.cur)
	r.countsUnchanged(before, seedCounts(t, db))
	r.noRestore(home, states)
	r.summary(fmt.Sprintf("16.7: TV change %s staged in-process on %s, swapped by the plugin on %s (slot %s -> %s), "+
		"rolled back to slot %s", change, k.prev, k.cur, slot0, slot1, slot0))
}
