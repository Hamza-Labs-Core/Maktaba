package systemstatus

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/Hamza-Labs-Core/Maktaba/api/internal/handlers/queues"
	"github.com/Hamza-Labs-Core/Maktaba/api/internal/hostload"
)

func hostLoad(st hostload.State, ok bool) func(context.Context) (hostload.State, bool) {
	return func(context.Context) (hostload.State, bool) { return st, ok }
}

func TestHostLoad(t *testing.T) {
	ctx := context.Background()
	run := func(st hostload.State, ok bool) []Item {
		return checkHostLoad(ctx, newEnv(&Handler{HostLoad: hostLoad(st, ok)}))
	}
	calm := only(t, run(hostload.State{Load1: 3.14, Load5: 2.5, Cores: 28, Bound: 56}, true))
	want(t, calm, StatusOK, "host_load_ok")
	if calm.Kind != KindHostLoad || calm.Group != GroupServices || calm.Params["load1"] != 3.1 || calm.Params["cores"] != 28 {
		t.Fatalf("%+v", calm)
	}

	over := only(t, run(hostload.State{Overloaded: true, Load1: 512.3, Load5: 498, Cores: 28, Bound: 56,
		Top: []hostload.Proc{{PID: 9, Name: "runaway", CPUPct: 795.4}, {PID: 3, Name: "Some App", CPUPct: 398}}}, true))
	want(t, over, StatusDegraded, "host_overloaded_top")
	if over.Params["top"] != "runaway (795%), Some App (398%)" || !strings.Contains(over.Reason, "runaway (795%)") ||
		over.Critical {
		t.Fatalf("%+v", over)
	}

	// the programs could not be listed: still said, without them
	want(t, only(t, run(hostload.State{Overloaded: true, Load1: 99, Load5: 80, Cores: 4, Bound: 8, Top: []hostload.Proc{}}, true)),
		StatusDegraded, "host_overloaded")

	// no load average on this machine (Windows), or no reader: no item
	if items := run(hostload.State{}, false); len(items) != 0 {
		t.Fatalf("an item with no reading: %+v", items)
	}
	if items := checkHostLoad(ctx, newEnv(&Handler{})); len(items) != 0 {
		t.Fatalf("an item with no reader: %+v", items)
	}
}

// The round has the item only when the load has a reader.
func TestHostLoad_InTheRound(t *testing.T) {
	has := func(h *Handler) bool {
		for _, c := range h.checks() {
			if c.Name == "host_load" {
				return c.Fallback.Kind == KindHostLoad && c.Fallback.Help == KindHostLoad
			}
		}
		return false
	}
	if has(&Handler{}) || !has(&Handler{HostLoad: hostLoad(hostload.State{}, true)}) {
		t.Fatal("the host_load check follows Handler.HostLoad")
	}
}

// fakeWorkersQueues is the queues with a workers answer.
type fakeWorkersQueues struct {
	fakeQueues
	ws  queues.WorkersState
	err error
}

func (f fakeWorkersQueues) WorkersNow(context.Context) (queues.WorkersState, error) {
	return f.ws, f.err
}

func TestQueues_StalledWorkers(t *testing.T) {
	ctx := context.Background()
	run := func(ws queues.WorkersState, err error, r queues.HealthRollup) Item {
		q := fakeWorkersQueues{fakeQueues: fakeQueues{r: r}, ws: ws, err: err}
		return only(t, checkQueues(ctx, newEnv(&Handler{Queues: q})))
	}
	stalled := queues.WorkersState{State: queues.WorkersNotResponding, Waiting: 40, Stalled: []queues.StalledWorker{
		{WorkerID: "home/2/stt", Kind: queues.WorkerTranscription, Since: time.Now()},
		{WorkerID: "home/3/stt", Kind: queues.WorkerTranscription, Since: time.Now()},
		{WorkerID: "home/1/main", Kind: queues.WorkerMain, Since: time.Now()},
	}}
	// stalled workers come before a failing queue: they are why nothing moves
	it := run(stalled, nil, queues.HealthRollup{Stages: 20, Failing: []string{"detect"}})
	want(t, it, StatusDegraded, "queues_workers_stalled")
	if it.Link != "/queue" || it.Params["workers"] != 3 || it.Params["waiting"] != 40 ||
		it.Params["kinds"] != "transcription, main" {
		t.Fatalf("%+v", it)
	}
	none := run(queues.WorkersState{State: queues.WorkersNotResponding, Waiting: 6}, nil, queues.HealthRollup{Stages: 20})
	want(t, none, StatusDegraded, "queues_no_workers")

	// the workers run, or their state cannot be read: what the queues say, as before
	want(t, run(queues.WorkersState{State: queues.WorkersRunning}, nil, queues.HealthRollup{Stages: 20}), StatusOK, "queues_ok")
	want(t, run(queues.WorkersState{}, errors.New("boom"), queues.HealthRollup{Stages: 20, Waiting: []string{"tag"}}),
		StatusDegraded, "queues_waiting")
}
