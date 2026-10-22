package switchover

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/google/uuid"
)

// Two switches started at once: one runs, the other is refused, also when they start on
// two processes of the home (the database's row: one under way at a time).
func TestTwoSwitchesStartedAtOnceRunOne(t *testing.T) {
	o, _, _, b, c := setup(t)
	ctx := context.Background()
	var wg sync.WaitGroup
	var started, refused atomic.Int32
	for _, node := range []string{b, c, b} {
		wg.Add(1)
		go func(node string) {
			defer wg.Done()
			_, err := o.Start(ctx, node, "system")
			switch {
			case err == nil:
				started.Add(1)
			case errors.Is(err, ErrNotReady):
				refused.Add(1)
			default:
				t.Errorf("start: %v", err)
			}
		}(node)
	}
	wg.Wait()
	if started.Load() != 1 || refused.Load() != 2 {
		t.Fatalf("started %d, refused %d", started.Load(), refused.Load())
	}
	// another process of this home (its own orchestrator, the same database)
	other := &Orchestrator{DB: o.DB, Hub: o.Hub, Supervisor: &fakeSupervisor{kit: "x"}, HomeName: "a", SchemaRev: 147}
	if _, err := other.Start(ctx, c, "system"); !errors.Is(err, ErrNotReady) {
		t.Fatalf("a second process started a switch too: %v", err)
	}
}

// The promotion's answer is lost: the old home stays frozen (never thawed) and asks the
// machine until it says it is the home, then goes on.
func TestALostPromotionAnswerKeepsTheHomeFrozen(t *testing.T) {
	o, h, s, b, c := setup(t)
	ctx := context.Background()
	newHome := NewHome{APIURL: "http://127.0.0.1:1", DBHost: "b", DBPort: 54329, SystemID: "7"}
	o.HomeWait = 50 * time.Millisecond
	var asked atomic.Int32
	h.answers["switch.promote"] = func(json.RawMessage) (bool, any) { return false, "the channel closed" }
	h.answers["switch.status"] = func(args json.RawMessage) (bool, any) {
		var a struct {
			Abort bool `json:"abort"`
		}
		_ = json.Unmarshal(args, &a)
		if !a.Abort {
			t.Error("the old home asked without calling a switch not promoted off")
		}
		if asked.Add(1) < 3 {
			return true, map[string]any{"state": "promoting"}
		}
		return true, map[string]any{"state": "home", "home": newHome}
	}
	id, err := o.Start(ctx, b, "system")
	if err != nil {
		t.Fatal(err)
	}
	p := waitState(t, o, id, "handed_over", "failed")
	if p.State != "handed_over" || p.NewHome == nil || p.NewHome.DBHost != "b" {
		t.Fatalf("the switch: %+v", p)
	}
	if got := strings.Join(s.called(), " "); strings.Contains(got, "thaw") || !strings.HasSuffix(got, "demote") {
		t.Fatalf("the supervisor's steps: %v (thawed while the promotion's outcome was not known)", got)
	}
	if got := strings.Join(h.called(), " "); !strings.Contains(got, "home.moved@s-"+c) {
		t.Fatalf("the other machine was not told: %v", got)
	}
}

// The promotion's answer is lost and the machine is gone: the old home reads the notice
// the new home sealed to it, at the new home's address, and goes on.
func TestTheNewHomesNoticeAnswersALostPromotion(t *testing.T) {
	o, h, s, b, _ := setup(t)
	ctx := context.Background()
	o.HomeWait = 50 * time.Millisecond
	h.answers["switch.promote"] = func(json.RawMessage) (bool, any) { return false, "timed out" }
	h.answers["switch.status"] = func(json.RawMessage) (bool, any) { return false, "the machine's agent is not connected" }
	s.mu.Lock()
	s.newHome = &NewHome{APIURL: "http://127.0.0.1:1", DBHost: "b", DBPort: 54329, SystemID: "7"}
	s.mu.Unlock()
	if _, err := o.DB.ExecContext(ctx, `UPDATE cluster_nodes SET web_url = 'http://b.local:9273' WHERE id = $1`, b); err != nil {
		t.Fatal(err)
	}
	id, err := o.Start(ctx, b, "system")
	if err != nil {
		t.Fatal(err)
	}
	p := waitState(t, o, id, "handed_over", "failed")
	if p.State != "handed_over" || len(p.ToURLs) != 1 || p.ToURLs[0] != "http://b.local:9273" {
		t.Fatalf("the switch: %+v", p)
	}
	if got := strings.Join(s.called(), " "); strings.Contains(got, "thaw") || !strings.Contains(got, "new-home") {
		t.Fatalf("the supervisor's steps: %v", got)
	}
}

