//go:build unix

package main

// Scenarios 16.x: Epic 54 Phase 4's waves across an update and a rollback (stories
// 54.28-54.36, the update rehearsal's T05 of each). The home runs each release's real
// pipeline (tools/rehearsal/venvs.sh: one environment per release of the kit), whose
// wave stages do their work as sleeps (tools/rehearsal/sleepy), so the jobs are in flight
// in the worker or in their runner when the update stops the release.
//
// For each wave: jobs of its stages queued and running on the previous release; the
// update to the release that fails its watch (a rollback to the previous release); more
// jobs; the update to this commit, which passes. Every job ends done, none failed or
// cancelled, no attempt spent by the switch, no job started twice; each job ran where
// its release runs the stage (in its runner, impl 'plugin', on a release whose built-in
// plugins have the stage; in-process on one without: a rollback to a release from before
// the wave runs it in-process again); and once this commit's runners serve, the stages
// are 'plugin' and new jobs run there (the flip at update, story 54.25 §3).
//
// A wave none of whose stages is a built-in plugin of this commit is skipped: its row
// runs from the commit that brings it. Wave 7 (16.7, story 54.34) has a scenario of its own
// (rehearsal_wave7_e2e_test.go): one TV change through a real Epic 33 swap across the
// update.

import (
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/lib/pq"
)

// rehearsalWave is one wave of Phase 4: its scenario, its story and the stages the
// scenario queues (those of its stages a home without a GPU, a model or a network
// service claims), and what the home needs before they claim (setup).
type rehearsalWave struct {
	id, story string
	stages    []string
	setup     func(r *rehearsal, home *appMachine, db *sql.DB)
}

// plexOn turns the Plex extension on as an owner does: Settings' PUT
// /api/extensions/plex with enabled and its URL and token, which also sets the ext.plex
// component's desired state (story 51.07). Its stage (plex_livetv, the only one since the
// Plex library sync went) claims only then. Its work is a sleep here, so the address
// answers nothing (port 9, discard) and the token is a placeholder.
func plexOn(r *rehearsal, home *appMachine, db *sql.DB) {
	t := r.t
	t.Helper()
	admin := home.signIn()
	code, body := admin.adminJSON("PUT", "/api/extensions/plex", map[string]any{"enabled": true,
		"settings": map[string]any{"url": "http://127.0.0.1:9", "token": "rehearsal-not-a-token"}})
	if code != http.StatusOK {
		t.Fatalf("turn the Plex extension on: %d %s", code, body)
	}
	waitStagesEnabled(t, db, []string{"plex_livetv"}, 3*time.Minute)
	r.logf("the Plex extension is on; plex_livetv claims")
}

// waitStagesEnabled waits until the stages' rows say enabled (the worker's graph sync
// writes an extension stage's row and enables it once the extension is on).
func waitStagesEnabled(t *testing.T, db *sql.DB, stages []string, within time.Duration) {
	t.Helper()
	deadline := time.Now().Add(within)
	for {
		var n int
		_ = db.QueryRow(`SELECT count(*) FROM job_stages WHERE stage = ANY($1) AND enabled`, pq.Array(stages)).Scan(&n)
		if n == len(stages) {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("%v not enabled within %s", stages, within)
		}
		time.Sleep(2 * time.Second)
	}
}

var rehearsalWaves = map[string]rehearsalWave{
	"16.1": {"16.1", "54.28", []string{"promo_links", "clip_groups", "topic_collections", "rate_style"}, nil},
	"16.2": {"16.2", "54.29", []string{"thumb_small", "chapter_thumbs", "subtitle_gen"}, nil},
	"16.3": {"16.3", "54.30", []string{"topic_assign", "chapter_infer"}, nil},
	"16.4": {"16.4", "54.31", []string{"detect", "tag"}, nil},
	"16.5": {"16.5", "54.32", []string{"dedup_scan"}, nil},
	"16.6": {"16.6", "54.33", []string{"exact_dates"}, nil},
	"16.8": {"16.8", "54.35", []string{"plex_livetv"}, plexOn},
	"16.9": {"16.9", "54.36", []string{"scan", "probe", "link", "fp_gate"}, nil},
}

