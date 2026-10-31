//go:build unix

package main

// The update rehearsal's scenario 10 (docs/update-rehearsal.md): a joined machine never
// updates before its home commits (the owner, 2026-10-28: his PC ran v0.3.42, paused,
// while the home rolled back to 0.3.41). Two paths, each its own test:
//
//	10a the home's driver orders no machine before the home commits, a run that gives
//	    up on its home mid-watch (journal_lost) included
//	10b the machine's own updater never goes past its home's committed release
//
// Both are expected to fail until #434 (fix/machines-after-home) merges
// (tools/rehearsal/expected-failures.txt): the CI's rollup then requires them.

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/google/uuid"
)

// noMachineOrder: the machine was never ordered version, still answers with its own
// release, not paused, serving.
func (r *rehearsal) noMachineOrder(node *appMachine, version, when string) {
	t := r.t
	t.Helper()
	for _, o := range node.orders() {
		if o.Version == version {
			t.Errorf("%s: the machine was ordered to install %s before its home committed: %+v", when, version, o)
			break
		}
	}
	if v := node.selfVersion(); v != r.kit.prev {
		t.Errorf("%s: the machine answers as %q, want %s (it must not have updated)", when, v, r.kit.prev)
	}
	node.assertNodeServing(r.kit.prev)
}

// 10a: a home and one joined full machine (a Mac app too, its copy of the home's
// database following it).
//  1. An update whose release fails on the home during its watch: the home rolls back,
//     the machine is never ordered.
//  2. A run that gives up on its home mid-watch (journal_lost: the home's supervisor
//     restarts on a journal of another run while its release is watched; the watch is
//     five minutes long here): the driver's ticks (its catch-up) order no machine while
//     the home still watches; once the home commits, the machine is ordered by itself
//     and updates: in a passing run, the machine only after the home's commit.
func TestUpdateRehearsal10aMachinesAfterHomeDriver(t *testing.T) {
	r := newRehearsal(t)
	k := r.kit
	home, _ := r.homeOnPrev(profile("small"))
	node := r.joinNode(home, "node")

	// 1. the home rolls back during its watch
	run, mon, _ := r.update(home, k.nextAdditiveFailing(), 25*time.Minute)
	if run.State != "rolled_back" {
		t.Errorf("the update to %s ended %s, want rolled_back: %s", k.next, run.State, run.Error)
	}
	home.waitServing(k.prev, 5*time.Minute)
	r.budget(mon, "the home's rollback (a machine joined)", 4*time.Minute)
	r.noMachineOrder(node, k.next, "a rollback during the home's watch")

	// 2. a run that gives up on its home mid-watch (journal_lost) while the home still
	// watches a healthy release for five minutes: the driver's ticks (its catch-up among
	// them) must not order the machine to the release the home runs before it commits;
	// once the home commits, the machine is ordered (no click) and updates
	home.env = append(home.env, "MAKTABA_UPDATE_WATCH_SECONDS=300")
	home.stop()
	home.start() // the long watch from the next start on
	home.waitServing(k.prev, 5*time.Minute)
	node.offer(k.curSpec())
	dir := home.offer(k.curSpec())
	r.publish(k.cur, dir)
	states := home.watchStates()
	a := home.signIn()
	mon = home.watchDowntime()
	id := a.updateNow(k.cur)
	waitFor(t, "the home watching "+k.cur, 15*time.Minute, func() bool { return home.journal().State == "watching" })
	home.stop()
	jpath := filepath.Join(home.root, "updates", "journal.json")
	var j map[string]any
	if raw, err := os.ReadFile(jpath); err == nil && json.Unmarshal(raw, &j) == nil {
		if job, ok := j["job"].(map[string]any); ok {
			job["run_id"] = uuid.NewString()
		}
		raw, _ = json.MarshalIndent(j, "", "  ")
		_ = os.WriteFile(jpath, raw, 0o600)
	}
	r.logf("the home's supervisor restarts mid-watch on a journal of another run (journal_lost); it watches 5 minutes")
	home.start()
	run = a.follow(id, 15*time.Minute)
	r.logf("the run that lost its home ended %s: %s", run.State, run.Error)
	var committed time.Time
	waitFor(t, "the home's commit of "+k.cur, 20*time.Minute, func() bool {
		if home.journal().State == "committed" {
			committed = time.Now()
			return true
		}
		// an order before the commit is seen as it happens
		return false
	})
	states.end()
	r.logf("the home committed %s (journal %v)", k.cur, states.journalStates())
	r.budget(mon, "an update whose run lost its home (a machine joined)", 3*time.Minute)
	// once the home committed, the machine's order goes out by itself (no click, D1) and
	// the machine updates
	firstOrder := func() time.Time {
		for _, o := range node.orders() {
			if o.Version == k.cur {
				return o.At
			}
		}
		return time.Time{}
	}
	waitFor(t, "the machine on "+k.cur+" after its home committed", 20*time.Minute, func() bool {
		return node.selfVersion() == k.cur
	})
	switch first := firstOrder(); {
	case first.IsZero():
		t.Errorf("the machine runs %s but its app was never ordered to install it", k.cur)
	case first.Before(committed.Add(-2 * time.Second)): // the commit is seen within 200 ms
		t.Errorf("the machine was ordered to %s %s before its home committed", k.cur, committed.Sub(first).Round(time.Second))
	default:
		r.logf("the machine was ordered %s after its home committed", first.Sub(committed).Round(time.Second))
	}
	home.assertServing(k.cur)
	node.assertNodeServing(k.cur)
}

// 10b: a newer release is published while the home stays on the previous one (its own
// checks are off: it has no reason to take it), the joined machine's own update checks
// run every 20 seconds and its app has the release's update at hand. The machine must not
// install it: a joined machine's own updater never goes past its home's committed
// release. It ends on its release, not paused, serving.
func TestUpdateRehearsal10bMachineOwnUpdater(t *testing.T) {
	r := newRehearsal(t)
	k := r.kit
	home, _ := r.homeOnPrev(profile("small"))
	node := r.joinNode(home, "node", "MAKTABA_UPDATE_INTERVAL_SEC=20")
	dir := node.offer(k.curSpec())
	r.publish(k.cur, dir)
	r.logf("%s is published; the home stays on %s; the machine checks every 20 s", k.cur, k.prev)
	deadline := time.Now().Add(5 * time.Minute)
	for time.Now().Before(deadline) {
		if v := node.selfVersion(); (v != "" && v != k.prev) || len(node.orders()) > 0 || node.journal().Job.Version != "" {
			break
		}
		time.Sleep(2 * time.Second)
	}
	for _, o := range node.orders() {
		t.Errorf("the machine was ordered to install %s while its home is on %s: %+v", o.Version, k.prev, o)
		break
	}
	if j := node.journal(); j.Job.Version != "" {
		t.Errorf("the machine's own updater took %s up while its home is on %s (journal %s)", j.Job.Version, k.prev, j.State)
	}
	if v := home.selfVersion(); v != k.prev {
		t.Errorf("the home answers as %q, want %s", v, k.prev)
	}
	waitFor(t, "the machine serving", 5*time.Minute, func() bool { return node.selfVersion() != "" })
	r.noMachineOrder(node, k.cur, "the machine's own updater")
}
