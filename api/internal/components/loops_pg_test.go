package components

import (
	"bytes"
	"context"
	"log/slog"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Hamza-Labs-Core/Maktaba/api/internal/cluster"
	"github.com/Hamza-Labs-Core/Maktaba/api/internal/cluster/clustertest"
	comps "github.com/Hamza-Labs-Core/Maktaba/shared/components"
	"github.com/Hamza-Labs-Core/Maktaba/shared/components/store"
)

// keptLog is the runner's log, shown when the test fails.
func keptLog(t *testing.T) *slog.Logger {
	t.Helper()
	var mu sync.Mutex
	var buf bytes.Buffer
	t.Cleanup(func() {
		if t.Failed() {
			mu.Lock()
			t.Log("runner log:\n" + buf.String())
			mu.Unlock()
		}
	})
	return slog.New(slog.NewTextHandler(writerFunc(func(p []byte) (int, error) {
		mu.Lock()
		defer mu.Unlock()
		return buf.Write(p)
	}), &slog.HandlerOptions{Level: slog.LevelDebug}))
}

type writerFunc func([]byte) (int, error)

func (f writerFunc) Write(p []byte) (int, error) { return f(p) }

// Story 51.05 on a joined machine: its API's runner follows that machine's desired state
// with its own login: an override that turns livetv off there stops the loop on that
// machine only (the home's keeps running), and each reports its own status row.
func TestAPIRunnerOnAJoinedMachineFollowsItsOverride(t *testing.T) {
	db, dsn := clustertest.ScratchDB(t)
	t.Setenv("MAKTABA_COMPONENTS_RECONCILE", "")
	n := joinNode(t, db, dsn, "a")
	var homeOn, nodeOn atomic.Int32
	loop := func(on *atomic.Int32) func(ctx context.Context) error {
		return func(ctx context.Context) error {
			on.Add(1)
			<-ctx.Done()
			on.Add(-1)
			return nil
		}
	}
	home := NewRunner(keptLog(t))
	home.Declare("tvchange_controller", loop(&homeOn))
	Start(home, db, func() string { return dsn }, keptLog(t))
	node := NewRunnerFor(keptLog(t), n.id)
	if node.Machine() != n.id {
		t.Fatalf("the node's runner follows %q", node.Machine())
	}
	node.Declare("tvchange_controller", loop(&nodeOn))
	nodeDSN := clustertest.WithUser(t, dsn, cluster.RoleName(n.id), "node-pass-a")
	Start(node, n.db, func() string { return nodeDSN }, keptLog(t))
	st := New(db)
	phase := func(machine string) string {
		rows, err := st.StatusOf(context.Background(), "livetv")
		if err != nil {
			t.Fatal(err)
		}
		for _, s := range rows {
			if s.Machine == machine && s.Part == Host {
				return s.Phase
			}
		}
		return ""
	}
	within := func(d time.Duration, what string, cond func() bool) {
		t.Helper()
		deadline := time.Now().Add(d)
		for !cond() {
			if time.Now().After(deadline) {
				t.Fatalf("%s: not within %s", what, d)
			}
			time.Sleep(20 * time.Millisecond)
		}
	}
	within(5*time.Second, "both running", func() bool {
		return homeOn.Load() == 1 && nodeOn.Load() == 1 && phase(store.Home) == comps.PhaseRunning &&
			phase(n.id) == comps.PhaseRunning
	})
	if err := st.SetOverride(context.Background(), store.Override{Node: n.id, ID: "livetv", Want: comps.WantInstalled},
		store.Audit{By: "admin", ActorSource: "admin", Action: "component.disable"}); err != nil {
		t.Fatal(err)
	}
	within(5*time.Second, "the node's loop stopped by its override", func() bool {
		return nodeOn.Load() == 0 && phase(n.id) == comps.PhaseOff
	})
	time.Sleep(300 * time.Millisecond)
	if homeOn.Load() != 1 || phase(store.Home) != comps.PhaseRunning || !home.On("livetv") {
		t.Fatalf("the home's loop followed another machine's override: %d %s", homeOn.Load(), phase(store.Home))
	}
}

// Story 51.05 on a real Postgres: livetv set to installed cancels the tvchange
// controller's context within 2 s (the NOTIFY), the status row (part api) goes Off;
// enabled again, it runs within 5 s (N2). The process is not restarted.
func TestAPIRunnerFollowsTheHome(t *testing.T) {
	db, dsn := clustertest.ScratchDB(t)
	t.Setenv("MAKTABA_COMPONENTS_RECONCILE", "")
	r := NewRunner(keptLog(t))
	var starts, stops atomic.Int32
	r.Declare("tvchange_controller", func(ctx context.Context) error {
		starts.Add(1)
		<-ctx.Done()
		stops.Add(1)
		return nil
	})
	Start(r, db, func() string { return dsn }, keptLog(t))
	st := New(db)
	phase := func() string {
		rows, err := st.StatusOf(context.Background(), "livetv")
		if err != nil {
			t.Fatal(err)
		}
		for _, s := range rows {
			if s.Machine == store.Home && s.Part == Host {
				return s.Phase
			}
		}
		return ""
	}
	within := func(d time.Duration, what string, cond func() bool) {
		t.Helper()
		deadline := time.Now().Add(d)
		for !cond() {
			if time.Now().After(deadline) {
				t.Fatalf("%s: not within %s", what, d)
			}
			time.Sleep(20 * time.Millisecond)
		}
	}
	within(5*time.Second, "running reported", func() bool { return phase() == comps.PhaseRunning })

	audit := store.Audit{By: "admin", ActorSource: "admin", Action: "component.disable"}
	if _, err := st.SetDesired(context.Background(), store.Desired{ID: "livetv", Want: comps.WantInstalled}, audit); err != nil {
		t.Fatal(err)
	}
	within(2*time.Second, "the controller's context cancelled", func() bool { return stops.Load() == 1 })
	within(5*time.Second, "Off reported", func() bool { return phase() == comps.PhaseOff })
	if r.On("livetv") {
		t.Fatal("livetv still on")
	}

	audit.Action = "component.enable"
	if _, err := st.SetDesired(context.Background(), store.Desired{ID: "livetv", Want: comps.WantEnabled}, audit); err != nil {
		t.Fatal(err)
	}
	within(5*time.Second, "running again", func() bool { return starts.Load() == 2 && phase() == comps.PhaseRunning })
}
