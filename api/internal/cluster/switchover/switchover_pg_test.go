package switchover

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/Hamza-Labs-Core/Maktaba/api/internal/cluster/agent"
	"github.com/Hamza-Labs-Core/Maktaba/api/internal/cluster/clustertest"
)

// fakeHub is the cluster channel: every machine's agent connected, answering as told.
type fakeHub struct {
	mu      sync.Mutex
	calls   []string
	answers map[string]func(args json.RawMessage) (bool, any)
}

func (h *fakeHub) MachineAgent(node string) string { return "s-" + node }
func (h *fakeHub) HomeProcesses() []string        { return []string{"home-worker"} }
func (h *fakeHub) CommandWithin(_ context.Context, sid, name string, args json.RawMessage, _ string, _ time.Duration) (agent.CmdResult, error) {
	h.mu.Lock()
	h.calls = append(h.calls, name+"@"+sid)
	fn := h.answers[name]
	h.mu.Unlock()
	ok, detail := true, any(nil)
	if fn != nil {
		ok, detail = fn(args)
	}
	b, _ := json.Marshal(detail)
	return agent.CmdResult{OK: ok, Detail: b}, nil
}

func (h *fakeHub) called() []string {
	h.mu.Lock()
	defer h.mu.Unlock()
	return append([]string(nil), h.calls...)
}

// fakeSupervisor is this home's supervisor: it records the steps and freezes nothing.
type fakeSupervisor struct {
	mu    sync.Mutex
	steps []string
	kit   string
	fail  string
}

func (s *fakeSupervisor) Call(_ context.Context, step string, _, out any) error {
	s.mu.Lock()
	s.steps = append(s.steps, step)
	s.mu.Unlock()
	if step == s.fail {
		return errors.New("refused")
	}
	if step == "kit" && out != nil {
		b, _ := json.Marshal(map[string]any{"path": s.kit, "chunks": 1})
		return json.Unmarshal(b, out)
	}
	return nil
}

func (s *fakeSupervisor) called() []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]string(nil), s.steps...)
}

func waitState(t *testing.T, o *Orchestrator, id string, want ...string) *Progress {
	t.Helper()
	end := time.Now().Add(30 * time.Second)
	for time.Now().Before(end) {
		if p := o.Progress(id); p != nil {
			for _, w := range want {
				if p.State == w {
					return p
				}
			}
		}
		time.Sleep(50 * time.Millisecond)
	}
	t.Fatalf("switch %s never reached %v: %+v", id, want, o.Progress(id))
	return nil
}

func setup(t *testing.T) (*Orchestrator, *fakeHub, *fakeSupervisor, string, string) {
	t.Helper()
	db, _ := clustertest.ScratchDB(t)
	ctx := context.Background()
	b, c := uuid.NewString(), uuid.NewString()
	for _, n := range []struct{ id, name string }{{b, "b"}, {c, "c"}} {
		if _, err := db.ExecContext(ctx, `INSERT INTO cluster_nodes (id, name, state, os, arch, node_pubkey, approved_at,
		        approved_by, full_node, replica, replica_reported_at)
		    VALUES ($1, $2, 'approved', 'windows', 'amd64', 'cHVi', now(), 'test', true,
		        '{"state": "following", "local_schema": 144}'::jsonb, now())`, n.id, n.name); err != nil {
			t.Fatal(err)
		}
	}
	kit := filepath.Join(t.TempDir(), "kit.chunks")
	// one sealed chunk's record: index 0, last, 3 bytes
	if err := os.WriteFile(kit, []byte{0, 0, 0, 0, 0, 0, 0, 0, 1, 0, 0, 0, 3, 'a', 'b', 'c'}, 0o600); err != nil {
		t.Fatal(err)
	}
	h := &fakeHub{answers: map[string]func(json.RawMessage) (bool, any){
		"switch.prepare": func(json.RawMessage) (bool, any) { return true, map[string]any{"marker": true} },
		"switch.promote": func(json.RawMessage) (bool, any) {
			return true, NewHome{APIURL: "http://b:8080", DBHost: "b", DBPort: 54329, SystemID: "7"}
		},
		"home.moved": func(json.RawMessage) (bool, any) { return true, map[string]any{"copy_kept": true} },
	}}
	s := &fakeSupervisor{kit: kit}
	o := &Orchestrator{DB: db, Hub: h, Supervisor: s, HomeName: "a", SchemaRev: 144, JobsWait: time.Second}
	return o, h, s, b, c
}