// waveSleep is how long each job of a wave stage sleeps: longer than the claim loop's
// poll, shorter than the worker's drain (15 s), so a stop lets the running ones finish.
const waveSleep = 8 * time.Second

// venvs is the folder of the releases' pipeline environments (tools/rehearsal/venvs.sh's
// --out): MAKTABA_REHEARSAL_VENVS, else the kit's venvs/.
func (k *rehearsalKit) venvs() string {
	if d := os.Getenv("MAKTABA_REHEARSAL_VENVS"); d != "" {
		return d
	}
	return filepath.Join(k.dir, "venvs")
}

// realPipeline makes every machine of the rehearsal run its release's own pipeline: a
// Python that execs the environment of the release whose server started it (the
// payload.json beside that server's bin/), with stages' work a sleep. Called before
// any machine starts.
func (r *rehearsal) realPipeline(stages []string) {
	t := r.t
	t.Helper()
	envs := r.kit.venvs()
	for _, v := range []string{r.kit.prev, r.kit.cur, r.kit.next} {
		if _, err := os.Stat(filepath.Join(envs, v, "bin", "python")); err != nil {
			t.Fatalf("the pipeline environment of %s: %v (tools/rehearsal/venvs.sh --kit %s --out %s)", v, err, r.kit.dir, envs)
		}
	}
	shim := filepath.Join(r.work, "pipeline-python")
	body := `#!/bin/sh
# the rehearsal's pipeline Python: the environment of the release whose server started it
p=$PPID
if [ -r "/proc/$p/exe" ]; then exe=$(readlink "/proc/$p/exe"); else exe=$(ps -o comm= -p "$p"); fi
v=$(sed -n 's/^  "version": *"v\{0,1\}\([^"]*\)".*/\1/p' "$(dirname "$exe")/../payload.json" 2>/dev/null | head -1)
if [ -z "$v" ] || [ ! -x "` + envs + `/$v/bin/python" ]; then
    echo "rehearsal: no pipeline environment for the release of $exe (version '$v')" >&2
    exit 70
fi
# the release it runs for (next and after run cur's environment: their jobs say their own)
export MAKTABA_REHEARSAL_RELEASE="$v"
exec "` + envs + `/$v/bin/python" "$@"
`
	if err := os.WriteFile(shim, []byte(body), 0o755); err != nil {
		t.Fatal(err)
	}
	r.python = shim
	r.buildPython = pipelinePython(t)
	r.pipelineEnv = []string{"MAKTABA_REHEARSAL_SLEEPY=" + strings.Join(stages, ","),
		fmt.Sprintf("MAKTABA_REHEARSAL_SLEEPY_SECONDS=%d", int(waveSleep/time.Second))}
	if len(stages) == 0 {
		r.logf("the real pipeline of each release; no stage sleeps")
		return
	}
	r.logf("the real pipeline of each release; %s sleep %s", strings.Join(stages, ", "), waveSleep)
}

// builtIn is the stages that are a built-in plugin's in the release of version (its
// pipeline environment's bundle).
func (r *rehearsal) builtIn(version string) map[string]bool {
	t := r.t
	t.Helper()
	py := filepath.Join(r.kit.venvs(), version, "bin", "python")
	// the graph first: it is what imports the bundle in the pipeline (a cycle otherwise)
	out, err := exec.Command(py, "-c", `import json
import maktaba_pipeline.engine.graph
try:
    from maktaba_pipeline.plugins import firstparty
except ModuleNotFoundError:
    print("[]")
else:
    print(json.dumps(sorted(firstparty.stage_names())))
`).Output()
	if err != nil {
		t.Fatalf("the built-in plugins of %s: %v", version, err)
	}
	var names []string
	if err := json.Unmarshal(out, &names); err != nil {
		t.Fatalf("the built-in plugins of %s: %v (%s)", version, err, out)
	}
	m := map[string]bool{}
	for _, n := range names {
		m[n] = true
	}
	return m
}

// waveJob is one job the scenario queued, as it ended.
type waveJob struct {
	id               int64
	stage, state     string
	attempts, starts int
	impl, release    string
}

