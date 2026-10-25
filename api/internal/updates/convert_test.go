package updates

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/Hamza-Labs-Core/Maktaba/api/internal/updates/dltoken"
)

// fakeWorker plays a worker-only machine's pipeline worker as its agent answers
// update.convert (story 35.8 AC8).
type fakeWorker struct {
	version string // the pipeline's
	old     bool   // a pipeline before update.convert
	fail    bool   // its conversion fails at setup
	never   bool   // its conversion never ends
	refuse  string // a refusal other than its age
	asks    []MachineJob
	polls   int
}

// convertCluster is clusterFake with worker-only machines ("w-<id>"): a conversion that
// succeeds takes the worker off the channel and brings the machine's agent on the
// release it was sent.
type convertCluster struct {
	*clusterFake
	wmu     sync.Mutex
	workers map[string]*fakeWorker
}

func (c *convertCluster) NodeWorkers() map[string]NodeWorker {
	c.wmu.Lock()
	defer c.wmu.Unlock()
	out := map[string]NodeWorker{}
	for id, w := range c.workers {
		out[id] = NodeWorker{Session: "w-" + id, Version: w.version}
	}
	return out
}

func (c *convertCluster) Command(ctx context.Context, sid, name string, args any, wait time.Duration) (bool, json.RawMessage, error) {
	if !strings.HasPrefix(sid, "w-") {
		if name == "update.convert" {
			return false, nil, errors.New("agent: unknown command or bad arguments") // a machine agent never takes it
		}
		return c.clusterFake.Command(ctx, sid, name, args, wait)
	}
	id := strings.TrimPrefix(sid, "w-")
	c.wmu.Lock()
	w, ok := c.workers[id]
	c.wmu.Unlock()
	if !ok {
		return false, nil, errors.New("no session")
	}
	answer := func(ok bool, v any) (bool, json.RawMessage, error) {
		b, _ := json.Marshal(v)
		return ok, b, nil
	}
	switch {
	case name != "update.convert":
		return true, nil, nil // drain, reload_stages
	case w.old:
		return answer(false, "unknown command 'update.convert'")
	case w.refuse != "":
		return answer(false, map[string]string{"state": "failed", "code": "convert_refused", "detail": w.refuse})
	}
	job, _ := args.(MachineJob)
	w.asks = append(w.asks, job)
	if len(w.asks) == 1 {
		return answer(true, map[string]string{"state": "started"})
	}
	w.polls++
	switch {
	case w.never:
		return answer(true, map[string]string{"state": "running", "step": "install"})
	case w.fail:
		return answer(true, map[string]string{"state": "failed", "step": "install", "code": "install_failed",
			"detail": "maktaba-server setup --node exited 1"})
	}
	// setup ran: the old worker units are gone, the new machine agent comes on the release
	c.wmu.Lock()
	delete(c.workers, id)
	c.wmu.Unlock()
	c.add(id, job.Version)
	return answer(true, map[string]string{"state": "running", "step": "install"})
}

func newConvertRig(t *testing.T) (*driverRig, *convertCluster) {
	t.Helper()
	r := newDriverRig(t)
	cc := &convertCluster{clusterFake: r.cluster, workers: map[string]*fakeWorker{}}
	r.d.Cluster = cc
	return r, cc
}

// workerNode adds a worker-only machine: a cluster_nodes row that is not a full node, and
// its pipeline worker on the channel (its version the pipeline's).
func workerNode(t *testing.T, r *driverRig, cc *convertCluster, id, name, version string) *fakeWorker {
	t.Helper()
	if _, err := r.f.db.Exec(`INSERT INTO cluster_nodes (id, name, state, platform, version, full_node) VALUES ($1, $2, 'approved', 'linux-amd64', $3, 0)`,
		id, name, version); err != nil {
		t.Fatal(err)
	}
	w := &fakeWorker{version: version}
	cc.workers[id] = w
	return w
}