func TestAPlannedSwitchRunsItsStepsInOrder(t *testing.T) {
	o, h, s, b, c := setup(t)
	ctx := context.Background()
	cs, err := o.Candidates(ctx)
	if err != nil || len(cs) != 2 || !cs[0].Ready || !cs[1].Ready {
		t.Fatalf("candidates: %+v %v", cs, err)
	}
	id, err := o.Start(ctx, b, "user:"+uuid.NewString())
	if err != nil {
		t.Fatal(err)
	}
	p := waitState(t, o, id, "handed_over", "failed")
	if p.State != "handed_over" || p.NewHome == nil || p.NewHome.APIURL != "http://b:8080" {
		t.Fatalf("the switch: %+v", p)
	}
	if got := s.called(); len(got) != 4 || got[0] != "check" || got[1] != "freeze" || got[2] != "kit" || got[3] != "demote" {
		t.Fatalf("the supervisor's steps: %v", got)
	}
	want := []string{"drain@home-worker", "switch.prepare@s-" + b, "switch.kit@s-" + b, "switch.promote@s-" + b,
		"home.moved@s-" + c}
	got := h.called()
	if len(got) != len(want) {
		t.Fatalf("the channel's commands: %v", got)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("the channel's commands: %v, want %v", got, want)
		}
	}
	if m := p.Machines[c]; m.State != "following" {
		t.Fatalf("the other machine: %+v", m)
	}
	// the machines' jobs held for the switch; the row is there for the new home
	var drained int
	_ = o.DB.QueryRowContext(ctx, `SELECT count(*) FROM cluster_nodes WHERE drain`).Scan(&drained)
	var state string
	_ = o.DB.QueryRowContext(ctx, `SELECT state FROM cluster_switchovers WHERE id = $1`, id).Scan(&state)
	if drained != 2 || state != "freezing" {
		t.Fatalf("drained %d, the row %q", drained, state)
	}
	// a second switch waits for this one... it is over here: a new one may start
	if cs, _ := o.Candidates(ctx); len(cs) == 0 {
		t.Fatal("no candidates after the hand-over")
	}
}

// A copy without the marker refuses the switch, and everything is as it was: the
// database thawed, the machines' jobs back, the row failed.
func TestASwitchWithoutTheMarkerIsUndone(t *testing.T) {
	o, h, s, b, _ := setup(t)
	ctx := context.Background()
	if _, err := o.DB.ExecContext(ctx, `UPDATE cluster_nodes SET drain = true WHERE id = $1`, b); err != nil {
		t.Fatal(err)
	}
	h.answers["switch.prepare"] = func(json.RawMessage) (bool, any) { return true, map[string]any{"marker": false} }
	id, err := o.Start(ctx, b, "system")
	if err != nil {
		t.Fatal(err)
	}
	p := waitState(t, o, id, "failed", "handed_over")
	if p.State != "failed" {
		t.Fatalf("the switch: %+v", p)
	}
	got := s.called()
	if got[len(got)-1] != "thaw" {
		t.Fatalf("the supervisor's steps: %v (the database left frozen)", got)
	}
	var drainB, drainOther bool
	_ = o.DB.QueryRowContext(ctx, `SELECT drain FROM cluster_nodes WHERE id = $1`, b).Scan(&drainB)
	_ = o.DB.QueryRowContext(ctx, `SELECT bool_or(drain) FROM cluster_nodes WHERE id <> $1`, b).Scan(&drainOther)
	if !drainB || drainOther {
		t.Fatalf("drains after the undo: %v (it was drained before) %v", drainB, drainOther)
	}
	var state string
	_ = o.DB.QueryRowContext(ctx, `SELECT state FROM cluster_switchovers WHERE id = $1`, id).Scan(&state)
	if state != "failed" {
		t.Fatalf("the row: %q", state)
	}
	calls := h.called()
	if calls[len(calls)-1] != "undrain@home-worker" {
		t.Fatalf("the home's jobs not given back: %v", calls)
	}
}

func TestAMachineThatCannotTakeOverIsRefused(t *testing.T) {
	o, _, _, b, c := setup(t)
	ctx := context.Background()
	if _, err := o.DB.ExecContext(ctx, `UPDATE cluster_nodes SET replica = '{"state": "copying", "local_schema": 143}'::jsonb
	    WHERE id = $1`, c); err != nil {
		t.Fatal(err)
	}
	cs, _ := o.Candidates(ctx)
	for _, x := range cs {
		if x.NodeID == c && (x.Ready || len(x.Reasons) != 2) {
			t.Fatalf("a machine copying, at another schema: %+v", x)
		}
	}
	if _, err := o.Start(ctx, c, "system"); !errors.Is(err, ErrNotReady) {
		t.Fatalf("started a switch to it: %v", err)
	}
	_ = b
}