// subjectTables is where the subjects of each kind are (processing_jobs' typed keys).
var subjectTables = map[string]string{"video": "videos", "library": "libraries", "series": "series",
	"season": "series_seasons"}

// catalogSubject is the one subject of kind catalog (queues.CatalogID): a catalog stage
// (plex_livetv) gets one job per queueing, whatever n is.
const catalogSubject = "00000000-0000-0000-0000-00000000ca7a"

// queueWave enqueues up to n jobs of each stage on the seed's subjects of its kind; the
// newest job id before them. A stage none of whose kinds the seed has is left out (said).
func (r *rehearsal) queueWave(db *sql.DB, stages []string, n int) int64 {
	t := r.t
	t.Helper()
	settleOpenJobs(t, db, stages, 3*time.Minute)
	var last int64
	_ = db.QueryRow(`SELECT COALESCE(max(id), 0) FROM processing_jobs`).Scan(&last)
	queued := 0
	for _, st := range stages {
		// an extension's stage gets its row from the worker's graph sync, not a migration
		// (plex_livetv): it may come a little after the home serves
		var kinds []string
		deadline := time.Now().Add(3 * time.Minute)
		for {
			err := db.QueryRow(`SELECT subject_kinds FROM job_stages WHERE stage = $1`, st).Scan(pq.Array(&kinds))
			if err == nil {
				break
			}
			if !errors.Is(err, sql.ErrNoRows) || time.Now().After(deadline) {
				t.Fatalf("%s's row: %v", st, err)
			}
			time.Sleep(2 * time.Second)
		}
		var kind string
		var ids []string
		for _, k := range kinds {
			if k == "catalog" {
				kind, ids = k, []string{catalogSubject}
				break
			}
			table, ok := subjectTables[k]
			if !ok {
				continue
			}
			rows, err := db.Query(`SELECT id::text FROM `+table+` ORDER BY id LIMIT $1`, n)
			if err != nil {
				t.Fatal(err)
			}
			for rows.Next() {
				var id string
				_ = rows.Scan(&id)
				ids = append(ids, id)
			}
			rows.Close()
			if len(ids) > 0 {
				kind = k
				break
			}
		}
		if kind == "" {
			r.logf("%s: the seed has no subject of %v: not queued", st, kinds)
			continue
		}
		for _, id := range ids {
			if _, err := db.Exec(`SELECT processing_jobs_enqueue($1, $2::uuid, $3, 100, true, false,
				'{"why":"rehearsal","by":"system"}'::jsonb, NULL, NULL, NULL)`, kind, id, st); err != nil {
				t.Fatalf("enqueue %s: %v", st, err)
			}
		}
		queued += len(ids)
	}
	if queued == 0 {
		t.Fatalf("no job of %v queued: the seed has none of their subjects", stages)
	}
	makeDue(t, db, stages, last)
	return last
}

// settleOpenJobs waits until no job of the stages is open. An enqueue on a subject that has
// an open job of the stage joins it, so a job the home queued itself (turning the Plex
// extension on queues plex_livetv on the catalog; a home scans its libraries) would take the
// wave's enqueue into it, a job older than the wave's since that the wave would never see run.
func settleOpenJobs(t *testing.T, db *sql.DB, stages []string, d time.Duration) {
	t.Helper()
	deadline := time.Now().Add(d)
	for {
		var open int
		if err := db.QueryRow(`SELECT count(*) FROM processing_jobs WHERE stage = ANY($1)
			AND state NOT IN ('done', 'failed', 'cancelled', 'skipped')`,
			pq.Array(stages)).Scan(&open); err != nil {
			t.Fatalf("the open jobs of %v: %v", stages, err)
		}
		if open == 0 {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("%d jobs of %v still open after %s", open, stages, d)
		}
		makeDue(t, db, stages, 0)
		time.Sleep(2 * time.Second)
	}
}