// A worker-only machine is converted on its turn, after the full nodes, one at a time:
// drained, sent update.convert (the run's job), healthy once its new agent is on the
// release. Its pipeline being on the release already changes nothing: it converts.
func TestConvertWorkerOnlyOnItsTurn(t *testing.T) {
	r, cc := newConvertRig(t)
	ctx := context.Background()
	r.node(t, "n1", "alpha", "linux-amd64", "1.4.0")
	w := workerNode(t, r, cc, "w1", "studio", "1.5.0")
	drainedAtConvert := false
	_, _ = r.f.svc.Tick(ctx)
	for i := 0; i < 80; i++ {
		_ = r.d.Step(ctx)
		r.f.now = r.f.now.Add(time.Minute)
		if len(w.asks) == 1 && !drainedAtConvert {
			var drain bool
			_ = r.f.db.QueryRow(`SELECT drain FROM cluster_nodes WHERE id = 'w1'`).Scan(&drain)
			drainedAtConvert = drain
		}
		if a, _ := r.f.store.ActiveRun(ctx); a == nil {
			break
		}
	}
	runs, _ := r.f.store.Runs(ctx, 1)
	run := runs[0]
	ts := targetsByName(t, r.f.store, run.ID)
	if run.State != RunDone || ts["studio"].State != TargetHealthy || ts["alpha"].State != TargetHealthy {
		t.Fatalf("run %s %s targets %+v", run.State, run.Error, ts)
	}
	if ts["studio"].Kind != "layout" || ts["studio"].Attempts != 1 {
		t.Fatalf("converted target %+v", ts["studio"])
	}
	if len(w.asks) < 2 || w.asks[0].RunID != run.ID || w.asks[0].Version != "1.5.0" || w.asks[0].Tag != "v1.5.0" {
		t.Fatalf("update.convert asks %+v", w.asks)
	}
	if !drainedAtConvert {
		t.Fatal("the worker was not drained before its conversion")
	}
	var drained int
	_ = r.f.db.QueryRow(`SELECT count(*) FROM cluster_nodes WHERE drain = 1`).Scan(&drained)
	if drained != 0 {
		t.Fatalf("drain left on %d machines", drained)
	}
	// one machine at a time: the full node's apply and the conversion never overlap
	a, s := ts["alpha"], ts["studio"]
	if a.AppliedAt == nil || a.HealthyAt == nil || s.AppliedAt == nil || s.HealthyAt == nil ||
		(a.AppliedAt.Before(*s.HealthyAt) && s.AppliedAt.Before(*a.HealthyAt)) {
		t.Fatalf("the apply and the conversion overlapped: alpha %v-%v, studio %v-%v", a.AppliedAt, a.HealthyAt, s.AppliedAt, s.HealthyAt)
	}
}

// A worker whose pipeline predates update.convert is worker_only ("re-run the installer
// once"): the run is done, nothing failed; later runs do not ask it again while its
// pipeline is the same; an installer run (a newer pipeline) makes it convertible again.
func TestConvertTooOldWorker(t *testing.T) {
	r, cc := newConvertRig(t)
	ctx := context.Background()
	w := workerNode(t, r, cc, "w1", "studio", "1.3.0")
	w.old = true
	_, _ = r.f.svc.Tick(ctx)
	run := r.drive(t, 40)
	ts := targetsByName(t, r.f.store, run.ID)
	if run.State != RunDone || ts["studio"].State != TargetWorkerOnly {
		t.Fatalf("run %s targets %+v", run.State, ts)
	}
	var det struct {
		Why     string `json:"why"`
		Version string `json:"pipeline_version"`
	}
	_ = json.Unmarshal(ts["studio"].Detail, &det)
	if det.Why != WhyTooOld || det.Version != "1.3.0" {
		t.Fatalf("worker_only detail %s", ts["studio"].Detail)
	}
	if got := r.d.WorkerConvert(ctx)["w1"]; got != ConvertInstaller {
		t.Fatalf("WorkerConvert %q", got)
	}
	ms := r.d.Machines(ctx, Machine{ID: "home", Role: "home", Version: "1.5.0"})
	if len(ms) != 2 || ms[1].State != "worker_only" || ms[1].Convert != ConvertInstaller {
		t.Fatalf("machines %+v", ms)
	}
	// the same pipeline: no catch-up asks it again
	asked := len(w.asks)
	_ = r.d.Step(ctx)
	if a, _ := r.f.store.ActiveRun(ctx); a != nil {
		t.Fatalf("a catch-up for a worker known too old: %+v", a)
	}
	// its installer ran (a pipeline with update.convert): the next catch-up converts it
	cc.wmu.Lock()
	w.version, w.old = "1.5.0", false
	cc.wmu.Unlock()
	if got := r.d.WorkerConvert(ctx)["w1"]; got != ConvertNextUpdate {
		t.Fatalf("WorkerConvert after the installer %q", got)
	}
	_ = r.d.Step(ctx) // plans the catch-up
	cu := r.drive(t, 40)
	if cu.Trigger != TriggerCatchUp || cu.State != RunDone || len(w.asks) <= asked {
		t.Fatalf("catch-up %+v asks %d", cu, len(w.asks))
	}
	if ts := targetsByName(t, r.f.store, cu.ID); ts["studio"].State != TargetHealthy {
		t.Fatalf("catch-up targets %+v", ts)
	}
}