// The machine taking over says it called the switch off: only then is the old home made
// writable again.
func TestASwitchCalledOffThawsTheHome(t *testing.T) {
	o, h, s, b, _ := setup(t)
	ctx := context.Background()
	h.answers["switch.promote"] = func(json.RawMessage) (bool, any) { return false, "the kit does not open" }
	h.answers["switch.status"] = func(json.RawMessage) (bool, any) { return true, map[string]any{"state": "called_off"} }
	id, err := o.Start(ctx, b, "system")
	if err != nil {
		t.Fatal(err)
	}
	p := waitState(t, o, id, "failed", "handed_over")
	if p.State != "failed" {
		t.Fatalf("the switch: %+v", p)
	}
	if got := strings.Join(s.called(), " "); !strings.HasSuffix(got, "thaw relay:true") {
		t.Fatalf("the supervisor's steps: %v", got)
	}
	var state string
	_ = o.DB.QueryRowContext(ctx, `SELECT state FROM cluster_switchovers WHERE id = $1`, id).Scan(&state)
	if state != "failed" {
		t.Fatalf("the row: %q", state)
	}
	// over: another may start
	if _, err := o.Start(ctx, b, "system"); err != nil {
		t.Fatalf("a switch after one called off: %v", err)
	}
}

// This home started again during a switch: one whose promotion was not asked is undone,
// one whose promotion went through goes on to its end (the machines told, the demotion).
func TestASwitchIsTakenUpAfterARestart(t *testing.T) {
	o, h, s, b, c := setup(t)
	ctx := context.Background()
	record := func(p Progress) {
		raw, _ := json.Marshal(p)
		s.mu.Lock()
		if s.records == nil {
			s.records = map[string]json.RawMessage{}
		}
		s.records[p.ID] = raw
		s.mu.Unlock()
	}
	// frozen, the kit sent, the promotion not asked: undone
	early := Progress{ID: uuid.NewString(), Term: 2, Kind: "planned", ToNode: b, ToName: "b", FromName: "a",
		State: "handing_over", Started: time.Now(), Steps: []Step{{Name: StepKit, State: "running"}},
		Machines: map[string]Machine{b: {Name: "b", PriorDrain: true}, c: {Name: "c"}}}
	if _, err := o.DB.ExecContext(ctx, `INSERT INTO cluster_switchovers (id, term, kind, to_node, to_name, from_name, state)
	    VALUES ($1, 2, 'planned', $2, 'b', 'a', 'handing_over')`, early.ID, b); err != nil {
		t.Fatal(err)
	}
	if _, err := o.DB.ExecContext(ctx, `UPDATE cluster_nodes SET drain = true`); err != nil {
		t.Fatal(err)
	}
	record(early)
	o.Resume(ctx)
	p := waitState(t, o, early.ID, "failed")
	if !strings.Contains(p.Detail, "started again") {
		t.Fatalf("the early switch: %+v", p)
	}
	if got := strings.Join(s.called(), " "); got != "thaw relay:true" {
		t.Fatalf("the supervisor's steps: %v", got)
	}
	var drainB, drainC bool
	_ = o.DB.QueryRowContext(ctx, `SELECT drain FROM cluster_nodes WHERE id = $1`, b).Scan(&drainB)
	_ = o.DB.QueryRowContext(ctx, `SELECT drain FROM cluster_nodes WHERE id = $1`, c).Scan(&drainC)
	if !drainB || drainC {
		t.Fatalf("the drains given back: %v %v", drainB, drainC)
	}

	// promoted (its answer in), the machines not told yet: told, and demoted
	late := Progress{ID: uuid.NewString(), Term: 2, Kind: "planned", ToNode: b, ToName: "b", FromName: "a",
		State: "promoting", Started: time.Now(), PromoteSent: true,
		NewHome:  &NewHome{APIURL: "http://127.0.0.1:1", DBHost: "b", DBPort: 54329, SystemID: "7"},
		Machines: map[string]Machine{b: {Name: "b"}, c: {Name: "c"}}}
	record(late)
	o.HomeWait = 50 * time.Millisecond
	o.Resume(ctx)
	p = waitState(t, o, late.ID, "handed_over", "failed")
	if p.State != "handed_over" {
		t.Fatalf("the late switch: %+v", p)
	}
	if got := strings.Join(h.called(), " "); !strings.HasSuffix(got, "undrain@home-worker home.moved@s-"+c) {
		t.Fatalf("the channel's commands: %v", got)
	}
	if got := strings.Join(s.called(), " "); got != "thaw relay:true demote" {
		t.Fatalf("the supervisor's steps: %v (thawed a promoted home?)", got)
	}
}