// makeDue makes the pending jobs of the stages after since due at once. A stage's debounce
// (tag and exact_dates wait 300 s for more changes) is not what the scenarios rehearse, and
// would outlast their five-minute waits: the jobs a scenario queues, and the ones the wave's
// own runs enqueue later (a detect enqueues its video's tag), are made due as they appear.
func makeDue(t *testing.T, db *sql.DB, stages []string, since int64) {
	t.Helper()
	if _, err := db.Exec(`UPDATE processing_jobs SET not_before = now() WHERE id > $1 AND stage = ANY($2)
		AND state = 'pending' AND not_before > now()`, since, pq.Array(stages)); err != nil {
		t.Fatalf("make the wave's jobs due: %v", err)
	}
}

// waveJobs is the jobs of the stages after id since, with their starts and where they ran.
func waveJobs(t *testing.T, db *sql.DB, stages []string, since int64) []waveJob {
	t.Helper()
	rows, err := db.Query(`SELECT j.id, j.stage, j.state, j.attempts,
			(SELECT count(*) FROM job_events e WHERE e.job_id = j.id AND e.kind = 'step'
				AND e.data->>'rehearsal' = 'start'),
			COALESCE(j.output_summary->'rehearsal'->>'impl', ''), COALESCE(j.output_summary->'rehearsal'->>'release', '')
		FROM processing_jobs j WHERE j.id > $1 AND j.stage = ANY($2) ORDER BY j.id`, since, pq.Array(stages))
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

// waitRunning waits until a job of the stages after since is running (in flight).
func waitRunning(t *testing.T, db *sql.DB, stages []string, since int64, d time.Duration) {
	t.Helper()
	deadline := time.Now().Add(d)
	for time.Now().Before(deadline) {
		var n int
		_ = db.QueryRow(`SELECT count(*) FROM processing_jobs WHERE id > $1 AND stage = ANY($2) AND state = 'running'`,
			since, pq.Array(stages)).Scan(&n)
		if n > 0 {
			return
		}
		time.Sleep(time.Second)
	}
	t.Fatalf("no job of %v ran within %s", stages, d)
}

// waitSettled waits until no job of the stages after since is queued or running.
func waitSettled(t *testing.T, db *sql.DB, stages []string, since int64, d time.Duration) []waveJob {
	t.Helper()
	deadline := time.Now().Add(d)
	for {
		jobs := waveJobs(t, db, stages, since)
		open := 0
		for _, j := range jobs {
			if j.state != "done" && j.state != "failed" && j.state != "cancelled" && j.state != "skipped" {
				open++
			}
		}
		if open == 0 {
			return jobs
		}
		if time.Now().After(deadline) {
			t.Fatalf("%d of %d jobs of %v still open after %s: %+v", open, len(jobs), stages, d, jobs)
		}
		makeDue(t, db, stages, since)
		time.Sleep(2 * time.Second)
	}
}

// queuedIDs is the ids of the jobs, as queued (the jobs the wave's own runs enqueue later,
// a promo_links run enqueuing clip_groups, are not the scenario's).
func queuedIDs(jobs []waveJob) map[int64]bool {
	m := map[int64]bool{}
	for _, j := range jobs {
		m[j.id] = true
	}
	return m
}

// checkWaveJobs fails for a queued job lost, failed, cancelled, run twice, or run where its
// release does not run the stage (want: version -> the stages a built-in plugin runs).
// The jobs the wave's runs enqueued themselves are said, not judged.
func (r *rehearsal) checkWaveJobs(what string, jobs []waveJob, queued map[int64]bool, want map[string]map[string]bool) {
	t := r.t
	t.Helper()
	seen := map[int64]bool{}
	cascaded := map[string]int{}
	var mine []waveJob
	for _, j := range jobs {
		if queued[j.id] {
			seen[j.id] = true
			mine = append(mine, j)
		} else {
			cascaded[j.stage+"/"+j.state]++
		}
	}
	for id := range queued {
		if !seen[id] {
			t.Errorf("%s: job %d was queued and is gone: a job was lost", what, id)
		}
	}
	if len(cascaded) > 0 {
		r.logf("%s: jobs the wave's own runs enqueued (not judged): %v", what, cascaded)
	}
	where := map[string]int{}
	for _, j := range mine {
		if j.state != "done" {
			t.Errorf("%s: job %d (%s) ended %s", what, j.id, j.stage, j.state)
			continue
		}
		if j.attempts > 1 {
			t.Errorf("%s: job %d (%s) took %d attempts: the switch spent one", what, j.id, j.stage, j.attempts)
		}
		if j.starts != 1 {
			t.Errorf("%s: job %d (%s) started %d times: run again from zero", what, j.id, j.stage, j.starts)
		}
		plugins, ok := want[j.release]
		if !ok {
			t.Errorf("%s: job %d (%s) ran on %q, not a release of the kit", what, j.id, j.stage, j.release)
			continue
		}
		impl := "inproc"
		if plugins[j.stage] {
			impl = "plugin"
		}
		// on a release with the plugin a job claimed before its runners said Hello runs
		// in-process (story 54.25 §3: until then the stage stays inproc)
		early := impl == "plugin" && j.impl == "inproc"
		if j.impl != impl && !early {
			t.Errorf("%s: job %d (%s) ran %s on %s, which runs it %s", what, j.id, j.stage, j.impl, j.release, impl)
		}
		where[j.release+"/"+j.impl]++
	}
	r.logf("%s: %d queued jobs done, where they ran: %v", what, len(mine), where)
}

// stageImpls is job_stages.impl of the stages ("" before the column, slot 0198).
func stageImpls(db *sql.DB, stages []string) map[string]string {
	out := map[string]string{}
	rows, err := db.Query(`SELECT stage, impl FROM job_stages WHERE stage = ANY($1)`, pq.Array(stages))
	if err != nil {
		return out
	}
	defer rows.Close()
	for rows.Next() {
		var s, i string
		_ = rows.Scan(&s, &i)
		out[s] = i
	}
	return out
}

func (r *rehearsal) waveScenario(id string) {
	t := r.t
	t.Helper()
	w := rehearsalWaves[id]
	k := r.kit
	r.realPipeline(w.stages)
	want := map[string]map[string]bool{k.prev: r.builtIn(k.prev), k.cur: r.builtIn(k.cur)}
	want[k.next] = want[k.cur]
	for _, st := range w.stages {
		if !want[k.cur][st] {
			t.Skipf("%s (story %s): %s is not a built-in plugin's stage in this commit: the wave has not merged", id, w.story, st)
		}
	}
	flips := []string{}
	for _, st := range w.stages {
		if !want[k.prev][st] {
			flips = append(flips, st)
		}
	}
	r.logf("%s (story %s): %v; in-process on %s: %v", id, w.story, w.stages, k.prev, flips)

	home, _ := r.homeOnPrev(profile("small"))
	db := home.superDB("maktaba")
	defer db.Close()
	if w.setup != nil {
		w.setup(r, home, db)
	}
	const per = 6

	// 1. jobs in flight on the previous release; the update to the release that fails its
	// watch, and the rollback to the previous release
	since := r.queueWave(db, w.stages, per)
	waitRunning(t, db, w.stages, since, 5*time.Minute)
	queued := queuedIDs(waveJobs(t, db, w.stages, since))
	run, mon, states := r.update(home, k.nextAdditiveFailing(), 25*time.Minute)
	if run.State != "rolled_back" {
		t.Fatalf("the update to %s ended %s, want rolled_back: %s (steps %+v)", k.next, run.State, run.Error, run.Steps)
	}
	home.waitServing(k.prev, 5*time.Minute)
	r.budget(mon, id+": a rollback with wave jobs in flight", 4*time.Minute)
	r.noRestore(home, states)
	jobs := waitSettled(t, db, w.stages, since, 10*time.Minute)
	r.checkWaveJobs(id+" through the rollback", jobs, queued, want)

	// 2. more in flight; the update to this commit, which passes: the flip at update
	since = r.queueWave(db, w.stages, per)
	waitRunning(t, db, w.stages, since, 5*time.Minute)
	queued = queuedIDs(waveJobs(t, db, w.stages, since))
	run, mon, _ = r.update(home, k.curSpec(), 20*time.Minute)
	if run.State != "done" {
		t.Fatalf("the update to %s ended %s: %s (steps %+v)", k.cur, run.State, run.Error, run.Steps)
	}
	home.waitServing(k.cur, 5*time.Minute)
	r.budget(mon, id+": an update with wave jobs in flight", 2*time.Minute)
	home.assertServing(k.cur)
	home.assertNoCrashLoop(id + ": after the update to " + k.cur)
	jobs = waitSettled(t, db, w.stages, since, 10*time.Minute)
	r.checkWaveJobs(id+" through the update", jobs, queued, want)

	// 3. this commit's runners serve: every stage of the wave is 'plugin', and new jobs run
	// in the runner
	deadline := time.Now().Add(5 * time.Minute)
	impls := stageImpls(db, w.stages)
	for time.Now().Before(deadline) {
		if all := func() bool {
			for _, st := range w.stages {
				if impls[st] != "plugin" {
					return false
				}
			}
			return true
		}(); all {
			break
		}
		time.Sleep(3 * time.Second)
		impls = stageImpls(db, w.stages)
	}
	for _, st := range w.stages {
		if impls[st] != "plugin" {
			t.Errorf("%s on %s: impl %q, want plugin once its runner serves", st, k.cur, impls[st])
		}
	}
	// the flip says plugin once a runner said Hello, but the update's last restarts (a
	// runner starting again on new areas) can leave a moment with none serving, when a
	// job runs in the worker as it should (story 54.25 §3). Warm-up jobs run until one of
	// each stage has run in its runner; each of them is held to the same rules.
	warm := time.Now().Add(4 * time.Minute)
	for {
		since = r.queueWave(db, w.stages, 1)
		queued = queuedIDs(waveJobs(t, db, w.stages, since))
		jobs = waitSettled(t, db, w.stages, since, 5*time.Minute)
		r.checkWaveJobs(id+" warming up after the flip", jobs, queued, want)
		inRunner := map[string]bool{}
		for _, j := range jobs {
			if queued[j.id] && j.impl == "plugin" {
				inRunner[j.stage] = true
			}
		}
		if len(inRunner) == len(w.stages) || time.Now().After(warm) {
			break
		}
		time.Sleep(5 * time.Second)
	}
	since = r.queueWave(db, w.stages, 2)
	queued = queuedIDs(waveJobs(t, db, w.stages, since))
	jobs = waitSettled(t, db, w.stages, since, 5*time.Minute)
	r.checkWaveJobs(id+" after the flip", jobs, queued, want)
	for _, j := range jobs {
		if queued[j.id] && j.impl != "plugin" {
			t.Errorf("%s after the flip: job %d (%s) ran %s on %s, want in its runner", id, j.id, j.stage, j.impl, j.release)
		}
	}
	// minutes on this commit: a child that dies at start has had time to reach its crash loop
	home.assertNoCrashLoop(id + ": at the end on " + k.cur)
	r.summary(fmt.Sprintf("%s: wave jobs through a rollback and an update, none lost or run twice; flipped to plugin: %s",
		id, strings.Join(slices.Sorted(slices.Values(w.stages)), ", ")))
}

func TestUpdateRehearsal16_1Wave1(t *testing.T) { newRehearsal(t).waveScenario("16.1") }
func TestUpdateRehearsal16_2Wave2(t *testing.T) { newRehearsal(t).waveScenario("16.2") }
func TestUpdateRehearsal16_3Wave3(t *testing.T) { newRehearsal(t).waveScenario("16.3") }
func TestUpdateRehearsal16_4Wave4(t *testing.T) { newRehearsal(t).waveScenario("16.4") }
func TestUpdateRehearsal16_5Wave5(t *testing.T) { newRehearsal(t).waveScenario("16.5") }
func TestUpdateRehearsal16_6Wave6(t *testing.T) { newRehearsal(t).waveScenario("16.6") }
func TestUpdateRehearsal16_8Wave8(t *testing.T) { newRehearsal(t).waveScenario("16.8") }
func TestUpdateRehearsal16_9Wave9(t *testing.T) { newRehearsal(t).waveScenario("16.9") }