// A conversion that fails (its setup exits non-zero) fails its target with the worker's
// reason; the machine stays drained and is not caught up again for this release.
func TestConvertFailure(t *testing.T) {
	r, cc := newConvertRig(t)
	ctx := context.Background()
	w := workerNode(t, r, cc, "w1", "studio", "1.4.0")
	w.fail = true
	_, _ = r.f.svc.Tick(ctx)
	run := r.drive(t, 40)
	ts := targetsByName(t, r.f.store, run.ID)
	if run.State != RunDoneFailures || ts["studio"].State != TargetFailed {
		t.Fatalf("run %s targets %+v", run.State, ts)
	}
	e := readErr(ts["studio"].Error)
	if e.Code != "install_failed" || e.Step != "install" || !strings.Contains(e.Detail, "setup --node") {
		t.Fatalf("error %s", ts["studio"].Error)
	}
	a := targetAttempt(run, ts["studio"])
	if a.Outcome != OutcomeFailed || a.Code != "install_failed" {
		t.Fatalf("attempt %+v", a)
	}
	var drain bool
	_ = r.f.db.QueryRow(`SELECT drain FROM cluster_nodes WHERE id = 'w1'`).Scan(&drain)
	if !drain {
		t.Fatal("a failed conversion put the machine back to work")
	}
	_ = r.d.Step(ctx)
	if a, _ := r.f.store.ActiveRun(ctx); a != nil {
		t.Fatalf("a failed conversion caught up again: %+v", a)
	}
}

// A refusal for another reason than its age fails the target (convert_refused).
func TestConvertRefused(t *testing.T) {
	r, cc := newConvertRig(t)
	ctx := context.Background()
	w := workerNode(t, r, cc, "w1", "studio", "1.4.0")
	w.refuse = "a conversion of another run is under way"
	_, _ = r.f.svc.Tick(ctx)
	run := r.drive(t, 40)
	ts := targetsByName(t, r.f.store, run.ID)
	if e := readErr(ts["studio"].Error); ts["studio"].State != TargetFailed || e.Code != CodeConvertRefused ||
		!strings.Contains(e.Detail, "another run") {
		t.Fatalf("target %+v", ts["studio"])
	}
}

// A conversion that never brings the machine's agent on the release fails after
// convertTimeout (convert_timeout).
func TestConvertTimeout(t *testing.T) {
	r, cc := newConvertRig(t)
	ctx := context.Background()
	w := workerNode(t, r, cc, "w1", "studio", "1.4.0")
	w.never = true
	_, _ = r.f.svc.Tick(ctx)
	run := r.drive(t, 200)
	ts := targetsByName(t, r.f.store, run.ID)
	if e := readErr(ts["studio"].Error); ts["studio"].State != TargetFailed || e.Code != CodeConvertTimeout {
		t.Fatalf("run %s target %+v", run.State, ts["studio"])
	}
}

// A worker-only machine offline when the run begins is converted once its worker is back
// (inside the window), within the same run.
func TestConvertWorkerBackDuringTheRun(t *testing.T) {
	r, cc := newConvertRig(t)
	ctx := context.Background()
	w := workerNode(t, r, cc, "w1", "studio", "1.4.0")
	cc.wmu.Lock()
	delete(cc.workers, "w1")
	cc.wmu.Unlock()
	r.node(t, "n1", "alpha", "linux-amd64", "1.4.0")
	r.cluster.machines["n1"].onApply = func() {
		cc.wmu.Lock()
		cc.workers["w1"] = w // back while the full node applies
		cc.wmu.Unlock()
	}
	_, _ = r.f.svc.Tick(ctx)
	run := r.drive(t, 80)
	if ts := targetsByName(t, r.f.store, run.ID); run.State != RunDone || ts["studio"].State != TargetHealthy {
		t.Fatalf("run %s targets %+v", run.State, ts)
	}
}

// The conversion's job carries a download token for the home's mirror, for that machine
// and that release (it has no delegation key yet).
func TestConvertJobCarriesADownloadToken(t *testing.T) {
	r, cc := newConvertRig(t)
	ctx := context.Background()
	r.d.Mirror = &Mirror{Store: r.f.store, Dir: t.TempDir()}
	w := workerNode(t, r, cc, "w1", "studio", "1.4.0")
	_, _ = r.f.svc.Tick(ctx)
	r.drive(t, 40)
	if len(w.asks) == 0 || w.asks[0].DownloadToken == "" {
		t.Fatalf("asks %+v", w.asks)
	}
	key, err := r.d.Mirror.key()
	if err != nil {
		t.Fatal(err)
	}
	c, err := dltoken.Verify(key, w.asks[0].DownloadToken, time.Now())
	if err != nil || c.Node != "w1" || c.Version != "1.5.0" {
		t.Fatalf("token %+v %v", c, err)
	}
}
