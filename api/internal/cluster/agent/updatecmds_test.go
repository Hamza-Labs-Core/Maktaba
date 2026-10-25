package agent

import (
	"context"
	"encoding/json"
	"testing"
	"time"
)

// An update's commands reach a machine's own agent (Epic 35 story 35.12 AC2) and its
// update.state is the driver's to read; any other session is refused them.
func TestUpdateCommandsReachOnlyAMachineAgent(t *testing.T) {
	f := newFixture(t)
	id, priv := f.approvedNode("studio")
	a, err := f.dialAgent(helloOpts{kind: "node", nodeID: id, nodePriv: priv, process: "supervisor"})
	if err != nil {
		t.Fatal(err)
	}
	a.send(map[string]any{"t": "update.state", "version": "1.4.0", "platform": "linux-amd64", "layout": "runtime",
		"run": map[string]any{"run_id": "r1", "state": "prepared"}})
	deadline := time.Now().Add(5 * time.Second)
	for {
		if st, ok := f.hub.MachineStates()[id]; ok {
			var s struct {
				Version string `json:"version"`
				Layout  string `json:"layout"`
			}
			if json.Unmarshal(st, &s) != nil || s.Version != "1.4.0" || s.Layout != "runtime" {
				t.Fatalf("machine state %s", st)
			}
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("no machine state")
		}
		time.Sleep(10 * time.Millisecond)
	}
	for _, name := range []string{"update.prepare", "update.apply", "update.status", "update.abort", "update.rollback"} {
		done := make(chan error, 1)
		var res CmdResult
		go func() {
			var err error
			res, err = f.hub.CommandWithin(context.Background(), a.Session, name, json.RawMessage(`{"run_id":"r1"}`),
				"updates", 5*time.Second)
			done <- err
		}()
		cmd := a.recvType("cmd")
		if cmd["name"] != name {
			t.Fatalf("%s: the machine got %v", name, cmd)
		}
		a.send(map[string]any{"t": "cmd.result", "id": cmd["id"], "ok": true, "detail": map[string]any{"status": nil}})
		if err := <-done; err != nil || !res.OK {
			t.Fatalf("%s: %+v %v", name, res, err)
		}
	}

	w, err := f.dialAgent(helloOpts{kind: "node", nodeID: id, nodePriv: priv, process: "pipeline"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.hub.CommandWithin(context.Background(), w.Session, "update.apply", nil, "updates", time.Second); err != ErrBadCommand {
		t.Fatalf("update.apply to a pipeline session: %v", err)
	}
}

// A worker-only machine's conversion (story 35.8 AC8) goes to its pipeline worker, the
// lowest worker id of the machine's; a machine agent or a home process is refused it.
func TestUpdateConvertReachesOnlyAWorker(t *testing.T) {
	f := newFixture(t)
	id, priv := f.approvedNode("studio")
	second, err := f.dialAgent(helloOpts{kind: "node", nodeID: id, nodePriv: priv, process: "pipeline", workerID: "studio-2"})
	if err != nil {
		t.Fatal(err)
	}
	w, err := f.dialAgent(helloOpts{kind: "node", nodeID: id, nodePriv: priv, process: "pipeline", workerID: "studio-1"})
	if err != nil {
		t.Fatal(err)
	}
	if got := f.hub.NodeWorkers()[id]; got.Session != w.Session || got.WorkerID != "studio-1" {
		t.Fatalf("NodeWorkers %+v (want %s, not %s)", got, w.Session, second.Session)
	}
	done := make(chan error, 1)
	var res CmdResult
	go func() {
		var err error
		res, err = f.hub.CommandWithin(context.Background(), w.Session, "update.convert",
			json.RawMessage(`{"run_id":"r1","version":"1.5.0"}`), "updates", 5*time.Second)
		done <- err
	}()
	cmd := w.recvType("cmd")
	if cmd["name"] != "update.convert" {
		t.Fatalf("the worker got %v", cmd)
	}
	w.send(map[string]any{"t": "cmd.result", "id": cmd["id"], "ok": true, "detail": map[string]any{"state": "started"}})
	if err := <-done; err != nil || !res.OK || string(res.Detail) != `{"state":"started"}` {
		t.Fatalf("update.convert: %+v %v", res, err)
	}

	a, err := f.dialAgent(helloOpts{kind: "node", nodeID: id, nodePriv: priv, process: "supervisor"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.hub.CommandWithin(context.Background(), a.Session, "update.convert", nil, "updates", time.Second); err != ErrBadCommand {
		t.Fatalf("update.convert to a machine agent: %v", err)
	}
	if _, ok := f.hub.NodeWorkers()[id]; !ok {
		t.Fatal("the machine agent took the worker's place")
	}
}
